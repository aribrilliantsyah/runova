import * as api from "/assets/js/api.js";
import { h, icon, fill, fmtBytes, fmtTime, fmtDateTime, fmtNum, pct, bar, fmtMB, copyText, debounce } from "/assets/js/dom.js";
import { dialog, confirm } from "/assets/js/components/dialog.js";
import { emptyState } from "/assets/js/components/list.js";
import { uploadError } from "/assets/js/validate.js";
import { toast } from "/assets/js/toast.js";
import { sortEntries, fileKind, KIND_ICON, rangeSelect, parentOf, appPaths, filesHash } from "/assets/js/pages/app/fileutil.js";

const join = (dir, name) => (dir ? `${dir}/${name}` : name);
const VIEW_KEY = "runova.files.view";
const SORT_KEY = "runova.files.sort";
const TEXT_LINES = 2000;

/** diskMeter(app) → the workspace's use against its disk size, as the Umum tab and this tab show it. */
export function diskMeter(app) {
  const used = app.disk_used_bytes || 0;
  const size = (app.disk_mb || 0) * 1048576;
  const p = size ? pct(used, size) : 0;
  const text = size ? `${fmtBytes(used)} / ${fmtMB(app.disk_mb)}` : `${fmtBytes(used)}, belum dibatasi`;
  const tone = app.disk_level === "full" ? "full" : app.disk_level === "warn" ? "warn" : "";
  return { text, percent: p, tone };
}

function stored(key, fallback) {
  try {
    return localStorage.getItem(key) || fallback;
  } catch {
    return fallback;
  }
}
function store(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // storage blocked: the choice lasts until reload
  }
}

/** The folder the URL points at: "#/apps/7/files?path=storage%2F2026". */
function hashPath() {
  const i = location.hash.indexOf("?");
  return new URLSearchParams(i < 0 ? "" : location.hash.slice(i + 1)).get("path") || "";
}

/**
 * Files tab: a file manager for the app's workspace (shown as "<app>/…"). The folder lives in the
 * URL (?path=) so refresh, back and shared links keep it. List or grid view, sort, filter,
 * multi-select with bulk download (zip) and delete, upload by picker or drag and drop with
 * per-file progress, new folder, rename, move, copy the app's path, and a preview panel.
 */
