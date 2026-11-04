import * as api from "/assets/js/api.js";
import { h, icon, fmtTime, fmtDateTime, row, fill } from "/assets/js/dom.js";
import { confirm } from "/assets/js/components/dialog.js";
import { logView, fromServer } from "/assets/js/components/logview.js";
import { timeline, problemCard, deployChip, currentStep, FOLLOW_KEY } from "/assets/js/components/deploytrace.js";
import { toast } from "/assets/js/toast.js";

const HEADS = ["Versi", "Status", "Artefak", "Runtime", "Oleh", "Waktu", ""];
const LIVE_MS = 1500;

/** The Deployment tab: the history, and one deployment opened (#/apps/1/deployments/7) with its steps and log. */
export function renderDeployments(page) {
  const node = h("div");
  let list = [];
  let openId = page.sub ? Number(page.sub) : null;
  let detail = null; // {id, view, timer}
  let disposed = false; // a load that finishes after the tab closed must not paint or navigate

  const runtime = (d) => (d.java_version ? `Java ${d.java_version}` : page.app.kind === "static" ? "Binary statis" : "Binary glibc");
  const who = (d) => (d.created_by === page.me.id ? page.me.username : d.created_by === page.app.user_id ? page.state.owner : "admin");

  function action(d) {
    const active = d.id === page.app.current_deployment_id;
    const open = h("a", { class: "btn sm", href: page.path(`deployments/${d.id}`) }, icon("scroll-text"), d.status === "failed" ? "Lihat penyebab" : "Detail");
    if (active || d.status === "failed" || d.status === "pending" || d.status === "running" || !page.mine) return open;
    return h("span", { class: "rowacts" }, open, h("button", { class: "btn sm ghost", type: "button", disabled: page.state.busy, onclick: () => rollback(d) }, icon("history"), "Kembalikan"));
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

  function renderList() {
    closeDetail();
    if (!list.length) {
      fill(node, h("div", { class: "empty" }, h("b", {}, "Belum ada deployment"), h("span", {}, "Unggah versi baru dari tab Umum untuk membuat deployment pertama.")));
      return;
    }
    const body = list.map((d) => {
      const why = d.status === "failed" && d.problem?.title ? h("div", { class: "dep-why", title: d.problem.title }, d.problem.title) : (d.status === "running" || d.status === "pending") ? h("div", { class: "dep-why muted" }, currentStep(d)) : null;
      return row(HEADS, [
        h("td", { class: "nowrap" }, h("b", {}, `v${d.number}`)),
        h("td", { class: "dep-st" }, deployChip(d, page.app.current_deployment_id), why),
        h("td", { class: "mono dep-file" }, d.file_name),
        runtime(d),
        who(d),
        h("td", { class: "num muted nowrap", title: fmtDateTime(d.created_at) }, fmtTime(d.created_at)),
        h("td", { class: "num" }, action(d)),
      ], { "data-dep": d.id });
    });
    fill(node, h("section", { class: "card" }, h("table", { class: "t deps" }, h("thead", {}, h("tr", {}, HEADS.map((t, i) => h("th", { class: i >= 5 ? "num" : "" }, t)))), h("tbody", {}, body))));
  }

  function closeDetail() {
    if (!detail) return;
    clearTimeout(detail.timer);
    detail.view.cleanup();
    detail = null;
  }

  // The deploy page hands over the deployment it started: its progress shows here, and a
  // success moves on to Umum.
  function followed(d) {
    let f = null;
    try {
      f = JSON.parse(sessionStorage.getItem(FOLLOW_KEY) || "null");
    } catch {
      return;
    }
    if (!f || f.id !== d.id) return;
    if (f.addrError && !f.told) {
      toast(`Aplikasi dibuat, tetapi alamat ditolak: ${f.addrError}`, "err");
      sessionStorage.setItem(FOLLOW_KEY, JSON.stringify({ ...f, told: true }));
    }
    if (d.status === "active" || d.status === "failed" || d.status === "superseded") sessionStorage.removeItem(FOLLOW_KEY);
    if (d.status === "active") {
      toast(`${page.app.name} v${d.number} berjalan`);
      location.hash = page.path("");
    }
  }

  async function renderDetail(id) {
    const d = list.find((x) => x.id === id);
    if (d) followed(d);
    if (!d) {
      fill(node, h("div", { class: "empty" }, h("b", {}, "Deployment tidak ditemukan"), h("a", { class: "btn", href: page.path("deployments") }, "Kembali ke riwayat")));
      return;
    }
    const fresh = !detail || detail.id !== id;
    if (fresh) {
      closeDetail();
      detail = { id, view: logView({ label: `Log v${d.number}`, fileName: `${page.app.name}-v${d.number}.log` }), timer: 0, loaded: "" };
    }
    const live = d.status === "running" || d.status === "pending";
    const active = d.id === page.app.current_deployment_id;
    const tone = d.problem?.kind === "deploy_kept_old" || d.problem?.kind === "stopped_user" || d.problem?.kind === "exited_clean" ? "warn" : "err";
    const card = d.status === "failed" || (active && d.problem && page.app.status !== "running") ? problemCard(d.problem, { failureLog: d.failure_log, tone }) : null;
    const back = h("a", { class: "btn sm ghost", href: page.path("deployments") }, icon("arrow-left"), "Riwayat");
    const meta = h("dl", { class: "kv dep-meta" },
      h("dt", {}, "Artefak"), h("dd", { class: "mono" }, d.file_name),
      h("dt", {}, "Runtime"), h("dd", {}, runtime(d)),
      h("dt", {}, "Port aplikasi"), h("dd", { class: "mono" }, String(d.internal_port)),
      h("dt", {}, "Dibuat"), h("dd", {}, `${who(d)}, ${fmtDateTime(d.created_at)}`),
      d.finished_at ? [h("dt", {}, "Selesai"), h("dd", {}, fmtDateTime(d.finished_at))] : null,
    );
    fill(
      node,
      h("div", { class: "dep-head" }, back, h("h2", {}, `Deployment v${d.number}`), deployChip(d, page.app.current_deployment_id)),
      card,
      h(
        "div",
        { class: "dep-grid" },
        h("section", { class: "card" }, h("header", {}, h("h3", {}, "Langkah deploy"), live ? h("span", { class: "chip warn" }, "berjalan") : null), h("div", { class: "bd" }, timeline(d))),
        h("section", { class: "card" }, h("header", {}, h("h3", {}, "Ringkasan")), h("div", { class: "bd" }, meta)),
      ),
      h("section", { class: "card lvcard" }, h("header", {}, h("h3", {}, "Log container")), detail.view.node),
    );
    const key = `${d.status}/${d.finished_at}/${live ? Date.now() : ""}`;
    if (detail.loaded !== key) {
      detail.loaded = key;
      try {
        const r = await api.get(`/api/apps/${page.id}/deployments/${d.id}/log`);
        if (disposed || !detail || detail.id !== id) return;
        detail.view.set(r.lines.map(fromServer));
        detail.view.setEmpty(d.status === "failed" && !d.container_id ? "Deploy ini gagal sebelum container berjalan, jadi tidak ada log container. Penyebabnya ada di atas." : live ? "Menunggu container menulis log…" : "Container ini tidak menulis log apa pun.");
        if (fresh && d.status === "failed") requestAnimationFrame(() => detail?.view.focusError());
      } catch (e) {
        detail.view.setEmpty(`Log tidak bisa dimuat: ${e.message}`);
      }
    }
    if (live) {
      clearTimeout(detail.timer);
      detail.timer = setTimeout(() => load().catch(() => {}), LIVE_MS);
    }
  }

  function render() {
    if (openId) renderDetail(openId);
    else renderList();
  }

  async function load() {
    const got = await api.get(`/api/apps/${page.id}/deployments`);
    if (disposed) return;
    list = got;
    render();
  }
  load().catch((e) => toast(e.message, "err"));
  return {
    node,
    update: () => load().catch(() => {}),
    sub(id) {
      const next = id ? Number(id) : null;
      if (next === openId) return;
      openId = next;
      render();
    },
    cleanup() {
      disposed = true;
      closeDetail();
    },
  };
}
