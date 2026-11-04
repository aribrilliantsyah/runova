# nginx di depan Runova

[English](nginx-front-proxy.md)

Untuk server yang hanya punya IP lokal dan berada di belakang server lain yang menjalankan nginx:

```
Cloudflare (DNS only) → IP publik → server nginx → server Runova (IP lokal)
```

nginx memegang sertifikat TLS publik (mode A) dan meneruskan ke Runova lewat **HTTPS**. Runova tetap memakai Caddy-nya sendiri, jadi cara kerja aplikasi dan domain tidak berubah. Dua hal harus benar, kalau tidak muncul masalah di bawah:

- nginx harus meneruskan dengan `proxy_pass https://` (bukan `http://`), kalau tidak Caddy mengalihkan HTTP ke HTTPS tanpa henti (`ERR_TOO_MANY_REDIRECTS`).
- Runova harus tahu bahwa server nginx adalah **proxy tepercaya**, kalau tidak setiap pengunjung tampak berasal dari server nginx: batas login (5 gagal per 15 menit) mengunci semua orang sekaligus, dan log aktivitas mencatat alamat nginx.

## 1. Sisi Runova

Beri tahu Runova alamat asal koneksi nginx (IP lokal nginx seperti yang terlihat dari server Runova):

```bash
sudo runova panel set --trusted-proxy 192.168.1.20
```

Ulangi opsi untuk beberapa proxy, pakai `--trusted-proxy ""` untuk mengosongkan, atau ubah kolom **Proxy di depan Runova** di halaman Server panel. Opsi installer: `--trusted-proxy <ip/cidr>`. `0.0.0.0/0` ditolak karena membuat siapa pun bisa memalsukan alamatnya.

Isi **IP publik** server dengan IP publik server **nginx** (`sudo runova panel set --public-ip <IP publik nginx>`), karena Cek DNS domain hosting membandingkan record wildcard dengan nilai itu.

Buka port 80 dan 443 server Runova hanya untuk server nginx.

## 2. DNS

Biarkan semua record **DNS only** (awan abu-abu) dan mengarah ke IP publik server nginx: domain panel dan wildcard domain hosting (`*.porto.devkriyanesia.my.id`). Cloudflare *Proxied* dengan SSL *Flexible* menimbulkan loop redirect yang sama.

## 3. Konfigurasi nginx

Simpan sebagai `/etc/nginx/conf.d/runova.conf`, lalu ganti `192.168.1.10` (IP lokal server Runova), `runova.prodak.id` (domain panel), dan `porto.devkriyanesia.my.id` (domain hosting). `test/e2e/nginx.sh` menguji persis berkas ini.

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

Port 80 diteruskan ke Runova karena Caddy menjawab tantangan ACME HTTP-01 di sana (Caddy mengambil sertifikat Let's Encrypt-nya sendiri untuk domain itu) dan mengalihkan HTTP ke HTTPS satu kali. WebSocket (log aplikasi, terminal) lewat header `Upgrade`; unggahan dialirkan dengan `proxy_request_buffering off` sampai `client_max_body_size 600m` (batas unggahan Runova bawaannya 500 MB).

## 4. Sertifikat dengan certbot dan DNS Cloudflare

Sertifikat wildcard butuh tantangan DNS-01. Satu sertifikat per zona (`prodak.id` dan `devkriyanesia.my.id` zona terpisah, jadi dua sertifikat):

```bash
sudo apt install certbot python3-certbot-dns-cloudflare
```

Di Cloudflare buat token API dengan izin **Zone → DNS → Edit**, dibatasi ke zona yang bersangkutan. Simpan dengan mode 0600:

```bash
sudo install -m 600 /dev/null /etc/letsencrypt/cloudflare-devkriyanesia.ini
echo 'dns_cloudflare_api_token = <token>' | sudo tee /etc/letsencrypt/cloudflare-devkriyanesia.ini >/dev/null
```

Terbitkan sertifikat (nama sertifikat harus sama dengan jalur di berkas nginx):

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

Pembaruan berjalan lewat `certbot.timer`; deploy hook tersimpan dan memuat ulang nginx setiap selesai pembaruan. Periksa dengan `sudo certbot renew --dry-run`.

## 5. Pemecahan masalah

| Gejala | Penyebab dan perbaikan |
|---|---|
| `ERR_TOO_MANY_REDIRECTS` | nginx memakai `proxy_pass http://` untuk port 443, atau Cloudflare *Proxied* dengan SSL *Flexible*. Pakai `proxy_pass https://` dan DNS only. |
| 502 Bad Gateway | IP Runova di `proxy_pass` salah, Caddy Runova mati, atau firewall di antara kedua server. Uji dari server nginx: `curl -vk https://<IP Runova> -H 'Host: <domain panel>'`. |
| Peramban memperingatkan sertifikat subdomain | Sertifikat wildcard belum diterbitkan untuk zona itu, atau `ssl_certificate` menunjuk sertifikat yang salah. |
| Semua orang terkunci login bersamaan, atau aktivitas mencatat IP nginx | Proxy tepercaya belum diisi atau berbeda dari alamat asal koneksi nginx. Lihat alamat yang dicatat log aktivitas, lalu `sudo runova panel set --trusted-proxy <alamat itu>`. |
| Cek DNS domain hosting menyebut record menunjuk ke IP lain | Isi IP publik di Runova dengan IP publik server nginx. |
