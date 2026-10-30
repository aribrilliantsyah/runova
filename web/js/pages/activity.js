import * as api from "/assets/js/api.js";
import { h, icon, fmtTime, fmtDateTime, clear, fill } from "/assets/js/dom.js";
import * as session from "/assets/js/session.js";

const PAGE = 50;

// action → [icon, tone, sentence]; the target is the app, user, database or file name.
const ACTIONS = {
  login: ["log-in", "", () => "Masuk"],
  login_failed: ["lock", "err", () => "Percobaan masuk gagal"],
  logout: ["log-out", "", () => "Keluar"],
  "user.create": ["user-plus", "ok", (a) => `Pengguna dibuat${detail(a)}`],
  "user.update": ["users", "", (a) => `Pengguna diubah${detail(a)}`],
  "user.delete": ["trash-2", "err", () => "Pengguna dihapus"],
  "quota.update": ["sliders-horizontal", "", (a) => `Kuota diubah${detail(a)}`],
  "password.change": ["key-round", "", (a) => (a.detail === "reset by admin" ? "Password direset admin" : "Password diganti")],
  "settings.update": ["server", "", () => "Setelan server diubah"],
  upload: ["upload", "", (a) => `Unggah ${a.detail || ""}`.trim()],
  "app.create": ["boxes", "ok", () => "Aplikasi dibuat"],
  "app.update": ["sliders-horizontal", "", () => "Pengaturan diubah"],
  "app.delete": ["trash-2", "err", () => "Aplikasi dihapus"],
  deploy: ["rocket", "ok", (a) => `Deploy ${a.detail || ""}`.trim()],
  rollback: ["history", "", (a) => `Kembalikan ${a.detail || ""}`.trim()],
  restart: ["rotate-cw", "", () => "Restart"],
  stop: ["square", "", () => "Dihentikan"],
  start: ["play", "", () => "Dijalankan"],
  "terminal.open": ["square-terminal", "", () => "Terminal dibuka"],
  "env.update": ["key-round", "", () => "Environment diubah"],
  "env.reveal": ["eye", "", (a) => `Nilai ${a.detail || "environment"} dilihat`],
  "domain.add": ["lock", "", (a) => `Domain ${a.detail || ""} ditambah`.trim()],
  "domain.remove": ["lock", "", (a) => `Domain ${a.detail || ""} dihapus`.trim()],
  "panel.address": ["globe", "", (a) => `Alamat panel: ${a.detail || ""}`],
  "db.create": ["database", "ok", () => "Database dibuat"],
  "db.delete": ["database", "err", () => "Database dihapus"],
  "db.backup": ["archive", "", () => "Backup dibuat"],
  "db.restore": ["archive", "", () => "Backup di-restore"],
  "db.password": ["key-round", "", () => "Password database diganti"],
  "db.reveal": ["eye", "", () => "Password database dilihat"],
};

function detail(a) {
  return a.detail ? ` (${a.detail})` : "";
}

export function describe(a) {
  const [ic, tone, text] = ACTIONS[a.action] || ["history", "", () => a.action];
  return { ic, tone, text: text(a) };
}

export async function renderActivity(root) {
  const me = session.current();
  const admin = session.isAdmin();
  let actor = "";
  let items = [];
  let more = false;
  let users = [];

  const tbody = h("tbody", { id: "actrows" });
  const moreBtn = h("button", { class: "btn", type: "button" }, "Muat lebih banyak");
  const moreBox = h("div", { class: "more", hidden: true }, moreBtn);
  const empty = h("div", { class: "empty", hidden: true }, h("span", { class: "ic" }, icon("history")), h("b", {}, "Belum ada aktivitas"));
  const filter = h("select", { class: "f", "aria-label": "Pelaku", style: { width: "200px" } }, h("option", { value: "" }, "Semua pelaku"));
  filter.addEventListener("change", () => {
    actor = filter.value;
    load(true);
  });
  const acts = admin ? h("div", { class: "acts" }, filter) : null;

  root.append(
    h(
      "section",
      {},
      h("div", { class: "ph" }, h("div", {}, h("h1", {}, "Aktivitas"), h("p", {}, admin ? "Deploy, restart, dan perubahan pengaturan di seluruh server." : "Deploy, restart, dan perubahan pengaturan di aplikasimu.")), acts),
      h("div", { class: "pb" }, h("section", { class: "card" }, h("table", { class: "t act" }, tbody), empty), moreBox),
    ),
  );

  function rowFor(a) {
    const d = describe(a);
    return h(
      "tr",
      {},
      h("td", { style: { width: "30px" } }, h("span", { class: `dot ${d.tone}` })),
      h("td", { style: { width: "30px", color: "var(--muted)" } }, icon(d.ic)),
      h("td", {}, a.target ? h("b", { style: { "font-weight": "500" } }, a.target) : null, a.target ? " " : null, h("span", { class: "muted" }, a.target ? `· ${d.text}` : d.text)),
      h("td", { class: "muted who" }, a.actor_name || "sistem"),
      h("td", { class: "num muted", title: fmtDateTime(a.at) }, fmtTime(a.at)),
    );
  }

  function paint() {
    fill(tbody, ...items.map(rowFor));
    empty.hidden = items.length > 0;
    moreBox.hidden = !more;
  }

  async function load(reset) {
    const before = !reset && items.length ? `&before=${items[items.length - 1].at}` : "";
    const who = actor ? `&actor=${actor}` : "";
    const page = await api.get(`/api/activity?limit=${PAGE}${before}${who}`);
    items = reset ? page : items.concat(page);
    more = page.length === PAGE;
    paint();
  }

  moreBtn.addEventListener("click", async () => {
    moreBtn.disabled = true;
    try {
      await load(false);
    } finally {
      moreBtn.disabled = false;
    }
  });

  if (admin) {
    api.get("/api/users").then((list) => {
      users = list;
      filter.append(...users.map((u) => h("option", { value: u.id }, u.id === me.id ? `${u.username} (kamu)` : u.username)));
    });
  }
  await load(true);
}
