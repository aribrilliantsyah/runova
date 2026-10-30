import * as api from "/assets/js/api.js";
import { h, icon, fmtMB, fmtNum, pct, bar, row, copyText, clear, initials, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { dialog } from "/assets/js/components/dialog.js";
import { quotaForm } from "/assets/js/components/quotaform.js";
import * as session from "/assets/js/session.js";
import { toast } from "/assets/js/toast.js";

const HEADS = ["Pengguna", "Peran", "Aplikasi", "Memori", "CPU", "Database", ""];
const GEN_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";

function generatePassword(n = 16) {
  const bytes = crypto.getRandomValues(new Uint8Array(n));
  return [...bytes].map((b) => GEN_ALPHABET[b % GEN_ALPHABET.length]).join("");
}

const qcell = (used, max, fmt, unit) => {
  const p = pct(used, max);
  return h("td", { class: `qcell ${used > max ? "over" : ""}`.trim() }, h("div", { class: "t" }, h("b", {}, fmt(used)), h("span", {}, `${fmt(max)}${unit ? ` ${unit}` : ""}`)), bar(p));
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
  let users = [];
  let usage = {};
  const tbody = h("tbody", { id: "userrows" });
  const addBtn = h("button", { class: "btn pri", id: "addUser", type: "button" }, icon("user-plus"), "Tambah pengguna");
  root.append(
    h(
      "section",
      {},
      h("div", { class: "ph" }, h("div", {}, h("h1", {}, "Pengguna"), h("p", {}, "Admin mendaftarkan pengguna dan menentukan kuota. Pengguna membagi kuota ke aplikasinya sendiri.")), h("div", { class: "acts" }, addBtn)),
      h("div", { class: "pb" }, h("section", { class: "card" }, h("table", { class: "t" }, h("thead", {}, h("tr", {}, HEADS.map((t) => h("th", {}, t)))), tbody))),
    ),
  );

  async function load() {
    [users, usage] = await Promise.all([api.get("/api/users"), api.get("/api/usage")]);
    paint();
    document.querySelector('[data-count="users"]')?.replaceChildren(String(users.length));
  }

  function paint() {
    fill(tbody, ...users.map(rowFor));
  }

  function rowFor(u) {
    const use = usage[u.id] || { apps: 0, memory_mb: 0, cpu_milli: 0, databases: 0 };
    const q = u.quota;
    const self = u.id === me.id;
    const name = u.display_name || u.username;
    const quotaBtn = h("button", { class: "btn sm", type: "button", "aria-label": `Kuota ${u.username}`, onclick: () => quotaDialog(u) }, icon("sliders-horizontal"), "Kuota");
    const more = h("button", { class: "ibtn", type: "button", "aria-label": `Lainnya untuk ${u.username}`, "aria-haspopup": "menu", style: { display: "inline-grid", "vertical-align": "middle" }, onclick: () => menu(u, more) }, icon("ellipsis"));
    return row(HEADS, [
      h("td", {}, h("div", { class: "ucell" }, h("span", { class: "av", "aria-hidden": "true" }, initials(u)), h("div", {}, h("b", {}, name, self ? h("span", { class: "faint", style: { "font-weight": "400" } }, " (kamu)") : null), h("small", {}, u.username)))),
      u.role === "admin" ? h("span", { class: "chip info" }, "Admin") : h("span", { class: "chip" }, "Pengguna"),
      qcell(use.apps, q.apps, String, ""),
      qcell(use.memory_mb, q.memory_mb, (v) => fmtMB(v), ""),
      qcell(use.cpu_milli, q.cpu_milli, (v) => fmtNum(v / 1000), "core"),
      qcell(use.databases, q.databases, String, ""),
      h("td", { class: "num nowrap", "data-label": "" }, quotaBtn, " ", more),
    ], { "data-user": u.username });
  }

  function menu(u, anchor) {
    const items = [
      ["key-round", "Reset password", () => resetPassword(u)],
      [u.role === "admin" ? "users" : "users", u.role === "admin" ? "Jadikan pengguna biasa" : "Jadikan admin", () => toggleRole(u), u.id === me.id],
      ["trash-2", "Hapus pengguna", () => remove(u), u.id === me.id, "danger"],
    ];
    const pop = h("div", { class: "pop", role: "menu", "aria-label": `Menu ${u.username}`, style: {} });
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
      }
    };
    pop.append(...items.map(([ic, label, fn, disabled, cls]) => h("button", { class: `item ${cls || ""}`, type: "button", role: "menuitem", disabled, onclick: () => { close(); fn(); } }, icon(ic), label)));
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

  addBtn.addEventListener("click", addDialog);
  await load();
}
