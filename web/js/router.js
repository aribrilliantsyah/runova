// Hash router: "#/apps/12/env" → route("/apps/:id/:tab?", render).
//
// render(root, params, meta) fills root and may return a cleanup function, or
// {cleanup, update}. When the next URL matches the same route and the page
// returned update(params), the page is kept and update is called instead of
// rendering again (used for switching tabs without reloading the app).
// update returns false when it cannot handle the new parameters; the page is then rendered afresh.

import { clear } from "/assets/js/dom.js";

const routes = [];
export const params = {};

function compile(pattern) {
  const segs = pattern.split("/").filter(Boolean).map((s) => {
    const m = /^:(\w+)(\?)?$/.exec(s);
    return m ? { name: m[1], optional: Boolean(m[2]) } : { literal: s };
  });
  return (path) => {
    const parts = path.split("/").filter(Boolean);
    const out = {};
    let i = 0;
    for (const s of segs) {
      if (s.literal !== undefined) {
        if (parts[i++] !== s.literal) return null;
      } else if (i < parts.length) {
        out[s.name] = decodeURIComponent(parts[i++]);
      } else if (!s.optional) {
        return null;
      }
    }
    return i === parts.length ? out : null;
  };
}

/** route(pattern, render, meta): register a page. meta is passed to render and to start()'s hooks. */
export function route(pattern, render, meta = {}) {
  routes.push({ pattern, match: compile(pattern), render, meta });
}

export const current = () => location.hash.replace(/^#/, "").split("?")[0] || "/";

/** go(path): navigate; replace avoids a history entry (used for redirects). */
export function go(path, { replace = false } = {}) {
  if (replace) location.replace(`#${path}`);
  else location.hash = path;
}

let mounted = null;
let cleanupFn = null;
let updateFn = null;
let generation = 0;

function unmount() {
  if (cleanupFn) {
    try {
      cleanupFn();
    } catch (e) {
      console.error("page cleanup", e);
    }
  }
  mounted = cleanupFn = updateFn = null;
}

/**
 * start(root, hooks): render the current URL and follow hash changes.
 * hooks.before(meta, params) may return a path to redirect to; hooks.shown(meta, params, path)
 * runs after the route is chosen; hooks.notFound(root) renders unknown paths.
 */
export function start(root, hooks = {}) {
  const render = async () => {
    const path = current();
    const my = ++generation;
    for (const r of routes) {
      const p = r.match(path);
      if (!p) continue;
      const redirect = hooks.before?.(r.meta, p);
      if (redirect) {
        go(redirect, { replace: true });
        return;
      }
      Object.keys(params).forEach((k) => delete params[k]);
      Object.assign(params, p);
      hooks.shown?.(r.meta, p, path);
      if (mounted === r && updateFn && updateFn(p) !== false) return;
      unmount();
      clear(root);
      mounted = r;
      const out = await r.render(root, p, r.meta);
      if (my !== generation) {
        // navigated away while the page was loading
        if (typeof out === "function") out();
        else out?.cleanup?.();
        return;
      }
      if (typeof out === "function") cleanupFn = out;
      else if (out) {
        cleanupFn = out.cleanup || null;
        updateFn = out.update || null;
      }
      return;
    }
    unmount();
    clear(root);
    hooks.notFound?.(root, path);
  };
  const onChange = () => render();
  addEventListener("hashchange", onChange);
  render();
  return () => {
    removeEventListener("hashchange", onChange);
    generation++;
    unmount();
  };
}
