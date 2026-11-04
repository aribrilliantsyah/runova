import * as api from "/assets/js/api.js";
import { h, icon, fmtNum, fmtTime, copyText, poll, fill } from "/assets/js/dom.js";
import * as session from "/assets/js/session.js";
import { toast } from "/assets/js/toast.js";
import { dataTable, pager, searchBox, chips, emptyState, skeletonRows } from "/assets/js/components/list.js";
import { histogram } from "/assets/js/components/histogram.js";
import { rangeControl, timeWindow, toLocalInput } from "/assets/js/components/timerange.js";
import { readState, writeState } from "/assets/js/components/hashstate.js";
import { tokens, toggle, without, has, words, fieldWords } from "/assets/js/components/query.js";

// action → [icon, short name, sentence]; the target is the app, user, database or file name.
const ACTIONS = {
  login: ["log-in", "Masuk", () => "Masuk"],
  login_failed: ["lock", "Gagal masuk", () => "Percobaan masuk gagal"],
  logout: ["log-out", "Keluar", () => "Keluar"],
  "user.create": ["user-plus", "Pengguna dibuat", (a) => `Pengguna dibuat${detail(a)}`],
  "user.update": ["users", "Pengguna diubah", (a) => `Pengguna diubah${detail(a)}`],
  "user.delete": ["trash-2", "Pengguna dihapus", () => "Pengguna dihapus"],
  "quota.update": ["sliders-horizontal", "Kuota diubah", (a) => `Kuota diubah${detail(a)}`],
  "password.change": ["key-round", "Password", (a) => (a.detail === "reset by admin" ? "Password direset admin" : "Password diganti")],
  "settings.update": ["server", "Setelan server", () => "Setelan server diubah"],
  upload: ["upload", "Unggah", (a) => `Unggah ${a.detail || ""}`.trim()],
  "app.create": ["package", "Aplikasi dibuat", () => "Aplikasi dibuat"],
  "app.update": ["sliders-horizontal", "Aplikasi diubah", () => "Pengaturan diubah"],
  "app.delete": ["trash-2", "Aplikasi dihapus", () => "Aplikasi dihapus"],
  deploy: ["rocket", "Deploy", (a) => `Deploy ${a.detail || ""}`.trim()],
  rollback: ["history", "Kembalikan", (a) => `Kembalikan ${a.detail || ""}`.trim()],
  restart: ["rotate-cw", "Restart", () => "Restart"],
  stop: ["square", "Dihentikan", () => "Dihentikan"],
  start: ["play", "Dijalankan", () => "Dijalankan"],
  "terminal.open": ["square-terminal", "Terminal", () => "Terminal dibuka"],
  "env.update": ["key-round", "Environment diubah", () => "Environment diubah"],
  "env.reveal": ["eye", "Environment dilihat", (a) => `Nilai ${a.detail || "environment"} dilihat`],
  "domain.add": ["lock", "Domain ditambah", (a) => `Domain ${a.detail || ""} ditambah`.trim()],
  "domain.remove": ["lock", "Domain dihapus", (a) => `Domain ${a.detail || ""} dihapus`.trim()],
  "hosting_domain.add": ["globe", "Domain hosting ditambah", (a) => `Domain hosting ${a.target || ""} didaftarkan`.trim()],
  "hosting_domain.update": ["globe", "Domain hosting diubah", (a) => `Domain hosting ${a.target || ""}: ${a.detail || "pengaturan diubah"}`],
  "hosting_domain.remove": ["globe", "Domain hosting dihapus", (a) => `Domain hosting ${a.target || ""} dihapus`.trim()],
  "panel.address": ["globe", "Alamat panel", (a) => `Alamat panel: ${a.detail || ""}`],
  "db.create": ["database", "Database dibuat", () => "Database dibuat"],
  "db.delete": ["database", "Database dihapus", () => "Database dihapus"],
  "db.backup": ["archive", "Backup", () => "Backup dibuat"],
  "db.restore": ["archive", "Restore", () => "Backup di-restore"],
  "db.password": ["key-round", "Password database", () => "Password database diganti"],
  "db.reveal": ["eye", "Password database dilihat", () => "Password database dilihat"],
};
const FAILED = new Set(["login_failed"]);

function detail(a) {
  return a.detail ? ` (${a.detail})` : "";
}

