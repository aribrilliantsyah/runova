import * as api from "/assets/js/api.js";
import { h, icon, fmtMB, fmtSize, fmtBytes, fmtTime, fmtDateTime, pct, bar, initials, poll, fill } from "/assets/js/dom.js";
import { ctx } from "/assets/js/ctx.js";
import { kindLogo, runtimeSub, statusOf } from "/assets/js/kinds.js";
import { emptyState } from "/assets/js/components/list.js";

const POLL_MS = 5000;
const ORDER = { failed: 0, deploying: 1, starting: 2, running: 3, stopped: 4 };

/** One quota line: what is used against the user's quota. */
function usage(ic, label, used, max, text) {
  const p = pct(used, max);
  return h("div", { class: `stat ${used > max ? "over" : ""}`.trim() }, h("div", { class: "l" }, icon(ic), h("span", {}, label), h("span", { class: "pct" }, `${p}%`)), h("div", { class: "v" }, text), bar(p));
}

/**
 * #/users/:id (admin): one user's profile, quota and usage, apps and databases. The app rows open
 * the app's own page, where the admin sees everything and runs it (restart, stop, start).
 */
export async function renderUser(root, params) {
  const id = Number(params.id);
  const body = h("div", { class: "pb" }, h("div", { class: "loading" }, "Memuat…"));
  const head = h("div", { class: "ph" });
  root.append(h("section", { id: "v-user" }, head, body));

  let data;
  async function load() {
    data = await api.get(`/api/users/${id}`);
    paint();
  }

  function paint() {
    const { user: u, apps, summary, databases, pg_down: pgDown } = data;
    const name = u.display_name || u.username;
    ctx.shell.setCrumb([["users", "Pengguna", "/users"], ["user", u.username, `/users/${u.id}`]]);
    document.title = `${u.username} · Pengguna · Runova`;
    fill(
      head,
      h(
        "div",
        { class: "dhead" },
        h("span", { class: "av lg", "aria-hidden": "true" }, initials(u)),
        h(
          "div",
          {},
          h("h1", {}, name, " ", u.role === "admin" ? h("span", { class: "chip info" }, "Admin") : null),
          h("p", {}, h("span", { class: "mono" }, u.username), u.email ? ` · ${u.email}` : null, ` · dibuat ${fmtTime(u.created_at)} · `, u.last_login_at ? h("span", { title: fmtDateTime(u.last_login_at) }, `login terakhir ${fmtTime(u.last_login_at)}`) : "belum pernah login"),
        ),
      ),
      h("div", { class: "acts" }, h("a", { class: "btn", href: `#/activity?q=${encodeURIComponent(`pelaku:${u.username}`)}` }, icon("history"), "Aktivitas")),
    );

    const us = u.usage;
    const q = u.quota;
    const diskMax = (q.disk_mb || 0) * 1048576;
    const stats = h(
      "div",
      { class: "stats", id: "ustats" },
      usage("package", "Aplikasi", us.apps, q.apps, [String(us.apps), " ", h("small", {}, `dari ${q.apps}`)]),
      usage("memory-stick", "Memori", us.memory_mb, q.memory_mb, [fmtMB(us.memory_mb), " ", h("small", {}, `dari ${fmtMB(q.memory_mb)}`)]),
      usage("hard-drive", "Disk terisi", us.disk_used_bytes || 0, diskMax, [fmtSize(us.disk_used_bytes || 0), " ", h("small", {}, `dari ${fmtMB(q.disk_mb || 0)}`)]),
      usage("database", "Database", us.databases, q.databases, [String(us.databases), " ", h("small", {}, `dari ${q.databases}`)]),
    );

    const sorted = [...apps].sort((a, b) => (ORDER[a.status] ?? 9) - (ORDER[b.status] ?? 9) || a.name.localeCompare(b.name));
    const appRows = sorted.map((a) => {
      const s = summary[a.id] || {};
      const [cls, label] = statusOf(a.status);
      const dom = s.domains?.[0];
      return h(
        "tr",
        { "data-app": a.name },
        h("td", { class: "u-app" }, h("a", { href: `#/apps/${a.id}`, class: "u-applink" }, h("span", { class: "lgbox" }, kindLogo(a.kind, s.language)), h("span", {}, h("b", {}, a.name), h("small", {}, runtimeSub(a, s.language))))),
        h("td", { "data-label": "Status" }, h("span", { class: `chip ${cls}` }, h("span", { class: `dot ${cls}` }), label), a.disk_level === "full" ? [" ", h("span", { class: "chip err" }, "Disk penuh")] : null),
        h("td", { "data-label": "Versi", class: "mono" }, s.version ? `v${s.version}` : h("span", { class: "faint" }, "–")),
        h("td", { "data-label": "Domain", class: "u-dom" }, dom ? [h("span", { class: "mono" }, dom.host), s.domains.length > 1 ? h("span", { class: "faint" }, ` +${s.domains.length - 1}`) : null] : h("span", { class: "faint" }, "Belum ada")),
        h("td", { "data-label": "Memori", class: "nowrap" }, fmtMB(a.memory_mb)),
        h("td", { "data-label": "Deploy terakhir", class: "nowrap muted", title: s.deployed_at ? fmtDateTime(s.deployed_at) : null }, s.deployed_at ? fmtTime(s.deployed_at) : "belum pernah"),
        h("td", { class: "num u-open" }, h("a", { class: "btn sm", href: `#/apps/${a.id}`, "aria-label": `Buka ${a.name}` }, "Buka", icon("arrow-right"))),
      );
    });
    const appsCard = h(
      "section",
      { class: "card", id: "uapps" },
      h("header", {}, h("h3", {}, "Aplikasi"), h("span", { class: "faint" }, String(apps.length))),
      apps.length
        ? h("table", { class: "t uapps" }, h("thead", {}, h("tr", {}, ["Aplikasi", "Status", "Versi", "Domain", "Memori", "Deploy terakhir", ""].map((t, i) => h("th", { class: i === 6 ? "num" : "" }, t || h("span", { class: "sr" }, "Buka"))))), h("tbody", {}, appRows))
        : emptyState("package", "Belum ada aplikasi", `${u.username} belum men-deploy aplikasi apa pun.`),
    );

    const dbCard = h(
      "section",
      { class: "card", id: "udbs" },
      h("header", {}, h("h3", {}, "Database"), h("span", { class: "faint" }, String(databases.length))),
      pgDown ? h("p", { class: "hint udb-note" }, "PostgreSQL sedang tidak bisa diakses; ukuran tidak diketahui.") : null,
      databases.length
        ? h("ul", { class: "udbs" }, databases.map((d) => h("li", {}, h("b", { class: "mono" }, d.name), h("span", { class: "muted" }, d.apps.length ? `dipakai ${d.apps.join(", ")}` : "tidak dipakai aplikasi"), h("span", { class: "num" }, pgDown ? "–" : fmtBytes(d.size_bytes)))))
        : emptyState("database", "Belum ada database", null),
    );

    body.replaceChildren(stats, appsCard, dbCard);
  }

  try {
    await load();
  } catch (e) {
    head.replaceChildren(h("div", {}, h("h1", {}, "Pengguna")));
    body.replaceChildren(emptyState("circle-alert", e.status === 404 ? "Pengguna tidak ditemukan" : "Pengguna tidak bisa dimuat", e.message, h("a", { class: "btn", href: "#/users" }, icon("arrow-left"), "Kembali ke Pengguna")));
    return null;
  }
  return poll(() => load().catch(() => {}), POLL_MS);
}
