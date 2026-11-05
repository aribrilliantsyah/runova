# Health wait: "Deploy ulang"/Restart gagal di app Java + port yang membingungkan

Branch `fix/health-wait` (dari `main` = v0.3.5). Brief: `.brief-health-wait.md`.

## Laporan

Produksi, Spring Boot 4.1.2 `starboom`, Java 21: "Menunggu aplikasi siap 1 mnt 1 dtk — Aplikasi tidak membuka port 8080 dalam 60 detik" saat deploy ulang; port 20000 vs 8080 membingungkan.

## Akar masalah (terbukti)

**Batas health 60 detik tetap lebih pendek dari waktu start JVM di CPU kecil.** Diukur di Docker dengan batas Runova (`--cpus`, `-m 512m`, rootfs read-only, `/tmp` tmpfs), Spring Boot 4.1.2 dibangun offline dari cache Maven (`/tmp/runova-health/starboom*`):

| app | CPU | kondisi | port siap |
|---|---|---|---|
| webmvc saja | 0,5 core | sendiri | 23,6 s ("process running for 23.7") |
| webmvc + JPA + PostgreSQL | 0,5 core | sendiri | 57,3 s ("Started … in 49.4 seconds (process running for 55.8)") |
| webmvc + JPA + PostgreSQL | 0,5 core | JVM lama masih jalan (deploy ulang tanpa jeda, bawaan) | 69,5 s ("Started … in 60.4 seconds (process running for 67.8)") |
| webmvc + JPA + PostgreSQL | 0,5 core ×2 | dua JVM dingin sekaligus | 66,0 s / 68,4 s |

0,5 core = preset "Sedang" (bawaan untuk JAR). Deploy pertama lolos pas di bawah 60 s; deploy ulang tanpa jeda (dua JVM berebut CPU host) melewati 60 s → gagal "1 mnt 1 dtk". Cocok dengan laporan.

Reproduksi:
- Go (fake engine, jam health maju 1 s per bacaan): `TestJarRedeployWaitsForSlowJVM` (57 s lalu 70 s), `TestJarHealthWaitScalesWithCPU` — gagal sebelum perbaikan dengan `aplikasi tidak menerima koneksi di port 8080 dalam 1m0s`.
- Docker nyata: `TestIntegrationSlowJVMDeployRedeployRestart` (`RUNOVA_DOCKER_TEST=1`, JAR Java 21 yang menulis log start lalu membuka `SERVER_PORT` setelah 70 s, 0,5 core). Sebelum perbaikan (commit `36f402c`): `first deploy: aplikasi tidak menerima koneksi di port 8080 dalam 1m0s (120 percobaan)`. Sesudah: deploy 1m14s, deploy ulang 1m19s, restart 1m12s, semuanya Berjalan.

## Hipotesis yang dibantah

- **Port beda antara deploy pertama dan deploy ulang.** Tidak: `Create` menyimpan `app.internal_port = report.port` bila kosong; `PrepareRedeploy` mengkloning dengan `app.InternalPort`; `ContainerSpec` mengisi `PORT` dan `SERVER_PORT` dari deployment. Dikunci oleh `TestRedeployKeepsStoredAppPort` (port terdeteksi 9090 tetap 9090 di semua versi). Pesan "port 8080" di laporan memang port aplikasi; "20000" = port akses IP (listener Caddy). Kebingungan murni label (lihat di bawah).
- **Probe `accepts()` salah membaca Tomcat.** Tidak: probe yang sama (Go, 300 ms read deadline) ke Tomcat Spring Boot 4.1.2 nyata: EOF selama proxy Docker belum punya pendengar, lalu `i/o timeout` = sehat begitu Tomcat listen. Tomcat tidak menutup koneksi idle < 300 ms. Probe HTTP tidak diperlukan.
- **Port host bentrok di mode tanpa jeda.** Tidak: `allocHostPort` memberi port host berbeda per deployment (`TestZeroDowntimeSwitchesUpstream`).
- **Container lama / crash counter.** Tidak berperan: `noteStop` dipanggil sebelum stop, die saat app tidak running hanya mengonsumsi catatan stop.

## Keputusan

- **Batas tunggu per runtime/CPU** (`healthTimeoutFor`): binary 60 s (tetap); JAR `90 s ÷ core`, min 180 s, maks 15 menit (0,5 core = 3 menit, 0,25 = 6 menit, ≥0,5 core = 3 menit). Alasan: start JVM dibatasi CPU (data di atas, ~70 s di 0,5 core dengan JVM kedua) → margin 2,5×. Tidak dibuat setelan per app: tidak ada kebutuhan yang terbukti dan menambah permukaan UI; `HealthTimeout` di Service tetap ada untuk tes/dev.
- **Jangan gagal dini saat JVM masih start**: setelah batas lewat, JAR yang log-nya bertambah dalam 30 s terakhir dan belum menulis `Started … in N seconds` ditunggu terus sampai 2× batas (`startStall`, dibaca tiap 5 s, 50 baris). Log yang diam → gagal di batas (`TestJarSilentPastTimeoutFails`).
- **Restart/Jalankan menunggu siap**: status baru `starting` ("Memulai…"), `awaitStart` di latar (lock app tidak ditahan agar Stop tetap bisa). Port sehat → `running`; tidak sehat → `failed` + diagnosis `health_timeout` di deployment aktif + notifikasi; container tidak dihapus (log tetap bisa dibaca). Hasil hanya dicatat bila app masih `starting` pada deployment yang sama. Selama `starting`: Caddy tetap me-route, monitor menghitung crash, stats dan terminal tersedia (`AppStatus.Up()`). Deploy yang gagal saat app `starting` mengembalikan `running` (penunggu start tidak lagi mencatat).
- **Pesan gagal**: judul tetap "Aplikasi tidak membuka port X dalam N detik" (N = lama menunggu sebenarnya). Hint:
  - log punya `Started <App> in N seconds` + `Tomcat started on port P` ≠ X → "Aplikasi sudah start dalam N detik di port P, sedangkan Runova menunggu port X. Ubah Port aplikasi di tab Lanjutan menjadi P…";
  - `Started` di port X → "…tetapi port itu tidak menerima koneksi. Pastikan 0.0.0.0…";
  - JAR belum `Started` → "JVM sudah berjalan <durasi> tetapi belum selesai start… <core> core… naikkan CPU" + baris log terakhir.
- **Port di panel**: "Port aplikasi (di dalam container)" di wizard/Umum/Lanjutan/detail deployment; "Akses IP: <host>:<ip_port>" di Umum; port host internal `127.0.0.1:<port>` hanya sebagai "Port host internal" (tooltip: tujuan Caddy, tidak dibuka ke luar). Wizard: field kosong menampilkan "Dipakai: 8080 (bawaan Runova…)" / "(dari application.properties)" dan mengirim nilai itu secara eksplisit.
- Timeline: langkah health menulis "Menunggu port 8080 (paling lama 3 menit)" dan progres "… dari paling lama …"; saat diperpanjang "JVM masih start (log terus bertambah)".

## Yang perlu dilakukan user setelah update

Tidak ada migrasi. Setelah update: tekan **Deploy ulang** (atau Restart). App Java di 0,5 core kini ditunggu sampai 3 menit (lebih bila log masih bergerak). Bila tetap gagal, baca hint: ia menyebut port yang dicetak Tomcat atau baris log terakhir JVM. Untuk start lebih cepat, naikkan CPU di tab Lanjutan (deploy tanpa jeda menjalankan dua JVM sekaligus sebentar).
