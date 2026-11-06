// Storage smoke: a JAR whose application.properties writes uploads to /app/storage and ./media →
// the review shows both folders ticked as "<app>/…" under Folder tersimpan with disk in the size →
// deploy → the File tab file manager: breadcrumb and URL path, multi-file and drag-and-drop upload,
// keyboard, sort and filter, image and text preview, new folder, rename, move, copy path, grid
// view, zip of a selection, bulk delete, the phone sheet → Lanjutan edits the folders (redeploy
// banner) → a full disk shows "Disk penuh" and the redeploy is refused. Another user cannot read
// the files. With SHOTS=dir it also writes screenshots at 1550, 768 and 360 wide, light and dark.
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

/** shoot(name, {focus, widths, before}): the page at 1550, 768 and 360 wide, light and dark, into SHOTS. */
async function shoot(name, { full = false, focus = "", widths = [1550, 768, 360], before = null } = {}) {
  if (!shots) return;
  fs.mkdirSync(shots, { recursive: true });
  for (const w of widths) {
    await page.setViewportSize({ width: w, height: w > 400 ? 1000 : 780 });
    await page.waitForTimeout(300);
    if (before) await before(w);
    for (const theme of ["light", "dark"]) {
      await page.evaluate((t) => document.documentElement.setAttribute("data-theme", t), theme);
      await page.waitForTimeout(150);
      if (focus) await page.locator(focus).first().evaluate((el) => el.scrollIntoView({ block: "center" }));
      await page.screenshot({ path: path.join(shots, `${name}-${w}-${theme}.png`), fullPage: full && w > 400 });
    }
  }
  await page.setViewportSize({ width: 1550, height: 1000 });
  await page.evaluate(() => document.documentElement.setAttribute("data-theme", "light"));
}

