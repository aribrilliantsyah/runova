// DOM helpers. Text from the server only ever reaches the page through
// h() children (text nodes) or textContent, never through innerHTML.

const SVG_NS = "http://www.w3.org/2000/svg";
const SPRITE = "/assets/vendor/icons.svg";
const UNSAFE_URL = /^\s*(?:javascript|data|vbscript):/i;
const URL_ATTRS = new Set(["href", "src", "action", "formaction", "xlink:href"]);

function setAttr(el, key, value) {
  if (key.startsWith("aria-")) {
    if (value != null) el.setAttribute(key, String(value));
    return;
  }
  if (value == null || value === false) return;
  if (key === "class") {
    el.className = Array.isArray(value) ? value.filter(Boolean).join(" ") : value;
  } else if (key === "dataset") {
    Object.assign(el.dataset, value);
  } else if (key === "style") {
    // A style attribute is blocked by the CSP; an object goes through the CSSOM.
    if (typeof value === "object") for (const [p, v] of Object.entries(value)) el.style.setProperty(p, v);
  } else if (key.startsWith("on")) {
    // Only functions become listeners; a string would be an inline handler.
    if (typeof value === "function") el.addEventListener(key.slice(2).toLowerCase(), value);
  } else if (key === "checked" || key === "indeterminate") {
    el[key] = Boolean(value);
  } else if (URL_ATTRS.has(key) && UNSAFE_URL.test(String(value))) {
    // refuse script URLs
  } else {
    el.setAttribute(key, value === true ? "" : String(value));
  }
}

function append(el, children) {
  for (const c of children) {
    if (c == null || c === false || c === true) continue;
    if (Array.isArray(c)) append(el, c);
    else el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

/** h(tag, attrs, ...children): build an element. Strings are text, never markup. */
export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  let value;
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === "value") value = v; // select/option: applied after the children exist
    else setAttr(el, k, v);
  }
  append(el, children);
  if (value != null) el.value = value;
  return el;
}

/** icon(name): a Lucide icon from the sprite. */
export function icon(name, cls = "") {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", `lucide lucide-${name} ${cls}`.trim());
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS(SVG_NS, "use");
  use.setAttribute("href", `${SPRITE}#${name}`);
  svg.append(use);
  return svg;
}

export function clear(el) {
  el.replaceChildren();
  return el;
}

/** fill(el, ...children): replace el's content; null/false are skipped and arrays flattened (Element.append would print "null"). */
export function fill(el, ...children) {
  el.replaceChildren();
  append(el, children);
  return el;
}

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

const nf = new Intl.NumberFormat("id", { maximumFractionDigits: 2 });
const rtf = new Intl.RelativeTimeFormat("id", { numeric: "auto" });

/** fmtNum(1.5) → "1,5" */
export const fmtNum = (n) => nf.format(n);

/** fmtBytes(44668928) → "42,6 MB" */
export function fmtBytes(n) {
  if (!Number.isFinite(n) || n < 0) return "–";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${new Intl.NumberFormat("id", { maximumFractionDigits: i === 0 ? 0 : 1 }).format(n)} ${units[i]}`;
}

/** fmtMB(1536) → "1,5 GB", fmtMB(512) → "512 MB" */
export function fmtMB(mb) {
  return mb >= 1024 ? `${fmtNum(mb / 1024)} GB` : `${fmtNum(mb)} MB`;
}

/** fmtCPU(500) → "0,5 core" */
export const fmtCPU = (milli) => `${fmtNum(milli / 1000)} core`;

/** fmtTime(ms): relative time, "12 menit lalu", "kemarin", or a date once it is old. */
export function fmtTime(ms, now = Date.now()) {
  if (!ms) return "–";
  const diff = Math.round((now - ms) / 1000);
  if (diff < 45) return "baru saja";
  const rel = (v, unit) => rtf.format(-v, unit).replace(" yang lalu", " lalu");
  if (diff < 3600) return rel(Math.round(diff / 60), "minute");
  if (diff < 86400) return rel(Math.round(diff / 3600), "hour");
  if (diff < 7 * 86400) return rel(Math.floor(diff / 86400), "day");
  return fmtDate(ms);
}

export function fmtDate(ms) {
  const d = new Date(ms);
  const opts = { day: "numeric", month: "short" };
  if (d.getFullYear() !== new Date().getFullYear()) opts.year = "numeric";
  return d.toLocaleDateString("id", opts);
}

export function fmtDateTime(ms) {
  return new Date(ms).toLocaleString("id", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

export function fmtClock(ms) {
  return new Date(ms).toLocaleTimeString("id", { hour12: false });
}

export const pct = (used, total) => (total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0);

/** bar(percent): a progress bar; its width goes through the CSSOM because the CSP forbids style="". */
export function bar(percent, { hiAt = 85, big = false } = {}) {
  const fill = h("i", { class: percent >= hiAt ? "hi" : "" });
  fill.style.setProperty("width", `${percent}%`);
  return h("div", { class: big ? "bar big" : "bar" }, fill);
}

export function setBar(barEl, percent, hiAt = 85) {
  const fill = barEl.firstElementChild;
  fill.style.setProperty("width", `${percent}%`);
  fill.classList.toggle("hi", percent >= hiAt);
}

/** copyText copies to the clipboard, falling back to a hidden textarea where the API is missing. */
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const ta = h("textarea", { class: "sr", "aria-hidden": "true" });
    ta.value = text;
    document.body.append(ta);
    ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    return ok;
  }
}

export function randomHex(bytes = 24) {
  return [...crypto.getRandomValues(new Uint8Array(bytes))].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export function debounce(fn, ms) {
  let t;
  return (...a) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...a), ms);
  };
}

/**
 * poll(fn, ms) runs fn every ms while the tab is visible and once more when it
 * becomes visible again. It does not run fn immediately. Returns stop().
 */
export function poll(fn, ms) {
  let timer = null;
  const tick = async () => {
    timer = null;
    if (document.visibilityState === "visible") {
      try {
        await fn();
      } catch {
        // the next tick retries
      }
    }
    if (stopped === false) timer = setTimeout(tick, ms);
  };
  let stopped = false;
  const onVisible = () => {
    if (document.visibilityState === "visible" && !stopped && timer === null) tick();
  };
  document.addEventListener("visibilitychange", onVisible);
  timer = setTimeout(tick, ms);
  return () => {
    stopped = true;
    clearTimeout(timer);
    document.removeEventListener("visibilitychange", onVisible);
  };
}

/** row(heads, cells): a table row whose cells carry their column names for the stacked mobile layout. */
export function row(heads, cells, attrs) {
  return h(
    "tr",
    attrs,
    cells.map((c, i) => {
      const cell = c instanceof Element && c.tagName === "TD" ? c : h("td", {}, c);
      cell.dataset.label = heads[i] || "";
      return cell;
    }),
  );
}

/** The one-letter-pair avatar text for a user. */
export function initials(user) {
  const src = (user.display_name || user.username || "?").trim();
  const parts = src.split(/\s+/);
  return (parts.length > 1 ? parts[0][0] + parts[1][0] : src.slice(0, 2)).toUpperCase();
}
