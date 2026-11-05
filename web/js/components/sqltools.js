// Pure helpers of the query editor: CSV export, per-database history, summaries.
import { fmtNum } from "/assets/js/dom.js";

export const HISTORY_MAX = 50;

export const historyKey = (dbID) => `runova.sqlhistory.${dbID}`;

/** loadHistory(dbID) → [{sql, at}], newest first. Broken or blocked storage reads as empty. */
export function loadHistory(dbID, storage = globalThis.localStorage) {
  try {
    const list = JSON.parse(storage.getItem(historyKey(dbID)) || "[]");
    return Array.isArray(list) ? list.filter((x) => x && typeof x.sql === "string") : [];
  } catch {
    return [];
  }
}

/** pushHistory(dbID, sql) puts sql on top (once) and keeps the newest HISTORY_MAX. */
export function pushHistory(dbID, sql, storage = globalThis.localStorage) {
  const text = sql.trim();
  if (!text) return loadHistory(dbID, storage);
  const list = [{ sql: text, at: Date.now() }, ...loadHistory(dbID, storage).filter((x) => x.sql !== text)].slice(0, HISTORY_MAX);
  try {
    storage.setItem(historyKey(dbID), JSON.stringify(list));
  } catch {
    // storage full or blocked: the history just is not kept
  }
  return list;
}

const FORMULA = /^[=+\-@\t\r]/;
const NUMBER = /^-?\d+(\.\d+)?([eE][+-]?\d+)?$/;

function cell(v) {
  if (v == null) return "";
  let s = String(v);
  // A value a spreadsheet would run as a formula is kept as text.
  if (FORMULA.test(s) && !NUMBER.test(s)) s = `'${s}`;
  return /[",\r\n]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s;
}

/** toCSV(columns, rows) → RFC 4180 text; NULL is an empty field. */
export function toCSV(columns, rows) {
  const lines = [columns.map((c) => cell(c.name)).join(",")];
  for (const r of rows) lines.push(r.map(cell).join(","));
  return `${lines.join("\r\n")}\r\n`;
}

export const lineCount = (text) => text.split("\n").length;

/** resultSummary(result) → "SELECT · 2 baris · 12 ms". */
export function resultSummary(r) {
  const parts = [r.command || "Selesai"];
  if (r.columns?.length) parts.push(r.truncated ? `${fmtNum(r.rows.length)} baris pertama` : `${fmtNum(r.rows.length)} baris`);
  else if (r.affected) parts.push(`${fmtNum(r.affected)} baris terpengaruh`);
  else parts.push("selesai");
  parts.push(`${fmtNum(r.duration_ms || 0)} ms`);
  return parts.join(" · ");
}
