// Shared hosting domains end to end in the panel: the admin registers a domain on the Server page,
// an ordinary user picks it in the wizard with a label, quota 1 locks the second address, the
// user never sees "Domain sendiri", and disabling the domain shows up on the app's Domain tab.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildStaticBinary, login, watch, check } from "./lib.mjs";

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-hosting-"));
const srv = await startRunova();
const browser = await chromium.launch();
const step = (m) => console.log(`- ${m}`);
const failures = [];

async function api(method, p, body, cookie) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null, headers: r.headers };
}

const ctxA = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const admin = await ctxA.newPage();
const problems = [watch(admin, srv.url)];
problems[0].allow("POST", "/api/hosting-domains", 400);
problems[0].allow("DELETE", "/api/hosting-domains/1", 409);

try {
  const bin = buildStaticBinary(tmp, "hello-site");
  const lr = await api("POST", "/api/login", { username: "admin", password: srv.adminPassword });
  const adminCookie = lr.headers.get("set-cookie").split(";")[0];
  const settings = (await api("GET", "/api/settings", undefined, adminCookie)).body;
  settings.public_ip = "203.0.113.15";
  await api("PUT", "/api/settings", settings, adminCookie);

  await login(admin, srv.url, "admin", srv.adminPassword);

  step("a user without any hosting domain is told to contact the admin; the admin gets a link");
  await api("POST", "/api/users", { username: "budi", display_name: "Budi", password: "budi-password-1", role: "user" }, adminCookie);
  const ctxB = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const budi = await ctxB.newPage();
  problems.push(watch(budi, srv.url));
  await login(budi, srv.url, "budi", "budi-password-1");
  await budi.goto(`${srv.url}/#/deploy`);
  await budi.setInputFiles("#upload-file", bin);
  await budi.waitForSelector(".fileok");
  await budi.click(".steps button[data-step='4']");
  await budi.waitForSelector(".addr .anone:has-text('Belum ada domain hosting; hubungi admin')");
  check((await budi.locator(".addr select").count()) === 0, "no picker without domains for a user");
  await admin.goto(`${srv.url}/#/deploy`);
  await admin.setInputFiles("#upload-file", bin);
  await admin.waitForSelector(".fileok");
  await admin.click(".steps button[data-step='4']");
  await admin.waitForSelector(".addr .anone a[href='#/server']");

  step("admin registers apps.contoh.id from the Server page; the DNS hint names *.apps");
  await admin.goto(`${srv.url}/#/server`);
  await admin.waitForSelector("#hostingcard");
  await admin.fill("#hostname", "*.Apps.Contoh.ID");
  await admin.waitForSelector("#hostdns");
  const hint = await admin.locator("#hostdns").innerText();
  check(hint.includes("*.apps") && hint.includes("203.0.113.15") && hint.includes("DNS only"), `DNS hint: ${hint}`);
  await admin.fill("#hostname", "x");
  await admin.waitForSelector(".errtext:has-text('Domain tidak valid')");
  await admin.fill("#hostname", "*.apps.contoh.id");
  await admin.click("#hostadd");
  await admin.waitForSelector("#hostrows tr[data-hosting='apps.contoh.id'] .chip.ok");

  step("a nested registration is refused with the reason");
  await admin.fill("#hostname", "x.apps.contoh.id");
  await admin.click("#hostadd");
  await admin.waitForSelector(".toast:has-text('Bertumpuk')");

  step("quota 1 for budi; wizard: no 'Domain sendiri', label rules, availability, preview");
  const budiRow = (await api("GET", "/api/users", undefined, adminCookie)).body.find((u) => u.username === "budi");
  await api("PATCH", `/api/users/${budiRow.id}`, { quota: { ...budiRow.quota, apps: 1 } }, adminCookie);
  await budi.goto(`${srv.url}/#/apps`);
  await budi.goto(`${srv.url}/#/deploy`);
  await budi.reload();
  await budi.setInputFiles("#upload-file", bin);
  await budi.waitForSelector(".fileok");
  await budi.click(".steps button[data-step='4']");
  await budi.waitForSelector(".addr select");
  const options = await budi.locator(".addr select option").allInnerTexts();
  check(options.length === 1 && options[0] === "apps.contoh.id", `budi's options: ${options}`);
  check((await budi.locator(".addr .aquota").innerText()).includes("0 dari 1"), "quota marker");
  await budi.fill(".addr input[aria-label='Nama alamat']", "ab");
  await budi.waitForSelector(".addr .astate.err:has-text('3–40')");
  await budi.fill(".addr input[aria-label='Nama alamat']", "www");
  await budi.waitForSelector(".addr .astate.err:has-text('dicadangkan')");
  check(await budi.locator("#deploy").isDisabled(), "a reserved label blocks Deploy");
  await budi.fill(".addr input[aria-label='Nama alamat']", "CV-Budi");
  await budi.waitForSelector(".addr .astate.ok:has-text('Tersedia')");
  check((await budi.locator(".addr .apreview").innerText()).includes("cv-budi.apps.contoh.id"), "preview");
  await budi.fill("#nm", "cv-budi");
  await budi.click("#deploy");
  await budi.waitForURL(/#\/apps\/\d+\/logs/);
  const appId = Number(budi.url().match(/apps\/(\d+)/)[1]);

  step("the app has that address, and the control is locked at quota");
  await budi.goto(`${srv.url}/#/apps/${appId}/domains`);
  await budi.waitForSelector("td.mono:has-text('cv-budi.apps.contoh.id')");
  await budi.waitForSelector(".addr .astate.err:has-text('Kuota subdomain habis (1 dari 1)')");
  check(await budi.locator(".addr input[aria-label='Nama alamat']").isDisabled(), "label input disabled at quota");
  const budiCookie = (await ctxB.cookies()).map((c) => `${c.name}=${c.value}`).join("; ");
  const mine = (await api("GET", `/api/apps/${appId}/domains`, undefined, budiCookie)).body.domains;
  check(mine.length === 1 && mine[0].label === "cv-budi" && mine[0].hosting_domain_id != null, JSON.stringify(mine));

  step("disabling the domain: the app's tab says so and budi is notified");
  await admin.goto(`${srv.url}/#/apps`);
  await admin.goto(`${srv.url}/#/server`);
  await admin.waitForSelector("#hostrows tr[data-hosting='apps.contoh.id']");
  await admin.click("#hostrows tr[data-hosting='apps.contoh.id'] button.sw");
  await admin.waitForSelector(".dlg:has-text('1 subdomain berhenti dilayani')");
  await admin.click(".dlg button[type=submit]");
  await admin.waitForSelector("#hostrows tr[data-hosting='apps.contoh.id'] button.sw[aria-checked=false]");
  await budi.goto(`${srv.url}/#/apps/${appId}/domains`);
  await budi.waitForSelector(".chip:has-text('Dimatikan admin')");
  const notes = (await api("GET", "/api/notifications", undefined, budiCookie)).body;
  check(JSON.stringify(notes).includes("dimatikan admin"), `notifications: ${JSON.stringify(notes)}`);

  step("deleting a domain that is still used is refused with the count");
  await admin.click("#hostrows tr[data-hosting='apps.contoh.id'] button[aria-label='Hapus apps.contoh.id']");
  await admin.click(".dlg button[type=submit]");
  await admin.waitForSelector(".dlg .err:has-text('Masih dipakai 1 subdomain')");
  await admin.click(".dlg button:has-text('Batal')");

  step("narrow screens: no horizontal scroll on the Server card and the wizard address");
  for (const width of [360, 768, 1440]) {
    const page = await (await browser.newContext({ viewport: { width, height: 800 } })).newPage();
    problems.push(watch(page, srv.url));
    await login(page, srv.url, "admin", srv.adminPassword);
    await page.goto(`${srv.url}/#/server`);
    await page.waitForSelector("#hostrows tr");
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (over > 0) failures.push(`${width}px: Server overflows by ${over}px`);
    await page.close();
  }
} catch (e) {
  failures.push(e.stack || String(e));
} finally {
  await browser.close();
  const logs = srv.logs();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
  for (const p of problems.flat()) failures.push(p);
  if (failures.length) {
    console.log("failures:\n" + failures.join("\n"));
    console.log(logs.split("\n").slice(-12).join("\n"));
    process.exitCode = 1;
  }
}
if (!process.exitCode) console.log("smoke-hosting: ok");