/** dropFiles(selector, files): a drag and drop of files [{name, text}] onto the element, as a browser fires it. */
async function dropFiles(selector, files) {
  const dt = await page.evaluateHandle((list) => {
    const d = new DataTransfer();
    for (const f of list) d.items.add(new File([f.text], f.name, { type: "text/plain" }));
    return d;
  }, files);
  await page.dispatchEvent(selector, "dragenter", { dataTransfer: dt });
  await page.dispatchEvent(selector, "dragover", { dataTransfer: dt });
  return dt;
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
  await shoot("deploy-review-folders", { full: true, focus: "#d-store" });

  step("deploy runs");
  await page.click("#deploy");
  await page.waitForURL(/#\/apps\/\d+$/, { timeout: 30000 });
  await page.waitForSelector("#appstatus:has-text('Berjalan')", { timeout: 20000 });
  const id = Number(/#\/apps\/(\d+)/.exec(page.url())[1]);
  const detail = (await apiAs(admin, "GET", `/api/apps/${id}`)).body;
  check(JSON.stringify(detail.app.folders) === JSON.stringify([{ path: "/app/storage", dir: "storage" }, { path: "media", dir: "media" }]), `stored folders: ${JSON.stringify(detail.app.folders)}`);
  check(detail.app.disk_mb === 512, `disk = ${detail.app.disk_mb}`);
  check((await page.locator(".meters").innerText()).includes("Disk"), "Umum shows the disk bar");

  step("File tab: breadcrumb, saved badge, multi-file upload, open a folder, back");
  fs.writeFileSync(path.join(srv.data, "data", "admin", "starboom", "media", "logo.png"), Buffer.alloc(2048));
  // A real 2x2 PNG so the preview and the grid thumbnail render.
  const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4AWP4z8DwnwEIGBgYGBgYAAA3AQEbx1ewAAAAAElFTkSuQmCC", "base64");
  await page.click("[role=tab][data-tab='files']");
  await page.waitForSelector(".fm-crumbs a:has-text('starboom')");
  await page.waitForSelector("tr[data-file='storage'] .chip:has-text('tersimpan')");
  check((await page.locator("tr[data-file='storage'] .chip").getAttribute("title")).includes("tidak bisa dihapus"), "the saved badge says why");
  const note = path.join(tmp, "catatan.txt");
  fs.writeFileSync(note, "baris satu\nbaris dua\n");
  const pic = path.join(tmp, "foto.png");
  fs.writeFileSync(pic, png);
  await page.setInputFiles("#t-files input[type=file]", [note, pic]);
  await page.waitForSelector("tr[data-file='catatan.txt']");
  await page.waitForSelector("tr[data-file='foto.png']");
  check((await page.locator("#fstatus").innerText()).includes("item"), "the footer counts the items");
  await page.click("tr[data-file='media'] .fm-name");
  await page.waitForURL(/files\?path=media$/);
  await page.waitForSelector("tr[data-file='logo.png']");
  check((await page.locator(".fm-crumbs").innerText()).replace(/\s+/g, "") === "starboom/media", `crumbs: ${await page.locator(".fm-crumbs").innerText()}`);
  await page.reload();
  await page.waitForSelector("tr[data-file='logo.png']", { timeout: 15000 });
  await page.goBack();
  await page.waitForSelector("tr[data-file='catatan.txt']");

  step("keyboard: arrows move, Enter opens a folder, Backspace goes up");
  await page.locator("tr[data-file='media']").focus();
  await page.keyboard.press("Enter");
  await page.waitForURL(/path=media$/);
  await page.waitForSelector("tr[data-file='logo.png']");
  await page.locator("tr[data-file='logo.png']").focus();
  await page.keyboard.press("Backspace");
  await page.waitForSelector("tr[data-file='catatan.txt']");
  check(!/path=/.test(page.url()), `Backspace goes to the workspace: ${page.url()}`);
  await page.locator("#filerows tr").first().focus();
  await page.keyboard.press("ArrowDown");
  check((await page.evaluate(() => document.activeElement?.dataset.file)) === (await page.locator("#filerows tr").nth(1).getAttribute("data-file")), "ArrowDown moves to the next row");

  step("sorting keeps folders on top; filter narrows the list");
  await page.click(".fm-t th:has-text('Ukuran') button");
  await page.click(".fm-t th:has-text('Ukuran') button");
  const order = await page.locator("#filerows tr").evaluateAll((rs) => rs.map((r) => r.dataset.kind));
  check(order.indexOf("folder") === 0 && order.lastIndexOf("folder") < order.findIndex((k) => k !== "folder"), `folders first: ${order}`);
  await page.click(".fm-t th:has-text('Nama') button");
  await page.fill("#ffilter", "catat");
  await page.waitForFunction(() => document.querySelectorAll("#filerows tr").length === 1);
  await page.fill("#ffilter", "");
  await page.waitForSelector("tr[data-file='foto.png']");

  step("preview: image inline, text with line numbers");
  await page.dblclick("tr[data-file='foto.png']");
  await page.waitForSelector("#fpreview .fm-pv-img img");
  await page.waitForFunction(() => document.querySelector("#fpreview img")?.naturalWidth === 2);
  check((await page.locator("#fpreview .fm-info").innerText()).includes("image/png"), "preview shows the sniffed type");
  check((await page.locator("#fpreview .fm-info").innerText()).includes("/data/foto.png"), "preview shows the app's path");
  await shoot("files-preview", { focus: "#fpreview" });
  await page.dblclick("tr[data-file='catatan.txt']");
  await page.waitForSelector("#fpreview pre.fm-code .ln");
  check((await page.locator("#fpreview pre.fm-code .ln").count()) === 2, "two lines of text");
  await page.keyboard.press("Escape");
  await page.waitForSelector("#fpreview", { state: "hidden" });

  step("new folder, rename, move into it");
  await page.click("#fnewdir");
  await page.fill(".dlg input.f", "arsip");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector("tr[data-file='arsip']");
  await page.click("tr[data-file='catatan.txt'] .fm-more");
  await page.click(".fm-menu button:has-text('Ganti nama')");
  await page.fill(".dlg input.f", "catatan-lama.txt");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector("tr[data-file='catatan-lama.txt']");
  await page.click("tr[data-file='catatan-lama.txt']", { button: "right" });
  await page.click(".fm-menu button:has-text('Pindahkan')");
  await page.click(".fm-pick button:has-text('arsip')");
  await page.waitForSelector(".fm-pick-note:has-text('starboom/arsip/catatan-lama.txt')");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector("tr[data-file='catatan-lama.txt']", { state: "detached" });
  check(fs.existsSync(path.join(srv.data, "data", "admin", "starboom", "arsip", "catatan-lama.txt")), "moved on disk");
  await page.click("tr[data-file='storage']", { button: "right" });
  check(await page.locator(".fm-menu button:has-text('Hapus')").isDisabled(), "a saved folder cannot be deleted");
  check(/folder tersimpan/i.test(await page.locator(".fm-menu").innerText()), "the menu says why");
  await page.keyboard.press("Escape");

  step("copy path names the app's paths");
  await page.click("tr[data-file='foto.png'] .fm-more");
  await page.click(".fm-menu button:has-text('Salin path')");
  const pathsText = await page.locator(".dlg .fm-paths").innerText();
  check(pathsText.includes("./foto.png") && pathsText.includes("/data/foto.png"), `paths: ${pathsText}`);
  await page.click(".dlg button[type=submit]");

  step("drag and drop: overlay, then both files upload");
  await dropFiles(".fm-stage", [{ name: "a.txt", text: "a" }]);
  await page.waitForSelector(".fm-drop:not([hidden])");
  if (shots) await shoot("files-dropzone", { before: async () => dropFiles(".fm-stage", [{ name: "a.txt", text: "a" }]) });
  const dt = await dropFiles(".fm-stage", [{ name: "drop-1.txt", text: "satu" }, { name: "drop-2.txt", text: "dua" }]);
  await page.dispatchEvent(".fm-stage", "drop", { dataTransfer: dt });
  await page.waitForSelector("tr[data-file='drop-2.txt']");
  await page.waitForSelector("tr[data-file='drop-1.txt']");
  check(await page.locator(".fm-drop").isHidden(), "the overlay leaves after the drop");

  step("grid view shows image thumbnails");
  await page.click(".fm-view button[data-view=grid]");
  await page.waitForSelector(".fm-tile[data-file='foto.png'] .fm-thumb img");
  await shoot("files-grid");
  await page.click(".fm-view button[data-view=list]");
  await page.waitForSelector("tr[data-file='foto.png']");
  await shoot("files-list");

  step("download one file, and a selection as zip");
  const [dl] = await Promise.all([page.waitForEvent("download"), (async () => { await page.click("tr[data-file='drop-1.txt'] .fm-more"); await page.click(".fm-menu button:has-text('Unduh')"); })()]);
  check(fs.readFileSync(await dl.path(), "utf8") === "satu", "download returns the file");
  await page.click("tr[data-file='drop-1.txt'] .c-chk input");
  await page.click("tr[data-file='drop-2.txt'] .c-chk input");
  await page.waitForSelector(".fm-sel:has-text('2 dipilih')");
  await shoot("files-bulk");
  const [zipDl] = await Promise.all([page.waitForEvent("download"), page.click("#fselzip")]);
  const zipBytes = fs.readFileSync(await zipDl.path());
  check(zipBytes.subarray(0, 2).toString() === "PK" && zipDl.suggestedFilename() === "starboom.zip", `zip: ${zipDl.suggestedFilename()}`);
  check(zipBytes.includes(Buffer.from("drop-1.txt")) && zipBytes.includes(Buffer.from("drop-2.txt")), "zip holds both files");

  step("bulk delete with confirmation; shift-click selects a range");
  await page.click("#fseldel");
  await page.waitForSelector(".dlg:has-text('Hapus 2 item?')");
  check((await page.locator(".dlg").innerText()).includes("starboom/drop-1.txt"), "the confirmation names the files");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector("tr[data-file='drop-1.txt']", { state: "detached" });
  await page.waitForSelector("tr[data-file='drop-2.txt']", { state: "detached" });
  await page.click("tr[data-file='arsip'] .c-chk input");
  await page.click("tr[data-file='foto.png'] .c-chk input", { modifiers: ["Shift"] });
  const n = await page.locator("#filerows tr.on").count();
  check(n >= 2, `shift-click selects a range: ${n}`);
  check(await page.locator("#fseldel").isDisabled(), "a range holding a saved folder cannot be deleted") ;
  await page.keyboard.press("Escape");
  await page.locator("tr[data-file='foto.png']").focus();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Delete");
  await page.waitForSelector(".dlg:has-text('Hapus file?')");
  await page.click(".dlg button:has-text('Batal')");

  step("mobile: compact rows, actions as a sheet, upload still there");
  await page.setViewportSize({ width: 360, height: 780 });
  await page.waitForTimeout(200);
  check(await page.locator("#fupload").isVisible(), "upload on phones");
  await page.click("tr[data-file='foto.png'] .fm-more");
  const sheet = await page.locator(".fm-menu").boundingBox();
  check(Math.round(sheet.width) === 360 && Math.round(sheet.y + sheet.height) >= 778, `actions are a bottom sheet: ${JSON.stringify(sheet)}`);
  await page.keyboard.press("Escape");
  await page.setViewportSize({ width: 1550, height: 1000 });

  step("another user cannot see the files");
  await apiAs(admin, "POST", "/api/users", { username: "andi", password: "andi-password-1", role: "user" });
  const andi = await cookieOf("andi", "andi-password-1");
  check((await apiAs(andi, "GET", `/api/apps/${id}/files?path=`)).status === 404, "andi lists admin's workspace");
  const leak = await fetch(`${srv.url}/api/apps/${id}/files/download?path=media/logo.png`, { headers: { cookie: andi } });
  check(leak.status === 404, `andi downloads: ${leak.status}`);

  step("Lanjutan: the folders card has its add button in the header and save in the footer");
  await page.click("[role=tab][data-tab='advanced']");
  await page.waitForSelector("#a-folders .fldlist li");
  const save = page.locator("#a-folders footer button:has-text('Simpan folder')");
  check(await save.isDisabled(), "save is off until something changes");
  check(await page.locator("#a-folders footer .dirty").isHidden(), "no unsaved mark yet");
  const addBox = await page.locator("#a-folders header button:has-text('Tambah folder')").boundingBox();
  const saveBox = await save.boundingBox();
  check(saveBox.y - (addBox.y + addBox.height) > 40, "add and save are apart, not stacked");
  check((await page.locator("#a-folders .fldlist").innerText()).includes("terdeteksi"), "rows name their source");
  await shoot("advanced-folders-filled", { focus: "#a-folders" });
  // Removing every row shows the empty state (not saved; the page is re-rendered below).
  while (await page.locator("#a-folders .fldlist li").count()) await page.locator("#a-folders .fldlist li").first().locator("button[aria-label='Hapus baris']").click();
  await page.waitForSelector("#a-folders .fldempty:has-text('Belum ada folder tersimpan')");
  await shoot("advanced-folders-empty", { focus: "#a-folders" });
  await page.click("[role=tab][data-tab='general']");
  await page.click("[role=tab][data-tab='advanced']");
  await page.waitForSelector("#a-folders .fldlist li");
  await page.locator("#a-folders header button:has-text('Tambah folder')").click();
  await page.locator("#a-folders .fldlist li").last().locator("input.f").fill("/var/www/uploads");
  await page.locator("#a-folders .fldlist li").last().locator("input.f").press("Tab");
  check((await page.locator("#a-folders").innerText()).includes("starboom/uploads"), "a new mount shows its workspace folder");
  await page.waitForSelector("#a-folders footer .dirty:has-text('Belum disimpan')");
  await save.click();
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
  await page.waitForSelector("#fstatus .ftone[data-level=full]");
  await shoot("disk-full-files");
  const bell = (await apiAs(admin, "GET", "/api/notifications")).body;
  const notes = bell.notifications || bell;
  check(notes.some((n) => n.level === "error" && n.title.includes("penuh")), `notification: ${JSON.stringify(notes).slice(0, 300)}`);

  step("list: card and quota tile show the disk");
  await page.goto(`${srv.url}/#/apps`);
  await page.waitForSelector(".acard:has-text('Disk penuh')");
  check((await page.locator("#stats").innerText()).includes("Disk"), "quota tiles include disk");
  await shoot("apps-list");

  step("review page without detected folders: empty state and the add button under it");
  const plain = buildSampleJar(tmp, { name: "plain-1.0.jar" });
  await page.goto(`${srv.url}/#/deploy`);
  await page.setInputFiles("#upload-file", plain);
  await page.waitForSelector(".fileok");
  await page.locator("#d-store summary").click();
  await page.waitForSelector("#d-store .fldempty:has-text('Belum ada folder tersimpan')");
  check(await page.locator("#d-store .fldadd button:has-text('Tambah folder')").isVisible(), "add button under the empty state");
  await shoot("deploy-review-folders-empty", { focus: "#d-store" });

  step("users and server pages show disk");
  await page.goto(`${srv.url}/#/users`);
  await page.waitForSelector("tr[data-user='admin']");
  check((await page.locator("thead").innerText()).includes("Disk"), "users table has a Disk column");
  await page.goto(`${srv.url}/#/server`);
  await page.waitForSelector("[data-stat=disk-server]");
  const serverDisk = await page.locator("[data-stat=disk-server]").innerText();
  const appDisk = await page.locator("[data-stat=disk-apps]").innerText();
  check(/terpakai dari \d+(,\d)? GB/.test(serverDisk) && !serverDisk.includes("aplikasi"), `server disk tile is only the server's disk: ${serverDisk}`);
  check(/dibagikan ke \d+ aplikasi/.test(appDisk) && /terisi/.test(appDisk) && !/\d B\b/.test(appDisk), `app disk tile: shared, filled, no bare bytes: ${appDisk}`);
  check(await page.locator("[data-stat=cpu]:has-text('CPU server')").isVisible(), "CPU tile is the server's CPU, not an allocation");
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
