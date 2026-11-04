# Front proxy (nginx di depan Runova) — desain

Tanggal: 2026-11-04. Status: disetujui user — mode A (nginx memegang TLS, meneruskan ke Runova lewat HTTPS).

## Masalah

Server Runova hanya punya IP lokal. Topologi user:

```
Cloudflare (DNS only) → IP publik → server nginx → server Runova (IP lokal)
```

1. nginx menerima HTTPS lalu `proxy_pass http://` ke Runova; Caddy di Runova mengalihkan HTTP→HTTPS (308) → **redirect loop** (sudah direproduksi dengan nginx 1.18 + Caddy 2.6.2).
2. Runova melihat semua pengunjung dari IP server nginx → **batas login (5×/15 menit) berlaku untuk semua orang sekaligus**, aktivitas mencatat IP nginx.
3. Subdomain wildcard dari domain hosting (`*.porto.devkriyanesia.my.id`) harus bisa dilayani lewat nginx.

## Keputusan

### 1. Setelan "Proxy di depan Runova" (`trusted_proxies`)
- Setelan baru `TrustedProxies []string json:"trusted_proxies"` (daftar IP/CIDR, maks 10, bawaan kosong), diubah admin di halaman **Server** dan lewat `runova panel set --trusted-proxy <ip/cidr>` (bisa diulang; `--trusted-proxy ""` mengosongkan) serta opsi installer `--trusted-proxy <ip/cidr>` (diteruskan `get.sh`, disimpan seperti `--public-ip`).
- Runova memasukkannya ke konfigurasi Caddy (`trusted_proxies` di handler `reverse_proxy` setiap route: panel, domain aplikasi, akses IP). Caddy 2.6.2 mendukung field ini di handler reverse_proxy (diverifikasi di source v2.6.2): bila koneksi datang dari proxy tepercaya, `X-Forwarded-For`/`-Proto`/`-Host` yang masuk dipertahankan dan IP proxy ditambahkan di belakang; bila tidak tepercaya, header masuk diabaikan (perilaku sekarang).
- **Runova (`auth.ClientIP`)**: koneksi ke Runova selalu dari Caddy di loopback. Ambil `X-Forwarded-For` dari Caddy dan berjalan **dari kanan ke kiri**: lewati alamat yang termasuk `TrustedProxies` (dan loopback), ambil alamat pertama yang bukan proxy tepercaya. Bila semuanya tepercaya, pakai yang paling kiri. Tanpa `TrustedProxies` hasilnya sama dengan sekarang (elemen terakhir yang ditambahkan Caddy = IP peer). Header yang dipalsukan klien di kiri tidak pernah dipakai selama ada alamat tidak-tepercaya di kanannya.
- `IsHTTPS` mengikuti `X-Forwarded-Proto` yang sudah disaring Caddy (sudah begitu sekarang), jadi cookie `Secure` benar di balik nginx HTTPS.
- Aplikasi pengguna juga menerima `X-Forwarded-For` berisi IP asli lewat Caddy (efek samping yang diinginkan).

