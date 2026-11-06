// The signed-in frame: sidebar, top bar, bottom navigation (phones), "Lainnya" sheet.
import * as api from "/assets/js/api.js";
import { h, icon, bar, setBar, pct, fmtMB, fill, copyText, $ } from "/assets/js/dom.js";
import { logo } from "/assets/js/logo.js";
import * as session from "/assets/js/session.js";
import { bell } from "/assets/js/bell.js";
import { dialog } from "/assets/js/components/dialog.js";
import { toast } from "/assets/js/toast.js";

const UPDATE_COMMAND = "sudo runova update";
const UPDATE_POLL_MS = 30 * 60 * 1000;

const NAV_KEY = { apps: "apps", app: "apps", new: "new", db: "db", activity: "activity", users: "users", server: "server" };

/**
 * buildShell({onLogout}) → {root, content, setView, setCrumb, refresh, mbar, destroy}.
 * content is where pages render.
 */
export function buildShell({ onLogout }) {
  const u = session.current();
  const admin = session.isAdmin();

  const navLink = (key, href, ic, label, count) =>
    h("a", { href: `#${href}`, "data-nav": key }, icon(ic), label, count ? h("span", { class: "n", "data-count": key }) : null);

  const memBar = bar(0);
  const diskBar = bar(0);
  const memPct = h("span", {}, "0%");
  const diskPct = h("span", {}, "0%");
  const quotaWho = h("span", { class: "muted" }, u.username);
  const usage = h(
    "div",
    { class: "usage" },
    h("div", { class: "row1" }, h("b", {}, "Kuota kamu"), quotaWho),
    h("div", { class: "mini" }, icon("memory-stick"), memBar, memPct, icon("hard-drive"), diskBar, diskPct),
  );

  const acctBtn = h(
    "button",
    { class: "acct", type: "button", "aria-haspopup": "menu", "aria-label": `Akun ${session.displayName()}` },
    session.avatar(u),
    h("div", {}, h("b", {}, session.displayName()), h("span", { class: "sub" }, u.email || u.username)),
    icon("chevrons-up-down"),
  );
  acctBtn.addEventListener("click", () => session.accountMenu(acctBtn, { onLogout }));

  const side = h(
    "aside",
    { class: "side" },
    h("div", { class: "ws" }, logo("logo"), h("div", {}, h("b", {}, "Runova"), h("span", { class: "sub", id: "ws-sub" }, location.hostname))),
    h(
      "div",
      { class: "grp" },
      h("h6", {}, "Workspace"),
      h(
        "nav",
        { class: "nav", "aria-label": "Utama" },
        navLink("apps", "/apps", "package", "Aplikasi", true),
        navLink("new", "/deploy", "cloud-upload", "Deploy baru"),
        navLink("db", "/databases", "database", "Database", true),
        navLink("activity", "/activity", "history", "Aktivitas"),
      ),
    ),
    admin
      ? h(
          "div",
          { class: "grp" },
          h("h6", {}, "Administrasi"),
          h("nav", { class: "nav", "aria-label": "Administrasi" }, navLink("users", "/users", "users", "Pengguna", true), navLink("server", "/server", "server", "Server")),
        )
      : null,
    h("div", { class: "side-foot" }, usage, acctBtn),
  );

  const crumb = h("nav", { class: "crumb", id: "crumb", "aria-label": "Lokasi" });
  const collapseBtn = h("button", { class: "ibtn dsk", type: "button", "aria-label": "Ciutkan sidebar" }, icon("panel-left"));
  const themeBtn = h("button", { class: "ibtn", id: "themeBtn", type: "button", "aria-label": "Ganti tema" }, icon(session.isDark() ? "sun" : "moon"));
  themeBtn.addEventListener("click", () => session.toggleTheme());
  const onTheme = () => themeBtn.replaceChildren(icon(session.isDark() ? "sun" : "moon"));
  addEventListener("runova:theme", onTheme);

  const searchBtn = h("button", { class: "kbd", type: "button", "aria-label": "Cari aplikasi" }, icon("search"), "Cari aplikasi", h("kbd", {}, "Ctrl K"));
  const msearch = h("button", { class: "ibtn msearch", type: "button", "aria-label": "Cari aplikasi" }, icon("search"));
  const bellCtl = bell();

  const top = h(
    "header",
    { class: "top" },
    h("span", { class: "mlogo", "aria-hidden": "true" }, logo("")),
    h("a", { class: "ibtn mback", href: "#/apps", "aria-label": "Kembali ke aplikasi" }, icon("arrow-left")),
    collapseBtn,
    h("span", { class: "vsep dsk" }),
    crumb,
    h("div", { class: "right" }, searchBtn, msearch, bellCtl.button, themeBtn),
  );

  const content = h("main", { class: "sheet", id: "content", tabindex: "-1" });

  const bnLink = (key, href, ic, label, cls = "") =>
    h("a", { href: `#${href}`, class: cls, "data-nav": key }, cls === "fab" ? h("span", { class: "fabic" }, icon(ic)) : icon(ic), h("span", {}, label));
  const moreBtn = h("button", { class: "bn-more", type: "button", "aria-haspopup": "dialog" }, icon("ellipsis"), h("span", {}, "Lainnya"));
  const bnav = h(
    "nav",
    { class: "bnav", "aria-label": "Navigasi bawah" },
    bnLink("apps", "/apps", "package", "Aplikasi"),
    bnLink("db", "/databases", "database", "Database"),
    bnLink("new", "/deploy", "plus", "Deploy", "fab"),
    admin ? bnLink("users", "/users", "users", "Pengguna") : bnLink("activity", "/activity", "history", "Aktivitas"),
    moreBtn,
  );

  const mprev = h("button", { class: "btn", type: "button" }, icon("arrow-left"), h("span", {}, "Batal"));
  const mnext = h("button", { class: "btn dark", type: "button" }, "Lanjut", icon("arrow-right"));
  const mdeploy = h("button", { class: "btn pri", type: "button", disabled: true }, icon("rocket"), "Deploy");
  const mbar = h("div", { class: "mbar" }, mprev, mnext, mdeploy);

  // Admin-only notice that a newer release exists; it cannot be dismissed and goes away once the server is updated.
  const upd = h("div", { class: "banner info upd", role: "status", hidden: true });
  const main = h("div", { class: "main" }, top, upd, content, bnav, mbar);

  function paintUpdate(st) {
    if (!st?.update_available) {
      upd.hidden = true;
      return;
    }
    const copy = h("button", { class: "btn sm", type: "button", "aria-label": `Salin perintah ${UPDATE_COMMAND}` }, icon("copy"), "Salin");
    copy.addEventListener("click", async () => toast((await copyText(UPDATE_COMMAND)) ? "Perintah disalin" : "Tidak bisa menyalin; salin manual"));
    upd.replaceChildren(
      icon("download"),
      h("span", { class: "msg" }, `Runova v${st.latest} tersedia (terpasang: v${st.current}). Perbarui di server: `, h("code", {}, UPDATE_COMMAND)),
      h("div", { class: "r" }, copy, h("a", { class: "btn sm ghost", href: st.release_url, target: "_blank", rel: "noopener noreferrer" }, "Catatan rilis", icon("external-link"))),
    );
    upd.hidden = false;
  }
  async function refreshUpdate() {
    if (!admin) return;
    try {
      paintUpdate(await api.get("/api/update"));
    } catch {
      upd.hidden = true;
    }
  }
  const updTimer = admin ? setInterval(refreshUpdate, UPDATE_POLL_MS) : null;
  addEventListener("runova:update", refreshUpdate);
  refreshUpdate();

  // "Lainnya" bottom sheet
  const sheetUsage = h("span", { class: "muted" });
  const sheetMem = bar(0);
  const sheetDisk = bar(0);
  const sheetMemPct = h("span", {}, "0%");
  const sheetDiskPct = h("span", {}, "0%");
  const themeSw = h("span", { class: "sw", role: "switch", "aria-checked": String(session.isDark()), "aria-label": "Mode gelap" });
  const mitem = (ic, label, fn, cls = "") =>
    h("button", { class: cls, type: "button", onclick: () => { closeSheet(); fn(); } }, icon(ic), label, cls === "danger" ? null : icon("chevron-right"));
  const themeItem = h("button", { type: "button", onclick: () => { session.toggleTheme(); themeSw.setAttribute("aria-checked", String(session.isDark())); } }, icon("moon"), "Mode gelap", themeSw);
  const mlist = h(
    "div",
    { class: "mlist" },
    admin ? mitem("history", "Aktivitas", () => (location.hash = "#/activity")) : null,
    admin ? mitem("server", "Server", () => (location.hash = "#/server")) : null,
    themeItem,
    mitem("key-round", "Ganti password", session.changePasswordDialog),
    mitem("log-out", "Keluar", onLogout, "danger"),
  );
  const mpanel = h(
    "div",
    { class: "mpanel", role: "dialog", "aria-label": "Menu lainnya" },
    h("div", { class: "grab" }),
    h("div", { class: "acct-static acct" }, session.avatar(u), h("div", {}, h("b", {}, session.displayName()), h("span", { class: "sub" }, `${u.email || u.username} · ${admin ? "admin" : "pengguna"}`))),
    h("div", { class: "usage" }, h("div", { class: "row1" }, h("b", {}, "Kuota kamu"), sheetUsage), h("div", { class: "mini" }, icon("memory-stick"), sheetMem, sheetMemPct, icon("hard-drive"), sheetDisk, sheetDiskPct)),
    mlist,
  );
  const sheet = h("div", { class: "msheet", hidden: true }, h("div", { class: "scrim", onclick: () => closeSheet() }), mpanel);
  function openSheet() {
    sheet.hidden = false;
    mpanel.querySelector("button").focus();
  }
  function closeSheet() {
    sheet.hidden = true;
    moreBtn.focus();
  }
  moreBtn.addEventListener("click", openSheet);
  sheet.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closeSheet();
  });

  const root = h("div", { class: "app", "data-view": "apps" }, side, main, sheet);

  collapseBtn.addEventListener("click", () => {
    const on = root.classList.toggle("collapsed");
    collapseBtn.setAttribute("aria-label", on ? "Tampilkan sidebar" : "Ciutkan sidebar");
  });

  // Search
  let appList = [];
  function search() {
    const input = h("input", { class: "f", type: "search", "aria-label": "Cari aplikasi", placeholder: "Nama aplikasi", autocomplete: "off" });
    const results = h("div", { class: "stack" });
    const fill = () => {
      const q = input.value.trim().toLowerCase();
      const hits = appList.filter((a) => a.name.includes(q)).slice(0, 8);
      results.replaceChildren(
        ...(hits.length
          ? hits.map((a) => h("a", { class: "btn", href: `#/apps/${a.id}`, onclick: () => document.querySelector(".dlg-scrim")?.remove() }, icon("package"), a.name))
          : [h("p", { class: "muted" }, "Tidak ada aplikasi yang cocok")]),
      );
    };
    input.addEventListener("input", fill);
    fill();
    return dialog({
      title: "Cari aplikasi",
      submit: "Buka",
      body: [input, results],
      onSubmit: async () => {
        const first = appList.find((a) => a.name.includes(input.value.trim().toLowerCase()));
        if (first) location.hash = `#/apps/${first.id}`;
      },
    });
  }
  searchBtn.addEventListener("click", search);
  msearch.addEventListener("click", search);
  const onKey = (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      search();
    }
  };
  addEventListener("keydown", onKey);

  const setCount = (key, text) => {
    const el = $(`[data-count="${key}"]`, root);
    if (el) el.textContent = text;
  };

  async function refresh() {
    const jobs = [
      api.get("/api/apps").then((r) => {
        appList = r.apps;
        const use = r.usage;
        const memP = pct(use.memory_mb, use.quota.memory_mb);
        const diskP = pct(use.disk_mb || 0, use.quota.disk_mb || 0);
        setBar(memBar, memP);
        setBar(diskBar, diskP);
        setBar(sheetMem, memP);
        setBar(sheetDisk, diskP);
        memPct.textContent = sheetMemPct.textContent = `${memP}%`;
        diskPct.textContent = sheetDiskPct.textContent = `${diskP}%`;
        sheetUsage.textContent = `${r.apps.length} dari ${use.quota.apps} aplikasi`;
        memBar.title = sheetMem.title = `Memori ${fmtMB(use.memory_mb)} dibagikan dari ${fmtMB(use.quota.memory_mb)}`;
        diskBar.title = sheetDisk.title = `Disk ${fmtMB(use.disk_mb || 0)} dibagikan dari ${fmtMB(use.quota.disk_mb || 0)}`;
        setCount("apps", String(r.apps.length));
      }),
      api.get("/api/databases").then((r) => setCount("db", `${r.used}/${r.limit}`)),
    ];
    if (admin) jobs.push(api.get("/api/users?limit=1").then((r) => setCount("users", String(r.total))));
    await Promise.allSettled(jobs);
  }

  function setView(view) {
    root.dataset.view = view;
    const key = NAV_KEY[view];
    for (const a of root.querySelectorAll("[data-nav]")) {
      if (a.dataset.nav === key) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    }
    const more = view === "server" || (admin && view === "activity");
    moreBtn.toggleAttribute("aria-current", more);
    if (more) moreBtn.setAttribute("aria-current", "page");
    closeSheetQuiet();
  }
  function closeSheetQuiet() {
    sheet.hidden = true;
  }

  /** setCrumb([[icon|Node, label, href?], ...]) */
  function setCrumb(items) {
    fill(
      crumb,
      ...items.flatMap(([ic, label, href], i) => [
        i ? h("span", { class: "sl", "aria-hidden": "true" }, "/") : null,
        h("a", { href: `#${href || "/apps"}`, "aria-current": i === items.length - 1 ? "page" : null }, typeof ic === "string" ? icon(ic) : ic, label),
      ]),
    );
    const last = items[items.length - 1];
    document.title = `${last[1]} · Runova`;
  }

  const bellStop = bellCtl.stop;
  return {
    root,
    content,
    setView,
    setCrumb,
    refresh,
    refreshBell: bellCtl.refresh,
    mbar: { prev: mprev, prevText: mprev.lastChild, next: mnext, deploy: mdeploy },
    destroy() {
      bellStop();
      clearInterval(updTimer);
      removeEventListener("runova:update", refreshUpdate);
      removeEventListener("keydown", onKey);
      removeEventListener("runova:theme", onTheme);
    },
  };
}
