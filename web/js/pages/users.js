import * as api from "/assets/js/api.js";
import { h, icon, fmtMB, fmtNum, fmtTime, fmtDateTime, pct, bar, copyText, initials } from "/assets/js/dom.js";
import { dialog } from "/assets/js/components/dialog.js";
import { quotaForm } from "/assets/js/components/quotaform.js";
import { dataTable, pager, searchBox, segFilter, emptyState, skeletonRows } from "/assets/js/components/list.js";
import { readState, writeState } from "/assets/js/components/hashstate.js";
import * as session from "/assets/js/session.js";
import { toast } from "/assets/js/toast.js";

const GEN_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
const PATH = "/users";
const DEFAULTS = { q: "", role: "", sort: "username", order: "asc", page: "1", size: "25" };
const SORTS = ["username", "role", "apps", "memory", "cpu", "databases", "created", "last_login"];

function generatePassword(n = 16) {
  const bytes = crypto.getRandomValues(new Uint8Array(n));
  return [...bytes].map((b) => GEN_ALPHABET[b % GEN_ALPHABET.length]).join("");
}

/** A usage cell: "used / quota" with a small bar against the quota; over quota turns red. */
const qcell = (used, max, fmt, unit) => {
  const p = pct(used, max);
  return h("td", { class: `qcell ${used > max ? "over" : ""}`.trim(), title: `${fmt(used)} dari ${fmt(max)}${unit ? ` ${unit}` : ""} (${p}%)` }, h("div", { class: "qv" }, h("div", { class: "t" }, h("b", {}, fmt(used)), h("span", {}, `${fmt(max)}${unit ? ` ${unit}` : ""}`)), bar(p)));
};

/** Shows a password once, with a copy button; the dialog is the only place it ever appears. */
function showPassword({ title, who, password }) {
  const code = h("code", {}, password);
  const copy = h("button", { class: "btn sm", type: "button" }, icon("copy"), "Salin");
  copy.addEventListener("click", async () => {
    if (await copyText(password)) {
      copy.replaceChildren(icon("check"), "Tersalin");
    }
  });
  return dialog({ title, submit: "Selesai", hideCancel: true, body: [h("p", {}, `Password untuk ${who} hanya ditampilkan sekarang. Simpan dan sampaikan ke pengguna; setelah dialog ini ditutup tidak bisa dilihat lagi.`), h("div", { class: "secret" }, code, copy)], onSubmit: async () => true });
}

