// Shared helpers for the Playwright scripts in this folder.
import { createRequire } from "node:module";
import { spawn, execFileSync } from "node:child_process";
import { createServer } from "node:http";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import net from "node:net";
import { fileURLToPath } from "node:url";

export const here = path.dirname(fileURLToPath(import.meta.url));
export const repo = path.resolve(here, "../..");

function findPlaywright() {
  const require = createRequire(import.meta.url);
  const candidates = [process.env.PLAYWRIGHT_DIR].filter(Boolean);
  const npx = path.join(os.homedir(), ".npm/_npx");
  if (fs.existsSync(npx)) {
    for (const d of fs.readdirSync(npx)) candidates.push(path.join(npx, d, "node_modules"));
  }
  for (const dir of [undefined, ...candidates]) {
    try {
      return require(require.resolve("playwright", dir ? { paths: [dir] } : undefined));
    } catch {
      // try the next place
    }
  }
  throw new Error("playwright not found; set PLAYWRIGHT_DIR to a node_modules folder that has it");
}

const pw = findPlaywright();

/** The full Chromium build under ~/.cache/ms-playwright (the headless shell may not be installed). */
function chromePath() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const root = path.join(os.homedir(), ".cache/ms-playwright");
  const dirs = fs.existsSync(root) ? fs.readdirSync(root).filter((d) => d.startsWith("chromium-")).sort().reverse() : [];
  for (const d of dirs) {
    const exe = path.join(root, d, "chrome-linux64/chrome");
    if (fs.existsSync(exe)) return exe;
  }
  return undefined;
}

export const chromium = {
  launch: (opts = {}) => pw.chromium.launch({ executablePath: chromePath(), args: ["--no-sandbox"], ...opts }),
};

export async function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
    s.on("error", reject);
  });
}

let built = null;
/** Builds ./cmd/runova once per run (web assets are embedded, so rebuild after editing them). */
export function buildRunova() {
  if (built) return built;
  built = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "runova-bin-")), "runova");
  execFileSync("go", ["build", "-o", built, "./cmd/runova"], { cwd: repo, stdio: "inherit", env: { ...process.env, CGO_ENABLED: "0" } });
  return built;
}

const ADMIN_PASSWORD_RE = /^Password: (\S+)$/m;

/**
 * startRunova() → {url, data, adminPassword, stop(), logs()}. A real `runova serve` on a fresh data
 * directory with RUNOVA_FAKE_BACKENDS=1 (fake Docker, Caddy and PostgreSQL).
 */
export async function startRunova({ env = {} } = {}) {
  const bin = buildRunova();
  const data = fs.mkdtempSync(path.join(os.tmpdir(), "runova-ui-"));
  const port = await freePort();
  const base = {
    ...process.env,
    RUNOVA_DATA: data,
    RUNOVA_SECRET_KEY: path.join(data, "secret.key"),
    RUNOVA_LISTEN: `127.0.0.1:${port}`,
    RUNOVA_FAKE_BACKENDS: "1",
    ...env,
  };
  execFileSync(bin, ["keygen"], { env: base });
  const created = execFileSync(bin, ["admin", "create", "admin"], { env: base, encoding: "utf8" });
  const adminPassword = ADMIN_PASSWORD_RE.exec(created)?.[1];
  if (!adminPassword) throw new Error(`no admin password in: ${created}`);

  const child = spawn(bin, ["serve"], { env: base, stdio: ["ignore", "pipe", "pipe"] });
  let log = "";
  child.stdout.on("data", (d) => (log += d));
  child.stderr.on("data", (d) => (log += d));
  const url = `http://127.0.0.1:${port}`;
  for (let i = 0; ; i++) {
    try {
      const r = await fetch(`${url}/api/me`);
      if (r.status === 401) break;
    } catch {
      // not up yet
    }
    if (child.exitCode !== null) throw new Error(`runova exited: ${log}`);
    if (i > 100) throw new Error(`runova did not start: ${log}`);
    await new Promise((r) => setTimeout(r, 100));
  }
  return {
    url,
    data,
    adminPassword,
    bin,
    env: base,
    logs: () => log,
    async stop() {
      child.kill("SIGTERM");
      await new Promise((r) => (child.exitCode !== null ? r() : child.once("exit", r)));
      fs.rmSync(data, { recursive: true, force: true });
    },
  };
}

/** A sample Spring-style JAR (Java 17 bytecode) that needs JWT_SECRET, written to dir. */
export function buildSampleJar(dir, { name = "billing-api-2.4.1.jar", release = 17, extraProps = "", extraFiles = {} } = {}) {
  const work = fs.mkdtempSync(path.join(dir, "jar-"));
  fs.mkdirSync(path.join(work, "src/com/perusahaan/billing"), { recursive: true });
  fs.writeFileSync(
    path.join(work, "src/com/perusahaan/billing/BillingApplication.java"),
    "package com.perusahaan.billing;\npublic class BillingApplication { public static void main(String[] a) throws Exception { System.out.println(\"started\"); Thread.sleep(Long.MAX_VALUE); } }\n",
  );
  fs.mkdirSync(path.join(work, "out"));
  execFileSync("javac", ["--release", String(release), "-d", "out", "src/com/perusahaan/billing/BillingApplication.java"], { cwd: work });
  fs.writeFileSync(
    path.join(work, "out/application.properties"),
    `server.port=8080\njwt.secret=\${JWT_SECRET}\ndb.url=\${DB_URL:jdbc:postgresql://localhost/app}\ndb.password=\${DB_PASSWORD}\n${extraProps}`,
  );
  for (const [rel, content] of Object.entries(extraFiles)) {
    fs.mkdirSync(path.dirname(path.join(work, "out", rel)), { recursive: true });
    fs.writeFileSync(path.join(work, "out", rel), content);
  }
  fs.writeFileSync(path.join(work, "manifest.txt"), "Main-Class: com.perusahaan.billing.BillingApplication\n");
  const jar = path.join(dir, name);
  execFileSync("jar", ["cfm", jar, "manifest.txt", "-C", "out", "."], { cwd: work });
  fs.rmSync(work, { recursive: true, force: true });
  return jar;
}

