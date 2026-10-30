import { h, icon, copyText } from "/assets/js/dom.js";

// Second-level public suffixes common in Indonesia and a few other places. A host under one of
// these has its registrable domain one label further left. Anything else is split after two labels.
const SLD = new Set(["co.id", "ac.id", "go.id", "or.id", "web.id", "my.id", "biz.id", "sch.id", "net.id", "co.uk", "org.uk", "com.au", "co.jp", "com.sg", "com.my"]);

/** splitHost("billing.perusahaan.co.id") → {sub: "billing", dom: "perusahaan.co.id"}; the apex gives sub "@". */
export function splitHost(host) {
  const parts = host.split(".");
  const n = SLD.has(parts.slice(-2).join(".")) ? 3 : 2;
  return { sub: parts.length > n ? parts.slice(0, -n).join(".") : "@", dom: parts.slice(-n).join(".") };
}

function cp(value) {
  const b = h("button", { class: "cp", type: "button", "aria-label": `Salin ${value}` }, icon("copy"));
  b.addEventListener("click", async () => {
    if (await copyText(value)) {
      b.replaceChildren(icon("check"));
      setTimeout(() => b.replaceChildren(icon("copy")), 1200);
    }
  });
  return b;
}

const kv = (label, value, copyable = true) =>
  h("div", {}, h("span", {}, label), h("code", {}, value), copyable ? cp(typeof value === "string" ? value : value.textContent) : h("span"));
const cloud = (kind, text) => h("span", { class: `cloud ${kind}` }, h("i"), text);
const step = (title, where, extra) => h("li", {}, h("div", {}, h("b", {}, title), where ? h("span", { class: "where" }, where) : null, extra));
const note = (ic, ...children) => h("div", { class: "gnote" }, icon(ic), h("span", {}, ...children));

/**
 * guide({host, publicIP, methods, onCheck, status}) → the three-way domain guide, filled in for host.
 * With no public IP only Cloudflare Tunnel is offered (the server has no address to point DNS at).
 * onCheck() runs the "Periksa koneksi" button; status is {kind, text} for the chip beside it.
 */
