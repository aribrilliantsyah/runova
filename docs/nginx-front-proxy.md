# nginx in front of Runova

[Bahasa Indonesia](nginx-front-proxy.id.md)

For servers that have only a local IP and sit behind another server running nginx:

```
Cloudflare (DNS only) → public IP → nginx server → Runova server (local IP)
```

nginx keeps the public TLS certificates (mode A) and forwards to Runova over **HTTPS**. Runova keeps its own Caddy; with a trusted proxy set (below) Runova runs in **front-proxy mode**: Caddy takes every certificate (panel, apps, hosting subdomains) from its own internal CA instead of Let's Encrypt, so a new subdomain works at once and Caddy needs no ACME challenge through nginx. Two things must be right, or you get the problems below:

- nginx must forward with `proxy_pass https://` (not `http://`), or Caddy redirects HTTP to HTTPS forever (`ERR_TOO_MANY_REDIRECTS`).
- Runova must know the nginx server is a **trusted proxy**, or every visitor looks like the nginx server: the login limit (5 failures per 15 minutes) then locks everybody out together, and the activity log records nginx's address.

## 1. Runova side

Tell Runova which address nginx connects from (its local IP as the Runova server sees it):

```bash
sudo runova panel set --trusted-proxy 192.168.1.20
```

Repeat the option for several proxies, use `--trusted-proxy ""` to clear, or edit **Proxy di depan Runova** on the Server page of the panel. Installer option: `--trusted-proxy <ip/cidr>`. `0.0.0.0/0` is refused: it would let anyone forge their address.

Set the server's **public IP** to the public IP of the **nginx** server (`sudo runova panel set --public-ip <nginx public IP>`), because the DNS check of a hosting domain compares the wildcard record with it.

Setting a trusted proxy switches on front-proxy mode; clearing the list switches it off and Caddy goes back to Let's Encrypt. After a change Runova reloads Caddy and checks every domain again.

Open port 443 of the Runova server to the nginx server only (port 80 too if nginx forwards it, see below).

## 2. DNS

Keep every record **DNS only** (grey cloud) and pointed at the public IP of the nginx server: the panel domain and the wildcard of the hosting domain (`*.porto.devkriyanesia.my.id`). Cloudflare *Proxied* with SSL *Flexible* produces the same redirect loop.

## 3. nginx configuration

Save this as `/etc/nginx/conf.d/runova.conf` and replace `192.168.1.10` (local IP of the Runova server), `runova.prodak.id` (panel domain) and `porto.devkriyanesia.my.id` (hosting domain). `test/e2e/nginx.sh` tests exactly this file.

<!-- nginx-runova.conf -->
```nginx
# Runova behind nginx, mode A: nginx holds the public TLS certificates and
# forwards to Runova over HTTPS. Save as /etc/nginx/conf.d/runova.conf.
#
# Replace 192.168.1.10 with the local IP of the Runova server, runova.prodak.id
# with the panel domain and porto.devkriyanesia.my.id with the hosting domain.

map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

# Port 80 redirects to HTTPS here, once. Behind nginx, Runova's Caddy uses
# certificates of its own internal CA, so it needs no ACME challenge on port 80.
server {
    listen 80;
    server_name runova.prodak.id porto.devkriyanesia.my.id *.porto.devkriyanesia.my.id;

    return 301 https://$host$request_uri;
}

# Panel domain (certificate of the prodak.id zone).
server {
    listen 443 ssl;
    server_name runova.prodak.id;

    ssl_certificate     /etc/letsencrypt/live/runova.prodak.id/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/runova.prodak.id/privkey.pem;

    client_max_body_size 600m;

    location / {
        proxy_pass https://192.168.1.10;
        proxy_ssl_server_name on;
        proxy_ssl_name $host;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_request_buffering off;
        proxy_read_timeout 1h;
    }
}

# Hosting domain and every subdomain (wildcard certificate of the
# devkriyanesia.my.id zone).
server {
    listen 443 ssl;
    server_name porto.devkriyanesia.my.id *.porto.devkriyanesia.my.id;

    ssl_certificate     /etc/letsencrypt/live/porto.devkriyanesia.my.id/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/porto.devkriyanesia.my.id/privkey.pem;

    client_max_body_size 600m;

    location / {
        proxy_pass https://192.168.1.10;
        proxy_ssl_server_name on;
        proxy_ssl_name $host;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_request_buffering off;
        proxy_read_timeout 1h;
    }
}
```

