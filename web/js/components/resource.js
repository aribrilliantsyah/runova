import { h, icon, fmtMB } from "/assets/js/dom.js";
import { memoryChoices, diskChoices } from "/assets/js/validate.js";

/**
 * resourceCards({usage, current}) → {node, memory(), disk(), fits(), set(mb, diskMB)}.
 * Memory and disk are chosen from what is left of the quota; current is the app's own
 * share when editing (it counts as free again). CPU is shared by all apps and not chosen.
 */
export function resourceCards({ usage, current = { memory_mb: 0, disk_mb: 0 }, memory, disk, onChange }) {
  const q = usage.quota;
  const memLeft = Math.max(q.memory_mb - usage.memory_mb + current.memory_mb, 0);
  const diskLeft = Math.max((q.disk_mb || 0) - (usage.disk_mb || 0) + (current.disk_mb || 0), 0);
  const memOpts = memoryChoices(memLeft);
  const diskOpts = diskChoices(diskLeft);
  const pick = (opts, want) => (opts.includes(want) ? want : opts.filter((v) => v <= want).pop() ?? opts[0] ?? 0);

  const memSel = h("select", { class: "f", "aria-label": "Memori" }, memOpts.map((v) => h("option", { value: v }, fmtMB(v))));
  const diskSel = h("select", { class: "f", "aria-label": "Disk" }, diskOpts.map((v) => h("option", { value: v }, fmtMB(v))));
  if (memOpts.length) memSel.value = String(pick(memOpts, memory ?? 512));
  if (diskOpts.length) diskSel.value = String(pick(diskOpts, disk || 256));
  for (const [sel, opts] of [[memSel, memOpts], [diskSel, diskOpts]]) {
    sel.disabled = !opts.length;
    sel.addEventListener("change", () => onChange?.());
  }

  const card = (ic, label, sel, hint) => h("div", { class: "rescard" }, h("div", { class: "l" }, icon(ic), label), sel, h("div", { class: "hint" }, hint));
  return {
    node: h(
      "div",
      { class: "res3" },
      card("memory-stick", "Memori", memSel, memOpts.length ? `Sisa kuota ${fmtMB(memLeft)} dari ${fmtMB(q.memory_mb)}` : `Kuota memori habis (${fmtMB(q.memory_mb)})`),
      card("hard-drive", "Disk", diskSel, diskOpts.length ? `Sisa kuota ${fmtMB(diskLeft)} dari ${fmtMB(q.disk_mb || 0)}. Isi folder kerja aplikasi.` : `Kuota disk habis (${fmtMB(q.disk_mb || 0)})`),
    ),
    memory: () => Number(memSel.value) || 0,
    disk: () => Number(diskSel.value) || 0,
    fits: () => memOpts.length > 0 && diskOpts.length > 0,
    memOpts,
    diskOpts,
    /** set(mb, diskMB) picks the nearest choices at or below the values (presets). */
    set(mb, diskMB) {
      if (memOpts.length) memSel.value = String(pick(memOpts, mb));
      if (diskMB && diskOpts.length) diskSel.value = String(pick(diskOpts, diskMB));
      onChange?.();
    },
  };
}
