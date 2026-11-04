// Event volume over time as an SVG bar chart drawn at the real pixel width.
// Hover or arrow keys show a tooltip; click a bar or drag across bars to narrow the range
// (onSelect(from, to)); Enter zooms into the focused bar. Failed entries are stacked on top
// in the error colour, so a burst of refused logins stands out.
import { h, fmtNum, fill } from "/assets/js/dom.js";

const SVG_NS = "http://www.w3.org/2000/svg";
const s = (tag, attrs = {}) => {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, String(v));
  return el;
};

const AXIS = 18;
const pad2 = (n) => String(n).padStart(2, "0");
const clock = (ms) => {
  const d = new Date(ms);
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
};
const day = (ms) => new Date(ms).toLocaleDateString("id", { day: "numeric", month: "short" });

/** bucketLabel(start, size) → "4 Nov 10:00–10:30", "4 Nov" for day buckets. */
export function bucketLabel(start, size) {
  if (size >= 86_400_000) {
    const end = start + size - 1;
    return size > 86_400_000 ? `${day(start)} – ${day(end)}` : day(start);
  }
  const sec = size < 60_000;
  const t = (ms) => (sec ? `${clock(ms)}:${pad2(new Date(ms).getSeconds())}` : clock(ms));
  return `${day(start)} ${t(start)}–${t(start + size)}`;
}

/** Round axis ticks from 0 that cover max in about 2 steps. */
function ticks(max) {
  if (!(max > 0)) return [0, 1];
  const raw = max / 2;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((x) => x >= raw);
  const out = [];
  for (let v = 0; v < max + step * 0.999; v += step) out.push(Math.round(v * 100) / 100);
  return out;
}

/**
 * histogram({height, onSelect}) → {el, set(histogram, {loading, error}), destroy()}.
 * histogram is the API's {from, to, bucket_ms, buckets: [{start, count, failed}]}.
 */
