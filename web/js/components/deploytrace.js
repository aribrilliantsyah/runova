// What happened to a deployment: its steps, the problem that stopped it, and the app's state
// in words. Shared by the app header, the Umum tab, the Deployment tab and the deploy progress.
import { h, icon, fmtDateTime } from "/assets/js/dom.js";

/** sessionStorage key: the deployment the deploy page just started, followed to its end. */
export const FOLLOW_KEY = "runova.follow";

/** Step IDs of internal/apps/trace.go → what the user reads. */
export const STEP_LABELS = {
  artifact: "Unggahan diperiksa",
  image: "Menyiapkan image runtime",
  prepare: "Menyiapkan jaringan, folder data, dan port",
  stop_old: "Menghentikan versi lama",
  create: "Membuat container",
  start: "Menjalankan container",
  health: "Menunggu aplikasi siap",
  route: "Mengaktifkan dan mengalihkan lalu lintas",
  retire: "Menghentikan versi lama",
};

const STEP_ICON = { done: "circle-check", failed: "circle-x", running: "loader-circle", skipped: "circle-dashed", pending: "circle" };
const STEP_WORD = { done: "selesai", failed: "gagal", running: "berjalan", skipped: "dilewati", pending: "menunggu" };

const dur = (ms) => {
  if (!(ms >= 0)) return "";
  if (ms < 1000) return `${Math.max(ms, 1)} md`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0).replace(".", ",")} dtk`;
  return `${Math.floor(s / 60)} mnt ${Math.round(s % 60)} dtk`;
};

/** currentStep(d) → the label of the step a deployment is on, or "". */
export function currentStep(d) {
  const st = d?.steps?.find((s) => s.status === "running") || d?.steps?.find((s) => s.status === "pending");
  return st ? STEP_LABELS[st.id] || st.id : "";
}

/**
 * appState(state) → {cls, label, line, deployment} describes the app for its header:
 * the badge, one sentence of cause, and the deployment it is about (for "Lihat detail").
 * state is GET /api/apps/{id}: {app, deployment, latest, busy}.
 */
export function appState({ app, deployment, latest, busy }) {
  const inProgress = latest && (latest.status === "running" || latest.status === "pending") && (app.status === "deploying" || busy);
  if (inProgress) {
    const step = currentStep(latest);
    return { cls: "warn", label: "Men-deploy", line: step ? `v${latest.number}: ${step}` : `v${latest.number} sedang disiapkan`, deployment: latest, live: true };
  }
  const lastFailed = latest && latest.status === "failed" ? latest : null;
  switch (app.status) {
    case "running":
      if (lastFailed && deployment && lastFailed.id !== deployment.id) {
        return { cls: "ok", label: "Berjalan", warn: true, line: `Deploy v${lastFailed.number} gagal: ${lastFailed.problem?.title || "lihat detail"}. v${deployment.number} tetap berjalan.`, deployment: lastFailed };
      }
      return { cls: "ok", label: "Berjalan", line: "", deployment };
    case "starting":
      return { cls: "warn", label: "Memulai…", line: `Menunggu aplikasi membuka port ${app.internal_port}${app.kind === "jar" ? "; aplikasi Java bisa butuh beberapa menit" : ""}.`, deployment, live: true };
    case "failed": {
      const crash = deployment?.problem;
      if (crash && (crash.kind === "crash_loop" || crash.kind === "container_lost" || crash.kind === "exited" || crash.kind === "health_timeout")) {
        const prefix = crash.kind === "crash_loop" ? "Crash berulang dan dihentikan" : "";
        return { cls: "err", label: "Gagal", line: prefix ? `${prefix}: ${crash.title}` : crash.title, deployment };
      }
      if (lastFailed) return { cls: "err", label: "Gagal", line: `Deploy v${lastFailed.number} gagal: ${lastFailed.problem?.title || firstLine(lastFailed.failure_log)}`, deployment: lastFailed };
      return { cls: "err", label: "Gagal", line: deployment ? "Container tidak berjalan; deploy ulang." : "Belum ada deployment yang berhasil.", deployment: deployment || latest };
    }
    case "deploying":
      return { cls: "warn", label: "Men-deploy", line: latest ? `v${latest.number} sedang disiapkan` : "", deployment: latest, live: true };
    default: {
      const p = deployment?.problem;
      const line = p?.kind === "stopped_user" ? p.title : p?.kind === "exited_clean" ? p.title : "";
      return { cls: "", label: "Berhenti", line, deployment };
    }
  }
}

const firstLine = (s) => (s || "").split("\n")[0] || "lihat detail";

/** problemCard(problem, {failureLog}) → the plain explanation of a failure: title, hint, technical detail. */
export function problemCard(p, { failureLog = "", tone = "err" } = {}) {
  if (!p && !failureLog) return null;
  const title = p?.title || "Deploy gagal";
  const detail = p?.detail || firstLine(failureLog);
  const det = detail ? h("details", { class: "pc-tech" }, h("summary", {}, "Detail teknis"), h("pre", {}, detail, p?.exit_code != null ? `\nexit code ${p.exit_code}` : "")) : null;
  return h(
    "section",
    { class: `pcard ${tone}`, role: "status" },
    h("span", { class: "pc-ic" }, icon(tone === "warn" ? "triangle-alert" : "circle-x")),
    h(
      "div",
      { class: "pc-main" },
      h("h3", {}, title),
      p?.step ? h("p", { class: "pc-step" }, `Berhenti di langkah: ${STEP_LABELS[p.step] || p.step}`) : null,
      p?.hint ? h("p", { class: "pc-hint" }, p.hint) : null,
      det,
    ),
  );
}

/** timeline(d) → the ordered list of a deployment's steps with status, duration and detail. */
export function timeline(d) {
  const steps = d?.steps || [];
  if (!steps.length) {
    return h("p", { class: "muted tl-none" }, d?.status === "pending" ? "Menunggu giliran deploy…" : "Deployment ini dibuat sebelum Runova mencatat langkah deploy.");
  }
  const now = Date.now();
  return h(
    "ol",
    { class: "tl", "aria-label": "Langkah deploy" },
    steps.map((s) => {
      // The upload was checked before the deployment existed: no duration of its own.
      const t = s.id === "artifact" ? null : s.status === "running" ? now - s.started_at : s.finished_at && s.started_at ? s.finished_at - s.started_at : null;
      return h(
        "li",
        { class: `tl-${s.status}`, "data-step": s.id },
        h("span", { class: "tl-ic", title: STEP_WORD[s.status] }, icon(STEP_ICON[s.status] || "circle", s.status === "running" ? "spin" : "")),
        h(
          "div",
          { class: "tl-main" },
          h("div", { class: "tl-h" }, h("b", {}, STEP_LABELS[s.id] || s.id), h("span", { class: "tl-t" }, s.status === "skipped" || s.status === "pending" ? STEP_WORD[s.status] : t != null ? dur(t) : "")),
          s.detail ? h("div", { class: "tl-d" }, s.detail) : null,
        ),
      );
    }),
  );
}

/** deployChip(d, activeId) → the small status chip of one deployment. */
export function deployChip(d, activeId) {
  if (d.id === activeId) return h("span", { class: "chip ok" }, h("span", { class: "dot ok" }), "Aktif");
  switch (d.status) {
    case "failed":
      return h("span", { class: "chip err" }, h("span", { class: "dot err" }), "Gagal");
    case "pending":
    case "running":
      return h("span", { class: "chip warn" }, h("span", { class: "dot warn" }), "Berjalan");
    case "superseded":
      return h("span", { class: "chip" }, "Digantikan");
    default:
      return h("span", { class: "chip" }, d.status);
  }
}

export const deployWhen = (d) => fmtDateTime(d.created_at);
