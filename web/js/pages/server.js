import * as api from "/assets/js/api.js";
import { h, icon, fmtMB, fmtNum, fmtTime, fmtBytes, pct, bar, clear, poll, fill } from "/assets/js/dom.js";
import { act } from "/assets/js/components/act.js";
import { dialog } from "/assets/js/components/dialog.js";
import { hostingCard } from "/assets/js/components/hostingcard.js";
import { quotaForm } from "/assets/js/components/quotaform.js";
import { RUNTIMES } from "/assets/js/kinds.js";
import { toast } from "/assets/js/toast.js";

const POLL_MS = 15000;

function stat(ic, label, value, of, percent) {
  return h("div", { class: "stat" }, h("div", { class: "l" }, icon(ic), label), h("div", { class: "v" }, value, h("small", {}, of)), bar(percent));
}

function service(ic, name, ok, detail, errText) {
  const text = ok ? detail : errText || "Tidak aktif";
  return h(
    "section",
    { class: "card" },
    h(
      "div",
      { class: "bd svc" },
      h("div", { class: "hd" }, h("span", { class: "ic" }, icon(ic)), h("b", {}, name), h("span", { class: `chip ${ok ? "ok" : "err"}` }, ok ? "Aktif" : "Mati")),
      h("div", { class: "det muted mono", title: text }, text),
    ),
  );
}

