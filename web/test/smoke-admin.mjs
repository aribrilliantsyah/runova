// Task 5 smoke: users (password shown once), quota → database limit lock, databases (connection,
// reveal, backup, restore), activity, server page, and non-admin redirects.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, login, watch, check } from "./lib.mjs";

const srv = await startRunova();
const browser = await chromium.launch();
const ctxA = await browser.newContext({ viewport: { width: 1440, height: 900 }, permissions: ["clipboard-read", "clipboard-write"] });
const admin = await ctxA.newPage();
const problems = watch(admin, srv.url);
problems.allow("POST", "/api/users", 400);
let budiProblems;
const step = (m) => console.log(`- ${m}`);

try {
  await login(admin, srv.url, "admin", srv.adminPassword);

  step("admin sees the admin menu");
  check((await admin.locator(".nav a[href='#/users']").count()) === 1 && (await admin.locator(".nav a[href='#/server']").count()) === 1, "admin links missing");

  step("create a user: initial password shown once, with copy");
  await admin.goto(`${srv.url}/#/users`);
  await admin.waitForSelector("#userrows tr");
  await admin.click("#addUser");
  await admin.fill("#nu-username", "Bad_Name");
  await admin.click(".dlg button[type=submit]");
  await admin.waitForSelector(".dlg .err:has-text('Username harus')");
  await admin.fill("#nu-username", "budi");
  await admin.fill("input[aria-label='Nama tampilan']", "Budi Santoso");
  await admin.click(".dlg button[type=submit]");
  await admin.waitForSelector(".dlg .secret code");
  const password = (await admin.locator(".dlg .secret code").innerText()).trim();
  check(password.length >= 10, `password ${password}`);
  await admin.click(".dlg .secret button");
  await admin.waitForSelector(".dlg .secret button:has-text('Tersalin')");
  check((await admin.evaluate(() => navigator.clipboard.readText())) === password, "copy button should copy the password");
  await admin.click(".dlg button[type=submit]");
  await admin.waitForSelector(".dlg", { state: "detached" });
  check(!(await admin.content()).includes(password), "the password must be gone from the page after closing");
  await admin.waitForSelector("#userrows tr[data-user='budi']");

  step("quota: databases = 1");
  await admin.click("#userrows tr[data-user='budi'] button[aria-label='Kuota budi']");
  const dbInput = admin.locator(".dlg input[id$='-databases']");
  await dbInput.fill("1");
  await admin.click(".dlg button[type=submit]");
  await admin.waitForSelector(".dlg", { state: "detached" });
  await admin.waitForFunction(() => document.querySelector("#userrows tr[data-user='budi']")?.textContent.includes("1"));

  step("budi signs in, creates one database, and the second is locked");
  const ctxB = await browser.newContext({ viewport: { width: 1440, height: 900 }, permissions: ["clipboard-read", "clipboard-write"] });
  const budi = await ctxB.newPage();
  budiProblems = watch(budi, srv.url);
budiProblems.allow("POST", "/api/databases", 400);
// his account is deleted at the end: every poll after that is a 401 and must send him to the login
for (const p of ["/api/apps", "/api/databases", "/api/info", "/api/notifications", "/api/activity"]) budiProblems.allow("GET", p, 401);
  await login(budi, srv.url, "budi", password);
  check((await budi.locator(".nav a[href='#/users']").count()) === 0, "a normal user must not see Pengguna");
  check((await budi.locator(".nav a[href='#/server']").count()) === 0, "a normal user must not see Server");
  await budi.goto(`${srv.url}/#/databases`);
  await budi.waitForSelector("#dbNewBtn");
  await budi.click("#dbNewBtn");
  await budi.fill(".dlg input[aria-label='Nama database']", "Bad Name");
  await budi.click(".dlg button[type=submit]");
  await budi.waitForSelector(".dlg .err:has-text('Nama database harus')");
  await budi.fill(".dlg input[aria-label='Nama database']", "billing");
  await budi.click(".dlg button[type=submit]");
  await budi.waitForSelector(".dbcard:has-text('budi_billing')");
  await budi.waitForSelector("#dbNewCard:has-text('Batas database tercapai')");
  check((await budi.locator("#dbNewCard").getAttribute("aria-disabled")) === "true", "new-database card should be disabled");
  check(await budi.locator("#dbNewBtn").isDisabled(), "Buat database button should be disabled");

  step("connection: password hidden, then revealed through the API");
  await budi.waitForSelector("#connCard");
  check((await budi.locator("#connCard").innerText()).includes("••••"), "password should be masked");
  await budi.click("button:has-text('Lihat password')");
  await budi.waitForFunction(() => !document.querySelector("#connCard").textContent.includes("••••"));

  step("backup now, then restore needs the database name");
  await budi.click("button:has-text('Backup sekarang')");
  await budi.waitForSelector("#backupCard tbody tr");
  await budi.click("#backupCard button:has-text('Restore')");
  await budi.fill(".dlg input", "wrong");
  check(await budi.locator(".dlg button[type=submit]").isDisabled(), "Restore must wait for the exact name");
  await budi.fill(".dlg input", "budi_billing");
  await budi.click(".dlg button[type=submit]");
  await budi.waitForSelector(".toast:has-text('di-restore')");

  step("normal user opening #/server or #/users lands on #/apps");
  for (const hash of ["#/server", "#/users"]) {
    await budi.goto(`${srv.url}/${hash}`);
    await budi.waitForURL(/#\/apps$/);
  }

  step("activity: budi sees his own, admin can filter by actor and page");
  await budi.goto(`${srv.url}/#/activity`);
  await budi.waitForSelector("#actrows tr");
  check((await budi.locator("select[aria-label='Pelaku']").count()) === 0, "no actor filter for users");
  const text = await budi.locator("#actrows").innerText();
  check(text.includes("budi_billing") && !text.includes("Pengguna dibuat"), `budi activity: ${text}`);
  await admin.goto(`${srv.url}/#/activity`);
  await admin.waitForSelector("select[aria-label='Pelaku']");
  await admin.waitForSelector("#actrows tr");
  const total = await admin.locator("#actrows tr").count();
  await admin.selectOption("select[aria-label='Pelaku']", { label: "budi" });
  await admin.waitForFunction((n) => document.querySelectorAll("#actrows tr").length < n, total);

  step("server page: services, host and defaults; saving a runtime toggle persists");
  await admin.goto(`${srv.url}/#/server`);
  await admin.waitForSelector(".g3 .svc");
  check((await admin.locator(".g3 .chip.ok").count()) === 3, "Docker, Caddy and PostgreSQL should all be active (fake backends)");
  await admin.click("#rtlist .rtitem:has-text('Java 8') button.sw");
  await admin.waitForSelector("#rtlist .rtitem:has-text('Java 8') button.sw[aria-checked=false]");
  const settings = await (await fetch(`${srv.url}/api/settings`, { headers: { cookie: (await ctxA.cookies()).map((c) => `${c.name}=${c.value}`).join("; ") } })).json();
  check(settings.runtimes.java8 === false, "Java 8 should be off on the server");

  step("server page: trusted proxies are saved, shown again after reload, and a bad one is refused");
  const proxyBox = admin.locator("textarea[aria-label='Proxy di depan Runova']");
  await proxyBox.fill("10.0.0.5\n192.168.0.0/24");
  await admin.click("button:has-text('Simpan setelan')");
  await admin.waitForSelector(".toast:has-text('Setelan disimpan')");
  await admin.reload();
  await admin.waitForSelector("textarea[aria-label='Proxy di depan Runova']");
  check((await admin.locator("textarea[aria-label='Proxy di depan Runova']").inputValue()) === "10.0.0.5\n192.168.0.0/24", "trusted proxies should survive a reload");
  problems.allow("PUT", "/api/settings", 400);
  await admin.locator("textarea[aria-label='Proxy di depan Runova']").fill("0.0.0.0/0");
  await admin.click("button:has-text('Simpan setelan')");
  await admin.waitForFunction(() => document.body.textContent.includes("seluruh internet"));
  await admin.locator("textarea[aria-label='Proxy di depan Runova']").fill("");
  await admin.click("button:has-text('Simpan setelan')");
  await admin.waitForSelector(".toast:has-text('Setelan disimpan')");

  step("delete a user: warns about data, needs the username");
  await admin.goto(`${srv.url}/#/users`);
  await admin.waitForSelector("#userrows tr[data-user='budi']");
  await admin.click("#userrows tr[data-user='budi'] button[aria-label='Lainnya untuk budi']");
  await admin.click(".pop button:has-text('Hapus pengguna')");
  check((await admin.locator(".dlg").innerText()).includes("database PostgreSQL"), "warning mentions databases");
  check(await admin.locator(".dlg button[type=submit]").isDisabled(), "delete disabled before typing");
  await admin.fill(".dlg input", "budi");
  await admin.click(".dlg button[type=submit]");
  await admin.waitForSelector("#userrows tr[data-user='budi']", { state: "detached" });

  step("budi's session is gone: next request returns to the login with the message");
  await budi.goto(`${srv.url}/#/apps`);
  await budi.waitForSelector("#login-user", { timeout: 15000 });
  check((await budi.locator(".login .err").innerText()).includes("Sesi berakhir, silakan masuk lagi"), "login should say the session ended");
} finally {
  await browser.close();
  const logs = srv.logs();
  await srv.stop();
  const all = [...problems, ...(budiProblems || [])];
  if (all.length) {
    console.log("problems:\n" + all.join("\n"));
    process.exitCode = 1;
  }
  if (process.exitCode) console.log(logs.split("\n").slice(-15).join("\n"));
}
if (!process.exitCode) console.log("smoke-admin: ok");
