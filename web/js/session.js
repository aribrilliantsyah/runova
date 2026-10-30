import * as api from "/assets/js/api.js";
import { h, icon, initials } from "/assets/js/dom.js";
import { dialog } from "/assets/js/components/dialog.js";
import { toast } from "/assets/js/toast.js";

const THEME_KEY = "runova.theme";
let user = null;

/** me(): the signed-in user, cached; null when nobody is signed in. */
export async function me({ refresh = false } = {}) {
  if (user && !refresh) return user;
  try {
    user = await api.get("/api/me");
  } catch (e) {
    if (e.status !== 401) throw e;
    user = null;
  }
  return user;
}

export const current = () => user;
export const isAdmin = () => user?.role === "admin";

export function setUser(u) {
  user = u;
}

/** requireLogin(): the user, or null after the caller should show the login screen. */
export async function requireLogin() {
  return me({ refresh: true });
}

export async function logout() {
  try {
    await api.post("/api/logout");
  } catch (e) {
    if (e.status !== 401) throw e;
  }
  user = null;
}

export function displayName(u = user) {
  return u?.display_name || u?.username || "";
}

/* Theme: system by default, forced from the account menu, remembered in localStorage. */
export const isDark = () => document.documentElement.dataset.theme === "dark";

export function setTheme(dark) {
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  try {
    localStorage.setItem(THEME_KEY, dark ? "dark" : "light");
  } catch {
    // storage blocked: the choice lasts until reload
  }
  dispatchEvent(new Event("runova:theme"));
}

export const toggleTheme = () => setTheme(!isDark());

export function changePasswordDialog() {
  const cur = h("input", { class: "f", type: "password", autocomplete: "current-password", "aria-label": "Password saat ini" });
  const next = h("input", { class: "f", type: "password", autocomplete: "new-password", "aria-label": "Password baru" });
  const again = h("input", { class: "f", type: "password", autocomplete: "new-password", "aria-label": "Ulangi password baru" });
  return dialog({
    title: "Ganti password",
    submit: "Ganti password",
    body: [
      h("div", { class: "fld" }, h("label", {}, "Password saat ini"), cur),
      h("div", { class: "fld" }, h("label", {}, "Password baru"), next, h("div", { class: "hint" }, "10–200 karakter. Sesi di perangkat lain akan berakhir.")),
      h("div", { class: "fld" }, h("label", {}, "Ulangi password baru"), again),
    ],
    onSubmit: async () => {
      if (next.value !== again.value) throw new Error("Password baru tidak sama");
      await api.post("/api/me/password", { current: cur.value, new: next.value });
      toast("Password diganti");
    },
  });
}

/**
 * accountMenu(anchor, {onLogout}) opens the account popup under/above the anchor:
 * change password, dark mode, sign out.
 */
export function accountMenu(anchor, { onLogout }) {
  const sw = h("span", { class: "sw", role: "switch", "aria-checked": String(isDark()), "aria-label": "Mode gelap", style: {} });
  sw.style.setProperty("pointer-events", "none");
  const item = (cls, ic, label, fn, extra) =>
    h("button", { class: `item ${cls}`, type: "button", onclick: () => { close(); fn(); } }, icon(ic), label, extra);
  const menu = h(
    "div",
    { class: "pop", role: "menu", "aria-label": "Menu akun" },
    item("", "key-round", "Ganti password", changePasswordDialog),
    h("button", { class: "item", type: "button", role: "menuitemcheckbox", "aria-checked": String(isDark()), onclick: () => { toggleTheme(); sw.setAttribute("aria-checked", String(isDark())); } }, icon(isDark() ? "sun" : "moon"), "Mode gelap", h("span", { class: "spacer" }), sw),
    h("div", { class: "sep" }),
    item("danger", "log-out", "Keluar", onLogout),
  );
  const onDoc = (e) => {
    if (!menu.contains(e.target) && !anchor.contains(e.target)) close();
  };
  const onKey = (e) => {
    if (e.key === "Escape") {
      close();
      anchor.focus();
    }
  };
  function close() {
    menu.remove();
    document.removeEventListener("mousedown", onDoc, true);
    document.removeEventListener("keydown", onKey, true);
  }
  document.body.append(menu);
  const r = anchor.getBoundingClientRect();
  const width = Math.min(menu.offsetWidth, innerWidth - 16);
  menu.style.setProperty("left", `${Math.max(8, Math.min(r.left, innerWidth - width - 8))}px`);
  const below = r.bottom + menu.offsetHeight + 8 < innerHeight;
  if (below) menu.style.setProperty("top", `${r.bottom + 4}px`);
  else menu.style.setProperty("bottom", `${innerHeight - r.top + 4}px`);
  document.addEventListener("mousedown", onDoc, true);
  document.addEventListener("keydown", onKey, true);
  menu.querySelector("button").focus();
  return close;
}

export function avatar(u = user, cls = "") {
  return h("span", { class: `av ${cls}`.trim(), "aria-hidden": "true" }, initials(u));
}
