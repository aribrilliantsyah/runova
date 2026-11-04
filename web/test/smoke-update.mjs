// Update banner smoke (fake backends report 0.3.0 against an installed 0.2.0): the admin sees the
// banner on every page and can copy the command; an ordinary user does not and never asks for it;
// the "Periksa pembaruan" switch on the Server page removes and restores it; no horizontal scroll
// from 360 to 1440 px in light and dark.
import { chromium, startRunova, login, watch, check } from "./lib.mjs";

const srv = await startRunova();
const browser = await chromium.launch();
const step = (m) => console.log(`- ${m}`);
const BANNER = ".app > .main > .banner.upd:not([hidden])";

async function call(method, p, body, cookie) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null, headers: r.headers };
}

try {
  const lr = await call("POST", "/api/login", { username: "admin", password: srv.adminPassword });
  const cookie = lr.headers.get("set-cookie").split(";")[0];
  await call("POST", "/api/users", { username: "sari", display_name: "Sari", password: "sari-password-1", role: "user" }, cookie);

  step("API: admin gets the status, anonymous 401");
  for (let i = 0; i < 50; i++) {
    const r = await call("GET", "/api/update", undefined, cookie);
    if (r.body?.update_available) break;
    await new Promise((r) => setTimeout(r, 100));
  }
  const api = await call("GET", "/api/update", undefined, cookie);
  check(api.status === 200 && api.body.current === "0.2.0" && api.body.latest === "0.3.0" && api.body.update_available && api.body.enabled, `status ${JSON.stringify(api)}`);
  check((await call("GET", "/api/update")).status === 401, "anonymous must get 401");

  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, permissions: ["clipboard-read", "clipboard-write"] });
  const admin = await ctx.newPage();
  const problems = watch(admin, srv.url);
  await login(admin, srv.url, "admin", srv.adminPassword);

  step("admin sees the banner on every page");
  for (const route of ["/apps", "/deploy", "/databases", "/activity", "/users", "/server"]) {
    await admin.goto(`${srv.url}/#${route}`);
    await admin.waitForSelector(BANNER, { timeout: 10000 });
    const text = (await admin.locator(BANNER).innerText()).replace(/\s+/g, " ");
    check(text.includes("Runova v0.3.0 tersedia (terpasang: v0.2.0). Perbarui di server: sudo runova update"), `${route}: ${text}`);
  }

  step("the banner has no dismiss button; the release notes link points at the release");
  check((await admin.locator(`${BANNER} button`).count()) === 1, "only the copy button is allowed");
  const href = await admin.locator(`${BANNER} a:has-text('Catatan rilis')`).getAttribute("href");
  check(href === "https://github.com/aribrilliantsyah/runova/releases/tag/v0.3.0", `release link ${href}`);

  step("copy puts the command on the clipboard");
  await admin.click(`${BANNER} button`);
  await admin.waitForSelector(".toast:has-text('Perintah disalin')");
  check((await admin.evaluate(() => navigator.clipboard.readText())) === "sudo runova update", "clipboard content");

  step("Server page shows the versions; the switch hides and restores the banner");
  await admin.goto(`${srv.url}/#/server`);
  await admin.waitForSelector("#ver-latest:has-text('v0.3.0')");
  check((await admin.locator("#ver-current").innerText()) === "v0.2.0", "installed version");
  const sw = admin.locator("button[aria-label='Periksa pembaruan']");
  check((await sw.getAttribute("aria-checked")) === "true", "switch should start on");
  await sw.click();
  await admin.waitForSelector(".app > .main > .banner.upd", { state: "hidden" });
  await admin.waitForSelector("#ver-latest:has-text('dimatikan')");
  check((await call("GET", "/api/settings", undefined, cookie)).body.update_check === false, "setting not saved");
  await admin.locator("button[aria-label='Periksa pembaruan']").click();
  await admin.waitForSelector(BANNER, { timeout: 10000 });
  check((await call("GET", "/api/settings", undefined, cookie)).body.update_check === true, "setting not restored");

  step("an ordinary user sees no banner and never asks for /api/update");
  const userCtx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const user = await userCtx.newPage();
  const asked = [];
  user.on("request", (r) => new URL(r.url()).pathname === "/api/update" && asked.push(r.url()));
  await login(user, srv.url, "sari", "sari-password-1");
  await user.goto(`${srv.url}/#/apps`);
  await user.waitForSelector(".acard, .empty, .ph");
  await user.waitForTimeout(1500);
  check((await user.locator(".banner.upd:not([hidden])").count()) === 0, "ordinary user sees the banner");
  check(asked.length === 0, `ordinary user requested ${asked}`);
  await userCtx.close();

  step("no horizontal scroll at 360-1440 px, light and dark");
  for (const scheme of ["light", "dark"]) {
    for (const width of [360, 390, 768, 1280, 1440]) {
      const c = await browser.newContext({ viewport: { width, height: 800 }, colorScheme: scheme });
      const page = await c.newPage();
      const pr = watch(page, srv.url);
      await login(page, srv.url, "admin", srv.adminPassword);
      for (const route of ["/apps", "/server"]) {
        await page.goto(`${srv.url}/#${route}`);
        await page.waitForSelector(BANNER, { timeout: 10000 });
        const over = await page.evaluate(() => {
          const bad = [document.documentElement, document.body, document.querySelector(".main"), document.querySelector(".sheet"), document.querySelector(".banner.upd")].filter((e) => e && e.scrollWidth > e.clientWidth);
          return bad.map((e) => `${e.className || e.tagName}:${e.scrollWidth}>${e.clientWidth}`);
        });
        check(over.length === 0, `${scheme} ${width}px ${route}: horizontal overflow ${over}`);
      }
      check(pr.length === 0, `${scheme} ${width}px: ${pr.join("; ")}`);
      await c.close();
    }
  }

  check(problems.length === 0, `admin page problems: ${problems.join("; ")}`);
  console.log("smoke-update: ok");
} finally {
  await browser.close();
  await srv.stop();
}
