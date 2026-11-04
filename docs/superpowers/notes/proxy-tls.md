# Domain gagal "tls: internal error" di belakang nginx — catatan

## Akar masalah (terbukti)

Reproduksi di `test/e2e/nginx.sh` skenario 8 (nginx 1.18, Caddy 2.6.2, domain hosting `app.runova-lab.test`, subdomain `startboom`) pada kode v0.3.3 (commit 7fb0c48):

```
GAGAL [8 new hosting subdomain]: https://startboom.app.runova-lab.test/ lewat nginx = 502 (diharapkan 200 hello nginx);
status domain: {"status":"pending","detail":"Koneksi TLS gagal: remote error: tls: internal error"}
```

Log Caddy untuk nama itu: `obtaining certificate` → Let's Encrypt `rejectedIdentifier` dan ZeroSSL `failed getting EAB credentials`, berulang (`will retry`). Caddy tidak punya sertifikat, menjawab jabat tangan dengan alert `internal error`; nginx (`proxy_ssl_server_name on`) gagal handshake ke upstream → 502. Di produksi penyebabnya sama dengan jalan lain: TLS-ALPN-01 tidak pernah sampai ke Caddy (TLS berakhir di nginx) dan HTTP-01 hanya jalan bila port 80 nginx meneruskan ke Caddy; satu `return 301` di nginx cukup untuk membuat setiap subdomain baru gagal.

Lab lama tidak menangkapnya karena memakai `lab.localhost`: Caddy menerbitkan `*.localhost` dari CA internal dengan sendirinya. Lab kini memakai `app.runova-lab.test`.

## Keputusan

- **Mode front proxy = `trusted_proxies` tidak kosong.** Tidak ada setelan kedua: proxy tepercaya sudah berarti "ada reverse proxy di depan yang memegang TLS publik" (mode A satu-satunya yang didukung). Mengosongkan daftar mengembalikan issuer publik.
- **Kebijakan TLS catch-all tanpa `subjects`, issuer `internal`** (sama dengan `local_certs` Caddyfile), setelah kebijakan IP panel yang punya `subjects`. Tanpa `on_demand`. Automatic HTTPS tetap menyala di `runova-https`: Caddy tetap mengelola sertifikat per host dan server :80 pengalih; tidak ada ACME, jadi port 80 tidak wajib diteruskan nginx.
- **Probe status domain** (`caddy.Prober`): mode biasa tidak berubah (sertifikat Caddy lokal, root sistem). Mode front proxy: root CA internal dibaca lewat admin API `GET /pki/ca/local` (berkas `root.crt` milik user caddy, mode 0600, tidak bisa dibaca user runova) dan ditambahkan ke root sistem untuk jabat tangan ke 127.0.0.1:443; lalu alamat publik host (DNS, port 443) dicek dengan root sistem. Gagal hanya bila alamat publik menjawab dengan sertifikat yang ditolak (tidak dipercaya, nama lain, alert) — pesan menyebut nginx. Tidak ada DNS, NAT tanpa hairpin, atau timeout tidak menggagalkan, karena tidak membuktikan apa pun dan sering terjadi di LAN.
- **Pesan**: `tls.alert` dibungkus `net.OpError{Op: "remote error"}` (bukan `tls.AlertError`, itu untuk alert lokal) — cabang alert lama tidak pernah kena sehingga yang tampil "Koneksi TLS gagal: …". Kini ada pesan mode biasa sesuai brief dan pesan mode proxy; detail teknis asli di akhir dalam kurung.
- **Perubahan proxy**: `SaveSettings` sudah memuat ulang Caddy; kini juga menjalankan `RecheckDomains` di latar (semua domain yang dilayani, termasuk yang aktif, sampai 10 percobaan per domain berjeda 2 detik selama Caddy menerbitkan sertifikat baru). `Service.Go` menjadi kait test.
- **Perkiraan Let's Encrypt** di kartu Domain hosting tidak ditampilkan di mode front proxy (tidak ada sertifikat LE yang diminta).
- **nginx contoh**: blok port 80 kini `return 301 https://$host$request_uri`. Blok lama yang meneruskan port 80 tetap berfungsi (skenario 10): Caddy mengalihkan 308.
- **Pengerasan opsional `proxy_ssl_verify on`** butuh `proxy_ssl_session_reuse off`: tanpa itu nginx 1.18 melanjutkan sesi TLS upstream dari host lain di location yang sama dan gagal `upstream SSL certificate does not match "startboom…"` (direproduksi di skenario 10, lalu diperbaiki). Path root paket Ubuntu diverifikasi di `ubuntu:26.04` + `caddy 2.6.2-14`: `/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt` (0600 caddy).
- `KEEP=1 test/e2e/nginx.sh` membiarkan container lab untuk diperiksa.

## Bukti

- TDD: `TestBuildFrontProxyUsesInternalIssuerForEveryHost` gagal sebelum perbaikan (`policies = []`), lulus sesudahnya; `TestBuildWithoutFrontProxyKeepsPublicIssuers`, `TestProbeDirect`, `TestProbeFrontProxy`, `TestClientLocalCA`, `TestTrustedProxyChangeRechecksDomains`, `TestHostingListByRole` (LE).
- Integrasi `caddy:2.6.2` (`RUNOVA_CADDY_TEST=1`, 3×): `TestCaddyFrontProxyIntegration` — nama `.test` mendapat sertifikat internal dan probe lulus; tanpa proxy kembali ke issuer publik dan probe menampilkan pesan baru.
- e2e `nginx.sh` sesudah perbaikan: skenario 0–10 lulus (8: `startboom` 200 lewat nginx, penerbit Caddy Local Authority, status active, port 80 `return 301`; 10: port 80 diteruskan + `proxy_ssl_verify on`, subdomain baru langsung 200, 308 sekali, status active).
- `go test ./...` dan `go vet ./...` lulus dengan stack e2e mati (saat stack menyala, test `internal/api` gagal karena Caddy e2e memegang port 8443/20000 — bukan regresi).
- e2e `run.sh` (9 skenario) lalu `nginx.sh` (0–10) di stack yang sama lulus. shellcheck (image `koalaman/shellcheck:stable`) bersih untuk `test/e2e/*.sh`; bats tidak dijalankan (tidak terpasang; `install.sh` tidak disentuh).

## Langkah user setelah update

1. Pastikan **Proxy di depan Runova** berisi IP nginx (`sudo runova panel set --trusted-proxy 10.81.20.x`, alamat asal koneksi nginx yang terlihat dari server Runova). Bila sudah terisi, tidak perlu apa-apa: Caddy dimuat ulang saat Runova start.
2. nginx tidak perlu diubah. Port 80 boleh tetap diteruskan atau diganti `return 301`.
3. Klik **Cek ulang** atau tunggu ≤5 menit; status menjadi Aktif.
