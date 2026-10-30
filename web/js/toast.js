import { h, icon } from "/assets/js/dom.js";

let host = null;

/** toast(message, kind): a short message in the corner; kind is "ok" or "err". */
export function toast(message, kind = "ok") {
  if (!host) {
    host = h("div", { class: "toasts", role: "status", "aria-live": "polite" });
    document.body.append(host);
  }
  const el = h("div", { class: `toast ${kind === "err" ? "err" : ""}` }, icon(kind === "err" ? "circle-alert" : "circle-check"), h("span", {}, message));
  host.append(el);
  setTimeout(() => el.remove(), kind === "err" ? 7000 : 3500);
}
