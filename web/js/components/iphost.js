// Where an app's IP port is reached. The server knows whether the configured public IP is
// one of its own; behind NAT or a proxy that forwards only 80/443 it names its LAN address.

/** ipHost(info) → {host, local, title} from GET /api/info. */
export function ipHost(info) {
  const host = info.ip_access_host || info.public_ip || location.hostname;
  const local = Boolean(info.ip_access_local);
  const title = local ? `Alamat jaringan lokal server (${host}). Hanya bisa dibuka dari jaringan yang sama atau lewat VPN.` : "";
  return { host, local, title };
}
