# nginx di depan Runova

[English](nginx-front-proxy.md)

Untuk server yang hanya punya IP lokal dan berada di belakang server lain yang menjalankan nginx:

```
Cloudflare (DNS only) → IP publik → server nginx → server Runova (IP lokal)
```

nginx memegang sertifikat TLS publik (mode A) dan meneruskan ke Runova lewat **HTTPS**. Runova tetap memakai Caddy-nya sendiri; bila proxy tepercaya diisi (di bawah) Runova berjalan dalam **mode front proxy**: Caddy mengambil semua sertifikat (panel, aplikasi, subdomain domain hosting) dari CA internalnya sendiri, bukan dari Let's Encrypt, sehingga subdomain baru langsung berfungsi dan Caddy tidak butuh tantangan ACME lewat nginx. Dua hal harus benar, kalau tidak muncul masalah di bawah:

- nginx harus meneruskan dengan `proxy_pass https://` (bukan `http://`), kalau tidak Caddy mengalihkan HTTP ke HTTPS tanpa henti (`ERR_TOO_MANY_REDIRECTS`).
- Runova harus tahu bahwa server nginx adalah **proxy tepercaya**, kalau tidak setiap pengunjung tampak berasal dari server nginx: batas login (5 gagal per 15 menit) mengunci semua orang sekaligus, dan log aktivitas mencatat alamat nginx.

## 1. Sisi Runova

Beri tahu Runova alamat asal koneksi nginx (IP lokal nginx seperti yang terlihat dari server Runova):

```bash
sudo runova panel set --trusted-proxy 192.168.1.20
```

Ulangi opsi untuk beberapa proxy, pakai `--trusted-proxy ""` untuk mengosongkan, atau ubah kolom **Proxy di depan Runova** di halaman Server panel. Opsi installer: `--trusted-proxy <ip/cidr>`. `0.0.0.0/0` ditolak karena membuat siapa pun bisa memalsukan alamatnya.

Isi **IP publik** server dengan IP publik server **nginx** (`sudo runova panel set --public-ip <IP publik nginx>`), karena Cek DNS domain hosting membandingkan record wildcard dengan nilai itu.

Mengisi proxy tepercaya menyalakan mode front proxy; mengosongkannya mematikan mode itu dan Caddy kembali ke Let's Encrypt. Setelah perubahan, Runova memuat ulang Caddy dan mengecek ulang semua domain.

Buka port 443 server Runova hanya untuk server nginx (port 80 juga bila nginx meneruskannya, lihat di bawah).

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

nginx menjawab port 80 sendiri dengan pengalihan ke HTTPS: Caddy di belakang nginx memakai sertifikat CA internalnya dan tidak butuh tantangan ACME. Blok port 80 yang masih meneruskan ke Runova (versi panduan sebelumnya) tetap berfungsi; pengalihan lalu dilakukan Caddy. WebSocket (log aplikasi, terminal) lewat header `Upgrade`; unggahan dialirkan dengan `proxy_request_buffering off` sampai `client_max_body_size 600m` (batas unggahan Runova bawaannya 500 MB).

### Opsional: nginx memverifikasi sertifikat Caddy

Jalur nginx ke Runova tetap terenkripsi; bawaannya nginx tidak memverifikasi sertifikat upstream (`proxy_ssl_verify off`). Agar Runova juga diautentikasi, salin root CA internal Caddy dari server Runova ke server nginx:

```bash
# di server Runova (paket caddy Ubuntu; berkas hanya bisa dibaca user caddy)
sudo cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt > caddy-root.crt
# atau lewat admin API di server yang sama
curl -s http://127.0.0.1:2019/pki/ca/local | jq -r .root_certificate > caddy-root.crt
```

Simpan sebagai `/etc/nginx/caddy-root.crt` di server nginx dan tambahkan di tiap `location /` blok 443, setelah `proxy_ssl_name $host;`:

```nginx
        proxy_ssl_verify on;
        proxy_ssl_verify_depth 2;
        proxy_ssl_session_reuse off;
        proxy_ssl_trusted_certificate /etc/nginx/caddy-root.crt;
```

`proxy_ssl_session_reuse off` wajib: nginx menyimpan satu sesi TLS upstream per location dan melanjutkannya untuk nama host lain, yang lalu gagal dicek namanya (`upstream SSL certificate does not match`, 502). Root berlaku 10 tahun dan bertahan saat Caddy di-restart; ia berganti hanya bila direktori data Caddy dihapus, dan nginx lalu menjawab 502 sampai root baru disalin. `test/e2e/nginx.sh` (skenario 10) menguji susunan ini.

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
| 502 Bad Gateway untuk subdomain, dan status domainnya `tls: internal error` | Runova 0.3.3 atau lebih lama, atau proxy tepercaya belum diisi: Caddy mencoba mengambil sertifikat Let's Encrypt yang tidak bisa didapat di belakang nginx. Perbarui Runova dan isi proxy tepercaya (bagian 1). |
| Status domain: *Proxy di depan (nginx) … belum menyajikan sertifikat tepercaya* | Alamat publik nama itu menjawab dengan sertifikat yang tidak mencakupnya. Pastikan sertifikat wildcard mencakup nama itu dan `server_name` blok 443 memuat `*.<domain hosting>`. |
| 502 Bad Gateway setelah `proxy_ssl_verify on` | `caddy-root.crt` di server nginx bukan root Caddy ini, atau `proxy_ssl_name $host` tidak ada. Salin ulang root. |
| 502 Bad Gateway | IP Runova di `proxy_pass` salah, Caddy Runova mati, atau firewall di antara kedua server. Uji dari server nginx: `curl -vk https://<IP Runova> -H 'Host: <domain panel>'`. |
| Peramban memperingatkan sertifikat subdomain | Sertifikat wildcard belum diterbitkan untuk zona itu, atau `ssl_certificate` menunjuk sertifikat yang salah. |
| Semua orang terkunci login bersamaan, atau aktivitas mencatat IP nginx | Proxy tepercaya belum diisi atau berbeda dari alamat asal koneksi nginx. Lihat alamat yang dicatat log aktivitas, lalu `sudo runova panel set --trusted-proxy <alamat itu>`. |
| Cek DNS domain hosting menyebut record menunjuk ke IP lain | Isi IP publik di Runova dengan IP publik server nginx. |
