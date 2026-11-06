// logmodel: the data behind the log viewer, without DOM. It keeps a bounded buffer of parsed
// lines, groups stack traces under the line they belong to, marks where the container started
// again, filters by level and search, and produces the flat list of rows the viewer draws.
import { parseLine, stripAnsi, findAll } from "/assets/js/components/logparse.js";

/**
 * logModel({max}) → model. Entries are {n, time, stream, text, parsed, plain}; n counts every line
 * ever pushed (stable across drops). rows() → [{kind: "dropped", count} | {kind: "restart", time,
 * source} | {kind: "line", entry, frames, open, frame}] after filters; frames > 0 on a line that
 * heads a stack trace.
 */
export function logModel({ max = 20000 } = {}) {
  let entries = [];
  let next = 1;
  let dropped = 0;
  let levels = null; // Set of levels to show; null = all
  let query = "";
  let only = false;
  const open = new Set(); // n of open stack-trace heads
  const restartAt = new Map(); // n of the first line after a restart → {time, source}
  let pendingRestart = null; // a restart the server reported before its first line
  let runs = 0; // "Starting … using Java" lines seen
  let lastStart = 0; // n of the line that started the latest run
  let cache = null;
  let matchCache = null;

  const dirty = () => {
    cache = null;
    matchCache = null;
  };

  function push(lines) {
    for (const l of lines) {
      const text = String(l.text ?? "");
      const parsed = parseLine(text);
      const e = { n: next++, time: l.time, stream: l.stream || "stdout", text, parsed, plain: stripAnsi(parsed.text) };
      if (pendingRestart) {
        restartAt.set(e.n, pendingRestart);
        pendingRestart = null;
      } else if (parsed.start) {
        runs++;
        if (runs > 1 && !nearMarker(e.n)) {
          // The new run's banner and blank lines come before "Starting": put the marker there.
          restartAt.set(bannerStart(e.n), { time: e.time, source: "log" });
        }
        lastStart = e.n;
      }
      entries.push(e);
    }
    if (entries.length > max) {
      const cut = entries.length - max;
      dropped += cut;
      entries = entries.slice(cut);
    }
    dirty();
  }

  // nearMarker: the server already marked this run (a start event within the last lines).
  function nearMarker(n) {
    for (const k of restartAt.keys()) if (k > lastStart && k <= n) return true;
    return false;
  }

  // bannerStart walks back over the blank and banner lines that open a Spring run.
  function bannerStart(n) {
    let i = entries.length - 1;
    let first = n;
    while (i >= 0 && n - entries[i].n <= 12) {
      const e = entries[i];
      if (e.parsed.own || e.parsed.frame || e.parsed.level) break;
      first = e.n;
      i--;
    }
    return first;
  }

  // groups: stack frames ("\tat …", "Caused by:", "... 4 more") fold under the line above them;
  // the exception header itself stays visible, it names the cause.
  function groups() {
    const out = [];
    let head = null;
    for (const e of entries) {
      if (head && e.parsed.frame && !restartAt.has(e.n)) {
        head.frames.push(e);
        continue;
      }
      head = { e, frames: [] };
      out.push(head);
    }
    return out;
  }

  const levelOk = (e) => !levels || levels.has(e.parsed.level || "none");
  const hit = (e) => query && e.plain.toLowerCase().includes(query);

  function rows() {
    if (cache) return cache;
    const out = [];
    if (dropped) out.push({ kind: "dropped", count: dropped });
    for (const g of groups()) {
      const marker = restartAt.get(g.e.n);
      const frameHit = query && g.frames.some(hit);
      const show = levelOk(g.e) && (!only || !query || hit(g.e) || frameHit);
      if (marker && (show || !only)) out.push({ kind: "restart", time: marker.time, source: marker.source });
      if (!show) continue;
      const isOpen = open.has(g.e.n) || Boolean(frameHit);
      out.push({ kind: "line", entry: g.e, frames: g.frames.length, open: isOpen, frame: false });
      if (isOpen) for (const f of g.frames) if (!only || !query || hit(f) || hit(g.e)) out.push({ kind: "line", entry: f, frames: 0, open: false, frame: true });
    }
    cache = out;
    return out;
  }

  return {
    push,
    /** markRestart(time): the server saw the container start again; the next line begins the run. */
    markRestart(time) {
      if (!entries.length) return;
      pendingRestart = { time, source: "event" };
    },
    rows,
    /** matches() → [{row, at}] over the visible rows: row index and offset of each hit in the text. */
    matches() {
      if (matchCache) return matchCache;
      const out = [];
      if (query) rows().forEach((r, i) => r.kind === "line" && findAll(r.entry.plain, query).forEach((at) => out.push({ row: i, at })));
      matchCache = out;
      return out;
    },
    toggle(n) {
      if (open.has(n)) open.delete(n);
      else open.add(n);
      dirty();
    },
    setLevels(set) {
      levels = set && set.size ? set : null;
      dirty();
    },
    setQuery(q) {
      query = String(q || "").toLowerCase();
      dirty();
    },
    setOnlyMatches(v) {
      only = Boolean(v);
      dirty();
    },
    query: () => query,
    clear() {
      dropped = 0;
      entries = [];
      restartAt.clear();
      open.clear();
      runs = 0;
      lastStart = 0;
      pendingRestart = null;
      dirty();
    },
    size: () => entries.length,
    dropped: () => dropped,
    entries: () => entries,
    restarts: () => restartAt.size,
    /** counts() → lines per level ("none" for lines without one). */
    counts() {
      const c = { error: 0, warn: 0, info: 0, debug: 0, trace: 0, none: 0 };
      for (const e of entries) c[e.parsed.level || "none"]++;
      return c;
    },
  };
}
