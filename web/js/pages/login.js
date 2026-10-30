import * as api from "/assets/js/api.js";
import { h, icon } from "/assets/js/dom.js";
import { logo } from "/assets/js/logo.js";

/**
 * renderLogin(root, {message, onLogin}) shows the sign-in card. The server's own
 * message is shown as it is, including the 429 lockout text.
 */
export function renderLogin(root, { message = "", onLogin }) {
  const user = h("input", { class: "f", id: "login-user", name: "username", autocomplete: "username", autocapitalize: "none", spellcheck: "false", required: true });
  const pass = h("input", { class: "f", id: "login-pass", name: "password", type: "password", autocomplete: "current-password", required: true });
  const err = h("div", { class: "err", role: "alert", hidden: !message }, icon("circle-alert"), h("span", {}, message));
  const btn = h("button", { class: "btn dark", type: "submit" }, "Masuk");
  const form = h(
    "form",
    { class: "card", novalidate: true },
    h("div", { class: "brand" }, logo("logo"), h("h1", {}, "Masuk ke Runova"), h("p", { class: "lead muted" }, "Panel untuk menjalankan hasil build")),
    h("div", { class: "err-slot" }, err),
    h("div", { class: "fld" }, h("label", { for: "login-user" }, "Username"), user),
    h("div", { class: "fld" }, h("label", { for: "login-pass" }, "Password"), pass),
    btn,
  );
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    btn.disabled = true;
    btn.setAttribute("aria-busy", "true");
    err.hidden = true;
    try {
      const u = await api.post("/api/login", { username: user.value.trim(), password: pass.value });
      await onLogin(u);
    } catch (ex) {
      err.lastChild.textContent = ex.message;
      err.hidden = false;
      pass.value = "";
      pass.focus();
    } finally {
      btn.disabled = false;
      btn.removeAttribute("aria-busy");
    }
  });
  root.replaceChildren(h("main", { class: "login" }, form));
  user.focus();
}
