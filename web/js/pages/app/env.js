import * as api from "/assets/js/api.js";
import { h, icon, clear, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { dialog } from "/assets/js/components/dialog.js";
import { envError, envNameError, isSecretName, parseDotenv } from "/assets/js/validate.js";
import { markRedeploy, redeployBanner } from "/assets/js/pages/app/redeploy.js";
import { toast } from "/assets/js/toast.js";

/**
 * The environment tab. Secrets come from the API blanked; "Lihat" fetches one value through the
 * reveal endpoint (which the server records). Edits are collected in a draft and sent together with
 * "Terapkan"; a secret whose value is left empty keeps what is stored.
 */
export function renderEnv(page) {
  const node = h("div");
  const base = page.state.env.map((e) => ({ name: e.name, value: e.value, secret: e.secret }));
  let draft = base.map((e) => ({ ...e, state: "same", revealed: false }));
  let showAll = false;
  let bannerShown = false;

  const changes = () => draft.filter((d) => d.state !== "same").length + base.filter((b) => !draft.some((d) => d.name === b.name)).length;

  function entriesToSend() {
    return draft.filter((d) => d.state !== "removed").map((d) => ({ name: d.name, value: d.value, secret: d.secret }));
  }

  async function reveal(d, btn) {
    if (d.revealed) {
      d.revealed = false;
      d.value = d.secret && d.state === "same" ? "" : d.value;
      return render();
    }
    if (d.state !== "same") {
      d.revealed = true;
      return render();
    }
    await act(btn, async () => {
      const r = await api.post(`/api/apps/${page.id}/env/${encodeURIComponent(d.name)}/reveal`);
      d.value = r.value;
      d.revealed = true;
      render();
    });
  }

  function valueCell(d) {
    const masked = d.secret && !d.revealed;
    return h("span", { class: `mono v ${masked ? "faint" : ""}`, title: masked ? null : d.value }, masked ? "••••••••••••" : d.value);
  }

  function rowFor(d) {
    const edit = h("button", { class: "ibtn", type: "button", "aria-label": `Ubah ${d.name}`, onclick: () => editRow(d) }, icon("pencil"));
    const del = h("button", { class: "ibtn", type: "button", "aria-label": `Hapus ${d.name}`, onclick: () => { d.state = "removed"; render(); } }, icon("trash-2"));
    const undo = h("button", { class: "ibtn", type: "button", "aria-label": `Batalkan hapus ${d.name}`, onclick: () => { d.state = "same"; render(); } }, icon("rotate-cw"));
    const eye = d.secret ? h("button", { class: "ibtn", type: "button", "aria-label": d.revealed ? `Sembunyikan ${d.name}` : `Lihat ${d.name}`, "aria-pressed": String(d.revealed), onclick: (e) => reveal(d, e.currentTarget) }, icon(d.revealed ? "eye-off" : "eye")) : null;
    return h(
      "div",
      { class: `envrow ${d.state === "removed" ? "removed" : ""}`, "data-env": d.name },
      h("span", { class: "k" }, d.name, d.secret ? icon("key-round") : null, d.state === "new" ? h("span", { class: "chip warn" }, "baru") : d.state === "changed" ? h("span", { class: "chip warn" }, "diubah") : d.state === "removed" ? h("span", { class: "chip warn" }, "dihapus") : null),
      valueCell(d),
      h("span", { class: "acts" }, d.state === "removed" ? undo : [eye, edit, del]),
    );
  }

  function editRow(d) {
    const isNew = !d;
    const name = h("input", { class: "f mono", "aria-label": "Nama variabel", value: d?.name || "", disabled: !isNew, placeholder: "NAMA_VARIABEL", autocomplete: "off", spellcheck: "false" });
    const value = h("input", { class: "f mono", "aria-label": "Nilai", value: d && d.state !== "same" || (d && !d.secret) ? d.value : "", type: "text", autocomplete: "off", spellcheck: "false", placeholder: d?.secret && d.state === "same" ? "Kosongkan untuk mempertahankan nilai tersimpan" : "" });
    const secretBox = h("input", { type: "checkbox", id: "env-secret", checked: d ? d.secret : false });
    if (d?.secret) secretBox.disabled = true;
    dialog({
      title: isNew ? "Tambah variabel" : `Ubah ${d.name}`,
      submit: "Simpan",
      body: [
        h("div", { class: "fld" }, h("label", {}, "Nama"), name),
        h("div", { class: "fld" }, h("label", {}, "Nilai"), value),
        isNew ? h("label", { class: "rowflex" }, secretBox, "Rahasia (disimpan terenkripsi, disamarkan)") : null,
      ],
      onSubmit: async () => {
        if (isNew) {
          const nm = name.value.trim();
          const bad = envNameError(nm);
          if (bad) throw new Error(bad);
          if (draft.some((x) => x.name === nm)) throw new Error(`Variabel ${nm} sudah ada`);
          draft.push({ name: nm, value: value.value, secret: secretBox.checked || isSecretName(nm), state: "new", revealed: false });
        } else if (d.secret && d.state === "same" && value.value === "") {
          return;
        } else {
          d.value = value.value;
          if (d.state === "same") d.state = "changed";
          d.revealed = d.secret ? d.revealed : false;
        }
        render();
      },
    });
  }

  function pasteDotenv() {
    const ta = h("textarea", { class: "f", "aria-label": "Isi .env", spellcheck: "false" });
    ta.value = draft.filter((d) => d.state !== "removed" && !d.secret).map((d) => `${d.name}=${d.value}`).join("\n");
    dialog({
      title: "Edit sebagai .env",
      submit: "Terapkan ke draf",
      body: [h("p", {}, "Nilai rahasia tidak ditampilkan di sini. Baris baru atau yang diubah dimasukkan ke draf; variabel yang tidak ada lagi di daftar ini dihapus, kecuali rahasia."), ta],
      onSubmit: async () => {
        const parsed = parseDotenv(ta.value);
        const bad = parsed.map((p) => envNameError(p.name)).find(Boolean);
        if (bad) throw new Error(bad);
        const names = new Set(parsed.map((p) => p.name));
        for (const d of draft) if (!d.secret && !names.has(d.name) && d.state !== "removed") d.state = "removed";
        for (const p of parsed) {
          const cur = draft.find((d) => d.name === p.name);
          if (cur) {
            if (cur.value !== p.value || cur.state === "removed") {
              cur.value = p.value;
              cur.state = cur.state === "new" ? "new" : "changed";
            }
          } else draft.push({ name: p.name, value: p.value, secret: p.secret, state: "new", revealed: false });
        }
        render();
      },
    });
  }

  async function apply(btn) {
    const entries = entriesToSend();
    const bad = envError(entries.map((e) => ({ name: e.name, value: e.value })));
    if (bad) return toast(bad, "err");
    await act(btn, async () => {
      const r = await api.put(`/api/apps/${page.id}/env`, entries);
      if (r.redeploy_required) markRedeploy(page);
      await page.reload();
      base.length = 0;
      page.state.env.forEach((e) => base.push({ name: e.name, value: e.value, secret: e.secret }));
      draft = base.map((e) => ({ ...e, state: "same", revealed: false }));
      render();
    }, "Environment disimpan");
  }

  function render() {
    const n = changes();
    const apply_ = h("button", { class: "btn sm pri", type: "button", onclick: (e) => apply(e.currentTarget) }, "Simpan perubahan");
    const cancel = h("button", { class: "btn sm", type: "button", onclick: () => { draft = base.map((e) => ({ ...e, state: "same", revealed: false })); render(); } }, "Batalkan");
    const dirty = n > 0 ? h("div", { class: "banner warn", role: "status" }, icon("triangle-alert"), h("span", {}, `${n} perubahan belum disimpan.`), h("div", { class: "r" }, cancel, apply_)) : null;
    const visible = draft;
    const toggleAll = h("button", { class: "btn sm ghost", type: "button", onclick: async () => {
      showAll = !showAll;
      if (showAll) {
        for (const d of visible.filter((x) => x.secret && x.state === "same" && !x.revealed)) {
          try {
            d.value = (await api.post(`/api/apps/${page.id}/env/${encodeURIComponent(d.name)}/reveal`)).value;
            d.revealed = true;
          } catch (e) {
            toast(e.message, "err");
          }
        }
      } else {
        for (const d of visible.filter((x) => x.secret && x.revealed && x.state === "same")) {
          d.revealed = false;
          d.value = "";
        }
      }
      render();
    } }, icon(showAll ? "eye-off" : "eye"), showAll ? "Sembunyikan nilai" : "Tampilkan nilai");
    const head = h("div", { class: "envhead" }, h("span", {}, `${visible.filter((d) => d.state !== "removed").length} variabel`), h("div", { class: "r" }, visible.some((d) => d.secret) ? toggleAll : null, h("button", { class: "btn sm ghost", type: "button", onclick: pasteDotenv }, icon("file-code-2"), "Edit sebagai .env"), h("button", { class: "btn sm", type: "button", onclick: () => editRow(null) }, icon("plus"), "Tambah")));
    const list = visible.length ? visible.map(rowFor) : [h("div", { class: "envedit-note" }, "Belum ada variabel. Tambah dengan tombol di atas.")];
    const banner = redeployBanner(page);
    bannerShown = Boolean(banner);
    fill(node, banner, dirty, h("section", { class: "card" }, head, h("div", { id: "envlist" }, list)));
  }

  render();
  // The 5 s poll only matters here when the "deploy ulang" banner should appear or go away;
  // repainting anything else would drop keyboard focus.
  return { node, update: () => (bannerShown !== Boolean(redeployBanner(page)) ? render() : undefined) };
}
