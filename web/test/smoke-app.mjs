// Task 4 smoke: app detail tabs through URLs, env edit + banner, secrets masked then revealed,
// live log lines, rollback creates a new deployment, delete needs the exact name, domains,
// terminal drawer, and text from the server is never interpreted as markup.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, buildStaticBinary, login, watch, check } from "./lib.mjs";

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-app-"));
const srv = await startRunova();
const browser = await chromium.launch();
const ctxt = await browser.newContext({ viewport: { width: 1440, height: 900 }, permissions: ["clipboard-read", "clipboard-write"] });
const page = await ctxt.newPage();
const problems = watch(page, srv.url);
const step = (m) => console.log(`- ${m}`);

async function api(method, p, body, cookie) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null };
}

try {
  const loginRes = await fetch(`${srv.url}/api/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: "admin", password: srv.adminPassword }) });
  const cookie = loginRes.headers.get("set-cookie").split(";")[0];

  // Create the app through the API (the wizard is covered by smoke-deploy).
  const jar = buildSampleJar(tmp);
  const form = new FormData();
  form.append("file", new Blob([fs.readFileSync(jar)]), "billing-api-2.4.1.jar");
  const up = await (await fetch(`${srv.url}/api/uploads`, { method: "POST", headers: { cookie }, body: form })).json();
  const created = await api("POST", "/api/apps", {
    upload_id: up.id, name: "billing-api", java_version: 21, memory_mb: 512, cpu_milli: 500, ip_access: true,
    env: [{ name: "JWT_SECRET", value: "super-secret-1", secret: true }, { name: "DB_PASSWORD", value: "pw", secret: true }, { name: "GREETING", value: "<script>alert(1)</script>", secret: false }, { name: "LOG_ECHO", value: "hello from <b>env</b>", secret: false }],
  }, cookie);
  check(created.status === 201, `create ${created.status} ${JSON.stringify(created.body)}`);
  const id = created.body.app.id;

  await login(page, srv.url, "admin", srv.adminPassword);

  step("tabs follow the URL");
  await page.goto(`${srv.url}/#/apps/${id}/env`);
  await page.waitForSelector("#envlist .envrow");
  check((await page.locator(".tabs a[aria-selected=true]").innerText()) === "Environment", "env tab not selected");
  for (const [slug, label] of [["domains", "Domain"], ["deployments", "Deployment"], ["logs", "Log"], ["monitor", "Monitoring"], ["advanced", "Lanjutan"], ["", "Umum"]]) {
    await page.goto(`${srv.url}/#/apps/${id}${slug ? `/${slug}` : ""}`);
    await page.waitForFunction((l) => document.querySelector(".tabs a[aria-selected=true]")?.textContent === l, label);
  }

  step("app is running; header shows the version and the IP link");
  await page.waitForSelector(".ph .chip:has-text('Berjalan')", { timeout: 20000 });
  check((await page.locator(".dhead p").innerText()).includes("v1 · billing-api-2.4.1.jar"), "header version line");
  await page.waitForSelector(".link:has-text(':200')");

  step("env: secrets masked, markup shown as text, Lihat reveals through the API");
  await page.goto(`${srv.url}/#/apps/${id}/env`);
  await page.waitForSelector("#envlist .envrow");
  const jwt = page.locator("[data-env='JWT_SECRET']");
  check((await jwt.innerText()).includes("••••"), "secret should be masked");
  check(!(await page.content()).includes("super-secret-1"), "secret value must not be in the page");
  check((await page.locator("[data-env='GREETING'] .v").innerText()) === "<script>alert(1)</script>", "script text should render as text");
  check((await page.locator("[data-env='GREETING'] script").count()) === 0 && (await page.locator("#envlist script").count()) === 0, "no script element");
  await jwt.locator("button[aria-label='Lihat JWT_SECRET']").click();
  await page.waitForFunction(() => document.querySelector("[data-env='JWT_SECRET'] .v")?.textContent === "super-secret-1");
  await jwt.locator("button[aria-label='Sembunyikan JWT_SECRET']").click();
  check((await jwt.innerText()).includes("••••"), "secret masked again");

  step("env edit → draft banner → save → 'Deploy ulang untuk menerapkan'");
  await page.click("button:has-text('Tambah')");
  await page.fill("input[aria-label='Nama variabel']", "NEW_FLAG");
  await page.fill("input[aria-label='Nilai']", "on");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector("[data-env='NEW_FLAG']");
  await page.waitForSelector(".banner:has-text('1 perubahan belum disimpan')");
  await page.click("button:has-text('Simpan perubahan')");
  await page.waitForSelector(".banner:has-text('Deploy ulang untuk menerapkan')");
  const stored = await api("GET", `/api/apps/${id}`, undefined, cookie);
  check(stored.body.env.some((e) => e.name === "NEW_FLAG" && e.value === "on"), "NEW_FLAG not stored");
  check(stored.body.env.find((e) => e.name === "JWT_SECRET").value === "", "secret must stay masked in the API");

  step("redeploy applies the change and the banner goes away");
  await page.click(".banner button:has-text('Deploy ulang')");
  await page.waitForSelector(".banner:has-text('Deploy ulang untuk menerapkan')", { state: "detached", timeout: 20000 });

  step("log tab shows lines from the fake container; markup in a line stays text");
  await page.goto(`${srv.url}/#/apps/${id}/logs`);
  await page.waitForSelector("#biglog .ln:has-text('Listening on port 8080')", { timeout: 15000 });
  await page.waitForSelector("#biglog .ln:has-text('hello from <b>env</b>')");
  check((await page.locator("#biglog b").count()) === (await page.locator("#biglog .ln b").count()), "stray <b> in the log");
  check((await page.locator("#biglog .ln.w").count()) > 0 && (await page.locator("#biglog .ln.e").count()) > 0, "warning and error lines colored");
  await page.fill("input[aria-label='Cari di log']", "slow connection");
  check((await page.locator("#biglog .ln").count()) === (await page.locator("#biglog .ln:has-text('slow connection')").count()), "filter leaves other lines");
  await page.fill("input[aria-label='Cari di log']", "");
  await page.click("button:has-text('Jeda')");
  await page.waitForSelector(".chip:has-text('Dijeda')");
  await page.click("button:has-text('Lanjut')");

  step("rollback to #1 creates a new deployment");
  await page.goto(`${srv.url}/#/apps/${id}/deployments`);
  await page.waitForSelector("table.t tbody tr");
  const before = await page.locator("table.t tbody tr").count();
  const rowV1 = page.locator("table.t tbody tr", { hasText: "v1" }).last();
  await rowV1.locator("button:has-text('Kembalikan')").click();
  await page.click(".dlg button[type=submit]");
  await page.waitForFunction((n) => document.querySelectorAll("table.t tbody tr").length > n, before, { timeout: 20000 });
  const deps = await api("GET", `/api/apps/${id}/deployments`, undefined, cookie);
  check(deps.body.length === before + 1, `deployments ${deps.body.length}, want ${before + 1}`);

  step("domains: add, status, guide filled in");
  await page.goto(`${srv.url}/#/apps/${id}/domains`);
  await page.waitForSelector("input[aria-label='Domain baru']");
  await page.fill("input[aria-label='Domain baru']", "not a host");
  await page.click("button:has-text('Tambah')");
  await page.waitForSelector(".errtext:has-text('Domain tidak valid')");
  await page.fill("input[aria-label='Domain baru']", "billing.perusahaan.co.id");
  await page.click("button:has-text('Tambah')");
  await page.waitForSelector("td.mono:has-text('billing.perusahaan.co.id')");
  check((await page.locator(".guide .gmethod.on").count()) === 1, "one guide method visible");
  check((await page.locator(".guide .seg button").count()) === 1, "no public IP → only Tunnel is offered");
  check((await page.locator(".guide .gmethod.on").innerText()).includes("perusahaan.co.id"), "guide filled with the domain");

  step("terminal drawer opens and echoes");
  await page.goto(`${srv.url}/#/apps/${id}`);
  await page.waitForSelector("button:has-text('Terminal'):not([disabled])", { timeout: 15000 });
  await page.click("button:has-text('Terminal')");
  await page.waitForSelector(".drawer .chip:has-text('Tersambung')");
  await page.keyboard.type("ls\r");
  await page.waitForFunction(() => document.querySelector(".drawer .xterm-rows")?.textContent.includes("ls"));
  await page.click("button[aria-label='Tutup terminal']");
  check((await page.locator(".drawer").count()) === 0, "drawer should close");

  step("stop and start from the general tab");
  await page.click("button:has-text('Stop')");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector(".ph .chip:has-text('Berhenti')", { timeout: 15000 });
  await page.click("button:has-text('Jalankan')");
  await page.waitForSelector(".ph .chip:has-text('Berjalan')", { timeout: 15000 });

  step("delete: wrong name keeps the button disabled; exact name deletes");
  await page.goto(`${srv.url}/#/apps/${id}/advanced`);
  await page.click("button:has-text('Hapus aplikasi')");
  await page.fill(".dlg input", "billing");
  check(await page.locator(".dlg button[type=submit]").isDisabled(), "Hapus must stay disabled for a wrong name");
  await page.fill(".dlg input", "billing-api");
  check(!(await page.locator(".dlg button[type=submit]").isDisabled()), "Hapus should enable for the exact name");
  await page.click(".dlg button[type=submit]");
  await page.waitForURL(/#\/apps$/);
  await page.waitForSelector("text=Deploy aplikasi pertama");

  step("static binary: terminal disabled with the reason");
  const bin = buildStaticBinary(tmp, "hello");
  const f2 = new FormData();
  f2.append("file", new Blob([fs.readFileSync(bin)]), "hello");
  const up2 = await (await fetch(`${srv.url}/api/uploads`, { method: "POST", headers: { cookie }, body: f2 })).json();
  const c2 = await api("POST", "/api/apps", { upload_id: up2.id, name: "hello-static", memory_mb: 128, cpu_milli: 100, ip_access: false, env: [] }, cookie);
  check(c2.status === 201, `create static ${c2.status}`);
  check(up2.report.language === "go", `upload language ${up2.report.language}`);
  await page.goto(`${srv.url}/#/apps/${c2.body.app.id}`);
  await page.waitForSelector("button:has-text('Terminal')");
  const t = page.locator("button:has-text('Terminal')");
  check(await t.isDisabled(), "terminal should be disabled for a static binary");
  check(((await t.getAttribute("title")) || "").includes("distroless"), `reason: ${await t.getAttribute("title")}`);
  check((await page.locator(".dhead .lgbox img").getAttribute("src")).includes("go-original"), "header shows the Go logo");
  check((await page.locator(".crumb img").getAttribute("src")).includes("go-original"), "breadcrumb shows the Go logo");
  check(((await page.locator(".dhead .mono").textContent()) || "").includes("Go · Binary statis"), "subtitle names the language");
} finally {
  await browser.close();
  const logs = srv.logs();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
  if (problems.length) {
    console.log("problems:\n" + problems.join("\n"));
    process.exitCode = 1;
  }
  if (process.exitCode) console.log(logs.split("\n").slice(-15).join("\n"));
}
if (!process.exitCode) console.log("smoke-app: ok");
