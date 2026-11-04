// Database env smoke: a .tar.gz whose .env.example asks for DB_URL, DB_USER, DB_PASSWORD and REDIS_URL.
// Choosing a database locks the DB_* rows as "Otomatis · dari <db>" (not required, no password typed);
// REDIS_URL stays required. After deploy the Environment tab shows the DB_* rows locked, "Isi sendiri"
// overrides one, and the app detail API never carries the database password.
// Screenshots go to RUNOVA_SHOTS (default /tmp/runova-pg-env).
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { chromium, startRunova, buildStaticBinary, login, watch, check } from "./lib.mjs";

const shots = process.env.RUNOVA_SHOTS || "/tmp/runova-pg-env";
fs.mkdirSync(shots, { recursive: true });
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-dbenv-"));
const srv = await startRunova();
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const problems = watch(page, srv.url);
const step = (m) => console.log(`- ${m}`);

try {
  buildStaticBinary(tmp, "server");
  fs.writeFileSync(path.join(tmp, ".env.example"), "DB_URL=\nDB_USER=\nDB_PASSWORD=\nREDIS_URL=\n");
  const tgz = path.join(tmp, "billing.tar.gz");
  execFileSync("tar", ["czf", tgz, "-C", tmp, "server", ".env.example"]);
  await login(page, srv.url, "admin", srv.adminPassword);

  step("wizard: upload the tar.gz, without a database the review page asks for everything");
  await page.goto(`${srv.url}/#/deploy`);
  await page.setInputFiles("#upload-file", tgz);
  await page.waitForSelector(".fileok");
  await page.waitForSelector(".dmiss button:has-text('Isi DB_PASSWORD')");
  await page.waitForSelector(".dmiss button:has-text('Isi REDIS_URL')");

  step("choose a new database: DB_* lock, REDIS_URL stays required");
  await page.click(".dbopt[data-db='new']");
  await page.fill("input[aria-label='Nama database baru']", "billing");
  for (const n of ["DB_URL", "DB_USER", "DB_PASSWORD"]) {
    const row = page.locator(`[data-env='${n}']`);
    check((await row.locator(".autoval").count()) === 1, `${n} should be filled automatically`);
    check((await row.locator("input").count()) === 0, `${n} should have no field to type in`);
    check((await row.innerText()).includes("admin_billing"), `${n} should name the database: ${await row.innerText()}`);
  }
  check((await page.locator("[data-env='REDIS_URL'] input").count()) === 1, "REDIS_URL should stay editable");
  check((await page.locator(".dmiss").innerText()).includes("Isi REDIS_URL"), "REDIS_URL should still be required");
  check(!(await page.locator(".dmiss").innerText()).includes("DB_PASSWORD"), "DB_PASSWORD must not be required");
  const hostTitle = await page.locator("[data-env='DB_URL'] .autoval").getAttribute("title");
  check((hostTitle || "").includes("localhost"), `DB_URL tooltip explains the local host: ${hostTitle}`);
  await page.screenshot({ path: path.join(shots, "wizard-db-env.png"), fullPage: true });

  step("override DB_USER, then return it to automatic");
  await page.click("[data-env='DB_USER'] button:has-text('Isi sendiri')");
  check((await page.locator("[data-env='DB_USER'] input").count()) === 1, "override shows a field");
  await page.click("[data-env='DB_USER'] button[aria-label='Pakai nilai otomatis untuk DB_USER']");
  check((await page.locator("[data-env='DB_USER'] .autoval").count()) === 1, "back to automatic");

  step("fill REDIS_URL and deploy");
  await page.fill("[data-env='REDIS_URL'] input", "redis://cache:6379");
  check(!(await page.locator("#deploy").isDisabled()), `Deploy should be enabled: ${await page.locator(".dmiss").innerText().catch(() => "")}`);
  let sent = null;
  page.on("request", (r) => {
    if (r.url().endsWith("/api/apps") && r.method() === "POST") sent = JSON.parse(r.postData());
  });
  await page.click("#deploy");
  await page.waitForURL(/#\/apps\/\d+\/deployments\/\d+/);
  const names = (sent?.env || []).map((e) => e.name);
  check(!names.includes("DB_URL") && !names.includes("DB_PASSWORD"), `database-filled names must not be sent: ${names}`);
  check(names.includes("REDIS_URL"), `REDIS_URL must be sent: ${names}`);
  const id = page.url().match(/#\/apps\/(\d+)/)[1];

  step("app API: db_env without values");
  const detail = await page.evaluate(async (id) => (await fetch(`/api/apps/${id}`)).json(), id);
  check(detail.db_env?.database === "admin_billing", `db_env.database: ${JSON.stringify(detail.db_env)}`);
  check(detail.db_env.vars.some((v) => v.name === "DB_PASSWORD" && v.kind === "password" && !v.overridden), "DB_PASSWORD listed as automatic");
  check(!JSON.stringify(detail.db_env).includes("value"), "db_env must not carry values");

  step("Environment tab: DB_* locked, override DB_USER through the dialog");
  await page.goto(`${srv.url}/#/apps/${id}/env`);
  await page.waitForSelector("[data-env='DB_URL'] .autoval");
  check((await page.locator("[data-env='REDIS_URL'] .autoval").count()) === 0, "REDIS_URL is the user's own");
  await page.screenshot({ path: path.join(shots, "env-tab-auto.png"), fullPage: true });
  await page.click("[data-env='DB_USER'] button:has-text('Isi sendiri')");
  await page.fill(".dlg input[aria-label='Nilai']", "reporting");
  await page.click(".dlg button[type=submit]");
  await page.click("button:has-text('Simpan perubahan')");
  await page.waitForSelector("[data-env='DB_USER'] .chip:has-text('isian sendiri')");
  const after = await page.evaluate(async (id) => (await fetch(`/api/apps/${id}`)).json(), id);
  check(after.db_env.vars.some((v) => v.name === "DB_USER" && v.overridden), `DB_USER overridden: ${JSON.stringify(after.db_env.vars)}`);
  await page.screenshot({ path: path.join(shots, "env-tab-override.png"), fullPage: true });

  step("Database page: SSH tunnel next to the existing details");
  await page.goto(`${srv.url}/#/databases`);
  await page.waitForSelector("#sshTunnel");
  check((await page.locator("#sshTunnel").innerText()).includes("ssh -N -L 5433:127.0.0.1:"), "tunnel command shown");
  check((await page.locator("#connCard").innerText()).includes("Host"), "existing host row kept");
  await page.screenshot({ path: path.join(shots, "databases-ssh.png"), fullPage: true });

  check(problems.length === 0, `page problems: ${problems.join("; ")}`);
} finally {
  await browser.close();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
}
if (!process.exitCode) console.log("smoke-dbenv: ok");
