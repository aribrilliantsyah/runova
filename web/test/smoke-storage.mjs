// Storage smoke: a JAR whose application.properties writes uploads to /app/storage and ./media →
// the review shows both folders ticked as "<app>/…" under Folder tersimpan with disk in the size →
// deploy → File tab lists the workspace as "<app>/", upload, open a folder, download, delete with
// confirmation → Lanjutan edits the folders (redeploy banner) → a full disk shows "Disk penuh"
// and the redeploy is refused. Another user cannot read the files. With SHOTS=dir it also writes
// screenshots at 1550 and 360 wide, light and dark.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, buildSampleJar, login, watch, check } from "./lib.mjs";

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-storage-"));
const shots = process.env.SHOTS || "";
const srv = await startRunova();
const browser = await chromium.launch();
const ctxt = await browser.newContext({ viewport: { width: 1550, height: 1000 }, acceptDownloads: true });
const page = await ctxt.newPage();
const problems = watch(page, srv.url);
const step = (m) => console.log(`- ${m}`);

async function apiAs(cookie, method, p, body) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null };
}
async function cookieOf(username, password) {
  const r = await fetch(`${srv.url}/api/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username, password }) });
  return r.headers.get("set-cookie").split(";")[0];
}

/** shoot(name): the page at 1550 and 360 wide, light and dark, into SHOTS. */
async function shoot(name, { full = false } = {}) {
  if (!shots) return;
  fs.mkdirSync(shots, { recursive: true });
  for (const [w, h] of [[1550, 1000], [360, 780]]) {
    await page.setViewportSize({ width: w, height: h });
    for (const theme of ["light", "dark"]) {
      await page.evaluate((t) => document.documentElement.setAttribute("data-theme", t), theme);
      await page.waitForTimeout(150);
      await page.screenshot({ path: path.join(shots, `${name}-${w}-${theme}.png`), fullPage: full });
    }
  }
  await page.setViewportSize({ width: 1550, height: 1000 });
  await page.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
}

try {
  const jar = buildSampleJar(tmp, {
    name: "starboom-1.0.jar",
    extraProps: "spring.servlet.multipart.location=/app/storage\nstarboom.media-dir=./media\n",
  });
  await login(page, srv.url, "admin", srv.adminPassword);
  const admin = await cookieOf("admin", srv.adminPassword);

  step("review: detected folders ticked as starboom/… with their source");
  await page.goto(`${srv.url}/#/deploy`);
  await page.setInputFiles("#upload-file", jar);
  await page.waitForSelector(".fileok");
  await page.fill("#nm", "starboom");
  await page.fill("[data-env='JWT_SECRET'] input", "x".repeat(32));
  await page.fill("[data-env='DB_PASSWORD'] input", "s3cret");
  const store = page.locator("#d-store");
  check(await store.evaluate((d) => d.open), "Folder tersimpan opens when something was detected");
  const rows = store.locator(".fldlist li");
  check((await rows.count()) === 2, `two folders detected: ${await store.innerText()}`);
  const text = await store.innerText();
  check(text.includes("starboom/storage") && text.includes("starboom/media"), `workspace names: ${text}`);
  check(text.includes("application.properties"), `source shown: ${text}`);
  check(!/\/var\/lib\/runova/.test(text), "no host path in the panel");
  check((await page.locator(".dsum").innerText()).includes("disk"), "the summary names the disk size");

  step("a bad folder blocks Deploy with the reason; removing it clears it");
  await store.locator("button:has-text('Tambah folder')").click();
  const extra = rows.nth(2).locator("input.f");
  await extra.fill("/etc/app");
  await page.waitForSelector(".dmiss button:has-text('folder sistem')");
  await rows.nth(2).locator("button[aria-label='Hapus baris']").click();
  await page.waitForSelector(".dok");
  await store.scrollIntoViewIfNeeded();
  await shoot("deploy-review-folders", { full: true });

  step("deploy runs");
  await page.click("#deploy");
  await page.waitForURL(/#\/apps\/\d+$/, { timeout: 30000 });
  await page.waitForSelector("#appstatus:has-text('Berjalan')", { timeout: 20000 });
  const id = Number(/#\/apps\/(\d+)/.exec(page.url())[1]);
  const detail = (await apiAs(admin, "GET", `/api/apps/${id}`)).body;
  check(JSON.stringify(detail.app.folders) === JSON.stringify([{ path: "/app/storage", dir: "storage" }, { path: "media", dir: "media" }]), `stored folders: ${JSON.stringify(detail.app.folders)}`);
  check(detail.app.disk_mb === 512, `disk = ${detail.app.disk_mb}`);
  check((await page.locator(".meters").innerText()).includes("Disk"), "Umum shows the disk bar");

  step("File tab: the workspace as starboom/, upload, open a folder, download, delete");
  fs.writeFileSync(path.join(srv.data, "data", "admin", "starboom", "media", "logo.png"), Buffer.alloc(2048));
  await page.click("[role=tab][data-tab='files']");
  await page.waitForSelector(".fcrumbs a:has-text('starboom/')");
  await page.waitForSelector("tr[data-file='storage'] .chip:has-text('tersimpan')");
  const note = path.join(tmp, "catatan.txt");
  fs.writeFileSync(note, "halo dari panel");
  await page.setInputFiles("#t-files input[type=file]", note);
  await page.waitForSelector("tr[data-file='catatan.txt']");
  await page.click("tr[data-file='media'] a.fname");
  await page.waitForURL(/files\/media$/);
  await page.waitForSelector("tr[data-file='logo.png']");
  check((await page.locator(".fcrumbs").innerText()).replace(/\s+/g, "") === "starboom/media/", `crumbs: ${await page.locator(".fcrumbs").innerText()}`);
  await page.goBack();
  await page.waitForSelector("tr[data-file='catatan.txt']");
  const [dl] = await Promise.all([page.waitForEvent("download"), page.click("tr[data-file='catatan.txt'] a[aria-label^='Unduh']")]);
  check(fs.readFileSync(await dl.path(), "utf8") === "halo dari panel", "download returns the file");
  check((await page.locator("tr[data-file='storage'] button[aria-label^='Hapus']").count()) === 0, "a saved folder cannot be deleted from here");
  await shoot("files-tab");
  await page.click("tr[data-file='catatan.txt'] button[aria-label^='Hapus']");
  await page.waitForSelector(".dlg:has-text('Hapus file?')");
  check((await page.locator(".dlg").innerText()).includes("starboom/catatan.txt"), "the confirmation names the file");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector("tr[data-file='catatan.txt']", { state: "detached" });

  step("another user cannot see the files");
  await apiAs(admin, "POST", "/api/users", { username: "andi", password: "andi-password-1", role: "user" });
  const andi = await cookieOf("andi", "andi-password-1");
  check((await apiAs(andi, "GET", `/api/apps/${id}/files?path=`)).status === 404, "andi lists admin's workspace");
  const leak = await fetch(`${srv.url}/api/apps/${id}/files/download?path=media/logo.png`, { headers: { cookie: andi } });
  check(leak.status === 404, `andi downloads: ${leak.status}`);

  step("Lanjutan: edit folders asks for a redeploy");
  await page.click("[role=tab][data-tab='advanced']");
  await page.waitForSelector("#a-folders .fldlist li");
  await page.locator("#a-folders button:has-text('Tambah folder')").click();
  await page.locator("#a-folders .fldlist li").last().locator("input.f").fill("/var/www/uploads");
  await page.locator("#a-folders .fldlist li").last().locator("input.f").press("Tab");
  check((await page.locator("#a-folders").innerText()).includes("starboom/uploads"), "a new mount shows its workspace folder");
  await page.click("#a-folders button:has-text('Simpan folder')");
  await page.waitForSelector("text=Deploy ulang untuk menerapkan");

  step("a full disk: chip, warning in the Umum bar, redeploy refused");
  fs.writeFileSync(path.join(srv.data, "data", "admin", "starboom", "big.bin"), Buffer.alloc(520 * 1024 * 1024));
  const re = await apiAs(admin, "POST", `/api/apps/${id}/deploy`);
  check(re.status === 409 && re.body.error.includes("Disk aplikasi penuh"), `redeploy with a full disk: ${re.status} ${JSON.stringify(re.body)}`);
  await page.click("[role=tab][data-tab='general']");
  await page.waitForSelector("#diskfull:has-text('Disk penuh')", { timeout: 10000 });
  await page.waitForSelector(".meters .disk-full");
  await shoot("disk-full-general");
  await page.click("[role=tab][data-tab='files']");
  await page.waitForSelector("#fusage .ftone.err");
  await shoot("disk-full-files");
  const bell = (await apiAs(admin, "GET", "/api/notifications")).body;
  const notes = bell.notifications || bell;
  check(notes.some((n) => n.level === "error" && n.title.includes("penuh")), `notification: ${JSON.stringify(notes).slice(0, 300)}`);

  step("list: card and quota tile show the disk");
  await page.goto(`${srv.url}/#/apps`);
  await page.waitForSelector(".acard:has-text('Disk penuh')");
  check((await page.locator("#stats").innerText()).includes("Disk"), "quota tiles include disk");
  await shoot("apps-list");

  step("users and server pages show disk");
  await page.goto(`${srv.url}/#/users`);
  await page.waitForSelector("tr[data-user='admin']");
  check((await page.locator("thead").innerText()).includes("Disk"), "users table has a Disk column");
  await page.goto(`${srv.url}/#/server`);
  await page.waitForSelector(".stat:has-text('Disk server')");
  check((await page.locator(".stat:has-text('Disk server')").innerText()).includes("dialokasikan"), "server disk names the apps' share");
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
if (!process.exitCode) console.log("smoke-storage: ok");
