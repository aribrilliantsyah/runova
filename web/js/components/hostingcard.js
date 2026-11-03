import * as api from "/assets/js/api.js";
import { h, icon, row, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { confirm, dialog } from "/assets/js/components/dialog.js";
import { kv, cloud, splitHost } from "/assets/js/components/guide.js";
import { hostingNameError } from "/assets/js/validate.js";
import { toast } from "/assets/js/toast.js";

const HEADS = ["Domain", "Aktif", "DNS", "Dipakai", ""];

function dnsChip(d) {
  if (d.dns_status === "ok") return h("span", { class: "chip ok" }, icon("check"), "DNS benar");
  if (d.dns_status === "error") return h("span", { class: "chip err", title: d.dns_detail }, icon("circle-alert"), "DNS salah");
  return h("span", { class: "chip" }, "Belum dicek");
}

function leBadge(d) {
  if (!d.le_level) return null;
  const crit = d.le_level === "crit";
  return h(
    "div",
    { class: `lebadge ${crit ? "err" : "warn"}`, role: "status" },
    icon("triangle-alert"),
    h("span", {}, `Perkiraan ${d.le_count} sertifikat baru dalam 7 hari untuk domain utama ini (batas Let's Encrypt 50 per minggu). ${crit ? "Sertifikat baru bisa tertunda sampai minggu depan." : "Mendekati batas."} Ini perkiraan Runova, bukan hitungan Let's Encrypt.`),
  );
}

/** The DNS record the admin has to create for a hosting domain, as a copyable box. */
function dnsHint(name, publicIP) {
  const { sub } = splitHost(name);
  const record = sub === "@" ? "*" : `*.${sub}`;
  return h(
    "div",
    { class: "gmethod on", id: "hostdns" },
    h("p", { class: "gintro" }, h("b", {}, "Record DNS yang perlu dibuat"), " di zona domain ini (Cloudflare → DNS → Records → Add record). Satu record untuk semua subdomain; sertifikat HTTPS diterbitkan Caddy per subdomain."),
    h("div", { class: "kvbox" }, kv("Type", "A"), kv("Name", record), kv("IPv4 address", publicIP || "IP publik server (isi di Setelan)"), kv("Proxy status", cloud("grey", "DNS only"), false), kv("TTL", "Auto", false)),
  );
}

/** hostingCard() → {node, reload(), busy()}: the admin's list of hosting domains with its add form. */
export function hostingCard() {
  let state = { domains: [], public_ip: "" };
  const body = h("div", { class: "bd" });
  const node = h(
    "section",
    { class: "card", id: "hostingcard", style: { "margin-top": "12px" } },
    h("header", {}, h("div", {}, h("h3", {}, "Domain hosting"), h("p", {}, "Pengguna memberi aplikasinya alamat <nama>.<domain hosting> tanpa mengurus DNS, sebanyak kuota aplikasinya."))),
    body,
  );

  const name = h("input", { class: "f mono", id: "hostname", placeholder: "porto.perusahaan.co.id", "aria-label": "Domain hosting baru", autocomplete: "off", spellcheck: "false" });
  const reserved = h("input", { class: "f mono", id: "hostreserved", placeholder: "blog, shop", "aria-label": "Nama cadangan tambahan", autocomplete: "off", spellcheck: "false" });
  const err = h("div", { class: "errtext", hidden: true });
  const hint = h("div", { hidden: true });
  const addBtn = h("button", { class: "btn pri", id: "hostadd", type: "button" }, icon("plus"), "Daftarkan");

  const typed = () => name.value.trim().toLowerCase().replace(/^\*\./, "");
  name.addEventListener("input", () => {
    const v = name.value.trim();
    const bad = v && hostingNameError(v);
    err.textContent = bad || "";
    err.hidden = !bad;
    hint.replaceChildren();
    hint.hidden = !v || Boolean(bad);
    if (!hint.hidden) hint.append(dnsHint(typed(), state.public_ip));
  });
  addBtn.addEventListener("click", async () => {
    const bad = hostingNameError(name.value);
    err.textContent = bad;
    err.hidden = !bad;
    if (bad) return;
    const added = await act(addBtn, async () => {
      const d = await api.post("/api/hosting-domains", { name: name.value.trim(), reserved: reserved.value.trim() });
      name.value = reserved.value = "";
      hint.hidden = true;
      await reload();
      toast(d.dns_status === "ok" ? `${d.name} terdaftar, DNS sudah mengarah ke server` : `${d.name} terdaftar, tetapi DNS belum benar: ${d.dns_detail}`, d.dns_status === "ok" ? undefined : "err");
    });
    void added;
  });
  name.addEventListener("keydown", (e) => e.key === "Enter" && addBtn.click());

  function rowFor(d) {
    const sw = h("button", { class: "sw", type: "button", role: "switch", "aria-checked": String(d.enabled), "aria-label": `${d.enabled ? "Matikan" : "Nyalakan"} ${d.name}` });
    sw.addEventListener("click", () => {
      const on = !d.enabled;
      const apply = () => api.patch(`/api/hosting-domains/${d.id}`, { enabled: on }).then(() => reload()).then(() => toast(`${d.name} ${on ? "dinyalakan" : "dimatikan"}`));
      if (on || d.used === 0) return act(sw, apply);
      return confirm({ title: `Matikan ${d.name}?`, message: `${d.used} subdomain berhenti dilayani dan pemiliknya diberi tahu. Subdomain tetap tercatat dan kembali aktif saat domain dinyalakan.`, submit: "Matikan", danger: true, onSubmit: apply });
    });
    const check = h("button", { class: "btn sm", type: "button", "aria-label": `Cek DNS ${d.name}`, title: "Cek DNS", onclick: (e) => act(e.currentTarget, async () => { await api.post(`/api/hosting-domains/${d.id}/check`); await reload(); }) }, icon("rotate-cw"));
    const edit = h("button", { class: "btn sm ghost", type: "button", "aria-label": `Nama cadangan ${d.name}`, onclick: () => reservedDialog(d) }, icon("pencil"));
    const del = h("button", { class: "btn sm ghost", type: "button", "aria-label": `Hapus ${d.name}`, onclick: () => confirm({ title: "Hapus domain hosting?", message: `${d.name} tidak lagi bisa dipilih pengguna.`, submit: "Hapus", danger: true, onSubmit: async () => { await api.del(`/api/hosting-domains/${d.id}`); await reload(); toast("Domain hosting dihapus"); } }) }, icon("trash-2"));
    const dns = h("td", {}, dnsChip(d), d.dns_status === "error" ? h("div", { class: "muted dnsdetail" }, d.dns_detail) : null);
    return row(HEADS, [h("td", { class: "mono" }, d.name, d.reserved ? h("div", { class: "faint resv" }, `cadangan: ${d.reserved}`) : null), h("td", {}, sw), dns, h("td", {}, String(d.used)), h("td", { class: "num nowrap" }, check, " ", edit, " ", del)], { "data-hosting": d.name });
  }

  function reservedDialog(d) {
    const input = h("input", { class: "f mono", "aria-label": "Nama cadangan", value: d.reserved, placeholder: "blog, shop", spellcheck: "false", autocomplete: "off" });
    dialog({
      title: `Nama cadangan ${d.name}`,
      body: [h("p", {}, "Nama yang tidak boleh dipakai pengguna, dipisah koma. Nama bawaan (www, api, mail, admin, dan lainnya) selalu dicadangkan."), h("div", { class: "fld" }, input)],
      onSubmit: async () => {
        await api.patch(`/api/hosting-domains/${d.id}`, { reserved: input.value });
        await reload();
        toast("Nama cadangan disimpan");
      },
    });
  }

  function paint() {
    const list = state.domains;
    const table = list.length
      ? h("table", { class: "t" }, h("thead", {}, h("tr", {}, HEADS.map((t, i) => h("th", { class: i === 4 ? "num" : "" }, t)))), h("tbody", { id: "hostrows" }, list.map(rowFor)))
      : h("div", { class: "empty" }, h("span", { class: "ic" }, icon("globe")), h("b", {}, "Belum ada domain hosting"), h("span", {}, "Daftarkan satu domain agar pengguna bisa memilihnya saat membuat aplikasi."));
    fill(
      body,
      table,
      list.map(leBadge),
      h("div", { class: "hostform" }, h("div", { class: "rowflex" }, name, reserved, addBtn), err, h("div", { class: "hint" }, "Boleh ditulis *.porto.perusahaan.co.id. Nama cadangan tambahan opsional.")),
      hint,
    );
  }

  async function reload() {
    state = await api.get("/api/hosting-domains");
    if (!node.contains(document.activeElement) || !document.activeElement.matches("input")) paint();
    else {
      const rows = body.querySelector("#hostrows");
      rows?.replaceChildren(...state.domains.map(rowFor));
    }
  }
  return { node, reload };
}
