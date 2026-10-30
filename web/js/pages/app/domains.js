import * as api from "/assets/js/api.js";
import { h, icon, row, clear, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { confirm } from "/assets/js/components/dialog.js";
import { guide } from "/assets/js/components/guide.js";
import { hostError } from "/assets/js/validate.js";
import { toast } from "/assets/js/toast.js";

const HEADS = ["Domain", "DNS", "HTTPS", ""];

function statusCells(d) {
  switch (d.status) {
    case "active":
      return [h("span", { class: "chip ok" }, icon("check"), "Mengarah ke server"), h("span", { class: "chip ok" }, icon("lock"), "HTTPS aktif")];
    case "error":
      return [h("span", { class: "chip err" }, icon("circle-alert"), "Bermasalah"), h("span", { class: "muted", title: d.detail }, d.detail || "Sertifikat belum terbit")];
    default:
      return [h("span", { class: "chip warn" }, icon("loader-circle"), "Belum mengarah"), h("span", { class: "muted" }, d.detail || "Menunggu DNS")];
  }
}

export function renderDomains(page) {
  const node = h("div");
  let state = { domains: [], public_ip: "", guide: ["tunnel"] };
  let pendingHost = "";

  const input = h("input", { class: "f mono", placeholder: "api.perusahaan.co.id", "aria-label": "Domain baru", autocomplete: "off", spellcheck: "false" });
  const err = h("div", { class: "errtext", hidden: true });
  const addBtn = h("button", { class: "btn pri", type: "button" }, icon("plus"), "Tambah");
  const add = async () => {
    const host = input.value.trim().toLowerCase();
    const bad = hostError(host);
    err.textContent = bad;
    err.hidden = !bad;
    input.setAttribute("aria-invalid", String(Boolean(bad)));
    if (bad) return;
    const ok = await act(addBtn, async () => {
      await api.post(`/api/apps/${page.id}/domains`, { host });
      input.value = "";
      pendingHost = host;
      await load();
    });
    if (!ok) return;
  };
  addBtn.addEventListener("click", add);
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") add();
  });
  input.addEventListener("input", () => {
    err.hidden = true;
  });

  function render() {
    const rows = state.domains.map((d) => {
      const [dns, tls] = statusCells(d);
      const remove = h("button", { class: "btn sm ghost", type: "button", "aria-label": `Hapus ${d.host}`, onclick: () => confirm({ title: "Hapus domain?", message: `${d.host} tidak lagi mengarah ke aplikasi ini.`, submit: "Hapus", danger: true, onSubmit: async () => { await api.del(`/api/apps/${page.id}/domains/${d.id}`); toast("Domain dihapus"); await load(); } }) }, icon("trash-2"));
      const recheck = h("button", { class: "btn sm", type: "button", onclick: (e) => act(e.currentTarget, async () => { await api.post(`/api/apps/${page.id}/domains/${d.id}/check`); await load(); }) }, icon("rotate-cw"), "Cek ulang");
      return row(HEADS, [h("td", { class: "mono" }, d.host), h("td", {}, dns), h("td", {}, tls), h("td", { class: "num nowrap" }, d.status === "active" ? null : recheck, " ", remove)]);
    });
    const table = state.domains.length
      ? h("section", { class: "card" }, h("table", { class: "t" }, h("thead", {}, h("tr", {}, HEADS.map((t, i) => h("th", { class: i === 3 ? "num" : "" }, t)))), h("tbody", {}, rows)))
      : h("div", { class: "card" }, h("div", { class: "empty" }, h("span", { class: "ic" }, icon("globe")), h("b", {}, "Belum ada domain"), h("span", {}, "Tambah domain di bawah. Sementara itu aplikasi tetap bisa diuji lewat akses IP.")));

    const target = state.domains.find((d) => d.host === pendingHost) || state.domains.find((d) => d.status !== "active") || state.domains[0];
    const ex = h("div", { class: "rowflex", style: { "margin-top": "12px", "max-width": "520px" } }, input, addBtn);
    fill(node, table, ex, err, target ? guideFor(target) : sampleGuide());
  }

  function guideFor(d) {
    return guide({
      host: d.host,
      publicIP: state.public_ip,
      methods: state.guide,
      status: { kind: d.status === "active" ? "ok" : d.status === "error" ? "err" : "warn", text: d.status === "active" ? `${d.host} sudah mengarah ke server` : d.detail || `${d.host} belum mengarah ke server` },
      onCheck: async () => {
        try {
          await api.post(`/api/apps/${page.id}/domains/${d.id}/check`);
        } catch (e) {
          toast(e.message, "err");
        }
        await load();
      },
    });
  }
  const sampleGuide = () => guide({ host: `${page.app.name}.perusahaan.co.id`, publicIP: state.public_ip, methods: state.guide });

  async function load() {
    state = await api.get(`/api/apps/${page.id}/domains`);
    page.setDomains?.(state.domains);
    render();
  }
  load().catch((e) => toast(e.message, "err"));
  const timer = setInterval(() => {
    if (document.visibilityState === "visible" && state.domains.some((d) => d.status !== "active") && !document.activeElement?.matches?.("input")) load().catch(() => {});
  }, 5000);
  return { node, cleanup: () => clearInterval(timer) };
}

