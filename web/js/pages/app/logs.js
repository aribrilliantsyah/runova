import * as api from "/assets/js/api.js";
import { h, icon, fill } from "/assets/js/dom.js";
import { logStream } from "/assets/js/components/logstream.js";
import { logView, fromServer } from "/assets/js/components/logview.js";
import { problemCard } from "/assets/js/components/deploytrace.js";

/**
 * The Log tab. A running app streams its container live. Otherwise the newest log that
 * exists is shown: the active container's output (Docker keeps it while the container is
 * stopped), or a failed deployment's saved output, with the reason it stopped on top.
 * A picker shows the log of any deployment Runova still has.
 */
export function renderLogs(page) {
  let deployments = [];
  let chosen = null; // deployment ID; null = automatic
  let stream = null;
  let streamFor = null;
  let loadedFor = null;
  let paused = false;
  const pending = [];

  const live = h("span", { class: "chip" });
  const pauseBtn = h("button", { class: "btn sm ghost", type: "button" });
  const picker = h("select", { class: "f lv-pick", "aria-label": "Log deployment" });
  const view = logView({ label: "Log aplikasi", tall: true, fileName: `${page.app.name}.log`, toolbarExtra: [live, pauseBtn] });
  view.node.querySelector(".lv-body").id = "biglog";
  const head = h("div", { class: "logtop" });
  const reason = h("div");

  const setLive = (cls, text) => {
    live.className = `chip ${cls}`;
    fill(live, cls ? h("span", { class: `dot ${cls}` }) : null, text);
  };
  const paintPause = () => pauseBtn.replaceChildren(icon(paused ? "play" : "square"), paused ? `Lanjut${pending.length ? ` (+${pending.length})` : ""}` : "Jeda");
  pauseBtn.addEventListener("click", () => {
    paused = !paused;
    if (!paused) pending.splice(0).forEach((l) => view.add(l));
    paintPause();
    setLive(paused ? "warn" : "ok", paused ? "Dijeda" : "Live");
  });

  function stopStream() {
    stream?.close();
    stream = streamFor = null;
  }

  const autoTarget = () => {
    const { app, deployment, latest } = page.state;
    if (latest && latest.id !== deployment?.id && (latest.status === "failed" || latest.status === "running" || latest.status === "pending")) {
      // A deploy in progress or one that failed is what the user came for, unless the
      // app is running fine on an older version and the failure is old news.
      if (!(app.status === "running" && latest.status === "failed")) return latest;
    }
    return deployment || latest || null;
  };

  function target() {
    if (chosen != null) return deployments.find((d) => d.id === chosen) || autoTarget();
    return autoTarget();
  }

  async function sync() {
    const { app, deployment } = page.state;
    const d = target();
    paintPicker(d);
    paintReason(d);
    const streaming = d && deployment && d.id === deployment.id && app.status === "running";
    const deploying = d && (d.status === "running" || d.status === "pending");
    pauseBtn.hidden = !(streaming || deploying);
    if (streaming || deploying) {
      if (streamFor === d.id) return;
      stopStream();
      loadedFor = null;
      view.clear();
      view.setEmpty(deploying ? "Menunggu container dibuat…" : "Container belum menulis log.");
      streamFor = d.id;
      paused = false;
      paintPause();
      stream = logStream({
        id: page.id,
        tail: 1000,
        onLine: (l) => (paused ? pending.push(l) : view.add(l)),
        onState: (s) => (paused ? null : setLive(s === "live" ? "ok" : s === "connecting" ? "" : "err", s === "live" ? "Live" : s === "connecting" ? "Menyambung…" : "Terputus")),
      });
      return;
    }
    stopStream();
    if (!d) {
      setLive("", "Belum ada deployment");
      view.clear();
      view.setEmpty("Belum ada deployment. Log muncul begitu aplikasi di-deploy.");
      return;
    }
    if (loadedFor === `${d.id}/${d.status}/${d.finished_at}`) return;
    loadedFor = `${d.id}/${d.status}/${d.finished_at}`;
    try {
      const r = await api.get(`/api/apps/${page.id}/deployments/${d.id}/log`);
      view.set(r.lines.map(fromServer));
      setLive("", r.source === "container" ? "Log terakhir container" : r.source === "saved" ? "Log tersimpan" : "Tidak ada log");
      view.setEmpty(
        d.status === "failed" && !d.container_id
          ? "Deploy ini gagal sebelum container berjalan, jadi container tidak menulis log. Penyebabnya ada di atas."
          : "Container ini tidak menulis log apa pun.",
      );
      if (d.status === "failed") requestAnimationFrame(() => view.focusError());
    } catch (e) {
      loadedFor = null;
      view.clear();
      view.setEmpty(`Log tidak bisa dimuat: ${e.message}`);
    }
  }

  function paintReason(d) {
    const { app, deployment } = page.state;
    let card = null;
    if (d?.status === "failed") card = problemCard(d.problem, { failureLog: d.failure_log, tone: d.problem?.kind === "deploy_kept_old" ? "warn" : "err" });
    else if (d && deployment && d.id === deployment.id && app.status !== "running" && d.problem) card = problemCard(d.problem, { tone: app.status === "failed" ? "err" : "warn" });
    const link = d ? h("a", { class: "btn sm", href: page.path(`deployments/${d.id}`) }, icon("history"), "Langkah deploy") : null;
    fill(reason, card ? h("div", { class: "logreason" }, card, link) : null);
  }

  function paintPicker(d) {
    const opts = [h("option", { value: "" }, "Otomatis")];
    for (const x of deployments) {
      const tag = x.id === page.state.deployment?.id ? "aktif" : x.status === "failed" ? "gagal" : x.status === "superseded" ? "lama" : "berjalan";
      opts.push(h("option", { value: String(x.id) }, `v${x.number} · ${tag}`));
    }
    picker.replaceChildren(...opts);
    picker.value = chosen == null ? "" : String(chosen);
    picker.hidden = deployments.length < 1;
    fill(head, h("div", { class: "logtitle" }, h("b", {}, d ? `Log v${d.number}` : "Log"), d ? h("span", { class: "muted" }, d.file_name) : null), picker);
  }

  picker.addEventListener("change", () => {
    chosen = picker.value ? Number(picker.value) : null;
    loadedFor = null;
    stopStream();
    sync();
  });

  async function loadList() {
    try {
      deployments = await api.get(`/api/apps/${page.id}/deployments`);
    } catch {
      deployments = [];
    }
  }

  setLive("", "Memuat…");
  paintPause();
  loadList().then(sync);
  const node = h("div", { class: "logtab" }, reason, h("section", { class: "card lvcard" }, h("header", {}, head), view.node));
  return {
    node,
    update: () => loadList().then(sync),
    cleanup: () => {
      stopStream();
      view.cleanup();
    },
  };
}
