import * as api from "/assets/js/api.js";
import { h, icon } from "/assets/js/dom.js";
import { route, start, go } from "/assets/js/router.js";
import * as session from "/assets/js/session.js";
import { renderLogin } from "/assets/js/pages/login.js";
import { buildShell } from "/assets/js/shell.js";
import { ctx } from "/assets/js/ctx.js";
import { closeAllDialogs } from "/assets/js/components/dialog.js";
import { closeTerminal } from "/assets/js/components/terminal.js";
import { renderApps } from "/assets/js/pages/apps.js";
import { renderDeploy } from "/assets/js/pages/deploy.js";
import { renderApp } from "/assets/js/pages/app.js";
import { renderDatabases } from "/assets/js/pages/databases.js";
import { renderActivity } from "/assets/js/pages/activity.js";
import { renderUsers } from "/assets/js/pages/users.js";
import { renderServer } from "/assets/js/pages/server.js";

const root = document.getElementById("root");
const DEFAULT_ROUTE = "/apps";
const SESSION_ENDED = "Sesi berakhir, silakan masuk lagi";

// view: which sidebar entry is current and which mobile chrome applies.
route("/", () => {}, { redirect: DEFAULT_ROUTE });
route("/apps", renderApps, { view: "apps", crumb: [["boxes", "Aplikasi", "/apps"]] });
route("/apps/:id/:tab?", renderApp, { view: "app" });
route("/deploy", renderDeploy, { view: "new", crumb: [["boxes", "Aplikasi", "/apps"], ["cloud-upload", "Deploy baru", "/deploy"]] });
route("/databases", renderDatabases, { view: "db", crumb: [["database", "Database", "/databases"]] });
route("/activity", renderActivity, { view: "activity", crumb: [["history", "Aktivitas", "/activity"]] });
route("/users", renderUsers, { view: "users", admin: true, crumb: [["users", "Pengguna", "/users"]] });
route("/server", renderServer, { view: "server", admin: true, crumb: [["server", "Server", "/server"]] });

let shell = null;
let stopRouter = null;

function teardown() {
  closeAllDialogs();
  closeTerminal();
  stopRouter?.();
  shell?.destroy();
  stopRouter = shell = ctx.shell = null;
}

function showLogin(message = "") {
  teardown();
  renderLogin(root, {
    message,
    onLogin: async (user) => {
      session.setUser(user);
      const back = sessionStorage.getItem(api.RETURN_KEY);
      sessionStorage.removeItem(api.RETURN_KEY);
      showApp();
      if (back && back.startsWith("#/")) location.hash = back;
    },
  });
}

function showApp() {
  teardown();
  shell = ctx.shell = buildShell({ onLogout });
  root.replaceChildren(shell.root);
  let lastMeta = null;
  stopRouter = start(shell.content, {
    before(meta) {
      if (meta.redirect) return meta.redirect;
      if (meta.admin && !session.isAdmin()) return DEFAULT_ROUTE;
      return null;
    },
    shown(meta) {
      shell.setView(meta.view);
      if (meta.crumb) shell.setCrumb(meta.crumb);
      if (lastMeta !== meta) shell.content.scrollTop = 0;
      lastMeta = meta;
      shell.refresh();
    },
    notFound() {
      go(DEFAULT_ROUTE, { replace: true });
    },
  });
}

async function onLogout() {
  await session.logout();
  showLogin();
}

addEventListener(api.UNAUTHORIZED, () => {
  if (!shell) return;
  session.setUser(null);
  showLogin(SESSION_ENDED);
});

(async function boot() {
  try {
    const user = await session.me({ refresh: true });
    if (user) showApp();
    else showLogin();
  } catch (e) {
    root.replaceChildren(h("div", { class: "login" }, h("div", { class: "card err" }, icon("circle-alert"), h("span", {}, e.message))));
  }
})();
