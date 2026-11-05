import * as api from "/assets/js/api.js";
import { h, icon, fill, fmtBytes, fmtTime, fmtDateTime, pct, bar, fmtMB } from "/assets/js/dom.js";
import { confirm } from "/assets/js/components/dialog.js";
import { emptyState } from "/assets/js/components/list.js";
import { uploadError } from "/assets/js/validate.js";
import { toast } from "/assets/js/toast.js";

const join = (dir, name) => (dir ? `${dir}/${name}` : name);

/** diskMeter(app) → the workspace's use against its disk size, as the Umum tab and this tab show it. */
export function diskMeter(app) {
  const used = app.disk_used_bytes || 0;
  const size = (app.disk_mb || 0) * 1048576;
  const p = size ? pct(used, size) : 0;
  const text = size ? `${fmtBytes(used)} / ${fmtMB(app.disk_mb)}` : `${fmtBytes(used)}, belum dibatasi`;
  const tone = app.disk_level === "full" ? "err" : app.disk_level === "warn" ? "warn" : "";
  return { text, percent: p, tone };
}

/**
 * Files tab: browse the app's workspace (shown as "<app>/…"), download, delete and upload small
 * files. The path lives in the tab's sub-path so the browser's back button walks folders.
 */
export function renderFiles(page) {
  const node = h("div");
  let dir = page.sub || "";
  let entries = null;
  let error = "";
  let req = 0;

  const input = h("input", { type: "file", hidden: true, "aria-hidden": "true" });
  const upBtn = h("button", { class: "btn", type: "button", id: "fupload" }, icon("upload"), "Unggah file");
  upBtn.addEventListener("click", () => input.click());
  input.addEventListener("change", () => {
    const f = input.files?.[0];
    input.value = "";
    if (f) upload(f);
  });
  const progress = h("div", { class: "fprog", hidden: true, role: "status", "aria-live": "polite" });

  async function load() {
    const my = ++req;
    try {
      const r = await api.get(`/api/apps/${page.id}/files?path=${encodeURIComponent(dir)}`);
      if (my !== req) return;
      entries = r.entries;
      error = "";
    } catch (e) {
      if (my !== req) return;
      entries = null;
      error = e.message;
    }
    render();
  }

  function go(path) {
    location.hash = page.path(path ? `files/${encodeURIComponent(path)}` : "files");
  }

  async function upload(file) {
    const bad = uploadError(file, page.info.upload_limit_mb);
    if (bad) return toast(bad, "err");
    progress.hidden = false;
    progress.textContent = `Mengunggah ${file.name}…`;
    upBtn.disabled = true;
    try {
      await api.upload(`/api/apps/${page.id}/files?path=${encodeURIComponent(dir)}`, file, (p) => (progress.textContent = `Mengunggah ${file.name}… ${Math.round(p * 100)}%`)).promise;
      toast(`${file.name} diunggah`);
      await page.reload();
      await load();
    } catch (e) {
      toast(e.message, "err");
    } finally {
      progress.hidden = true;
      upBtn.disabled = false;
    }
  }

  function remove(e) {
    const path = join(dir, e.name);
    const what = e.type === "dir" ? `folder ${page.app.name}/${path} beserta seluruh isinya` : `${page.app.name}/${path}`;
    confirm({
      title: e.type === "dir" ? "Hapus folder?" : "Hapus file?",
      message: `Hapus ${what}? File yang dihapus tidak bisa dikembalikan. Aplikasi yang sedang berjalan bisa saja masih membutuhkannya.`,
      submit: "Hapus",
      danger: true,
      onSubmit: async () => {
        await api.del(`/api/apps/${page.id}/files`, { path });
        toast(`${e.name} dihapus`);
        await page.reload();
        await load();
      },
    });
  }

  function crumbs() {
    const parts = dir ? dir.split("/") : [];
    const items = [h("a", { href: page.path("files"), class: "mono" }, `${page.app.name}/`)];
    parts.forEach((p, i) => {
      const to = parts.slice(0, i + 1).join("/");
      items.push(i === parts.length - 1 ? h("span", { class: "mono", "aria-current": "page" }, `${p}/`) : h("a", { href: page.path(`files/${encodeURIComponent(to)}`), class: "mono" }, `${p}/`));
    });
    return h("nav", { class: "fcrumbs", "aria-label": "Lokasi folder" }, items);
  }

  function rowFor(e) {
    const path = join(dir, e.name);
    const ic = e.type === "dir" ? "folder" : e.type === "link" ? "link" : "file";
    const nameCell = e.type === "dir"
      ? h("a", { href: page.path(`files/${encodeURIComponent(path)}`), class: "fname" }, icon(ic), h("span", { class: "mono" }, e.name), e.saved ? h("span", { class: "chip ok", title: "Folder tersimpan: isinya tetap ada saat deploy ulang" }, "tersimpan") : null)
      : h("span", { class: "fname" }, icon(ic), h("span", { class: "mono" }, e.name), e.type === "link" ? h("span", { class: "chip", title: "Link tidak diikuti Runova" }, "link") : null);
    const acts = [];
    if (e.type === "file") acts.push(h("a", { class: "ibtn", href: `/api/apps/${page.id}/files/download?path=${encodeURIComponent(path)}`, download: e.name, "aria-label": `Unduh ${e.name}`, title: "Unduh" }, icon("download")));
    if (!e.saved) acts.push(h("button", { class: "ibtn", type: "button", "aria-label": `Hapus ${e.name}`, title: "Hapus", onclick: () => remove(e) }, icon("trash-2")));
    return h(
      "tr",
      { "data-file": e.name },
      h("td", {}, nameCell),
      h("td", { class: "num" }, e.type === "file" ? fmtBytes(e.size) : ""),
      h("td", { class: "c-date" }, h("span", { title: fmtDateTime(e.mod_time) }, fmtTime(e.mod_time))),
      h("td", { class: "num nowrap" }, h("span", { class: "uacts" }, acts)),
    );
  }

  function listing() {
    if (error) return emptyState("circle-alert", "Isi folder tidak bisa dimuat", error, h("button", { type: "button", class: "btn", onclick: () => (dir ? go("") : load()) }, icon("rotate-cw"), dir ? "Kembali ke awal" : "Coba lagi"));
    if (!entries) return h("div", { class: "loading" }, "Memuat isi folder…");
    if (!entries.length) {
      return emptyState("folder-open", dir ? "Folder ini kosong" : "Belum ada file",
        dir ? "File yang ditulis aplikasi ke folder ini muncul di sini." : `File yang ditulis aplikasi ke ./ atau folder tersimpan muncul di ${page.app.name}/. Unggah file bila aplikasi membutuhkannya sejak awal.`);
    }
    const up = dir ? h("tr", {}, h("td", { colspan: "4" }, h("a", { href: page.path(dir.includes("/") ? `files/${encodeURIComponent(dir.slice(0, dir.lastIndexOf("/")))}` : "files"), class: "fname" }, icon("arrow-left"), "Folder di atasnya"))) : null;
    return h(
      "table",
      { class: "t ftable", "aria-label": `Isi ${page.app.name}/${dir}` },
      h("thead", {}, h("tr", {}, h("th", { scope: "col" }, "Nama"), h("th", { scope: "col", class: "num" }, "Ukuran"), h("th", { scope: "col" }, "Diubah"), h("th", { scope: "col" }, h("span", { class: "sr" }, "Aksi")))),
      h("tbody", { id: "filerows" }, up, entries.map(rowFor)),
    );
  }

  function usage() {
    const app = page.app;
    const m = diskMeter(app);
    const full = app.disk_level === "full";
    const warn = app.disk_level === "warn";
    return h(
      "div",
      { class: `fusage ${m.tone}`.trim(), id: "fusage" },
      h("div", { class: "t" }, h("b", {}, icon("hard-drive"), "Disk aplikasi"), h("span", {}, m.text)),
      bar(m.percent, { hiAt: 80 }),
      full ? h("p", { class: "ftone err" }, "Disk penuh. Aplikasi tetap berjalan, tetapi deploy baru ditolak sampai ada file yang dihapus atau ukuran disk dinaikkan di tab Lanjutan.")
        : warn ? h("p", { class: "ftone warn" }, "Disk hampir penuh. Hapus file yang tidak dipakai atau naikkan ukuran disk di tab Lanjutan.")
        : h("p", { class: "hint" }, app.disk_scanned_at ? `Diukur ${fmtTime(app.disk_scanned_at)}; Runova mengukur ulang tiap 5 menit.` : "Belum diukur; Runova mengukur tiap 5 menit."),
    );
  }

  function render() {
    fill(
      node,
      h(
        "section",
        { class: "card" },
        h("header", {}, h("h3", {}, "File"), h("p", {}, "Folder kerja aplikasi di server. Disimpan saat deploy ulang, tidak ikut backup PostgreSQL."), h("div", { class: "r" }, upBtn, input)),
        h("div", { class: "bd fhead" }, crumbs(), progress),
        listing(),
      ),
      h("section", { class: "card" }, h("div", { class: "bd" }, usage())),
    );
  }

  render();
  load();
  return {
    node,
    sub(s) {
      dir = s || "";
      entries = null;
      render();
      load();
    },
    update() {
      // Detail polling refreshes the usage line; the listing reloads on navigation or actions.
      const u = node.querySelector("#fusage");
      u?.replaceWith(usage());
    },
  };
}
