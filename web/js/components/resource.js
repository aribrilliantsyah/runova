import { h, icon, fmtMB, fmtNum } from "/assets/js/dom.js";
import { memoryChoices, cpuChoices } from "/assets/js/validate.js";

/**
 * resourceCards({usage, current}) → {node, memory(), cpu(), fits()}.
 * Memory and CPU are chosen from what is left of the quota; current is the app's own
 * allocation when editing (it counts as free again).
 */
export function resourceCards({ usage, current = { memory_mb: 0, cpu_milli: 0 }, memory, cpu, onChange }) {
  const q = usage.quota;
  const memLeft = Math.max(q.memory_mb - usage.memory_mb + current.memory_mb, 0);
  const cpuLeft = Math.max(q.cpu_milli - usage.cpu_milli + current.cpu_milli, 0);
  const memOpts = memoryChoices(memLeft);
  const cpuOpts = cpuChoices(cpuLeft);
  const pick = (opts, want) => (opts.includes(want) ? want : opts.filter((v) => v <= want).pop() ?? opts[0] ?? 0);

  const memSel = h("select", { class: "f", "aria-label": "Memori" }, memOpts.map((v) => h("option", { value: v }, fmtMB(v))));
  const cpuSel = h("select", { class: "f", "aria-label": "CPU" }, cpuOpts.map((v) => h("option", { value: v }, `${fmtNum(v / 1000)} core`)));
  if (memOpts.length) memSel.value = String(pick(memOpts, memory ?? 512));
  if (cpuOpts.length) cpuSel.value = String(pick(cpuOpts, cpu ?? 500));
  memSel.disabled = !memOpts.length;
  cpuSel.disabled = !cpuOpts.length;
  memSel.addEventListener("change", () => onChange?.());
  cpuSel.addEventListener("change", () => onChange?.());

  const card = (ic, label, sel, hint) => h("div", { class: "rescard" }, h("div", { class: "l" }, icon(ic), label), sel, h("div", { class: "hint" }, hint));
  return {
    node: h(
      "div",
      { class: "res3" },
      card("memory-stick", "Memori", memSel, memOpts.length ? `Sisa ${fmtMB(memLeft)} dari ${fmtMB(q.memory_mb)}` : `Kuota memori habis (${fmtMB(q.memory_mb)})`),
      card("cpu", "CPU", cpuSel, cpuOpts.length ? `Sisa ${fmtNum(cpuLeft / 1000)} core dari ${fmtNum(q.cpu_milli / 1000)}` : `Kuota CPU habis (${fmtNum(q.cpu_milli / 1000)} core)`),
    ),
    memory: () => Number(memSel.value) || 0,
    cpu: () => Number(cpuSel.value) || 0,
    fits: () => memOpts.length > 0 && cpuOpts.length > 0,
  };
}
