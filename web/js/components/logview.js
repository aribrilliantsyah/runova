// logView: the one log reader of the panel (tab Log, a deployment's log). Lines are
// {time, text, stream}; logmodel.js parses and groups them, this file draws them.
//
// Smooth with many lines: only the rows in view are in the DOM (windowing over measured row
// heights), new lines are applied once per animation frame, the buffer is bounded (oldest lines
// go, with a marker), and following the tail never jumps: the view sticks to the bottom only
// while the user is there, and otherwise offers "N baris baru".
//
// Text from the server is always a text node; ANSI colors become spans with classes.
import { h, icon, fill, copyText } from "/assets/js/dom.js";
import { toast } from "/assets/js/toast.js";
import { logModel } from "/assets/js/components/logmodel.js";
import { ansiSegments, LEVELS } from "/assets/js/components/logparse.js";

export { levelOf } from "/assets/js/components/logparse.js";

const MAX_LINES = 20000;
const PREFS_KEY = "runova.logview";
const OVERSCAN = 400; // px of rows drawn above and below the view
const BOTTOM_SLACK = 24; // px from the bottom that still counts as "at the tail"
const FONT_SIZES = [11, 12, 13, 14];
const LEVEL_LABEL = { error: "Error", warn: "Warning", info: "Info", debug: "Debug", trace: "Trace", none: "Tanpa level" };
const LEVEL_SHORT = { error: "ERR", warn: "WRN", info: "INF", debug: "DBG", trace: "TRC" };

function loadPrefs() {
  const def = { wrap: true, ts: true, font: 12 };
  try {
    return { ...def, ...JSON.parse(localStorage.getItem(PREFS_KEY) || "{}") };
  } catch {
    return def;
  }
}

const pad = (n, w = 2) => String(n).padStart(w, "0");
const clock = (ms) => {
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
};
const clockMs = (ms) => `${clock(ms)}.${pad(new Date(ms).getMilliseconds(), 3)}`;

/**
 * logView({label, tall, fileName, toolbarExtra}) → {node, set(lines), add(line), markRestart(time),
 * clear(), lines(), setEmpty(text), focusError(), cleanup()}.
 * label names the log for screen readers; toolbarExtra are nodes for the right of the toolbar.
 */
