// Task 6 smoke: every route at 360/390/768/1280/1440 px, light and dark:
// no horizontal scroll, no console errors, no request leaving the origin, every icon-only
// control has an accessible name, keyboard focus is visible, and an env value of
// <script>alert(1)</script> renders as text.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, login, watch, check } from "./lib.mjs";

const WIDTHS = [360, 390, 768, 1280, 1440];
const THEMES = ["light", "dark"];
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-layout-"));
const srv = await startRunova();
const browser = await chromium.launch();
const failures = [];
const fail = (m) => failures.push(m);

async function call(method, p, body, cookie) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null, headers: r.headers };
}

try {
  const lr = await call("POST", "/api/login", { username: "admin", password: srv.adminPassword });
  const cookie = lr.headers.get("set-cookie").split(";")[0];

  // Seed: a JAR app with markup in its env, a second user, a database, a backup, a domain.
  const jar = buildSampleJar(tmp, { name: "billing-api-2.4.1.jar" });
  const form = new FormData();
  form.append("file", new Blob([fs.readFileSync(jar)]), "billing-api-2.4.1.jar");
  const up = await (await fetch(`${srv.url}/api/uploads`, { method: "POST", headers: { cookie }, body: form })).json();
  const db = await call("POST", "/api/databases", { suffix: "billing" }, cookie);
  check(db.status === 201, `db ${db.status}`);
  const created = await call("POST", "/api/apps", {
    upload_id: up.id, name: "billing-api", java_version: 21, memory_mb: 512, cpu_milli: 500, ip_access: true, database_id: db.body.id,
    env: [{ name: "JWT_SECRET", value: "s3", secret: true }, { name: "DB_PASSWORD", value: "p", secret: true }, { name: "GREETING", value: "<script>alert(1)</script>", secret: false }, { name: "LOG_ECHO", value: "<img src=x onerror=alert(1)>", secret: false }],
  }, cookie);
  check(created.status === 201, `app ${created.status} ${JSON.stringify(created.body)}`);
  const appId = created.body.app.id;
  await call("POST", `/api/apps/${appId}/domains`, { host: "billing.perusahaan.co.id" }, cookie);
  await call("POST", `/api/apps/${appId}/domains`, { host: "pending.perusahaan.co.id" }, cookie);
  await call("POST", `/api/databases/${db.body.id}/backups`, {}, cookie);
  await call("POST", "/api/users", { username: "sari", display_name: "Sari Wulandari", email: "sari@perusahaan.co.id", password: "sari-password-1", role: "user" }, cookie);
  for (let i = 0; i < 40; i++) {
    const a = await call("GET", `/api/apps/${appId}`, undefined, cookie);
    if (a.body.app.status === "running") break;
    await new Promise((r) => setTimeout(r, 250));
  }

  const routes = [
    "/apps",
    ...["", "/env", "/domains", "/deployments", "/logs", "/monitor", "/advanced"].map((t) => `/apps/${appId}${t}`),
    "/deploy", "/databases", "/activity", "/users", "/server",
  ];

  for (const theme of THEMES) {
    for (const width of WIDTHS) {
      const ctx = await browser.newContext({ viewport: { width, height: width < 700 ? 780 : 900 }, colorScheme: theme });
      const page = await ctx.newPage();
      const problems = watch(page, srv.url);
      const where = (r) => `${r} @${width} ${theme}`;
      await login(page, srv.url, "admin", srv.adminPassword);
      check((await page.evaluate(() => document.documentElement.dataset.theme)) === theme, `theme should follow prefers-color-scheme (${theme})`);

      const measure = async (label) => {
        await page.waitForTimeout(250);
        const m = await page.evaluate(() => {
          const el = document.documentElement;
          const sheet = document.querySelector(".sheet");
          const wide = [...document.querySelectorAll(".sheet *")].filter((e) => e.getBoundingClientRect().right > innerWidth + 1 && !e.closest(".tabs, .seg, pre, .xterm, [class*=drop]")).slice(0, 3).map((e) => `${e.tagName}.${e.className}`);
          return { doc: [el.scrollWidth, el.clientWidth], sheet: sheet ? [sheet.scrollWidth, sheet.clientWidth] : null, wide };
        });
        if (m.doc[0] > m.doc[1]) fail(`${label}: page scrolls horizontally (${m.doc[0]} > ${m.doc[1]}) ${m.wide}`);
        if (m.sheet && m.sheet[0] > m.sheet[1]) fail(`${label}: content scrolls horizontally (${m.sheet[0]} > ${m.sheet[1]}) ${m.wide}`);

        const unnamed = await page.evaluate(() => {
          const out = [];
          for (const el of document.querySelectorAll("button, a[href], [role=switch], [role=tab], select, input:not([type=hidden]), textarea")) {
            if (el.closest("[hidden]") || el.offsetParent === null && getComputedStyle(el).position !== "fixed") continue;
            const name = (el.getAttribute("aria-label") || el.getAttribute("aria-labelledby") || el.textContent || el.getAttribute("title") || el.getAttribute("placeholder") || "").trim();
            const labelled = el.id && document.querySelector(`label[for="${el.id}"]`);
            const wrapped = el.closest("label");
            if (!name && !labelled && !wrapped) out.push(`${el.tagName.toLowerCase()}.${el.className}`);
          }
          return out;
        });
        if (unnamed.length) fail(`${label}: controls without an accessible name: ${[...new Set(unnamed)].join(", ")}`);
      };

      for (const r of routes) {
        await page.goto(`${srv.url}/#${r}`);
        await page.waitForSelector(".sheet > *", { timeout: 15000 });
        if (r.endsWith("/logs")) await page.waitForSelector("#biglog .ln", { timeout: 10000 }).catch(() => {});
        if (r.endsWith("/env")) await page.waitForSelector("#envlist .envrow");
        if (r.endsWith("/databases")) await page.waitForSelector(".dbcard");
        if (r.endsWith("/users")) await page.waitForSelector("#userrows tr");
        if (r.endsWith("/server")) await page.waitForSelector(".g3 .svc");
        if (r.endsWith("/domains")) await page.waitForSelector("table.t tbody tr");
        await measure(where(r));
        if (r.endsWith("/env")) {
          const text = await page.locator("[data-env='GREETING'] .v").innerText();
          if (text !== "<script>alert(1)</script>") fail(`${where(r)}: env value should render as text, got ${text}`);
          if ((await page.locator("#envlist script, #envlist img").count()) > 0) fail(`${where(r)}: markup in env became an element`);
        }
      }

      // Deploy wizard: every step with a real upload.
      await page.goto(`${srv.url}/#/deploy`);
      await page.waitForSelector(".drop");
      await page.setInputFiles("#upload-file", jar);
      await page.waitForSelector(".fileok");
      for (let s = 1; s <= 4; s++) {
        if (width > 760) await page.click(`.steps button[data-step='${s}']`);
        else if (s > 1) await page.click(".mbar #mnext, .mbar button:has-text('Lanjut')").catch(() => {});
        await measure(where(`/deploy step ${s}`));
        if (s === 3) {
          const hint = await page.locator("[data-env='GREETING']").count();
          void hint;
        }
      }
      if (width <= 760) {
        const vis = await page.evaluate(() => ({ bnav: getComputedStyle(document.querySelector(".bnav")).display, mbar: getComputedStyle(document.querySelector(".mbar")).display }));
        if (vis.bnav !== "none") fail(`${where("/deploy")}: bottom nav should be replaced by the step bar`);
        if (vis.mbar === "none") fail(`${where("/deploy")}: step action bar missing on phones`);
      }

      // The "Lainnya" sheet and the mobile chrome.
      await page.goto(`${srv.url}/#/apps`);
      await page.waitForSelector(".acard");
      if (width <= 760) {
        const d = await page.evaluate(() => ({ side: getComputedStyle(document.querySelector(".side")).display, bnav: getComputedStyle(document.querySelector(".bnav")).display }));
        if (d.side !== "none" || d.bnav === "none") fail(`${where("/apps")}: phone chrome wrong ${JSON.stringify(d)}`);
        await page.click(".bn-more");
        await page.waitForSelector(".msheet:not([hidden])");
        await measure(where("Lainnya sheet"));
        await page.keyboard.press("Escape");
      } else {
        const d = await page.evaluate(() => ({ side: getComputedStyle(document.querySelector(".side")).display, bnav: getComputedStyle(document.querySelector(".bnav")).display }));
        if (d.side === "none" || d.bnav !== "none") fail(`${where("/apps")}: desktop chrome wrong ${JSON.stringify(d)}`);
      }

      // Keyboard focus is visible.
      await page.goto(`${srv.url}/#/apps`);
      await page.waitForSelector(".acard");
      await page.locator("body").press("Tab");
      const seen = new Set();
      for (let i = 0; i < 6; i++) {
        const o = await page.evaluate(() => {
          const el = document.activeElement;
          if (!el || el === document.body) return null;
          // a search field inside a .filter box shows its ring on the box
          const host = el.closest(".filter:focus-within") || el;
          const cs = getComputedStyle(host);
          return { tag: el.tagName, cls: el.className, outline: cs.outlineStyle, w: parseFloat(cs.outlineWidth), shadow: cs.boxShadow };
        });
        if (o && !(o.outline !== "none" && o.w >= 2) && o.shadow === "none") fail(`${where("focus")}: ${o.tag}.${o.cls} has no visible focus indicator`);
        if (o) seen.add(`${o.tag}.${o.cls}`);
        await page.keyboard.press("Tab");
      }
      if (seen.size < 3) fail(`${where("focus")}: Tab reached only ${seen.size} controls`);

      for (const p of problems) fail(`${width} ${theme}: ${p}`);
      await ctx.close();
    }
  }
} finally {
  await browser.close();
  const logs = srv.logs();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
  if (failures.length) {
    const uniq = [...new Set(failures)];
    console.log(`${uniq.length} failures:\n` + uniq.join("\n"));
    console.log(logs.split("\n").slice(-8).join("\n"));
    process.exitCode = 1;
  }
}
if (!process.exitCode) console.log(`smoke-layout: ok (${WIDTHS.length} widths x ${THEMES.length} themes)`);
