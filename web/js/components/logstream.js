import * as api from "/assets/js/api.js";

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\[[0-9;?]*[A-Za-z]/g;
const RETRY_MS = 2500;

export const levelOf = (text) => {
  if (/\b(ERROR|FATAL|SEVERE|CRITICAL|panic)\b/.test(text)) return "e";
  if (/\b(WARN|WARNING)\b/.test(text)) return "w";
  if (/^runova\b/.test(text)) return "s";
  return "";
};

/**
 * logStream({id, tail, onLine, onState}) follows an app's container log over a WebSocket.
 * onLine({time, text, stream, level}); onState("connecting" | "live" | "closed").
 * It reconnects every few seconds until close() (the container may not exist yet during a deploy),
 * asking for no history on a reconnect so lines are not repeated.
 */
export function logStream({ id, tail = 200, onLine, onState }) {
  let sock = null;
  let timer = null;
  let stopped = false;
  let first = true;

  const connect = () => {
    if (stopped) return;
    onState("connecting");
    const s = api.ws(`/api/apps/${id}/logs?tail=${first ? tail : 0}`);
    sock = s;
    s.onopen = () => {
      first = false;
      onState("live");
    };
    s.onmessage = (e) => {
      let l;
      try {
        l = JSON.parse(e.data);
      } catch {
        return;
      }
      const text = String(l.text ?? "").replace(ANSI, "");
      onLine({ time: Date.parse(l.time) || Date.now(), text, stream: l.stream, level: levelOf(text) });
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