export async function renderUsers(root) {
  const me = session.current();
  let st = readState(DEFAULTS);
  let page = null;
  let req = 0;

  const set = (patch, opts) => {
    const next = { ...st, ...patch };
    if (!("page" in patch)) next.page = "1";
    writeState(PATH, next, DEFAULTS, opts);
  };

  const addBtn = h("button", { class: "btn pri", id: "addUser", type: "button" }, icon("user-plus"), "Tambah pengguna");
  const search = searchBox({ id: "userq", label: "Cari pengguna", placeholder: "Cari username, nama, atau email", onInput: (v) => set({ q: v.trim() }) });
  const roles = segFilter({
    label: "Filter peran",
    options: [
      { id: "", label: "Semua" },
      { id: "admin", label: "Admin" },
      { id: "user", label: "Pengguna" },
    ],
    onChange: (id) => set({ role: id }),
  });
  const resetBtn = h("button", { type: "button", class: "btn ghost", onclick: () => set({ q: "", role: "" }) }, icon("x"), "Reset filter");
  const sortSel = h(
    "select",
    { class: "f usort", "aria-label": "Urutkan" },
    [
      ["username:asc", "Username A–Z"],
      ["username:desc", "Username Z–A"],
      ["role:asc", "Admin dulu"],
      ["apps:desc", "Aplikasi terbanyak"],
      ["memory:desc", "Memori terbesar"],
      ["cpu:desc", "CPU terbesar"],
      ["databases:desc", "Database terbanyak"],
      ["created:desc", "Terbaru dibuat"],
      ["created:asc", "Terlama dibuat"],
      ["last_login:desc", "Login terakhir"],
    ].map(([v, l]) => h("option", { value: v }, l)),
  );
  sortSel.addEventListener("change", () => {
    const [sort, order] = sortSel.value.split(":");
    set({ sort, order });
  });

  const name = (u) => u.display_name || u.username;
  const columns = [
    {
      key: "user", label: "Pengguna", sort: "username", cls: "c-user",
      cell: (u) => h("div", { class: "ucell", title: u.email || null }, h("span", { class: "av", "aria-hidden": "true" }, initials(u)), h("div", {}, h("b", {}, name(u), u.id === me.id ? h("span", { class: "faint you" }, " (kamu)") : null), h("small", {}, u.username), u.email ? h("small", { class: "umail" }, u.email) : null)),
    },
    { key: "role", label: "Peran", sort: "role", cls: "c-role", cell: (u) => (u.role === "admin" ? h("span", { class: "chip info" }, "Admin") : h("span", { class: "chip" }, "Pengguna")) },
    { key: "apps", label: "Aplikasi", sort: "apps", firstOrder: "desc", cell: (u) => qcell(u.usage.apps, u.quota.apps, String, "") },
    { key: "memory", label: "Memori", sort: "memory", firstOrder: "desc", cell: (u) => qcell(u.usage.memory_mb, u.quota.memory_mb, (v) => fmtMB(v), "") },
    { key: "cpu", label: "CPU", sort: "cpu", firstOrder: "desc", cell: (u) => qcell(u.usage.cpu_milli, u.quota.cpu_milli, (v) => fmtNum(v / 1000), "core") },
    { key: "db", label: "Database", sort: "databases", firstOrder: "desc", cell: (u) => qcell(u.usage.databases, u.quota.databases, String, "") },
    { key: "created", label: "Dibuat", sort: "created", firstOrder: "desc", cls: "c-date", cell: (u) => h("span", { title: fmtDateTime(u.created_at) }, fmtTime(u.created_at)) },
    { key: "login", label: "Login terakhir", sort: "last_login", firstOrder: "desc", cls: "c-date", cell: (u) => (u.last_login_at ? h("span", { title: fmtDateTime(u.last_login_at) }, fmtTime(u.last_login_at)) : h("span", { class: "faint" }, "Belum pernah")) },
    {
      key: "acts", label: "", srLabel: "Aksi", hideLabel: true, cls: "c-acts num nowrap",
      cell: (u) => {
        const quotaBtn = h("button", { class: "btn sm", type: "button", "aria-label": `Kuota ${u.username}`, onclick: () => quotaDialog(u) }, icon("sliders-horizontal"), "Kuota");
        const more = h("button", { class: "ibtn", type: "button", "aria-label": `Lainnya untuk ${u.username}`, "aria-haspopup": "menu", onclick: () => menu(u, more) }, icon("ellipsis"));
        return h("td", {}, h("span", { class: "uacts" }, quotaBtn, more));
      },
    },
  ];
  const table = dataTable({ columns, label: "Daftar pengguna", id: "userrows", cls: "users-t", sort: st.sort, order: st.order, onSort: (sort, order) => set({ sort, order }), rowAttrs: (u) => ({ "data-user": u.username }) });
  const pg = pager({ onPage: (off) => set({ page: String(Math.floor(off / Number(st.size)) + 1) }), onSize: (n) => set({ size: String(n) }), unit: "pengguna" });
  const listBox = h("div", { class: "lbody" }, table.el);

  root.append(
    h(
      "section",
      { class: "lpage" },
      h("div", { class: "ph" }, h("div", {}, h("h1", {}, "Pengguna"), h("p", {}, "Admin mendaftarkan pengguna dan menentukan kuota. Pengguna membagi kuota ke aplikasinya sendiri.")), h("div", { class: "acts" }, addBtn)),
      h("div", { class: "pb" }, h("section", { class: "card lcard" }, h("div", { class: "ltools utools" }, search.el, roles.el, resetBtn, h("span", { class: "spacer" }), sortSel), listBox, pg.el)),
    ),
  );

  async function load() {
    const my = ++req;
    const size = Number(st.size);
    const qs = new URLSearchParams({ sort: st.sort, order: st.order, limit: String(size), offset: String((Math.max(1, Number(st.page) || 1) - 1) * size) });
    if (st.q) qs.set("q", st.q);
    if (st.role) qs.set("role", st.role);
    if (!page) table.tbody.replaceChildren(...skeletonRows(6, columns.length));
    try {
      const p = await api.get(`/api/users?${qs}`);
      if (my !== req) return;
      if (p.total && !p.items.length && Number(st.page) > 1) {
        set({ page: String(Math.ceil(p.total / size)) }, { replace: true });
        return update();
      }
      page = p;
      roles.set(st.role, { "": p.roles.all, admin: p.roles.admin, user: p.roles.user });
      if (!st.q && !st.role) document.querySelector('[data-count="users"]')?.replaceChildren(String(p.total));
      paint();
    } catch (e) {
      if (my !== req) return;
      page = null;
      listBox.replaceChildren(emptyState("circle-alert", "Daftar pengguna tidak bisa dimuat", e.message, h("button", { type: "button", class: "btn", onclick: () => load() }, icon("rotate-cw"), "Coba lagi")));
      pg.set({ total: 0, offset: 0, limit: size });
    }
  }

  function paint() {
    const filtered = Boolean(st.q || st.role);
    resetBtn.hidden = !filtered;
    pg.set({ total: page.total, offset: page.offset, limit: page.limit });
    if (!page.items.length) {
      listBox.replaceChildren(
        filtered
          ? emptyState("search", "Tidak ada pengguna yang cocok", st.q ? `Tidak ada pengguna dengan username, nama, atau email berisi "${st.q}"${st.role ? " di peran ini" : ""}.` : "Tidak ada pengguna dengan peran ini.", h("button", { type: "button", class: "btn", onclick: () => set({ q: "", role: "" }) }, "Reset filter"))
          : emptyState("users", "Belum ada pengguna", "Tambah pengguna untuk membagi server ini.", h("button", { type: "button", class: "btn pri", onclick: addDialog }, icon("user-plus"), "Tambah pengguna")),
      );
      return;
    }
    listBox.replaceChildren(table.el);
    table.set(page.items);
  }

  function menu(u, anchor) {
    const items = [
      ["key-round", "Reset password", () => resetPassword(u)],
      ["users", u.role === "admin" ? "Jadikan pengguna biasa" : "Jadikan admin", () => toggleRole(u), u.id === me.id],
      ["trash-2", "Hapus pengguna", () => remove(u), u.id === me.id, "danger"],
    ];
    const pop = h("div", { class: "pop", role: "menu", "aria-label": `Menu ${u.username}` });
    const close = () => {
      pop.remove();
      document.removeEventListener("mousedown", onDoc, true);
      document.removeEventListener("keydown", onKey, true);
    };
    const onDoc = (e) => !pop.contains(e.target) && !anchor.contains(e.target) && close();
    const onKey = (e) => {
      if (e.key === "Escape") {
        close();
        anchor.focus();
      } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const list = [...pop.querySelectorAll("button:not(:disabled)")];
        const i = list.indexOf(document.activeElement);
        list[(i + (e.key === "ArrowDown" ? 1 : -1) + list.length) % list.length]?.focus();
      }
    };
    pop.append(...items.map(([ic, label, fn, disabled, cls]) => h("button", { class: `item ${cls || ""}`, type: "button", role: "menuitem", disabled, title: disabled ? "Tidak bisa untuk akunmu sendiri" : null, onclick: () => { close(); fn(); } }, icon(ic), label)));
    document.body.append(pop);
    const r = anchor.getBoundingClientRect();
    pop.style.setProperty("min-width", "200px");
    pop.style.setProperty("left", `${Math.max(8, Math.min(r.right - 220, innerWidth - 228))}px`);
    pop.style.setProperty("top", `${Math.min(r.bottom + 4, innerHeight - pop.offsetHeight - 8)}px`);
    document.addEventListener("mousedown", onDoc, true);
    document.addEventListener("keydown", onKey, true);
    pop.querySelector("button:not(:disabled)")?.focus();
  }

  function addDialog() {
    const username = h("input", { class: "f mono", id: "nu-username", "aria-label": "Username", autocomplete: "off", spellcheck: "false", autocapitalize: "none" });
    const display = h("input", { class: "f", "aria-label": "Nama tampilan", autocomplete: "off" });
    const email = h("input", { class: "f", type: "email", "aria-label": "Email", autocomplete: "off" });
    const role = h("select", { class: "f", "aria-label": "Peran" }, h("option", { value: "user" }, "Pengguna"), h("option", { value: "admin" }, "Admin"));
    const password = generatePassword();
    return dialog({
      title: "Tambah pengguna",
      submit: "Buat pengguna",
      body: [
        h("div", { class: "fld" }, h("label", { for: "nu-username" }, "Username"), username, h("div", { class: "hint" }, "Huruf kecil dan angka, diawali huruf. Menjadi awalan nama database.")),
        h("div", { class: "fld" }, h("label", {}, "Nama tampilan ", h("span", { class: "faint" }, "opsional")), display),
        h("div", { class: "fld" }, h("label", {}, "Email ", h("span", { class: "faint" }, "opsional")), email),
        h("div", { class: "fld" }, h("label", {}, "Peran"), role),
        h("p", {}, "Runova membuat password awal yang acak dan menampilkannya sekali setelah pengguna dibuat."),
      ],
      onSubmit: async () => {
        const u = await api.post("/api/users", { username: username.value.trim(), display_name: display.value.trim(), email: email.value.trim(), role: role.value, password });
        await load();
        return { user: u, password };
      },
    }).then(async (out) => {
      if (out && out.password) await showPassword({ title: `Pengguna ${out.user.username} dibuat`, who: out.user.username, password: out.password });
    });
  }

  function quotaDialog(u) {
    const form = quotaForm(u.quota, { idPrefix: `quota-${u.id}` });
    return dialog({
      title: `Kuota ${u.username}`,
      submit: "Simpan kuota",
      body: [h("p", {}, "Menurunkan kuota di bawah pemakaian boleh: yang sudah berjalan tetap, alokasi baru ditolak sampai pemakaian turun."), form.node],
      onSubmit: async () => {
        await api.patch(`/api/users/${u.id}`, { quota: form.value() });
        toast(`Kuota ${u.username} disimpan`);
        await load();
      },
    });
  }

  function resetPassword(u) {
    const password = generatePassword();
    return dialog({
      title: `Reset password ${u.username}`,
      submit: "Reset password",
      danger: true,
      body: [h("p", {}, `Semua sesi ${u.username} berakhir. Password baru ditampilkan sekali setelah ini.`)],
      onSubmit: async () => {
        await api.post(`/api/users/${u.id}/password`, { password });
        return { password };
      },
    }).then(async (out) => {
      if (out && out.password) await showPassword({ title: `Password baru ${u.username}`, who: u.username, password: out.password });
    });
  }

  function toggleRole(u) {
    const to = u.role === "admin" ? "user" : "admin";
    return dialog({
      title: to === "admin" ? `Jadikan ${u.username} admin?` : `Jadikan ${u.username} pengguna biasa?`,
      submit: "Ya, ubah",
      body: [h("p", {}, to === "admin" ? "Admin bisa melihat semua aplikasi dan mengatur server, pengguna, dan kuota." : "Akses ke halaman Pengguna dan Server dicabut.")],
      onSubmit: async () => {
        await api.patch(`/api/users/${u.id}`, { role: to });
        await load();
      },
    });
  }

  function remove(u) {
    return dialog({
      title: `Hapus ${u.username}`,
      submit: "Hapus pengguna",
      danger: true,
      confirmWord: u.username,
      body: [h("div", { class: "err" }, icon("triangle-alert"), h("span", {}, "Semua aplikasi, data di folder /data, database PostgreSQL, dan role milik pengguna ini ikut terhapus. Tidak bisa dibatalkan."))],
      onSubmit: async () => {
        await api.del(`/api/users/${u.id}`);
        toast(`${u.username} dihapus`);
        await load();
      },
    });
  }

  function update() {
    st = readState(DEFAULTS);
    if (!SORTS.includes(st.sort)) st.sort = DEFAULTS.sort;
    if (st.order !== "desc") st.order = "asc";
    if (!["", "admin", "user"].includes(st.role)) st.role = "";
    if (!["25", "50", "100"].includes(st.size)) st.size = DEFAULTS.size;
    if (document.activeElement !== search.input) search.input.value = st.q;
    table.setSort(st.sort, st.order);
    const opt = `${st.sort}:${st.order}`;
    if (![...sortSel.options].some((o) => o.value === opt)) sortSel.append(h("option", { value: opt }, `${columns.find((c) => c.sort === st.sort)?.label || st.sort} ${st.order === "desc" ? "↓" : "↑"}`));
    sortSel.value = opt;
    return load();
  }

  addBtn.addEventListener("click", addDialog);
  await update();
  return {
    update: () => {
      update();
      return true;
    },
    cleanup: () => req++,
  };
}
