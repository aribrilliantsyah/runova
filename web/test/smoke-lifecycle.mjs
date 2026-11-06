// Session and route lifecycle: nothing a page opened (terminal drawer, dialogs, log sockets,
// timers) may outlive the page or the session.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, login, watch, check } from "./lib.mjs";

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-life-"));
const srv = await startRunova();
const browser = await chromium.launch();
const page = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
const problems = watch(page, srv.url);
problems.allow("GET", "/api/notifications", 401);
problems.allow("POST", "/api/me/password", 401);
const step = (m) => console.log(`- ${m}`);

const sockets = [];
page.on("websocket", (ws) => {
  const rec = { url: ws.url(), closed: false };
  ws.on("close", () => (rec.closed = true));
  sockets.push(rec);
});

try {
  const lr = await fetch(`${srv.url}/api/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: "admin", password: srv.adminPassword }) });
  const cookie = lr.headers.get("set-cookie").split(";")[0];
  const form = new FormData();
  form.append("file", new Blob([fs.readFileSync(buildSampleJar(tmp))]), "billing-api-2.4.1.jar");
  const up = await (await fetch(`${srv.url}/api/uploads`, { method: "POST", headers: { cookie }, body: form })).json();
  const c = await (await fetch(`${srv.url}/api/apps`, { method: "POST", headers: { cookie, "Content-Type": "application/json" }, body: JSON.stringify({ upload_id: up.id, name: "billing-api", java_version: 21, memory_mb: 512, env: [{ name: "JWT_SECRET", value: "x", secret: true }, { name: "DB_PASSWORD", value: "x", secret: true }] }) })).json();
  const id = c.app.id;

  await login(page, srv.url, "admin", srv.adminPassword);

  step("leaving the app page closes its log sockets and the terminal drawer");
  await page.goto(`${srv.url}/#/apps/${id}`);
  await page.waitForSelector("button:has-text('Terminal'):not([disabled])", { timeout: 20000 });
  await page.waitForSelector(".cols pre.log .ln:has-text('Listening')");
  await page.click("button:has-text('Terminal')");
  await page.waitForSelector(".drawer .chip:has-text('Tersambung')");
  await page.goto(`${srv.url}/#/apps`);
  await page.waitForSelector(".acard");
  check((await page.locator(".drawer").count()) === 0, "terminal drawer must close when the app page is left");
  await page.waitForFunction(() => true);
  await new Promise((r) => setTimeout(r, 300));
  const open = sockets.filter((s) => !s.closed);
  check(open.length === 0, `sockets still open after leaving the app page: ${open.map((s) => s.url)}`);

  step("a quick visit does not leave an orphan log stream");
  const before = sockets.length;
  for (let i = 0; i < 4; i++) {
    await page.goto(`${srv.url}/#/apps/${id}`);
    await page.goto(`${srv.url}/#/apps`);
  }
  await page.waitForSelector(".acard");
  await new Promise((r) => setTimeout(r, 800));
  check(sockets.filter((s) => !s.closed).length === 0, `orphan sockets: ${sockets.filter((s) => !s.closed).map((s) => s.url)} (opened ${sockets.length - before})`);

  step("an open dialog is closed when the session ends");
  await page.click(".acct");
  await page.click(".pop button:has-text('Ganti password')");
  await page.waitForSelector(".dlg");
  await fetch(`${srv.url}/api/logout`, { method: "POST", headers: { cookie, "Content-Type": "application/json" }, body: "{}" });
  // the page's own session is a different cookie: end it through the UI's 401 path instead
  await page.evaluate(() => fetch("/api/logout", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" }));
  await page.fill(".dlg input[aria-label='Password saat ini']", "x");
  await page.fill(".dlg input[aria-label='Password baru']", "a-long-password-1");
  await page.fill(".dlg input[aria-label='Ulangi password baru']", "a-long-password-1");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector("#login-user");
  check((await page.locator(".dlg-scrim").count()) === 0, "dialog must not cover the login screen");
  check((await page.locator(".login .err").innerText()).includes("Sesi berakhir"), "login should explain the session ended");
  check((await page.locator(".drawer").count()) === 0, "no drawer on the login screen");

  step("the general-tab log keeps at most a few lines in memory");
  await login(page, srv.url, "admin", srv.adminPassword);
  await page.goto(`${srv.url}/#/apps/${id}`);
  await page.waitForSelector(".cols pre.log .ln");
  check((await page.locator(".cols pre.log .ln").count()) <= 5, "log card shows at most 5 lines");
} finally {
  await browser.close();
  const logs = srv.logs();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
  if (problems.length) {
    console.log("problems:\n" + problems.join("\n"));
    process.exitCode = 1;
  }
  if (process.exitCode) console.log(logs.split("\n").slice(-10).join("\n"));
}
if (!process.exitCode) console.log("smoke-lifecycle: ok");
