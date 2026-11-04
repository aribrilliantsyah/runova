import { h, icon, fmtTime, fmtMB, fmtCPU } from "/assets/js/dom.js";
import { kindLogo, runtimeSub, statusOf } from "/assets/js/kinds.js";
import { ipHost } from "/assets/js/components/iphost.js";

/** appCard(app, summary, info) → the clickable card of the app list; info is GET /api/info. */
export function appCard(app, summary, info) {
  const ip = ipHost(info);
  const [cls, label] = statusOf(app.status);
  const logo = h("span", { class: "lgbox" }, kindLogo(app.kind, summary?.language));
  const dom = summary?.domains?.[0];
  const extra = (summary?.domains?.length || 0) - 1;
  const domainRow = dom
    ? h("div", {}, icon("lock"), h("span", { class: "mono" }, dom.host), extra > 0 ? h("span", { class: "faint" }, `+${extra}`) : null)
    : h("div", {}, icon("globe"), h("span", {}, "Belum ada domain"));
  const ipRow = app.ip_access
    ? h("div", {}, icon("network"), h("span", { class: "mono", title: ip.title || null }, `${ip.host}:${app.ip_port}`), ip.local ? h("span", { class: "lan" }, "LAN") : null)
    : h("div", {}, icon("network"), h("span", {}, "Akses IP ditutup"));
  const settled = app.status === "running";
  const foot = settled
    ? [h("span", {}, `${fmtMB(app.memory_mb)} · ${fmtCPU(app.cpu_milli)}`)]
    : [h("span", { class: `chip ${cls}` }, label)];
  const when = summary?.deployed_at ? fmtTime(summary.deployed_at) : "belum pernah";
  return h(
    "a",
    { class: "acard", href: `#/apps/${app.id}`, "aria-label": `${app.name}, ${label}` },
    h(
      "div",
      { class: "h" },
      logo,
      h("div", {}, h("b", {}, app.name), h("div", { class: "sub" }, runtimeSub(app, summary?.language))),
      h("span", { class: `dot ${cls}`, title: label }),
    ),
    h("div", { class: "ep" }, domainRow, ipRow),
    h("div", { class: "ft" }, ...foot, h("span", { style: { "margin-left": "auto" } }, when)),
  );
}
