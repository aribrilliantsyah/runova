// The Tabel and Query workspace of one database on the Database page. It is
// built once per selected database and kept across the page's polling
// re-renders, so typing in the editor is never interrupted.
import * as api from "/assets/js/api.js";
import { h, icon, fmtBytes, fmtNum, fmtDateTime, fill } from "/assets/js/dom.js";
import { dialog } from "/assets/js/components/dialog.js";
import { emptyState } from "/assets/js/components/list.js";
import { toast } from "/assets/js/toast.js";
import { toCSV, loadHistory, pushHistory, lineCount, resultSummary } from "/assets/js/components/sqltools.js";

const PAGE = 100;
const CELL_MAX = 300;

/** valueCell(v): one result value; NULL is marked, long text is cut with the full value as title. */
function valueCell(v) {
  if (v == null) return h("td", { class: "null" }, "NULL");
  const short = v.length > CELL_MAX ? `${v.slice(0, CELL_MAX)}…` : v;
  return h("td", { title: v.length > 40 ? v.slice(0, 2000) : null }, short);
}

/** resultGrid(columns, rows) → a scrollable data table (not the stacked mobile table). */
export function resultGrid(columns, rows, { offset = 0 } = {}) {
  return h(
    "div",
    { class: "grid-wrap", tabindex: "0", role: "region", "aria-label": "Hasil" },
    h(
      "table",
      { class: "grid" },
      h("thead", {}, h("tr", {}, h("th", { class: "rn", scope: "col" }, h("span", { class: "sr" }, "Baris")), columns.map((c) => h("th", { scope: "col" }, h("span", { class: "cn" }, c.name), c.type ? h("span", { class: "ct" }, c.type) : null)))),
      h("tbody", {}, rows.map((r, i) => h("tr", {}, h("td", { class: "rn" }, fmtNum(offset + i + 1)), r.map(valueCell)))),
    ),
  );
}

