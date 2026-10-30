import { h, icon, randomHex, fill } from "/assets/js/dom.js";
import { dialog } from "/assets/js/components/dialog.js";
import { envNameError, isSecretName, parseDotenv } from "/assets/js/validate.js";

/**
 * envForm({detected, autoNames}) → {node, entries(), missing(), error(), setAuto(names)}.
 * The editable environment of the deploy wizard. detected is report.env: each becomes a row
 * with its source ("kemungkinan" for a binary scan). A detected variable with no default is
 * required unless it came from a scan or the database fills it (autoNames). Secrets are masked.
 */
export function envForm({ detected, onChange }) {
  let auto = new Set();
  const rows = [];
  const list = h("div");
  const head = h("span", {});
  const pasteBtn = h("button", { class: "btn sm ghost", type: "button" }, icon("file-code-2"), "Tempel .env");
  const addBtn = h("button", { class: "btn sm", type: "button", style: { "margin-top": "10px" } }, icon("plus"), "Tambah variabel");
  const card = h("section", { class: "card" }, h("div", { class: "envhead" }, head, h("div", { class: "r" }, pasteBtn)), list);
  const node = h("div", {}, card, addBtn);

  const sourceNote = (src) => {
    if (src === "scan") return "kemungkinan, dari pemindaian binary";
    if (src === "properties") return "dirujuk di application.properties";
    if (src === "env.example") return "dari .env.example";
    return "";
  };

  function addRow({ name = "", value = "", secret = false, source = "", defaultValue = "", custom = false }) {
    const nameInput = custom ? h("input", { class: "f mono nm", "aria-label": "Nama variabel", placeholder: "NAMA_VARIABEL", autocomplete: "off", spellcheck: "false" }) : null;
    if (nameInput) nameInput.value = name;
    const valInput = h("input", { class: "f mono", "aria-label": name || "Nilai variabel", autocomplete: "off", spellcheck: "false", type: secret ? "password" : "text" });
    valInput.value = value || defaultValue;
    const reveal = h("button", { class: "ibtn", type: "button", "aria-label": "Lihat nilai", "aria-pressed": "false" }, icon("eye"));
    const gen = h("button", { class: "ibtn", type: "button", "aria-label": "Buat nilai acak", title: "Buat nilai acak" }, icon("rotate-cw"));
    const remove = h("button", { class: "ibtn", type: "button", "aria-label": "Hapus variabel" }, icon("trash-2"));
    const row = { name, source, custom, secret, defaultValue, nameInput, valInput, el: null };
    const keyIcon = h("span", { title: "Rahasia" }, icon("key-round"));
    const label = h("span", { class: "k" });
    const acts = h("span", { class: "acts" });

    const paint = () => {
      const nm = custom ? row.nameInput.value.trim().toUpperCase() : row.name;
      row.secret = custom ? row.secret || isSecretName(nm) : row.secret;
      valInput.type = row.secret && reveal.getAttribute("aria-pressed") !== "true" ? "password" : "text";
      if (!custom) valInput.setAttribute("aria-label", row.name);
      fill(
        label,
        ...(custom ? [row.nameInput] : [row.name]),
        row.secret ? keyIcon : null,
        isRequired(row) ? h("span", { class: "req", title: "Wajib diisi", "aria-label": "wajib" }, "*") : null,
        row.source === "scan" ? h("span", { class: "chip warn" }, "kemungkinan") : null,
      );
      fill(acts, row.secret ? reveal : null, row.secret ? gen : null, custom ? remove : null);
    };
    row.paint = paint;
    row.currentName = () => (custom ? row.nameInput.value.trim().toUpperCase() : row.name);

    reveal.addEventListener("click", () => {
      const on = reveal.getAttribute("aria-pressed") !== "true";
      reveal.setAttribute("aria-pressed", String(on));
      reveal.setAttribute("aria-label", on ? "Sembunyikan nilai" : "Lihat nilai");
      paint();
    });
    gen.addEventListener("click", () => {
      valInput.value = randomHex(24);
      reveal.setAttribute("aria-pressed", "false");
      paint();
      changed();
    });
    remove.addEventListener("click", () => {
      rows.splice(rows.indexOf(row), 1);
      row.el.remove();
      changed();
    });
    valInput.addEventListener("input", changed);
    nameInput?.addEventListener("input", () => { paint(); changed(); });

    row.el = h("div", { class: "envrow", "data-env": name }, label, valInput, acts);
    if (source && !custom) {
      row.el.append(h("div", { class: "hint src", style: { "grid-column": "1 / -1" } }, sourceNote(source)));
    }
    rows.push(row);
    list.append(row.el);
    paint();
    return row;
  }

  // Required: an .env.example entry without a default, or a referenced secret (JWT_SECRET, DB_PASSWORD…).
  // The report does not say whether a ${NAME} placeholder has a default, so other referenced names stay optional.
  const isRequired = (row) => !row.custom && row.source !== "scan" && row.defaultValue === "" && !auto.has(row.name) && (row.source === "env.example" || row.secret);

  function changed() {
    for (const r of rows) r.paint();
    head.textContent = `${rows.length} variabel`;
    onChange?.();
  }

  for (const d of detected) addRow({ name: d.name, secret: d.secret, source: d.source, defaultValue: d.default || "" });
  head.textContent = `${rows.length} variabel`;
  if (!rows.length) list.append(h("div", { class: "envedit-note" }, "Tidak ada variabel yang terdeteksi. Tambah sendiri bila aplikasi membutuhkannya."));

  addBtn.addEventListener("click", () => {
    list.querySelector(".envedit-note")?.remove();
    const r = addRow({ custom: true });
    r.nameInput.focus();
    changed();
  });

  pasteBtn.addEventListener("click", () => {
    const ta = h("textarea", { class: "f", "aria-label": "Isi .env", placeholder: "DB_USER=budi\nJWT_SECRET=…", spellcheck: "false" });
    dialog({
      title: "Tempel .env",
      submit: "Terapkan",
      body: [h("p", {}, "Satu variabel per baris, format NAMA=nilai. Variabel yang sudah ada diperbarui, yang baru ditambahkan."), ta],
      onSubmit: async () => {
        const parsed = parseDotenv(ta.value);
        if (!parsed.length) throw new Error("Tidak ada baris NAMA=nilai yang ditemukan");
        const bad = parsed.map((p) => envNameError(p.name)).find(Boolean);
        if (bad) throw new Error(bad);
        list.querySelector(".envedit-note")?.remove();
        for (const p of parsed) {
          const existing = rows.find((r) => r.currentName() === p.name);
          if (existing) {
            existing.valInput.value = p.value;
            existing.secret = existing.secret || p.secret;
          } else {
            addRow({ name: p.name, value: p.value, secret: p.secret, custom: true });
          }
        }
        changed();
      },
    });
  });

  return {
    node,
    /** The variables to send: required and filled ones; empty optional ones are left out. */
    entries() {
      return rows
        .map((r) => ({ name: r.currentName(), value: r.valInput.value, secret: r.secret }))
        .filter((e) => e.name && (e.value !== "" || e.secret));
    },
    /** Names of required variables that are still empty. */
    missing() {
      return rows.filter((r) => isRequired(r) && r.valInput.value.trim() === "").map((r) => r.name);
    },
    /** Rows added by hand with an unusable name. */
    nameError() {
      for (const r of rows) {
        if (!r.custom) continue;
        const n = r.currentName();
        if (!n) return "Ada variabel tanpa nama";
        const bad = envNameError(n);
        if (bad) return bad;
      }
      const names = rows.map((r) => r.currentName()).filter(Boolean);
      const dup = names.find((n, i) => names.indexOf(n) !== i);
      return dup ? `Variabel ${dup} muncul lebih dari sekali` : "";
    },
    setAuto(names) {
      auto = new Set(names);
      changed();
    },
    count: () => rows.length,
  };
}
