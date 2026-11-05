import { h, icon } from "/assets/js/dom.js";
import { folderError, workspaceLabel } from "/assets/js/validate.js";

const SOURCES = { "env.example": ".env.example", "application.properties": "application.properties", "application.yml": "application.yml", "application.yaml": "application.yaml" };

const clean = (p) => {
  const s = p.trim();
  const parts = s.split("/").filter((x) => x && x !== ".");
  if (!s.startsWith("/")) return parts.join("/");
  const abs = `/${parts.join("/")}`;
  return abs.startsWith("/data/") ? abs.slice(6) : abs;
};
const isMount = (p) => p.startsWith("/");

/**
 * folderList({appName, detected, folders, onChange}) → {node, value(), error(), changed()}.
 * The saved folders of an app: "path in the app → folder in its workspace". detected are the
 * report's folders (ticked by default when folders is not given); the user can untick, add and
 * remove rows. appName() names the workspace as the panel shows it.
 */
export function folderList({ appName, detected = [], folders = null, onChange }) {
  const rows = [];
  const listEl = h("ul", { class: "fldlist", "aria-label": "Folder tersimpan" });
  const errEl = h("div", { class: "errtext", role: "alert", hidden: true });
  const initial = JSON.stringify(folders ?? detected.map((d) => ({ path: d.path, dir: d.dir })));

  function dirsTaken(except) {
    const out = new Set();
    for (const r of rows) if (r !== except && r.on()) out.add(r.dir());
    return out;
  }
  function uniqueDir(base, except) {
    const taken = dirsTaken(except);
    if (!taken.has(base)) return base;
    for (let i = 2; ; i++) if (!taken.has(`${base}-${i}`)) return `${base}-${i}`;
  }

  function addRow({ path = "", dir = "", source = "", key = "", on = true, fixedDir = false }) {
    const check = h("input", { type: "checkbox", checked: on, "aria-label": `Simpan ${path || "folder ini"}` });
    const input = h("input", { class: "f mono", value: path, placeholder: "./storage atau /app/storage", spellcheck: "false", autocomplete: "off", "aria-label": "Path di aplikasi" });
    const label = h("span", { class: "mono fldto" });
    const note = h("small", { class: "faint" });
    const rm = h("button", { class: "ibtn", type: "button", "aria-label": "Hapus baris" }, icon("x"));
    let ownDir = fixedDir ? dir : "";
    const row = {
      on: () => check.checked,
      path: () => clean(input.value),
      dir() {
        const p = row.path();
        if (!isMount(p)) return p;
        if (ownDir) return ownDir;
        return p.split("/").pop() || "";
      },
      paint() {
        const p = row.path();
        const bad = input.value.trim() ? folderError(input.value) : "";
        input.setAttribute("aria-invalid", String(Boolean(bad)));
        label.textContent = !p || bad ? "–" : workspaceLabel(appName(), row.dir());
        note.textContent = bad || (source ? `Terdeteksi di ${SOURCES[source] || source}${key ? ` (${key})` : ""}` : isMount(p) ? "Di-mount dari folder kerja" : p ? "Sudah di folder kerja" : "");
        note.classList.toggle("bad", Boolean(bad));
        li.classList.toggle("off", !check.checked);
      },
      li: null,
    };
    const li = h("li", {}, check, h("div", { class: "fldmain" }, h("div", { class: "fldline" }, input, h("span", { class: "fldarr", "aria-hidden": "true" }, icon("arrow-right")), label), note), rm);
    row.li = li;
    check.addEventListener("change", () => update());
    input.addEventListener("input", () => {
      ownDir = "";
      update();
    });
    input.addEventListener("change", () => {
      const p = row.path();
      if (isMount(p) && !folderError(input.value)) ownDir = uniqueDir(p.split("/").pop(), row);
      update();
    });
    rm.addEventListener("click", () => {
      rows.splice(rows.indexOf(row), 1);
      li.remove();
      update();
    });
    rows.push(row);
    listEl.append(li);
    return row;
  }

  const bySource = new Map(detected.map((d) => [d.path, d]));
  const start = folders ?? detected;
  for (const f of start) {
    const d = bySource.get(f.path);
    addRow({ path: f.path, dir: f.dir, source: d?.source, key: d?.key, fixedDir: true });
  }
  if (folders) {
    for (const d of detected) if (!folders.some((f) => f.path === d.path)) addRow({ ...d, on: false, fixedDir: true });
  }

  const addBtn = h("button", { class: "btn sm", type: "button" }, icon("plus"), "Tambah folder");
  addBtn.addEventListener("click", () => {
    addRow({}).li.querySelector("input.f").focus();
    update();
  });

  const empty = h("p", { class: "hint fldempty" }, "Tidak ada folder yang terdeteksi. Tambahkan bila aplikasi menyimpan unggahan di path absolut seperti /app/storage.");

  function value() {
    return rows.filter((r) => r.on() && r.path() && !folderError(r.li.querySelector("input.f").value)).map((r) => ({ path: r.path(), dir: r.dir() }));
  }
  function error() {
    for (const r of rows) {
      if (!r.on()) continue;
      const raw = r.li.querySelector("input.f").value;
      if (!raw.trim()) return "Isi path folder tersimpan atau hapus barisnya";
      const bad = folderError(raw);
      if (bad) return bad;
    }
    const seen = new Set();
    for (const f of value()) {
      if (seen.has(f.dir)) return `Dua folder memakai ${workspaceLabel(appName(), f.dir)}`;
      seen.add(f.dir);
    }
    return "";
  }
  function update() {
    for (const r of rows) r.paint();
    empty.hidden = rows.length > 0;
    const e = error();
    errEl.textContent = e;
    errEl.hidden = !e;
    onChange?.();
  }
  update();

  return {
    node: h("div", { class: "folders" }, empty, listEl, errEl, h("div", { class: "actions start" }, addBtn)),
    value,
    error,
    changed: () => JSON.stringify(value()) !== initial,
    refresh: update,
  };
}
