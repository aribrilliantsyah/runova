# nginx in front of Runova

[Bahasa Indonesia](nginx-front-proxy.id.md)

For servers that have only a local IP and sit behind another server running nginx:

```
Cloudflare (DNS only) → public IP → nginx server → Runova server (local IP)
```

nginx keeps the public TLS certificates (mode A) and forwards to Runova over **HTTPS**. Runova keeps its own Caddy, so nothing else changes in how apps and domains work. Two things must be right, or you get the problems below:

- nginx must forward with `proxy_pass https://` (not `http://`), or Caddy redirects HTTP to HTTPS forever (`ERR_TOO_MANY_REDIRECTS`).
- Runova must know the nginx server is a **trusted proxy**, or every visitor looks like the nginx server: the login limit (5 failures per 15 minutes) then locks everybody out together, and the activity log records nginx's address.

## 1. Runova side

Tell Runova which address nginx connects from (its local IP as the Runova server sees it):

```bash
sudo runova panel set --trusted-proxy 192.168.1.20
```

Repeat the option for several proxies, use `--trusted-proxy ""` to clear, or edit **Proxy di depan Runova** on the Server page of the panel. Installer option: `--trusted-proxy <ip/cidr>`. `0.0.0.0/0` is refused: it would let anyone forge their address.

Set the server's **public IP** to the public IP of the **nginx** server (`sudo runova panel set --public-ip <nginx public IP>`), because the DNS check of a hosting domain compares the wildcard record with it.

Open ports 80 and 443 of the Runova server to the nginx server only.

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

# Port 80 goes to Runova's port 80: Caddy answers the ACME HTTP-01 challenges
# there and redirects everything else to HTTPS, once.
server {
    listen 80;
    server_name runova.prodak.id porto.devkriyanesia.my.id *.porto.devkriyanesia.my.id;

    location / {
        proxy_pass http://192.168.1.10;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
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

Port 80 is forwarded to Runova because Caddy answers the ACME HTTP-01 challenges there (it fetches its own Let's Encrypt certificates for the domains) and redirects HTTP to HTTPS once. WebSockets (app logs, terminal) pass through the `Upgrade` headers; uploads stream with `proxy_request_buffering off` up to `client_max_body_size 600m` (Runova's upload limit is 500 MB by default).

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
| 502 Bad Gateway | Wrong Runova IP in `proxy_pass`, Runova's Caddy not running, or a firewall between the servers. Test from the nginx server: `curl -vk https://<Runova IP> -H 'Host: <panel domain>'`. |
| Browser warns about a certificate for a subdomain | The wildcard certificate was not issued for that zone or `ssl_certificate` points at the wrong certificate. |
| Everybody is locked out of login together, or activity shows the nginx IP | The trusted proxy is not set, or differs from the address nginx connects from. Check the activity log for the address Runova records, then `sudo runova panel set --trusted-proxy <that address>`. |
| Cek DNS of a hosting domain says the record points elsewhere | Set the public IP in Runova to the nginx server's public IP. |
