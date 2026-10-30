import { h, icon, clear, copyText, fmtClock } from "/assets/js/dom.js";
import { logStream } from "/assets/js/components/logstream.js";
import { toast } from "/assets/js/toast.js";

const MAX_LINES = 5000;

/** Live log: pause/resume, text filter, level filter, at most 5000 lines kept in the DOM. */
export function renderLogs(page) {
  let paused = false;
  let query = "";
  let level = "all";
  const all = []; // every line received, capped at MAX_LINES
  const pending = []; // received while paused
  let state = "connecting";

  const pre = h("pre", { class: "log tall", id: "biglog", tabindex: "0", "aria-label": "Log aplikasi", "aria-live": "off" });
  const search = h("input", { class: "f", type: "search", placeholder: "Cari di log", "aria-label": "Cari di log" });
  const seg = h(
    "div",
    { class: "seg", role: "group", "aria-label": "Tingkat" },
    [["all", "Semua"], ["w", "Warning"], ["e", "Error"]].map(([k, label]) => h("button", { type: "button", "aria-pressed": String(k === level), "data-level": k, onclick: () => { level = k; syncSeg(); repaint(); } }, label)),
  );
  const syncSeg = () => seg.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.level === level)));
  const live = h("span", { class: "chip" });
  const pauseBtn = h("button", { class: "btn sm", type: "button" });
  const copyBtn = h("button", { class: "btn sm ghost", type: "button", "aria-label": "Salin log" }, icon("copy"));

  const matches = (l) => (level === "all" || (level === "w" ? l.level === "w" || l.level === "e" : l.level === "e")) && (!query || l.text.toLowerCase().includes(query));
  const lineEl = (l) => h("span", { class: `ln ${l.level}` }, h("b", {}, fmtClock(l.time)), "  ", l.text);

  function paintState() {
    const label = paused ? "Dijeda" : state === "live" ? "Live" : state === "connecting" ? "Menyambung…" : "Terputus";
    const cls = paused ? "warn" : state === "live" ? "ok" : state === "connecting" ? "" : "err";
    live.className = `chip ${cls}`;
    live.replaceChildren(h("span", { class: `dot ${cls}` }), label + (paused && pending.length ? ` (+${pending.length})` : ""));
    pauseBtn.replaceChildren(icon(paused ? "play" : "square"), paused ? "Lanjut" : "Jeda");
  }

  function placeholder() {
    return h("span", { class: "empty-log" }, all.length ? "Tidak ada baris yang cocok dengan filter" : state === "live" ? "Belum ada log" : "Menunggu log…");
  }

  function repaint() {
    const shown = all.filter(matches);
    pre.replaceChildren(...(shown.length ? shown.map(lineEl) : [placeholder()]));
    pre.scrollTop = pre.scrollHeight;
  }

  function add(l) {
    all.push(l);
    if (all.length > MAX_LINES) all.shift();
    if (!matches(l)) return;
    const atEnd = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 40;
    if (pre.firstElementChild?.classList.contains("empty-log")) pre.replaceChildren();
    pre.append(lineEl(l));
    while (pre.childElementCount > MAX_LINES) pre.firstElementChild.remove();
    if (atEnd) pre.scrollTop = pre.scrollHeight;
  }

  let buffer = [];
  let raf = 0;
  const onLine = (l) => {
    if (paused) {
      pending.push(l);
      if (pending.length > MAX_LINES) pending.shift();
      paintState();
      return;
    }
    buffer.push(l);
    raf ||= requestAnimationFrame(() => {
      raf = 0;
      const b = buffer;
      buffer = [];
      b.forEach(add);
    });
  };

  pauseBtn.addEventListener("click", () => {
    paused = !paused;
    if (!paused) {
      const p = pending.splice(0);
      p.forEach(add);
    }
    paintState();
  });
  search.addEventListener("input", () => {
    query = search.value.trim().toLowerCase();
    repaint();
  });
  copyBtn.addEventListener("click", async () => {
    const text = all.filter(matches).map((l) => `${fmtClock(l.time)}  ${l.text}`).join("\n");
    toast((await copyText(text)) ? "Log disalin" : "Tidak bisa menyalin", text ? "ok" : "err");
  });

  const stream = logStream({ id: page.id, tail: 500, onLine, onState: (s) => { state = s; paintState(); if (!all.length) repaint(); } });
  paintState();
  repaint();

  const node = h("section", { class: "card" }, h("header", {}, h("div", { class: "logbar" }, search, seg), h("div", { class: "r" }, live, pauseBtn, copyBtn)), pre);
  return { node, cleanup: () => { cancelAnimationFrame(raf); stream.close(); } };
}
