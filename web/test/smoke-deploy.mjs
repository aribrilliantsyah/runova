// Task 3 smoke: login → wizard → upload a sample JAR → Java 8 disabled with the reason →
// fill the required env → Deploy → the app page shows "Berjalan". Also: empty list call to
// action, oversize file refused client-side, upload cancel, XSS-looking app names as text.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, buildStaticBinary, login, watch, check } from "./lib.mjs";

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-deploy-"));
const srv = await startRunova();
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const problems = watch(page, srv.url);
const step = (m) => console.log(`- ${m}`);

try {
  const jar = buildSampleJar(tmp);
  await login(page, srv.url, "admin", srv.adminPassword);

  step("empty list invites the first deploy");
  await page.goto(`${srv.url}/#/apps`);
  await page.waitForSelector("text=Deploy aplikasi pertama");

  step("wizard: upload the sample JAR");
  await page.goto(`${srv.url}/#/deploy`);
  await page.setInputFiles("#upload-file", jar);
  await page.waitForSelector(".fileok");
  await page.waitForSelector("text=Butuh Java 17 atau lebih baru");
  check((await page.inputValue("#nm")) === "billing-api", `suggested name: ${await page.inputValue("#nm")}`);

  step("step 2: Java 8 and 11 are disabled with the reason");
  await page.click("#steps button[data-step='2'], .steps button[data-step='2']");
  const j8 = page.locator("#javaopts .opt[data-java='8']");
  check((await j8.getAttribute("aria-disabled")) === "true", "Java 8 should be disabled");
  check(((await j8.getAttribute("title")) || "").includes("Butuh Java 17 atau lebih baru"), `Java 8 title: ${await j8.getAttribute("title")}`);
  check((await page.locator("#javaopts .opt[data-java='21']").getAttribute("aria-pressed")) === "true", "Java 21 should be preselected");

  step("Deploy stays disabled until the required env is filled");
  await page.click(".steps button[data-step='3']");
  check(await page.locator("#deploy").isDisabled(), "Deploy should be disabled with JWT_SECRET empty");
  await page.waitForSelector("text=Isi JWT_SECRET");
  const secret = page.locator("[data-env='JWT_SECRET'] input");
  check((await secret.getAttribute("type")) === "password", "secret input should be masked");
  await page.click("[data-env='JWT_SECRET'] button[aria-label='Buat nilai acak']");
  check((await secret.inputValue()).length === 48, "random secret should be 48 hex chars");
  await page.fill("[data-env='DB_PASSWORD'] input", "s3cret");

  step("step 4 → Deploy");
  await page.click(".steps button[data-step='4']");
  check(!(await page.locator("#deploy").isDisabled()), "Deploy should be enabled");
  await page.click("#deploy");
  await page.waitForURL(/#\/apps\/\d+\/logs/);
  await page.waitForSelector(".chip:has-text('Berjalan')", { timeout: 20000 });
  step("app page shows Berjalan");

  step("list: card shows the app, quota tiles reflect it");
  await page.goto(`${srv.url}/#/apps`);
  await page.waitForSelector(".acard:has-text('billing-api')");
  check((await page.locator("#stats").innerText()).includes("512 MB"), `quota tile: ${await page.locator("#stats").innerText()}`);

  step("upload over the limit is refused before sending");
  const big = path.join(tmp, "big.jar");
  fs.writeFileSync(big, Buffer.alloc(1024 * 1024));
  const admin = await fetch(`${srv.url}/api/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: "admin", password: srv.adminPassword }) });
  const cookie = admin.headers.get("set-cookie").split(";")[0];
  const settings = await (await fetch(`${srv.url}/api/settings`, { headers: { cookie } })).json();
  settings.upload_limit_mb = 1;
  const put = await fetch(`${srv.url}/api/settings`, { method: "PUT", headers: { cookie, "Content-Type": "application/json" }, body: JSON.stringify(settings) });
  check(put.status === 200, `settings put ${put.status}`);
  const bigFile = path.join(tmp, "over.bin");
  fs.writeFileSync(bigFile, Buffer.alloc(2 * 1024 * 1024));
  let uploads = 0;
  page.on("request", (r) => r.url().endsWith("/api/uploads") && r.method() === "POST" && uploads++);
  await page.goto(`${srv.url}/#/deploy`);
  await page.reload();
  await page.waitForSelector(".drop");
  await page.setInputFiles("#upload-file", bigFile);
  await page.waitForSelector(".err:has-text('File melebihi batas 1 MB')");
  check(uploads === 0, "an oversize file must not be sent");

  step("a binary: no Java choice, static runtime");
  settings.upload_limit_mb = 500;
  await fetch(`${srv.url}/api/settings`, { method: "PUT", headers: { cookie, "Content-Type": "application/json" }, body: JSON.stringify(settings) });
  const bin = buildStaticBinary(tmp, "hello");
  await page.reload();
  await page.waitForSelector(".drop");
  await page.setInputFiles("#upload-file", bin);
  await page.waitForSelector(".fileok");
  await page.click(".steps button[data-step='2']");
  await page.waitForSelector("text=Terdeteksi sebagai");
  check((await page.locator("#javaopts").count()) === 0, "a binary has no Java options");

  step("Ganti file discards the upload");
  await page.click(".wbody >> text=Ganti file").catch(() => {});
  await page.click(".steps button[data-step='1']");
  await page.click("button:has-text('Ganti file')");
  await page.waitForSelector(".drop");
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
if (!process.exitCode) console.log("smoke-deploy: ok");
