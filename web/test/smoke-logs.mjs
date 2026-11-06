// Log viewer smoke: a busy app streams 20 000 lines through the fake backend while the Log tab
// follows it; long tasks (> 50 ms) are counted with PerformanceObserver during the stream and
// while scrolling. Then: scrolling up stops following and shows "N baris baru", search, level
// filter, folded stack traces, line selection, the restart marker of an app the restart policy
// keeps starting again (live tab), and the failed OOM deploy's detail page. With SHOTS=dir it
// writes screenshots at 1440, 768 and 360 wide in light and dark.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, login, watch, check } from "./lib.mjs";

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-logs-"));
const shots = process.env.SHOTS || "";
if (shots) fs.mkdirSync(shots, { recursive: true });
const FLOOD = 20000;
const srv = await startRunova();
const browser = await chromium.launch();
const step = (m) => console.log(`- ${m}`);
const problems = [];

async function api(method, p, body, cookie) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null };
}

// One observer for the page's life; each measurement takes what it gathered since the last one.
const longTasks = () => {
  if (window.__lt) {
    window.__lt.length = 0;
    return;
  }
  window.__lt = [];
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) window.__lt.push(Math.round(e.duration));
  }).observe({ type: "longtask", buffered: false });
};
try {
  const lr = await api("POST", "/api/login", { username: "admin", password: srv.adminPassword });
  const cookie = (await fetch(`${srv.url}/api/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: "admin", password: srv.adminPassword }) })).headers.get("set-cookie").split(";")[0];
  check(lr.status === 200, "login");
  const me = (await api("GET", "/api/me", undefined, cookie)).body;
  await api("PATCH", `/api/users/${me.id}`, { quota: { apps: 5, memory_mb: 4096, databases: 3, pg_connections: 20, disk_mb: 8192 } }, cookie);
  const jar = buildSampleJar(tmp);
  const make = async (name, env, memory = 512) => {
    const form = new FormData();
    form.append("file", new Blob([fs.readFileSync(jar)]), `${name}.jar`);
    const up = await (await fetch(`${srv.url}/api/uploads`, { method: "POST", headers: { cookie }, body: form })).json();
    const r = await api("POST", "/api/apps", { upload_id: up.id, name, java_version: 21, memory_mb: memory, env: [{ name: "JWT_SECRET", value: "x", secret: true }, { name: "DB_PASSWORD", value: "x", secret: true }, ...env] }, cookie);
    check(r.status === 201, `create ${name}: ${r.status} ${JSON.stringify(r.body)}`);
    return r.body.app.id;
  };
  const busy = await make("starboom", [{ name: "LOG_FLOOD", value: String(FLOOD) }]);
  const looping = await make("galeri", [{ name: "RUNOVA_FAKE_FAIL", value: "restartlive" }]);
  const oom = await make("hemat", [{ name: "RUNOVA_FAKE_FAIL", value: "restartloop" }], 256);

  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  problems.push(watch(page, srv.url));
  await login(page, srv.url, "admin", srv.adminPassword);

  step(`stream ${FLOOD} lines into the Log tab while following; count long tasks`);
  await page.goto(`${srv.url}/#/apps/${busy}/logs`);
  await page.waitForSelector("#biglog .ln");
  await page.evaluate(longTasks);
  const t0 = Date.now();
  await page.waitForSelector("#biglog .ln:has-text('item 19999 not found')", { timeout: 60000 });
  const streamMs = Date.now() - t0;
  const ltStream = await page.evaluate(() => window.__lt.splice(0));
  const atBottom = await page.evaluate(() => {
    const b = document.querySelector("#biglog");
    return b.scrollHeight - b.scrollTop - b.clientHeight < 24;
  });
  check(atBottom, "following keeps the view at the tail");
  const domRows = await page.locator("#biglog .ln").count();
  check(domRows < 200, `only the rows in view are in the DOM (${domRows})`);

  step("scroll the whole log; count long tasks");
  await page.evaluate(longTasks);
  await page.evaluate(async () => {
    const b = document.querySelector("#biglog");
    for (let y = b.scrollHeight; y > 0; y -= 900) {
      b.scrollTop = y;
      await new Promise((r) => requestAnimationFrame(r));
    }
  });
  const ltScroll = await page.evaluate(() => window.__lt.splice(0));
  console.log(`  perf: ${FLOOD} lines streamed in ${streamMs} ms; long tasks while streaming: ${ltStream.length} ${JSON.stringify(ltStream)}; while scrolling: ${ltScroll.length} ${JSON.stringify(ltScroll)}; DOM rows ${domRows}`);
  fs.writeFileSync(path.join(os.tmpdir(), "runova-logs-perf.json"), JSON.stringify({ lines: FLOOD, streamMs, ltStream, ltScroll, domRows }));
  // The target is no task over 50 ms. A shared CI or dev machine adds GC pauses and preemption
  // the viewer does not control, so the check fails on a pattern, not on one stray pause.
  check(ltScroll.length <= 3 && ltScroll.every((d) => d < 120), `long tasks while scrolling: ${ltScroll}`);
  check(ltStream.length <= 3 && ltStream.every((d) => d < 120), `long tasks while streaming: ${ltStream}`);

  step("scrolled up, the view stays put and offers the new lines");
  await page.goto(`${srv.url}/#/apps/${looping}/logs`);
  await page.waitForSelector("#biglog .ln:has-text('Hibernate ORM core')", { timeout: 15000 });
  await page.evaluate(() => (document.querySelector("#biglog").scrollTop = 0));
  await page.waitForSelector(".lv-jump:not([hidden])", { timeout: 15000 });
  check(/baris baru/.test(await page.locator(".lv-jump").innerText()), "jump button names the new lines");
  check(await page.evaluate(() => document.querySelector("#biglog").scrollTop === 0), "view jumped while the user read above");

  step("the restart policy starting the app again shows a marker");
  await page.waitForSelector("#biglog .lv-restart", { timeout: 15000 });
  await page.click(".lv-jump");
  const marker = await page.locator("#biglog .lv-restart").last().innerText();
  check(/Container dimulai ulang \d\d:\d\d:\d\d/.test(marker), `marker: ${marker}`);
  check(await page.locator(".lv-jump").isHidden(), "jump hides at the tail");

  step("deploy that loops is failed with the OOM diagnosis");
  await page.waitForFunction(async (id) => {
    const r = await fetch(`/api/apps/${id}/deployments`);
    const l = await r.json();
    return l[0]?.status === "failed";
  }, oom, { timeout: 30000, polling: 500 });
  const deps = (await api("GET", `/api/apps/${oom}/deployments`, undefined, cookie)).body;
  check(deps[0].problem?.title === "Memori habis (OOMKilled)", `problem ${JSON.stringify(deps[0].problem)}`);
  await page.goto(`${srv.url}/#/apps/${oom}/deployments/${deps[0].id}`);
  await page.waitForSelector(".pcard:has-text('Memori habis (OOMKilled)')");
  await page.waitForSelector(".tl-fail, [data-step='health']");

  step("search, level filter, stack traces, selection on the busy log");
  await page.goto(`${srv.url}/#/apps/${busy}/logs`);
  await page.waitForSelector("#biglog .ln");
  await page.fill("input[aria-label='Cari di log']", "item 19999 not found");
  await page.waitForSelector("#biglog mark.on");
  check((await page.locator(".lv-count").innerText()).startsWith("1/"), "count");
  await page.fill("input[aria-label='Cari di log']", "");
  await page.click(".lv-lvbtn");
  for (const lv of ["info", "debug", "trace", "none"]) await page.uncheck(`.lv-menu input[value='${lv}']`);
  await page.keyboard.press("Escape");
  check((await page.locator("#biglog .ln.lvl-info").count()) === 0, "info lines hidden");
  await page.click(".lv-lvbtn");
  for (const lv of ["info", "debug", "trace", "none"]) await page.check(`.lv-menu input[value='${lv}']`);
  await page.keyboard.press("Escape");
  const fold = page.locator("#biglog .lv-fold").first();
  check((await fold.innerText()).includes("2 baris stack trace"), `fold label: ${await fold.innerText()}`);
  check((await page.locator("#biglog .ln.fr").count()) === 0, "frames folded by default");
  await fold.click();
  check((await page.locator("#biglog .ln.fr").count()) === 2, "frames shown after unfolding");

  if (shots) {
    step(`screenshots in ${shots}`);
    for (const scheme of ["light", "dark"]) {
      for (const [w, hgt] of [[1440, 900], [768, 1024], [360, 780]]) {
        const c = await browser.newContext({ viewport: { width: w, height: hgt }, colorScheme: scheme });
        const p = await c.newPage();
        problems.push(watch(p, srv.url));
        await login(p, srv.url, "admin", srv.adminPassword);
        const tag = `${w}-${scheme}`;
        await p.goto(`${srv.url}/#/apps/${looping}/logs`);
        // Stopped by the crash monitor by now: the saved log still marks every run.
        await p.waitForSelector("#biglog .ln", { timeout: 20000 });
        await p.waitForTimeout(600);
        // Walk up from the tail until a restart marker is in view, then put it near the top.
        await p.evaluate(async () => {
          const b = document.querySelector("#biglog");
          for (let y = b.scrollHeight; y > 0; y -= b.clientHeight / 2) {
            b.scrollTop = y;
            await new Promise((r) => setTimeout(r, 60));
            const m = [...b.querySelectorAll(".lv-restart")].pop();
            if (m) {
              b.scrollTop += m.getBoundingClientRect().top - b.getBoundingClientRect().top - b.clientHeight * 0.3;
              return;
            }
          }
        });
        await p.waitForSelector("#biglog .lv-restart", { timeout: 10000 });
        await p.waitForTimeout(300);
        await p.screenshot({ path: path.join(shots, `log-live-restart-${tag}.png`) });
        await p.goto(`${srv.url}/#/apps/${busy}/logs`);
        await p.waitForSelector("#biglog .ln");
        await p.fill("input[aria-label='Cari di log']", "not found");
        await p.waitForSelector("#biglog mark.on");
        await p.screenshot({ path: path.join(shots, `log-search-${tag}.png`) });
        await p.fill("input[aria-label='Cari di log']", "");
        await p.click(".lv-lvbtn");
        await p.waitForTimeout(150);
        await p.screenshot({ path: path.join(shots, `log-level-menu-${tag}.png`) });
        await p.keyboard.press("Escape");
        await p.evaluate(() => {
          const b = document.querySelector("#biglog");
          b.scrollTop = b.scrollHeight / 2;
        });
        await p.waitForTimeout(200);
        const f = p.locator("#biglog .lv-fold").first();
        if (await f.count()) await f.click();
        await p.waitForTimeout(150);
        await p.screenshot({ path: path.join(shots, `log-stack-${tag}.png`) });
        await p.goto(`${srv.url}/#/apps/${oom}/deployments/${deps[0].id}`);
        await p.waitForSelector(".pcard");
        await p.waitForTimeout(300);
        await p.screenshot({ path: path.join(shots, `deploy-oom-${tag}.png`), fullPage: true });
        await c.close();
      }
    }
  }
} finally {
  await browser.close();
  const logs = srv.logs();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
  const all = problems.flat();
  if (all.length) {
    console.log("problems:\n" + all.join("\n"));
    process.exitCode = 1;
  }
  if (process.exitCode) console.log(logs.split("\n").slice(-15).join("\n"));
}
if (!process.exitCode) console.log("smoke-logs: ok");
