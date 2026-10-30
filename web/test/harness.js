// A tiny test harness for the browser test pages: test(name, fn), assert(cond, msg), run().
// run() reports to window.__results = {passed, failed: [{name, error}]} for the runner to read,
// and prints a list on the page.
const tests = [];

export function test(name, fn) {
  tests.push({ name, fn });
}

export function assert(cond, msg = "assertion failed") {
  if (!cond) throw new Error(msg);
}

export function eq(got, want, msg = "") {
  if (JSON.stringify(got) !== JSON.stringify(want)) throw new Error(`${msg} got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
}

export async function run() {
  const results = { passed: 0, failed: [] };
  const out = document.createElement("pre");
  document.body.append(out);
  for (const t of tests) {
    try {
      await t.fn();
      results.passed++;
      out.append(`ok   ${t.name}\n`);
    } catch (e) {
      results.failed.push({ name: t.name, error: String(e && e.message ? e.message : e) });
      out.append(`FAIL ${t.name}: ${e}\n`);
    }
  }
  window.__results = results;
}
