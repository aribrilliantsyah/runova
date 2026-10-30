import * as api from "/assets/js/api.js";
import { h, icon, fmtTime, poll } from "/assets/js/dom.js";

const POLL_MS = 30000;

/**
 * bell() → {button, stop}. The button shows the unread count, polls every 30 s and
 * opens the list; opening it does not mark anything read, "Tandai sudah dibaca" does.
 */
export function bell() {
  const badge = h("span", { class: "badge", hidden: true });
  const button = h("button", { class: "ibtn", type: "button", "aria-label": "Notifikasi", "aria-haspopup": "true" }, icon("bell"), badge);
  let data = { notifications: [], unread: 0 };
  let pop = null;

  const render = () => {
    badge.hidden = data.unread === 0;
    badge.textContent = data.unread > 99 ? "99+" : String(data.unread);
    button.setAttribute("aria-label", data.unread ? `Notifikasi, ${data.unread} belum dibaca` : "Notifikasi");
    if (pop) fill();
  };

  const load = async () => {
    data = await api.get("/api/notifications");
    render();
  };

  const levelDot = (l) => (l === "error" ? "err" : l === "warning" ? "warn" : "ok");

  function fill() {
    const list = data.notifications.length
      ? data.notifications.map((n) => {
          const body = [h("span", { class: `dot ${levelDot(n.level)}` }), h("b", {}, n.title), n.body ? h("small", {}, n.body) : null, h("small", {}, fmtTime(n.at))];
          return n.app_id
            ? h("a", { class: `notif ${n.read ? "read" : ""}`, href: `#/apps/${n.app_id}`, onclick: close }, body)
            : h("div", { class: `notif ${n.read ? "read" : ""}` }, body);
        })
      : [h("div", { class: "none" }, "Belum ada notifikasi")];
    const mark = h("button", { class: "btn sm ghost", type: "button", disabled: data.unread === 0, onclick: async () => {
      await api.post("/api/notifications/read");
      await load();
    } }, "Tandai sudah dibaca");
    pop.replaceChildren(h("h4", {}, "Notifikasi", mark), ...list);
  }

  function onDoc(e) {
    if (pop && !pop.contains(e.target) && !button.contains(e.target)) close();
  }
  function onKey(e) {
    if (e.key === "Escape") {
      close();
      button.focus();
    }
  }
  function close() {
    pop?.remove();
    pop = null;
    button.setAttribute("aria-expanded", "false");
    document.removeEventListener("mousedown", onDoc, true);
    document.removeEventListener("keydown", onKey, true);
  }

  button.addEventListener("click", async () => {
    if (pop) return close();
    pop = h("div", { class: "pop", role: "dialog", "aria-label": "Notifikasi" });
    document.body.append(pop);
    const r = button.getBoundingClientRect();
    const width = Math.min(360, innerWidth - 16);
    pop.style.setProperty("width", `${width}px`);
    pop.style.setProperty("left", `${Math.max(8, Math.min(r.right - width, innerWidth - width - 8))}px`);
    pop.style.setProperty("top", `${r.bottom + 4}px`);
    button.setAttribute("aria-expanded", "true");
    document.addEventListener("mousedown", onDoc, true);
    document.addEventListener("keydown", onKey, true);
    fill();
    try {
      await load();
    } catch {
      // the list stays as it was
    }
  });

  load().catch(() => {});
  const stopPoll = poll(load, POLL_MS);
  return { button, stop: () => { stopPoll(); close(); }, refresh: load };
}
