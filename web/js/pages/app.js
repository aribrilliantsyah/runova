import * as api from "/assets/js/api.js";
import { h, icon, poll, fill } from "/assets/js/dom.js";
import { ctx } from "/assets/js/ctx.js";
import * as session from "/assets/js/session.js";
import { kindLogo, runtimeLabel, runtimeSub, statusOf } from "/assets/js/kinds.js";
import { renderGeneral } from "/assets/js/pages/app/general.js";
import { renderDeployments } from "/assets/js/pages/app/deployments.js";
import { renderDomains } from "/assets/js/pages/app/domains.js";
import { renderEnv } from "/assets/js/pages/app/env.js";
import { renderLogs } from "/assets/js/pages/app/logs.js";
import { renderMonitor } from "/assets/js/pages/app/monitor.js";
import { renderAdvanced } from "/assets/js/pages/app/advanced.js";

const POLL_MS = 5000;
const HISTORY = 60;

const TABS = [
  ["general", "", "Umum", renderGeneral],
  ["env", "env", "Environment", renderEnv],
  ["domains", "domains", "Domain", renderDomains],
  ["deployments", "deployments", "Deployment", renderDeployments],
  ["logs", "logs", "Log", renderLogs],
  ["monitor", "monitor", "Monitoring", renderMonitor],
  ["advanced", "advanced", "Lanjutan", renderAdvanced],
];

export const tabFromSlug = (slug) => TABS.find((t) => t[1] === (slug || ""));

export async function renderApp(root, params) {
  const id = Number(params.id);
  const me = session.current();
  const [detail, info] = await Promise.all([api.get(`/api/apps/${id}`), api.get("/api/info")]);

  // page is what the tabs share: the latest detail, a few samples of usage, and helpers.
  const page = {
    id,
    me,
    info,
    state: detail, // {app, owner, env, deployment, stats, busy}
    history: { mem: [], cpu: [] },
    get app() {
      return this.state.app;
    },
    get mine() {
      return this.state.app.user_id === me.id;
    },
    path: (tab) => `#/apps/${id}${tab ? `/${tab}` : ""}`,
    async reload() {
      this.state = await api.get(`/api/apps/${id}`);
      sample();
      paintHeader();
      current?.update?.();
      ctx.shell.refresh();
    },
  };

  function sample() {
    const s = page.state.stats;
    if (!s) return;
    const { mem, cpu } = page.history;
    mem.push(s.memory_bytes);
    cpu.push(s.cpu_percent);
    if (mem.length > HISTORY) mem.shift();
    if (cpu.length > HISTORY) cpu.shift();
  }
  sample();

  // Header
  const title = h("h1", { style: { display: "flex", "align-items": "center", gap: "8px", "flex-wrap": "wrap" } });
  const subline = h("p", { class: "mono" });
  const logoBox = h("span", { class: "lgbox" });
  const deleteHeader = h("a", { class: "btn sm ghost danger", href: page.path("advanced"), "aria-label": "Hapus aplikasi" }, icon("trash-2"));
  const links = h("div", { class: "links" });
  const head = h("div", { class: "ph" }, h("div", { class: "dhead" }, logoBox, h("div", {}, title, subline)), h("div", { class: "acts" }, deleteHeader));

  const tabsEl = h(
    "div",
    { class: "tabs", role: "tablist", "aria-label": "Bagian aplikasi" },
    TABS.map(([key, slug, label]) => h("a", { role: "tab", href: page.path(slug), "data-tab": key, "aria-selected": "false" }, label)),
  );
  const panel = h("div", { class: "tab", role: "tabpanel" });
  root.append(h("section", { id: "v-app" }, head, links, tabsEl, panel));

  let domainsCache = [];
  function paintHeader() {
    const { app, deployment, owner } = page.state;
    const [cls, label] = statusOf(app.status);
    const language = deployment?.report?.language;
    logoBox.replaceChildren(kindLogo(app.kind, language));
    fill(title, app.name, " ", h("span", { class: `chip ${cls}` }, h("span", { class: `dot ${cls}` }), page.state.busy && app.status !== "deploying" ? "Sedang diproses" : label));
    subline.textContent = deployment ? `v${deployment.number} · ${deployment.file_name} · ${runtimeSub(app, language)}` : `${runtimeLabel(app)} · belum ada deployment aktif`;
    if (!page.mine) subline.textContent += ` · milik ${owner}`;
    const host = info.public_ip || location.hostname;
    fill(links,
      ...domainsCache.map((d) => h("a", { class: "link", href: `https://${d.host}`, target: "_blank", rel: "noopener noreferrer" }, icon("lock"), d.host, icon("external-link"))),
      h(
        app.ip_access ? "a" : "span",
        { class: `link ${app.ip_access ? "" : "off"}`, ...(app.ip_access ? { href: `http://${host}:${app.ip_port}`, target: "_blank", rel: "noopener noreferrer" } : { title: "Akses IP ditutup" }) },
        icon("network"),
        `${host}:${app.ip_port}`,
        icon("external-link"),
      ),
    );
    ctx.shell.setCrumb([["boxes", "Aplikasi", "/apps"], [kindLogo(app.kind, language), app.name, `/apps/${id}`]]);
  }
  page.setDomains = (list) => {
    domainsCache = list;
    paintHeader();
  };
  api.get(`/api/apps/${id}/domains`).then((r) => page.setDomains(r.domains)).catch(() => {});
  paintHeader();

  // Tabs
  let current = null;
  let currentKey = null;
  function show(slug) {
    const entry = tabFromSlug(slug) || TABS[0];
    const [key, , , render] = entry;
    if (key === currentKey) return;
    current?.cleanup?.();
    currentKey = key;
    for (const a of tabsEl.children) a.setAttribute("aria-selected", String(a.dataset.tab === key));
    panel.replaceChildren();
    panel.id = `t-${key}`;
    current = render(page);
    panel.append(current.node);
    document.title = `${entry[2]} · ${page.app.name} · Runova`;
  }
  show(params.tab);

  const stop = poll(async () => {
    await page.reload();
  }, POLL_MS);

  return {
    cleanup() {
      stop();
      current?.cleanup?.();
    },
    update(p) {
      if (Number(p.id) !== id) return false;
      show(p.tab);
      return true;
    },
  };
}