function download(name, text) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/csv;charset=utf-8" }));
  const a = h("a", { href: url, download: name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function dbWork(db, { pgDown }) {
  let tab = "tables";
  const tabBtns = {
    tables: h("button", { type: "button", id: "dbTabTables", role: "tab", onclick: () => show("tables") }, icon("table"), "Tabel"),
    query: h("button", { type: "button", id: "dbTabQuery", role: "tab", onclick: () => show("query") }, icon("square-terminal"), "Query"),
  };
  const tablesPane = h("div", { class: "dbw-pane", id: "tablesPane", role: "tabpanel", "aria-labelledby": "dbTabTables" });
  const queryPane = h("div", { class: "dbw-pane", id: "queryPane", role: "tabpanel", "aria-labelledby": "dbTabQuery" });
  const el = h(
    "section",
    { class: "card dbw", id: "dbWork" },
    h("header", {}, h("h3", {}, h("span", { class: "mono" }, db.name)), h("div", { class: "seg dbw-tabs", role: "tablist", "aria-label": "Bagian database" }, tabBtns.tables, tabBtns.query)),
    tablesPane,
    queryPane,
  );

  function show(next) {
    tab = next;
    for (const [k, b] of Object.entries(tabBtns)) {
      b.setAttribute("aria-selected", String(k === tab));
      b.setAttribute("aria-pressed", String(k === tab));
      b.tabIndex = k === tab ? 0 : -1;
    }
    tablesPane.hidden = tab !== "tables";
    queryPane.hidden = tab !== "query";
    if (tab === "query") editor.focus({ preventScroll: true });
  }

  // ---- Tabel --------------------------------------------------------------
  const t = { list: null, error: "", selected: null, page: null, offset: 0, loading: false };
  const listBox = h("div", { class: "tlist", id: "tableList" });
  const detailBox = h("div", { class: "tdetail", id: "tableDetail" });
  fill(tablesPane, h("div", { class: "tsplit" }, listBox, detailBox));

  async function loadTables() {
    t.error = "";
    try {
      t.list = await api.get(`/api/databases/${db.id}/tables`);
    } catch (e) {
      t.list = [];
      t.error = e.message;
    }
    if (t.selected && !t.list.some((x) => x.name === t.selected)) t.selected = null;
    renderList();
    renderDetail();
  }

  function renderList() {
    if (t.list === null) return fill(listBox, h("div", { class: "loading" }, "Memuat tabel…"));
    if (t.error) return fill(listBox, h("div", { class: "err", role: "alert" }, icon("circle-alert"), h("span", {}, t.error)));
    if (!t.list.length) {
      return fill(
        listBox,
        emptyState("table", "Belum ada tabel", "Buat tabel lewat Query (mode Tulis), atau restore dari file dump.", h("button", { class: "btn sm", type: "button", onclick: () => { show("query"); editor.value ||= "CREATE TABLE contoh (\n  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,\n  nama text NOT NULL\n)"; syncGutter(); } }, icon("square-terminal"), "Buka Query")),
      );
    }
    fill(
      listBox,
      h("div", { class: "tlist-h" }, h("span", {}, `${fmtNum(t.list.length)} tabel di skema public`), h("button", { class: "ibtn", type: "button", "aria-label": "Muat ulang daftar tabel", onclick: loadTables }, icon("refresh-cw"))),
      h(
        "ul",
        { role: "listbox", "aria-label": "Tabel" },
        t.list.map((x) =>
          h(
            "li",
            {},
            h(
              "button",
              { type: "button", class: "trow", role: "option", "aria-selected": String(x.name === t.selected), "data-table": x.name, onclick: () => openTable(x.name) },
              icon("table"),
              h("span", { class: "tn mono" }, x.name),
              h("span", { class: "tm" }, `${x.rows_estimated ? "≈ " : ""}${fmtNum(x.rows)} baris · ${fmtBytes(x.size_bytes)}`),
            ),
          ),
        ),
      ),
    );
  }

  async function openTable(name, offset = 0) {
    t.selected = name;
    t.offset = offset;
    t.loading = true;
    renderList();
    renderDetail();
    try {
      t.page = await api.get(`/api/databases/${db.id}/tables/${encodeURIComponent(name)}/data?offset=${offset}&limit=${PAGE}`);
    } catch (e) {
      t.page = { error: e.message };
    }
    t.loading = false;
    if (t.selected === name) renderDetail();
    if (matchMedia("(max-width: 760px)").matches) detailBox.scrollIntoView({ block: "start", behavior: "smooth" });
  }

  function tableAction(x, kind) {
    const drop = kind === "drop";
    dialog({
      title: drop ? `Hapus tabel ${x.name}` : `Kosongkan tabel ${x.name}`,
      submit: drop ? "Hapus tabel" : "Kosongkan tabel",
      danger: true,
      confirmWord: x.name,
      body: [h("p", {}, drop ? `Tabel beserta ${fmtNum(x.rows)} barisnya dihapus permanen. Tabel lain yang merujuknya lewat foreign key membuat penghapusan gagal.` : `Semua ${fmtNum(x.rows)} baris dihapus (TRUNCATE); struktur tabel tetap. Tidak bisa dibatalkan kecuali dari backup.`)],
      onSubmit: async () => {
        if (drop) await api.del(`/api/databases/${db.id}/tables/${encodeURIComponent(x.name)}`, { confirm: x.name });
        else await api.post(`/api/databases/${db.id}/tables/${encodeURIComponent(x.name)}/truncate`, { confirm: x.name });
        toast(drop ? `${x.name} dihapus` : `${x.name} dikosongkan`);
        if (drop) t.selected = null;
        await loadTables();
        if (!drop) openTable(x.name);
      },
    });
  }

  function renderDetail() {
    const x = t.list?.find((v) => v.name === t.selected);
    if (!x) {
      return fill(detailBox, t.list?.length ? h("div", { class: "tpick muted" }, icon("table"), h("span", {}, "Pilih tabel untuk melihat kolom dan isinya.")) : null);
    }
    const cols = h(
      "table",
      { class: "t tcols" },
      h("thead", {}, h("tr", {}, h("th", {}, "Kolom"), h("th", {}, "Tipe"), h("th", {}, "Null"), h("th", {}, "Default"))),
      h(
        "tbody",
        {},
        x.columns.map((c) =>
          h(
            "tr",
            {},
            h("td", { "data-label": "Kolom" }, h("span", { class: "mono" }, c.name), c.primary_key ? h("span", { class: "chip pk", title: "Primary key" }, icon("key-round"), "PK") : null),
            h("td", { "data-label": "Tipe" }, h("span", { class: "mono" }, c.type)),
            h("td", { "data-label": "Null" }, c.nullable ? "ya" : h("span", { class: "muted" }, "tidak")),
            h("td", { "data-label": "Default" }, c.default ? h("span", { class: "mono" }, c.default) : h("span", { class: "faint" }, "—")),
          ),
        ),
      ),
    );
    const truncate = h("button", { class: "btn sm danger", type: "button", disabled: pgDown(), onclick: () => tableAction(x, "truncate") }, icon("eraser"), "Kosongkan");
    const drop = h("button", { class: "btn sm danger", type: "button", disabled: pgDown(), onclick: () => tableAction(x, "drop") }, icon("trash-2"), "Hapus tabel");
    const q = h("button", { class: "btn sm ghost", type: "button", onclick: () => { editor.value = `SELECT * FROM ${quoteIdent(x.name)} LIMIT 100`; syncGutter(); show("query"); } }, icon("square-terminal"), "Query");

    let data;
    if (t.loading && !t.page) data = h("div", { class: "loading" }, "Memuat isi…");
    else if (t.page?.error) data = h("div", { class: "err", role: "alert" }, icon("circle-alert"), h("span", {}, t.page.error));
    else if (!t.page?.rows.length) data = h("div", { class: "bd muted tempty" }, t.offset ? "Tidak ada baris di halaman ini." : "Tabel ini kosong.");
    else data = resultGrid(t.page.columns, t.page.rows, { offset: t.offset });

    const from = t.offset + 1;
    const to = t.offset + (t.page?.rows?.length || 0);
    const prev = h("button", { class: "btn sm", type: "button", "aria-label": "Halaman sebelumnya", disabled: t.offset === 0 || t.loading, onclick: () => openTable(x.name, Math.max(0, t.offset - PAGE)) }, icon("chevron-left"));
    const next = h("button", { class: "btn sm", type: "button", "aria-label": "Halaman berikutnya", disabled: !t.page?.truncated || t.loading, onclick: () => openTable(x.name, t.offset + PAGE) }, icon("chevron-right"));
    const pager = t.page?.rows?.length ? h("nav", { class: "pg", "aria-label": "Halaman data" }, h("span", { class: "pg-info" }, h("b", {}, `${fmtNum(from)}–${fmtNum(to)}`), ` dari ${x.rows_estimated ? "≈ " : ""}${fmtNum(x.rows)} baris`), h("span", { class: "spacer" }), h("span", { class: "pg-nav" }, prev, next)) : null;

    fill(
      detailBox,
      h(
        "div",
        { class: "td-h" },
        h("div", {}, h("b", { class: "mono" }, x.name), h("div", { class: "sub" }, `${x.columns.length} kolom · ${x.rows_estimated ? "≈ " : ""}${fmtNum(x.rows)} baris · ${fmtBytes(x.size_bytes)}`)),
        h("div", { class: "r" }, q, truncate, drop),
      ),
      h("details", { class: "tcol-box", open: !matchMedia("(max-width: 760px)").matches }, h("summary", {}, "Kolom"), h("div", { class: "tcol-scroll" }, cols)),
      h("div", { class: "tdata", "aria-busy": t.loading ? "true" : null }, h("div", { class: "tdata-h" }, h("b", {}, "Isi"), h("span", { class: "muted" }, "urut primary key, 100 baris per halaman")), data, pager),
    );
  }

  // ---- Query --------------------------------------------------------------
  let write = false;
  let running = false;
  let lastResult = null;
  let errLine = 0;
  const editor = h("textarea", { class: "sqltext", id: "sqlEditor", "aria-label": "Query SQL", spellcheck: "false", autocapitalize: "none", autocomplete: "off", wrap: "off", placeholder: "SELECT * FROM nama_tabel LIMIT 100" });
  const gutter = h("div", { class: "sqlgut", "aria-hidden": "true" });
  const readBtn = h("button", { type: "button", id: "modeRead", "aria-pressed": "true", onclick: () => setMode(false) }, icon("eye"), "Baca");
  const writeBtn = h("button", { type: "button", id: "modeWrite", "aria-pressed": "false", onclick: () => setMode(true) }, icon("pencil"), "Tulis");
  const runBtn = h("button", { class: "btn pri", type: "button", id: "runQuery", onclick: runQuery }, icon("play"), "Jalankan", h("kbd", { class: "kbdh" }, "Ctrl ↵"));
  const timeBox = h("span", { class: "qtime", id: "queryTime", "aria-live": "polite" });
  const histBtn = h("button", { class: "btn sm ghost", type: "button", id: "historyBtn", "aria-expanded": "false", "aria-controls": "historyList", onclick: toggleHistory }, icon("history"), "Riwayat");
  const histBox = h("div", { class: "qhist", id: "historyList", hidden: true });
  const modeNote = h("div", { class: "qnote", id: "modeNote" });
  const resultBox = h("div", { class: "qres", id: "queryResult" });

  fill(
    queryPane,
    h("div", { class: "qbar" }, h("div", { class: "seg qmode", role: "group", "aria-label": "Mode query" }, readBtn, writeBtn), runBtn, timeBox, h("span", { class: "spacer" }), histBtn),
    modeNote,
    histBox,
    h("div", { class: "sqled" }, gutter, editor),
    resultBox,
  );

  function setMode(w) {
    write = w;
    readBtn.setAttribute("aria-pressed", String(!w));
    writeBtn.setAttribute("aria-pressed", String(w));
    queryPane.classList.toggle("writing", w);
    fill(
      modeNote,
      w
        ? [icon("triangle-alert"), h("span", {}, h("b", {}, "Mode Tulis: "), "perubahan langsung disimpan. Boleh INSERT, UPDATE, DELETE, TRUNCATE, dan CREATE/ALTER/DROP TABLE atau INDEX. Batas waktu 30 detik.")]
        : [icon("eye"), h("span", {}, h("b", {}, "Mode Baca: "), "SELECT, WITH, EXPLAIN, SHOW; tidak ada yang berubah. Hasil maksimal 1.000 baris, batas waktu 10 detik.")],
    );
  }

  function syncGutter() {
    const n = lineCount(editor.value);
    if (gutter.childElementCount !== n || gutter.dataset.err !== String(errLine)) {
      gutter.replaceChildren(...Array.from({ length: n }, (_, i) => h("span", { class: i + 1 === errLine ? "e" : null }, String(i + 1))));
      gutter.dataset.err = String(errLine);
    }
    gutter.scrollTop = editor.scrollTop;
  }
  editor.addEventListener("input", () => {
    if (errLine) errLine = 0;
    syncGutter();
  });
  editor.addEventListener("scroll", () => (gutter.scrollTop = editor.scrollTop));
  editor.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      runQuery();
    }
  });

  function renderHistory() {
    const list = loadHistory(db.id);
    fill(
      histBox,
      list.length
        ? h(
            "ul",
            {},
            list.map((x) =>
              h(
                "li",
                {},
                h("button", { type: "button", class: "qh", onclick: () => { editor.value = x.sql; errLine = 0; syncGutter(); toggleHistory(false); editor.focus(); } }, h("code", {}, x.sql.length > 400 ? `${x.sql.slice(0, 400)}…` : x.sql), h("span", { class: "faint" }, fmtDateTime(x.at))),
              ),
            ),
          )
        : h("div", { class: "muted qh-empty" }, "Belum ada riwayat. Query yang dijalankan di browser ini tersimpan di sini (50 terakhir)."),
    );
  }
  function toggleHistory(open = histBox.hidden) {
    histBox.hidden = !open;
    histBtn.setAttribute("aria-expanded", String(open));
    if (open) renderHistory();
  }

  async function runQuery() {
    const sql = editor.value;
    if (running || !sql.trim()) {
      if (!sql.trim()) editor.focus();
      return;
    }
    running = true;
    runBtn.disabled = true;
    runBtn.setAttribute("aria-busy", "true");
    timeBox.textContent = "Menjalankan…";
    pushHistory(db.id, sql);
    if (!histBox.hidden) renderHistory();
    try {
      lastResult = await api.post(`/api/databases/${db.id}/query`, { sql, mode: write ? "write" : "read" });
    } catch (e) {
      lastResult = { error: e.message, columns: [], rows: [] };
    } finally {
      running = false;
      runBtn.disabled = false;
      runBtn.removeAttribute("aria-busy");
    }
    errLine = lastResult.error_line || 0;
    syncGutter();
    timeBox.textContent = lastResult.error ? "" : `${fmtNum(lastResult.duration_ms)} ms`;
    renderResult();
    if (!lastResult.error && write && !lastResult.columns.length) loadTables();
  }

  function renderResult() {
    const r = lastResult;
    if (!r) return fill(resultBox, h("div", { class: "qempty muted" }, icon("play"), h("span", {}, "Tulis satu perintah SQL lalu tekan Jalankan atau Ctrl+Enter.")));
    if (r.error) {
      return fill(resultBox, h("div", { class: "err qerr", role: "alert" }, icon("circle-alert"), h("div", {}, r.error_line ? h("b", {}, `Baris ${r.error_line}`) : null, h("pre", {}, r.error))));
    }
    const head = h("div", { class: "qres-h" }, h("span", { class: "qsum" }, resultSummary(r)), r.truncated ? h("span", { class: "chip warn" }, "dipotong") : null, h("span", { class: "spacer" }));
    if (!r.columns.length) return fill(resultBox, h("div", { class: "qdone" }, icon("circle-check"), h("span", {}, resultSummary(r))));
    const csv = h("button", { class: "btn sm", type: "button", id: "csvBtn", onclick: () => download(`${db.name}-${new Date().toISOString().slice(0, 19).replace(/[-:T]/g, "")}.csv`, toCSV(r.columns, r.rows)) }, icon("download"), "CSV");
    head.append(csv);
    fill(
      resultBox,
      head,
      r.truncated ? h("div", { class: "qnote warn" }, icon("triangle-alert"), h("span", {}, "Hasil dipotong di 1.000 baris atau 5 MB. Tambahkan WHERE atau LIMIT/OFFSET untuk melihat sisanya.")) : null,
      r.rows.length ? resultGrid(r.columns, r.rows) : h("div", { class: "qdone muted" }, icon("circle-minus"), h("span", {}, "0 baris.")),
    );
  }

  setMode(false);
  syncGutter();
  renderResult();
  show("tables");
  renderList();
  loadTables();

  return { el, reload: loadTables };
}

/** quoteIdent("Order Items") → "\"Order Items\""; plain lower-case names stay bare. */
export function quoteIdent(name) {
  return /^[a-z_][a-z0-9_]*$/.test(name) ? name : `"${name.replaceAll('"', '""')}"`;
}
