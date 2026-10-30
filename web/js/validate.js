// Client-side checks with the same rules and wording as the server (internal/apps/validate.go),
// so a mistake shows before the request leaves.

const APP_NAME = /^[a-z][a-z0-9-]{1,38}[a-z0-9]$/;
const ENV_NAME = /^[A-Z][A-Z0-9_]{1,63}$/;
const DB_SUFFIX = /^[a-z][a-z0-9_]{0,30}$/;
const HOST = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

export const MAX_ENV_ENTRIES = 200;
export const MAX_ENV_VALUE = 32 << 10;
export const SECRET_WORDS = ["PASSWORD", "SECRET", "TOKEN", "KEY"];

export const isSecretName = (name) => SECRET_WORDS.some((w) => name.includes(w));

export function appNameError(name) {
  return APP_NAME.test(name) ? "" : "Nama aplikasi harus 3–40 karakter: huruf kecil, angka, dan tanda hubung; diawali huruf dan tidak diakhiri tanda hubung";
}

export function dbSuffixError(suffix) {
  return DB_SUFFIX.test(suffix) ? "" : "Nama database harus huruf kecil, angka, atau garis bawah, diawali huruf, maksimal 31 karakter";
}

export function hostError(host) {
  return HOST.test(host) ? "" : "Domain tidak valid: huruf kecil dan angka, minimal dua bagian, contoh app.perusahaan.co.id";
}

export function envNameError(name) {
  return ENV_NAME.test(name) ? "" : `Nama variabel "${name}" tidak valid: huruf besar, angka, dan garis bawah, diawali huruf`;
}

/** envError(entries) → the first problem in a list of {name, value}, or "". */
export function envError(entries) {
  if (entries.length > MAX_ENV_ENTRIES) return `Maksimal ${MAX_ENV_ENTRIES} variabel lingkungan`;
  const seen = new Set();
  for (const e of entries) {
    const bad = envNameError(e.name);
    if (bad) return bad;
    if (seen.has(e.name)) return `Variabel ${e.name} muncul lebih dari sekali`;
    if (new TextEncoder().encode(e.value).length > MAX_ENV_VALUE) return `Nilai ${e.name} melebihi ${MAX_ENV_VALUE >> 10} KB`;
    if (e.value.includes("\0")) return `Nilai ${e.name} tidak boleh mengandung karakter NUL`;
    seen.add(e.name);
  }
  return "";
}

export function jvmOptsError(opts) {
  if (new TextEncoder().encode(opts).length > 1024) return "Opsi JVM maksimal 1024 byte";
  const bad = opts.split(/\s+/).filter(Boolean).find((t) => !t.startsWith("-"));
  return bad ? `Opsi JVM harus diawali "-": ${bad}` : "";
}

export function portError(p) {
  return Number.isInteger(p) && p >= 1 && p <= 65535 ? "" : "Port internal harus antara 1 dan 65535";
}

/** uploadError(file, limitMB): the server's own wording for a file over the limit. */
export function uploadError(file, limitMB) {
  return file.size > limitMB * 1024 * 1024 ? `File melebihi batas ${limitMB} MB` : "";
}

/** parseDotenv(text) → [{name, value, secret}] from KEY=VALUE lines; comments and blanks are skipped. */
export function parseDotenv(text) {
  const out = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const m = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
    if (!m) continue;
    let value = m[2];
    const q = value[0];
    if ((q === '"' || q === "'") && value.lastIndexOf(q) > 0) {
      value = value.slice(1, value.lastIndexOf(q));
    } else {
      value = value.replace(/\s+#.*$/, "");
    }
    const name = m[1].toUpperCase();
    out.push({ name, value, secret: isSecretName(name) });
  }
  return out;
}

/** suggestName("billing-api-2.4.1.jar") → "billing-api" */
export function suggestName(fileName) {
  let n = fileName.replace(/\.(tar\.gz|tgz|jar|exe)$/i, "").toLowerCase();
  n = n.replace(/[-_.]v?\d[\w.+-]*$/, "");
  n = n.replace(/[^a-z0-9-]+/g, "-").replace(/^[^a-z]+/, "").replace(/-+/g, "-").replace(/-$/, "");
  n = n.slice(0, 40).replace(/-$/, "");
  return n.length >= 3 ? n : "";
}

/** memoryChoices(leftMB) / cpuChoices(leftMilli): the values on offer within what is left of the quota. */
export function memoryChoices(left) {
  const set = new Set([128, 256, 512, 768, 1024, 1536, 2048, 3072, 4096, 6144, 8192].filter((v) => v <= left));
  const top = left - (left % 64);
  if (top >= 128) set.add(top);
  return [...set].sort((a, b) => a - b);
}

export function cpuChoices(left) {
  const set = new Set([100, 250, 500, 750, 1000, 1500, 2000, 3000, 4000, 6000, 8000].filter((v) => v <= left));
  const top = left - (left % 50);
  if (top >= 100) set.add(top);
  return [...set].sort((a, b) => a - b);
}
