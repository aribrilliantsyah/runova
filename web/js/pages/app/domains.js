import * as api from "/assets/js/api.js";
import { h, icon, row, clear, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { confirm } from "/assets/js/components/dialog.js";
import { guide } from "/assets/js/components/guide.js";
import { addressControl } from "/assets/js/components/address.js";
import * as session from "/assets/js/session.js";
import { toast } from "/assets/js/toast.js";

const HEADS = ["Domain", "DNS", "HTTPS", ""];

function statusCells(d, off) {
  if (off) return [h("span", { class: "chip warn" }, icon("circle-alert"), "Dimatikan admin"), h("span", { class: "muted" }, "Domain hosting ini dimatikan; alamat tidak dilayani")];
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

  let hosting = { domains: [], quota: { used: 0, limit: 0 } };
  let address = null;
  const addBtn = h("button", { class: "btn pri", id: "addaddr", type: "button" }, icon("plus"), "Tambah");
  const add = async () => {
    const body = address.value();
    const bad = address.error();
    if (!body || bad || address.checking()) {
      toast(bad || (address.checking() ? "Alamat sedang diperiksa" : "Isi alamat dulu"), "err");
      return;
    }
    await act(addBtn, async () => {
      const d = await api.post(`/api/apps/${page.id}/domains`, body);
      address.reset();
      pendingHost = d.host;
      await load();
    });
  };
  addBtn.addEventListener("click", add);

  const isAdmin = session.current().role === "admin";
  const addrBox = h("div", { class: "addrbox" });
  let addrQuota = "";

  function buildAddress() {
    const key = JSON.stringify([hosting.quota, hosting.domains.map((d) => d.id)]);
    if (address && key === addrQuota) return;
    addrQuota = key;
    address = addressControl({ data: hosting, isAdmin, publicIP: state.public_ip });
    fill(addrBox, address.node, h("div", { class: "rowflex", style: { "margin-top": "10px" } }, addBtn));
  }

  const offBase = (d) => d.hosting_domain_id != null && !hosting.domains.some((x) => x.id === d.hosting_domain_id && x.enabled !== false);

  function render() {
    buildAddress();
    const rows = state.domains.map((d) => {
      const off = offBase(d);
      const [dns, tls] = statusCells(d, off);
      const remove = h("button", { class: "btn sm ghost", type: "button", "aria-label": `Hapus ${d.host}`, onclick: () => confirm({ title: "Hapus alamat?", message: `${d.host} tidak lagi mengarah ke aplikasi ini.${d.label ? " Namanya bisa langsung diambil orang lain." : ""}`, submit: "Hapus", danger: true, onSubmit: async () => { await api.del(`/api/apps/${page.id}/domains/${d.id}`); toast("Alamat dihapus"); await load(); } }) }, icon("trash-2"));
      const recheck = h("button", { class: "btn sm", type: "button", onclick: (e) => act(e.currentTarget, async () => { await api.post(`/api/apps/${page.id}/domains/${d.id}/check`); await load(); }) }, icon("rotate-cw"), "Cek ulang");
      const kind = d.hosting_domain_id != null ? h("span", { class: "chip info" }, "Domain hosting") : null;
      return row(HEADS, [h("td", { class: "mono" }, d.host, " ", kind), h("td", {}, dns), h("td", {}, tls), h("td", { class: "num nowrap" }, d.status === "active" || off ? null : recheck, " ", remove)]);
    });
    const table = state.domains.length
      ? h("section", { class: "card" }, h("table", { class: "t" }, h("thead", {}, h("tr", {}, HEADS.map((t, i) => h("th", { class: i === 3 ? "num" : "" }, t)))), h("tbody", {}, rows)))
      : h("div", { class: "card" }, h("div", { class: "empty" }, h("span", { class: "ic" }, icon("globe")), h("b", {}, "Belum ada alamat"), h("span", {}, "Tambah alamat di bawah. Sementara itu aplikasi tetap bisa diuji lewat akses IP.")));

    const free = state.domains.filter((d) => d.hosting_domain_id == null);
    const target = free.find((d) => d.host === pendingHost) || free.find((d) => d.status !== "active") || free[0];
    fill(node, table, h("section", { class: "card", style: { "margin-top": "12px" } }, h("div", { class: "bd" }, h("b", {}, "Tambah alamat"), h("div", { style: { "margin-top": "8px", "max-width": "620px" } }, addrBox))), target ? guideFor(target) : null);
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

  async function load() {
    [state, hosting] = await Promise.all([api.get(`/api/apps/${page.id}/domains`), api.get("/api/hosting-domains")]);
    page.setDomains?.(state.domains);
    render();
  }
  load().catch((e) => toast(e.message, "err"));
  const timer = setInterval(() => {
    if (document.visibilityState === "visible" && state.domains.some((d) => d.status !== "active") && !document.activeElement?.matches?.("input,select")) load().catch(() => {});
  }, 5000);
  return { node, cleanup: () => clearInterval(timer) };
}

