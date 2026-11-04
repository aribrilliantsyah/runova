// Shared list building blocks for data pages (Aktivitas, Pengguna; later Aplikasi and Database):
// a sortable table that becomes cards on phones, a pager with page size and "x–y dari z",
// a search box, a segmented filter with counts, removable filter chips and empty states.
import { h, icon, fmtNum, debounce } from "/assets/js/dom.js";

export const PAGE_SIZES = [25, 50, 100];

/**
 * dataTable({columns, sort, order, onSort, rowAttrs, label}) → {el, tbody, set(rows), setSort(sort, order)}.
 * columns: [{key, label, sort?: sortKey, num?, cls?, cell(row) → Node|string, hideLabel?}].
 * Sortable headers are buttons with aria-sort on the th; clicking toggles the order.
 */
export function dataTable({ columns, sort = "", order = "asc", onSort, rowAttrs, label, id, cls = "" }) {
  const ths = columns.map((c) => {
    const th = h("th", { class: [c.num ? "num" : "", c.cls].filter(Boolean).join(" ") || null, scope: "col" });
    if (c.sort && onSort) {
      const btn = h("button", { type: "button", class: "dt-sort", onclick: () => onSort(c.sort, sort === c.sort && order === "asc" ? "desc" : sort === c.sort ? "asc" : c.firstOrder || "asc") }, c.label, icon("chevrons-up-down", "dt-ind"));
      th.append(btn);
    } else {
      th.append(c.label || h("span", { class: "sr" }, c.srLabel || ""));
    }
    return th;
  });
  const tbody = h("tbody", { id });
  const table = h("table", { class: `t dt ${cls}`.trim(), "aria-label": label }, h("thead", {}, h("tr", {}, ths)), tbody);

  function setSort(s, o) {
    sort = s;
    order = o;
    columns.forEach((c, i) => {
      const th = ths[i];
      if (!c.sort) return;
      const on = c.sort === s;
      th.setAttribute("aria-sort", on ? (o === "desc" ? "descending" : "ascending") : "none");
      const ind = th.querySelector(".dt-ind");
      ind?.replaceWith(icon(on ? (o === "desc" ? "chevron-down" : "chevron-up") : "chevrons-up-down", "dt-ind"));
    });
  }
  setSort(sort, order);

  function set(rows) {
    tbody.replaceChildren(
      ...rows.map((r) => {
        const tr = h("tr", rowAttrs ? rowAttrs(r) : {});
        for (const c of columns) {
          const v = c.cell(r);
          const td = v instanceof Element && v.tagName === "TD" ? v : h("td", {}, v);
          if (c.num) td.classList.add("num");
          if (c.cls) td.classList.add(...c.cls.split(" "));
          td.dataset.label = c.hideLabel ? "" : c.label || "";
          tr.append(td);
        }
        return tr;
      }),
    );
  }
  return { el: table, tbody, set, setSort };
}

/**
 * pager({onPage, onSize, label}) → {el, set({total, offset, limit})}.
 * Shows "1–25 dari 312", the page size and previous/next; hidden parts collapse on one page.
 */