export function describe(a) {
  const [ic, name, text] = ACTIONS[a.action] || ["history", a.action, () => a.action];
  return { ic, name, tone: FAILED.has(a.action) ? "err" : "ok", text: text(a) };
}
const actionName = (v) => (ACTIONS[v] ? ACTIONS[v][1] : v);
const actorName = (a) => a.actor_name || "sistem";

const DEFAULTS = { q: "", range: "24h", from: "", to: "", page: "1", size: "50", live: "" };
const PATH = "/activity";
const LIVE_MS = 5000;

const pad = (n) => String(n).padStart(2, "0");
function stamp(ms) {
  const d = new Date(ms);
  return { date: d.toLocaleDateString("id", { day: "numeric", month: "short" }), time: `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}` };
}
const fullStamp = (ms) => new Date(ms).toLocaleString("id", { dateStyle: "full", timeStyle: "medium" });

/** mark(text, terms): text with every case-insensitive match of terms wrapped in <mark>. */
function mark(text, terms) {
  const t = terms.filter(Boolean);
  if (!text || !t.length) return text;
  const re = new RegExp(t.map((x) => x.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|"), "gi");
  const out = [];
  let last = 0;
  for (const m of text.matchAll(re)) {
    if (!m[0]) continue;
    out.push(text.slice(last, m.index), h("mark", {}, m[0]));
    last = m.index + m[0].length;
  }
  out.push(text.slice(last));
  return out;
}

const FIELD_LABEL = { aksi: "aksi", pelaku: "pelaku", target: "target", ip: "ip", hasil: "hasil" };

export async function renderActivity(root) {
  const admin = session.isAdmin();
  let st = readState(DEFAULTS);
  let result = null;
  let req = 0;
  let openId = null;
  let stopLive = null;
  let lastKey = "";
  const shown = new Set(); // ids already on screen, to mark fresh rows in live mode

  const set = (patch, opts) => {
    const next = { ...st, ...patch };
    if (!("page" in patch)) next.page = "1";
    writeState(PATH, next, DEFAULTS, opts);
  };

  // Toolbar
  const search = searchBox({
    id: "actq",
    label: "Cari aktivitas",
    placeholder: admin ? "Cari: deploy -staging pelaku:budi aksi:db.*" : "Cari: deploy -staging target:billing",
    onInput: (v) => set({ q: v.trim() }),
  });
  const helpBtn = h("button", { type: "button", class: "ibtn", "aria-label": "Cara menulis pencarian", "aria-expanded": "false", "aria-controls": "acthelp" }, icon("info"));
  const help = h(
    "div",
    { class: "qhelp", id: "acthelp", hidden: true },
    h("b", {}, "Menulis pencarian"),
    h(
      "dl",
      {},
      ...[
        ["deploy billing", "semua kata harus ada"],
        ['"#1 staging"', "frasa persis"],
        ["-staging", "kecualikan kata"],
        ["aksi:deploy", "aksi tertentu; aksi:db.* untuk awalan"],
        admin ? ["pelaku:budi", "pelaku tertentu; pelaku:sistem untuk proses otomatis"] : null,
        ["target:billing", "aplikasi, pengguna, atau database"],
        ["ip:203.0.113.*", "alamat IP atau awalannya"],
        ["hasil:gagal", "hanya yang gagal; hasil:berhasil sebaliknya"],
        ["-aksi:login", "kecualikan satu nilai field"],
      ]
        .filter(Boolean)
        .map(([k, v]) => [h("dt", {}, h("code", {}, k)), h("dd", {}, v)]),
    ),
  );
  helpBtn.addEventListener("click", () => {
    help.hidden = !help.hidden;
    helpBtn.setAttribute("aria-expanded", String(!help.hidden));
  });
  const range = rangeControl((patch) => set({ ...patch, live: patch.range === "custom" ? "" : st.live }));
  const liveBtn = h("button", { type: "button", class: "btn live-btn", "aria-pressed": "false", title: "Muat ulang otomatis tiap 5 detik" }, icon("radio"), "Live");
  liveBtn.addEventListener("click", () => set({ live: st.live ? "" : "1", ...(st.range === "custom" ? { range: "24h", from: "", to: "" } : {}) }));
  const refreshBtn = h("button", { type: "button", class: "ibtn", "aria-label": "Muat ulang" }, icon("refresh-cw"));
  refreshBtn.addEventListener("click", () => load({ force: true }));
  const facetBtn = h("button", { type: "button", class: "btn facet-open", "aria-controls": "actfacets", "aria-expanded": "false" }, icon("funnel"), "Filter", h("span", { class: "facet-n" }));

  // Histogram
  const hg = histogram({
    onSelect: (from, to) => set({ range: "custom", from: toLocalInput(from), to: toLocalInput(Math.max(from, to - 60e3)), live: "" }),
  });

  // Facets
  const facetBody = h("div", { class: "facets-bd" });
  const facetClose = h("button", { type: "button", class: "ibtn", "aria-label": "Tutup filter" }, icon("x"));
  const facets = h("aside", { class: "facets", id: "actfacets", "aria-label": "Filter cepat" }, h("div", { class: "facets-hd" }, h("b", {}, "Filter cepat"), facetClose), facetBody);
  const facetScrim = h("div", { class: "facets-scrim", hidden: true });
  const openFacets = (on) => {
    facets.classList.toggle("open", on);
    facetScrim.hidden = !on;
    facetBtn.setAttribute("aria-expanded", String(on));
    if (on) facetClose.focus();
  };
  facetBtn.addEventListener("click", () => openFacets(true));
  facetClose.addEventListener("click", () => openFacets(false));
  facetScrim.addEventListener("click", () => openFacets(false));
  facets.addEventListener("keydown", (e) => e.key === "Escape" && facets.classList.contains("open") && (openFacets(false), facetBtn.focus()));

  // Results
  const chipRow = h("div", { class: "fchips", "aria-label": "Filter aktif" });
  const status = h("p", { class: "lstatus", "aria-live": "polite" });
  const columns = [
    { key: "at", label: "Waktu", cls: "c-time", cell: (a) => { const s = stamp(a.at); return h("td", { title: fullStamp(a.at) }, h("span", { class: "t-d" }, s.date), " ", h("span", { class: "t-t" }, s.time)); } },
    { key: "res", label: "Hasil", cls: "c-res", cell: (a) => (FAILED.has(a.action) ? h("span", { class: "res res-fail" }, icon("circle-x"), "Gagal") : h("span", { class: "res res-ok" }, icon("circle-check"), "Berhasil")) },
    admin ? { key: "actor", label: "Pelaku", cls: "c-actor", cell: (a) => h("span", { class: a.actor_name ? "" : "muted" }, mark(actorName(a), [...terms(), ...fieldWords(st.q, "pelaku")])) } : null,
    { key: "action", label: "Aksi", cls: "c-action", cell: (a) => h("span", { class: "act-a" }, icon(describe(a).ic), h("code", {}, mark(a.action, [...terms(), ...fieldWords(st.q, "aksi")]))) },
    { key: "target", label: "Target", cls: "c-target", cell: (a) => (a.target ? h("span", { class: "ell", title: a.target }, mark(a.target, [...terms(), ...fieldWords(st.q, "target")])) : h("span", { class: "faint" }, "–")) },
    { key: "ip", label: "IP", cls: "c-ip", cell: (a) => (a.ip ? h("code", {}, mark(a.ip, [...terms(), ...fieldWords(st.q, "ip")])) : h("span", { class: "faint" }, "–")) },
    { key: "sum", label: "Ringkasan", cls: "c-sum", cell: (a) => h("span", { class: "ell" }, mark(describe(a).text, terms())) },
    { key: "open", label: "", srLabel: "Buka", cls: "c-open", hideLabel: true, cell: (a) => h("button", { type: "button", class: "ibtn row-open", "aria-label": `Detail ${describe(a).text}`, "aria-expanded": String(openId === a.id) }, icon("chevron-right")) },
  ].filter(Boolean);
  const table = dataTable({ columns, label: "Daftar aktivitas", id: "actrows", cls: "actt", rowAttrs: (a) => ({ "data-id": a.id, class: "act-row" }) });
  const pg = pager({ onPage: (off) => set({ page: String(Math.floor(off / Number(st.size)) + 1) }), onSize: (n) => set({ size: String(n) }), unit: "event" });
  const listBox = h("div", { class: "lbody" }, table.el);
  const results = h("section", { class: "card lcard" }, h("div", { class: "lhead" }, chipRow, status), listBox, pg.el);

  root.append(
    h(
      "section",
      { class: "lpage" },
      h("div", { class: "ph" }, h("div", {}, h("h1", {}, "Aktivitas"), h("p", {}, admin ? "Siapa melakukan apa, kapan, dan dari mana, di seluruh server." : "Login, deploy, dan perubahan yang kamu lakukan.")), h("div", { class: "acts" }, liveBtn)),
      h(
        "div",
        { class: "pb" },
        h("div", { class: "ltools" }, h("div", { class: "lq" }, search.el, helpBtn), range.el, refreshBtn, facetBtn),
        help,
        h("section", { class: "card hgcard" }, hg.el),
        h("div", { class: "lgrid" }, facets, results),
        facetScrim,
      ),
    ),
  );

  const terms = () => words(st.q);

  // Opening a row: click anywhere on it (unless text is being selected) or its button.
  table.tbody.addEventListener("click", (e) => {
    const tr = e.target.closest("tr.act-row");
    if (!tr || e.target.closest("a, .dt-detail") || getSelection()?.toString()) return;
    const id = Number(tr.dataset.id);
    openId = openId === id ? null : id;
    paintRows();
    if (openId != null) table.tbody.querySelector(`tr[data-id="${id}"] .row-open`)?.focus({ preventScroll: true });
  });

  function detailRow(a) {
    const d = describe(a);
    const filterBtns = (field, value) =>
      value
        ? h(
            "span",
            { class: "kv-acts" },
            h("button", { type: "button", class: "btn sm ghost", title: "Tampilkan hanya nilai ini", onclick: () => set({ q: toggle(st.q, field, value, false) }) }, icon("circle-plus"), "Filter"),
            h("button", { type: "button", class: "btn sm ghost", title: "Sembunyikan nilai ini", onclick: () => set({ q: toggle(st.q, field, value, true) }) }, icon("circle-minus"), "Kecualikan"),
          )
        : null;
    const rows = [
      ["Waktu", fullStamp(a.at), null],
      ["Hasil", FAILED.has(a.action) ? "Gagal" : "Berhasil", filterBtns("hasil", FAILED.has(a.action) ? "gagal" : "berhasil")],
      ["Pelaku", actorName(a), admin ? filterBtns("pelaku", actorName(a)) : null],
      ["Aksi", `${a.action} (${d.name})`, filterBtns("aksi", a.action)],
      ["Target", a.target || "–", filterBtns("target", a.target)],
      ["Detail", a.detail || "–", null],
      ["IP", a.ip || "–", filterBtns("ip", a.ip)],
      ["ID", String(a.id), null],
    ];
    const json = JSON.stringify(a, null, 2);
    const copyBtn = h("button", { type: "button", class: "btn sm" }, icon("copy"), "Salin JSON");
    copyBtn.addEventListener("click", async () => {
      if (await copyText(json)) {
        copyBtn.replaceChildren(icon("check"), "Tersalin");
        toast("JSON aktivitas disalin");
      }
    });
    return h(
      "tr",
      { class: "dt-detail" },
      h(
        "td",
        { colspan: String(columns.length) },
        h(
          "div",
          { class: "adet" },
          h("dl", { class: "adet-kv" }, rows.map(([k, v, acts]) => h("div", {}, h("dt", {}, k), h("dd", {}, h("span", { class: "adet-v" }, v), acts)))),
          h("div", { class: "adet-raw" }, h("div", { class: "adet-rawh" }, h("span", {}, icon("braces"), "JSON mentah"), copyBtn), h("pre", {}, json)),
        ),
      ),
    );
  }

  function paintRows() {
    const items = result?.items || [];
    table.set(items);
    for (const tr of table.tbody.querySelectorAll("tr.act-row")) {
      const id = Number(tr.dataset.id);
      const a = items.find((x) => x.id === id);
      if (st.live && lastKey && !shown.has(id)) tr.classList.add("fresh");
      if (id === openId) {
        tr.classList.add("open");
        tr.after(detailRow(a));
      }
    }
    for (const a of items) shown.add(a.id);
  }

  const FACET_SHOWN = 6;
  const expanded = new Set();
  function facetGroup(title, field, list, label = (v) => v) {
    if (!list?.length) return null;
    const peak = Math.max(...list.map((f) => f.count));
    // An active filter value is always visible, even past the cut.
    const open = expanded.has(field) || list.length <= FACET_SHOWN + 1;
    const visible = open ? list : list.filter((f, i) => i < FACET_SHOWN || has(st.q, field, f.value));
    const more = open
      ? list.length > FACET_SHOWN + 1 && h("button", { type: "button", class: "fmore", onclick: () => { expanded.delete(field); paintFacets(); } }, icon("chevron-up"), "Lebih sedikit")
      : h("button", { type: "button", class: "fmore", onclick: () => { expanded.add(field); paintFacets(); } }, icon("chevron-down"), `${fmtNum(list.length - visible.length)} lainnya`);
    return h(
      "section",
      { class: "fgroup" },
      h("h3", {}, title),
      h(
        "ul",
        {},
        visible.map((f) => {
          const state = has(st.q, field, f.value);
          const meter = h("i", { class: "fbar" });
          meter.style.setProperty("width", `${Math.max(4, Math.round((f.count / peak) * 100))}%`);
          return h(
            "li",
            { class: `fitem ${state}`.trim() },
            h(
              "button",
              { type: "button", class: "fitem-b", "aria-pressed": String(state === "in"), title: state === "in" ? "Hapus filter" : "Tampilkan hanya ini", onclick: () => { set({ q: toggle(st.q, field, f.value, false) }); openFacets(false); } },
              h("span", { class: "fitem-l" }, label(f.value)),
              h("span", { class: "fitem-n" }, fmtNum(f.count)),
              meter,
            ),
            h("button", { type: "button", class: "fitem-x", "aria-pressed": String(state === "out"), "aria-label": `Kecualikan ${label(f.value)}`, title: "Kecualikan", onclick: () => { set({ q: toggle(st.q, field, f.value, true) }); openFacets(false); } }, icon("circle-minus")),
          );
        }),
      ),
      more || null,
    );
  }

  function paintFacets() {
    const f = result?.facets;
    if (!f) return facetBody.replaceChildren();
    const outcome = [
      { value: "berhasil", count: f.outcome.ok },
      { value: "gagal", count: f.outcome.failed },
    ].filter((x) => x.count > 0 || has(st.q, "hasil", x.value));
    facetBody.replaceChildren(
      ...[
        facetGroup("Hasil", "hasil", outcome, (v) => (v === "gagal" ? "Gagal" : "Berhasil")),
        facetGroup("Aksi", "aksi", f.action, actionName),
        admin ? facetGroup("Pelaku", "pelaku", f.actor) : null,
        facetGroup("Target", "target", f.target),
      ].filter(Boolean),
    );
    if (!facetBody.childElementCount) facetBody.append(h("p", { class: "muted fnone" }, "Tidak ada nilai untuk difilter."));
  }

  function paintChips() {
    const list = tokens(st.q).map((t) => ({ ...t, label: `${t.neg ? "bukan " : ""}${t.field ? `${FIELD_LABEL[t.field]} ` : ""}${t.value}`, value: t.field === "aksi" ? `${t.value}` : t.value }));
    const n = list.length;
    facetBtn.querySelector(".facet-n").textContent = n ? String(n) : "";
    fill(chipRow, chips(list, (i) => set({ q: without(st.q, i) })), n > 1 ? h("button", { type: "button", class: "btn sm ghost", onclick: () => set({ q: "" }) }, "Hapus semua") : null);
    chipRow.hidden = !n;
  }

  function paintEmpty(err) {
    if (err) {
      const retry = h("button", { type: "button", class: "btn", onclick: () => load({ force: true }) }, icon("rotate-cw"), "Coba lagi");
      listBox.replaceChildren(emptyState("circle-alert", "Aktivitas tidak bisa dimuat", err, retry));
      return;
    }
    const filtered = st.q || st.range !== DEFAULTS.range;
    if (st.live && !st.q) {
      listBox.replaceChildren(emptyState("radio", "Menunggu aktivitas baru", "Belum ada yang tercatat di rentang ini. Event baru muncul di sini dalam 5 detik."));
      return;
    }
    const acts = [];
    if (st.range !== "24h" && st.range !== "all") acts.push(h("button", { type: "button", class: "btn", onclick: () => set({ range: "24h", from: "", to: "" }) }, "24 jam terakhir"));
    if (st.range !== "all") acts.push(h("button", { type: "button", class: "btn", onclick: () => set({ range: "all", from: "", to: "" }) }, "Semua waktu"));
    if (st.q) acts.push(h("button", { type: "button", class: "btn", onclick: () => set({ q: "", range: DEFAULTS.range, from: "", to: "" }) }, "Reset filter"));
    listBox.replaceChildren(
      filtered
        ? emptyState("search", "Tidak ada aktivitas yang cocok", st.q ? "Tidak ada event di rentang waktu ini yang cocok dengan pencarian." : "Tidak ada event di rentang waktu ini.", ...acts)
        : emptyState("history", "Belum ada aktivitas", "Login, deploy, dan perubahan pengaturan tercatat di sini.", ...acts),
    );
  }

  function paintStatus() {
    if (!result) return;
    const w = timeWindow(st);
    const span = st.range === "custom" ? `${stamp(w.from).date} ${stamp(w.from).time.slice(0, 5)} sampai ${stamp(w.to - 60e3).date} ${stamp(w.to - 60e3).time.slice(0, 5)}` : (range.el.querySelector("option:checked")?.textContent || "").toLowerCase();
    fill(status, h("b", {}, fmtNum(result.total)), ` event, ${span}`, st.live ? h("span", { class: "live-note" }, `, diperbarui ${fmtTime(Date.now())}`) : null);
  }

  async function load({ quiet = false, force = false } = {}) {
    const my = ++req;
    const w = timeWindow(st);
    const size = Number(st.size) || 50;
    const page = Math.max(1, Number(st.page) || 1);
    const qs = new URLSearchParams({ limit: String(size), offset: String((page - 1) * size), tz: String(-new Date().getTimezoneOffset()) });
    if (st.q) qs.set("q", st.q);
    if (w.from) qs.set("from", String(Math.round(w.from)));
    if (w.to) qs.set("to", String(Math.round(w.to)));
    const key = qs.toString().replace(/&?(from|to)=\d+/g, "") + st.range + st.from + st.to;
    if (!quiet || force) {
      if (!quiet) {
        listBox.replaceChildren(table.el);
        table.tbody.replaceChildren(...skeletonRows(8, columns.length));
        hg.set(null, { loading: true });
      }
    }
    try {
      const r = await api.get(`/api/activity/search?${qs}`);
      if (my !== req) return;
      if (key !== lastKey) shown.clear();
      result = r;
      if (r.total && !r.items.length && page > 1) {
        set({ page: String(Math.max(1, Math.ceil(r.total / size))) }, { replace: true });
        return update();
      }
      if (!r.items.some((a) => a.id === openId)) openId = null;
      hg.set(r.histogram, { total: r.total });
      if (r.items.length) {
        listBox.replaceChildren(table.el);
        paintRows();
      } else paintEmpty();
      pg.set({ total: r.total, offset: r.offset, limit: r.limit });
      paintFacets();
      paintStatus();
      lastKey = key;
    } catch (e) {
      if (my !== req) return;
      if (quiet) return;
      result = null;
      hg.set(null, { error: e.message });
      paintEmpty(e.message);
      pg.set({ total: 0, offset: 0, limit: size });
      facetBody.replaceChildren();
      status.replaceChildren();
    }
  }

  function syncControls() {
    if (document.activeElement !== search.input) search.input.value = st.q;
    range.sync(st);
    liveBtn.setAttribute("aria-pressed", String(Boolean(st.live)));
    liveBtn.classList.toggle("on", Boolean(st.live));
    table.el.classList.toggle("no-actor", !admin);
    paintChips();
  }

  function liveOn() {
    stopLive?.();
    stopLive = null;
    if (st.live && st.page === "1") stopLive = poll(() => load({ quiet: true }), LIVE_MS);
  }

  function update() {
    st = readState(DEFAULTS);
    if (!["15m", "1h", "24h", "7d", "30d", "all", "custom"].includes(st.range)) st.range = DEFAULTS.range;
    if (!["25", "50", "100"].includes(st.size)) st.size = DEFAULTS.size;
    syncControls();
    liveOn();
    load();
  }

  update();
  return {
    update: () => {
      update();
      return true;
    },
    cleanup() {
      stopLive?.();
      hg.destroy();
      req++;
    },
  };
}
