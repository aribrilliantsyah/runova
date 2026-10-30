import { Terminal } from "/assets/vendor/xterm/xterm.mjs";
import { FitAddon } from "/assets/vendor/xterm/addon-fit.mjs";
import * as api from "/assets/js/api.js";
import { h, icon } from "/assets/js/dom.js";

let open = null;

/**
 * openTerminal({id, name}) shows a drawer at the bottom of the page with a shell in the app's
 * container. Binary WebSocket frames carry stdin/stdout; a text frame {"type":"resize"} sizes the TTY.
 * One drawer at a time; opening another closes the first.
 */
export function openTerminal({ id, name }) {
  open?.close();
  const term = new Terminal({
    cursorBlink: true,
    fontFamily: '"IBM Plex Mono", ui-monospace, monospace',
    fontSize: 13,
    scrollback: 2000,
    theme: { background: "#141413", foreground: "#EDEBE4", cursor: "#D97757", selectionBackground: "#3A3935" },
  });
  const fit = new FitAddon();
  term.loadAddon(fit);

  const status = h("span", { class: "chip" }, "Menyambung…");
  const closeBtn = h("button", { class: "ibtn", type: "button", "aria-label": "Tutup terminal" }, icon("x"));
  const box = h("div", { class: "term" });
  const drawer = h("section", { class: "drawer", "aria-label": `Terminal ${name}` }, h("header", {}, icon("square-terminal"), h("b", {}, `Terminal · ${name}`), status, h("span", { class: "spacer" }), closeBtn), box);
  document.body.append(drawer);
  term.open(box);

  const sock = api.ws(`/api/apps/${id}/terminal`);
  const enc = new TextEncoder();
  const sendSize = () => {
    fit.fit();
    if (sock.readyState === WebSocket.OPEN) sock.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
  };
  sock.onopen = () => {
    status.textContent = "Tersambung";
    status.className = "chip ok";
    sendSize();
    term.focus();
  };
  sock.onmessage = (e) => term.write(typeof e.data === "string" ? e.data : new Uint8Array(e.data));
  sock.onclose = (e) => {
    status.textContent = e.reason || "Terputus";
    status.className = "chip err";
    term.options.disableStdin = true;
  };
  term.onData((d) => sock.readyState === WebSocket.OPEN && sock.send(enc.encode(d)));
  const ro = new ResizeObserver(() => sendSize());
  ro.observe(box);

  const onKey = (e) => {
    if (e.key === "Escape" && drawer.contains(document.activeElement) && e.ctrlKey) close();
  };
  addEventListener("keydown", onKey);

  function close() {
    if (open !== handle) return;
    open = null;
    removeEventListener("keydown", onKey);
    ro.disconnect();
    sock.close();
    term.dispose();
    drawer.remove();
  }
  closeBtn.addEventListener("click", close);
  const handle = { close, term, drawer, ready: new Promise((r) => sock.addEventListener("open", r, { once: true })) };
  open = handle;
  return handle;
}

export const closeTerminal = () => open?.close();