### 2. Panduan nginx resmi (`docs/nginx-front-proxy.md`, Inggris + `docs/nginx-front-proxy.id.md`)
Hanya **mode A** (dipilih user): nginx memegang TLS, lalu meneruskan ke Runova lewat **HTTPS**.
- `proxy_pass https://RUNOVA` dengan `proxy_ssl_server_name on; proxy_ssl_name $host;`, header `Host`/`X-Forwarded-For`/`X-Forwarded-Proto https`, WebSocket (`Upgrade`/`Connection` lewat `map`), `client_max_body_size 600m`, `proxy_request_buffering off`, `proxy_read_timeout 1h`, `proxy_http_version 1.1`.
- Port 80 diteruskan ke `http://RUNOVA` (Caddy butuh untuk tantangan ACME HTTP-01 dan pengalihan ke HTTPS).
- **Wildcard:** sertifikat `*.porto.devkriyanesia.my.id` (dan panel `runova.prodak.id`) di nginx lewat **certbot + `python3-certbot-dns-cloudflare`** (paket apt Ubuntu 22.04/24.04/26.04): token API Cloudflare dengan izin *Zone → DNS → Edit* hanya untuk zona terkait, file kredensial 0600, perintah `certbot certonly --dns-cloudflare …`, pembaruan otomatis (`certbot.timer`) + `--deploy-hook "systemctl reload nginx"`. Satu sertifikat per zona (zona `prodak.id` dan zona `devkriyanesia.my.id` terpisah).
- Di Runova: `sudo runova panel set --trusted-proxy <IP lokal nginx>` (atau kartu Server) dan **IP publik = IP publik server nginx** agar Cek DNS domain hosting benar.
- Cloudflare tetap **DNS only** (Proxied + SSL Flexible menimbulkan loop yang sama); firewall server Runova membuka 80/443 untuk IP nginx.
- Pemecahan masalah: `ERR_TOO_MANY_REDIRECTS` (penyebab `proxy_pass http://` atau Cloudflare Flexible), 502 (IP/port Runova salah, firewall), sertifikat subdomain tidak cocok (wildcard belum diterbitkan), login masih terkunci bersama (trusted proxy belum diisi / IP nginx berbeda dari yang diisi).
- Tautkan dari README (bagian Installation dan Domains) dan `docs/install-ubuntu.md` (bagian DNS/server tanpa IP publik), Inggris dan Indonesia.

### 3. Domain hosting di balik nginx
- **Cek DNS** domain hosting sekarang membandingkan dengan `Settings.PublicIP`. Di balik nginx, record wildcard menunjuk ke IP publik server nginx, jadi admin mengisi **IP publik** = IP publik nginx (bukan IP lokal Runova). Panduan menyebutkannya; tidak ada perubahan kode.
- Status sertifikat domain (`ProbeTLS` ke `127.0.0.1:443` dengan SNI) tetap menguji Caddy lokal; di mode A sertifikat publik dipegang nginx, sehingga Caddy memakai sertifikat Let's Encrypt-nya sendiri via HTTP-01 (port 80 diteruskan nginx) — tidak berubah.

## Pengujian
- **Go:** `ClientIP` tabel: tanpa trusted proxy (perilaku lama), satu proxy tepercaya, rantai `asli, nginx`, klien memalsukan XFF (`palsu, asli, nginx` → `asli`), semua tepercaya, IPv6, alamat tidak valid di XFF. Builder Caddy memasang `trusted_proxies` di semua route reverse_proxy bila setelan terisi, dan tidak bila kosong. Validasi setelan (CIDR/IP valid, maks 10, menolak `0.0.0.0/0`/`::/0` dengan pesan jelas).
- **Integrasi Caddy 2.6.2** (`RUNOVA_CADDY_TEST=1`): request dari "proxy" dengan `X-Forwarded-For: <asli>` → upstream menerima `<asli>, <proxy>` bila tepercaya, hanya `<proxy>` bila tidak.
- **E2E lab nginx** (skrip baru `test/e2e/nginx.sh`, Docker): nginx 1.18 (`ubuntu:22.04`, sertifikat self-signed termasuk wildcard) di depan stack e2e lokal, persis konfigurasi dari panduan: panel tanpa loop; HTTP → HTTPS satu kali; login gagal 5× dari "klien A" lalu "klien B" tetap bisa login (dua container klien dengan IP berbeda di jaringan Docker, bukan header palsu); "klien A" mencoba memalsukan `X-Forwarded-For` → tetap terkunci; WebSocket log berjalan; subdomain domain hosting dilayani; aktivitas login mencatat IP klien, bukan IP nginx.
- Panel (Playwright): kartu Server menampilkan dan menyimpan proxy tepercaya.
- bats: `--trusted-proxy` diteruskan `get.sh`/`install.sh` dan tersimpan.

## Di luar lingkup
Mode B (TLS passthrough), Cloudflare Tunnel, PROXY protocol (butuh Caddy lebih baru dari paket apt), konfigurasi nginx otomatis dari Runova.