/** A static 64-bit x86-64 Linux ELF (no interpreter) built from testdata/hello. */
export function buildStaticBinary(dir, name = "hello") {
  const out = path.join(dir, name);
  execFileSync("go", ["build", "-o", out, "./testdata/hello"], { cwd: repo, env: { ...process.env, CGO_ENABLED: "0", GOOS: "linux", GOARCH: "amd64" }, stdio: "inherit" });
  return out;
}

/**
 * A minimal static x86-64 ELF (one PT_LOAD, no interpreter, no language of its own) followed by
 * the given NUL-terminated strings: stands in for binaries of languages that cannot be built here.
 */
export function buildMarkedBinary(dir, name, ...markers) {
  const head = Buffer.alloc(64 + 56);
  head.write("\x7fELF", 0, "latin1");
  head[4] = 2;
  head[5] = 1;
  head[6] = 1;
  head.writeUInt16LE(2, 16);
  head.writeUInt16LE(62, 18);
  head.writeUInt32LE(1, 20);
  head.writeBigUInt64LE(64n, 32);
  head.writeUInt16LE(64, 52);
  head.writeUInt16LE(56, 54);
  head.writeUInt16LE(1, 56);
  head.writeUInt32LE(1, 64);
  head.writeUInt32LE(4, 68);
  const body = Buffer.concat(markers.map((m) => Buffer.from(`${m}\0`, "latin1")));
  head.writeBigUInt64LE(BigInt(head.length + body.length), 96);
  head.writeBigUInt64LE(BigInt(head.length + body.length), 104);
  const out = path.join(dir, name);
  fs.writeFileSync(out, Buffer.concat([head, body]), { mode: 0o755 });
  return out;
}

/** Serves web/ as /assets/ and web/test as /test/, unhashed, for the unit-test pages. */
export function serveUnitPages() {
  const types = { ".js": "text/javascript", ".html": "text/html", ".css": "text/css", ".svg": "image/svg+xml" };
  const server = createServer((req, res) => {
    const u = new URL(req.url, "http://x");
    let file = null;
    if (u.pathname.startsWith("/assets/")) file = path.join(repo, "web", u.pathname.slice("/assets/".length));
    else if (u.pathname.startsWith("/test/")) file = path.join(here, u.pathname.slice("/test/".length));
    if (!file || !file.startsWith(path.join(repo, "web")) || !fs.existsSync(file)) {
      res.writeHead(404).end();
      return;
    }
    res.writeHead(200, { "Content-Type": types[path.extname(file)] || "application/octet-stream" }).end(fs.readFileSync(file));
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve({ server, url: `http://127.0.0.1:${server.address().port}` })));
}

export async function login(page, base, username, password) {
  await page.goto(`${base}/`);
  await page.fill("#login-user", username);
  await page.fill("#login-pass", password);
  await Promise.all([page.waitForSelector(".app"), page.click("button[type=submit]")]);
}

/** Collects console errors and requests that leave the origin. */
export function watch(page, base) {
  const problems = [];
  // Calls a test provokes on purpose (a rejected form) are listed here and not counted.
  problems.allow = (method, pathname, status) => allowed.push(`${method} ${pathname} ${status}`);
  const allowed = [];
  const origin = new URL(base).origin;
  // The browser logs every failed response as a console error. A 401 from /api/me is how the
  // panel learns nobody is signed in (and a refused login is a 401 too), so those are expected.
  const expected = new Set();
  page.on("response", (r) => {
    const u = new URL(r.url());
    if (r.status() === 401 && (u.pathname === "/api/me" || u.pathname === "/api/login")) expected.add(r.url());
    const key = `${r.request().method()} ${u.pathname} ${r.status()}`;
    if (r.status() >= 400 && !expected.has(r.url())) {
      if (allowed.includes(key)) expected.add(r.url());
      else problems.push(`http ${key}`);
    }
  });
  page.on("console", (m) => {
    if (m.type() !== "error") return;
    if (/status of 4\d\d/.test(m.text()) && expected.size) return;
    problems.push(`console: ${m.text()}`);
  });
  page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`));
  page.on("request", (r) => {
    const u = r.url();
    if (!u.startsWith(origin) && !u.startsWith("data:") && !u.startsWith("blob:") && !u.startsWith("ws://127.0.0.1")) problems.push(`external request: ${u}`);
  });
  return problems;
}

export function check(cond, msg) {
  if (!cond) throw new Error(msg);
}
