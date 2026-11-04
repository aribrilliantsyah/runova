# Front proxy (nginx mode A) — catatan

- `auth.ClientIP` sekarang menerima `[]netip.Prefix` (dari `auth.ParseTrusted`) dan menelusuri `X-Forwarded-For` dari kanan; semua baris header digabung. Entri yang tidak bisa di-parse menghentikan penelusuran dan hasilnya IP peer (loopback), bukan entri di kirinya. Tanpa proxy tepercaya hasilnya entri terakhir (IP peer yang ditambahkan Caddy), bukan lagi entri pertama.
- Pembacaan setelan per permintaan login/aktivitas lewat `server.clientIP` (satu query SQLite kecil); bila gagal baca, dianggap tanpa proxy tepercaya dan dicatat.
- Mengubah `trusted_proxies` lewat `PUT /api/settings` memuat ulang Caddy (`apps.SaveSettings`, dikembalikan bila Caddy menolak). Bagian ini tidak ada di spec tapi dibutuhkan: tanpa itu Caddy memakai daftar lama sampai reload berikutnya.
- Validasi menolak prefiks panjang 0 (`0.0.0.0/0`, `::/0`, `0.0.0.0/00`). Installer memeriksa hal yang sama di bash (`valid_proxy`).
- `runova panel set --trusted-proxy` mengganti seluruh daftar; `""` mengosongkan.
- Konfigurasi nginx: satu sumber, `docs/examples/nginx-runova.conf`; kedua panduan memuatnya setelah penanda `<!-- nginx-runova.conf -->` dan `test/e2e/nginx.sh` (skenario 0) gagal bila isi berbeda. nginx hanya diberi pengganti nama/alamat lewat `sed`.
- `test/e2e/lib.sh` baru: helper dipindah dari `run.sh` agar dipakai bersama `nginx.sh`. Lab memakai jaringan Docker 172.29.77.0/24 (gateway = host tempat Caddy e2e mendengar 80/443); ubah `SUBNET` bila bentrok.
- Bukti: go test, Caddy integrasi 5×, bats (86), shellcheck, 8 skrip web, e2e run.sh (9 skenario), e2e nginx.sh (skenario 0–9), test/system/run.sh (8 skenario) semua lulus.
