// Runs the *.test.html pages in headless Chromium and fails if any test fails.
import { chromium, serveUnitPages, here } from "./lib.mjs";
import fs from "node:fs";

const { server, url } = await serveUnitPages();
const browser = await chromium.launch();
let failed = 0;
for (const file of fs.readdirSync(here).filter((f) => f.endsWith(".test.html"))) {
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(`${url}/test/${file}`);
  try {
    await page.waitForFunction(() => window.__results, null, { timeout: 15000 });
  } catch {
    console.log(`${file}: no result`, errors);
    failed++;
    await page.close();
    continue;
  }
  const r = await page.evaluate(() => window.__results);
  console.log(`${file}: ${r.passed} passed, ${r.failed.length} failed`);
  for (const f of r.failed) console.log(`  FAIL ${f.name}: ${f.error}`);
  failed += r.failed.length;
  await page.close();
}
await browser.close();
server.close();
process.exit(failed ? 1 : 0);