export async function renderServer(root) {
  let st = null;
  let upd = null;
  let settings = await api.get("/api/settings");
  const hosting = hostingCard();
  const view = h("div", { class: "pb srv" });
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
        stat("hard-drive", "Disk server", gb(host.disk_used_mb), `dari ${gb(host.disk_total_mb)}; aplikasi ${fmtBytes(host.app_disk_used_bytes || 0)} terpakai dari ${fmtMB(host.app_disk_alloc_mb || 0)} dialokasikan`, pct(host.disk_used_mb, host.disk_total_mb)),
        stat("package", "Aplikasi", String(apps.total), `${apps.running} berjalan, ${apps.stopped + apps.failed} berhenti`, pct(apps.running, apps.total)),
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

  function versionCard() {
    const sw = h("button", { class: "sw", type: "button", role: "switch", "aria-checked": String(settings.update_check !== false), "aria-label": "Periksa pembaruan" });
    sw.addEventListener("click", () => {
      const on = sw.getAttribute("aria-checked") !== "true";
      act(sw, async () => {
        await save(null, { update_check: on }, on ? "Pemeriksaan pembaruan dinyalakan" : "Pemeriksaan pembaruan dimatikan");
        dispatchEvent(new Event("runova:update"));
        await loadUpdate();
        paint();
      });
    });
    const latest = !upd?.enabled ? "Pemeriksaan dimatikan" : upd.latest ? `v${upd.latest}${upd.update_available ? " (ada pembaruan)" : ""}` : "Belum diperiksa";
    const checked = upd?.checked_at ? fmtTime(upd.checked_at) : "-";
    return h(
      "section",
      { class: "card", id: "versioncard" },
      h("header", {}, h("div", {}, h("h3", {}, "Versi Runova"), h("p", {}, "Memeriksa rilis baru tiap 6 jam dan menampilkannya sebagai banner untuk admin."))),
      h(
        "div",
        { class: "bd" },
        h(
          "dl",
          { class: "kv ver" },
          h("dt", {}, "Terpasang"),
          h("dd", { class: "mono", id: "ver-current" }, upd ? `v${upd.current}` : "-"),
          h("dt", {}, "Terbaru"),
          h("dd", { class: "mono", id: "ver-latest" }, latest),
          h("dt", {}, "Terakhir diperiksa"),
          h("dd", { id: "ver-checked" }, checked),
        ),
        h("div", { class: "setting" }, h("div", {}, h("b", {}, "Periksa pembaruan"), h("div", { class: "d" }, "Server menghubungi github.com; matikan bila server tanpa internet.")), h("div", { class: "ctl" }, sw)),
      ),
    );
  }

  async function loadUpdate() {
    try {
      upd = await api.get("/api/update");
    } catch {
      upd = null;
    }
  }

  // The public IP is what DNS points at; app IP ports are linked at the LAN address when
  // the public IP belongs to a proxy or NAT in front of this server.
  function ipHint() {
    const acc = st?.host?.ip_access;
    const base = "Dipakai Cek DNS, panduan domain, dan detail koneksi database. Kosong: panduan hanya menawarkan Cloudflare Tunnel.";
    const lan = st?.host?.local_ip;
    if (!lan || lan === settings.public_ip) return base;
    return [
      base,
      h(
        "span",
        { class: "iplocal" },
        icon("network"),
        "IP lokal terdeteksi ",
        h("b", { class: "mono" }, lan),
        acc?.local ? ". Link akses IP aplikasi memakai alamat ini karena IP publik bukan milik server ini (port 20000 ke atas tidak melewati proxy)." : ".",
      ),
    ];
  }

  function field(label, input, hint) {
    return h("div", { class: "fld" }, h("label", {}, label), input, hint ? h("div", { class: "hint" }, hint) : null);
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
    const pending = h("input", { class: "f", type: "number", min: "1", max: "50", "aria-label": "Unggahan tertunda per pengguna", value: String(settings.max_pending_uploads) });
    const portMin = h("input", { class: "f", type: "number", "aria-label": "Port awal", value: String(settings.port_min) });
    const portMax = h("input", { class: "f", type: "number", "aria-label": "Port akhir", value: String(settings.port_max) });
    const publicIP = h("input", { class: "f mono", "aria-label": "IP publik server", placeholder: "203.0.113.15", value: settings.public_ip, spellcheck: "false" });
    const panelDomain = h("input", { class: "f mono", "aria-label": "Domain panel", placeholder: "panel.perusahaan.co.id", value: settings.panel_domain, spellcheck: "false" });
    const panelPort = h("input", { class: "f", type: "number", "aria-label": "Port panel", placeholder: "kosong = tidak dipakai", value: settings.panel_port || "" });
    const nets = h("textarea", { class: "f", "aria-label": "Jaringan PostgreSQL yang diizinkan", placeholder: "203.0.113.0/24", spellcheck: "false", style: { "min-height": "72px" } });
    nets.value = settings.pg_allowed_nets.join("\n");
    const proxies = h("textarea", { class: "f", "aria-label": "Proxy di depan Runova", placeholder: "10.0.0.5", spellcheck: "false", style: { "min-height": "56px" } });
    proxies.value = settings.trusted_proxies.join("\n");
    const saveServer = h("button", { class: "btn dark", type: "button" }, "Simpan setelan");
    saveServer.addEventListener("click", () =>
      save(saveServer, {
        upload_limit_mb: Number(upload.value),
        max_pending_uploads: Number(pending.value),
        port_min: Number(portMin.value),
        port_max: Number(portMax.value),
        public_ip: publicIP.value.trim(),
        panel_domain: panelDomain.value.trim().toLowerCase(),
        panel_port: Number(panelPort.value) || 0,
        pg_allowed_nets: nets.value.split(/[\s,]+/).filter(Boolean),
        trusted_proxies: proxies.value.split(/[\s,]+/).filter(Boolean),
      }, "Setelan disimpan"),
    );

    q.node.classList.add("two");
    const card = (title, desc, body, foot, attrs) =>
      h("section", { class: "card", ...attrs }, h("header", {}, h("div", {}, h("h3", {}, title), h("p", {}, desc))), h("div", { class: "bd" }, body), foot ? h("footer", {}, foot) : null);

    return [
      h(
        "div",
        { class: "setgrid" },
        h(
          "div",
          { class: "stack" },
          versionCard(),
          card("Bawaan untuk pengguna baru", "Bisa diubah per pengguna lewat tombol Kuota.", q.node, saveQuota),
        ),
        card(
          "Jaringan dan alamat",
          "Satu kali diatur, berlaku untuk seluruh server.",
          h(
            "div",
            { class: "cfgform" },
            h("div", { class: "two" }, field("Batas ukuran unggahan (MB)", upload), field("Unggahan tertunda per pengguna", pending)),
            h("div", { class: "two" }, field("Rentang port akses IP: awal", portMin), field("akhir", portMax)),
            field("IP publik server", publicIP, ipHint()),
            h("div", { class: "two" }, field("Domain panel", panelDomain, "HTTPS otomatis lewat Caddy. Mengisi domain dan port sekaligus tidak disarankan."), field("Port panel (HTTPS di IP:port)", panelPort)),
            field("Jaringan PostgreSQL yang diizinkan", nets, "Satu CIDR per baris, mis. 203.0.113.0/24. Subnet Docker selalu diizinkan."),
            field("Proxy di depan Runova", proxies, "IP atau CIDR proxy seperti nginx yang meneruskan ke server ini, satu per baris. Isi agar batas login dan aktivitas memakai IP asli pengunjung; Caddy lalu memakai sertifikat CA internalnya karena sertifikat publik dipegang proxy. Kosong bila tidak ada proxy."),
          ),
          saveServer,
        ),
      ),
      card("Yang boleh dijalankan", "Matikan untuk melarang deploy baru dengan runtime itu.", runtimes, null, { class: "card rtcard" }),
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
    fill(view, ...paintStatus(), hosting.node, ...settingsCards());
  }

  async function refreshStatus() {
    [st] = await Promise.all([api.get("/api/server"), hosting.reload(), loadUpdate()]);
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

  // The hosting card can set the public IP (DNS suggestion): reload the form with it.
  const onSettings = async () => {
    settings = await api.get("/api/settings");
    await refreshStatus();
  };
  addEventListener("runova:settings", onSettings);
  await refreshStatus();
  const stop = poll(refreshStatus, POLL_MS);
  return () => {
    stop();
    removeEventListener("runova:settings", onSettings);
  };
}
