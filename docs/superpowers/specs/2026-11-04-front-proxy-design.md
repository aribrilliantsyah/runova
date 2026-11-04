# Front proxy (nginx di depan Runova) — desain

Tanggal: 2026-11-04. Status: keputusan disetujui user di percakapan.

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

### 2. Panduan nginx resmi (`docs/nginx-front-proxy.md`, Inggris + `.id.md`)
Dua mode, keduanya diuji di lab (nginx 1.18 Ubuntu 22.04 + Caddy 2.6.2):
- **Mode A — terminate TLS di nginx (disarankan bila nginx sudah memegang sertifikat).** `proxy_pass https://RUNOVA` dengan `proxy_ssl_server_name on; proxy_ssl_name $host;`, header `X-Forwarded-*`, WebSocket (`Upgrade`/`Connection`), `client_max_body_size 600m`, `proxy_request_buffering off`, `proxy_read_timeout 1h`; port 80 diteruskan ke `http://RUNOVA` (untuk tantangan ACME Caddy dan redirect). Wildcard: sertifikat `*.porto.devkriyanesia.my.id` di nginx lewat **certbot + `python3-certbot-dns-cloudflare`** (paket apt Ubuntu; token API Cloudflare dengan izin Zone:DNS:Edit hanya untuk zona itu), dengan contoh perintah dan pembaruan otomatis. Di Runova: `--trusted-proxy <IP lokal nginx>`.
- **Mode B — TLS passthrough (`stream` + `ssl_preread`).** nginx meneruskan berdasarkan SNI tanpa membuka TLS; Caddy di Runova memegang semua sertifikat (subdomain otomatis tanpa certbot, tanpa token Cloudflare); situs lain di nginx tetap jalan (contoh dengan `127.0.0.1:8443`). IP asli diteruskan dengan **PROXY protocol** bila Caddy mendukungnya — Caddy 2.6.2 dari apt **tidak** punya listener `proxy_protocol` (diverifikasi: `caddy list-modules`), jadi di mode B Runova tetap melihat IP nginx. Panduan menyebut ini terus terang dan menyarankan mode A bila batas login per IP penting.
- Kedua mode: port 80 juga harus diteruskan; Cloudflare tetap **DNS only** (Proxied + SSL Flexible menyebabkan loop yang sama); firewall server Runova membuka 80/443 untuk IP nginx.
- Bagian "Pemecahan masalah": `ERR_TOO_MANY_REDIRECTS` (penyebab: `proxy_pass http://`, atau Cloudflare Flexible), 502 (IP/port Runova salah, firewall), sertifikat subdomain tidak cocok (mode A tanpa wildcard).

### 3. Domain hosting di balik nginx
- **Cek DNS** domain hosting sekarang membandingkan dengan `Settings.PublicIP`. Di balik nginx, record wildcard menunjuk ke IP publik server nginx, jadi admin mengisi **IP publik** = IP publik nginx (bukan IP lokal Runova). Panduan menyebutkannya; tidak ada perubahan kode.
- Status sertifikat domain (`ProbeTLS` ke `127.0.0.1:443` dengan SNI) tetap menguji Caddy lokal; di mode A sertifikat publik dipegang nginx, sehingga Caddy memakai sertifikat Let's Encrypt-nya sendiri via HTTP-01 (port 80 diteruskan nginx) — tidak berubah.

## Pengujian
- **Go:** `ClientIP` tabel: tanpa trusted proxy (perilaku lama), satu proxy tepercaya, rantai `asli, nginx`, klien memalsukan XFF (`palsu, asli, nginx` → `asli`), semua tepercaya, IPv6, alamat tidak valid di XFF. Builder Caddy memasang `trusted_proxies` di semua route reverse_proxy bila setelan terisi, dan tidak bila kosong. Validasi setelan (CIDR/IP valid, maks 10, menolak `0.0.0.0/0`/`::/0` dengan pesan jelas).
- **Integrasi Caddy 2.6.2** (`RUNOVA_CADDY_TEST=1`): request dari "proxy" dengan `X-Forwarded-For: <asli>` → upstream menerima `<asli>, <proxy>` bila tepercaya, hanya `<proxy>` bila tidak.
- **E2E lab nginx** (skrip baru `test/e2e/nginx.sh`, Docker): nginx 1.18 (`ubuntu:22.04`) di depan stack e2e, mode A: panel tanpa loop, login gagal dari dua "IP asli" berbeda (lewat `X-Forwarded-For` yang ditambahkan nginx dari `X-Real-IP` uji / atau dua container klien berbeda) → hanya satu yang terkunci; WebSocket log berjalan; subdomain domain hosting dilayani. Mode B: panel dan subdomain tanpa loop.
- Panel (Playwright): kartu Server menampilkan dan menyimpan proxy tepercaya.
- bats: `--trusted-proxy` diteruskan `get.sh`/`install.sh` dan tersimpan.

## Di luar lingkup
PROXY protocol (butuh Caddy lebih baru dari paket apt), mengganti Caddy dengan build kustom, konfigurasi nginx otomatis dari Runova.
