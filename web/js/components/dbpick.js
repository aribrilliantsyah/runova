import { h, icon, fill } from "/assets/js/dom.js";
import { pgLogo } from "/assets/js/kinds.js";
import { dbSuffixError } from "/assets/js/validate.js";
import { DB_HOST_NOTE } from "/assets/js/components/envform.js";

/**
 * dbPick({databases, used, limit, username, pgDown, dbEnv, onChange}) → {node, choice(), suffix(), error(), auto()}.
 * choice() is "none", "new" or a database ID. "Buat database baru" is locked when the quota is used up.
 * dbEnv is the upload's db_env ([{name, kind, standard}]); auto() is {database, kinds} for envForm.setAuto,
 * or null without a database.
 */
export function dbPick({ databases, used, limit, username, pgDown, dbEnv, onChange }) {
  let choice = "none";
  let suffix = "";
  const left = limit - used;
  const locked = left <= 0;
  const list = h("div", { class: "dbpick", role: "radiogroup", "aria-label": "Database" });
  const standard = dbEnv.filter((v) => v.standard).map((v) => v.name);
  const autoNote = h("div", {});
  const auto = h(
    "div",
    { class: "autoenv" },
    autoNote,
    h("div", {}, "Juga selalu diberikan: ", standard.map((n) => [h("code", {}, n), " "])),
    h("div", { title: DB_HOST_NOTE }, "Host: server ini (lokal). Password ikut terisi, tidak perlu diketik."),
  );
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

  function dbName() {
    if (choice === "none") return "";
    if (choice === "new") return `${username}_${suffix || "…"}`;
    return databases.find((d) => d.id === choice)?.name || "";
  }

  function paint() {
    for (const [v, b] of opts) b.setAttribute("aria-checked", String(v === choice));
    for (const [, b] of opts) b.setAttribute("aria-pressed", b.getAttribute("aria-checked"));
    auto.hidden = choice === "none";
    const detected = dbEnv.filter((v) => !v.standard).map((v) => v.name);
    fill(
      autoNote,
      "Diisi otomatis dari ",
      h("b", { class: "mono" }, dbName()),
      detected.length ? [": ", detected.map((n) => [h("code", {}, n), " "])] : ".",
    );
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
    auto: () => (choice === "none" ? null : { database: dbName(), kinds: Object.fromEntries(dbEnv.map((v) => [v.name, v.kind])) }),
    error: () => (choice === "new" ? (suffix ? dbSuffixError(suffix) : "Isi nama database baru") : ""),
    label: () => (choice === "none" ? "Tanpa database" : choice === "new" ? `${username}_${suffix || "…"} (baru)` : databases.find((d) => d.id === choice)?.name || "–"),
  };
}
