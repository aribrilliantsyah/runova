import * as api from "/assets/js/api.js";
import { h, icon } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";

// "Needs redeploy" is remembered per app and per active deployment: it goes away once a newer
// deployment is active, and survives a reload of the page.
const key = (id) => `runova.redeploy.${id}`;

export function markRedeploy(page) {
  try {
    sessionStorage.setItem(key(page.id), String(page.state.deployment?.id ?? 0));
  } catch {
    // storage blocked: the banner shows until the next reload
  }
}

export function needsRedeploy(page) {
  try {
    const at = sessionStorage.getItem(key(page.id));
    if (at === null) return false;
    if (Number(at) !== (page.state.deployment?.id ?? 0)) {
      sessionStorage.removeItem(key(page.id));
      return false;
    }
    return true;
  } catch {
    return false;
  }
}

/** redeployBanner(page) → banner Node, or null when nothing is pending. */
export function redeployBanner(page) {
  if (!needsRedeploy(page) || !page.state.deployment) return null;
  const btn = h("button", { class: "btn sm pri", type: "button" }, icon("rocket"), "Deploy ulang");
  btn.addEventListener("click", () =>
    act(btn, async () => {
      await api.post(`/api/apps/${page.id}/deploy`);
      sessionStorage.removeItem(key(page.id));
      await page.reload();
    }, "Deploy ulang dimulai"),
  );
  return h("div", { class: "banner warn", role: "status" }, icon("triangle-alert"), h("span", {}, "Deploy ulang untuk menerapkan perubahan."), h("div", { class: "r" }, btn));
}

