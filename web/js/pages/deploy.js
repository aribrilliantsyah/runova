import * as api from "/assets/js/api.js";
import { h, icon, fmtBytes, fmtMB, fmtNum, fill } from "/assets/js/dom.js";
import { ctx } from "/assets/js/ctx.js";
import * as session from "/assets/js/session.js";
import { uploader } from "/assets/js/components/uploader.js";
import { envForm } from "/assets/js/components/envform.js";
import { dbPick } from "/assets/js/components/dbpick.js";
import { resourceCards } from "/assets/js/components/resource.js";
import { addressControl } from "/assets/js/components/address.js";
import { ipHost } from "/assets/js/components/iphost.js";
import { FOLLOW_KEY } from "/assets/js/components/deploytrace.js";
import { kindLogo } from "/assets/js/kinds.js";
import { appNameError, jvmOptsError, portError, suggestName } from "/assets/js/validate.js";
import { JAVA_VERSIONS, LTS_NOTE, runtimeImage } from "/assets/js/kinds.js";

// Sizes on offer before "Atur sendiri": what most JAR and binary apps need.
const PRESETS = [
  { id: "s", name: "Kecil", mb: 256, milli: 250, note: "Binary kecil, worker" },
  { id: "m", name: "Sedang", mb: 512, milli: 500, note: "Kebanyakan aplikasi Spring" },
  { id: "l", name: "Besar", mb: 1024, milli: 1000, note: "Aplikasi berat, banyak pengguna" },
];

/**
 * The deploy page. Two parts: upload the build (Runova reads it), then one review page with the
 * name on top, what was detected, the optional address, database and environment, the size,
 * and the advanced options folded. Deploy opens the deployment's live progress.
 */
