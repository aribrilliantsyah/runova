// Admin view smoke: an admin opens Pengguna → a user → that user's app, sees the "Milik …" banner,
// the live log and the deployment timeline, runs Restart, and finds owner-only controls absent;
// "Semua pengguna" on Aplikasi shows owners; a normal user cannot open #/users/<id> or another
// user's app. SHOTS=dir also saves screenshots at 1440 and 360 px, light and dark.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, login, watch, check } from "./lib.mjs";

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-admin-view-"));
const shots = process.env.SHOTS || "";
const srv = await startRunova();
const browser = await chromium.launch();
const step = (m) => console.log(`- ${m}`);
const problems = [];

async function api(method, p, body, cookie) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null };
}
async function signIn(username, password) {
  const r = await fetch(`${srv.url}/api/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username, password }) });
  check(r.status === 200, `login ${username}: ${r.status}`);
  return r.headers.get("set-cookie").split(";")[0];
}

try {
  const adminCookie = await signIn("admin", srv.adminPassword);
  for (const u of ["budi", "sari"]) {
    const r = await api("POST", "/api/users", { username: u, display_name: u === "budi" ? "Budi Santoso" : "", role: "user", password: `${u}-password-12` }, adminCookie);
    check(r.status === 201, `create ${u}: ${r.status} ${JSON.stringify(r.body)}`);
  }
  const budiId = (await api("GET", "/api/users?q=budi", undefined, adminCookie)).body.items[0].id;
  const budiCookie = await signIn("budi", "budi-password-12");
  const jar = buildSampleJar(tmp);
  const form = new FormData();
  form.append("file", new Blob([fs.readFileSync(jar)]), "billing-api-2.4.1.jar");
  const up = await (await fetch(`${srv.url}/api/uploads`, { method: "POST", headers: { cookie: budiCookie }, body: form })).json();
  const created = await api("POST", "/api/apps", { upload_id: up.id, name: "billing-api", java_version: 21, memory_mb: 512, ip_access: true, env: [{ name: "JWT_SECRET", value: "budi-secret-value", secret: true }, { name: "MODE", value: "prod", secret: false }] }, budiCookie);
  check(created.status === 201, `create app ${created.status}`);
  const appId = created.body.app.id;
  for (let i = 0; i < 100; i++) {
    if ((await api("GET", `/api/apps/${appId}`, undefined, budiCookie)).body.app.status === "running") break;
    await new Promise((r) => setTimeout(r, 200));
  }

  const ctxA = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const admin = await ctxA.newPage();
  const pa = watch(admin, srv.url);
  await login(admin, srv.url, "admin", srv.adminPassword);

  step("Pengguna: the row links to the user's page");
  await admin.goto(`${srv.url}/#/users`);
  await admin.waitForSelector("#userrows tr[data-user='budi']");
  await admin.click("#userrows tr[data-user='budi'] a[aria-label='Lihat aplikasi budi']");
  await admin.waitForSelector("#uapps tr[data-app='billing-api']");
  check((await admin.evaluate(() => location.hash)) === `#/users/${budiId}`, "user page URL");
  check((await admin.locator("#v-user h1").innerText()).includes("Budi Santoso"), "profile name");
  check((await admin.locator("#ustats .stat").count()) === 4, "usage tiles");
  const row = admin.locator("#uapps tr[data-app='billing-api']");
  check((await row.innerText()).includes("Berjalan") && (await row.innerText()).includes("v1"), `app row: ${await row.innerText()}`);
  check((await admin.locator(".crumb").innerText()).includes("budi"), "breadcrumb names the user");

  step("the app opens with the owner banner; owner-only controls are absent");
  await row.locator("a.u-applink").click();
  await admin.waitForSelector("#adminview");
  check((await admin.locator("#adminview").innerText()).includes("Milik budi"), "banner names the owner");
  check((await admin.locator("text=Unggah versi baru").count()) === 0, "no new version for an admin");
  check(await admin.locator("button:has-text('Terminal')").isDisabled(), "terminal disabled");
  check((await admin.locator(".ph .acts a[aria-label='Hapus aplikasi']").count()) === 0, "no delete in the header");
  if (shots) {
    for (const [w, hgt] of [[1440, 900], [360, 780]]) {
      for (const theme of ["light", "dark"]) {
        await admin.setViewportSize({ width: w, height: hgt });
        await admin.evaluate((t) => (document.documentElement.dataset.theme = t), theme);
        await admin.waitForTimeout(150);
        await admin.screenshot({ path: path.join(shots, `app-as-admin-${w}-${theme}.png`), fullPage: w === 1440 });
      }
    }
    await admin.setViewportSize({ width: 1440, height: 900 });
    await admin.evaluate(() => (document.documentElement.dataset.theme = "light"));
  }

  step("Restart works and tells the owner");
  await admin.click("button:has-text('Restart')");
  await admin.waitForSelector(".dlg :text('diberi tahu')");
  await admin.click(".dlg button[type=submit]");
  await admin.waitForSelector(".toast:has-text('Restart dimulai')");

  step("environment: secrets stay masked, no reveal or edit");
  await admin.goto(`${srv.url}/#/apps/${appId}/env`);
  await admin.waitForSelector("#envlist .envrow[data-env='JWT_SECRET']");
  check(!(await admin.content()).includes("budi-secret-value"), "secret value on the page");
  check((await admin.locator("#t-env button").count()) === 0, "env tab has no buttons for an admin");

  step("live log and deployment timeline");
  await admin.goto(`${srv.url}/#/apps/${appId}/logs`);
  await admin.waitForSelector("#biglog .ln:has-text('Listening on port 8080')", { timeout: 15000 });
  await admin.goto(`${srv.url}/#/apps/${appId}/deployments`);
  await admin.waitForSelector("tr[data-dep]");
  check((await admin.locator("text=Kembalikan").count()) === 0, "no rollback for an admin");
  await admin.locator("tr[data-dep] a:has-text('Detail')").first().click();
  await admin.waitForSelector(".dep-head h2");

  step("files: read only; Lanjutan: read only");
  await admin.goto(`${srv.url}/#/apps/${appId}/files`);
  await admin.waitForSelector(".fm-head :text('Hanya baca')");
  check((await admin.locator("#fupload, #fnewdir").count()) === 0, "file write buttons for an admin");
  await admin.goto(`${srv.url}/#/apps/${appId}/advanced`);
  await admin.waitForSelector("#a-readonly");
  check((await admin.locator("button:has-text('Hapus aplikasi')").count()) === 0, "delete button for an admin");

  step("the actions are in Aktivitas under budi/billing-api");
  const acts = await api("GET", `/api/activity/search?q=${encodeURIComponent("target:budi/billing-api")}`, undefined, adminCookie);
  const kinds = new Set(acts.body.items.map((a) => a.action));
  check(kinds.has("restart") && kinds.has("log.view"), `activity: ${[...kinds]}`);
  const notes = await api("GET", "/api/notifications", undefined, budiCookie);
  check(notes.body.notifications.some((n) => n.title.includes("Admin admin me-restart billing-api")), "owner notified");

  step("Aplikasi → Semua pengguna shows the owner");
  await admin.goto(`${srv.url}/#/apps`);
  await admin.waitForSelector(".empty, #apps");
  await admin.click("#allusers");
  await admin.waitForSelector("#apps .acard .chip.owner:has-text('budi')");

  if (shots) {
    for (const [name, hash, sel] of [["apps-all", "#/apps", "#apps"], ["user", `#/users/${budiId}`, "#uapps"]]) {
      for (const [w, hgt] of [[1440, 900], [360, 780]]) {
        for (const theme of ["light", "dark"]) {
          await admin.setViewportSize({ width: w, height: hgt });
          if (name === "user") await admin.goto(`${srv.url}/${hash}`);
          await admin.waitForSelector(sel);
          await admin.evaluate((t) => (document.documentElement.dataset.theme = t), theme);
          await admin.waitForTimeout(150);
          const wide = await admin.evaluate(() => document.documentElement.scrollWidth > innerWidth);
          check(!wide, `${name} ${w} scrolls sideways`);
          await admin.screenshot({ path: path.join(shots, `${name}-${w}-${theme}.png`), fullPage: true });
        }
      }
    }
  }

  step("a normal user cannot open #/users/<id> or another user's app");
  const ctxS = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const sari = await ctxS.newPage();
  const ps = watch(sari, srv.url);
  ps.allow("GET", `/api/apps/${appId}`, 404);
  await login(sari, srv.url, "sari", "sari-password-12");
  await sari.goto(`${srv.url}/#/users/${budiId}`);
  await sari.waitForFunction(() => location.hash === "#/apps");
  check((await sari.locator("#v-user").count()) === 0, "user page rendered for a normal user");
  check((await api("GET", `/api/users/${budiId}`, undefined, await signIn("sari", "sari-password-12"))).status === 403, "API user detail for a normal user");
  await sari.goto(`${srv.url}/#/apps/${appId}`);
  await sari.waitForSelector("#v-noapp :text('Aplikasi tidak ditemukan')");
  check((await sari.locator("#v-app").count()) === 0 && !(await sari.content()).includes("billing-api"), "another user's app rendered for sari");
  problems.push(...pa, ...ps);
} catch (e) {
  console.error(e);
  process.exitCode = 1;
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
if (!process.exitCode) console.log("smoke-admin-view: ok");
