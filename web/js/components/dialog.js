import { h, icon } from "/assets/js/dom.js";
import { ApiError } from "/assets/js/api.js";

const openDialogs = new Set();

/** closeAllDialogs() dismisses every open dialog (their promises resolve to undefined). Used when the session ends. */
export function closeAllDialogs() {
  for (const close of [...openDialogs]) close(undefined);
}

/**
 * dialog({title, body, submit, cancel, danger, confirmWord, onSubmit, wide}) opens a modal.
 * body is a Node or an array of Nodes. onSubmit may be async; if it throws, the message is
 * shown in the dialog and it stays open. The promise resolves to onSubmit's result, or
 * undefined when the user cancels. confirmWord makes the submit button wait for that exact text.
 */
export function dialog({ title, body = [], submit = "Simpan", cancel = "Batal", danger = false, confirmWord = null, onSubmit = async () => true, hideCancel = false }) {
  return new Promise((resolve) => {
    const opener = document.activeElement;
    const errBox = h("div", { class: "err", hidden: true, role: "alert" });
    const submitBtn = h("button", { class: `btn ${danger ? "pri" : "dark"}`, type: "submit" }, submit);
    const cancelBtn = h("button", { class: "btn", type: "button" }, cancel);
    let confirmInput = null;
    const content = [].concat(body);
    if (confirmWord) {
      confirmInput = h("input", { class: "f mono", "aria-label": `Ketik ${confirmWord} untuk konfirmasi`, autocomplete: "off", spellcheck: "false", placeholder: confirmWord });
      submitBtn.disabled = true;
      confirmInput.addEventListener("input", () => {
        submitBtn.disabled = confirmInput.value !== confirmWord;
      });
      content.push(h("div", { class: "fld" }, h("label", {}, "Ketik ", h("b", { class: "mono" }, confirmWord), " untuk mengonfirmasi"), confirmInput));
    }
    const titleId = `dlg-${Math.random().toString(36).slice(2, 8)}`;
    const form = h(
      "form",
      { class: "dlg", role: "dialog", "aria-modal": "true", "aria-labelledby": titleId, novalidate: true },
      h("header", {}, h("h2", { id: titleId }, title)),
      h("div", { class: "bd" }, content, errBox),
      h("footer", {}, hideCancel ? null : cancelBtn, submitBtn),
    );
    const scrim = h("div", { class: "dlg-scrim" }, form);
    const close = (value) => {
      openDialogs.delete(close);
      scrim.remove();
      removeEventListener("keydown", onKey, true);
      opener?.focus?.();
      resolve(value);
    };
    const onKey = (e) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        close(undefined);
      } else if (e.key === "Tab") {
        const f = [...form.querySelectorAll("a[href],button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled)")];
        if (!f.length) return;
        const first = f[0];
        const last = f[f.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    cancelBtn.addEventListener("click", () => close(undefined));
    scrim.addEventListener("mousedown", (e) => {
      if (e.target === scrim) close(undefined);
    });
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (submitBtn.disabled) return;
      submitBtn.disabled = true;
      submitBtn.setAttribute("aria-busy", "true");
      errBox.hidden = true;
      try {
        const out = await onSubmit();
        close(out === undefined ? true : out);
      } catch (err) {
        errBox.replaceChildren(icon("circle-alert"), h("span", {}, err instanceof ApiError ? err.message : String(err.message || err)));
        errBox.hidden = false;
        submitBtn.removeAttribute("aria-busy");
        submitBtn.disabled = confirmWord ? confirmInput.value !== confirmWord : false;
      }
    });
    addEventListener("keydown", onKey, true);
    openDialogs.add(close);
    document.body.append(scrim);
    (form.querySelector("input,select,textarea") || submitBtn).focus();
  });
}

/** confirm({title, message, submit, danger, confirmWord, onSubmit}) */
export function confirm(opts) {
  return dialog({ ...opts, body: [h("p", {}, opts.message)] });
}