export function logView({ label = "Log", tall = false, fileName = "runova.log", toolbarExtra = [] } = {}) {
  const model = logModel({ max: MAX_LINES });
  const prefs = loadPrefs();
  const savePrefs = () => localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
  let empty = "Belum ada log.";
  let follow = true;
  let unseen = 0; // lines that arrived while the user was away from the tail
  let hit = -1;
  let pending = [];
  let raf = 0;
  let rows = [];
  let heights = new Float64Array(0); // measured (or estimated) height per row
  let tops = new Float64Array(1); // prefix sums: tops[i] = y of row i
  let measuredWidth = 0;
  let lineH = 0;
  let selected = new Set(); // entry n of lines picked for copying
  let anchor = null;
  let announced = 0;

  // --- toolbar ---
  const search = h("input", { class: "f", type: "search", placeholder: "Cari di log", "aria-label": "Cari di log", autocomplete: "off", spellcheck: "false" });
  const count = h("span", { class: "lv-count", "aria-live": "polite" });
  const prevHit = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Hasil sebelumnya", title: "Hasil sebelumnya (Shift+Enter)" }, icon("chevron-up"));
  const nextHit = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Hasil berikutnya", title: "Hasil berikutnya (Enter)" }, icon("chevron-down"));
  const onlyBox = h("input", { type: "checkbox" });
  const only = h("label", { class: "lv-only", title: "Tampilkan hanya baris yang cocok" }, onlyBox, h("span", {}, "Hanya cocok"));

  const levelBtn = h("button", { class: "btn sm ghost lv-lvbtn", type: "button", "aria-haspopup": "true", "aria-expanded": "false" }, icon("funnel"), h("span", { class: "lv-lvtxt" }, "Semua level"));
  const levelBoxes = {};
  const levelMenu = h(
    "div",
    { class: "lv-menu", role: "group", "aria-label": "Tampilkan level", hidden: true },
    [...LEVELS, "none"].map((lv) => {
      const box = h("input", { type: "checkbox", checked: true, value: lv });
      levelBoxes[lv] = box;
      return h("label", { class: `lv-mi lvl-${lv}` }, box, h("span", { class: "lv-badge" }, LEVEL_SHORT[lv] || "–"), h("span", { class: "lv-mitxt" }, LEVEL_LABEL[lv]), h("span", { class: "lv-mn", "data-n": lv }, "0"));
    }),
  );

  const moreBtn = h("button", { class: "btn sm ghost", type: "button", "aria-haspopup": "true", "aria-expanded": "false", "aria-label": "Tampilan dan aksi log", title: "Tampilan dan aksi" }, icon("ellipsis"));
  const tsBox = h("input", { type: "checkbox", checked: prefs.ts !== false });
  const wrapBox = h("input", { type: "checkbox", checked: prefs.wrap });
  const fontDown = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Perkecil huruf" }, "A−");
  const fontUp = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Perbesar huruf" }, "A+");
  const fontVal = h("span", { class: "lv-fontval" });
  const item = (ic, text, fn) => h("button", { class: "lv-mi lv-act", type: "button", onclick: fn }, icon(ic), h("span", {}, text));
  const copyItem = item("copy", "Salin baris terpilih", () => copySelected());
  const moreMenu = h(
    "div",
    { class: "lv-menu lv-more", hidden: true },
    h("label", { class: "lv-mi" }, tsBox, h("span", { class: "lv-mitxt" }, "Kolom waktu")),
    h("label", { class: "lv-mi" }, wrapBox, h("span", { class: "lv-mitxt" }, "Bungkus baris panjang")),
    h("div", { class: "lv-mi" }, h("span", { class: "lv-mitxt" }, "Ukuran huruf"), h("span", { class: "lv-font" }, fontDown, fontVal, fontUp)),
    h("hr"),
    copyItem,
    item("copy", "Salin semua log", () => copyAll()),
    item("download", "Unduh .log", () => download()),
    item("eraser", "Bersihkan tampilan", () => {
      model.clear();
      selected.clear();
      hit = -1;
      unseen = 0;
      follow = true;
      rebuild();
    }),
  );
  const errBtn = h("button", { class: "btn sm ghost", type: "button", title: "Lompat ke error pertama", hidden: true }, icon("circle-alert"), h("span", { class: "lv-lbl" }, "Error pertama"));

  const bar = h(
    "div",
    { class: "lv-bar" },
    h("div", { class: "lv-find" }, icon("search"), search, count, prevHit, nextHit),
    only,
    h("div", { class: "lv-pop" }, levelBtn, levelMenu),
    h("div", { class: "lv-acts" }, ...toolbarExtra, errBtn, h("div", { class: "lv-pop" }, moreBtn, moreMenu)),
  );

  // --- body: a scroller with a sized spacer and the visible rows ---
  const spacer = h("div", { class: "lv-spacer" });
  const win = h("div", { class: "lv-win" });
  const body = h("div", { class: "lv-body", tabindex: "0", role: "log", "aria-label": label, "aria-live": "off" }, spacer, win);
  const emptyEl = h("div", { class: "lv-empty", hidden: true });
  const jump = h("button", { class: "lv-jump", type: "button", hidden: true }, icon("arrow-down-to-line"), h("span"));
  const live = h("div", { class: "sr-only", "aria-live": "polite" });
  const node = h("div", { class: ["lv", tall ? "tall" : ""] }, bar, h("div", { class: "lv-stage" }, body, emptyEl, jump), live);

  function applyPrefs() {
    node.classList.toggle("wrap", prefs.wrap);
    node.style.setProperty("--lv-font", `${prefs.font}px`);
    fontVal.textContent = `${prefs.font}px`;
    tsBox.checked = prefs.ts !== false;
    wrapBox.checked = prefs.wrap;
  }
  // One aligned time column (Docker's receive time, the viewer's clock). The app's own leading
  // timestamp is cut from the text (logparse) and kept in the tooltip, so no line shows two.
  const showTs = () => prefs.ts !== false;

  // --- rows ---
  function segs(text) {
    return ansiSegments(text).map((s) => {
      if (s.fg === null && s.bg === null && !s.bold && !s.dim && !s.italic && !s.underline) return s.text;
      const cls = [s.fg !== null ? `af${s.fg}` : "", s.bg !== null ? `ab${s.bg}` : "", s.bold ? "ab" : "", s.dim ? "ad" : "", s.italic ? "ai" : "", s.underline ? "au" : ""].filter(Boolean).join(" ");
      return h("span", { class: cls }, s.text);
    });
  }

  function marked(text, rowIndex) {
    const q = model.query();
    if (!q) return segs(text);
    // Marks work on the plain text; colors are dropped on lines that match.
    const plain = rows[rowIndex].entry.plain;
    const low = plain.toLowerCase();
    const out = [];
    let at = 0;
    const cur = hit >= 0 ? model.matches()[hit] : null;
    for (let i = low.indexOf(q); i >= 0; i = low.indexOf(q, at)) {
      if (i > at) out.push(plain.slice(at, i));
      const on = cur && cur.row === rowIndex && cur.at === i;
      out.push(h("mark", { class: on ? "on" : null }, plain.slice(i, i + q.length)));
      at = i + q.length;
    }
    if (at === 0) return segs(text);
    if (at < plain.length) out.push(plain.slice(at));
    return out;
  }

  function rowEl(i) {
    const r = rows[i];
    if (r.kind === "dropped") return h("div", { class: "lv-note", "data-i": i }, `${r.count.toLocaleString("id")} baris lama dibuang dari tampilan (batas ${MAX_LINES.toLocaleString("id")} baris). Unduh .log hanya berisi yang tersisa.`);
    if (r.kind === "restart") return h("div", { class: "lv-restart", "data-i": i, role: "separator" }, h("span", {}, `Container dimulai ulang ${clock(r.time)}`));
    const e = r.entry;
    const p = e.parsed;
    const blank = !e.plain.trim();
    const lv = p.level;
    const cls = ["ln", lv ? `lvl-${lv}` : "", r.frame ? "fr" : "", blank ? "blank" : "", selected.has(e.n) ? "sel" : "", e.stream === "stderr" ? "err-stream" : ""];
    const ts = h("span", { class: "ts", title: p.own ? `Docker ${new Date(e.time).toLocaleString("id")} · aplikasi ${p.own}` : new Date(e.time).toLocaleString("id") }, blank ? "" : clockMs(e.time));
    const badge = lv && !r.frame ? h("span", { class: "lv-badge" }, LEVEL_SHORT[lv]) : null;
    const text = h("span", { class: "tx" }, badge, ...marked(p.text, i), p.fields ? h("span", { class: "lv-fields" }, ` ${p.fields}`) : null);
    const el = h("div", { class: cls, "data-i": i, "data-n": e.n }, h("span", { class: "no", "aria-hidden": "true" }, String(e.n)), ts, text);
    if (r.frames) {
      const t = h("button", { class: "lv-fold", type: "button", "aria-expanded": String(r.open), "data-fold": e.n }, icon(r.open ? "chevron-down" : "chevron-right"), `${r.frames} baris stack trace`);
      el.append(t);
    }
    return el;
  }

  // --- layout: heights and the window ---
  function estimate(i) {
    const r = rows[i];
    if (r.kind !== "line") return lineH * 1.6;
    return r.frames ? lineH * 2 : lineH;
  }

  function rebuild(keepFollow = true) {
    rows = model.rows();
    heights = new Float64Array(rows.length);
    for (let i = 0; i < rows.length; i++) heights[i] = estimate(i);
    sumTops();
    node.classList.toggle("no-ts", !showTs());
    paintEmpty();
    paintCounts();
    draw(keepFollow);
  }

  function sumTops() {
    tops = new Float64Array(rows.length + 1);
    for (let i = 0; i < rows.length; i++) tops[i + 1] = tops[i] + heights[i];
    spacer.style.height = `${tops[rows.length]}px`;
  }

  const rowAt = (y) => {
    let lo = 0;
    let hi = rows.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (tops[mid + 1] <= y) lo = mid + 1;
      else hi = mid;
    }
    return Math.min(lo, Math.max(rows.length - 1, 0));
  };

  function draw(stick = follow) {
    if (!lineH) measureLine();
    if (stick) body.scrollTop = body.scrollHeight;
    const top = body.scrollTop;
    const view = body.clientHeight || 400;
    const first = rowAt(Math.max(0, top - OVERSCAN));
    const last = rowAt(top + view + OVERSCAN);
    const els = [];
    for (let i = first; i <= last && i < rows.length; i++) els.push(rowEl(i));
    win.replaceChildren(...els);
    win.style.transform = `translateY(${tops[first] || 0}px)`;
    // Measure what is drawn; correct heights that the estimate missed.
    let changed = false;
    let shiftAbove = 0;
    for (const el of els) {
      const i = Number(el.dataset.i);
      const hgt = el.offsetHeight;
      if (hgt && Math.abs(hgt - heights[i]) > 0.5) {
        if (tops[i] < top) shiftAbove += hgt - heights[i];
        heights[i] = hgt;
        changed = true;
      }
    }
    if (changed) {
      sumTops();
      win.style.transform = `translateY(${tops[first] || 0}px)`;
      if (stick) body.scrollTop = body.scrollHeight;
      else if (shiftAbove) body.scrollTop = top + shiftAbove; // keep what the user reads in place
    }
    paintJump();
  }

  function measureLine() {
    const probe = h("div", { class: "ln" }, h("span", { class: "no" }, "1"), h("span", { class: "ts" }, "00:00:00.000"), h("span", { class: "tx" }, "x"));
    win.replaceChildren(probe);
    lineH = probe.offsetHeight || 19;
    measuredWidth = body.clientWidth;
  }

  // --- state painting ---
  function paintEmpty() {
    const none = rows.every((r) => r.kind !== "line");
    emptyEl.hidden = !none;
    if (none) fill(emptyEl, model.size() ? "Tidak ada baris yang cocok dengan filter." : empty);
  }

  function paintCounts() {
    const c = model.counts();
    for (const lv of [...LEVELS, "none"]) levelMenu.querySelector(`[data-n="${lv}"]`).textContent = c[lv].toLocaleString("id");
    const on = [...LEVELS, "none"].filter((lv) => levelBoxes[lv].checked);
    levelBtn.querySelector(".lv-lvtxt").textContent = on.length === LEVELS.length + 1 ? "Semua level" : on.length ? on.map((lv) => LEVEL_LABEL[lv]).join(", ") : "Tidak ada level";
    levelBtn.classList.toggle("on", on.length !== LEVELS.length + 1);
    const m = model.matches();
    if (hit >= m.length) hit = m.length - 1;
    count.textContent = model.query() ? (m.length ? `${hit >= 0 ? hit + 1 : 0}/${m.length.toLocaleString("id")}` : "0 hasil") : "";
    const firstErr = rows.findIndex((r) => r.kind === "line" && r.entry.parsed.level === "error");
    errBtn.hidden = firstErr < 0;
    copyItem.hidden = !selected.size;
  }

  function paintJump() {
    const show = !follow && unseen > 0;
    jump.hidden = !show;
    if (show) jump.lastChild.textContent = `${unseen.toLocaleString("id")} baris baru`;
  }

  function atBottom() {
    return body.scrollHeight - body.scrollTop - body.clientHeight < BOTTOM_SLACK;
  }

  // --- streaming: one batch per frame ---
  function flush() {
    raf = 0;
    if (!pending.length) return;
    const batch = pending;
    pending = [];
    model.push(batch);
    if (!follow) unseen += batch.length;
    else unseen = 0;
    rebuild(follow);
    announce(batch);
  }

  // Screen readers hear a short summary at most every few seconds, not every line.
  function announce(batch) {
    const now = Date.now();
    const errs = batch.filter((l) => /\b(ERROR|FATAL|Exception)\b/.test(l.text)).length;
    if (now - announced < 5000) return;
    announced = now;
    live.textContent = `${batch.length} baris baru${errs ? `, ${errs} error` : ""}`;
  }

  // --- search ---
  function goHit(d) {
    const m = model.matches();
    if (!m.length) return;
    hit = (hit + d + m.length) % m.length;
    follow = false;
    const r = m[hit].row;
    const y = tops[r] - body.clientHeight / 2 + heights[r] / 2;
    body.scrollTop = Math.max(0, y);
    draw(false);
    paintCounts();
  }

  search.addEventListener("input", () => {
    model.setQuery(search.value.trim());
    hit = -1;
    rebuild(false);
    if (model.query()) goHit(1);
    else if (follow) draw(true);
  });
  search.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      goHit(e.shiftKey ? -1 : 1);
    } else if (e.key === "Escape" && search.value) {
      search.value = "";
      search.dispatchEvent(new Event("input"));
    }
  });
  prevHit.addEventListener("click", () => goHit(-1));
  nextHit.addEventListener("click", () => goHit(1));
  onlyBox.addEventListener("change", () => {
    model.setOnlyMatches(onlyBox.checked);
    hit = -1;
    rebuild(false);
  });

  // --- menus ---
  function popup(btn, menu) {
    const close = () => {
      menu.hidden = true;
      btn.setAttribute("aria-expanded", "false");
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("keydown", esc, true);
    };
    const outside = (e) => {
      if (!menu.contains(e.target) && !btn.contains(e.target)) close();
    };
    const esc = (e) => {
      if (e.key === "Escape") {
        close();
        btn.focus();
      }
    };
    btn.addEventListener("click", () => {
      if (!menu.hidden) return close();
      menu.hidden = false;
      btn.setAttribute("aria-expanded", "true");
      document.addEventListener("pointerdown", outside, true);
      document.addEventListener("keydown", esc, true);
      menu.querySelector("input,select,button")?.focus();
    });
    return close;
  }
  const closeLevel = popup(levelBtn, levelMenu);
  const closeMore = popup(moreBtn, moreMenu);
  levelMenu.addEventListener("change", () => {
    const on = new Set([...LEVELS, "none"].filter((lv) => levelBoxes[lv].checked));
    model.setLevels(on.size === LEVELS.length + 1 ? null : on);
    hit = -1;
    rebuild(false);
  });
  tsBox.addEventListener("change", () => {
    prefs.ts = tsBox.checked;
    savePrefs();
    relayout();
  });
  wrapBox.addEventListener("change", () => {
    prefs.wrap = wrapBox.checked;
    savePrefs();
    relayout();
  });
  const setFont = (d) => {
    const i = Math.max(0, Math.min(FONT_SIZES.length - 1, FONT_SIZES.indexOf(prefs.font) + d));
    prefs.font = FONT_SIZES[i] || 12;
    savePrefs();
    relayout();
  };
  fontDown.addEventListener("click", () => setFont(-1));
  fontUp.addEventListener("click", () => setFont(1));

  // Layout changed: keep the row at the top of the view where it is.
  function relayout() {
    const keep = follow ? null : rowAt(body.scrollTop);
    applyPrefs();
    lineH = 0;
    measureLine();
    rebuild(follow);
    if (keep !== null) {
      body.scrollTop = tops[keep];
      draw(false);
    }
  }

  // --- scrolling and follow ---
  let drawQueued = 0;
  body.addEventListener("scroll", () => {
    follow = atBottom();
    if (follow) unseen = 0;
    drawQueued ||= requestAnimationFrame(() => {
      drawQueued = 0;
      draw(false);
    });
  }, { passive: true });
  jump.addEventListener("click", () => {
    follow = true;
    unseen = 0;
    draw(true);
    body.focus({ preventScroll: true });
  });
  body.addEventListener("keydown", (e) => {
    if (e.key === "End") {
      e.preventDefault();
      jump.click();
    } else if (e.key === "Home") {
      e.preventDefault();
      follow = false;
      body.scrollTop = 0;
    } else if ((e.ctrlKey || e.metaKey) && e.key === "f") {
      e.preventDefault();
      search.focus();
    }
  });
  const ro = new ResizeObserver(() => {
    if (Math.abs(body.clientWidth - measuredWidth) > 1 && prefs.wrap) relayout();
    else draw(follow);
  });
  ro.observe(body);

  // --- clicks: fold, select lines ---
  win.addEventListener("click", (e) => {
    const fold = e.target.closest(".lv-fold");
    if (fold) {
      follow = false;
      const keep = body.scrollTop;
      model.toggle(Number(fold.dataset.fold));
      rebuild(false);
      body.scrollTop = keep;
      draw(false);
      return;
    }
    const no = e.target.closest(".no");
    if (!no) return;
    const n = Number(no.parentElement.dataset.n);
    if (e.shiftKey && anchor !== null) {
      const [a, b] = anchor < n ? [anchor, n] : [n, anchor];
      for (const en of model.entries()) if (en.n >= a && en.n <= b) selected.add(en.n);
    } else if (selected.has(n) && selected.size === 1) {
      selected.clear();
    } else {
      if (!(e.ctrlKey || e.metaKey)) selected.clear();
      selected.add(n);
    }
    anchor = n;
    paintCounts();
    draw(false);
  });

  // --- copy and download ---
  const asText = (list) => list.map((en) => `${new Date(en.time).toISOString()} ${en.text}`).join("\n");
  async function copySelected() {
    closeMore();
    const list = model.entries().filter((en) => selected.has(en.n));
    if (!list.length) return toast("Pilih baris dulu: klik nomor baris, Shift+klik untuk rentang", "err");
    toast((await copyText(asText(list).replace(/\x1b\[[0-9;]*m/g, ""))) ? `${list.length} baris disalin` : "Tidak bisa menyalin", "ok");
  }
  async function copyAll() {
    closeMore();
    const list = model.entries();
    if (!list.length) return toast("Belum ada log untuk disalin", "err");
    toast((await copyText(asText(list).replace(/\x1b\[[0-9;]*m/g, ""))) ? `${list.length.toLocaleString("id")} baris disalin` : "Tidak bisa menyalin", "ok");
  }
  function download() {
    closeMore();
    const url = URL.createObjectURL(new Blob([asText(model.entries()).replace(/\x1b\[[0-9;]*m/g, "") + "\n"], { type: "text/plain" }));
    const a = h("a", { href: url, download: fileName });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  const focusError = () => {
    const i = rows.findIndex((r) => r.kind === "line" && r.entry.parsed.level === "error");
    if (i < 0) return false;
    follow = false;
    body.scrollTop = Math.max(0, tops[i] - body.clientHeight / 3);
    draw(false);
    const el = win.querySelector(`[data-i="${i}"]`);
    el?.classList.add("flash");
    setTimeout(() => el?.classList.remove("flash"), 1200);
    return true;
  };
  errBtn.addEventListener("click", focusError);

  applyPrefs();
  rebuild();
  return {
    node,
    set(lines) {
      cancelAnimationFrame(raf);
      raf = 0;
      pending = [];
      model.clear();
      selected.clear();
      model.push(lines);
      follow = true;
      unseen = 0;
      hit = -1;
      rebuild(true);
    },
    add(l) {
      pending.push(l);
      raf ||= requestAnimationFrame(flush);
    },
    /** markRestart(time): the container started again; the next line opens a new run. */
    markRestart(time) {
      if (pending.length) flush();
      model.markRestart(time);
    },
    clear() {
      pending = [];
      model.clear();
      selected.clear();
      follow = true;
      unseen = 0;
      rebuild(true);
    },
    lines: () => model.entries(),
    setEmpty(text) {
      empty = text;
      paintEmpty();
    },
    focusError,
    cleanup() {
      cancelAnimationFrame(raf);
      cancelAnimationFrame(drawQueued);
      ro.disconnect();
      closeLevel();
      closeMore();
    },
  };
}

/** fromServer(l): a docker.LogLine from the API as a line for logView. */
export const fromServer = (l) => ({ time: Date.parse(l.time) || Date.now(), text: String(l.text ?? ""), stream: l.stream });
