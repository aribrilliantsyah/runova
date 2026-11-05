// Pure helpers of the Files tab: sorting, file kinds, range selection and paths.

const collator = new Intl.Collator("id", { numeric: true, sensitivity: "base" });

/** sortEntries(list, key, order): folders always first; key "name", "size" (folders by item count) or "mod". */
export function sortEntries(list, key = "name", order = "asc") {
  const dir = order === "desc" ? -1 : 1;
  const by = {
    name: (a, b) => collator.compare(a.name, b.name),
    size: (a, b) => (a.type === "dir" ? a.items - b.items : a.size - b.size) || collator.compare(a.name, b.name),
    mod: (a, b) => a.mod_time - b.mod_time || collator.compare(a.name, b.name),
  }[key] || ((a, b) => collator.compare(a.name, b.name));
  return [...list].sort((a, b) => {
    const da = a.type === "dir";
    const db = b.type === "dir";
    if (da !== db) return da ? -1 : 1;
    return by(a, b) * dir;
  });
}

const EXT = {
  image: ["png", "jpg", "jpeg", "gif", "webp", "svg", "ico", "bmp", "avif"],
  code: ["js", "mjs", "ts", "json", "go", "java", "kt", "py", "rb", "php", "sh", "html", "css", "xml", "yml", "yaml", "toml", "properties", "sql", "env", "ini", "conf"],
  archive: ["zip", "gz", "tgz", "tar", "jar", "war", "7z", "rar", "bz2", "xz"],
  pdf: ["pdf"],
  text: ["txt", "md", "log", "csv"],
};

/** fileKind(entry) → "folder", "link", "image", "code", "archive", "pdf", "text" or "other". */
export function fileKind(e) {
  if (e.type === "dir") return "folder";
  if (e.type === "link") return "link";
  const i = e.name.lastIndexOf(".");
  if (i <= 0) return "other";
  const ext = e.name.slice(i + 1).toLowerCase();
  for (const [k, list] of Object.entries(EXT)) if (list.includes(ext)) return k;
  return "other";
}

/** The Lucide icon of each kind. */
export const KIND_ICON = { folder: "folder", link: "link", image: "file-image", code: "file-code", archive: "file-archive", pdf: "file-text", text: "file-text", other: "file" };

/** rangeSelect(names, selected, anchor, name) → a new Set with every row from anchor to name added (shift-click). */
export function rangeSelect(names, selected, anchor, name) {
  const out = new Set(selected);
  const a = names.indexOf(anchor);
  const b = names.indexOf(name);
  if (a < 0 || b < 0) return new Set([name]);
  for (let i = Math.min(a, b); i <= Math.max(a, b); i++) out.add(names[i]);
  return out;
}

/** parentOf("a/b/c") → "a/b"; the workspace root's parent is itself. */
export function parentOf(path) {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i);
}

/**
 * appPaths(rel, folders) → the paths the app uses for a workspace item: the mount path first when
 * it is inside a mounted saved folder, then "./rel" and "/data/rel" (the working directory).
 */
export function appPaths(rel, folders = []) {
  const out = [];
  const m = folders.find((f) => f.path.startsWith("/") && (rel === f.dir || rel.startsWith(`${f.dir}/`)));
  if (m) out.push(m.path + rel.slice(m.dir.length));
  out.push(`./${rel}`, `/data/${rel}`);
  return out;
}

/** filesHash(id, path) → "#/apps/7/files?path=storage%2F2026". */
export function filesHash(id, path) {
  const q = path ? `?${new URLSearchParams({ path })}` : "";
  return `#/apps/${id}/files${q}`;
}
