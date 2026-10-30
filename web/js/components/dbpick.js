import { h, icon } from "/assets/js/dom.js";
import { pgLogo } from "/assets/js/kinds.js";
import { dbSuffixError } from "/assets/js/validate.js";

export const DB_ENV_NAMES = ["DATABASE_URL", "SPRING_DATASOURCE_URL", "SPRING_DATASOURCE_USERNAME", "SPRING_DATASOURCE_PASSWORD", "PGHOST", "PGPORT", "PGUSER", "PGPASSWORD", "PGDATABASE"];

/**
 * dbPick({databases, used, limit, username, pgDown, onChange}) → {node, choice(), suffix(), error()}.
 * choice() is "none", "new" or a database ID. "Buat database baru" is locked when the quota is used up.
 */
export function dbPick({ databases, used, limit, username, pgDown, onChange }) {
  let choice = "none";
  let suffix = "";
  const left = limit - used;
  const locked = left <= 0;
  const list = h("div", { class: "dbpick", role: "radiogroup", "aria-label": "Database" });
  const auto = h("div", { class: "autoenv" }, "Diisi otomatis: ", DB_ENV_NAMES.map((n) => h("code", {}, n)));
  const suffixInput = h("input", { class: "f mono", "aria-label": "Nama database baru", placeholder: "billing", autocomplete: "off", spellcheck: "false" });
  const suffixErr = h("div", { class: "errtext", hidden: true });
  const suffixBox = h("div", { class: "fld", hidden: true, style: { "margin-top": "10px" } }, h("label", {}, "Nama database baru"), h("div", { class: "rowflex" }, h("span", { class: "mono muted" }, `${username}_`), suffixInput), suffixErr);

  const opts = [];
  const opt = (value, logo, title, sub, disabled = false) => {
    const b = h(
      "button",
      { class: "dbopt", type: "button", role: "radio", "aria-checked": "false", "aria-disabled": disabled ? "true" : null, "data-db": String(value) },
      h("span", { class: "pglogo" }, logo),
      h("div", {}, h("b", { class: value === "none" || value === "new" ? "" : "mono" }, title), h("small", {}, sub)),
    );
    b.addEventListener("click", () => {
      if (disabled) return;
      choice = value;
      paint();
      onChange?.();
    });
    opts.push([value, b]);
    list.append(b);
  };

  opt("none", icon("ban"), "Tanpa database", "Aplikasi tidak memakai PostgreSQL");
  for (const d of databases) {
    const others = d.apps.length ? `Dipakai oleh ${d.apps.join(", ")}` : "Belum dipakai aplikasi";
    opt(d.id, pgLogo(), d.name, others, pgDown);
  }
  opt("new", icon("plus"), "Buat database baru", pgDown ? "PostgreSQL sedang tidak bisa diakses" : locked ? `Batas ${limit} database tercapai` : `Sisa ${left} dari ${limit}`, locked || pgDown);

  function paint() {
    for (const [v, b] of opts) b.setAttribute("aria-checked", String(v === choice));
    for (const [, b] of opts) b.setAttribute("aria-pressed", b.getAttribute("aria-checked"));
    auto.hidden = choice === "none";
    suffixBox.hidden = choice !== "new";
    if (choice === "new") {
      const bad = suffix ? dbSuffixError(suffix) : "";
      suffixErr.textContent = bad;
      suffixErr.hidden = !bad;
      suffixInput.setAttribute("aria-invalid", String(Boolean(bad)));
    }
  }
  suffixInput.addEventListener("input", () => {
    suffix = suffixInput.value.trim().toLowerCase();
    paint();
    onChange?.();
  });
  paint();
  return {
    node: h("div", { class: "fld" }, h("label", {}, "Database"), list, suffixBox, auto),
    choice: () => choice,
    suffix: () => suffix,
    autoNames: () => (choice === "none" ? [] : DB_ENV_NAMES),
    error: () => (choice === "new" ? (suffix ? dbSuffixError(suffix) : "Isi nama database baru") : ""),
    label: () => (choice === "none" ? "Tanpa database" : choice === "new" ? `${username}_${suffix || "…"} (baru)` : databases.find((d) => d.id === choice)?.name || "–"),
  };
}
