// logparse: what one container log line means, for the log viewer (logview.js).
// Pure functions, no DOM: level, the app's own timestamp, ANSI colors, stack-trace
// continuation lines and the start of a new run of the app.

// eslint-disable-next-line no-control-regex
const ESC_RE = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g;
// eslint-disable-next-line no-control-regex
const SGR_RE = /\x1b\[([0-9;]*)m/g;

/** stripAnsi(text) → the text without terminal escape sequences. */
export const stripAnsi = (text) => (text.includes("\x1b") ? text.replace(ESC_RE, "") : text);

/**
 * ansiSegments(text) → [{text, fg, bg, bold, dim, italic, underline}] for text with SGR color codes.
 * fg/bg are 0–15 (the 16 terminal colors) or null; other escape sequences are dropped.
 */
export function ansiSegments(text) {
  if (!text.includes("\x1b")) return [{ text, fg: null, bg: null, bold: false, dim: false, italic: false, underline: false }];
  const out = [];
  let st = { fg: null, bg: null, bold: false, dim: false, italic: false, underline: false };
  let at = 0;
  const push = (s) => {
    const clean = stripAnsi(s);
    if (clean) out.push({ text: clean, ...st });
  };
  SGR_RE.lastIndex = 0;
  for (let m = SGR_RE.exec(text); m; m = SGR_RE.exec(text)) {
    push(text.slice(at, m.index));
    at = m.index + m[0].length;
    const codes = m[1] === "" ? [0] : m[1].split(";").map(Number);
    st = { ...st };
    for (let i = 0; i < codes.length; i++) {
      const c = codes[i];
      if (c === 0) st = { fg: null, bg: null, bold: false, dim: false, italic: false, underline: false };
      else if (c === 1) st.bold = true;
      else if (c === 2) st.dim = true;
      else if (c === 3) st.italic = true;
      else if (c === 4) st.underline = true;
      else if (c === 22) st.bold = st.dim = false;
      else if (c === 23) st.italic = false;
      else if (c === 24) st.underline = false;
      else if (c >= 30 && c <= 37) st.fg = c - 30;
      else if (c >= 90 && c <= 97) st.fg = c - 90 + 8;
      else if (c === 39) st.fg = null;
      else if (c >= 40 && c <= 47) st.bg = c - 40;
      else if (c >= 100 && c <= 107) st.bg = c - 100 + 8;
      else if (c === 49) st.bg = null;
      else if ((c === 38 || c === 48) && codes[i + 1] === 5) {
        // 256 colors: the first 16 map onto the palette, the rest are dropped.
        const n = codes[i + 2];
        if (n < 16) st[c === 38 ? "fg" : "bg"] = n;
        i += 2;
      } else if ((c === 38 || c === 48) && codes[i + 1] === 2) i += 4; // true color: dropped
    }
  }
  push(text.slice(at));
  return out.length ? out : [{ text: "", fg: null, bg: null, bold: false, dim: false, italic: false, underline: false }];
}

// A leading timestamp the app printed itself: ISO 8601 (Spring Boot, logback, slog, zap,
// logrus full timestamps), "2026-11-05 23:12:01,123" (log4j) and "[2026-11-05 …]".
const OWN_TS_RE = /^\[?(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})(?:[.,]\d{1,9})?(?:Z|[+-]\d{2}:?\d{2}| ?UTC)?\]?(?:\s+|$)/;
// The level word right after it: "  INFO 7 --- …", "WARN …", "[ERROR] …", "level=warn".
const LEVEL_WORD_RE = /^\[?(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|SEVERE|CRITICAL|PANIC)\]?(?=\s|:|$)/i;
const LEVEL_ANY_RE = /(?:^|[\s[|])(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|SEVERE|CRITICAL)(?=[\s\]|:]|$)/;
const LEVEL_KV_RE = /\blevel=["']?(trace|debug|info|warn|warning|error|fatal|panic|critical)\b/i;
const PANIC_RE = /^(panic: |fatal error: |Exception in thread |Traceback \(most recent call last\))/;
// "org.foo.BarException: message" or "java.lang.OutOfMemoryError: Java heap space".
const EXC_HEAD_RE = /^(?:[a-zA-Z_$][\w$]*\.)+[A-Z][\w$]*(?:Exception|Error|Throwable)(?::|$)/;
// Lines that continue the message above: Java stack frames, "Caused by", "... 42 more",
// Go goroutine dumps, Python traceback frames.
const FRAME_RE = /^(?:\s+at |\tat |\s*\.\.\. \d+ (?:more|common frames omitted)|Caused by: |\s+Suppressed: |goroutine \d+ \[|\t\S+\.go:\d+|\s+File "[^"]+", line \d+|\s{4,}\S)/;
// A new run of the app: Spring Boot's "Starting XApplication … using Java 21 with PID 7".
const START_RE = /\bStarting \S+ (?:v\S+ )?using Java \d/;

const NORM = { trace: "trace", debug: "debug", info: "info", warn: "warn", warning: "warn", error: "error", fatal: "error", severe: "error", critical: "error", panic: "error" };
const norm = (w) => NORM[String(w).toLowerCase()] || "";

/** LEVELS are the levels the viewer knows, most severe first. */
export const LEVELS = ["error", "warn", "info", "debug", "trace"];

/**
 * parseLine(raw) → {text, own, level, fields, frame, exc, start}.
 * text: what to show (ANSI kept, the app's own leading timestamp removed, JSON logs as their
 * message); own: that timestamp as printed, or ""; level: one of LEVELS or ""; fields: the rest
 * of a JSON log line as "k=v …"; frame: a continuation of the message above (stack frame);
 * exc: an exception header line; start: the first line of a new run of the app.
 */
export function parseLine(raw) {
  const plain = stripAnsi(raw);
  const out = { text: raw, own: "", level: "", fields: "", frame: false, exc: false, start: false };
  const t = plain.trimStart();
  if (t.startsWith("{") && t.endsWith("}")) {
    try {
      const o = JSON.parse(t);
      if (o && typeof o === "object" && !Array.isArray(o)) {
        const lv = o.level ?? o.lvl ?? o.severity ?? o["log.level"] ?? o.levelname;
        const msg = o.msg ?? o.message ?? o.event;
        const ts = o.time ?? o.ts ?? o.timestamp ?? o["@timestamp"];
        if (lv !== undefined || msg !== undefined) {
          out.level = typeof lv === "number" ? (lv >= 50 ? "error" : lv >= 40 ? "warn" : lv >= 30 ? "info" : lv >= 20 ? "debug" : "trace") : norm(lv);
          out.text = msg === undefined ? plain : String(msg);
          if (typeof ts === "string") out.own = ts;
          const skip = new Set(["level", "lvl", "severity", "log.level", "levelname", "msg", "message", "event", "time", "ts", "timestamp", "@timestamp"]);
          out.fields = Object.entries(o).filter(([k]) => !skip.has(k)).map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`).join(" ");
          out.exc = out.level === "error" && EXC_HEAD_RE.test(out.text);
          out.start = START_RE.test(out.text);
          return out;
        }
      }
    } catch {
      // not JSON after all
    }
  }
  const m = OWN_TS_RE.exec(plain);
  let rest = plain;
  if (m) {
    out.own = m[0].trim();
    rest = plain.slice(m[0].length);
    // Keep the colors of the rest: drop the same number of visible characters from raw.
    out.text = raw === plain ? rest : dropVisible(raw, m[0].length);
    const lw = LEVEL_WORD_RE.exec(rest);
    if (lw) out.level = norm(lw[1]);
  }
  if (!out.level) {
    const kv = LEVEL_KV_RE.exec(rest);
    if (kv) out.level = norm(kv[1]);
    else if (m) {
      const any = LEVEL_ANY_RE.exec(rest.slice(0, 40));
      if (any) out.level = norm(any[1]);
    } else {
      const lw = LEVEL_WORD_RE.exec(rest);
      if (lw) out.level = norm(lw[1]);
    }
  }
  if (!out.level && PANIC_RE.test(rest)) out.level = "error";
  out.frame = !m && FRAME_RE.test(rest);
  out.exc = !m && !out.frame && EXC_HEAD_RE.test(rest);
  out.start = START_RE.test(rest);
  return out;
}

/** levelOf(text) → the line's level, or "". */
export const levelOf = (text) => parseLine(text).level;

// dropVisible(raw, n) removes the first n visible characters of raw, keeping escape sequences.
function dropVisible(raw, n) {
  let i = 0;
  let seen = 0;
  let kept = "";
  while (i < raw.length && seen < n) {
    if (raw[i] === "\x1b") {
      ESC_RE.lastIndex = i;
      const m = ESC_RE.exec(raw);
      if (m && m.index === i) {
        kept += m[0];
        i += m[0].length;
        continue;
      }
    }
    i++;
    seen++;
  }
  return kept + raw.slice(i);
}

/** findAll(text, query) → the start offsets of every case-insensitive match of query in text. */
export function findAll(text, query) {
  if (!query) return [];
  const low = text.toLowerCase();
  const out = [];
  for (let i = low.indexOf(query); i >= 0; i = low.indexOf(query, i + query.length)) out.push(i);
  return out;
}
