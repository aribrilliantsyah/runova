// logView: the one log reader of the panel (tab Log, a deployment's log, Log terbaru).
// Lines are {time, text, stream, level}. It wraps long lines by default, numbers them,
// searches with highlight and next/previous, filters by level, folds stack traces, copies,
// downloads, and scrolls to the first error. Text from the server is always a text node.
import { h, icon, fill, copyText } from "/assets/js/dom.js";
import { toast } from "/assets/js/toast.js";

const MAX_LINES = 5000;
const WRAP_KEY = "runova.logwrap";

// Level words of common Java (logback, log4j, JUL), Go (slog, zap, logrus) and Python output.
const ERR_RE = /\b(ERROR|FATAL|SEVERE|CRITICAL|PANIC|panic|level=error|"level":"error")\b|^Exception in thread|^\S*(Exception|Error)(:|$)/;
const WARN_RE = /\b(WARN|WARNING|level=warn|"level":"warn")\b/;
const INFO_RE = /\b(INFO|DEBUG|TRACE|level=info|"level":"info")\b/;
// A stack frame or the "Caused by" / "... n more" lines that belong to the error above.
const FRAME_RE = /^(\s+at |\tat |\s*\.\.\. \d+ (more|common frames omitted)|Caused by: |\s+Suppressed: |goroutine \d+ \[|\s+\S+\.go:\d+)/;
// A leading ISO timestamp the app printed itself (Spring, slog): shown shortened.
const ISO_RE = /^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s+/;

export const levelOf = (text) => {
  if (ERR_RE.test(text)) return "e";
  if (WARN_RE.test(text)) return "w";
  if (/^runova\b/.test(text)) return "s";
  if (INFO_RE.test(text)) return "i";
  return "";
};

const pad = (n) => String(n).padStart(2, "0");
const clock = (ms) => {
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
};

/**
 * logView({label, tall, download, toolbarExtra}) → {node, set(lines), add(line), clear(), lines(), setEmpty(node|text), focusError()}
 * label names the log for screen readers and the download file; toolbarExtra are nodes for the right side.
 */
export function logView({ label = "Log", tall = false, fileName = "runova.log", toolbarExtra = [], compact = false } = {}) {
  let all = [];
  let query = "";
  let level = "all";
  let wrap = localStorage.getItem(WRAP_KEY) !== "0";
  let hits = [];
  let hit = -1;
  let empty = "Belum ada log.";
  let follow = true;
  const openGroups = new Set();

  const body = h("div", { class: "lv-body", tabindex: "0", role: "log", "aria-label": label, "aria-live": "off" });
  const search = h("input", { class: "f", type: "search", placeholder: "Cari di log", "aria-label": "Cari di log", autocomplete: "off", spellcheck: "false" });
  const count = h("span", { class: "lv-count", "aria-live": "polite" });
  const prevHit = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Hasil sebelumnya", title: "Hasil sebelumnya (Shift+Enter)" }, icon("chevron-up"));
  const nextHit = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Hasil berikutnya", title: "Hasil berikutnya (Enter)" }, icon("chevron-down"));
  const seg = h(
    "div",
    { class: "seg", role: "group", "aria-label": "Tingkat" },
    [["all", "Semua"], ["e", "Error"], ["w", "Warning"], ["i", "Info"]].map(([k, text]) => h("button", { type: "button", "aria-pressed": String(k === level), "data-level": k }, text)),
  );
  const wrapBtn = h("button", { class: "btn sm ghost", type: "button", "aria-pressed": String(wrap), title: "Bungkus baris panjang" }, icon("wrap-text"), h("span", { class: "lv-lbl" }, "Wrap"));
  const errBtn = h("button", { class: "btn sm ghost", type: "button", title: "Lompat ke error pertama", hidden: true }, icon("circle-alert"), h("span", { class: "lv-lbl" }, "Error pertama"));
  const copyBtn = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Salin log", title: "Salin log" }, icon("copy"));
  const dlBtn = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Unduh .log", title: "Unduh .log" }, icon("download"));

  const bar = compact
    ? null
    : h(
        "div",
        { class: "lv-bar" },
        h("div", { class: "lv-find" }, icon("search"), search, count, prevHit, nextHit),
        seg,
        h("div", { class: "lv-acts" }, ...toolbarExtra, errBtn, wrapBtn, copyBtn, dlBtn),
      );
  const node = h("div", { class: ["lv", tall ? "tall" : "", compact ? "compact" : "", wrap ? "wrap" : ""] }, bar, body);

  // Groups: an error line followed by its stack frames folds into one entry.
  function groups(lines) {
    const out = [];
    for (let i = 0; i < lines.length; i++) {
      const l = lines[i];
      const last = out[out.length - 1];
      if (last && FRAME_RE.test(l.text) && (last.head.level === "e" || last.frames.length || /Exception|Error/.test(last.head.text))) {
        last.frames.push({ ...l, n: i + 1 });
        continue;
      }
      out.push({ head: { ...l, n: i + 1 }, frames: [] });
    }
    return out;
  }

  const levelOk = (l) => level === "all" || (level === "e" ? l.level === "e" : level === "w" ? l.level === "w" : level === "i" ? l.level === "i" || l.level === "" || l.level === "s" : true);
  const textOk = (t) => !query || t.toLowerCase().includes(query);

  function mark(text) {
    if (!query) return [text];
    const out = [];
    const low = text.toLowerCase();
    let at = 0;
    for (let i = low.indexOf(query); i >= 0; i = low.indexOf(query, at)) {
      if (i > at) out.push(text.slice(at, i));
      out.push(h("mark", {}, text.slice(i, i + query.length)));
      at = i + query.length;
    }
    if (at < text.length) out.push(text.slice(at));
    return out;
  }

  function lineEl(l, frame = false) {
    // Docker's receive time is shown in the viewer's clock; the app's own leading timestamp
    // (often UTC) only repeats it, so it moves to the tooltip.
    const m = ISO_RE.exec(l.text);
    const text = m ? l.text.slice(m[0].length) : l.text;
    const ts = clock(l.time);
    return h(
      "div",
      { class: ["ln", l.level, frame ? "fr" : ""], "data-n": l.n },
      h("span", { class: "no", "aria-hidden": "true" }, String(l.n)),
      h("span", { class: "ts", title: m ? m[0].trim() : new Date(l.time).toLocaleString("id") }, ts),
      h("span", { class: "tx" }, ...mark(text)),
    );
  }

  function paint() {
    const shown = [];
    let firstErr = null;
    for (const g of groups(all)) {
      const matchFrames = query && g.frames.some((f) => textOk(f.text));
      if (!levelOk(g.head) || !(textOk(g.head.text) || matchFrames)) continue;
      const el = lineEl(g.head);
      if (g.head.level === "e" && firstErr === null) firstErr = el;
      if (!g.frames.length) {
        shown.push(el);
        continue;
      }
      const key = g.head.n;
      const open = openGroups.has(key) || Boolean(matchFrames);
      const toggle = h("button", { class: "lv-fold", type: "button", "aria-expanded": String(open) }, icon(open ? "chevron-down" : "chevron-right"), `${g.frames.length} baris stack trace`);
      toggle.addEventListener("click", () => {
        if (openGroups.has(key)) openGroups.delete(key);
        else openGroups.add(key);
        const keep = body.scrollTop;
        follow = false;
        paint();
        body.scrollTop = keep;
      });
      shown.push(h("div", { class: "grp" }, el, toggle, open ? g.frames.map((f) => lineEl(f, true)) : null));
    }
    if (!shown.length) {
      const msg = all.length ? "Tidak ada baris yang cocok dengan filter." : empty;
      fill(body, h("div", { class: "lv-empty" }, msg));
    } else {
      body.replaceChildren(...shown);
    }
    hits = query ? [...body.querySelectorAll("mark")] : [];
    if (hit >= hits.length) hit = hits.length - 1;
    count.textContent = query ? (hits.length ? `${hit + 1 > 0 ? hit + 1 : 0}/${hits.length}` : "0 hasil") : "";
    errBtn.hidden = !firstErr;
    node._firstErr = firstErr;
    if (follow) body.scrollTop = body.scrollHeight;
  }

  function goHit(d) {
    if (!hits.length) return;
    hits[hit]?.classList.remove("on");
    hit = (hit + d + hits.length) % hits.length;
    hits[hit].classList.add("on");
    hits[hit].scrollIntoView({ block: "center" });
    follow = false;
    count.textContent = `${hit + 1}/${hits.length}`;
  }

  let raf = 0;
  const schedule = () => {
    raf ||= requestAnimationFrame(() => {
      raf = 0;
      paint();
    });
  };

  search.addEventListener("input", () => {
    query = search.value.trim().toLowerCase();
    hit = -1;
    paint();
    if (query) goHit(1);
  });
  search.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      goHit(e.shiftKey ? -1 : 1);
    }
  });
  prevHit.addEventListener("click", () => goHit(-1));
  nextHit.addEventListener("click", () => goHit(1));
  seg.addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    level = b.dataset.level;
    seg.querySelectorAll("button").forEach((x) => x.setAttribute("aria-pressed", String(x === b)));
    paint();
  });
  wrapBtn.addEventListener("click", () => {
    wrap = !wrap;
    localStorage.setItem(WRAP_KEY, wrap ? "1" : "0");
    wrapBtn.setAttribute("aria-pressed", String(wrap));
    node.classList.toggle("wrap", wrap);
  });
  body.addEventListener("scroll", () => {
    follow = body.scrollHeight - body.scrollTop - body.clientHeight < 40;
  });
  const asText = () => all.map((l) => `${new Date(l.time).toISOString()} ${l.text}`).join("\n");
  copyBtn.addEventListener("click", async () => {
    const text = asText();
    if (!text) return toast("Belum ada log untuk disalin", "err");
    toast((await copyText(text)) ? `${all.length} baris disalin` : "Tidak bisa menyalin", "ok");
  });
  dlBtn.addEventListener("click", () => {
    const url = URL.createObjectURL(new Blob([asText() + "\n"], { type: "text/plain" }));
    const a = h("a", { href: url, download: fileName });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
  const focusError = () => {
    const el = node._firstErr;
    if (!el) return false;
    follow = false;
    el.scrollIntoView({ block: "center" });
    el.classList.add("flash");
    setTimeout(() => el.classList.remove("flash"), 1200);
    return true;
  };
  errBtn.addEventListener("click", focusError);

  const norm = (l) => ({ time: l.time, text: l.text, stream: l.stream, level: l.level ?? levelOf(l.text) });
  paint();
  return {
    node,
    set(lines) {
      all = lines.slice(-MAX_LINES).map(norm);
      paint();
    },
    add(l) {
      all.push(norm(l));
      if (all.length > MAX_LINES) all.shift();
      schedule();
    },
    clear() {
      all = [];
      paint();
    },
    lines: () => all,
    setEmpty(text) {
      empty = text;
      if (!all.length) paint();
    },
    focusError,
    cleanup: () => cancelAnimationFrame(raf),
  };
}

/** fromServer(l): a docker.LogLine from the API as a line for logView. */
export const fromServer = (l) => ({ time: Date.parse(l.time) || Date.now(), text: String(l.text ?? ""), stream: l.stream });
