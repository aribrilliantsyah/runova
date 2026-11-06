import * as api from "/assets/js/api.js";

const RETRY_MS = 1500;
const RESUME_TAIL = 2000;

// sortable("2026-11-05T23:12:01.5Z") → "2026-11-05T23:12:01.500000000Z": Docker trims trailing
// zeros of the fraction, which breaks comparing the text; UTC times then sort as text.
const sortable = (t) => t.replace(/(\d{2}:\d{2}:\d{2})(?:\.(\d+))?Z$/, (_, hms, f = "") => `${hms}.${f.padEnd(9, "0")}Z`);

/**
 * logStream({id, tail, onLine, onState, onRestart}) follows an app's container log over a WebSocket.
 * onLine({time, text, stream}) with text as written (ANSI codes kept); onState("connecting" |
 * "live" | "closed"); onRestart(time) when the container started again since the last connection.
 * Docker ends the stream when the container stops, also when its restart policy starts it again,
 * so it reconnects until close(). A reconnect asks for recent history again and skips the lines
 * it already delivered, so nothing written in between is lost and nothing repeats.
 */
export function logStream({ id, tail = 200, onLine, onState, onRestart = () => {} }) {
  let sock = null;
  let timer = null;
  let stopped = false;
  let started = ""; // started_at of the container on the last connection
  let last = ""; // RFC 3339 time of the last line delivered (nanoseconds, sortable as text)
  let lastSame = 0; // how many lines carried exactly that time

  const connect = () => {
    if (stopped) return;
    onState("connecting");
    const s = api.ws(`/api/apps/${id}/logs?tail=${last ? RESUME_TAIL : tail}`);
    sock = s;
    let skipSame = lastSame;
    let resuming = Boolean(last);
    s.onopen = () => onState("live");
    s.onmessage = (e) => {
      let l;
      try {
        l = JSON.parse(e.data);
      } catch {
        return;
      }
      if (l.type === "start") {
        if (started && l.started_at !== started) onRestart(Date.parse(l.started_at) || Date.now());
        started = l.started_at;
        return;
      }
      const t = sortable(String(l.time || ""));
      // Resuming after a reconnect: skip what was already delivered (up to the last time seen).
      // Within one connection lines pass in Docker's order, even when stdout and stderr interleave
      // with slightly older times.
      if (resuming) {
        if (t && t < last) return;
        if (t === last && skipSame > 0) {
          skipSame--;
          return;
        }
        resuming = false;
      }
      if (t === last) lastSame++;
      else if (!last || t > last) {
        last = t;
        lastSame = 1;
      }
      onLine({ time: Date.parse(l.time) || Date.now(), text: String(l.text ?? ""), stream: l.stream });
    };
    s.onclose = () => {
      if (sock !== s) return;
      onState("closed");
      if (!stopped) timer = setTimeout(connect, RETRY_MS);
    };
  };
  connect();
  return {
    close() {
      stopped = true;
      clearTimeout(timer);
      sock?.close();
    },
  };
}
