import * as api from "/assets/js/api.js";
import { h, icon, fmtBytes } from "/assets/js/dom.js";
import { uploadError } from "/assets/js/validate.js";
import { kindLogo } from "/assets/js/kinds.js";

/**
 * uploader({limitMB, onDone, onClear}) → {node, current, reset}.
 * Drag or pick a file; it uploads with a progress bar and a Batal button. A file
 * over the limit is refused here, before any byte is sent. onDone(result) gets the
 * server's {id, report, allowed_java}; onClear() runs when the file is dropped again.
 */
export function uploader({ limitMB, onDone, onClear }) {
  let result = null;
  let job = null;
  const box = h("div", { class: "upl" });
  const input = h("input", { type: "file", id: "upload-file", "aria-label": "Pilih file untuk diunggah", accept: ".jar,.gz,.tgz,application/java-archive,application/gzip,application/octet-stream" });
  const error = h("div", { class: "err", role: "alert", hidden: true });

  const showError = (msg) => {
    error.replaceChildren(icon("circle-alert"), h("span", {}, msg));
    error.hidden = !msg;
  };

  function idle() {
    const drop = h(
      "label",
      { class: "drop", for: "upload-file" },
      h("span", { class: "ic" }, icon("upload")),
      h("b", {}, "Tarik file ke sini atau klik untuk memilih"),
      h("small", {}, `Maksimal ${limitMB} MB. Server: linux/amd64.`),
      h("div", { class: "accept" }, h("span", { class: "chip" }, icon("coffee"), ".jar"), h("span", { class: "chip" }, icon("binary"), "Binary Linux"), h("span", { class: "chip" }, icon("file-archive"), ".tar.gz berisi binary")),
    );
    for (const ev of ["dragenter", "dragover"]) drop.addEventListener(ev, (e) => { e.preventDefault(); drop.classList.add("over"); });
    for (const ev of ["dragleave", "drop"]) drop.addEventListener(ev, (e) => { e.preventDefault(); drop.classList.remove("over"); });
    drop.addEventListener("drop", (e) => {
      const f = e.dataTransfer?.files?.[0];
      if (f) start(f);
    });
    box.replaceChildren(input, drop, error);
  }

  function start(file) {
    showError("");
    const bad = uploadError(file, limitMB);
    if (bad) {
      showError(bad);
      return;
    }
    const fill = h("i");
    fill.style.setProperty("width", "0%");
    const text = h("span", {}, "0%");
    const cancel = h("button", { class: "btn sm", type: "button" }, icon("x"), "Batal");
    box.replaceChildren(
      h(
        "div",
        { class: "progress", role: "group", "aria-label": "Mengunggah" },
        h("div", { class: "t" }, h("b", { class: "mono" }, file.name), h("span", { class: "muted" }, fmtBytes(file.size)), h("span", { class: "r" }, cancel)),
        h("div", { class: "bar", role: "progressbar", "aria-valuemin": "0", "aria-valuemax": "100", "aria-valuenow": "0" }, fill),
        text,
      ),
      error,
    );
    const barEl = box.querySelector(".bar");
    job = api.upload("/api/uploads", file, (f) => {
      const p = Math.round(f * 100);
      fill.style.setProperty("width", `${p}%`);
      barEl.setAttribute("aria-valuenow", String(p));
      text.textContent = p >= 100 ? "Memeriksa isi file…" : `${p}%`;
    });
    cancel.addEventListener("click", () => job.abort());
    job.promise
      .then((r) => {
        result = r;
        done(file);
        onDone(r);
      })
      .catch((e) => {
        idle();
        if (e.message !== "Unggahan dibatalkan") showError(e.message);
      })
      .finally(() => { job = null; });
  }

  function done(file) {
    const rep = result.report;
    const replace = h("button", { class: "btn sm ghost", type: "button" }, icon("x"), "Ganti file");
    replace.addEventListener("click", async () => {
      const id = result.id;
      result = null;
      idle();
      onClear();
      try {
        await api.del(`/api/uploads/${id}`);
      } catch {
        // the server removes unclaimed uploads after a day
      }
    });
    box.replaceChildren(
      input,
      h(
        "div",
        { class: "fileok" },
        h("span", { class: "lgbox lg" }, kindLogo(rep.kind || "static", rep.language)),
        h("div", {}, h("b", { class: "mono" }, rep.file_name || file.name), h("div", { class: "muted", style: { "font-size": "12px" } }, `${fmtBytes(rep.size || file.size)} · selesai diunggah`)),
        h("div", { class: "r" }, replace),
      ),
    );
  }

  input.addEventListener("change", () => {
    const f = input.files?.[0];
    if (f) start(f);
    input.value = "";
  });
  idle();
  return {
    node: box,
    current: () => result,
    /** The server consumed the upload (an app was created from it); there is nothing left to discard. */
    release() {
      result = null;
    },
    /** Drops an upload that was never used, so the server does not keep it for a day. */
    async discard() {
      job?.abort();
      if (!result) return;
      const id = result.id;
      result = null;
      try {
        await api.del(`/api/uploads/${id}`);
      } catch {
        // already gone
      }
    },
  };
}
