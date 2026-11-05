import * as api from "/assets/js/api.js";
import { h, icon, fmtClock, fmtBytes, fmtMB, fmtNum, fmtTime, pct, bar, setBar, clear, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { dialog, confirm } from "/assets/js/components/dialog.js";
import { uploader } from "/assets/js/components/uploader.js";
import { openTerminal, closeTerminal } from "/assets/js/components/terminal.js";
import { runtimeImage } from "/assets/js/kinds.js";
import { toast } from "/assets/js/toast.js";
import { markRedeploy } from "/assets/js/pages/app/redeploy.js";
import { ipHost } from "/assets/js/components/iphost.js";
import { levelOf, fromServer } from "/assets/js/components/logview.js";
import { problemCard } from "/assets/js/components/deploytrace.js";
import { diskMeter } from "/assets/js/pages/app/files.js";

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
      : h("p", { class: "muted" }, page.state.latest ? `Belum ada deployment yang berhasil. Deploy terakhir: v${page.state.latest.number}.` : "Belum ada deployment aktif.");
    const latest = page.state.latest;
    // Why the app is not running: a failed deploy with nothing else serving, or the active
    // container that crashed, exited or went missing.
    const failedDeploy = latest && latest.status === "failed" && (!deployment || latest.id !== deployment.id) && app.status !== "running" ? latest : null;
    const broken = !failedDeploy && deployment?.problem && app.status === "failed" ? deployment : null;
    const shown = failedDeploy || broken;
    const failCard = shown
      ? h("div", { class: "genfail" }, problemCard(shown.problem, { failureLog: shown.failure_log }), h("div", { class: "actions" }, h("a", { class: "btn", href: page.path(`deployments/${shown.id}`) }, icon("scroll-text"), "Langkah & log"), h("a", { class: "btn ghost", href: page.path("advanced") }, icon("sliders-horizontal"), "Ubah pengaturan")))
      : null;

    const ipSw = h("button", { class: "sw", type: "button", role: "switch", "aria-checked": String(app.ip_access), "aria-label": "Akses lewat IP", disabled: busy });
    ipSw.addEventListener("click", () =>
      act(ipSw, async () => {
        await api.patch(`/api/apps/${page.id}`, { ip_access: !app.ip_access });
        await page.reload();
      }),
    );
    const ip = ipHost(page.info);
    const domains = page.domains || [];
    const mem = stats ? pct(stats.memory_bytes, stats.memory_limit || app.memory_mb * 1048576) : 0;
    const cpuLimit = app.cpu_milli / 10;
    const cpu = stats ? pct(stats.cpu_percent, cpuLimit) : 0;
    const memLabel = stats ? `${fmtNum(stats.memory_bytes / 1048576)} / ${fmtMB(app.memory_mb)}` : `– / ${fmtMB(app.memory_mb)}`;
    const cpuLabel = stats ? `${fmtNum(stats.cpu_percent / 100)} / ${fmtNum(app.cpu_milli / 1000)} core` : `– / ${fmtNum(app.cpu_milli / 1000)} core`;
    const disk = diskMeter(app);
    const diskRow = h("div", { class: disk.tone === "err" ? "disk-full" : "" }, h("div", { class: "t" }, h("b", {}, icon("hard-drive"), "Disk"), h("span", {}, disk.text)), bar(disk.percent, { hiAt: 80 }));

    const logPre = h("pre", { class: "log tail", "aria-label": "Log terbaru" });
    paintLog(logPre);

    fill(node,
      failCard,
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
            h("div", { class: "setting" }, h("div", {}, h("b", {}, "Akses lewat IP"), h("div", { class: "d" }, app.ip_access ? ["Terbuka di ", h("span", { class: "mono" }, `${ip.host}:${app.ip_port}`), ip.local ? " (jaringan lokal server; port akses IP tidak diteruskan dari IP publik)" : " untuk pengujian"] : "Ditutup. Hanya bisa diakses lewat domain")), h("div", { class: "ctl" }, ipSw)),
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
          h("div", { class: "bd meters" }, meter("memory-stick", "Memori", memLabel, mem), meter("cpu", "CPU", cpuLabel, cpu), diskRow, h("a", { class: "hint", href: page.path("files") }, `Lihat file di ${app.name}/`)),
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

  let logEmpty = "Memuat log…";
  function paintLog(pre) {
    // The app's own ISO timestamp repeats the clock; the card shows the message only.
    const text = (t) => t.replace(/^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s+/, "");
    pre.replaceChildren(...(logCache.length ? logCache.map((l) => h("span", { class: `ln ${l.level}`, title: l.text }, h("b", {}, fmtClock(l.time)), "  ", text(l.text))) : [h("span", { class: "empty-log" }, logEmpty)]));
  }

  // Without a running container, the card shows the end of the newest log Runova still has.
  async function savedTail(d) {
    try {
      const r = await api.get(`/api/apps/${page.id}/deployments/${d.id}/log?tail=5`);
      logCache = r.lines.map(fromServer).map((l) => ({ ...l, level: levelOf(l.text) }));
      logEmpty = d.status === "failed" && !d.container_id ? "Deploy gagal sebelum container menulis log. Penyebabnya ada di atas." : "Container ini tidak menulis log.";
    } catch {
      logEmpty = "Log tidak bisa dimuat.";
    }
    const pre = node.querySelector("pre.log");
    if (pre) paintLog(pre);
  }

  // The last few lines for the card, from a short-lived log stream. Starts are numbered so a start
  // that finishes after cleanup or after the deployment changed is dropped instead of leaking a socket.
  let tailFor = null;
  let tailGen = 0;
  let disposed = false;
  async function startTail() {
    const gen = ++tailGen;
    stopLog?.();
    stopLog = null;
    logCache = [];
    const { app, deployment, latest } = page.state;
    const streaming = deployment && app.status === "running";
    tailFor = `${(streaming ? deployment : latest || deployment)?.id ?? "none"}/${app.status}`;
    if (!streaming) {
      const d = latest && latest.status === "failed" && app.status !== "stopped" ? latest : deployment || latest;
      if (!d) {
        logEmpty = "Belum ada deployment. Log muncul begitu aplikasi di-deploy.";
        const pre = node.querySelector("pre.log");
        if (pre) paintLog(pre);
        return;
      }
      await savedTail(d);
      return;
    }
    logEmpty = "Container belum menulis log.";
    const { logStream } = await import("/assets/js/components/logstream.js");
    if (disposed || gen !== tailGen) return;
    const lines = [];
    let timer = null;
    let queued = false;
    const flush = () => {
      queued = false;
      logCache = lines.slice(-5);
      const pre = node.querySelector("pre.log");
      if (pre) paintLog(pre);
    };
    const s = logStream({
      id: page.id,
      tail: 5,
      onLine: (l) => {
        lines.push(l);
        if (lines.length > 5) lines.shift();
        if (!queued) {
          queued = true;
          timer = setTimeout(flush, 80);
        }
      },
      onState: () => {},
    });
    stopLog = () => {
      clearTimeout(timer);
      s.close();
    };
  }

  const domainsPoll = () => api.get(`/api/apps/${page.id}/domains`).then((r) => { page.domains = r.domains; page.setDomains?.(r.domains); }).catch(() => {});
  render();
  domainsPoll().then(render);
  startTail();

  return {
    node,
    update() {
      domainsPoll();
      render();
      const { app, deployment, latest } = page.state;
      const streaming = deployment && app.status === "running";
      if (`${(streaming ? deployment : latest || deployment)?.id ?? "none"}/${app.status}` !== tailFor) startTail();
    },
    cleanup() {
      disposed = true;
      tailGen++;
      stopLog?.();
      closeTerminal();
    },
  };
}
