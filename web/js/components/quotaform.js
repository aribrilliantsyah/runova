import { h } from "/assets/js/dom.js";

const FIELDS = [
  ["apps", "Jumlah aplikasi", "aplikasi", 1, 100],
  ["memory_mb", "Memori", "MB", 64, 1048576],
  ["databases", "Jumlah database", "database", 1, 100],
  ["pg_connections", "Koneksi PostgreSQL", "koneksi", 1, 500],
  ["disk_mb", "Disk", "MB", 64, 10485760],
];

/** quotaForm(quota) → {node, value()}: one number field per quota entry. */
export function quotaForm(quota, { idPrefix = "q" } = {}) {
  const inputs = {};
  const node = h(
    "div",
    { class: "cfgform" },
    FIELDS.map(([key, label, unit]) => {
      inputs[key] = h("input", { class: "f", id: `${idPrefix}-${key}`, type: "number", inputmode: "numeric", min: "0", step: "1", value: String(quota[key]) });
      return h("div", { class: "fld", style: { margin: "0" } }, h("label", { for: `${idPrefix}-${key}` }, label, " ", h("span", { class: "faint", style: { "font-weight": "400" } }, unit)), inputs[key]);
    }),
  );
  return {
    node,
    value() {
      return Object.fromEntries(FIELDS.map(([key]) => [key, Number(inputs[key].value)]));
    },
  };
}
