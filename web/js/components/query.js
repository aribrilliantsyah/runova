// The Activity search syntax, mirrored from internal/store/activity_search.go so the page can
// show each filter as a chip, add facet filters to the query and highlight matches.
// Words and "phrases" are ANDed, -word excludes, field:value matches one column, -field:value excludes it.

export const FIELDS = ["aksi", "pelaku", "target", "ip", "hasil"];

/** tokens(q) → [{neg, field, value, raw}] in order; empty pieces are dropped. */
export function tokens(q) {
  const out = [];
  const s = q || "";
  let i = 0;
  const quoted = (at) => {
    let end = at + 1;
    while (end < s.length && s[end] !== '"') end++;
    return [s.slice(at + 1, end).trim(), end < s.length ? end + 1 : end];
  };
  while (i < s.length) {
    if (/\s/.test(s[i])) {
      i++;
      continue;
    }
    const start = i;
    let neg = false;
    if (s[i] === "-") {
      neg = true;
      i++;
    }
    let field = "";
    let value = "";
    if (s[i] === '"') {
      [value, i] = quoted(i);
    } else {
      const wordStart = i;
      while (i < s.length && !/\s/.test(s[i])) {
        if (s[i] === '"' && i > wordStart && s[i - 1] === ":") break;
        i++;
      }
      value = s.slice(wordStart, i);
      const c = value.indexOf(":");
      if (c > 0 && FIELDS.includes(value.slice(0, c).toLowerCase())) {
        field = value.slice(0, c).toLowerCase();
        value = value.slice(c + 1);
        if (!value && s[i] === '"') [value, i] = quoted(i);
      } else if (s[i] === '"') {
        // a word glued to a quote that is not field:"…": keep reading it as text
        const [rest, next] = quoted(i);
        value += rest;
        i = next;
      }
    }
    if (value) out.push({ neg, field, value, raw: s.slice(start, i) });
  }
  return out;
}

/** quote(v): v as one search token, quoted when it holds a space or quote. */
export const quote = (v) => (/[\s"]/.test(v) ? `"${v.replace(/"/g, "")}"` : v);

/** format(t): one token back as text. */
export const format = (t) => `${t.neg ? "-" : ""}${t.field ? `${t.field}:` : ""}${quote(t.value)}`;

/**
 * toggle(q, field, value, neg): the query with field:value added (or replaced when the same
 * pair is present with the other sign), or removed when exactly that filter is already there.
 */
export function toggle(q, field, value, neg = false) {
  const list = tokens(q);
  const same = (t) => t.field === field && t.value.toLowerCase() === String(value).toLowerCase();
  const hit = list.find(same);
  const rest = list.filter((t) => !same(t) && !(field === "hasil" && t.field === "hasil"));
  if (!(hit && hit.neg === neg)) rest.push({ neg, field, value: String(value) });
  return rest.map(format).join(" ");
}

/** without(q, index): the query without its index-th token. */
export function without(q, index) {
  return tokens(q).filter((_, i) => i !== index).map(format).join(" ");
}

/** has(q, field, value) → "" | "in" | "out". */
export function has(q, field, value) {
  const t = tokens(q).find((x) => x.field === field && x.value.toLowerCase() === String(value).toLowerCase());
  return t ? (t.neg ? "out" : "in") : "";
}

/** words(q): the free-text words and phrases to highlight (not exclusions, not fields). */
export const words = (q) => tokens(q).filter((t) => !t.neg && !t.field).map((t) => t.value);

/** fieldWords(q, field): positive values of one field, for highlighting that column. Prefix values lose the *. */
export const fieldWords = (q, field) => tokens(q).filter((t) => !t.neg && t.field === field).map((t) => t.value.replace(/\*$/, ""));
