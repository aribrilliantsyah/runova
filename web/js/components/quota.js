import { h, icon, bar, pct, fmtNum, fmtMB, fmtBytes } from "/assets/js/dom.js";

function stat(ic, label, value, of, percent) {
  return h(
    "div",
    { class: "stat" },
    h("div", { class: "l" }, icon(ic), h("span", {}, label), h("span", { class: "pct" }, `${percent}%`)),
    h("div", { class: "v" }, value, " ", h("small", {}, of)),
    bar(percent),
  );
}

/** quotaStats(usage) → the tiles above the app list: apps, memory, CPU and disk against the quota. */
export function quotaStats(usage) {
  const q = usage.quota;
  const used = usage.disk_used_bytes || 0;
  return h(
    "div",
    { class: "stats", id: "stats" },
    stat("package", "Aplikasi", String(usage.apps), `dari ${q.apps}`, pct(usage.apps, q.apps)),
    stat("memory-stick", "Memori", fmtMB(usage.memory_mb), `dari ${fmtMB(q.memory_mb)}`, pct(usage.memory_mb, q.memory_mb)),
    stat("cpu", "CPU", fmtNum(usage.cpu_milli / 1000), `dari ${fmtNum(q.cpu_milli / 1000)} core`, pct(usage.cpu_milli, q.cpu_milli)),
    stat("hard-drive", "Disk", fmtMB(usage.disk_mb || 0), `dari ${fmtMB(q.disk_mb || 0)}, terpakai ${fmtBytes(used)}`, pct(usage.disk_mb || 0, q.disk_mb || 0)),
  );
}