export function histogram({ height = 96, onSelect }) {
  const plot = h("div", { class: "hg-plot", tabindex: "0", role: "img" });
  plot.style.setProperty("height", `${height}px`);
  const tip = h("div", { class: "hg-tip", role: "status", hidden: true });
  const sum = h("span", { class: "hg-sum" });
  const hint = h("span", { class: "hg-hint" }, "Klik atau seret batang untuk mempersempit rentang");
  const el = h("div", { class: "hg" }, h("div", { class: "hg-head" }, sum, h("span", { class: "spacer" }), hint), plot, tip);
  let data = null;
  let svg = null;
  let geo = { gutter: 0, bw: 0, n: 0 };
  let cur = -1;
  let drag = null; // {a, b} bucket indexes
  let sel = null;
  let hl = null;

  const count = (i) => data.buckets[i].count;
  function draw() {
    svg?.remove();
    if (!data || !data.buckets.length) return;
    const W = plot.clientWidth;
    if (!W) return;
    const n = data.buckets.length;
    const peak = Math.max(0, ...data.buckets.map((b) => b.count));
    const tk = ticks(peak);
    const top = tk[tk.length - 1];
    const gutter = Math.ceil(Math.max(...tk.map((t) => fmtNum(t).length)) * 6.4) + 10;
    const bw = (W - gutter) / n;
    const barW = Math.max(1, Math.min(28, bw - (bw > 4 ? 2 : 0.5)));
    const plotH = height - AXIS;
    const y = (v) => plotH - (v / top) * (plotH - 4);
    geo = { gutter, bw, n };
    svg = s("svg", { width: W, height, viewBox: `0 0 ${W} ${height}`, "aria-hidden": "true" });
    for (const t of tk) {
      const yy = Math.round(y(t)) + 0.5;
      svg.append(s("line", { class: t ? "hg-grid" : "hg-base", x1: gutter, x2: W, y1: yy, y2: yy }));
      const lab = s("text", { class: "hg-axis", x: gutter - 6, y: yy + 3.5, "text-anchor": "end" });
      lab.textContent = fmtNum(t);
      svg.append(lab);
    }
    sel = s("rect", { class: "hg-sel", y: 0, height: plotH, visibility: "hidden" });
    hl = s("rect", { class: "hg-hl", y: 0, height: plotH, width: bw, rx: 3, visibility: "hidden" });
    svg.append(sel, hl);
    data.buckets.forEach((b, i) => {
      if (!b.count) return;
      const x = gutter + i * bw + (bw - barW) / 2;
      const okH = Math.max(0, y(0) - y(b.count - b.failed));
      const allH = Math.max(1, y(0) - y(b.count));
      if (b.count - b.failed > 0) svg.append(s("rect", { class: "hg-bar", x, y: y(0) - okH, width: barW, height: Math.max(okH, 1), rx: barW > 6 ? 2 : 0 }));
      if (b.failed) svg.append(s("rect", { class: "hg-bar err", x, y: y(0) - allH, width: barW, height: Math.max(allH - okH, 1.5), rx: barW > 6 ? 2 : 0 }));
    });
    const marks = W - gutter < 420 ? [0, n - 1] : [0, Math.floor(n / 3), Math.floor((2 * n) / 3), n - 1];
    const size = data.bucket_ms;
    const spansDays = data.to - data.from > 20 * 3_600_000;
    [...new Set(marks)].forEach((i, k, arr) => {
      const anchor = k === 0 ? "start" : k === arr.length - 1 ? "end" : "middle";
      const ms = anchor === "end" ? data.buckets[i].start + size : data.buckets[i].start;
      const t = s("text", { class: "hg-axis", x: anchor === "start" ? gutter : anchor === "end" ? W : gutter + i * bw + bw / 2, y: height - 4, "text-anchor": anchor });
      t.textContent = size >= 86_400_000 ? day(ms) : spansDays ? `${day(ms)} ${clock(ms)}` : clock(ms);
      svg.append(t);
    });
    plot.prepend(svg);
    if (cur >= 0) show(cur);
  }

  function show(i) {
    if (!data || i < 0 || i >= data.buckets.length || !hl) return;
    cur = i;
    const b = data.buckets[i];
    hl.setAttribute("x", geo.gutter + i * geo.bw);
    hl.setAttribute("visibility", "visible");
    fill(tip,
      h("div", { class: "hg-tip-t" }, bucketLabel(b.start, data.bucket_ms)),
      h("div", {}, h("b", {}, fmtNum(b.count)), " event"),
      b.failed ? h("div", { class: "hg-tip-err" }, h("b", {}, fmtNum(b.failed)), " gagal") : null,
      onSelect && b.count ? h("div", { class: "hg-tip-h" }, "Klik untuk memperbesar") : null,
    );
    tip.hidden = false;
    const x = geo.gutter + (i + 1) * geo.bw;
    const W = plot.clientWidth;
    const left = x + 10 + tip.offsetWidth > W ? Math.max(0, geo.gutter + i * geo.bw - tip.offsetWidth - 10) : x + 10;
    tip.style.setProperty("left", `${left}px`);
  }
  function hide() {
    tip.hidden = true;
    hl?.setAttribute("visibility", "hidden");
  }
  const at = (ev) => {
    const i = Math.floor((ev.clientX - plot.getBoundingClientRect().left - geo.gutter) / geo.bw);
    return data && i >= 0 && i < geo.n ? i : -1;
  };
  const paintSel = () => {
    if (!sel) return;
    if (!drag || drag.a === drag.b) return sel.setAttribute("visibility", "hidden");
    const [a, b] = [Math.min(drag.a, drag.b), Math.max(drag.a, drag.b)];
    sel.setAttribute("x", geo.gutter + a * geo.bw);
    sel.setAttribute("width", (b - a + 1) * geo.bw);
    sel.setAttribute("visibility", "visible");
  };
  const select = (a, b) => {
    if (!onSelect || !data) return;
    const [lo, hi] = [Math.min(a, b), Math.max(a, b)];
    onSelect(data.buckets[lo].start, Math.min(data.buckets[hi].start + data.bucket_ms, data.to));
  };

  plot.addEventListener("pointerdown", (ev) => {
    const i = at(ev);
    if (i < 0 || ev.button !== 0) return;
    drag = { a: i, b: i };
    plot.setPointerCapture(ev.pointerId);
  });
  plot.addEventListener("pointermove", (ev) => {
    const i = at(ev);
    if (drag && i >= 0) {
      drag.b = i;
      paintSel();
    }
    if (i < 0) hide();
    else show(i);
  });
  plot.addEventListener("pointerup", () => {
    if (!drag) return;
    const d = drag;
    drag = null;
    paintSel();
    if (d.a !== d.b || count(d.a) > 0) select(d.a, d.b);
  });
  plot.addEventListener("pointerleave", () => !drag && hide());
  plot.addEventListener("focus", () => data?.buckets.length && show(cur < 0 ? data.buckets.length - 1 : cur));
  plot.addEventListener("blur", hide);
  plot.addEventListener("keydown", (e) => {
    if (!data?.buckets.length) return;
    const n = data.buckets.length;
    const moves = { ArrowLeft: -1, ArrowRight: 1, Home: -n, End: n };
    if (e.key in moves) {
      e.preventDefault();
      show(Math.max(0, Math.min(n - 1, (cur < 0 ? n - 1 : cur) + moves[e.key])));
    } else if (e.key === "Enter" && cur >= 0) {
      e.preventDefault();
      select(cur, cur);
    }
  });

  const ro = new ResizeObserver(draw);
  ro.observe(plot);
  return {
    el,
    set(hist, { total = 0, loading = false, error = "" } = {}) {
      plot.classList.toggle("loading", loading);
      if (loading) return;
      data = hist;
      cur = -1;
      hide();
      plot.querySelector(".hg-empty")?.remove();
      const n = hist?.buckets?.length || 0;
      const failed = n ? hist.buckets.reduce((a, b) => a + b.failed, 0) : 0;
      fill(sum, error ? h("span", { class: "errtext" }, error) : n ? [h("b", {}, fmtNum(total)), " event", failed ? [", ", h("span", { class: "hg-sum-err" }, `${fmtNum(failed)} gagal`)] : null] : null);
      hint.hidden = !n || !onSelect;
      plot.setAttribute("aria-label", n ? `Jumlah event per ${bucketLabel(0, hist.bucket_ms).includes("–") ? "interval" : "hari"}, ${fmtNum(total)} total. Panah kiri dan kanan untuk membaca tiap batang.` : "Tidak ada event");
      if (!n) {
        svg?.remove();
        svg = null;
        plot.append(h("div", { class: "hg-empty" }, error ? "Grafik tidak bisa dimuat" : "Tidak ada event di rentang ini"));
        return;
      }
      draw();
    },
    destroy: () => ro.disconnect(),
  };
}
