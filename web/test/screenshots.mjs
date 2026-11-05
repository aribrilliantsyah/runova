// Seeds a fake-backend server with a few apps and writes docs/screenshots/*.png from the real panel:
//   node web/test/screenshots.mjs
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, buildStaticBinary, buildMarkedBinary, login, repo, check } from "./lib.mjs";

const out = path.join(repo, "docs/screenshots");
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "shots-"));
const srv = await startRunova({ env: { RUNOVA_DEV_SEED: "1" } });
const browser = await chromium.launch();

async function call(method, p, body, cookie) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null, headers: r.headers };
}
async function upload(file, name, cookie) {
  const form = new FormData();
  form.append("file", new Blob([fs.readFileSync(file)]), name);
  return (await fetch(`${srv.url}/api/uploads`, { method: "POST", headers: { cookie }, body: form })).json();
}

try {
  const lr = await call("POST", "/api/login", { username: "admin", password: srv.adminPassword });
  const cookie = lr.headers.get("set-cookie").split(";")[0];
  const me = (await call("GET", "/api/me", undefined, cookie)).body;
  await call("PATCH", `/api/users/${me.id}`, { display_name: "Budi Santoso", email: "budi@perusahaan.co.id", quota: { apps: 8, memory_mb: 3072, cpu_milli: 3000, databases: 3, pg_connections: 20, disk_mb: 8192 } }, cookie);
  const settings = (await call("GET", "/api/settings", undefined, cookie)).body;
  settings.public_ip = "203.0.113.15";
  settings.upload_limit_mb = 200;
  settings.port_min = Number(process.env.SHOT_PORT_MIN || 20000); // move off ports another local stack may hold
  settings.port_max = settings.port_min + 999;
  settings.update_check = false; // the banner only belongs in the two update shots
  await call("PUT", "/api/settings", settings, cookie);

  const jar = buildSampleJar(tmp, { name: "billing-api-2.4.1.jar", extraProps: "spring.servlet.multipart.location=/app/storage\nbilling.export-dir=./exports\n" });
  const bin = buildStaticBinary(tmp, "auth-gateway");
  const rustBin = buildMarkedBinary(tmp, "report-worker", "rustc version 1.90.0 (1159e78c4 2025-09-14)");
  const unknownBin = buildMarkedBinary(tmp, "notif-relay", "built by something unrecognisable");
  const kotlinJar = buildSampleJar(tmp, { name: "legacy-hr.jar", extraFiles: { "kotlin/Unit.class": "x" } });
  const db1 = (await call("POST", "/api/databases", { suffix: "billing" }, cookie)).body;
  await call("POST", "/api/databases", { suffix: "auth" }, cookie);

  const make = async (file, fileName, body) => {
    const up = await upload(file, fileName, cookie);
    const r = await call("POST", "/api/apps", { upload_id: up.id, ...body }, cookie);
    check(r.status === 201, `create ${body.name}: ${r.status} ${JSON.stringify(r.body)}`);
    return r.body.app.id;
  };
  const billing = await make(jar, "billing-api-2.4.1.jar", {
    name: "billing-api", java_version: 21, memory_mb: 512, cpu_milli: 500, ip_access: true, database_id: db1.id,
    env: [{ name: "JWT_SECRET", value: "x", secret: true }, { name: "DB_PASSWORD", value: "x", secret: true }, { name: "SPRING_PROFILES_ACTIVE", value: "prod", secret: false }],
  });
  const gateway = await make(bin, "auth-gateway", { name: "auth-gateway", memory_mb: 128, cpu_milli: 250, ip_access: false, env: [] });
  const worker = await make(rustBin, "report-worker", { name: "report-worker", memory_mb: 256, cpu_milli: 250, ip_access: true, env: [] });
  const legacy = await make(kotlinJar, "legacy-hr.jar", { name: "legacy-hr", java_version: 17, memory_mb: 256, cpu_milli: 250, ip_access: false, env: [{ name: "JWT_SECRET", value: "x", secret: true }, { name: "DB_PASSWORD", value: "x", secret: true }] });
  const notif = await make(unknownBin, "notif-relay", { name: "notif-relay", memory_mb: 128, cpu_milli: 100, ip_access: false, env: [] });
  const hosting = (await call("POST", "/api/hosting-domains", { name: "*.porto.perusahaan.co.id", reserved: "blog" }, cookie)).body;
  await call("POST", "/api/hosting-domains", { name: "apps.perusahaan.co.id" }, cookie);
  await call("POST", `/api/apps/${notif}/domains`, { hosting_domain_id: hosting.id, label: "notif-relay" }, cookie);
  await call("POST", `/api/apps/${worker}/domains`, { hosting_domain_id: hosting.id, label: "laporan-bulanan" }, cookie);
  await call("POST", `/api/apps/${billing}/domains`, { host: "billing.perusahaan.co.id" }, cookie);
  await call("POST", `/api/apps/${gateway}/domains`, { host: "sso.perusahaan.co.id" }, cookie);
  await call("POST", `/api/apps/${legacy}/domains`, { host: "hr-lama.perusahaan.co.id" }, cookie);
  for (const id of [billing, gateway, worker, legacy, notif]) {
    for (let i = 0; i < 80; i++) {
      const a = await call("GET", `/api/apps/${id}`, undefined, cookie);
      if (a.body.app.status === "running") break;
      await new Promise((r) => setTimeout(r, 250));
    }
  }
  await call("POST", `/api/apps/${notif}/stop`, {}, cookie);
  await call("POST", "/api/users", { username: "sari", display_name: "Sari Wulandari", password: "sari-password-1", role: "user" }, cookie);

  const shot = async (name, { width, height, scheme, route, wait, after, fullPage = false }) => {
    const ctx = await browser.newContext({ viewport: { width, height }, colorScheme: scheme, deviceScaleFactor: width < 600 ? 2 : 1 });
    const page = await ctx.newPage();
    await login(page, srv.url, "admin", srv.adminPassword);
    await page.goto(`${srv.url}/#${route}`);
    await page.waitForSelector(wait, { timeout: 15000 });
    await after?.(page);
    await page.waitForTimeout(600);
    await page.screenshot({ path: path.join(out, name), fullPage });
    console.log(`wrote ${name}`);
    await ctx.close();
  };

  // A few more versions so the deploy timeline has history, and files in the workspace.
  for (const v of [2, 3]) {
    const up = await upload(jar, "billing-api-2.4.1.jar", cookie);
    await call("POST", `/api/apps/${billing}/versions`, { upload_id: up.id }, cookie);
    for (let i = 0; i < 80; i++) {
      const deps = (await call("GET", `/api/apps/${billing}/deployments`, undefined, cookie)).body;
      if (deps.length >= v && deps[0].status === "running") break;
      await new Promise((r) => setTimeout(r, 250));
    }
  }
  const deps = (await call("GET", `/api/apps/${billing}/deployments`, undefined, cookie)).body;
  const ws = path.join(srv.data, "data", "admin", "billing-api");
  fs.mkdirSync(path.join(ws, "storage/invoices"), { recursive: true });
  fs.mkdirSync(path.join(ws, "exports"), { recursive: true });
  fs.mkdirSync(path.join(ws, "logs"), { recursive: true });
  fs.writeFileSync(path.join(ws, "storage/invoices/INV-2025-0001.pdf"), Buffer.alloc(184 * 1024));
  fs.writeFileSync(path.join(ws, "exports/laporan-oktober.csv"), "id,pelanggan,total\n1,PT Maju Jaya,1250000\n");
  fs.writeFileSync(path.join(ws, "exports/laporan-september.csv"), "id,pelanggan,total\n1,CV Sentosa,830000\n");
  fs.writeFileSync(path.join(ws, "config.yml"), "mata_uang: IDR\nzona_waktu: Asia/Jakarta\n");
  fs.writeFileSync(path.join(ws, "logs/app.log"), "2025-11-03 08:00:01 INFO Listening on :8080\n");
  fs.writeFileSync(path.join(ws, "logo-perusahaan.png"), Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4AWP4z8DwnwEIGBgYGBgYAAA3AQEbx1ewAAAAAElFTkSuQmCC", "base64"));

  const withBanner = async (on) => {
    const cur = (await call("GET", "/api/settings", undefined, cookie)).body;
    await call("PUT", "/api/settings", { ...cur, update_check: on }, cookie);
  };
  await withBanner(true);
  await shot("desktop-update-banner.png", { width: 1440, height: 900, scheme: "light", route: "/apps", wait: ".app > .main > .banner.upd:not([hidden])" });
  await withBanner(false);

  const pickDb = (p) => p.click("button[data-conn='admin_billing']");
  const runQuery = (sql) => async (p) => {
    await pickDb(p);
    await p.waitForSelector("#dbTabQuery");
    await p.click("#dbTabQuery");
    await p.fill("#sqlEditor", sql);
    await p.click("#runQuery");
    await p.waitForSelector("#queryResult table.grid");
    if (p.viewportSize().width < 600) await p.evaluate(() => document.querySelector("#runQuery").scrollIntoView({ block: "start" }));
  };
  // The dev host's own name and fake versions would show on the Server page; say what a real server says.
  const tidyServer = async (p) => {
    await p.evaluate(() => {
      const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      for (let n; (n = walk.nextNode()); ) {
        if (/linux\/amd64/.test(n.data) && n.data.includes("·")) n.data = "srv-kantor-01 · Ubuntu 26.04 · linux/amd64";
        else if (n.data.trim() === "fake") n.data = "29.1.3";
        else if (/^v\d+\.\d+\.\d+$/.test(n.data.trim())) n.data = "v0.3.10";
      }
    });
  };
  const deployAfter = async (p) => {
    await p.setInputFiles("#upload-file", jar);
    await p.waitForSelector(".fileok");
    await p.fill("#nm", "laporan-keuangan");
    await p.fill("[data-env='JWT_SECRET'] input", "x".repeat(32));
    await p.fill("[data-env='DB_PASSWORD'] input", "s3cret");
    await p.locator(".drop, .fileok").first().scrollIntoViewIfNeeded();
    await p.evaluate(() => document.querySelectorAll("*").forEach((e) => { if (e.scrollTop) e.scrollTop = 0; }));
  };

  await shot("desktop-apps.png", { width: 1440, height: 900, scheme: "light", route: "/apps", wait: ".acard:has-text('notif-relay')" });
  await shot("desktop-app-dark.png", {
    width: 1440, height: 900, scheme: "dark", route: `/apps/${billing}`,
    wait: ".cols pre.log .ln:has-text('Listening')",
    after: (p) => p.waitForSelector(".link:has-text('billing.perusahaan.co.id')"),
  });
  await shot("desktop-deployments.png", { width: 1440, height: 900, scheme: "light", route: `/apps/${billing}/deployments/${deps[1].id}`, wait: ".dep-grid .tl-ok, .dep-grid [data-step]" });
  await shot("desktop-deploy.png", { width: 1440, height: 1000, scheme: "light", route: "/deploy", wait: ".drop", after: deployAfter });
  await shot("desktop-files.png", {
    width: 1440, height: 900, scheme: "light", route: `/apps/${billing}/files`, wait: "tr[data-file='storage']",
    after: (p) => p.waitForSelector("tr[data-file='exports']"),
  });
  await shot("desktop-database-tables.png", {
    width: 1440, height: 900, scheme: "light", route: "/databases", wait: ".dbcard[data-db='admin_billing']",
    after: async (p) => {
      await pickDb(p);
      await p.waitForSelector("#tableList .trow[data-table='invoices']");
      await p.click(".trow[data-table='invoices']");
      await p.waitForSelector("#tableDetail table.grid tbody tr");
    },
  });
  await shot("desktop-database-query-dark.png", {
    width: 1440, height: 900, scheme: "dark", route: "/databases", wait: ".dbcard[data-db='admin_billing']",
    after: runQuery("-- semua tagihan\nSELECT * FROM invoices;"),
  });
  await shot("desktop-activity.png", { width: 1440, height: 900, scheme: "light", route: "/activity", wait: "#actrows tr.act-row" });
  // Domain hosting is the subject; the stat cards above it would show the dev seed's oversubscribed totals.
  await shot("desktop-server-hosting.png", {
    width: 1440, height: 800, scheme: "light", route: "/server", wait: "#hostrows tr",
    after: async (p) => {
      await tidyServer(p);
      await p.fill("#hostname", "*.baru.perusahaan.co.id");
      await p.waitForSelector("#hostdns");
      await p.evaluate(() => document.querySelector("#hostingcard").scrollIntoView({ block: "start" }));
    },
  });
  await shot("mobile-apps.png", { width: 390, height: 844, scheme: "light", route: "/apps", wait: ".acard:has-text('notif-relay')" });
  await shot("mobile-app-dark.png", { width: 390, height: 844, scheme: "dark", route: `/apps/${billing}`, wait: ".link:has-text('billing.perusahaan.co.id')" });
  await shot("mobile-deploy.png", { width: 390, height: 844, scheme: "light", route: "/deploy", wait: ".drop", after: deployAfter });
  await shot("mobile-database-query.png", {
    width: 390, height: 844, scheme: "light", route: "/databases", wait: ".dbcard[data-db='admin_billing']",
    after: runQuery("SELECT * FROM invoices;"),
  });
} finally {
  await browser.close();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
}
