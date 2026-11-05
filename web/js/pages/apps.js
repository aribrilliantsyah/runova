import * as api from "/assets/js/api.js";
import { h, icon, poll, debounce } from "/assets/js/dom.js";
import { appCard } from "/assets/js/components/appcard.js";
import { quotaStats } from "/assets/js/components/quota.js";
import * as session from "/assets/js/session.js";

const POLL_MS = 5000;
const FILTERS = [
  ["all", "Semua", () => true],
  ["running", "Berjalan", (a) => a.status === "running" || a.status === "starting" || a.status === "deploying"],
  ["problem", "Bermasalah", (a) => a.status === "failed"],
  ["stopped", "Berhenti", (a) => a.status === "stopped"],
];

export async function renderApps(root) {
  const u = session.current();
  let data = null;
  let info = { public_ip: "" };
  let filter = "all";
  let text = "";
  let showAll = false;

  const heading = h("div", {}, h("h1", {}, "Aplikasi"), h("p", {}, `Aplikasi milik ${u.username}. Pengguna lain tidak bisa melihat atau mengubahnya.`));
  const deployBtn = h("a", { class: "btn pri", href: "#/deploy" }, icon("plus"), "Deploy aplikasi");
  const body = h("div", { class: "pb" }, h("div", { class: "loading" }, "Memuat…"));
  root.append(h("section", {}, h("div", { class: "ph" }, heading, h("div", { class: "acts" }, deployBtn)), body));

  async function load() {
    const [apps, inf] = await Promise.all([api.get(`/api/apps${showAll ? "?all=1" : ""}`), api.get("/api/info")]);
    data = apps;
    info = inf;
    render();
  }

  function render() {
    const hadFocus = document.activeElement?.matches?.("input[type=search]") && body.contains(document.activeElement);
    paint();
    if (hadFocus) {
      const el = body.querySelector("input[type=search]");
      el?.focus();
      el?.setSelectionRange(el.value.length, el.value.length);
    }
  }

  function paint() {
    const apps = data.apps;
    if (!apps.length && !showAll) {
      body.replaceChildren(
        h(
          "div",
          { class: "empty" },
          h("span", { class: "ic" }, icon("cloud-upload")),
          h("b", {}, "Belum ada aplikasi"),
          h("span", {}, "Unggah .jar atau binary Linux hasil build, lalu Runova menjalankannya."),
          h("a", { class: "btn pri", href: "#/deploy" }, icon("rocket"), "Deploy aplikasi pertama"),
        ),
      );
      return;
    }
    const counts = Object.fromEntries(FILTERS.map(([k, , fn]) => [k, apps.filter(fn).length]));
    const fn = FILTERS.find(([k]) => k === filter)[2];
    const shown = apps.filter((a) => fn(a) && a.name.includes(text));

    const input = h("input", { class: "f", type: "search", placeholder: "Filter aplikasi", "aria-label": "Filter aplikasi", value: text });
    input.addEventListener(
      "input",
      debounce(() => {
        text = input.value.trim().toLowerCase();
        render();
      }, 120),
    );
    const seg = h(
      "div",
      { class: "seg", role: "group", "aria-label": "Status" },
      FILTERS.filter(([k]) => k !== "stopped" || counts.stopped > 0).map(([k, label]) =>
        h("button", { type: "button", "aria-pressed": String(filter === k), onclick: () => { filter = k; render(); } }, label, " ", h("span", { class: "faint" }, String(counts[k]))),
      ),
    );
    const adminToggle = u.role === "admin"
      ? h("button", { class: "btn sm", type: "button", "aria-pressed": String(showAll), onclick: async () => { showAll = !showAll; await load(); } }, icon("users"), showAll ? "Semua pengguna" : "Hanya milikku")
      : null;

    const grid = shown.length
      ? h("div", { class: "apps", id: "apps" }, shown.map((a) => appCard(a, data.summary[a.id], info)))
      : h("div", { class: "empty" }, h("b", {}, "Tidak ada aplikasi yang cocok"), h("span", {}, "Ubah filter atau kata kunci."));

    body.replaceChildren(
      quotaStats(data.usage),
      h("div", { class: "toolbar" }, h("div", { class: "filter" }, icon("search"), input), seg, h("div", { class: "r" }, adminToggle)),
      grid,
    );
  }

  await load().catch((e) => body.replaceChildren(h("div", { class: "err", role: "alert" }, icon("circle-alert"), h("span", {}, e.message))));
  const stop = poll(() => load(), POLL_MS);
  return stop;
}
