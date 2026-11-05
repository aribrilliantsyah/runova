# Catatan perubahan

Semua perubahan penting Runova. Versi mengikuti [semantic versioning](https://semver.org/); yang terbaru di atas. English: [CHANGELOG.md](CHANGELOG.md).

Perbarui server dengan `sudo runova update` (server 0.2.0 atau lebih lama: jalankan perintah `curl … get.sh | sudo bash` sekali). Perubahan skema database dimigrasikan otomatis setelah cadangan (`runova.db.bak-v<N>-*`); `sudo runova rollback` kembali ke versi sebelumnya beserta database-nya.

## [0.3.11] - 2026-11-05

### Diubah
- README dibuat singkat untuk orang yang memasang Runova: apa yang didapat, screenshot baru (file manager, tabel dan query database, timeline deploy, aktivitas), cara pasang enam langkah, update, dan rollback.
- Materi teknis dipindah dari README ke dokumen sendiri, dalam bahasa Inggris dan Indonesia: `docs/architecture.md`, `docs/domains.md`, `docs/operations.md`, dan `docs/development.md`. Panduan instalasi menautkan ke dokumen itu.

## [0.3.10] - 2026-11-05

### Ditambahkan
- **Pengelola file di tab File.** Breadcrumb yang bisa diklik dengan folder tersimpan di URL (`#/apps/<id>/files?path=storage/2026`), tombol naik satu folder, pencarian, tampilan daftar dan kisi (gambar kecil), urutan Nama/Ukuran/Diubah dengan folder selalu di atas, jumlah isi tiap folder.
- Pilih banyak item (checkbox, Shift-klik, Ctrl+A) untuk diunduh sebagai satu ZIP atau dihapus bersama; keyboard: panah, Enter membuka, Backspace naik, Delete menghapus (dengan konfirmasi), F2 ganti nama.
- Menu baris (⋯ dan klik kanan): pratinjau, unduh (folder sebagai ZIP), ganti nama, pindahkan ke folder lain, salin path di aplikasi (`./storage/x.png`, `/data/storage/x.png`, `/app/storage/x.png`), hapus. Folder tersimpan menjelaskan kenapa tidak bisa diganti nama, dipindah, atau dihapus.
- Unggah banyak file sekaligus atau tarik ke daftar, dengan progres per file; folder baru.
- Panel pratinjau: gambar PNG/JPEG/GIF/WebP dan file teks sampai 1 MB (nomor baris), dengan ukuran, tipe terdeteksi, waktu, dan path. SVG, PDF, dan file lain hanya diunduh.
- Footer berisi jumlah item, ukuran folder, dan bar disk aplikasi (peringatan 80 %, penuh 100 %). Di ponsel baris ringkas dan aksi lewat sheet.
- API: `POST /api/apps/{id}/files/folder`, `POST …/files/move`, `GET …/files/zip`, `GET …/files/info`, `GET …/files/preview`; unggah banyak file. Aktivitas: `file.rename`, `file.mkdir`.
- **Tabel dan Query di halaman Database.** Setelah memilih database: daftar tabelnya (baris, ukuran, kolom dengan tipe, null, default, dan primary key), isi tabel 100 baris per halaman, Kosongkan (truncate) dan Hapus tabel (drop) dengan konfirmasi nama tabel.
- Editor query dengan nomor baris dan Ctrl+Enter: mode Baca (SELECT, WITH, EXPLAIN, SHOW dalam transaksi read-only, 10 detik) atau mode Tulis (juga INSERT, UPDATE, DELETE, TRUNCATE, CREATE/ALTER/DROP TABLE atau INDEX, 30 detik). Satu perintah per jalankan, hasil maksimal 1.000 baris / 5 MB, jumlah baris terpengaruh untuk DML, galat PostgreSQL beserta barisnya. Riwayat 50 query terakhir per database di browser; hasil bisa diunduh sebagai CSV.
- Restore database dari file `pg_dump -Fc` yang diunggah (sampai batas unggah, dengan konfirmasi nama database) dan **Export** dump terbaru sebagai unduhan.
- API: `GET /api/databases/{id}/tables`, `GET …/tables/{table}/data`, `POST …/tables/{table}/truncate`, `DELETE …/tables/{table}`, `POST …/query`, `POST …/restore-upload`, `GET …/export`. Aktivitas: `db.query`, `db.table_truncate`, `db.table_drop`, `db.restore_upload`, `db.export`.

### Diubah
- `DELETE /api/apps/{id}/files` menerima `{"paths": [...]}`; satu path yang ditolak membatalkan semuanya.
- Pekerjaan panel di database pengguna (tabel, query, restore, export) masuk dengan role pengguna itu lewat `127.0.0.1`, tidak pernah lewat role Runova; restore dari backup tersimpan juga. `pg_hba.d/runova.conf` milik Runova kini selalu mengizinkan `127.0.0.1/32` dengan `scram-sha-256`.
- Kartu di tab Lanjutan memakai satu tata letak: judul dan deskripsi di kiri, tombol simpan di kanan footer kartu.

### Diperbaiki
- Folder tersimpan: tombol "Tambah folder" dan "Simpan folder" tidak lagi berhimpitan. Kartu punya keadaan kosong yang jelas, tiap baris menyebut asalnya (terdeteksi atau manual), "Tambah folder" ada di header (di bawah daftar pada halaman Tinjau), "Simpan folder" di footer, aktif hanya setelah ada perubahan dan ditandai "Belum disimpan".

## [0.3.9] - 2026-11-05

### Ditambahkan
- `CHANGELOG.md` dan `CHANGELOG.id.md` untuk semua rilis.

### Diubah
- Catatan rilis GitHub diambil dari bagian changelog versinya (`scripts/changelog.sh`); tag tanpa bagian itu gagal sebelum apa pun dibangun.

### Diperbaiki
- README tidak lagi menyebut 0.3.4 belum rilis.

## [0.3.8] - 2026-11-05

Skema 4 → 5.

### Ditambahkan
- **Workspace aplikasi.** Setiap aplikasi punya workspace yang tampil di panel sebagai `<app>/` (server: `/var/lib/runova/data/<user>/<app>`; container: `/data`, direktori kerja). Path relatif seperti `./storage` masuk ke sana dan tetap ada setelah deploy ulang dan restart.
- **Folder tersimpan terdeteksi otomatis** dari `.env.example`, `application.properties`, dan `application.yml` (`STORAGE_PATH=./storage`, `app.upload-dir`, `spring.servlet.multipart.location`, `${user.dir}/uploads`, …), tampil di halaman Tinjau dan bisa diatur di Lanjutan → Folder tersimpan. Path absolut seperti `/app/storage` dipasang dari workspace, jadi aplikasi yang menulis ke sana tidak lagi gagal "read-only file system". Path sistem, `/tmp`, dan `..` ditolak; link tidak pernah diikuti.
- **Kuota disk per aplikasi.** Pengguna mendapat kuota Disk (bawaan 1024 MB) yang dibagi ke aplikasinya; tiap aplikasi memilih ukuran saat deploy (preset 256/512/1024 MB). Workspace diukur tiap 5 menit dan saat deploy/unggah: peringatan di 80 %, "Disk penuh" dan deploy baru ditolak di 100 %. Aplikasi yang berjalan tidak dihentikan (batas lunak; batas keras butuh XFS project quota).
- **Tab File**: jelajah, unduh, hapus, dan unggah file di workspace (pemilik dan admin; unduhan selalu sebagai file).
- Pemakaian disk di Umum, kartu aplikasi, Pengguna, dan Server.

### Diubah
- `PATCH /api/users/{id}` menggabungkan `quota` ke kuota tersimpan; field yang tidak dikirim tetap.

### Pembaruan
- Aplikasi lama tetap berjalan dengan disk "belum dibatasi" sampai pemiliknya memilih ukuran di Lanjutan. Aplikasi yang menulis ke path absolut: tambahkan path itu di Lanjutan → Folder tersimpan, lalu deploy ulang.

## [0.3.7] - 2026-11-05

### Diperbaiki
- **Deploy ulang aplikasi Spring Boot gagal di "Menunggu aplikasi siap".** Batas 60 detik lebih pendek dari waktu start Spring Boot di 0,5 core (57 detik sendiri, 69 detik saat deploy ulang tanpa jeda). JAR kini menunggu 90 detik per core (minimal 3 menit, maksimal 15 menit), lebih lama selama JVM masih menulis log start-up.
- Restart dan Jalankan menunggu port aplikasi terbuka: "Memulai…", lalu "Berjalan" atau "Gagal" beserta alasannya.
- Health check yang gagal menyebut lama JVM berjalan, baris log terakhir, dan port Tomcat dari log.

### Diubah
- Label port: "Port aplikasi (di dalam container)", "Akses IP", dan port host internal hanya di detail teknis. Port yang dikosongkan di halaman Deploy menampilkan dan menyimpan port yang akan dipakai.

## [0.3.6] - 2026-11-05

### Diperbaiki
- Menutup sidebar membuat halaman kosong. Konten kini mengisi layar dan sidebar bergeser halus (tanpa animasi bila gerak dikurangi).

## [0.3.5] - 2026-11-05

### Diperbaiki
- Mengetik di kolom variabel environment halaman Deploy kehilangan fokus setiap satu huruf.

## [0.3.4] - 2026-11-05

Skema 2 → 4.

### Ditambahkan
- **Timeline deployment.** Setiap deployment mencatat langkahnya (unggahan diperiksa → image runtime → jaringan/data/port → buat → jalankan → tunggu siap → alihkan lalu lintas → hentikan versi lama) beserta durasi, tampil live.
- Diagnosis gagal dalam bahasa awam beserta saran dan detail teknis (port tidak terbuka tepat waktu, OOMKilled, versi Java, Caddy menolak alamat, crash loop dengan exit code terakhir).
- Log container disimpan per deployment, jadi aplikasi yang gagal atau berhenti tetap punya log; penampil log lebar penuh dengan wrap, cari, filter level, stack trace terlipat, salin, unduh, dan lompat ke error pertama.
- **Variabel database terisi otomatis.** Memilih database mengisi variabel terdeteksi seperti `DB_URL`, `DB_HOST`, `DB_USER`, `DB_PASSWORD`, `JDBC_URL`, `SPRING_DATASOURCE_*` (format URL mengikuti aplikasi), termasuk password, tanpa pernah dikirim ke browser; masing-masing bisa diisi sendiri. Petunjuk SSH tunnel di halaman Database.
- **Aktivitas gaya ELK**: sintaks pencarian (`aksi:` `pelaku:` `target:` `ip:` `hasil:`, frasa, pengecualian), rentang waktu, histogram dengan zoom, facet, baris yang bisa dibuka, paging, live.
- **Daftar Pengguna**: cari, filter peran dengan jumlah, kolom bisa diurutkan, paging, bar pemakaian.

### Diubah
- **Halaman Deploy**: unggah, lalu satu halaman Tinjau dengan nama aplikasi di atas, hasil deteksi runtime/Java/port, alamat, database, variabel wajib di depan, preset ukuran, opsi lanjutan terlipat; setelah itu progres deploy tampil live.
- Deploy yang gagal membiarkan aplikasi yang berjalan tetap "Berjalan" dengan peringatan, bukan "Gagal".
- Link akses IP memakai alamat LAN bila IP publik milik proxy; Cek DNS menawarkan "Pakai IP ini" di belakang NAT.
- `GET /api/users` berhalaman (`{items, total, …}`); `GET /api/usage` dihapus.

### Diperbaiki
- JAR mendapat `SERVER_PORT` selain `PORT`, jadi Spring Boot membuka port yang diperiksa Runova.
- Di belakang nginx, subdomain hosting baru gagal dengan `tls: internal error` / 502. Dengan proxy tepercaya terisi, Caddy menerbitkan semua sertifikat dari CA internalnya; nginx boleh mengalihkan port 80 sendiri. Status domain menyebut penyebabnya.
- Badge status, kartu error, kartu halaman Server.
- Pemindaian env binary tidak lagi melaporkan label TLS (`CLIENT_HANDSHAKE_TRAFFIC_SECRET`) sebagai variabel.

### Pembaruan
- Di belakang nginx: pastikan Proxy di depan Runova berisi IP nginx (`sudo runova panel set --trusted-proxy <IP>`). nginx tidak perlu diubah.

## [0.3.3] - 2026-11-04

### Ditambahkan
- **Proxy tepercaya** (`sudo runova panel set --trusted-proxy <IP>`, kartu Server, installer `--trusted-proxy`): di belakang nginx, batas login dan log aktivitas memakai IP asli pengunjung; `X-Forwarded-For` palsu diabaikan.
- Panduan [nginx di depan Runova](docs/nginx-front-proxy.id.md) dengan contoh konfigurasi yang diuji, sertifikat wildcard lewat certbot dan DNS Cloudflare, serta pemecahan masalah `ERR_TOO_MANY_REDIRECTS`.

## [0.3.2] - 2026-11-04

### Diubah
- Ikon aplikasi gaya macOS (roket di atas squircle gelap) untuk logo panel, favicon, touch icon, dan README.

## [0.3.1] - 2026-11-04

### Diubah
- Keluaran terminal installer, `get.sh`, dan binary `runova` dalam bahasa Inggris, dengan progres per langkah dan berkas log (`/var/log/runova-install.log`).
- README dan panduan instalasi berbahasa Inggris secara bawaan; bahasa Indonesia di `*.id.md`.

### Diperbaiki
- Instalasi interaktif dengan `curl … | sudo bash` berhenti diam di Ubuntu 26.04 (sudo-rs). `get.sh` kini bertanya sendiri di terminal.

## [0.3.0] - 2026-11-04

### Ditambahkan
- Banner khusus admin bila ada rilis baru (dicek server tiap 6 jam; bisa dimatikan di Server).
- `sudo runova update [--check] [--version X.Y.Z] [--yes]` dan `sudo runova rollback` (binary sebelumnya dan cadangan database).

## [0.2.0] - 2026-11-03

Skema 1 → 2.

### Ditambahkan
- **Domain hosting**: admin mendaftarkan domain wildcard; pengguna memilih `<label>.<domain>` untuk aplikasinya tanpa mengatur DNS. Cek DNS, sertifikat per subdomain, kuota subdomain = kuota aplikasi.

## [0.1.0] - 2026-11-03

Rilis pertama.

### Ditambahkan
- Installer satu perintah untuk Ubuntu Server 26.04 (Docker, Caddy, PostgreSQL 18, systemd, ufw), panel lewat domain (HTTPS) atau IP:port.
- Unggah `.jar` (Java 8–25), binary Linux statis atau glibc, atau `.tar.gz` dengan `.env.example`; deteksi runtime, port, dan environment; logo bahasa.
- Satu container Docker per aplikasi yang dikeraskan (root read-only, tanpa capability, jaringan sendiri per pengguna), deploy tanpa jeda atau stop-start, rollback, deteksi crash loop, log, terminal web, monitoring.
- Domain dengan HTTPS otomatis lewat Caddy, akses IP per aplikasi.
- Role PostgreSQL per pengguna, database dengan backup harian, restore, dan unduh.
- Pengguna dengan kuota (aplikasi, memori, CPU, database), batas login, log aktivitas, notifikasi.
- Migrasi database berversi dengan cadangan otomatis.

[0.3.11]: https://github.com/aribrilliantsyah/runova/compare/v0.3.10...v0.3.11
[0.3.10]: https://github.com/aribrilliantsyah/runova/compare/v0.3.9...v0.3.10
[0.3.9]: https://github.com/aribrilliantsyah/runova/compare/v0.3.8...v0.3.9
[0.3.8]: https://github.com/aribrilliantsyah/runova/compare/v0.3.7...v0.3.8
[0.3.7]: https://github.com/aribrilliantsyah/runova/compare/v0.3.6...v0.3.7
[0.3.6]: https://github.com/aribrilliantsyah/runova/compare/v0.3.5...v0.3.6
[0.3.5]: https://github.com/aribrilliantsyah/runova/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/aribrilliantsyah/runova/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/aribrilliantsyah/runova/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/aribrilliantsyah/runova/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/aribrilliantsyah/runova/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/aribrilliantsyah/runova/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/aribrilliantsyah/runova/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/aribrilliantsyah/runova/releases/tag/v0.1.0
