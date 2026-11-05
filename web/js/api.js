// The only place that talks to the server.

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export const UNAUTHORIZED = "runova:unauthorized";
export const RETURN_KEY = "runova.return";

// A 401 from these paths is an ordinary answer, not an expired session.
const AUTH_PATHS = new Set(["/api/login", "/api/me"]);

function unauthorized(path) {
  if (AUTH_PATHS.has(path)) return;
  try {
    sessionStorage.setItem(RETURN_KEY, location.hash);
  } catch {
    // storage blocked: the user lands on the default page after login
  }
  dispatchEvent(new Event(UNAUTHORIZED));
}

async function parse(res) {
  const text = await res.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

async function request(method, path, body, headers = {}) {
  const init = { method, credentials: "same-origin", headers: { ...headers } };
  if (body !== undefined) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, "Tidak bisa menghubungi server");
  }
  const data = await parse(res);
  if (res.ok) return data;
  if (res.status === 401) unauthorized(path);
  const message = (data && typeof data === "object" && data.error) || `Permintaan gagal (${res.status})`;
  throw new ApiError(res.status, message);
}

export const get = (path) => request("GET", path);
export const post = (path, body = {}) => request("POST", path, body);
export const patch = (path, body) => request("PATCH", path, body);
export const put = (path, body) => request("PUT", path, body);
export const del = (path, body = {}) => request("DELETE", path, body);

/**
 * upload(path, file, onProgress, fields) → {promise, abort}. A multipart POST through
 * XHR, because fetch cannot report upload progress. onProgress gets 0..1. fields are
 * sent before the file, so the server can check them before it reads the file.
 */
export function upload(path, file, onProgress, fields = {}) {
  const xhr = new XMLHttpRequest();
  const promise = new Promise((resolve, reject) => {
    xhr.open("POST", path);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(e.loaded / e.total);
    };
    xhr.onload = () => {
      let data = null;
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        // not JSON
      }
      if (xhr.status >= 200 && xhr.status < 300) return resolve(data);
      if (xhr.status === 401) unauthorized(path);
      reject(new ApiError(xhr.status, data?.error || `Unggahan gagal (${xhr.status})`));
    };
    xhr.onerror = () => reject(new ApiError(0, "Unggahan terputus"));
    xhr.onabort = () => reject(new ApiError(0, "Unggahan dibatalkan"));
    const form = new FormData();
    for (const [k, v] of Object.entries(fields)) form.append(k, v);
    form.append("file", file);
    xhr.send(form);
  });
  return { promise, abort: () => xhr.abort() };
}

/** ws(path) → WebSocket to the same origin; binary frames arrive as ArrayBuffer. */
export function ws(path) {
  const url = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}${path}`;
  const sock = new WebSocket(url);
  sock.binaryType = "arraybuffer";
  return sock;
}
