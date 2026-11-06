import { h, icon, fmtNum, fmtMB, fmtBytes, pct, bar, setBar } from "/assets/js/dom.js";

const W = 300;
const H = 60;

/** A sparkline as inline SVG; values are scaled to max (or the series' own peak). */
function spark(values, max) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
  svg.setAttribute("class", "spark");
  svg.setAttribute("role", "img");
  const top = Math.max(max, ...values, 1);
  const pts = values.map((v, i) => `${values.length === 1 ? W : (i / (values.length - 1)) * W},${H - 2 - (v / top) * (H - 6)}`);
  const line = document.createElementNS("http://www.w3.org/2000/svg", "polyline");
  line.setAttribute("points", pts.join(" "));
  line.setAttribute("fill", "none");
  line.setAttribute("stroke", "currentColor");
  line.setAttribute("stroke-width", "1.75");
  line.setAttribute("stroke-linejoin", "round");
  svg.append(line);
  return svg;
}

/** Memory and CPU from the app's `stats`, sampled every 5 s while this page is open. CPU is against the whole server. */
export function renderMonitor(page) {
  const node = h("div");

  function render() {
    const { app, stats } = page.state;
    if (app.status !== "running" || !stats) {
      node.replaceChildren(h("div", { class: "card" }, h("div", { class: "empty" }, h("span", { class: "ic" }, icon("activity")), h("b", {}, "Tidak ada data"), h("span", {}, app.status === "running" ? "Docker belum mengirim statistik." : "Statistik tampil saat aplikasi berjalan."))));
      return;
    }
    const memLimit = stats.memory_limit || app.memory_mb * 1048576;
    const memP = pct(stats.memory_bytes, memLimit);
    const cores = stats.online_cpus || 1;
    const cpuLimit = cores * 100; // cpu_percent counts one core as 100; CPU is shared, so the ceiling is every core
    const cpuP = pct(stats.cpu_percent, cpuLimit);
    const { mem, cpu } = page.history;
    const panel = (ic, title, value, of, percent, series, max) =>
      h("section", { class: "card" }, h("header", {}, h("h3", {}, icon(ic), " ", title)), h("div", { class: "bd" }, h("div", { class: "stat", style: { border: "0", padding: "0", background: "none" } }, h("div", { class: "v" }, value, h("small", {}, of)), bar(percent, { big: true })), h("div", { class: "sparkbox" }, spark(series, max), h("div", { class: "hint" }, `${series.length} sampel, tiap 5 detik`))));
    node.replaceChildren(
      h(
        "div",
        { class: "cols" },
        panel("memory-stick", "Memori", fmtBytes(stats.memory_bytes), `dari ${fmtMB(app.memory_mb)} (${memP}%)`, memP, mem, memLimit),
        panel("cpu", "CPU", `${fmtNum(Math.round(stats.cpu_percent) / 100)} core`, `dari ${cores} core server, dipakai bersama (${cpuP}%)`, cpuP, cpu, cpuLimit),
      ),
    );
  }
  render();
  return { node, update: render };
}