export async function renderDeploy(root) {
  const user = session.current();
  const [info, appsData, dbData, hostingData] = await Promise.all([api.get("/api/info"), api.get("/api/apps"), api.get("/api/databases"), api.get("/api/hosting-domains")]);
  const usage = appsData.usage;
  const shell = ctx.shell;

  let up = null; // the server's upload result
  let javaVersion = 0;
  let nameTouched = false;
  let createdDB = null; // a database made by a submit that then failed; reused by the retry
  let submitting = false;
  let env = null;
  let pick = null;
  let preset = "m";

  const uploadCtl = uploader({
    limitMB: info.upload_limit_mb,
    onDone: (r) => {
      up = r;
      const allowed = r.allowed_java || [];
      javaVersion = allowed.includes(21) ? 21 : (allowed[0] ?? 0);
      port.value = String(r.report.port || 8080);
      if (!nameTouched) name.value = suggestName(r.report.file_name);
      preset = r.report.kind === "jar" ? "m" : "s";
      build();
    },
    onClear: () => {
      up = null;
      javaVersion = 0;
      build();
    },
  });

  // Inputs that outlive a rebuild of the review (a new file keeps what the user typed).
  const name = h("input", { class: "f mono nmbig", id: "nm", "aria-label": "Nama aplikasi", autocomplete: "off", spellcheck: "false", "aria-describedby": "nm-hint nm-err" });
  name.addEventListener("input", () => { nameTouched = true; refresh(); });
  const nameErr = h("div", { class: "errtext", id: "nm-err", hidden: true });
  const port = h("input", { class: "f mono", id: "pt", inputmode: "numeric", "aria-describedby": "pt-hint" });
  port.addEventListener("input", () => refresh());
  const jvm = h("input", { class: "f mono", id: "jopts", value: "-XX:MaxRAMPercentage=75", spellcheck: "false", autocomplete: "off" });
  jvm.addEventListener("input", () => refresh());
  const ipSw = h("button", { class: "sw", type: "button", role: "switch", "aria-checked": "true", "aria-label": "Akses lewat IP" });
  ipSw.addEventListener("click", () => ipSw.setAttribute("aria-checked", String(ipSw.getAttribute("aria-checked") !== "true")));
  const res = resourceCards({ usage, onChange: () => refresh() });
  const address = addressControl({ data: hostingData, isAdmin: user.role === "admin", publicIP: info.public_ip, onChange: () => refresh() });
  const ip = ipHost(info);

  const submitErr = h("div", { class: "err", role: "alert", hidden: true });
  const deployBtn = h("button", { class: "btn pri dbtn", id: "deploy", type: "button", disabled: true }, icon("rocket"), "Deploy");
  const missing = h("ul", { class: "dmiss", "aria-live": "polite" });
  const sumBox = h("dl", { class: "dsum" });

  const intro = h("div", { class: "dintro" }, h("h1", {}, "Deploy aplikasi"), h("p", {}, "Unggah hasil build. Runova membaca isinya: jenis aplikasi, runtime, port, dan variabel yang dibutuhkan."));
  const uploadSec = h("section", { class: "dupload" }, uploadCtl.node);
  const review = h("div", { class: "dreview", hidden: true });
  const rail = h("aside", { class: "drail", "aria-label": "Ringkasan deploy", hidden: true }, h("h3", {}, "Siap deploy?"), sumBox, missing, submitErr, deployBtn, h("a", { class: "btn ghost dcancel", href: "#/apps" }, "Batal"));
  const page = h("section", { id: "v-new", class: "dpage" }, h("div", { class: "dcol" }, intro, uploadSec, review), rail);
  root.append(page);

  const sec = (id, title, sub, ...body) => h("section", { class: "dsec", id }, h("header", {}, h("h2", {}, title), sub ? h("p", {}, sub) : null), ...body);
  const fld = (label, input, hint, forId) => h("div", { class: "fld" }, h("label", forId ? { for: forId } : {}, label), input, hint ? h("div", { class: "hint" }, hint) : null);
  const opt = (text) => h("span", { class: "optl" }, text);

  // What Runova read from the file, editable where it matters.
  function detected() {
    const r = up.report;
    const rows = [];
    const bad = r.problems.map((p) => h("li", { class: "bad" }, icon("circle-x"), h("span", {}, p)));
    if (bad.length) return h("ul", { class: "dchecks" }, bad);
    const kind = r.kind === "jar" ? "Aplikasi Java (JAR)" : r.kind === "static" ? "Binary Linux statis" : "Binary Linux glibc";
    rows.push(h("div", { class: "dk" }, h("span", {}, "Jenis"), h("b", {}, kind, r.main_class ? h("span", { class: "mono faint" }, ` ${r.main_class}`) : null)));
    if (r.kind === "jar") {
      const allowed = up.allowed_java || [];
      const reasons = [];
      const opts = JAVA_VERSIONS.map((v) => {
        const off = !allowed.includes(v);
        let why = "";
        if (off) {
          why = v < r.min_java ? `Butuh Java ${r.min_java} atau lebih baru` : `Java ${v} dinonaktifkan admin di server ini`;
          reasons.push(`Java ${v}: ${why}`);
        }
        return h("button", { class: "opt", type: "button", "aria-disabled": off ? "true" : null, "aria-pressed": String(!off && v === javaVersion), title: why || null, "data-java": v }, h("b", {}, String(v)), h("small", {}, off ? "Tidak cocok" : v === allowed[0] ? "minimum" : LTS_NOTE[v]));
      });
      const img = h("span", { class: "mono" }, runtimeImage({ kind: "jar", java_version: javaVersion }));
      const list = h("div", { class: "opts", id: "javaopts", role: "group", "aria-label": "Versi Java" }, opts);
      list.addEventListener("click", (e) => {
        const b = e.target.closest(".opt");
        if (!b || b.getAttribute("aria-disabled") === "true") return;
        javaVersion = Number(b.dataset.java);
        for (const o of list.children) o.setAttribute("aria-pressed", String(o === b));
        img.textContent = runtimeImage({ kind: "jar", java_version: javaVersion });
        refresh();
      });
      rows.push(h("div", { class: "fld" }, h("label", {}, "Versi Java"), list, h("div", { class: "hint" }, r.min_java ? `Butuh Java ${r.min_java} atau lebih baru. ` : "", "Image: ", img), reasons.length ? h("details", { class: "hint why" }, h("summary", {}, "Kenapa ada versi yang tidak bisa dipilih?"), h("ul", {}, reasons.map((x) => h("li", {}, x)))) : null));
    } else {
      rows.push(h("div", { class: "dk" }, h("span", {}, "Image"), h("b", { class: "mono" }, runtimeImage({ kind: r.kind }))));
    }
    const src = r.port_source === "bawaan" ? "bawaan, tidak ditemukan di file" : `dari ${r.port_source}`;
    rows.push(fld("Port aplikasi", h("div", { class: "portrow" }, port, h("span", { class: "hint", id: "pt-hint" }, `Port yang dibuka aplikasi di dalam container (${src}). Runova mengisi PORT${r.kind === "jar" ? " dan SERVER_PORT" : ""} dengan nilai ini.`)), null, "pt"));
    return h("div", { class: "ddet" }, rows);
  }

  function sizes() {
    const max = (opts) => opts[opts.length - 1] ?? 0;
    const fitsPreset = (p) => max(res.memOpts) >= p.mb && max(res.cpuOpts) >= p.milli;
    if (preset !== "c" && !fitsPreset(PRESETS.find((p) => p.id === preset))) preset = PRESETS.filter(fitsPreset).pop()?.id || "c";
    const custom = h("details", { class: "dcustom", open: preset === "c" }, h("summary", {}, "Atur sendiri"), res.node);
    const btns = PRESETS.map((p) => {
      const ok = fitsPreset(p);
      const b = h("button", { class: "opt preset", type: "button", "aria-pressed": String(preset === p.id), "aria-disabled": ok ? null : "true", title: ok ? null : "Melebihi sisa kuota", "data-size": p.id },
        h("b", {}, p.name), h("small", {}, `${fmtMB(p.mb)} · ${fmtNum(p.milli / 1000)} core`), h("small", { class: "pn" }, ok ? p.note : "Melebihi sisa kuota"));
      b.addEventListener("click", () => {
        if (!ok) return;
        preset = p.id;
        res.set(p.mb, p.milli);
        custom.open = false;
        for (const x of btns) x.setAttribute("aria-pressed", String(x === b));
      });
      return b;
    });
    custom.addEventListener("toggle", () => {
      if (!custom.open) return;
      preset = "c";
      for (const x of btns) x.setAttribute("aria-pressed", "false");
      refresh();
    });
    const cur = PRESETS.find((p) => p.id === preset);
    if (cur) res.set(cur.mb, cur.milli);
    const q = usage.quota;
    return [h("div", { class: "presets", role: "group", "aria-label": "Ukuran" }, btns), h("p", { class: "hint" }, `Kuotamu: sisa ${fmtMB(Math.max(q.memory_mb - usage.memory_mb, 0))} memori dan ${fmtNum(Math.max(q.cpu_milli - usage.cpu_milli, 0) / 1000)} core CPU. Bisa diubah kapan saja setelah deploy.`), custom];
  }

  function advanced() {
    const r = up.report;
    return h(
      "details",
      { class: "dsec dadv", id: "d-adv" },
      h("summary", {}, h("h2", {}, "Lanjutan"), h("span", { class: "hint" }, "Akses IP untuk pengujian", r.kind === "jar" ? ", opsi JVM" : "")),
      h(
        "div",
        { class: "dadv-bd" },
        h("div", { class: "setting" }, h("div", {}, h("b", {}, "Akses lewat IP"), h("div", { class: "d" }, "Buka di ", h("span", { class: "mono" }, `${ip.host}:<port>`), ip.local ? " (jaringan lokal server)" : "", " untuk mencoba sebelum domain siap.")), h("div", { class: "ctl" }, ipSw)),
        r.kind === "jar" ? fld(["Opsi JVM ", opt("opsional")], jvm, "Heap mengikuti batas memori container (MaxRAMPercentage). Kosongkan bila tidak perlu.", "jopts") : null,
        h("p", { class: "hint" }, "Mode deploy tanpa jeda bisa dinyalakan di tab Lanjutan aplikasi setelah deploy pertama."),
      ),
    );
  }

  function build() {
    const has = Boolean(up);
    page.classList.toggle("has", has);
    intro.hidden = has;
    review.hidden = rail.hidden = !has;
    shell.mbar.deploy.style.setProperty("display", has ? "" : "none");
    if (!has) {
      env = pick = null;
      review.replaceChildren();
      refresh();
      return;
    }
    const r = up.report;
    pick = dbPick({ databases: dbData.databases, used: dbData.used, limit: dbData.limit, username: user.username, pgDown: dbData.pg_down, dbEnv: up.db_env || [], onChange: () => { env.setAuto(pick.auto()); refresh(); } });
    env = envForm({ detected: r.env, onChange: () => refresh(), foldOptional: true });
    env.setAuto(pick.auto());
    const usable = !r.problems.length;
    fill(
      review,
      h(
        "section",
        { class: "dsec dname" },
        h("label", { for: "nm" }, "Nama aplikasi"),
        name,
        nameErr,
        h("div", { class: "hint", id: "nm-hint" }, "Huruf kecil, angka, dan tanda hubung. Dipakai di alamat dan di daftar aplikasi."),
      ),
      sec("d-run", "Terdeteksi dari file", usable ? "Periksa dan ubah bila perlu." : "File ini belum bisa dijalankan di server ini.", detected()),
      usable ? [
        sec("d-addr", ["Alamat ", opt("opsional")], "Subdomain siap pakai tanpa mengatur DNS. Bisa ditambah nanti.", address.node),
        sec("d-db", ["Database ", opt("opsional")], "PostgreSQL untuk aplikasi ini. Variabel koneksinya diisi otomatis.", pick.node),
        sec("d-env", "Environment", env.missing().length ? "Isi variabel bertanda * sebelum deploy." : "Variabel yang dibaca aplikasi saat berjalan.", env.node),
        sec("d-size", "Ukuran", "Memori dan CPU dari kuota akunmu.", ...sizes()),
        advanced(),
      ] : null,
    );
    refresh();
  }

  // Rules: every reason Deploy is not ready yet, with the section that fixes it.
  function problems() {
    const out = [];
    if (!up) return [{ at: "", text: "Unggah file" }];
    if (up.report.problems.length) return [{ at: "d-run", text: "File ini tidak bisa dijalankan" }];
    const nameBad = appNameError(name.value);
    if (nameBad) out.push({ at: "nm", text: name.value ? nameBad : "Isi nama aplikasi" });
    if (up.report.kind === "jar") {
      if (!javaVersion) out.push({ at: "d-run", text: "Pilih versi Java" });
      const j = jvmOptsError(jvm.value.trim());
      if (j) out.push({ at: "d-adv", text: j });
    }
    const pe = portError(Number(port.value));
    if (pe) out.push({ at: "d-run", text: pe });
    if (env && pick) {
      const dbErr = pick.error();
      if (dbErr) out.push({ at: "d-db", text: dbErr });
      for (const n of env.missing()) out.push({ at: "d-env", text: `Isi ${n}` });
      const ne = env.nameError();
      if (ne) out.push({ at: "d-env", text: ne });
    }
    if (!res.fits()) out.push({ at: "d-size", text: "Kuota memori atau CPU habis" });
    if (usage.apps >= usage.quota.apps) out.push({ at: "d-size", text: `Kuota aplikasi habis (${usage.apps} dari ${usage.quota.apps})` });
    const addrBad = address.error();
    if (addrBad) out.push({ at: "d-addr", text: addrBad });
    if (address.checking()) out.push({ at: "d-addr", text: "Memeriksa alamat" });
    return out;
  }

  function refresh() {
    const rep = up?.report;
    const bad = name.value ? appNameError(name.value) : "";
    nameErr.textContent = bad;
    nameErr.hidden = !bad;
    name.setAttribute("aria-invalid", String(Boolean(bad)));

    const addr = address.value();
    const runtime = !rep ? "–" : rep.kind === "jar" ? (javaVersion ? `Java ${javaVersion}` : "–") : rep.kind === "static" ? "Binary statis" : "Binary glibc";
    const rowsum = (k, v, cls) => [h("dt", {}, k), h("dd", cls ? { class: cls } : {}, v)];
    fill(
      sumBox,
      rep ? h("div", { class: "dfile" }, h("span", { class: "lgbox" }, kindLogo(rep.kind || "static", rep.language)), h("div", {}, h("b", { class: "mono" }, name.value || "–"), h("small", { class: "mono" }, `${rep.file_name} · ${fmtBytes(rep.size)}`))) : null,
      rowsum("Runtime", runtime),
      rowsum("Port", rep ? port.value || "–" : "–", "mono"),
      rowsum("Alamat", addr ? (addr.host || `${addr.label}.${(hostingData.domains || []).find((d) => d.id === addr.hosting_domain_id)?.name.replace(/^\*\./, "") || ""}`) : "Belum ada", addr ? "mono" : "muted"),
      rowsum("Database", pick ? pick.label() : "–", pick && pick.choice() !== "none" ? "mono" : ""),
      rowsum("Ukuran", `${fmtMB(res.memory())} · ${fmtNum(res.cpu() / 1000)} core`),
    );
    const list = problems();
    fill(missing, list.length ? [h("li", { class: "dmh" }, "Sebelum deploy:"), list.map((p) => h("li", {}, h("button", { type: "button", onclick: () => jump(p.at) }, icon("circle"), p.text)))] : h("li", { class: "dok" }, icon("circle-check"), "Semua siap. Deploy membangun container dan menyalakannya."));
    const ready = list.length === 0 && !submitting;
    deployBtn.disabled = !ready;
    // On phones the bar button stays usable: while something is missing it names it and jumps there.
    shell.mbar.deploy.disabled = submitting;
    shell.mbar.deploy.classList.toggle("pending", list.length > 0);
    const label = submitting ? "Memulai deploy…" : list.length ? `Deploy (${list.length} belum siap)` : "Deploy";
    fill(deployBtn, icon("rocket"), label);
    fill(shell.mbar.deploy, icon(list.length && !submitting ? "circle-alert" : "rocket"), h("span", { class: "mlabel" }, list.length && !submitting ? list[0].text : label));
  }

  function jump(at) {
    const el = at && document.getElementById(at);
    if (!el) return;
    if (el.tagName === "DETAILS") el.open = true;
    el.scrollIntoView({ block: "start", behavior: "smooth" });
    const focus = el.matches("input,select,textarea,button") ? el : el.querySelector("input:not([type=hidden]):not([hidden]),select,textarea");
    setTimeout(() => focus?.focus({ preventScroll: true }), 250);
  }

  async function submit() {
    const list = problems();
    if (list.length || submitting) {
      if (list.length) jump(list[0].at);
      return;
    }
    submitting = true;
    submitErr.hidden = true;
    deployBtn.setAttribute("aria-busy", "true");
    refresh();
    try {
      let databaseId = null;
      if (pick.choice() === "new") {
        createdDB ??= await api.post("/api/databases", { suffix: pick.suffix() });
        databaseId = createdDB.id;
      } else if (pick.choice() !== "none") {
        databaseId = pick.choice();
      }
      const body = {
        upload_id: up.id,
        name: name.value,
        internal_port: Number(port.value),
        memory_mb: res.memory(),
        cpu_milli: res.cpu(),
        ip_access: ipSw.getAttribute("aria-checked") === "true",
        env: env.entries(),
        database_id: databaseId,
      };
      if (up.report.kind === "jar") {
        body.java_version = javaVersion;
        body.jvm_opts = jvm.value.trim();
      }
      const created = await api.post("/api/apps", body);
      const id = created.app.id;
      const addrValue = address.value();
      let addrError = "";
      if (addrValue) {
        try {
          await api.post(`/api/apps/${id}/domains`, addrValue);
        } catch (e) {
          addrError = e.message;
        }
      }
      up = null;
      uploadCtl.release();
      try {
        sessionStorage.setItem(FOLLOW_KEY, JSON.stringify({ id: created.deployment.id, addrError }));
      } catch {
        // storage blocked: the progress page still works, without the jump to Umum
      }
      location.hash = `#/apps/${id}/deployments/${created.deployment.id}`;
    } catch (e) {
      submitErr.replaceChildren(icon("circle-alert"), h("span", {}, e.message));
      submitErr.hidden = false;
      submitErr.scrollIntoView({ block: "nearest" });
    } finally {
      submitting = false;
      deployBtn.removeAttribute("aria-busy");
      refresh();
    }
  }
  deployBtn.addEventListener("click", submit);

  // Phones: the bottom bar carries Batal and Deploy.
  shell.mbar.next.style.setProperty("display", "none");
  shell.mbar.prevText.textContent = "Batal";
  shell.mbar.prev.onclick = () => (location.hash = "#/apps");
  shell.mbar.deploy.onclick = submit;
  build();

  return () => {
    shell.mbar.prev.onclick = shell.mbar.next.onclick = shell.mbar.deploy.onclick = null;
    shell.mbar.next.style.removeProperty("display");
    shell.mbar.deploy.style.removeProperty("display");
    fill(shell.mbar.deploy, icon("rocket"), "Deploy");
    shell.mbar.deploy.classList.remove("pending");
    uploadCtl.discard();
  };
}