export function pager({ onPage, onSize, unit = "" }) {
  const info = h("span", { class: "pg-info", "aria-live": "polite" });
  const size = h("select", { class: "f pg-size", "aria-label": "Baris per halaman" }, PAGE_SIZES.map((n) => h("option", { value: String(n) }, `${n} / halaman`)));
  size.addEventListener("change", () => onSize(Number(size.value)));
  const prev = h("button", { type: "button", class: "btn sm", "aria-label": "Halaman sebelumnya" }, icon("chevron-left"));
  const next = h("button", { type: "button", class: "btn sm", "aria-label": "Halaman berikutnya" }, icon("chevron-right"));
  const pageNo = h("span", { class: "pg-no" });
  let cur = { total: 0, offset: 0, limit: PAGE_SIZES[0] };
  prev.addEventListener("click", () => onPage(Math.max(0, cur.offset - cur.limit)));
  next.addEventListener("click", () => onPage(cur.offset + cur.limit));
  const el = h("nav", { class: "pg", "aria-label": "Halaman" }, info, h("span", { class: "spacer" }), size, h("span", { class: "pg-nav" }, prev, pageNo, next));
  return {
    el,
    set(p) {
      cur = p;
      const from = p.total ? p.offset + 1 : 0;
      const to = Math.min(p.offset + p.limit, p.total);
      info.replaceChildren(h("b", {}, `${fmtNum(from)}–${fmtNum(to)}`), ` dari ${fmtNum(p.total)}${unit ? ` ${unit}` : ""}`);
      size.value = String(p.limit);
      const pages = Math.max(1, Math.ceil(p.total / p.limit));
      const page = Math.floor(p.offset / p.limit) + 1;
      pageNo.textContent = `${fmtNum(page)} / ${fmtNum(pages)}`;
      prev.disabled = p.offset <= 0;
      next.disabled = p.offset + p.limit >= p.total;
      el.hidden = p.total === 0;
    },
  };
}

/** searchBox({label, placeholder, onInput, onSubmit, hint}) → {el, input}. onInput is debounced. */
export function searchBox({ label, placeholder, onInput, onSubmit, wait = 350, id }) {
  const input = h("input", { type: "search", id, "aria-label": label, placeholder, autocomplete: "off", spellcheck: "false", autocapitalize: "none" });
  const later = debounce(() => onInput(input.value), wait);
  input.addEventListener("input", later);
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      (onSubmit || onInput)(input.value);
    } else if (e.key === "Escape" && input.value) {
      input.value = "";
      (onSubmit || onInput)("");
    }
  });
  return { el: h("label", { class: "filter lsearch" }, icon("search"), input), input };
}

/**
 * segFilter({label, options: [{id, label}], onChange}) → {el, set(value, counts)}.
 * A segmented control whose buttons carry the count of matches for each option.
 */
export function segFilter({ label, options, onChange }) {
  const counts = new Map();
  const buttons = options.map((o) => {
    const n = h("span", { class: "seg-n" }, "–");
    counts.set(o.id, n);
    return h("button", { type: "button", "aria-pressed": "false", onclick: () => onChange(o.id) }, o.label, n);
  });
  return {
    el: h("div", { class: "seg lseg", role: "group", "aria-label": label }, buttons),
    set(value, c = {}) {
      options.forEach((o, i) => {
        buttons[i].setAttribute("aria-pressed", String(o.id === value));
        counts.get(o.id).textContent = c[o.id] == null ? "–" : fmtNum(c[o.id]);
      });
    },
  };
}

/** chips(items, onRemove) → node: one removable chip per active filter; items: [{label, neg}]. */
export function chips(items, onRemove) {
  return items.map((it, i) =>
    h(
      "span",
      { class: `fchip${it.neg ? " neg" : ""}` },
      it.neg ? h("span", { class: "fchip-op", "aria-hidden": "true" }, "bukan") : null,
      it.field ? h("span", { class: "fchip-k" }, it.field) : null,
      h("span", { class: "fchip-v" }, it.value),
      h("button", { type: "button", class: "fchip-x", "aria-label": `Hapus filter ${it.label}`, onclick: () => onRemove(i) }, icon("x")),
    ),
  );
}

/** emptyState(icon, title, text, ...actions) for a list that has nothing to show. */
export function emptyState(ic, title, text, ...actions) {
  return h("div", { class: "empty lempty" }, h("span", { class: "ic" }, icon(ic)), h("b", {}, title), text ? h("span", {}, text) : null, actions.length ? h("div", { class: "rowflex lempty-acts" }, actions) : null);
}

/** skeletonRows(n, cols) → table rows that hold the space while a page loads. */
export function skeletonRows(n, cols) {
  return Array.from({ length: n }, () => h("tr", { class: "skel-row", "aria-hidden": "true" }, Array.from({ length: cols }, () => h("td", {}, h("i", { class: "skel" })))));
}
