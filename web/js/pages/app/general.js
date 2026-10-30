import * as api from "/assets/js/api.js";
import { h, icon, fmtBytes, fmtMB, fmtNum, fmtTime, pct, bar, setBar, clear, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { dialog, confirm } from "/assets/js/components/dialog.js";
import { uploader } from "/assets/js/components/uploader.js";
import { openTerminal } from "/assets/js/components/terminal.js";
import { runtimeImage } from "/assets/js/kinds.js";
import { toast } from "/assets/js/toast.js";
import { markRedeploy } from "/assets/js/pages/app/redeploy.js";

const kindName = (r) => (r.kind === "jar" ? `JAR${r.main_class ? `, main class ${r.main_class}` : ""}${r.min_java ? `, bytecode butuh Java ${r.min_java}` : ""}` : r.kind === "static" ? "Binary Linux statis" : "Binary Linux glibc");

export function renderGeneral(page) {
  const node = h("div");
  let logCache = [];
  let stopLog = null;

  function terminalState() {
    const { app } = page.state;
    if (!page.mine) return "Terminal hanya untuk pemilik aplikasi";
    if (app.kind === "static") return "Binary statis memakai image distroless yang tidak punya shell";
    if (app.status !== "running") return "Aplikasi tidak sedang berjalan";
    return "";
  }

  async function newVersion() {
    const up = uploader({ limitMB: page.info.upload_limit_mb, onDone: () => {}, onClear: () => {} });
    const done = await dialog({
      title: "Unggah versi baru",
      submit: "Deploy versi baru",
      body: [h("p", {}, "File baru dipasang dengan environment dan pengaturan yang sekarang."), up.node],
      onSubmit: async () => {
        const r = up.current();
        if (!r) throw new Error("Pilih file dulu dan tunggu sampai selesai diunggah");
        if (r.report.problems.length) throw new Error(r.report.problems.join("; "));
        await api.post(`/api/apps/${page.id}/versions`, { upload_id: r.id });
        return true;
      },
    });
    if (done) {
      up.release();
      toast("Versi baru sedang dideploy");
      page.reload();
    } else {
      up.discard();
    }
  }

  function render() {
    const { app, deployment, stats, busy } = page.state;
    const running = app.status === "running";
    const termWhy = terminalState();
    const kill = running || app.status === "deploying";
    const lock = busy || app.status === "deploying";

    const upBtn = h("button", { class: "btn dark", type: "button", disabled: lock, onclick: newVersion }, icon("cloud-upload"), "Unggah versi baru");
    const restart = h("button", { class: "btn", type: "button", disabled: lock || !running, onclick: (e) => confirm({ title: "Restart aplikasi?", message: "Aplikasi berhenti sebentar lalu jalan lagi dengan deployment yang sama.", submit: "Restart", onSubmit: async () => { await api.post(`/api/apps/${page.id}/restart`); toast("Restart dimulai"); await page.reload(); } }) }, icon("rotate-cw"), "Restart");
    const toggle = kill
      ? h("button", { class: "btn danger", type: "button", disabled: lock, onclick: () => confirm({ title: "Hentikan aplikasi?", message: "Container dihentikan. Domain dan akses IP tidak melayani apa pun sampai aplikasi dijalankan lagi.", submit: "Stop", danger: true, onSubmit: async () => { await api.post(`/api/apps/${page.id}/stop`); toast("Aplikasi dihentikan"); await page.reload(); } }) }, icon("square"), "Stop")
      : h("button", { class: "btn", type: "button", disabled: lock || !deployment, title: deployment ? null : "Belum ada deployment", onclick: (e) => act(e.currentTarget, async () => { await api.post(`/api/apps/${page.id}/start`); await page.reload(); }, "Aplikasi dijalankan") }, icon("play"), "Jalankan");
    const term = h("button", { class: "btn", type: "button", disabled: Boolean(termWhy), title: termWhy || null, onclick: () => openTerminal({ id: page.id, name: app.name }) }, icon("square-terminal"), "Terminal");
    const why = termWhy && app.kind === "static" ? h("p", { class: "hint", style: { margin: "8px 0 0" } }, termWhy) : null;

    const dl = deployment
      ? h(
          "dl",
          { class: "kv" },
          h("dt", {}, "Artefak"), h("dd", { class: "mono" }, deployment.file_name, " ", h("span", { class: "faint" }, fmtBytes(deployment.report?.size))),
          h("dt", {}, "Jenis"), h("dd", {}, kindName(deployment.report)),
          h("dt", {}, "Runtime"), h("dd", { class: "mono" }, runtimeImage(app)),
          h("dt", {}, "Port aplikasi"), h("dd", { class: "mono" }, String(app.internal_port)),
          h("dt", {}, "Port server"), h("dd", { class: "mono" }, deployment.host_port ? String(deployment.host_port) : "–"),
          h("dt", {}, "Diunggah"), h("dd", {}, `${deployment.created_by === page.me.id ? page.me.username : page.state.owner}, ${fmtTime(deployment.created_at)}`),
        )
      : h("p", { class: "muted" }, "Belum ada deployment aktif.");

    const ipSw = h("button", { class: "sw", type: "button", role: "switch", "aria-checked": String(app.ip_access), "aria-label": "Akses lewat IP", disabled: busy });
    ipSw.addEventListener("click", () =>
      act(ipSw, async () => {
        await api.patch(`/api/apps/${page.id}`, { ip_access: !app.ip_access });
        await page.reload();
      }),
    );
    const host = page.info.public_ip || location.hostname;
    const domains = page.domains || [];
    const mem = stats ? pct(stats.memory_bytes, stats.memory_limit || app.memory_mb * 1048576) : 0;
    const cpuLimit = app.cpu_milli / 10;
    const cpu = stats ? pct(stats.cpu_percent, cpuLimit) : 0;
    const memLabel = stats ? `${fmtNum(stats.memory_bytes / 1048576)} / ${fmtMB(app.memory_mb)}` : `– / ${fmtMB(app.memory_mb)}`;
    const cpuLabel = stats ? `${fmtNum(stats.cpu_percent / 100)} / ${fmtNum(app.cpu_milli / 1000)} core` : `– / ${fmtNum(app.cpu_milli / 1000)} core`;

    const logPre = h("pre", { class: "log", "aria-label": "Log terbaru" });
    paintLog(logPre);

    fill(node, 
      h("section", { class: "card" }, h("header", {}, h("h3", {}, "Kontrol")), h("div", { class: "bd" }, h("div", { class: "actions" }, upBtn, restart, toggle, term), why)),
      h(
        "div",
        { class: "cols" },
        h("section", { class: "card" }, h("header", {}, h("h3", {}, "Deployment aktif"), deployment ? h("span", { class: "chip ok" }, `v${deployment.number}`) : null, h("div", { class: "r" }, h("a", { class: "btn sm ghost", href: page.path("deployments") }, "Riwayat", icon("arrow-right")))), h("div", { class: "bd" }, dl)),
        h(
          "section",
          { class: "card" },
          h("header", {}, h("h3", {}, "Akses")),
          h(
            "div",
            { class: "bd", style: { "padding-top": "2px", "padding-bottom": "2px" } },
            h("div", { class: "setting" }, h("div", {}, h("b", {}, "Akses lewat IP"), h("div", { class: "d" }, app.ip_access ? ["Terbuka di ", h("span", { class: "mono" }, `${host}:${app.ip_port}`), " untuk pengujian"] : "Ditutup. Hanya bisa diakses lewat domain")), h("div", { class: "ctl" }, ipSw)),
            h("div", { class: "setting" }, h("div", {}, h("b", {}, "Domain"), h("div", { class: "d" }, domainSummary(domains))), h("div", { class: "ctl" }, h("a", { class: "btn sm", href: page.path("domains") }, "Kelola"))),
          ),
        ),
      ),
      h(
        "div",
        { class: "cols" },
        h("section", { class: "card" }, h("header", {}, h("h3", {}, "Log terbaru"), h("div", { class: "r" }, h("a", { class: "btn sm ghost", href: page.path("logs") }, "Buka log", icon("arrow-right")))), logPre),
        h(
          "section",
          { class: "card" },
          h("header", {}, h("h3", {}, "Resource"), h("div", { class: "r" }, h("a", { class: "btn sm ghost", href: page.path("advanced") }, "Ubah"))),
          h("div", { class: "bd meters" }, meter("memory-stick", "Memori", memLabel, mem), meter("cpu", "CPU", cpuLabel, cpu)),
        ),
      ),
    );
  }

  const meter = (ic, label, text, p) => h("div", {}, h("div", { class: "t" }, h("b", {}, icon(ic), label), h("span", {}, text)), bar(p));

  function domainSummary(list) {
    if (!list.length) return "Belum ada domain";
    const active = list.filter((d) => d.status === "active").length;
    const waiting = list.length - active;
    return [active ? `${active} aktif` : null, active && waiting ? ", " : null, waiting ? `${waiting} menunggu` : null];
  }

  function paintLog(pre) {
    pre.replaceChildren(...(logCache.length ? logCache.map((l) => h("span", { class: `ln ${l.level}` }, h("b", {}, new Date(l.time).toLocaleTimeString("id", { hour12: false })), "  ", l.text)) : [h("span", { class: "empty-log" }, "Belum ada log")]));
  }

  // The last few lines for the card: a plain GET-less tail over a short-lived WebSocket.
  async function loadTail() {
    const { logStream } = await import("/assets/js/components/logstream.js");
    const lines = [];
    let timer = null;
    const flush = () => {
      logCache = lines.slice(-5);
      const pre = node.querySelector("pre.log");
      if (pre) paintLog(pre);
    };
    const s = logStream({ id: page.id, tail: 5, onLine: (l) => { lines.push(l); clearTimeout(timer); timer = setTimeout(flush, 80); }, onState: () => {} });
    return () => { clearTimeout(timer); s.close(); };
  }

  const domainsPoll = () => api.get(`/api/apps/${page.id}/domains`).then((r) => { page.domains = r.domains; page.setDomains?.(r.domains); }).catch(() => {});
  render();
  domainsPoll().then(render);
  loadTail().then((stop) => { stopLog = stop; });

  return {
    node,
    update() {
      domainsPoll();
      render();
    },
    cleanup() {
      stopLog?.();
    },
  };
}