nginx answers port 80 itself with a redirect to HTTPS: Caddy behind nginx uses certificates of its internal CA and needs no ACME challenge. A port 80 block that still forwards to Runova (the earlier version of this guide) keeps working; Caddy then does the redirect. WebSockets (app logs, terminal) pass through the `Upgrade` headers; uploads stream with `proxy_request_buffering off` up to `client_max_body_size 600m` (Runova's upload limit is 500 MB by default).

### Optional: nginx verifies Caddy's certificate

The link between nginx and Runova is encrypted either way; nginx does not verify the upstream certificate by default (`proxy_ssl_verify off`). To also authenticate Runova, copy the root of Caddy's internal CA from the Runova server to the nginx server:

```bash
# on the Runova server (Ubuntu caddy package; the file is readable by the caddy user only)
sudo cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt > caddy-root.crt
# or through the admin API on the same server
curl -s http://127.0.0.1:2019/pki/ca/local | jq -r .root_certificate > caddy-root.crt
```

Save it as `/etc/nginx/caddy-root.crt` on the nginx server and add to each `location /` of the 443 blocks, after `proxy_ssl_name $host;`:

```nginx
        proxy_ssl_verify on;
        proxy_ssl_verify_depth 2;
        proxy_ssl_session_reuse off;
        proxy_ssl_trusted_certificate /etc/nginx/caddy-root.crt;
```

`proxy_ssl_session_reuse off` is required: nginx caches one upstream TLS session per location and would resume it for another host name, whose name check then fails (`upstream SSL certificate does not match`, 502). The root is valid for 10 years and survives Caddy restarts; it changes only when Caddy's data directory is deleted, after which nginx answers 502 until the new root is copied. `test/e2e/nginx.sh` (scenario 10) tests this setup.

## 4. Certificates with certbot and Cloudflare DNS

A wildcard certificate needs the DNS-01 challenge. Per zone (`prodak.id` and `devkriyanesia.my.id` are separate zones, so two certificates):

```bash
sudo apt install certbot python3-certbot-dns-cloudflare
```

In Cloudflare create an API token with the permission **Zone → DNS → Edit**, limited to the zone concerned. Store it with mode 0600:

```bash
sudo install -m 600 /dev/null /etc/letsencrypt/cloudflare-devkriyanesia.ini
echo 'dns_cloudflare_api_token = <token>' | sudo tee /etc/letsencrypt/cloudflare-devkriyanesia.ini >/dev/null
```

Issue the certificates (the certificate name must match the paths in the nginx file):

```bash
sudo certbot certonly --dns-cloudflare \
  --dns-cloudflare-credentials /etc/letsencrypt/cloudflare-devkriyanesia.ini \
  --cert-name porto.devkriyanesia.my.id \
  -d porto.devkriyanesia.my.id -d '*.porto.devkriyanesia.my.id' \
  --deploy-hook 'systemctl reload nginx'

sudo certbot certonly --dns-cloudflare \
  --dns-cloudflare-credentials /etc/letsencrypt/cloudflare-prodak.ini \
  --cert-name runova.prodak.id -d runova.prodak.id \
  --deploy-hook 'systemctl reload nginx'
```

Renewal runs from `certbot.timer`; the deploy hook is stored and reloads nginx after each renewal. Check with `sudo certbot renew --dry-run`.

## 5. Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `ERR_TOO_MANY_REDIRECTS` | nginx uses `proxy_pass http://` for port 443, or Cloudflare is *Proxied* with SSL *Flexible*. Use `proxy_pass https://` and DNS only. |
| 502 Bad Gateway for a subdomain, and its domain status reads `tls: internal error` | Runova 0.3.3 or earlier, or no trusted proxy set: Caddy is trying to get a Let's Encrypt certificate it cannot get behind nginx. Update Runova and set the trusted proxy (section 1). |
| Domain status: *Proxy di depan (nginx) … belum menyajikan sertifikat tepercaya* | The public address of the name answers with a certificate that does not cover it. Check that the wildcard certificate covers the name and that `server_name` of the 443 block lists `*.<hosting domain>`. |
| 502 Bad Gateway after enabling `proxy_ssl_verify on` | `caddy-root.crt` on the nginx server is not the root of this Caddy, or `proxy_ssl_name $host` is missing. Copy the root again. |
| 502 Bad Gateway | Wrong Runova IP in `proxy_pass`, Runova's Caddy not running, or a firewall between the servers. Test from the nginx server: `curl -vk https://<Runova IP> -H 'Host: <panel domain>'`. |
| Browser warns about a certificate for a subdomain | The wildcard certificate was not issued for that zone or `ssl_certificate` points at the wrong certificate. |
| Everybody is locked out of login together, or activity shows the nginx IP | The trusted proxy is not set, or differs from the address nginx connects from. Check the activity log for the address Runova records, then `sudo runova panel set --trusted-proxy <that address>`. |
| Cek DNS of a hosting domain says the record points elsewhere | Set the public IP in Runova to the nginx server's public IP. |
