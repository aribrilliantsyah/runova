// Seeds a fake-backend server with a few apps and writes docs/screenshots/*.png from the real panel:
//   node web/test/screenshots.mjs
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, buildStaticBinary, buildMarkedBinary, login, repo, check } from "./lib.mjs";

const out = path.join(repo, "docs/screenshots");
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "shots-"));
const srv = await startRunova();
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
  await call("PATCH", `/api/users/${me.id}`, { display_name: "Budi Santoso", email: "budi@perusahaan.co.id", quota: { apps: 8, memory_mb: 3072, cpu_milli: 3000, databases: 3, pg_connections: 20 } }, cookie);
  const settings = (await call("GET", "/api/settings", undefined, cookie)).body;
  settings.public_ip = "203.0.113.15";
  settings.upload_limit_mb = 200;
  await call("PUT", "/api/settings", settings, cookie);

  const jar = buildSampleJar(tmp, { name: "billing-api-2.4.1.jar" });
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

  await shot("desktop-apps.png", { width: 1440, height: 900, scheme: "light", route: "/apps", wait: ".acard:has-text('notif-relay')" });
  await shot("desktop-app-dark.png", {
    width: 1440, height: 900, scheme: "dark", route: `/apps/${billing}`,
    wait: ".cols pre.log .ln:has-text('Listening')",
    after: (p) => p.waitForSelector(".link:has-text('billing.perusahaan.co.id')"),
  });
  const deployAfter = (step) => async (p) => {
    await p.setInputFiles("#upload-file", jar);
    await p.waitForSelector(".fileok");
    if (step > 1) {
      const w = p.viewportSize().width;
      if (w > 760) await p.click(`.steps button[data-step='${step}']`);
      else for (let i = 1; i < step; i++) await p.click(".mbar button:has-text('Lanjut')");
    }
  };
  const addressAfter = async (p) => {
    await deployAfter(4)(p);
    await p.fill(".addr input[aria-label='Nama alamat']", "cv-budi");
    await p.waitForSelector(".addr .astate.ok");
  };
  await shot("desktop-server-hosting.png", {
    width: 1440, height: 1000, scheme: "light", route: "/server", wait: "#hostrows tr",
    after: async (p) => {
      await p.locator("#hostingcard").scrollIntoViewIfNeeded();
      await p.fill("#hostname", "*.baru.perusahaan.co.id");
      await p.waitForSelector("#hostdns");
    },
  });
  await shot("desktop-address-dark.png", { width: 1440, height: 900, scheme: "dark", route: "/deploy", wait: ".drop", after: addressAfter });
  await shot("mobile-address.png", { width: 390, height: 844, scheme: "light", route: "/deploy", wait: ".drop", after: addressAfter });
  await shot("mobile-server-hosting.png", {
    width: 390, height: 844, scheme: "dark", route: "/server", wait: "#hostrows tr",
    after: (p) => p.locator("#hostingcard").scrollIntoViewIfNeeded(),
  });
  await shot("desktop-deploy.png", { width: 1440, height: 900, scheme: "light", route: "/deploy", wait: ".drop", after: deployAfter(2) });
  await shot("mobile-apps.png", { width: 390, height: 844, scheme: "light", route: "/apps", wait: ".acard:has-text('notif-relay')" });
  await shot("mobile-deploy.png", { width: 390, height: 844, scheme: "light", route: "/deploy", wait: ".drop", after: deployAfter(2) });
} finally {
  await browser.close();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
}
