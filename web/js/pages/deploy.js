import * as api from "/assets/js/api.js";
import { h, icon, fmtBytes, fmtMB, fmtNum } from "/assets/js/dom.js";
import { ctx } from "/assets/js/ctx.js";
import * as session from "/assets/js/session.js";
import { uploader } from "/assets/js/components/uploader.js";
import { envForm } from "/assets/js/components/envform.js";
import { dbPick } from "/assets/js/components/dbpick.js";
import { resourceCards } from "/assets/js/components/resource.js";
import { addressControl } from "/assets/js/components/address.js";
import { appNameError, jvmOptsError, portError, suggestName } from "/assets/js/validate.js";
import { JAVA_VERSIONS, LTS_NOTE, runtimeImage } from "/assets/js/kinds.js";
import { toast } from "/assets/js/toast.js";

const STEPS = [
  ["Unggah", () => "Hasil build"],
  ["Runtime", () => "Versi, port"],
  ["Database & env", () => "PostgreSQL, variabel"],
  ["Resource & akses", () => "Memori, CPU, IP"],
];

export async function renderDeploy(root) {
  const user = session.current();
  const [info, appsData, dbData, hostingData] = await Promise.all([api.get("/api/info"), api.get("/api/apps"), api.get("/api/databases"), api.get("/api/hosting-domains")]);
  const usage = appsData.usage;
  const shell = ctx.shell;

  let step = 1;
  let up = null; // the server's upload result
  let javaVersion = 0;
  let nameTouched = false;
  let createdDB = null; // a database made by a submit that then failed; reused by the retry
  let submitting = false;

  const uploadCtl = uploader({
    limitMB: info.upload_limit_mb,
    onDone: (r) => {
      up = r;
      const allowed = r.allowed_java || [];
      javaVersion = allowed.includes(21) ? 21 : (allowed[0] ?? 0);
      port.value = String(r.report.port || 8080);
      if (!nameTouched) {
        name.value = suggestName(r.report.file_name);
      }
      rebuildStep2();
      rebuildStep3();
      refresh();
    },
    onClear: () => {
      up = null;
      javaVersion = 0;
      rebuildStep2();
      rebuildStep3();
      refresh();
    },
  });

  // Step 1
  const checks = h("div", { class: "checks", hidden: true, "aria-label": "Hasil pemeriksaan file" });
  const s1 = h("div", { class: "wstep", "data-w": "1" }, h("h2", {}, "Unggah hasil build"), h("p", { class: "lead" }, "Runova membaca isi file untuk menentukan runtime, port, dan environment."), uploadCtl.node, checks);

  function paintChecks() {
    if (!up) {
      checks.hidden = true;
      return;
    }
    const r = up.report;
    const rows = [];
    const ok = (children) => rows.push(h("div", {}, icon("circle-check"), h("span", { class: "txt" }, children)));
    const warn = (children) => rows.push(h("div", { class: "w" }, icon("circle-alert"), h("span", { class: "txt" }, children)));
    const bad = (children) => rows.push(h("div", { class: "bad" }, icon("circle-x"), h("span", { class: "txt" }, children)));
    for (const p of r.problems) bad(p);
    if (!r.problems.length || r.kind) {
      if (r.kind === "jar") {
        ok(["JAR", r.main_class ? [", main class ", h("span", { class: "mono" }, r.main_class)] : ""]);
        r.min_java ? ok(`Butuh Java ${r.min_java} atau lebih baru`) : null;
      } else if (r.kind === "static") ok("Binary Linux statis (tanpa interpreter), cocok untuk server ini");
      else if (r.kind === "glibc") ok("Binary Linux dengan glibc, cocok untuk server ini");
      if (r.kind && r.port) ok(["Port ", h("span", { class: "mono" }, String(r.port)), r.port_source === "bawaan" ? " (bawaan, tidak ditemukan di file)" : [" dari ", h("span", { class: "mono" }, r.port_source)]]);
      if (r.kind && r.env.length) {
        const maybe = r.env.filter((e) => e.source === "scan").length;
        warn(`${r.env.length} environment ditemukan${maybe ? `, ${maybe} kemungkinan (dari pemindaian binary)` : ""}`);
      } else if (r.kind) ok("Tidak ada environment yang dirujuk");
    }
    checks.replaceChildren(...rows);
    checks.hidden = false;
  }

  // Step 2
  const port = h("input", { class: "f mono", id: "pt", inputmode: "numeric", style: { "max-width": "140px" }, "aria-describedby": "pt-hint" });
  const jvm = h("input", { class: "f mono", id: "jopts", value: "-XX:MaxRAMPercentage=75", spellcheck: "false", autocomplete: "off" });
  const s2 = h("div", { class: "wstep", "data-w": "2" });
  function rebuildStep2() {
    if (!up) {
      s2.replaceChildren(h("h2", {}, "Runtime"), h("p", { class: "lead" }, "Unggah file dulu di langkah 1."));
      return;
    }
    const r = up.report;
    const kindName = r.kind === "jar" ? "JAR" : r.kind === "static" ? "binary statis" : "binary glibc";
    const parts = [h("h2", {}, "Runtime"), h("p", { class: "lead" }, "Terdeteksi sebagai ", h("b", {}, kindName), r.kind === "jar" ? ". Pilih versi Java yang menjalankan aplikasi ini." : ". Tidak ada versi yang perlu dipilih.")];
    if (r.kind === "jar") {
      const allowed = up.allowed_java || [];
      const reasons = [];
      const opts = JAVA_VERSIONS.map((v) => {
        const disabled = !allowed.includes(v);
        let why = "";
        if (disabled) {
          why = v < r.min_java ? `Butuh Java ${r.min_java} atau lebih baru` : `Java ${v} dinonaktifkan admin di server ini`;
          reasons.push(`Java ${v}: ${why}`);
        }
        const sub = disabled ? "Tidak cocok" : v === allowed[0] ? "minimum" : LTS_NOTE[v];
        return h(
          "button",
          { class: "opt", type: "button", "aria-disabled": disabled ? "true" : null, "aria-pressed": String(!disabled && v === javaVersion), title: why || null, "data-java": v },
          h("b", {}, String(v)),
          h("small", {}, sub),
        );
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
      parts.push(
        h(
          "div",
          { class: "fld" },
          h("label", {}, "Versi Java"),
          list,
          h("div", { class: "hint" }, "Image: ", img),
          reasons.length ? h("ul", { class: "hint why-list" }, reasons.map((x) => h("li", {}, x))) : null,
        ),
        h("div", { class: "fld" }, h("label", { for: "jopts" }, "Opsi JVM ", h("span", { class: "faint", style: { "font-weight": "400" } }, "opsional")), jvm, h("div", { class: "hint" }, "Heap mengikuti batas memori container.")),
      );
    } else {
      parts.push(h("div", { class: "fld" }, h("label", {}, "Image"), h("div", { class: "mono" }, runtimeImage({ kind: r.kind }))));
    }
    parts.push(h("div", { class: "fld" }, h("label", { for: "pt" }, "Port aplikasi"), port, h("div", { class: "hint", id: "pt-hint" }, "Port yang didengarkan aplikasi di dalam container. Runova memetakannya ke port server.")));
    s2.replaceChildren(...parts);
  }

  // Step 3
  const s3 = h("div", { class: "wstep", "data-w": "3" });
  let env = null;
  let pick = null;
  function rebuildStep3() {
    if (!up) {
      env = pick = null;
      s3.replaceChildren(h("h2", {}, "Database & environment"), h("p", { class: "lead" }, "Unggah file dulu di langkah 1."));
      return;
    }
    const keep = pick?.choice();
    pick = dbPick({ databases: dbData.databases, used: dbData.used, limit: dbData.limit, username: user.username, pgDown: dbData.pg_down, onChange: () => { env.setAuto(pick.autoNames()); refresh(); } });
    env = envForm({ detected: up.report.env, onChange: refresh });
    env.setAuto(pick.autoNames());
    void keep;
    s3.replaceChildren(
      h("h2", {}, "Database & environment"),
      h("p", { class: "lead" }, "Pilih database PostgreSQL untuk aplikasi ini, lalu lengkapi variabel lain."),
      pick.node,
      env.node,
    );
  }

  // Step 4
  const ipSw = h("button", { class: "sw", type: "button", role: "switch", "aria-checked": "true", "aria-label": "Akses lewat IP" });
  ipSw.addEventListener("click", () => ipSw.setAttribute("aria-checked", String(ipSw.getAttribute("aria-checked") !== "true")));
  const res = resourceCards({ usage, onChange: refresh });
  const hostLabel = info.public_ip || location.hostname;
  const address = addressControl({ data: hostingData, isAdmin: user.role === "admin", publicIP: info.public_ip, onChange: () => refresh() });
  const s4 = h(
    "div",
    { class: "wstep", "data-w": "4" },
    h("h2", {}, "Resource & akses"),
    h("p", { class: "lead" }, "Diambil dari kuota akunmu. Bisa diubah kapan saja setelah deploy."),
    res.node,
    h(
      "section",
      { class: "card" },
      h(
        "div",
        { class: "bd", style: { "padding-top": "2px", "padding-bottom": "2px" } },
        h("div", { class: "setting" }, h("div", {}, h("b", {}, "Akses lewat IP"), h("div", { class: "d" }, "Buka di ", h("span", { class: "mono" }, `${hostLabel}:<port>`), " untuk pengujian sebelum domain siap")), h("div", { class: "ctl" }, ipSw)),
        h("div", { class: "setting addrset" }, h("div", {}, h("b", {}, "Alamat"), h("div", { class: "d" }, "Opsional, bisa ditambah nanti. Pilih domain hosting dan beri nama, tanpa mengatur DNS.")), h("div", { class: "ctl wide" }, address.node)),
      ),
    ),
  );

  // Summary
  const name = h("input", { class: "f mono", id: "nm", "aria-label": "Nama aplikasi", autocomplete: "off", spellcheck: "false" });
  name.addEventListener("input", () => { nameTouched = true; refresh(); });
  const nameErr = h("div", { class: "errtext", hidden: true });
  const sum = {
    artifact: h("b", { class: "mono" }, "–"),
    runtime: h("b", {}, "–"),
    port: h("b", { class: "mono" }, "–"),
    db: h("b", { class: "mono" }, "–"),
    resource: h("b", {}, "–"),
  };
  const srow = (label, el) => h("div", { class: "srow" }, h("span", {}, label), el);
  const todo = h("div", { class: "todo", "aria-live": "polite" });
  const submitErr = h("div", { class: "err", role: "alert", hidden: true });
  const deployBtn = h("button", { class: "btn pri", id: "deploy", type: "button", style: { "justify-content": "center", height: "34px" }, disabled: true }, icon("rocket"), "Deploy");
  const summary = h(
    "aside",
    { class: "summary", "aria-label": "Ringkasan" },
    h("h3", {}, "Ringkasan"),
    h("div", { class: "fld", style: { margin: "0" } }, h("label", { for: "nm" }, "Nama aplikasi"), name, nameErr, h("div", { class: "hint" }, "Huruf kecil, angka, dan tanda hubung.")),
    h("div", { class: "sep" }),
    srow("Artefak", sum.artifact),
    srow("Runtime", sum.runtime),
    srow("Port", sum.port),
    srow("Database", sum.db),
    srow("Resource", sum.resource),
    h("div", { class: "sep" }),
    todo,
    submitErr,
    deployBtn,
  );

  // Frame
  const mstepTxt = h("span", {});
  const mstepName = h("b", {});
  const mstepFill = h("i");
  const mstep = h("div", { class: "mstep" }, h("div", { class: "t" }, mstepTxt, mstepName), h("div", { class: "bar" }, mstepFill));
  const stepBtns = STEPS.map(([title, sub], i) => {
    const no = h("span", { class: "no" }, String(i + 1));
    const small = h("small", {}, sub());
    const b = h("button", { type: "button", "data-step": i + 1 }, no, h("b", {}, title), small);
    b.addEventListener("click", () => goStep(i + 1));
    return { b, no, small };
  });
  const stepsNav = h("nav", { class: "steps", "aria-label": "Langkah deploy" }, h("h4", {}, "Deploy aplikasi"), stepBtns.map((s) => s.b));
  const prev = h("button", { class: "btn ghost", type: "button" }, icon("arrow-left"), "Kembali");
  const next = h("button", { class: "btn dark", type: "button" }, "Lanjut", icon("arrow-right"));
  const wnav = h("div", { class: "wnav" }, prev, h("div", { class: "r" }, h("a", { class: "btn", href: "#/apps" }, "Batal"), next));
  const wbody = h("div", { class: "wbody" }, mstep, s1, s2, s3, s4, wnav);
  const wiz = h("div", { class: "wiz", "data-at": "1" }, stepsNav, wbody, summary);
  root.append(h("section", { id: "v-new" }, wiz));

  const steps = [s1, s2, s3, s4];

  // Rules
  function problems() {
    const out = [];
    if (!up) return [{ step: 1, text: "Unggah file" }];
    if (up.report.problems.length) out.push({ step: 1, text: "Perbaiki masalah pada file" });
    if (up.report.kind === "jar") {
      if (!javaVersion) out.push({ step: 2, text: "Pilih versi Java" });
      const j = jvmOptsError(jvm.value.trim());
      if (j) out.push({ step: 2, text: j });
    }
    const pe = portError(Number(port.value));
    if (pe) out.push({ step: 2, text: pe });
    if (env && pick) {
      const dbErr = pick.error();
      if (dbErr) out.push({ step: 3, text: dbErr });
      for (const n of env.missing()) out.push({ step: 3, text: `Isi ${n}` });
      const ne = env.nameError();
      if (ne) out.push({ step: 3, text: ne });
    }
    if (!res.fits()) out.push({ step: 4, text: "Kuota memori atau CPU habis" });
    if (usage.apps >= usage.quota.apps) out.push({ step: 4, text: `Kuota aplikasi habis (${usage.apps} dari ${usage.quota.apps})` });
    const addrBad = address.error();
    if (addrBad) out.push({ step: 4, text: addrBad });
    if (address.checking()) out.push({ step: 4, text: "Memeriksa alamat" });
    const nameBad = appNameError(name.value);
    if (nameBad) out.push({ step: 4, text: "Isi nama aplikasi yang valid", field: true });
    return out;
  }

  function refresh() {
    const rep = up?.report;
    sum.artifact.textContent = rep?.file_name || "–";
    sum.runtime.textContent = !rep ? "–" : rep.kind === "jar" ? (javaVersion ? `Java ${javaVersion}` : "–") : rep.kind === "static" ? "Binary statis" : "Binary glibc";
    sum.port.textContent = rep ? `${port.value || "–"} → otomatis` : "–";
    sum.db.textContent = pick ? pick.label() : "–";
    sum.resource.textContent = `${fmtMB(res.memory())} · ${fmtNum(res.cpu() / 1000)} core`;
    stepBtns[0].small.textContent = rep ? rep.file_name : "Hasil build";
    stepBtns[2].small.textContent = pick && env ? `${pick.label()} · ${env.count()} variabel` : "PostgreSQL, variabel";

    const bad = name.value ? appNameError(name.value) : "";
    nameErr.textContent = bad;
    nameErr.hidden = !bad;
    name.setAttribute("aria-invalid", String(Boolean(bad)));

    const list = problems();
    const items = [
      [!!up && !up.report.problems.length, "File valid untuk server ini"],
      [!!up && (up.report.kind !== "jar" || !!javaVersion), "Runtime dipilih"],
      ...(env ? env.missing().map((n) => [false, `Isi ${n}`]) : []),
      [!appNameError(name.value), "Nama aplikasi valid"],
      [res.fits() && usage.apps < usage.quota.apps, "Muat dalam kuota"],
    ];
    todo.replaceChildren(...items.map(([okay, text]) => h("div", { class: okay ? "ok" : "no" }, icon(okay ? "circle-check" : "circle"), text)));

    const complete = list.length === 0;
    deployBtn.disabled = !complete || submitting;
    shell.mbar.deploy.disabled = !complete || submitting;
    next.disabled = step === 1 && (!up || up.report.problems.length > 0);
    shell.mbar.next.disabled = next.disabled;
    paintChecks();
  }

  function goStep(n) {
    if (n > 1 && (!up || up.report.problems.length)) n = 1;
    step = Math.max(1, Math.min(4, n));
    wiz.dataset.at = String(step);
    steps.forEach((s, i) => s.classList.toggle("on", i + 1 === step));
    mstepTxt.textContent = `Langkah ${step} dari 4`;
    mstepName.textContent = STEPS[step - 1][0];
    mstepFill.style.setProperty("width", `${step * 25}%`);
    stepBtns.forEach(({ b, no }, i) => {
      const done = i + 1 < step;
      b.classList.toggle("done", done);
      if (i + 1 === step) b.setAttribute("aria-current", "step");
      else b.removeAttribute("aria-current");
      no.replaceChildren(done ? icon("check") : String(i + 1));
    });
    prev.style.setProperty("visibility", step === 1 ? "hidden" : "visible");
    next.style.setProperty("display", step === 4 ? "none" : "");
    shell.mbar.next.style.setProperty("display", step === 4 ? "none" : "");
    shell.mbar.deploy.style.setProperty("display", step === 4 ? "" : "none");
    shell.mbar.prevText.textContent = step === 1 ? "Batal" : "Kembali";
    refresh();
    ctx.shell.content.scrollTop = 0;
  }

  prev.addEventListener("click", () => goStep(step - 1));
  next.addEventListener("click", () => goStep(step + 1));
  shell.mbar.prev.onclick = () => (step === 1 ? (location.hash = "#/apps") : goStep(step - 1));
  shell.mbar.next.onclick = () => goStep(step + 1);

  async function submit() {
    const list = problems();
    if (list.length || submitting) return;
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
      const addr = address.value();
      if (addr) {
        try {
          await api.post(`/api/apps/${id}/domains`, addr);
        } catch (e) {
          toast(`Aplikasi dibuat, tetapi alamat ditolak: ${e.message}`, "err");
        }
      }
      up = null;
      uploadCtl.release();
      location.hash = `#/apps/${id}/logs`;
    } catch (e) {
      submitErr.replaceChildren(icon("circle-alert"), h("span", {}, e.message));
      submitErr.hidden = false;
      summary.scrollIntoView({ block: "nearest" });
    } finally {
      submitting = false;
      deployBtn.removeAttribute("aria-busy");
      refresh();
    }
  }
  deployBtn.addEventListener("click", submit);
  shell.mbar.deploy.onclick = submit;

  rebuildStep2();
  rebuildStep3();
  goStep(1);

  return () => {
    shell.mbar.prev.onclick = shell.mbar.next.onclick = shell.mbar.deploy.onclick = null;
    shell.mbar.next.style.removeProperty("display");
    shell.mbar.deploy.style.removeProperty("display");
    uploadCtl.discard();
  };
}
