import * as api from "/assets/js/api.js";
import { h, icon, fill } from "/assets/js/dom.js";
import { guide } from "/assets/js/components/guide.js";
import { hostError, labelError } from "/assets/js/validate.js";

const OWN = "own";
const DEBOUNCE_MS = 300;

/**
 * addressControl({data, isAdmin, publicIP, onChange}) → {node, value(), error(), reset()}
 *
 * data is GET /api/hosting-domains. The user picks a hosting domain and types a label (live
 * availability from the API, nothing is saved); an admin can also pick "Domain sendiri" and type
 * a full host name. value() is null while nothing is typed, {hosting_domain_id, label} for a
 * subdomain, or {host} for an own domain: the body of POST /api/apps/{id}/domains.
 * error() is the first reason the current input cannot be submitted, or "".
 */
export function addressControl({ data, isAdmin, publicIP, onChange = () => {} }) {
  const domains = (data.domains || []).filter((d) => d.enabled !== false);
  const { used, limit } = data.quota;
  const quotaLeft = used < limit;

  const pick = h("select", { class: "f mono", "aria-label": "Domain hosting" }, domains.map((d) => h("option", { value: String(d.id) }, d.name)), isAdmin ? h("option", { value: OWN }, "Domain sendiri…") : null);
  const label = h("input", { class: "f mono alabel", "aria-label": "Nama alamat", placeholder: "nama-aplikasi", autocomplete: "off", spellcheck: "false" });
  const own = h("input", { class: "f mono alabel", "aria-label": "Domain sendiri", placeholder: "billing.perusahaan.co.id", autocomplete: "off", spellcheck: "false", hidden: true });
  const preview = h("div", { class: "apreview", "aria-live": "polite" });
  const state = h("div", { class: "astate", "aria-live": "polite" });
  const quota = h("div", { class: "aquota" }, `${used} dari ${limit} subdomain terpakai`);
  const guideBox = h("div", { hidden: true });
  const none = h("div", { class: "anone" });
  const node = h("div", { class: "addr", "data-addr": "" });

  let availability = { key: "", ok: null, reason: "" };
  let timer = 0;

  const mode = () => (pick.value === OWN ? OWN : "hosting");
  const hosting = () => domains.find((d) => String(d.id) === pick.value);
  const labelText = () => label.value.trim().toLowerCase();
  const ownText = () => own.value.trim().toLowerCase();

  function setState(kind, text, ic) {
    state.className = `astate ${kind}`;
    state.replaceChildren(...(text ? [ic ? icon(ic) : null, text] : []));
  }

  function paintOwn() {
    const host = ownText();
    const bad = host && hostError(host);
    preview.replaceChildren();
    setState(bad ? "err" : "", bad || "", "circle-alert");
    guideBox.replaceChildren();
    guideBox.hidden = !host || Boolean(bad);
    if (!guideBox.hidden) guideBox.append(guide({ host, publicIP }));
  }

  function paintHosting() {
    const d = hosting();
    const text = labelText();
    guideBox.hidden = true;
    preview.replaceChildren(...(text && d ? ["Alamat: ", h("b", {}, `${text}.${d.name}`)] : []));
    if (!text) return setState("", "");
    const bad = labelError(text);
    if (bad) return setState("err", bad, "circle-alert");
    const key = `${d.id}/${text}`;
    if (availability.key === key && availability.ok !== null) {
      return availability.ok ? setState("ok", "Tersedia", "circle-check") : setState("err", availability.reason, "circle-alert");
    }
    setState("", "Memeriksa…", "loader-circle");
    clearTimeout(timer);
    timer = setTimeout(async () => {
      let res;
      try {
        res = await api.get(`/api/hosting-domains/${d.id}/available?label=${encodeURIComponent(text)}`);
      } catch (e) {
        res = { available: false, reason: e.message };
      }
      availability = { key, ok: res.available, reason: res.reason };
      if (hosting()?.id === d.id && labelText() === text) {
        paintHosting();
        onChange();
      }
    }, DEBOUNCE_MS);
  }

  function paint() {
    const isOwn = mode() === OWN;
    label.hidden = isOwn;
    own.hidden = !isOwn;
    quota.hidden = isOwn || !domains.length;
    label.disabled = !isOwn && !quotaLeft;
    if (isOwn) return paintOwn();
    if (!quotaLeft) {
      preview.replaceChildren();
      setState("err", `Kuota subdomain habis (${used} dari ${limit}). Hapus subdomain lain atau minta admin menambah kuota aplikasi.`, "circle-alert");
      return;
    }
    paintHosting();
  }

  if (!domains.length) {
    pick.hidden = !isAdmin;
    if (isAdmin) pick.value = OWN;
    label.hidden = true;
    none.replaceChildren(
      ...(isAdmin
        ? ["Belum ada domain hosting. ", h("a", { href: "#/server" }, "Daftarkan di halaman Server"), " atau pakai domain sendiri."]
        : ["Belum ada domain hosting; hubungi admin."]),
    );
  }

  pick.addEventListener("change", () => {
    paint();
    onChange();
  });
  label.addEventListener("input", () => {
    paint();
    onChange();
  });
  own.addEventListener("input", () => {
    paintOwn();
    onChange();
  });

  fill(node, domains.length || isAdmin ? h("div", { class: "arow" }, pick, label, own) : null, domains.length ? null : none, preview, state, quota, guideBox);
  paint();

  return {
    node,
    value() {
      if (mode() === OWN) return ownText() ? { host: ownText() } : null;
      const d = hosting();
      return d && labelText() ? { hosting_domain_id: d.id, label: labelText() } : null;
    },
    error() {
      if (mode() === OWN) return ownText() && hostError(ownText()) ? "Domain tidak valid" : "";
      if (!labelText()) return "";
      if (!quotaLeft) return "Kuota subdomain habis";
      const bad = labelError(labelText());
      if (bad) return bad;
      const d = hosting();
      const key = `${d.id}/${labelText()}`;
      return availability.key === key && availability.ok === false ? availability.reason : "";
    },
    /** true while the typed label has not been checked yet. */
    checking() {
      if (mode() === OWN || !labelText() || labelError(labelText())) return false;
      return !(availability.key === `${hosting()?.id}/${labelText()}` && availability.ok !== null);
    },
    reset() {
      label.value = own.value = "";
      availability = { key: "", ok: null, reason: "" };
      paint();
    },
  };
}