export function guide({ host, publicIP, methods, onCheck, status }) {
  const { sub, dom } = splitHost(host);
  const offered = methods || (publicIP ? ["dns", "proxy", "tunnel"] : ["tunnel"]);
  const labels = { dns: ["DNS langsung", "DNS langsung"], proxy: ["Proxy", "Proxy Cloudflare"], tunnel: ["Tunnel", "Cloudflare Tunnel"] };
  let active = offered[0];

  const body = {
    dns: h(
      "div",
      { class: "gmethod" },
      h("p", { class: "gintro" }, h("b", {}, "Paling sederhana."), " Server punya IP publik dan port 80 serta 443 bisa dibuka dari internet. Sertifikat HTTPS diterbitkan Caddy di server Runova."),
      h(
        "ol",
        { class: "gsteps" },
        step(
          "Buat DNS record",
          "Cloudflare → pilih domain → DNS → Records → Add record",
          h("div", { class: "kvbox" }, kv("Type", "A"), kv("Name", sub), kv("IPv4 address", publicIP || "–"), kv("Proxy status", cloud("grey", "DNS only"), false), kv("TTL", "Auto", false)),
        ),
        step("Simpan, lalu tekan Periksa di bawah", "Biasanya aktif dalam 1 sampai 5 menit. Begitu DNS benar, Caddy menerbitkan sertifikat sendiri."),
      ),
      note("info", h("b", {}, publicIP || "IP publik"), " adalah IP publik server Runova. Admin mengaturnya sekali di halaman Server."),
    ),
    proxy: h(
      "div",
      { class: "gmethod" },
      h("p", { class: "gintro" }, h("b", {}, "Lewat proxy Cloudflare"), " (awan oranye). IP server tersembunyi dan dapat perlindungan DDoS. Port 80 dan 443 tetap harus terbuka untuk Cloudflare."),
      h(
        "ol",
        { class: "gsteps" },
        step(
          "Buat DNS record, mula-mula tanpa proxy",
          "Cloudflare → DNS → Records → Add record",
          h("div", { class: "kvbox" }, kv("Type", "A"), kv("Name", sub), kv("IPv4 address", publicIP || "–"), (() => { const row = kv("Proxy status", h("span", {}, cloud("grey", "DNS only"), " dulu"), false); return row; })()),
        ),
        step('Tunggu sampai Runova menampilkan "HTTPS aktif"', "Caddy perlu menerbitkan sertifikat pertama selagi server bisa diakses langsung."),
        step("Nyalakan proxy", h("span", {}, "Edit record yang sama → Proxy status → ", cloud("orange", "Proxied"))),
        step("Atur mode SSL", "Cloudflare → SSL/TLS → Overview → Configure", h("div", { class: "kvbox" }, kv("Encryption mode", "Full (strict)", false))),
      ),
      note("triangle-alert", "Jangan pakai mode ", h("b", {}, "Flexible"), ", karena akan terjadi redirect berulang. Untuk perpanjangan sertifikat, Caddy butuh jalur ", h("b", { class: "mono" }, "/.well-known/acme-challenge/"), " tetap bisa diakses lewat HTTP. Kalau domain ini pakai aturan redirect ke HTTPS di Cloudflare, kecualikan jalur itu."),
    ),
    tunnel: h(
      "div",
      { class: "gmethod" },
      h("p", { class: "gintro" }, h("b", {}, "Untuk server tanpa IP publik"), " atau tanpa port terbuka. Server membuka koneksi keluar ke Cloudflare, dan HTTPS diurus Cloudflare. Runova melayani domain ini lewat HTTP biasa."),
      h(
        "ol",
        { class: "gsteps" },
        step("Buat tunnel sekali untuk server ini", "Cloudflare → Networking → Tunnels → Create a tunnel. Jalankan perintah instalasinya di server Runova. Tunggu status Healthy."),
        step(
          "Tambah route untuk aplikasi ini",
          "Pilih tunnel → tab Routes → Add route → Published application",
          h("div", { class: "kvbox" }, kv("Subdomain", sub === "@" ? "" : sub), kv("Domain", dom), kv("Path", h("span", { class: "faint" }, "kosongkan"), false), kv("Service URL", "http://localhost:80")),
        ),
        step("Simpan, lalu tekan Periksa di bawah", "Cloudflare membuat DNS record-nya otomatis."),
      ),
      note("info", "Isi ", h("b", {}, "Path"), " hanya kalau aplikasi memang melayani prefix itu sendiri, misalnya ", h("b", { class: "mono" }, "/api"), ". Cloudflare meneruskan path apa adanya, tidak dipotong. ", h("b", {}, "localhost:80"), " adalah Caddy di server Runova, yang lalu meneruskan ke aplikasi berdasarkan nama domain."),
    ),
  };

  const buttons = offered.map((m) => {
    const b = h("button", { type: "button", "aria-pressed": String(m === active), "data-gm": m }, h("span", { class: "short" }, labels[m][0]), h("span", { class: "long" }, labels[m][1]));
    b.addEventListener("click", () => {
      active = m;
      paint();
    });
    return b;
  });
  const paint = () => {
    buttons.forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.gm === active)));
    for (const m of Object.keys(body)) body[m].classList.toggle("on", m === active);
  };

  const parts = [
    h("header", {}, h("h3", {}, icon("route"), "Panduan menghubungkan domain"), h("div", { class: "seg", role: "group", "aria-label": "Cara menghubungkan" }, buttons)),
    h("div", { class: "gbody" }, ...offered.map((m) => body[m]), onCheck ? check(onCheck, status) : null),
  ];
  paint();
  return h("section", { class: "guide" }, ...parts);
}

function check(onCheck, status) {
  const chip = h("span", { class: `chip ${status?.kind || ""}` }, icon(status?.kind === "ok" ? "check" : "loader-circle"), h("span", {}, status?.text || ""));
  const btn = h("button", { class: "btn sm", type: "button" }, icon("rotate-cw"), "Periksa koneksi");
  btn.addEventListener("click", async () => {
    btn.setAttribute("aria-busy", "true");
    btn.disabled = true;
    try {
      await onCheck();
    } finally {
      btn.disabled = false;
      btn.removeAttribute("aria-busy");
    }
  });
  return h("div", { class: "gcheck" }, btn, status ? chip : null);
}
