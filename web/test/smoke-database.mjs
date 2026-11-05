// Database management smoke: table browser (list, columns, paging, truncate with name check),
// query editor (read result, write mode, refusal, line numbers, Ctrl+Enter, history, CSV),
// restore from an uploaded .dump and export download. Typing survives the page's polling.
// Screenshots at 1550/768/360 in light and dark go to RUNOVA_SHOTS (default /tmp/runova-db).
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { chromium, startRunova, login, watch, check } from "./lib.mjs";

const shots = process.env.RUNOVA_SHOTS || "/tmp/runova-db";
fs.mkdirSync(shots, { recursive: true });
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "smoke-db-"));
const srv = await startRunova();
const browser = await chromium.launch();
const step = (m) => console.log(`- ${m}`);

async function call(method, p, body, cookie) {
  const r = await fetch(`${srv.url}${p}`, { method, headers: { cookie, "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null, headers: r.headers };
}

try {
  const lr = await call("POST", "/api/login", { username: "admin", password: srv.adminPassword });
  const cookie = lr.headers.get("set-cookie").split(";")[0];
  const db = await call("POST", "/api/databases", { suffix: "billing" }, cookie);
  check(db.status === 201, `create db ${db.status}`);

  const ctx = await browser.newContext({ viewport: { width: 1550, height: 980 }, acceptDownloads: true });
  const page = await ctx.newPage();
  const problems = watch(page, srv.url);
  await login(page, srv.url, "admin", srv.adminPassword);
  await page.goto(`${srv.url}/#/databases`);

  step("table browser lists the tables with rows and size");
  await page.waitForSelector("#tableList .trow[data-table='invoices']");
  const listText = await page.locator("#tableList").innerText();
  check(listText.includes("230 baris") && listText.includes("customers"), `table list: ${listText}`);

  step("open a table: columns with primary key, first page of 100, next page");
  await page.click(".trow[data-table='invoices']");
  await page.waitForSelector("#tableDetail table.grid tbody tr");
  check((await page.locator("#tableDetail table.tcols tbody tr").count()) === 5, "five columns");
  check((await page.locator("#tableDetail .chip.pk").count()) === 1, "one primary key");
  check((await page.locator("#tableDetail table.grid tbody tr").count()) === 100, "100 rows on the first page");
  check((await page.locator("#tableDetail .pg-info").innerText()).includes("1–100 dari 230"), "pager text");
  await page.click("#tableDetail button[aria-label='Halaman berikutnya']");
  await page.waitForFunction(() => document.querySelector("#tableDetail .pg-info")?.textContent.includes("101–200"));
  check((await page.locator("#tableDetail table.grid tbody tr").first().locator("td").first().innerText()) === "101", "row numbers continue");
  await page.screenshot({ path: path.join(shots, "tables-1550-light.png"), fullPage: true });

  step("NULL shows as NULL; truncate needs the exact table name");
  await page.click(".trow[data-table='customers']");
  await page.waitForSelector("#tableDetail td.null");
  await page.click("#tableDetail button:has-text('Kosongkan')");
  await page.fill(".dlg input", "Customers");
  check(await page.locator(".dlg button[type=submit]").isDisabled(), "truncate waits for the exact name");
  await page.fill(".dlg input", "customers");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector(".toast:has-text('customers dikosongkan')");
  await page.waitForSelector("#tableDetail .tempty:has-text('kosong')");

  step("query editor: read query, result grid, duration, line numbers");
  await page.click("#dbTabQuery");
  await page.fill("#sqlEditor", "-- tagihan\nSELECT * FROM invoices");
  check((await page.locator(".sqlgut span").count()) === 2, "two line numbers");
  // A poll tick re-renders the cards above; the editor keeps text and focus.
  await page.focus("#sqlEditor");
  await page.waitForTimeout(5600);
  check((await page.inputValue("#sqlEditor")).includes("SELECT * FROM invoices"), "editor text survives polling");
  check(await page.evaluate(() => document.activeElement?.id === "sqlEditor"), "editor keeps focus through polling");
  await page.keyboard.press("Control+Enter");
  await page.waitForSelector("#queryResult table.grid");
  check((await page.locator("#queryResult table.grid tbody tr").count()) === 230, "all 230 rows");
  check(/\d+ ms/.test(await page.locator("#queryTime").innerText()), "duration shown");
  await page.screenshot({ path: path.join(shots, "query-1550-light.png"), fullPage: true });

  step("CSV export of the result");
  const [csv] = await Promise.all([page.waitForEvent("download"), page.click("#csvBtn")]);
  const csvText = fs.readFileSync(await csv.path(), "utf8");
  check(csvText.startsWith("id,memo,amount,paid,created_at\r\n1,Tagihan 0001,"), `csv: ${csvText.slice(0, 80)}`);

  step("a write in read mode is refused with a hint; write mode runs it");
  await page.fill("#sqlEditor", "DELETE FROM invoices WHERE id = 1");
  await page.click("#runQuery");
  await page.waitForSelector("#queryResult .qerr:has-text('mode Tulis')");
  await page.click("#modeWrite");
  check((await page.locator("#modeNote").innerText()).includes("Mode Tulis"), "write note");
  await page.click("#runQuery");
  await page.waitForSelector("#queryResult .qdone:has-text('DELETE · 1 baris terpengaruh')");
  await page.screenshot({ path: path.join(shots, "query-write-1550-light.png"), fullPage: true });

  step("refused command never reaches the server's database");
  await page.fill("#sqlEditor", "COPY invoices TO '/tmp/x'");
  await page.click("#runQuery");
  await page.waitForSelector("#queryResult .qerr:has-text('COPY')");
  await page.fill("#sqlEditor", "SELECT 1;\nDROP TABLE invoices");
  await page.click("#runQuery");
  await page.waitForSelector("#queryResult .qerr:has-text('satu perintah')");

  step("history: newest first, click puts it back");
  await page.click("#historyBtn");
  await page.waitForSelector("#historyList .qh");
  const hist = await page.locator("#historyList .qh code").allInnerTexts();
  check(hist[0].startsWith("SELECT 1;") && hist.some((x) => x.startsWith("-- tagihan")), `history: ${hist}`);
  await page.screenshot({ path: path.join(shots, "history-1550-light.png"), fullPage: true });
  await page.locator("#historyList .qh", { hasText: "-- tagihan" }).click();
  check((await page.inputValue("#sqlEditor")).startsWith("-- tagihan"), "history item restored");

  step("export downloads a dump named after the database");
  const [dump] = await Promise.all([page.waitForEvent("download"), page.click("#exportBtn")]);
  check(/^admin_billing-\d{8}-\d{6}\.dump$/.test(dump.suggestedFilename()), `export name ${dump.suggestedFilename()}`);
  const dumpFile = path.join(tmp, "billing.dump");
  await dump.saveAs(dumpFile);
  check(fs.readFileSync(dumpFile, "utf8").startsWith("PGDMP"), "export is a custom dump");

  step("restore from file: name check, upload, toast");
  await page.click("#restoreFileBtn");
  await page.setInputFiles("#restoreFile", dumpFile);
  await page.waitForSelector(".rdrop .mono:has-text('billing.dump')");
  check(await page.locator(".dlg button[type=submit]").isDisabled(), "restore waits for the name");
  await page.fill(".dlg input[aria-label^='Ketik']", "admin_billing");
  await page.screenshot({ path: path.join(shots, "restore-1550-light.png"), fullPage: true });
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector(".toast:has-text('di-restore dari billing.dump')");

  step("a plain SQL file is refused by the server");
  const sqlFile = path.join(tmp, "data.sql");
  fs.writeFileSync(sqlFile, "CREATE TABLE x();\n");
  problems.allow("POST", `/api/databases/${db.body.id}/restore-upload`, 400);
  await page.click("#restoreFileBtn");
  await page.setInputFiles("#restoreFile", sqlFile);
  await page.fill(".dlg input[aria-label^='Ketik']", "admin_billing");
  await page.click(".dlg button[type=submit]");
  await page.waitForSelector(".dlg div.err:has-text('pg_dump -Fc')");
  await page.keyboard.press("Escape");

  step("activity records the new actions");
  const acts = (await call("GET", "/api/activity", undefined, cookie)).body.map((a) => a.action);
  for (const a of ["db.query", "db.table_truncate", "db.export", "db.restore_upload"]) check(acts.includes(a), `activity lacks ${a}`);
  check(problems.length === 0, `page problems: ${problems.join("; ")}`);
  await ctx.close();

  step("screenshots at 1550, 768 and 360, light and dark");
  for (const theme of ["light", "dark"]) {
    for (const width of [1550, 768, 360]) {
      const c = await browser.newContext({ viewport: { width, height: width === 360 ? 780 : 980 }, colorScheme: theme });
      const p = await c.newPage();
      const pr = watch(p, srv.url);
      await p.addInitScript((t) => localStorage.setItem("runova.theme", t), theme);
      await login(p, srv.url, "admin", srv.adminPassword);
      await p.goto(`${srv.url}/#/databases`);
      await p.waitForSelector(".trow[data-table='invoices']");
      await p.click(".trow[data-table='invoices']");
      await p.waitForSelector("#tableDetail table.grid tbody tr");
      const overflow = await p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      check(overflow <= 0, `${width} ${theme}: page scrolls sideways by ${overflow}px`);
      await p.screenshot({ path: path.join(shots, `tables-${width}-${theme}.png`), fullPage: true });
      await p.click("#dbTabQuery");
      await p.fill("#sqlEditor", "SELECT id, memo, amount, paid\nFROM invoices\nWHERE paid = false");
      await p.click("#runQuery");
      await p.waitForSelector("#queryResult table.grid");
      const overflow2 = await p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      check(overflow2 <= 0, `${width} ${theme} query: page scrolls sideways by ${overflow2}px`);
      await p.screenshot({ path: path.join(shots, `query-${width}-${theme}.png`), fullPage: true });
      await p.click("#historyBtn");
      await p.screenshot({ path: path.join(shots, `history-${width}-${theme}.png`), fullPage: true });
      await p.click("#restoreFileBtn");
      await p.waitForSelector(".dlg");
      await p.screenshot({ path: path.join(shots, `restore-${width}-${theme}.png`) });
      check(pr.length === 0, `${width} ${theme}: ${pr.join("; ")}`);
      await c.close();
    }
  }
  console.log(`smoke-database: ok (screenshots in ${shots})`);
} catch (e) {
  console.error(e);
  console.error(srv.logs().slice(-3000));
  process.exitCode = 1;
} finally {
  await browser.close();
  await srv.stop();
  fs.rmSync(tmp, { recursive: true, force: true });
}
