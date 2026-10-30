import * as api from "/assets/js/api.js";
import { h, icon, fmtBytes, fmtDate, fmtDateTime, copyText, clear, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { dialog, confirm } from "/assets/js/components/dialog.js";
import { pgLogo } from "/assets/js/kinds.js";
import { dbSuffixError } from "/assets/js/validate.js";
import * as session from "/assets/js/session.js";
import { toast } from "/assets/js/toast.js";

const POLL_MS = 5000;

function kvRow(label, value, { secret = false } = {}) {
  const code = h("code", { class: secret ? "faint" : "" }, value);
  const cp = h("button", { class: "cp", type: "button", "aria-label": `Salin ${label}` }, icon("copy"));
  cp.addEventListener("click", async () => {
    if (await copyText(code.dataset.plain ?? code.textContent)) {
      cp.replaceChildren(icon("check"));
      setTimeout(() => cp.replaceChildren(icon("copy")), 1200);
    }
  });
  return { el: h("div", {}, h("span", {}, label), code, cp), code };
}

export async function renderDatabases(root) {
  const me = session.current();
  let data = await api.get("/api/databases");
  let info = await api.get("/api/info");
  let selected = null;
  let conn = null;
  let revealed = null;

  const body = h("div", { class: "pb" });
  const countChip = h("span", { class: "chip" });
  const newBtn = h("button", { class: "btn pri", id: "dbNewBtn", type: "button" }, icon("plus"), "Buat database");
  root.append(h("section", {}, h("div", { class: "ph" }, h("div", {}, h("h1", {}, "Database"), h("p", {}, "PostgreSQL di server ini. Semua database milikmu memakai satu akun: ", h("span", { class: "mono" }, me.username), ".")), h("div", { class: "acts" }, countChip, newBtn)), body));

  const full = () => data.used >= data.limit;
  const blocked = () => full() || data.pg_down;

  function createDialog() {
    const input = h("input", { class: "f mono", "aria-label": "Nama database", placeholder: "billing", autocomplete: "off", spellcheck: "false" });
    return dialog({
      title: "Buat database",
      submit: "Buat database",
      body: [h("div", { class: "fld" }, h("label", {}, "Nama"), h("div", { class: "rowflex" }, h("span", { class: "mono muted" }, `${me.username}_`), input), h("div", { class: "hint" }, "Huruf kecil, angka, dan garis bawah; diawali huruf."))],
      onSubmit: async () => {
        const suffix = input.value.trim().toLowerCase();
        const bad = dbSuffixError(suffix);
        if (bad) throw new Error(bad);
        const d = await api.post("/api/databases", { suffix });
        selected = d.id;
        toast(`${d.name} dibuat`);
        await load();
      },
    });
  }
  newBtn.addEventListener("click", createDialog);

  function card(d) {
    const logoBox = h("span", { class: "pglogo" }, pgLogo());
    const del = h("button", { class: "btn sm ghost", type: "button", "aria-label": `Hapus ${d.name}`, onclick: () => remove(d) }, icon("trash-2"));
    const open = h("button", { class: "btn sm ghost", type: "button", "data-conn": d.name, onclick: () => { selected = d.id; revealed = null; conn = null; render(); loadConn(); } }, icon("plug"), "Koneksi");
    const used = d.apps.length ? d.apps.map((a) => h("span", { class: "chip" }, icon("boxes"), a)) : [h("span", { class: "muted", style: { "font-size": "12px" } }, "Belum dipakai aplikasi")];
    return h(
      "article",
      { class: "dbcard", "data-db": d.name },
      h("div", { class: "h" }, logoBox, h("div", {}, h("b", { class: "mono" }, d.name), h("div", { class: "sub" }, `${fmtBytes(d.size_bytes)} · dibuat ${fmtDate(d.created_at)}`)), h("span", { class: "r" }, h("span", { class: "dot ok", title: "Aktif" }))),
      h("div", { class: "used" }, used),
      h("div", { class: "ft" }, h("span", {}, `${d.connections} koneksi aktif`), h("div", { class: "r" }, open, del)),
    );
  }

  function remove(d) {
    const note = d.apps.length ? `Masih dipakai oleh ${d.apps.join(", ")}. Lepaskan dulu dari aplikasi itu.` : "Semua tabel dan datanya hilang. Backup yang tersimpan ikut dihapus.";
    dialog({
      title: `Hapus ${d.name}`,
      submit: "Hapus database",
      danger: true,
      confirmWord: d.name,
      body: [h("p", {}, note)],
      onSubmit: async () => {
        await api.del(`/api/databases/${d.id}`, { confirm: d.name });
        toast(`${d.name} dihapus`);
        if (selected === d.id) selected = null;
        await load();
      },
    });
  }

  function newCard() {
    const reason = data.pg_down ? "PostgreSQL sedang tidak bisa diakses." : full() ? `${data.used} dari ${data.limit}. Minta admin menaikkan batas, atau hapus database yang tidak dipakai.` : `Sisa ${data.limit - data.used} dari batas ${data.limit} yang diatur admin`;
    const title = data.pg_down ? "PostgreSQL tidak aktif" : full() ? "Batas database tercapai" : "Buat database";
    const b = h("button", { class: "dbnew", id: "dbNewCard", type: "button", "aria-disabled": blocked() ? "true" : null }, icon(blocked() ? "lock" : "plus"), h("b", {}, title), h("span", {}, reason));
    b.addEventListener("click", () => !blocked() && createDialog());
    return b;
  }

  function connCard(d) {
    const rows = conn
      ? [kvRow("Host", conn.host || "(IP publik belum diatur admin)"), kvRow("Port", String(conn.port)), kvRow("Database", d.name), kvRow("User", conn.user), kvRow("Password", revealed ?? "••••••••••••••••", { secret: revealed === null })]
      : [];
    const pw = rows[4];
    const showBtn = h("button", { class: "btn sm ghost", type: "button" }, icon("eye"), revealed ? "Sembunyikan password" : "Lihat password");
    showBtn.addEventListener("click", () =>
      act(showBtn, async () => {
        if (revealed) revealed = null;
        else revealed = (await api.post(`/api/databases/${d.id}/connection/reveal`)).password;
        render();
      }),
    );
    if (pw && revealed) pw.code.dataset.plain = revealed;
    const rotate = h("button", { class: "btn sm ghost", type: "button" }, icon("key-round"), "Ganti password");
    rotate.addEventListener("click", () =>
      confirm({
        title: "Ganti password database?",
        message: "Berlaku untuk semua database milikmu. Aplikasi yang memakai database mendapat password baru setelah deploy ulang; alat luar harus memakai password baru.",
        submit: "Ganti password",
        onSubmit: async () => {
          await api.post("/api/databases/password");
          revealed = null;
          toast("Password database diganti");
          await loadConn();
        },
      }),
    );
    return h(
      "section",
      { class: "card conn", id: "connCard" },
      h("header", {}, h("h3", {}, h("span", { class: "mono" }, d.name), " · koneksi"), h("div", { class: "r" }, showBtn, rotate)),
      h(
        "div",
        { class: "bd" },
        conn ? h("div", { class: "kvbox" }, rows.map((r) => r.el)) : h("div", { class: "loading" }, "Memuat…"),
        h("div", { class: "gnote" }, icon("info"), h("span", {}, "Aplikasi tidak perlu mengisi ini. Runova memberi ", h("b", { class: "mono" }, "DATABASE_URL"), " dan ", h("b", { class: "mono" }, "PG*"), " otomatis ke aplikasi yang memakai database ini. Koneksi dari luar server (misalnya DBeaver) hanya bisa dari jaringan yang diizinkan admin", info.public_ip ? "." : ", dan butuh IP publik server yang diatur admin.")),
      ),
    );
  }

  const bk = { list: [], busy: false };
  function backupCard(d) {
    const now = h("button", { class: "btn sm", type: "button", disabled: data.pg_down }, icon("archive"), "Backup sekarang");
    now.addEventListener("click", () =>
      act(now, async () => {
        await api.post(`/api/databases/${d.id}/backups`);
        await loadBackups(d);
      }, "Backup dibuat"),
    );
    const rows = bk.list.map((b) => {
      const dl = h("a", { class: "btn sm ghost", href: `/api/databases/${d.id}/backups/${encodeURIComponent(b.id)}`, download: "" }, icon("download"), "Unduh");
      const restore = h("button", { class: "btn sm", type: "button", disabled: data.pg_down }, icon("history"), "Restore");
      restore.addEventListener("click", () =>
        dialog({
          title: `Restore ${d.name}`,
          submit: "Restore",
          danger: true,
          confirmWord: d.name,
          body: [h("p", {}, `Isi database diganti dengan backup ${fmtDateTime(b.at)}. Koneksi yang sedang terbuka diputus, dan aplikasi yang memakainya mungkin perlu deploy ulang.`)],
          onSubmit: async () => {
            await api.post(`/api/databases/${d.id}/backups/${encodeURIComponent(b.id)}/restore`, { confirm: d.name });
            toast("Backup di-restore");
            await loadConn();
          },
        }),
      );
      return h("tr", {}, h("td", {}, fmtDateTime(b.at)), h("td", {}, b.manual ? h("span", { class: "chip" }, "manual") : h("span", { class: "muted" }, "otomatis")), h("td", { class: "num" }, fmtBytes(b.size_bytes)), h("td", { class: "num nowrap" }, dl, " ", restore));
    });
    return h(
      "section",
      { class: "card backups", id: "backupCard" },
      h("header", {}, h("div", {}, h("h3", {}, "Backup"), h("p", {}, "Otomatis tiap malam pukul 02:00, disimpan 7 hari.")), h("div", { class: "r" }, now)),
      rows.length
        ? h("table", { class: "t" }, h("thead", {}, h("tr", {}, h("th", {}, "Waktu"), h("th", {}, "Jenis"), h("th", { class: "num" }, "Ukuran"), h("th", {}))), h("tbody", {}, rows))
        : h("div", { class: "bd muted" }, "Belum ada backup."),
    );
  }

  function render() {
    countChip.textContent = `${data.used} dari ${data.limit} database`;
    newBtn.disabled = blocked();
    newBtn.title = full() ? "Batas database tercapai" : data.pg_down ? "PostgreSQL tidak bisa diakses" : "";
    if (!data.databases.length) {
      fill(body, data.pg_down ? pgDownBanner() : null, h("div", { class: "dbgrid" }, newCard()));
      return;
    }
    const sel = data.databases.find((d) => d.id === selected);
    fill(body, data.pg_down ? pgDownBanner() : null, h("div", { class: "dbgrid" }, data.databases.map(card), newCard()), sel ? [connCard(sel), backupCard(sel)] : null);
  }

  const pgDownBanner = () => h("div", { class: "banner warn", role: "status" }, icon("triangle-alert"), h("span", {}, "PostgreSQL tidak bisa diakses. Daftar tampil dari catatan Runova; ukuran dan koneksi mungkin tidak terbaru."));

  async function loadConn() {
    const d = data.databases.find((x) => x.id === selected);
    if (!d) return;
    try {
      conn = await api.get(`/api/databases/${d.id}/connection`);
    } catch (e) {
      conn = null;
      toast(e.message, "err");
    }
    await loadBackups(d);
  }

  async function loadBackups(d) {
    try {
      bk.list = await api.get(`/api/databases/${d.id}/backups`);
    } catch {
      bk.list = [];
    }
    render();
  }

  async function load() {
    data = await api.get("/api/databases");
    if (selected && !data.databases.some((d) => d.id === selected)) selected = null;
    if (!selected && data.databases.length === 1) selected = data.databases[0].id;
    render();
    if (selected) await loadConn();
  }

  if (data.databases.length === 1) selected = data.databases[0].id;
  render();
  if (selected) loadConn();

  const timer = setInterval(async () => {
    if (document.visibilityState !== "visible" || document.querySelector(".dlg-scrim")) return;
    try {
      const next = await api.get("/api/databases");
      const same = JSON.stringify(next) === JSON.stringify(data);
      data = next;
      if (!same) render();
    } catch {
      // the next tick retries
    }
  }, POLL_MS);
  return () => clearInterval(timer);
}