export function renderFiles(page) {
  const node = h("div", { class: "fm-wrap" });
  let dir = page.sub ? page.sub : hashPath();
  if (page.sub) history.replaceState(history.state, "", filesHash(page.id, dir));
  let entries = null;
  let error = "";
  let req = 0;
  let filter = "";
  let view = stored(VIEW_KEY, "list") === "grid" ? "grid" : "list";
  let [sortKey, sortOrder] = stored(SORT_KEY, "name:asc").split(":");
  let selected = new Set();
  let anchor = "";
  let cursor = "";
  let previewing = null; // the entry shown in the side panel
  const uploads = []; // {name, pct, state: "wait"|"run"|"ok"|"err", msg}

  const folders = () => page.app.folders || [];
  const holdsSaved = (rel) => folders().some((f) => f.dir.startsWith(`${rel}/`));
  /** locked(e) → why the entry cannot be deleted, renamed or moved, or "". */
  const locked = (e) => {
    const rel = join(dir, e.name);
    if (e.saved) return "Folder tersimpan: isinya tetap ada saat deploy ulang. Foldernya tidak bisa dihapus, diganti namanya, atau dipindah; atur di tab Lanjutan.";
    if (e.type === "dir" && holdsSaved(rel)) return "Berisi folder tersimpan, jadi tidak bisa dihapus, diganti namanya, atau dipindah.";
    return "";
  };

  // Fixed parts, built once; render() refills the moving ones.
  const fileInput = h("input", { type: "file", multiple: true, hidden: true, "aria-hidden": "true", tabindex: "-1" });
  const upBtn = h("button", { class: "btn dark", type: "button", id: "fupload" }, icon("upload"), h("span", { class: "fm-lbl" }, "Unggah"));
  upBtn.addEventListener("click", () => fileInput.click());
  fileInput.addEventListener("change", () => {
    const list = [...fileInput.files];
    fileInput.value = "";
    queueUploads(list);
  });
  const newBtn = h("button", { class: "btn", type: "button", id: "fnewdir", title: "Folder baru" }, icon("folder-plus"), h("span", { class: "fm-lbl" }, "Folder baru"));
  newBtn.addEventListener("click", () => newFolder());
  const reloadBtn = h("button", { class: "btn", type: "button", "aria-label": "Muat ulang", title: "Muat ulang" }, icon("rotate-cw"));
  reloadBtn.addEventListener("click", () => load());
  const upDirBtn = h("button", { class: "btn", type: "button", id: "fup", "aria-label": "Naik satu folder", title: "Naik satu folder (Backspace)" }, icon("corner-left-up"));
  upDirBtn.addEventListener("click", () => go(parentOf(dir)));
  const search = h("input", { type: "search", id: "ffilter", placeholder: "Cari di folder ini", "aria-label": "Cari nama di folder ini", autocomplete: "off", spellcheck: "false", autocapitalize: "none" });
  const later = debounce(() => {
    filter = search.value.trim().toLowerCase();
    paintBody();
  }, 120);
  search.addEventListener("input", later);
  search.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && search.value) {
      e.stopPropagation();
      search.value = "";
      filter = "";
      paintBody();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      focusRow(visible()[0]?.name);
    }
  });
  const viewSeg = h(
    "div",
    { class: "seg fm-view", role: "group", "aria-label": "Tampilan" },
    h("button", { type: "button", "data-view": "list", "aria-label": "Daftar", title: "Daftar" }, icon("list")),
    h("button", { type: "button", "data-view": "grid", "aria-label": "Kisi", title: "Kisi dengan gambar kecil" }, icon("layout-grid")),
  );
  viewSeg.addEventListener("click", (e) => {
    const b = e.target.closest("button[data-view]");
    if (!b) return;
    view = b.dataset.view;
    store(VIEW_KEY, view);
    paintBody();
  });

  const crumbsEl = h("nav", { class: "fm-crumbs fcrumbs", "aria-label": "Lokasi folder" });
  const selBar = h("div", { class: "fm-sel", role: "region", "aria-label": "File terpilih", hidden: true });
  const upList = h("ul", { class: "fm-up", "aria-label": "Unggahan", hidden: true });
  const body = h("div", { class: "fm-body", id: "filelist" });
  const drop = h("div", { class: "fm-drop", hidden: true, "aria-hidden": "true" }, h("div", {}, icon("upload"), h("b", {}, "Lepaskan untuk mengunggah"), h("span", { class: "fm-drop-to mono" })));
  const stage = h("div", { class: "fm-stage" }, body, drop);
  const status = h("div", { class: "fm-status", id: "fstatus" });
  const side = h("aside", { class: "fm-side", id: "fpreview", "aria-label": "Pratinjau", hidden: true });
  const main = h(
    "section",
    { class: "card fm" },
    h("header", { class: "fm-head" }, crumbsEl, h("div", { class: "r" }, newBtn, upBtn, fileInput)),
    h("div", { class: "fm-tools" }, upDirBtn, h("label", { class: "filter fm-search" }, icon("search"), search), h("span", { class: "spacer" }), reloadBtn, viewSeg),
    selBar,
    upList,
    stage,
    status,
  );
  const layout = h("div", { class: "fm-layout" }, main, side);
  node.append(layout);

  // Drag and drop onto the list.
  let dragDepth = 0;
  const hasFiles = (e) => [...(e.dataTransfer?.types || [])].includes("Files");
  stage.addEventListener("dragenter", (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    dragDepth++;
    drop.querySelector(".fm-drop-to").textContent = `ke ${page.app.name}/${dir ? `${dir}/` : ""}`;
    drop.hidden = false;
    stage.classList.add("over");
  });
  stage.addEventListener("dragover", (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
  });
  stage.addEventListener("dragleave", () => {
    if (--dragDepth <= 0) endDrag();
  });
  stage.addEventListener("drop", (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    endDrag();
    const items = [...(e.dataTransfer.items || [])];
    const skipped = items.filter((it) => it.webkitGetAsEntry?.()?.isDirectory).length;
    const files = items.length
      ? items.filter((it) => it.kind === "file" && !it.webkitGetAsEntry?.()?.isDirectory).map((it) => it.getAsFile()).filter(Boolean)
      : [...e.dataTransfer.files];
    if (skipped) toast(`${skipped} folder dilewati: unggah isinya, atau buat foldernya dulu dengan Folder baru`, "err");
    queueUploads(files);
  });
  function endDrag() {
    dragDepth = 0;
    drop.hidden = true;
    stage.classList.remove("over");
  }

  async function load({ keep = false } = {}) {
    const my = ++req;
    if (!keep) {
      entries = null;
      paintBody();
    }
    try {
      const r = await api.get(`/api/apps/${page.id}/files?path=${encodeURIComponent(dir)}`);
      if (my !== req) return;
      entries = r.entries;
      error = "";
      const names = new Set(entries.map((e) => e.name));
      selected = new Set([...selected].filter((n) => names.has(n)));
      if (previewing && !names.has(previewing.name)) closePreview();
    } catch (e) {
      if (my !== req) return;
      entries = null;
      error = e.message;
    }
    render();
  }

  function go(path) {
    location.hash = filesHash(page.id, path);
  }
  function enter(path) {
    if (path === dir && entries) return;
    dir = path;
    entries = null;
    selected = new Set();
    anchor = cursor = "";
    filter = "";
    search.value = "";
    closePreview();
    render();
    load();
  }

  async function reloadAll() {
    await page.reload();
    await load({ keep: true });
  }

  // Uploads: one request per file, in order; each row shows its own progress.
  let uploading = false;
  function queueUploads(files) {
    if (!files.length) return;
    const limit = page.info.upload_limit_mb;
    for (const f of files) {
      const bad = uploadError(f, limit);
      uploads.push({ file: f, name: f.name, dir, pct: 0, state: bad ? "err" : "wait", msg: bad });
    }
    paintUploads();
    if (!uploading) runUploads();
  }
  async function runUploads() {
    uploading = true;
    let done = 0;
    for (let u = uploads.find((x) => x.state === "wait"); u; u = uploads.find((x) => x.state === "wait")) {
      u.state = "run";
      paintUploads();
      try {
        await api.upload(`/api/apps/${page.id}/files?path=${encodeURIComponent(u.dir)}`, u.file, (p) => {
          u.pct = Math.round(p * 100);
          paintUploads();
        }).promise;
        u.state = "ok";
        done++;
      } catch (e) {
        u.state = "err";
        u.msg = e.message;
      }
      paintUploads();
    }
    uploading = false;
    if (done) {
      toast(done === 1 ? "1 file diunggah" : `${fmtNum(done)} file diunggah`);
      await reloadAll();
    }
    // Finished rows leave after a moment; failures stay until dismissed.
    setTimeout(() => {
      for (let i = uploads.length - 1; i >= 0; i--) if (uploads[i].state === "ok") uploads.splice(i, 1);
      paintUploads();
    }, 2500);
  }
  function paintUploads() {
    upList.hidden = !uploads.length;
    fill(
      upList,
      uploads.map((u, i) =>
        h(
          "li",
          { "data-state": u.state, "data-upload": u.name },
          icon(u.state === "ok" ? "circle-check" : u.state === "err" ? "circle-alert" : "upload"),
          h("span", { class: "n mono", title: u.name }, u.name),
          u.state === "err" ? h("span", { class: "m" }, u.msg) : h("span", { class: "m" }, u.state === "wait" ? "menunggu" : u.state === "ok" ? "selesai" : `${u.pct}%`),
          u.state === "run" || u.state === "wait" ? bar(u.pct, { hiAt: 101 }) : null,
          u.state === "err" ? h("button", { type: "button", class: "ibtn", "aria-label": `Tutup ${u.name}`, onclick: () => { uploads.splice(i, 1); paintUploads(); } }, icon("x")) : null,
        ),
      ),
    );
  }

  function visible() {
    if (!entries) return [];
    const list = filter ? entries.filter((e) => e.name.toLowerCase().includes(filter)) : entries;
    return sortEntries(list, sortKey, sortOrder);
  }
  const entry = (name) => entries?.find((e) => e.name === name);

  // Actions
  function open(e) {
    const rel = join(dir, e.name);
    if (e.type === "dir") return go(rel);
    if (e.type === "link") return toast("Link tidak diikuti Runova; isinya tidak bisa dibuka dari sini", "err");
    showPreview(e);
  }
  const downloadUrl = (rel) => `/api/apps/${page.id}/files/download?path=${encodeURIComponent(rel)}`;
  function startDownload(href, name) {
    const a = h("a", { href, download: name || "", hidden: true });
    document.body.append(a);
    a.click();
    a.remove();
  }
  async function download(list) {
    if (list.length === 1 && list[0].type === "file") return startDownload(downloadUrl(join(dir, list[0].name)), list[0].name);
    const qs = list.map((e) => `path=${encodeURIComponent(join(dir, e.name))}`).join("&");
    try {
      const plan = await api.get(`/api/apps/${page.id}/files/zip?${qs}&check=1`);
      toast(`Menyiapkan ${plan.name} (${fmtBytes(plan.size)})`);
      startDownload(`/api/apps/${page.id}/files/zip?${qs}`, plan.name);
    } catch (err) {
      toast(err.message, "err");
    }
  }
  function remove(list) {
    const blocked = list.find(locked);
    if (blocked) return toast(locked(blocked), "err");
    const one = list.length === 1 ? list[0] : null;
    const names = list.slice(0, 5).map((e) => `${page.app.name}/${join(dir, e.name)}${e.type === "dir" ? "/" : ""}`);
    const more = list.length > 5 ? `dan ${fmtNum(list.length - 5)} lainnya` : "";
    const hasDir = list.some((e) => e.type === "dir");
    return dialog({
      title: one ? (one.type === "dir" ? "Hapus folder?" : "Hapus file?") : `Hapus ${fmtNum(list.length)} item?`,
      submit: "Hapus",
      danger: true,
      body: [
        h("ul", { class: "fm-dlist mono" }, names.map((n) => h("li", {}, n)), more ? h("li", { class: "muted" }, more) : null),
        h("p", {}, `${hasDir ? "Folder dihapus beserta seluruh isinya. " : ""}Tidak bisa dikembalikan; aplikasi yang sedang berjalan mungkin masih membutuhkannya.`),
      ],
      onSubmit: async () => {
        await api.del(`/api/apps/${page.id}/files`, { paths: list.map((e) => join(dir, e.name)) });
        toast(one ? `${one.name} dihapus` : `${fmtNum(list.length)} item dihapus`);
        for (const e of list) selected.delete(e.name);
        if (previewing && list.some((e) => e.name === previewing.name)) closePreview();
        await reloadAll();
      },
    });
  }
  function nameDialog({ title, submit, value = "", label, onSubmit }) {
    const input = h("input", { class: "f mono", value, "aria-label": label, autocomplete: "off", spellcheck: "false", autocapitalize: "none" });
    const err = h("div", { class: "errtext", hidden: true });
    const check = () => {
      const v = input.value.trim();
      const bad = !v ? "" : v === "." || v === ".." ? "Nama tidak boleh . atau .." : /[/\\]/.test(v) ? "Nama tidak boleh memuat / atau \\" : v.length > 255 ? "Nama maksimal 255 karakter" : entries?.some((e) => e.name === v && v !== value) ? `${v} sudah ada di folder ini` : "";
      err.textContent = bad;
      err.hidden = !bad;
      input.setAttribute("aria-invalid", String(Boolean(bad)));
      return bad;
    };
    input.addEventListener("input", check);
    const p = dialog({
      title,
      submit,
      body: [h("div", { class: "fld", style: { margin: "0" } }, h("label", {}, label), input, err, h("div", { class: "hint" }, `Di ${page.app.name}/${dir ? `${dir}/` : ""}`))],
      onSubmit: async () => {
        const v = input.value.trim();
        if (!v) throw new Error("Isi namanya dulu");
        const bad = check();
        if (bad) throw new Error(bad);
        await onSubmit(v);
      },
    });
    // Select the name without its extension, as desktop file managers do.
    const dot = value.lastIndexOf(".");
    input.setSelectionRange(0, dot > 0 ? dot : value.length);
    return p;
  }
  function newFolder() {
    return nameDialog({
      title: "Folder baru",
      submit: "Buat folder",
      label: "Nama folder",
      onSubmit: async (name) => {
        await api.post(`/api/apps/${page.id}/files/folder`, { path: dir, name });
        toast(`Folder ${name} dibuat`);
        await load({ keep: true });
        focusRow(name);
      },
    });
  }
  function rename(e) {
    const why = locked(e);
    if (why) return toast(why, "err");
    return nameDialog({
      title: e.type === "dir" ? "Ganti nama folder" : "Ganti nama file",
      submit: "Ganti nama",
      label: "Nama baru",
      value: e.name,
      onSubmit: async (name) => {
        if (name === e.name) return;
        await api.post(`/api/apps/${page.id}/files/move`, { from: join(dir, e.name), to: join(dir, name) });
        toast(`${e.name} diganti menjadi ${name}`);
        if (previewing?.name === e.name) closePreview();
        await reloadAll();
        focusRow(name);
      },
    });
  }
  function move(e) {
    const why = locked(e);
    if (why) return toast(why, "err");
    const from = join(dir, e.name);
    let at = dir;
    const crumbs = h("nav", { class: "fcrumbs fm-pick-crumbs", "aria-label": "Folder tujuan" });
    const list = h("ul", { class: "fm-pick", role: "listbox", "aria-label": "Subfolder" });
    const note = h("p", { class: "hint fm-pick-note" });
    let reqPick = 0;
    async function show(path) {
      at = path;
      const my = ++reqPick;
      const parts = path ? path.split("/") : [];
      fill(crumbs, h("button", { type: "button", class: "mono", onclick: () => show("") }, `${page.app.name}/`), parts.map((p, i) => h("button", { type: "button", class: "mono", "aria-current": i === parts.length - 1 ? "location" : null, onclick: () => show(parts.slice(0, i + 1).join("/")) }, `${p}/`)));
      fill(list, h("li", { class: "muted" }, "Memuat…"));
      try {
        const r = await api.get(`/api/apps/${page.id}/files?path=${encodeURIComponent(path)}`);
        if (my !== reqPick) return;
        const dirs = sortEntries(r.entries.filter((x) => x.type === "dir" && join(path, x.name) !== from), "name");
        fill(list, dirs.length ? dirs.map((x) => h("li", {}, h("button", { type: "button", onclick: () => show(join(path, x.name)) }, icon("folder"), h("span", { class: "mono" }, x.name), x.saved ? h("span", { class: "chip ok" }, "tersimpan") : null, icon("chevron-right")))) : h("li", { class: "muted" }, "Tidak ada subfolder"));
      } catch (err) {
        fill(list, h("li", { class: "errtext" }, err.message));
      }
      note.textContent = path === dir ? "Ini folder asalnya; pilih folder lain." : `Dipindah ke ${page.app.name}/${join(path, e.name)}`;
    }
    show(dir);
    return dialog({
      title: `Pindahkan ${e.name}`,
      submit: "Pindahkan ke sini",
      body: [crumbs, list, note],
      onSubmit: async () => {
        if (at === dir) throw new Error("Pilih folder lain sebagai tujuan");
        await api.post(`/api/apps/${page.id}/files/move`, { from, to: join(at, e.name) });
        toast(`${e.name} dipindah ke ${page.app.name}/${at ? `${at}/` : ""}`);
        if (previewing?.name === e.name) closePreview();
        await reloadAll();
      },
    });
  }
  function copyPaths(e) {
    const rel = join(dir, e.name);
    const paths = appPaths(rel, folders());
    return dialog({
      title: "Salin path",
      submit: "Selesai",
      hideCancel: true,
      body: [
        h("p", {}, "Path yang dipakai aplikasi di dalam container untuk ", h("span", { class: "mono" }, e.name), ". Folder kerjanya ", h("span", { class: "mono" }, "/data"), ", jadi path relatif dihitung dari sana."),
        h("div", { class: "fm-paths" }, paths.map((p) => pathRow(p))),
      ],
    });
  }
  function pathRow(p) {
    const btn = h("button", { type: "button", class: "ibtn", "aria-label": `Salin ${p}`, title: "Salin" }, icon("copy"));
    btn.addEventListener("click", async () => {
      if (await copyText(p)) toast(`Disalin: ${p}`);
    });
    return h("div", { class: "fm-path" }, h("code", {}, p), btn);
  }

  // Row menu: the ⋯ button and right-click open the same items; phones get it as a bottom sheet.
  let menuClose = null;
  function menu(e, at) {
    menuClose?.();
    const why = locked(e);
    const items = [
      e.type === "dir" ? ["folder-open", "Buka", () => open(e)] : e.type === "file" ? ["eye", "Pratinjau", () => open(e)] : null,
      e.type !== "link" ? ["download", e.type === "dir" ? "Unduh (zip)" : "Unduh", () => download([e])] : null,
      ["text-cursor-input", "Ganti nama", () => rename(e), why],
      ["folder-output", "Pindahkan", () => move(e), why],
      ["copy", "Salin path", () => copyPaths(e)],
      "sep",
      ["trash-2", "Hapus", () => remove([e]), why, "danger"],
    ].filter(Boolean);
    const pop = h("div", { class: "pop fm-menu", role: "menu", "aria-label": `Aksi ${e.name}` });
    const scrim = h("div", { class: "fm-scrim" });
    const close = () => {
      pop.remove();
      scrim.remove();
      document.removeEventListener("mousedown", onDoc, true);
      document.removeEventListener("keydown", onKey, true);
      menuClose = null;
    };
    menuClose = close;
    const onDoc = (ev) => !pop.contains(ev.target) && close();
    const onKey = (ev) => {
      if (ev.key === "Escape") {
        ev.stopPropagation();
        close();
        focusRow(e.name);
      } else if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
        ev.preventDefault();
        const list = [...pop.querySelectorAll("button:not(:disabled)")];
        const i = list.indexOf(document.activeElement);
        list[(i + (ev.key === "ArrowDown" ? 1 : -1) + list.length) % list.length]?.focus();
      }
    };
    pop.append(
      h("div", { class: "fm-menu-h mono" }, e.name),
      ...items.map((it) =>
        it === "sep"
          ? h("div", { class: "sep" })
          : h("button", { class: `item ${it[4] || ""}`, type: "button", role: "menuitem", disabled: Boolean(it[3]), title: it[3] || null, onclick: () => { close(); it[2](); } }, icon(it[0]), it[1]),
      ),
      why ? h("p", { class: "fm-menu-why" }, icon("lock"), why) : null,
    );
    document.body.append(scrim, pop);
    const w = pop.offsetWidth;
    const hgt = pop.offsetHeight;
    pop.style.setProperty("left", `${Math.max(8, Math.min(at.x, innerWidth - w - 8))}px`);
    pop.style.setProperty("top", `${Math.max(8, Math.min(at.y, innerHeight - hgt - 8))}px`);
    document.addEventListener("mousedown", onDoc, true);
    document.addEventListener("keydown", onKey, true);
    pop.querySelector("button:not(:disabled)")?.focus();
  }

  // Preview panel
  let previewReq = 0;
  async function showPreview(e) {
    previewing = e;
    const rel = join(dir, e.name);
    const my = ++previewReq;
    side.hidden = false;
    layout.classList.add("has-side");
    paintRows();
    const kind = fileKind(e);
    const closeBtn = h("button", { type: "button", class: "ibtn", "aria-label": "Tutup pratinjau", title: "Tutup (Esc)", onclick: () => closePreview(true) }, icon("x"));
    const view = h("div", { class: "fm-pv" }, h("div", { class: "fm-pv-wait" }, "Memuat pratinjau…"));
    const info = h("dl", { class: "fm-info" });
    const acts = h(
      "div",
      { class: "fm-pv-acts" },
      h("a", { class: "btn", href: downloadUrl(rel), download: e.name }, icon("download"), "Unduh"),
      h("button", { type: "button", class: "btn", disabled: Boolean(locked(e)), onclick: () => rename(e) }, icon("text-cursor-input"), "Ganti nama"),
      h("button", { type: "button", class: "btn danger", disabled: Boolean(locked(e)), onclick: () => remove([e]) }, icon("trash-2"), "Hapus"),
    );
    fill(side, h("header", {}, icon(KIND_ICON[kind]), h("b", { class: "mono", title: e.name }, e.name), closeBtn), view, info, acts);
    side.focus?.();
    let d;
    try {
      d = await api.get(`/api/apps/${page.id}/files/info?path=${encodeURIComponent(rel)}`);
    } catch (err) {
      if (my === previewReq) fill(view, h("div", { class: "fm-pv-none" }, icon("circle-alert"), h("span", {}, err.message)));
      return;
    }
    if (my !== previewReq) return;
    fill(
      info,
      h("dt", {}, "Ukuran"), h("dd", {}, `${fmtBytes(d.size)}${d.size >= 1024 ? ` (${fmtNum(d.size)} byte)` : ""}`),
      h("dt", {}, "Tipe"), h("dd", { class: "mono" }, d.mime),
      h("dt", {}, "Diubah"), h("dd", { title: fmtDateTime(d.mod_time) }, `${fmtTime(d.mod_time)}, ${fmtDateTime(d.mod_time)}`),
      h("dt", {}, "Di workspace"), h("dd", { class: "mono" }, `${page.app.name}/${rel}`),
      h("dt", {}, "Di aplikasi"), h("dd", { class: "fm-paths" }, appPaths(rel, folders()).map(pathRow)),
    );
    const src = `/api/apps/${page.id}/files/preview?path=${encodeURIComponent(rel)}`;
    if (d.preview === "image") {
      const img = h("img", { src, alt: `Pratinjau ${e.name}`, decoding: "async" });
      img.addEventListener("error", () => fill(view, h("div", { class: "fm-pv-none" }, icon("image"), h("span", {}, "Gambar tidak bisa ditampilkan."))), { once: true });
      img.addEventListener("load", () => img.classList.toggle("tiny", img.naturalWidth < 48), { once: true });
      fill(view, h("div", { class: "fm-pv-img" }, img));
    } else if (d.preview === "text") {
      try {
        const res = await fetch(src, { credentials: "same-origin" });
        if (!res.ok) throw new Error(`Pratinjau gagal (${res.status})`);
        const text = await res.text();
        if (my !== previewReq) return;
        const lines = text.split("\n");
        if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
        const shown = lines.slice(0, TEXT_LINES);
        fill(
          view,
          h("pre", { class: "fm-code", tabindex: "0", "aria-label": `Isi ${e.name}` }, shown.map((l) => h("span", { class: "ln" }, l || "\u200b"))),
          lines.length > TEXT_LINES ? h("p", { class: "hint" }, `Menampilkan ${fmtNum(TEXT_LINES)} dari ${fmtNum(lines.length)} baris. Unduh untuk melihat semuanya.`) : null,
        );
      } catch (err) {
        fill(view, h("div", { class: "fm-pv-none" }, icon("circle-alert"), h("span", {}, err.message)));
      }
    } else {
      const why = d.mime === "image/svg+xml" ? "SVG tidak ditampilkan di panel karena bisa memuat skrip." : d.mime.startsWith("text/") ? "File teks di atas 1 MB tidak ditampilkan." : "Pratinjau tidak tersedia untuk tipe ini.";
      fill(view, h("div", { class: "fm-pv-none" }, icon(KIND_ICON[kind]), h("span", {}, why), h("a", { class: "btn sm", href: downloadUrl(rel), download: e.name }, icon("download"), "Unduh")));
    }
  }
  function closePreview(refocus = false) {
    if (!previewing) return;
    const name = previewing.name;
    previewing = null;
    previewReq++;
    side.hidden = true;
    side.replaceChildren();
    layout.classList.remove("has-side");
    paintRows();
    if (refocus) focusRow(name);
  }

  // Selection and keyboard
  function toggle(name, ev) {
    if (ev?.shiftKey && anchor) {
      selected = rangeSelect(visible().map((e) => e.name), selected, anchor, name);
    } else {
      if (selected.has(name)) selected.delete(name);
      else selected.add(name);
      anchor = name;
    }
    cursor = name;
    paintRows();
    paintSel();
  }
  function focusRow(name) {
    if (!name) return;
    cursor = name;
    const el = body.querySelector(`[data-file="${CSS.escape(name)}"]`);
    if (!el) return;
    for (const r of body.querySelectorAll("[data-file]")) r.tabIndex = r === el ? 0 : -1;
    el.focus();
  }
  body.addEventListener("keydown", (ev) => {
    const row = ev.target.closest?.("[data-file]");
    // Only the focused row itself handles keys; its inner buttons keep their own Enter/Space.
    if (!row || ev.target !== row) return;
    const names = visible().map((e) => e.name);
    const i = names.indexOf(row.dataset.file);
    const e = entry(row.dataset.file);
    const step = view === "grid" ? gridCols() : 1;
    switch (ev.key) {
      case "ArrowDown":
      case "ArrowUp":
      case "ArrowRight":
      case "ArrowLeft": {
        if (view === "list" && (ev.key === "ArrowLeft" || ev.key === "ArrowRight")) return;
        ev.preventDefault();
        const d = ev.key === "ArrowDown" ? step : ev.key === "ArrowUp" ? -step : ev.key === "ArrowRight" ? 1 : -1;
        const next = names[Math.max(0, Math.min(names.length - 1, i + d))];
        if (ev.shiftKey) {
          selected = rangeSelect(names, selected, anchor || row.dataset.file, next);
          paintRows();
          paintSel();
        }
        focusRow(next);
        break;
      }
      case "Home":
      case "End":
        ev.preventDefault();
        focusRow(ev.key === "Home" ? names[0] : names[names.length - 1]);
        break;
      case "Enter":
        ev.preventDefault();
        if (e) open(e);
        break;
      case " ":
        ev.preventDefault();
        toggle(row.dataset.file, ev);
        focusRow(row.dataset.file);
        break;
      case "Backspace":
        ev.preventDefault();
        if (dir) go(parentOf(dir));
        break;
      case "Delete": {
        ev.preventDefault();
        const list = selected.size ? entries.filter((x) => selected.has(x.name)) : e ? [e] : [];
        if (list.length) remove(list);
        break;
      }
      case "F2":
        ev.preventDefault();
        if (e) rename(e);
        break;
      case "ContextMenu": {
        ev.preventDefault();
        const r = row.getBoundingClientRect();
        if (e) menu(e, { x: r.left + 40, y: r.bottom });
        break;
      }
      case "a":
        if (ev.ctrlKey || ev.metaKey) {
          ev.preventDefault();
          selected = new Set(names);
          paintRows();
          paintSel();
        }
        break;
      case "Escape":
        if (selected.size) {
          ev.preventDefault();
          selected = new Set();
          paintRows();
          paintSel();
        } else if (previewing) {
          closePreview(true);
        }
        break;
    }
  });
  function gridCols() {
    const items = [...body.querySelectorAll(".fm-tile")];
    if (items.length < 2) return 1;
    const top = items[0].offsetTop;
    const n = items.findIndex((x) => x.offsetTop !== top);
    return n < 0 ? items.length : n;
  }

  // Rendering
  function crumbs() {
    const parts = dir ? dir.split("/") : [];
    const items = [h("a", { href: filesHash(page.id, ""), class: "mono", "aria-current": parts.length ? null : "page" }, page.app.name)];
    parts.forEach((p, i) => {
      const to = parts.slice(0, i + 1).join("/");
      items.push(h("span", { class: "sl", "aria-hidden": "true" }, "/"));
      items.push(i === parts.length - 1 ? h("span", { class: "mono cur", "aria-current": "page" }, p) : h("a", { href: filesHash(page.id, to), class: "mono" }, p));
    });
    fill(crumbsEl, icon("hard-drive"), items);
  }

  function badge(e) {
    if (e.saved) return h("span", { class: "chip ok", title: locked(e) }, "tersimpan");
    if (e.type === "link") return h("span", { class: "chip", title: "Link tidak diikuti Runova" }, "link");
    return null;
  }
  const sizeText = (e) => (e.type === "dir" ? `${fmtNum(e.items)} item` : e.type === "link" ? "–" : fmtBytes(e.size));

  function rowFor(e) {
    const on = selected.has(e.name);
    const check = h("input", { type: "checkbox", checked: on, tabindex: "-1", "aria-label": `Pilih ${e.name}` });
    check.addEventListener("click", (ev) => {
      ev.stopPropagation();
      ev.preventDefault();
      toggle(e.name, ev);
    });
    const kind = fileKind(e);
    const name = h("button", { type: "button", class: "fm-name", tabindex: "-1", onclick: (ev) => { ev.stopPropagation(); open(e); } }, h("span", { class: "mono" }, e.name));
    const more = h("button", { type: "button", class: "ibtn fm-more", tabindex: "-1", "aria-label": `Aksi ${e.name}`, title: "Aksi" }, icon("ellipsis"));
    more.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const r = more.getBoundingClientRect();
      menu(e, { x: r.right - 220, y: r.bottom + 4 });
    });
    const attrs = {
      "data-file": e.name,
      "data-kind": kind,
      tabindex: e.name === cursor ? "0" : "-1",
      "aria-selected": String(on),
      class: [on ? "on" : "", previewing?.name === e.name ? "pv" : ""].filter(Boolean).join(" ") || null,
    };
    let el;
    if (view === "grid") {
      const thumb = kind === "image" && !/\.svg$/i.test(e.name)
        ? h("img", { src: `/api/apps/${page.id}/files/preview?path=${encodeURIComponent(join(dir, e.name))}`, alt: "", loading: "lazy", decoding: "async" })
        : icon(KIND_ICON[kind]);
      if (thumb.tagName === "IMG") thumb.addEventListener("error", () => thumb.replaceWith(icon(KIND_ICON[kind])), { once: true });
      el = h("li", { ...attrs, class: ["fm-tile", attrs.class].filter(Boolean).join(" "), role: "option" }, h("div", { class: "fm-thumb" }, thumb), check, h("div", { class: "fm-tl" }, name, h("span", { class: "fm-meta" }, sizeText(e), badge(e))), more);
    } else {
      el = h(
        "tr",
        { ...attrs, role: "row" },
        h("td", { class: "c-chk" }, check),
        h("td", { class: "c-name" }, h("div", { class: "fm-cell" }, h("span", { class: "fm-ic" }, icon(KIND_ICON[kind])), h("span", { class: "fm-nm" }, name, badge(e)), h("span", { class: "fm-meta" }, sizeText(e), e.type !== "link" ? `, ${fmtTime(e.mod_time)}` : ""))),
        h("td", { class: "c-size num" }, sizeText(e)),
        h("td", { class: "c-mod" }, h("span", { title: fmtDateTime(e.mod_time) }, fmtTime(e.mod_time))),
        h("td", { class: "c-act" }, more),
      );
    }
    el.addEventListener("click", (ev) => {
      if (ev.shiftKey || ev.ctrlKey || ev.metaKey) return toggle(e.name, ev);
      focusRow(e.name);
      anchor = e.name;
    });
    el.addEventListener("dblclick", () => open(e));
    el.addEventListener("contextmenu", (ev) => {
      ev.preventDefault();
      focusRow(e.name);
      menu(e, { x: ev.clientX, y: ev.clientY });
    });
    return el;
  }

  function sortHead(key, label, cls) {
    const on = sortKey === key;
    const btn = h("button", { type: "button", class: "dt-sort" }, label, icon(on ? (sortOrder === "desc" ? "chevron-down" : "chevron-up") : "chevrons-up-down", "dt-ind"));
    btn.addEventListener("click", () => {
      sortOrder = on && sortOrder === "asc" ? "desc" : on ? "asc" : key === "name" ? "asc" : "desc";
      sortKey = key;
      store(SORT_KEY, `${sortKey}:${sortOrder}`);
      paintBody();
    });
    return h("th", { scope: "col", class: cls, "aria-sort": on ? (sortOrder === "desc" ? "descending" : "ascending") : "none" }, btn);
  }

  function paintBody() {
    for (const b of viewSeg.querySelectorAll("button")) b.setAttribute("aria-pressed", String(b.dataset.view === view));
    if (error) {
      fill(body, emptyState("circle-alert", "Isi folder tidak bisa dimuat", error, h("button", { type: "button", class: "btn", onclick: () => (dir ? go("") : load()) }, icon("rotate-cw"), dir ? `Kembali ke ${page.app.name}` : "Coba lagi")));
      return;
    }
    if (!entries) {
      fill(body, h("div", { class: "fm-skel", "aria-busy": "true", "aria-label": "Memuat isi folder" }, Array.from({ length: 6 }, () => h("div", {}, h("i", { class: "skel" }), h("i", { class: "skel" })))));
      return;
    }
    if (!entries.length) {
      fill(
        body,
        emptyState(
          "folder-open",
          dir ? "Folder ini kosong" : "Belum ada file",
          dir ? "Tarik file ke sini atau pakai Unggah. File yang ditulis aplikasi ke folder ini juga muncul di sini." : `File yang ditulis aplikasi ke ./ atau folder tersimpan muncul di ${page.app.name}/. Tarik file ke sini bila aplikasi membutuhkannya sejak awal.`,
          h("button", { type: "button", class: "btn", onclick: () => fileInput.click() }, icon("upload"), "Unggah file"),
          h("button", { type: "button", class: "btn", onclick: () => newFolder() }, icon("folder-plus"), "Folder baru"),
        ),
      );
      return;
    }
    const list = visible();
    if (!list.length) {
      fill(body, emptyState("search", "Tidak ada yang cocok", `Tidak ada nama berisi "${search.value.trim()}" di folder ini.`, h("button", { type: "button", class: "btn", onclick: () => { search.value = ""; filter = ""; paintBody(); search.focus(); } }, "Hapus pencarian")));
      return;
    }
    if (!list.some((e) => e.name === cursor)) cursor = list[0].name;
    const allOn = list.every((e) => selected.has(e.name));
    const someOn = list.some((e) => selected.has(e.name));
    const all = h("input", { type: "checkbox", checked: allOn, indeterminate: someOn && !allOn, "aria-label": "Pilih semua" });
    all.addEventListener("change", () => {
      selected = all.checked ? new Set(list.map((e) => e.name)) : new Set();
      paintRows();
      paintSel();
    });
    if (view === "grid") {
      fill(
        body,
        h("div", { class: "fm-gridbar" }, h("label", { class: "rowflex" }, all, "Pilih semua"), h("span", { class: "spacer" }), h("span", { class: "hint" }, "Urut:"), sortSelect()),
        h("ul", { class: "fm-grid", role: "listbox", "aria-multiselectable": "true", "aria-label": `Isi ${page.app.name}/${dir}` }, list.map(rowFor)),
      );
    } else {
      fill(
        body,
        h(
          "table",
          { class: "fm-t", role: "grid", "aria-multiselectable": "true", "aria-label": `Isi ${page.app.name}/${dir}` },
          h("thead", {}, h("tr", {}, h("th", { class: "c-chk" }, all), sortHead("name", "Nama", "c-name"), sortHead("size", "Ukuran", "c-size num"), sortHead("mod", "Diubah", "c-mod"), h("th", { class: "c-act" }, h("span", { class: "sr" }, "Aksi")))),
          h("tbody", { id: "filerows" }, list.map(rowFor)),
        ),
      );
    }
    paintSel();
    paintStatus();
  }
  function sortSelect() {
    const sel = h("select", { class: "f fm-sortsel", "aria-label": "Urutkan" }, h("option", { value: "name:asc" }, "Nama A–Z"), h("option", { value: "name:desc" }, "Nama Z–A"), h("option", { value: "mod:desc" }, "Terbaru"), h("option", { value: "mod:asc" }, "Terlama"), h("option", { value: "size:desc" }, "Terbesar"), h("option", { value: "size:asc" }, "Terkecil"));
    sel.value = `${sortKey}:${sortOrder}`;
    sel.addEventListener("change", () => {
      [sortKey, sortOrder] = sel.value.split(":");
      store(SORT_KEY, sel.value);
      paintBody();
    });
    return sel;
  }
  // Repaint rows in place (selection, preview mark) without losing focus.
  function paintRows() {
    const had = document.activeElement?.closest?.("[data-file]")?.dataset.file;
    paintBody();
    if (had) focusRow(had);
  }

  function paintSel() {
    const list = (entries || []).filter((e) => selected.has(e.name));
    selBar.hidden = !list.length;
    if (!list.length) return selBar.replaceChildren();
    const blocked = list.find(locked);
    const size = list.reduce((n, e) => n + (e.type === "file" ? e.size : 0), 0);
    fill(
      selBar,
      h("b", {}, `${fmtNum(list.length)} dipilih`),
      size ? h("span", { class: "hint" }, fmtBytes(size)) : null,
      h("span", { class: "spacer" }),
      h("button", { type: "button", class: "btn sm", id: "fselzip", onclick: () => download(list) }, icon("download"), list.length === 1 && list[0].type === "file" ? "Unduh" : "Unduh zip"),
      h("button", { type: "button", class: "btn sm danger", id: "fseldel", disabled: Boolean(blocked), title: blocked ? `${blocked.name}: ${locked(blocked)}` : null, onclick: () => remove(list) }, icon("trash-2"), "Hapus"),
      h("button", { type: "button", class: "ibtn", "aria-label": "Batal pilih", title: "Batal pilih (Esc)", onclick: () => { selected = new Set(); paintRows(); } }, icon("x")),
    );
  }

  function paintStatus() {
    const app = page.app;
    const m = diskMeter(app);
    const files = (entries || []).filter((e) => e.type === "file");
    const total = files.reduce((n, e) => n + e.size, 0);
    const count = entries ? entries.length : 0;
    const left = entries ? h("span", { class: "fm-count" }, `${fmtNum(count)} item`, files.length ? `, ${fmtBytes(total)} file di folder ini` : "") : h("span", { class: "fm-count" }, "");
    const disk = h(
      "div",
      { class: "fusage fm-disk", "data-level": m.tone || null, id: "fusage", title: app.disk_scanned_at ? `Diukur ${fmtTime(app.disk_scanned_at)}; Runova mengukur ulang tiap 5 menit dan setelah file berubah.` : "Belum diukur" },
      h("span", { class: "fm-dl" }, icon("hard-drive"), "Disk aplikasi"),
      bar(m.percent, { hiAt: 80 }),
      h("span", { class: "fm-dv" }, m.text),
    );
    const tone = app.disk_level === "full"
      ? h("p", { class: "ftone", "data-level": "full" }, icon("circle-alert"), h("span", {}, "Disk penuh. Aplikasi tetap berjalan, tetapi deploy baru dan unggahan ditolak sampai ada file yang dihapus atau ukuran disk dinaikkan di ", h("a", { href: page.path("advanced") }, "Lanjutan"), "."))
      : app.disk_level === "warn"
        ? h("p", { class: "ftone", "data-level": "warn" }, icon("triangle-alert"), h("span", {}, "Disk hampir penuh (80% atau lebih). Hapus file yang tidak dipakai atau naikkan ukuran disk di ", h("a", { href: page.path("advanced") }, "Lanjutan"), "."))
        : null;
    fill(status, h("div", { class: "fm-srow" }, left, disk), tone);
  }

  function render() {
    crumbs();
    upDirBtn.disabled = !dir;
    paintBody();
    paintStatus();
    paintUploads();
  }

  // Esc closes the preview from anywhere in the tab.
  const onKey = (ev) => {
    if (ev.key === "Escape" && previewing && !document.querySelector(".dlg-scrim,.fm-menu")) closePreview(true);
  };
  document.addEventListener("keydown", onKey);

  render();
  load();
  return {
    node,
    sub(s) {
      // The folder is in the query; an old "files/<path>" link is rewritten to it.
      if (s) {
        history.replaceState(history.state, "", filesHash(page.id, s));
        return enter(s);
      }
      enter(hashPath());
    },
    update() {
      // Detail polling refreshes the disk line; the listing reloads on navigation or actions.
      paintStatus();
    },
    cleanup() {
      document.removeEventListener("keydown", onKey);
      menuClose?.();
    },
  };
}
