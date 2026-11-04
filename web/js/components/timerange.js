// Time range picker: quick ranges (15 menit … 30 hari), "Semua" and a custom from/to.
// The URL keeps the key ("24h") or, for custom, from/to as local "YYYY-MM-DDTHH:MM".
import { h, icon } from "/assets/js/dom.js";

export const RANGES = [
  ["15m", "15 menit terakhir", 15 * 60e3],
  ["1h", "1 jam terakhir", 3600e3],
  ["24h", "24 jam terakhir", 86400e3],
  ["7d", "7 hari terakhir", 7 * 86400e3],
  ["30d", "30 hari terakhir", 30 * 86400e3],
  ["all", "Semua waktu", 0],
  ["custom", "Kustom", 0],
];
const SPAN = Object.fromEntries(RANGES.map(([k, , ms]) => [k, ms]));

const pad = (n) => String(n).padStart(2, "0");
/** toLocalInput(ms) → "2026-11-04T10:42" in the browser's zone (for datetime-local). */
export function toLocalInput(ms) {
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
/** fromLocalInput("2026-11-04T10:42") → ms, NaN when invalid. */
export const fromLocalInput = (v) => (v ? new Date(v).getTime() : NaN);

/**
 * window({range, from, to}, now) → {from, to} in ms; 0 means unbounded. Custom "to" is inclusive
 * of its minute. An unknown or incomplete custom range falls back to the last 24 hours.
 */
export function timeWindow(p, now = Date.now()) {
  if (p.range === "all") return { from: 0, to: 0 };
  if (p.range === "custom") {
    const f = fromLocalInput(p.from);
    const t = fromLocalInput(p.to);
    if (Number.isFinite(f) && Number.isFinite(t) && t + 60e3 > f) return { from: f, to: t + 60e3 };
  }
  return { from: now - (SPAN[p.range] || SPAN["24h"]), to: 0 };
}

/** rangeControl(onChange) → {el, sync(state)}; onChange gets the state patch. */
export function rangeControl(onChange) {
  let cur = {};
  const sel = h("select", { class: "f rc-sel", "aria-label": "Rentang waktu" }, RANGES.map(([k, label]) => h("option", { value: k }, label)));
  const from = h("input", { type: "datetime-local", class: "f rc-dt", "aria-label": "Dari" });
  const to = h("input", { type: "datetime-local", class: "f rc-dt", "aria-label": "Sampai" });
  const custom = h("span", { class: "rc-custom" }, from, h("span", { class: "muted" }, "sampai"), to);
  sel.addEventListener("change", () => {
    if (sel.value === "custom") {
      const w = timeWindow(cur);
      onChange({ range: "custom", from: toLocalInput(w.from || Date.now() - 86400e3), to: toLocalInput(w.to ? w.to - 60e3 : Date.now()) });
    } else onChange({ range: sel.value, from: "", to: "" });
  });
  const push = () => Number.isFinite(fromLocalInput(from.value)) && Number.isFinite(fromLocalInput(to.value)) && onChange({ range: "custom", from: from.value, to: to.value });
  from.addEventListener("change", push);
  to.addEventListener("change", push);
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return {
    el: h("div", { class: "rc", title: `Waktu ditampilkan dalam zona ${tz}` }, h("span", { class: "rc-ic", "aria-hidden": "true" }, icon("clock")), sel, custom),
    sync(p) {
      cur = p;
      sel.value = p.range;
      custom.hidden = p.range !== "custom";
      from.value = p.from || "";
      to.value = p.to || "";
    },
  };
}
