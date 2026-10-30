import * as api from "/assets/js/api.js";
import { h, icon, fmtTime, fmtDateTime, row, clear, fill } from "/assets/js/dom.js";
import { confirm, dialog } from "/assets/js/components/dialog.js";
import { toast } from "/assets/js/toast.js";

const HEADS = ["Versi", "Artefak", "Runtime", "Oleh", "Waktu", ""];

export function renderDeployments(page) {
  const node = h("div");
  let list = [];

  const runtime = (d) => (d.java_version ? `Java ${d.java_version}` : page.app.kind === "static" ? "Binary statis" : "Binary glibc");
  const who = (d) => (d.created_by === page.me.id ? page.me.username : d.created_by === page.app.user_id ? page.state.owner : "admin");

  const label = (d) => {
    const active = d.id === page.app.current_deployment_id;
    if (active) return h("span", { class: "chip ok" }, `v${d.number} aktif`);
    if (d.status === "failed") return h("span", { class: "chip err" }, `v${d.number} gagal`);
    if (d.status === "pending" || d.status === "running") return h("span", { class: "chip warn" }, `v${d.number} berjalan`);
    return `v${d.number}`;
  };

  function showFailure(d) {
    dialog({ title: `Log v${d.number}`, submit: "Tutup", hideCancel: true, body: [h("pre", { class: "log", style: { "max-height": "50vh", margin: "0", "border-radius": "8px" } }, d.failure_log || "Tidak ada log yang tersimpan.")], onSubmit: async () => true });
  }

  function action(d) {
    const active = d.id === page.app.current_deployment_id;
    if (active) return h("a", { class: "btn sm ghost", href: page.path("logs") }, icon("scroll-text"), "Log");
    if (d.status === "failed") {
      return h("span", {}, d.failure_log ? h("button", { class: "btn sm ghost", type: "button", onclick: () => showFailure(d) }, icon("scroll-text"), "Log gagal") : null, d.report?.min_java && d.report.min_java > (d.java_version || 0) && d.java_version ? h("span", { class: "muted", style: { "font-size": "12px" } }, `Butuh Java ${d.report.min_java}`) : null);
    }
    if (d.status === "pending" || d.status === "running" || !page.mine) return null;
    return h("button", { class: "btn sm", type: "button", disabled: page.state.busy, onclick: () => rollback(d) }, icon("history"), "Kembalikan");
  }

  function rollback(d) {
    confirm({
      title: `Kembalikan ke v${d.number}?`,
      message: `Deployment baru dibuat dari ${d.file_name} dengan environment yang berlaku sekarang. Versi yang berjalan digantikan.`,
      submit: "Kembalikan",
      onSubmit: async () => {
        await api.post(`/api/apps/${page.id}/deployments/${d.id}/rollback`);
        toast(`Mengembalikan v${d.number}`);
        await page.reload();
        await load();
      },
    });
  }

  function render() {
    if (!list.length) {
      fill(node, h("div", { class: "empty" }, h("b", {}, "Belum ada deployment")));
      return;
    }
    const body = list.map((d) => row(HEADS, [label(d), h("td", { class: "mono" }, d.file_name), runtime(d), who(d), h("td", { class: "num muted", title: fmtDateTime(d.created_at) }, fmtTime(d.created_at)), h("td", { class: "num" }, action(d))]));
    fill(node, h("section", { class: "card" }, h("table", { class: "t" }, h("thead", {}, h("tr", {}, HEADS.map((t, i) => h("th", { class: i >= 4 ? "num" : "" }, t)))), h("tbody", {}, body))));
  }

  async function load() {
    list = await api.get(`/api/apps/${page.id}/deployments`);
    render();
  }
  load().catch((e) => toast(e.message, "err"));
  return { node, update: () => load().catch(() => {}) };
}
