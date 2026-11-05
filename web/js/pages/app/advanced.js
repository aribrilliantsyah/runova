import * as api from "/assets/js/api.js";
import { h, icon, clear, fill, fmtMB, fmtNum } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { dialog } from "/assets/js/components/dialog.js";
import { resourceCards } from "/assets/js/components/resource.js";
import { folderList } from "/assets/js/components/folders.js";
import { jvmOptsError, portError } from "/assets/js/validate.js";
import { JAVA_VERSIONS } from "/assets/js/kinds.js";
import { markRedeploy, redeployBanner } from "/assets/js/pages/app/redeploy.js";
import { toast } from "/assets/js/toast.js";
import { go } from "/assets/js/router.js";

/** Advanced tab: deploy mode, resources, saved folders, runtime, port, bound database, delete. */
export function renderAdvanced(page) {
  const node = h("div");
  let usage = null;
  let dbs = null;

  async function patch(body, okMessage) {
    const r = await api.patch(`/api/apps/${page.id}`, body);
    if (r.redeploy_required) markRedeploy(page);
    toast(okMessage + (r.redeploy_required ? ". Deploy ulang untuk menerapkan." : ""));
    await page.reload();
    render();
  }

  // card(title, desc, body, {id, aside, foot}): the Lanjutan card. Title and description stack on the
  // left, aside (a secondary action) sits right in the header, foot holds the save action at the right.
  function card(title, desc, body, { id = null, aside = null, foot = null } = {}) {
    return h(
      "section",
      { class: "card", id },
      h("header", {}, h("div", { class: "hd" }, h("h3", {}, title), desc ? h("p", {}, desc) : null), aside ? h("div", { class: "r" }, aside) : null),
      body == null ? null : h("div", { class: "bd" }, body),
      foot ? h("footer", { class: "cfoot" }, foot) : null,
    );
  }

  function modeCard() {
    const app = page.app;
    const sel = h("select", { class: "f", "aria-label": "Mode deploy", }, h("option", { value: "zero" }, "Tanpa jeda: container baru sehat dulu, lalu Caddy dialihkan"), h("option", { value: "stopstart" }, "Stop-start: container lama dihentikan dulu"));
    sel.value = app.deploy_mode;
    sel.addEventListener("change", () => act(sel, () => patch({ deploy_mode: sel.value }, "Mode deploy disimpan")));
    return card("Mode deploy", "Berlaku untuk deploy berikutnya; tersimpan begitu dipilih.", h("div", { class: "fld", style: { margin: "0" } }, h("label", {}, "Cara deploy versi baru"), sel));
  }

  function resourceCard() {
    const app = page.app;
    const res = resourceCards({ usage, current: { memory_mb: app.memory_mb, cpu_milli: app.cpu_milli, disk_mb: app.disk_mb }, memory: app.memory_mb, cpu: app.cpu_milli, disk: app.disk_mb || 256 });
    const save = h("button", { class: "btn dark", type: "button", disabled: !res.fits() });
    save.append("Simpan resource");
    save.addEventListener("click", () => act(save, () => patch({ memory_mb: res.memory(), cpu_milli: res.cpu(), disk_mb: res.disk() }, "Resource disimpan")));
    const disk = app.disk_mb ? fmtMB(app.disk_mb) : "disk belum dibatasi";
    return card("Resource", `Sekarang ${fmtMB(app.memory_mb)}, ${fmtNum(app.cpu_milli / 1000)} core, ${disk}. Disk berlaku langsung; memori dan CPU setelah deploy ulang.`, res.node, { foot: [h("span", { class: "spacer" }), save] });
  }

  function foldersCard() {
    const app = page.app;
    const detected = page.state.latest?.report?.folders || page.state.deployment?.report?.folders || [];
    const save = h("button", { class: "btn dark", type: "button", disabled: true }, "Simpan folder");
    const dirty = h("span", { class: "dirty", hidden: true }, h("i", { class: "dot warn", "aria-hidden": "true" }), "Belum disimpan");
    const errHint = h("span", { class: "hint", hidden: true });
    let list = null;
    const paint = () => {
      if (!list) return;
      const changed = list.changed();
      const bad = list.error();
      save.disabled = Boolean(bad) || !changed;
      dirty.hidden = !changed;
      errHint.hidden = !changed || !bad;
      errHint.textContent = bad ? "Perbaiki baris yang ditandai dulu" : "";
    };
    list = folderList({ appName: () => app.name, detected, folders: app.folders || [], onChange: paint });
    paint();
    save.addEventListener("click", () => act(save, () => patch({ folders: list.value() }, "Folder tersimpan diubah")));
    return card(
      "Folder tersimpan",
      ["Isi ", h("span", { class: "mono" }, `${app.name}/`), " tetap ada saat deploy ulang dan bisa dibuka di tab ", h("a", { href: page.path("files") }, "File"), ". Path absolut di-mount dari folder kerja. Berlaku setelah deploy ulang."],
      list.node,
      { id: "a-folders", aside: list.addButton, foot: [dirty, errHint, h("span", { class: "spacer" }), save] },
    );
  }

  function runtimeCard() {
    const app = page.app;
    const port = h("input", { class: "f mono", "aria-label": "Port aplikasi", inputmode: "numeric", value: String(app.internal_port), style: { "max-width": "140px" } });
    const parts = [];
    let java, jvm;
    if (app.kind === "jar") {
      const min = page.state.deployment?.report?.min_java || 0;
      java = h("select", { class: "f", "aria-label": "Versi Java" }, JAVA_VERSIONS.map((v) => h("option", { value: v, disabled: v < min || page.info.runtimes[`java${v}`] === false }, `Java ${v}${v < min ? ` (butuh ${min} atau lebih baru)` : ""}`)));
      java.value = String(app.java_version);
      jvm = h("input", { class: "f mono", "aria-label": "Opsi JVM", value: app.jvm_opts, spellcheck: "false" });
      parts.push(h("div", { class: "fld" }, h("label", {}, "Versi Java"), java), h("div", { class: "fld" }, h("label", {}, "Opsi JVM"), jvm));
    }
    parts.push(h("div", { class: "fld" }, h("label", {}, "Port aplikasi (di dalam container)"), port, h("div", { class: "hint" }, `Port yang didengarkan aplikasi di dalam container. Runova mengisi PORT${app.kind === "jar" ? " dan SERVER_PORT" : ""} dengan nilai ini; setiap deploy ulang memakai nilai ini. Bukan port akses IP${app.ip_access ? ` (${app.ip_port})` : ""}.`)));
    const save = h("button", { class: "btn dark", type: "button" }, "Simpan runtime");
    save.addEventListener("click", () => {
      const body = { internal_port: Number(port.value) };
      const bad = portError(body.internal_port) || (jvm ? jvmOptsError(jvm.value.trim()) : "");
      if (bad) return toast(bad, "err");
      if (java) {
        body.java_version = Number(java.value);
        body.jvm_opts = jvm.value.trim();
      }
      return act(save, () => patch(body, "Runtime disimpan"));
    });
    return card("Runtime", "Perubahan berlaku setelah deploy ulang.", parts, { foot: [h("span", { class: "spacer" }), save] });
  }

  function dbCard() {
    const app = page.app;
    const sel = h("select", { class: "f", "aria-label": "Database yang dipakai" }, h("option", { value: "0" }, "Tanpa database"), dbs.databases.map((d) => h("option", { value: d.id }, d.name)));
    sel.value = String(app.database_id || 0);
    const save = h("button", { class: "btn dark", type: "button" }, "Simpan database");
    save.addEventListener("click", () => act(save, () => patch({ database_id: Number(sel.value) }, "Database disimpan")));
    return card("Database terpakai", "DATABASE_URL, variabel PG*, dan variabel database yang dikenali di aplikasi (DB_URL, DB_PASSWORD…) diisi otomatis, termasuk password. Berlaku setelah deploy ulang.", [dbs.pg_down ? h("p", { class: "hint", style: { "margin-top": "0" } }, "PostgreSQL sedang tidak bisa diakses; pilihan tetap bisa disimpan.") : null, h("div", { class: "fld", style: { margin: "0" } }, sel)], { foot: [h("span", { class: "spacer" }), save] });
  }

  function dangerCard() {
    const app = page.app;
    const btn = h("button", { class: "btn danger", type: "button" }, icon("trash-2"), "Hapus aplikasi");
    btn.addEventListener("click", () =>
      dialog({
        title: "Hapus aplikasi",
        submit: "Hapus aplikasi",
        danger: true,
        confirmWord: app.name,
        body: [h("p", {}, `Container, semua versi yang tersimpan, folder kerja ${app.name}/ beserta semua filenya, dan domainnya ikut dihapus. Database PostgreSQL tidak dihapus.`)],
        onSubmit: async () => {
          await api.del(`/api/apps/${page.id}`, { confirm: app.name });
          toast(`${app.name} dihapus`);
          go("/apps");
        },
      }),
    );
    return card("Hapus aplikasi", "Penghapusan tidak bisa dibatalkan. Ketik nama aplikasi untuk mengonfirmasi.", null, { foot: [h("span", { class: "spacer" }), btn] });
  }

  function render() {
    clear(node);
    if (!usage || !dbs) return fill(node, h("div", { class: "loading" }, "Memuat…"));
    fill(node, redeployBanner(page), h("div", { class: "stack" }, modeCard(), resourceCard(), foldersCard(), runtimeCard(), dbCard(), dangerCard()));
  }

  render();
  Promise.all([api.get("/api/apps"), api.get("/api/databases")])
    .then(([a, d]) => {
      usage = a.usage;
      dbs = d;
      render();
    })
    .catch((e) => toast(e.message, "err"));
  return { node };
}
