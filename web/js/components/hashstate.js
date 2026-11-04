// Page state in the URL hash: "#/activity?q=deploy&range=24h&page=2".
// Defaults are left out of the URL, so a plain "#/activity" is the default view.

/** readState(defaults) → the defaults overlaid with the hash query (values are strings). */
export function readState(defaults) {
  const i = location.hash.indexOf("?");
  const qs = new URLSearchParams(i < 0 ? "" : location.hash.slice(i + 1));
  const out = { ...defaults };
  for (const k of Object.keys(defaults)) if (qs.has(k)) out[k] = qs.get(k);
  return out;
}

/** stateHash(path, state, defaults) → "#/path?…" without default values. */
export function stateHash(path, state, defaults) {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(state)) if (v !== "" && v != null && String(v) !== String(defaults[k] ?? "")) qs.set(k, v);
  const s = qs.toString();
  return `#${path}${s ? `?${s}` : ""}`;
}

/**
 * writeState(path, state, defaults, {replace}): put state in the URL. A new history entry
 * fires hashchange (the router then calls the page's update); replace does not, for typing.
 */
export function writeState(path, state, defaults, { replace = false } = {}) {
  const next = stateHash(path, state, defaults);
  if (next === location.hash) return false;
  if (replace) history.replaceState(history.state, "", next);
  else location.hash = next;
  return true;
}
