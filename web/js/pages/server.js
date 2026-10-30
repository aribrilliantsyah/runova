import * as api from "/assets/js/api.js";
import { h, icon, fmtMB, fmtNum, pct, bar, clear, poll, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { dialog } from "/assets/js/components/dialog.js";
import { quotaForm } from "/assets/js/components/quotaform.js";
import { RUNTIMES } from "/assets/js/kinds.js";
import { toast } from "/assets/js/toast.js";

const POLL_MS = 15000;

function stat(ic, label, value, of, percent) {
  return h("div", { class: "stat" }, h("div", { class: "l" }, icon(ic), label), h("div", { class: "v" }, value, h("small", {}, of)), bar(percent));
}

function service(ic, name, ok, detail, errText) {
  return h("section", { class: "card" }, h("div", { class: "bd svc" }, h("span", { class: "ic" }, icon(ic)), h("div", {}, h("b", {}, name), h("div", { class: "muted mono", title: ok ? "" : errText }, ok ? detail : errText || "Tidak aktif")), h("span", { class: `chip ${ok ? "ok" : "err"}` }, ok ? "Aktif" : "Mati")));
}

export async function renderServer(root) {
  let st = null;
  let settings = await api.get("/api/settings");
  const view = h("div", { class: "pb" });
  const sub = h("p", {}, "Memuat…");
  root.append(h("section", {}, h("div", { class: "ph" }, h("div", {}, h("h1", {}, "Server"), sub)), view));

  const gb = (mb) => `${fmtNum(mb / 1024)} GB`;

  function paintStatus() {
    const { host, apps, docker, caddy, postgres } = st;
    sub.textContent = `${host.hostname} · ${host.os} · ${host.arch}`;
    return [
      h(
        "div",
        { class: "stats" },
        stat("cpu", "CPU dialokasikan", fmtNum(host.cpu_alloc_milli / 1000), `dari ${host.cpus} core`, pct(host.cpu_alloc_milli, host.cpus * 1000)),
        stat("memory-stick", "Memori dialokasikan", gb(host.mem_alloc_mb), `dari ${gb(host.mem_total_mb)}`, pct(host.mem_alloc_mb, host.mem_total_mb)),
        stat("hard-drive", "Disk", gb(host.disk_used_mb), `dari ${gb(host.disk_total_mb)}`, pct(host.disk_used_mb, host.disk_total_mb)),
        stat("boxes", "Aplikasi", String(apps.total), `${apps.running} berjalan, ${apps.stopped + apps.failed} berhenti`, pct(apps.running, apps.total)),
      ),
      h(
        "div",
        { class: "g3" },
        service("container", "Docker", docker.ok, docker.version, docker.error),
        service("lock", "Caddy", caddy.ok, `${caddy.domains} domain`, caddy.error),
        service("database", "PostgreSQL", postgres.ok, `${postgres.version} · ${postgres.databases} database`, postgres.error),
      ),
    ];
  }

  function field(label, input, hint) {
    return h("div", { class: "fld", style: { margin: "0" } }, h("label", {}, label), input, hint ? h("div", { class: "hint" }, hint) : null);
  }

  function settingsCards() {
    const q = quotaForm(settings.default_quota, { idPrefix: "dq" });
    const saveQuota = h("button", { class: "btn dark", type: "button" }, "Simpan bawaan");
    saveQuota.addEventListener("click", () => save(saveQuota, { default_quota: q.value() }, "Bawaan pengguna baru disimpan"));

    const runtimes = h(
      "div",
      { class: "rt", id: "rtlist" },
      RUNTIMES.map((r) => {
        const sw = h("button", { class: "sw", type: "button", role: "switch", "aria-checked": String(settings.runtimes[r.id] !== false), "aria-label": r.label });
        sw.addEventListener("click", () => {
          const on = sw.getAttribute("aria-checked") !== "true";
          act(sw, () => save(null, { runtimes: { ...settings.runtimes, [r.id]: on } }, `${r.label} ${on ? "diizinkan" : "dimatikan"}`));
        });
        return h("div", { class: "rtitem" }, h("span", { class: "rtlogo" }, icon(r.kind === "jar" ? "coffee" : "binary")), h("div", {}, h("b", {}, r.label), h("small", {}, r.image)), sw);
      }),
    );

    const upload = h("input", { class: "f", type: "number", min: "1", "aria-label": "Batas unggahan (MB)", value: String(settings.upload_limit_mb) });
    const portMin = h("input", { class: "f", type: "number", "aria-label": "Port awal", value: String(settings.port_min) });
    const portMax = h("input", { class: "f", type: "number", "aria-label": "Port akhir", value: String(settings.port_max) });
    const publicIP = h("input", { class: "f mono", "aria-label": "IP publik server", placeholder: "203.0.113.15", value: settings.public_ip, spellcheck: "false" });
    const panelDomain = h("input", { class: "f mono", "aria-label": "Domain panel", placeholder: "panel.perusahaan.co.id", value: settings.panel_domain, spellcheck: "false" });
    const panelPort = h("input", { class: "f", type: "number", "aria-label": "Port panel", placeholder: "kosong = tidak dipakai", value: settings.panel_port || "" });
    const nets = h("textarea", { class: "f", "aria-label": "Jaringan PostgreSQL yang diizinkan", placeholder: "203.0.113.0/24", spellcheck: "false", style: { "min-height": "72px" } });
    nets.value = settings.pg_allowed_nets.join("\n");
    const saveServer = h("button", { class: "btn dark", type: "button" }, "Simpan setelan");
    saveServer.addEventListener("click", () =>
      save(saveServer, {
        upload_limit_mb: Number(upload.value),
        port_min: Number(portMin.value),
        port_max: Number(portMax.value),
        public_ip: publicIP.value.trim(),
        panel_domain: panelDomain.value.trim().toLowerCase(),
        panel_port: Number(panelPort.value) || 0,
        pg_allowed_nets: nets.value.split(/[\s,]+/).filter(Boolean),
      }, "Setelan disimpan"),
    );

    return [
      h("section", { class: "card", style: { "margin-top": "12px" } }, h("header", {}, h("div", {}, h("h3", {}, "Bawaan untuk pengguna baru"), h("p", {}, "Bisa diubah per pengguna lewat tombol Kuota."))), h("div", { class: "bd" }, q.node, h("div", { class: "actions", style: { "margin-top": "12px" } }, saveQuota))),
      h("section", { class: "card", style: { "margin-top": "12px" } }, h("header", {}, h("div", {}, h("h3", {}, "Yang boleh dijalankan"), h("p", {}, "Matikan untuk melarang deploy baru dengan runtime itu."))), h("div", { class: "bd" }, runtimes)),
      h(
        "section",
        { class: "card", style: { "margin-top": "12px" } },
        h("header", {}, h("div", {}, h("h3", {}, "Jaringan dan alamat"), h("p", {}, "Satu kali diatur, berlaku untuk seluruh server."))),
        h(
          "div",
          { class: "bd" },
          h(
            "div",
            { class: "cfgform" },
            field("Batas ukuran unggahan (MB)", upload),
            h("div", { class: "two" }, field("Rentang port akses IP: awal", portMin), field("akhir", portMax)),
            field("IP publik server", publicIP, "Dipakai panduan domain dan detail koneksi database. Kosong: panduan hanya menawarkan Cloudflare Tunnel."),
            field("Domain panel", panelDomain, "HTTPS otomatis lewat Caddy. Mengisi domain dan port sekaligus tidak disarankan."),
            field("Port panel (HTTPS di IP:port)", panelPort),
            field("Jaringan PostgreSQL yang diizinkan", nets, "Satu CIDR per baris, mis. 203.0.113.0/24. Subnet Docker selalu diizinkan."),
            h("div", { class: "actions" }, saveServer),
          ),
        ),
      ),
    ];
  }

  async function save(btn, patch, okMessage) {
    const next = { ...settings, ...patch };
    const run = async () => {
      settings = await api.put("/api/settings", next);
      paint();
    };
    if (!btn) return run().then(() => toast(okMessage));
    return act(btn, run, okMessage);
  }

  function paint() {
    if (!st) return;
    fill(view, ...paintStatus(), ...settingsCards());
  }

  async function refreshStatus() {
    st = await api.get("/api/server");
    sub.textContent = `${st.host.hostname} · ${st.host.os} · ${st.host.arch}`;
    const focus = document.activeElement;
    if (view.contains(focus) && focus.matches("input,textarea,select")) {
      // do not repaint under the cursor; only the status tiles change
      const old = view.querySelector(".stats");
      const [stats, services] = paintStatus();
      old.replaceWith(stats);
      view.querySelector(".g3").replaceWith(services);
      return;
    }
    paint();
  }

  await refreshStatus();
  return poll(refreshStatus, POLL_MS);
}
