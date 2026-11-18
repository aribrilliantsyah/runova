# Admin melihat aplikasi dan deployment pengguna lain

Brief: `.brief-admin-view.md`. Permintaan: admin masuk ke aplikasi pengguna lain lewat Pengguna dan melihat apa yang berjalan.

## Keputusan

Prinsip: **admin melihat semuanya dan menjalankan aplikasi; apa yang dideploy, dikonfigurasi, atau disimpan pemilik tetap milik pemilik.** Admin mengoperasikan server (pengguna gagal, server penuh, aplikasi macet di malam hari), jadi ia perlu melihat dan bisa menghidupkan/mematikan. Mengubah isi aplikasi orang lain mengaburkan siapa yang bertanggung jawab atas apa yang berjalan dan membuka jalur untuk membaca rahasia; kebutuhan itu (jarang) dipenuhi dengan meminta pemilik, atau dari server lewat SSH.

Sebelumnya `Service.load` mengizinkan admin untuk **semua** aksi kecuali terminal (`part23-review.md` #2); panel hanya tidak menampilkannya. Sekarang batasnya ada di service (`Service.loadOwn` → `ErrOwnerOnly`, 403), jadi API dan panel sepakat dan tidak bisa dilewati dengan `curl`.

| Aksi | Admin | Alasan |
|---|---|---|
| Detail aplikasi, deployment + timeline, log deployment, monitoring | boleh | inti permintaan; hanya baca |
| Log live (WebSocket) | boleh, tercatat `log.view` | log bisa berisi data pengguna; tercatat paling sering sekali per 10 menit per admin per aplikasi (panel membuka ulang stream di setiap ganti tab/restart) |
| File: daftar, info, pratinjau | boleh | hanya baca, isi tetap tidak dirender (CSP sandbox) |
| File: unduh, zip | boleh, tercatat `file.download` | mengeluarkan data dari server, jadi dicatat |
| Restart, Stop, Jalankan | boleh, tercatat + pemilik dinotifikasi | operasional; mengubah state yang dilihat pemilik |
| Domain: tambah, hapus, cek | boleh, tercatat + pemilik dinotifikasi (tambah/hapus) | sudah diizinkan sebelumnya dan diandalkan (domain sendiri hanya bisa ditambah admin, `hosting-domains-design.md` §5); dipertahankan |
| Environment: daftar nama dan nilai non-rahasia | boleh | sama dengan yang diterima pemilik; rahasia dikosongkan server (`maskSecrets`) |
| Environment: lihat nilai rahasia (`reveal`) | **403** | tidak ada kebijakan existing yang membuka rahasia ke admin; reveal tetap pemilik saja |
| Environment: ubah | **403** | bisa mengubah perilaku dan kredensial aplikasi |
| Pengaturan (`PATCH`: memori, disk, Java, port, akses IP, mode deploy, database, folder) | **403** | sebelumnya diizinkan; dicabut karena mengubah konfigurasi milik pemilik (mis. mengikat database lain) |
| Unggah versi baru, deploy ulang, kembalikan versi | **403** | menjalankan kode/konfigurasi baru atas nama pemilik |
| File: unggah, folder baru, pindah, hapus | **403** | mengubah data pemilik |
| Terminal web | **403** (sebelumnya 404) | spec §5 dan review #2: shell dengan UID dan data pemilik; satu cookie admin yang dicuri = eksekusi kode di semua aplikasi |
| Hapus aplikasi | **403** | tidak bisa dibatalkan; admin tetap bisa menghapus pengguna beserta semua aplikasinya (`DeleteAllForUser` memakai `deleteApp` tanpa cek kepemilikan) |

Pengguna biasa tetap 404 untuk aplikasi orang lain di semua endpoint (HTTP dan WebSocket), sehingga ID tidak membocorkan keberadaan. Admin melihat 403 karena ia memang boleh tahu aplikasinya ada.

Terminal menjadi 403 untuk admin (bukan 404 seperti sebelumnya): admin sudah melihat aplikasinya, 404 hanya membingungkan. `TestTerminalOnlyInOwnContainer` diperbarui.

## Audit

- Target aksi admin pada aplikasi orang lain = `pemilik/aplikasi` (`apps.Target`), sehingga `target:budi/billing` atau `target:budi/*` menemukan semua yang dilakukan admin di aplikasi budi; aksi pemilik tetap bertarget nama aplikasi saja (kompatibel dengan entri lama).
- Aksi baru: `log.view`, `file.download` (hanya dicatat untuk admin di aplikasi orang lain; pemilik membaca miliknya tanpa dicatat).
- Notifikasi pemilik (level info): "Admin <nama> me-restart|menghentikan|menjalankan <aplikasi>", "Admin <nama> menambah alamat <host> ke <aplikasi>", "… menghapus alamat <host> dari <aplikasi>".

## API

- `GET /api/users/{id}` (admin): `{user: user + usage + last_login_at, apps, summary, databases, pg_down}`. Satu query yang sama dengan tabel Pengguna (`store.UserRowByID` = `UserList` dengan filter ID). Database dari `dbs.Service.List` (ukuran dari PostgreSQL; `pg_down` bila tidak terjangkau). 403 untuk non-admin, 404 bila tidak ada.
- `GET /api/apps?all=1` (admin) menambah `owners: {app_id: username}`.
- Tidak ada endpoint `GET /api/users/{id}/apps` terpisah: detail pengguna sudah memuat aplikasinya dan halaman hanya butuh satu permintaan.

## Panel

- **Pengguna**: nama dan sel Aplikasi menaut ke `#/users/<id>`, tombol "Lihat aplikasi" di setiap baris (di ponsel hanya ikon).
- **`#/users/<id>`** (`web/js/pages/user.js`, admin; non-admin dialihkan ke `#/apps` oleh router, API 403): profil, tombol Aktivitas (`pelaku:<username>`), empat ubin pemakaian/kuota (aplikasi, memori, disk terisi, database), tabel aplikasi (status, versi aktif, domain, memori, deploy terakhir; yang gagal di atas), daftar database (nama, aplikasi pemakai, ukuran). Kartu di ponsel.
- **Halaman aplikasi orang lain**: banner "Milik <pemilik> — kamu melihat sebagai admin" dengan apa yang boleh, tautan ke halaman pengguna, breadcrumb Pengguna / pemilik / aplikasi. Kontrol pemilik tidak ditampilkan (Unggah versi baru, hapus di header, Kembalikan) atau nonaktif dengan alasan (Terminal, saklar akses IP). Environment, Lanjutan, dan File menjadi tampilan baca. Konfirmasi Restart/Stop menyebut bahwa pemilik diberi tahu.
- **Aplikasi**: tombol "Semua pengguna" (admin) kini juga ada di keadaan kosong dan di ponsel; kartu aplikasi orang lain memberi chip pemilik; filter juga mencocokkan nama pemilik.
- Aplikasi yang tidak ada / milik orang lain (non-admin) kini menampilkan "Aplikasi tidak ditemukan" dengan tautan kembali, bukan halaman kosong dengan error di konsol.

## Pengujian

- `TestAdminAccessMatrix` (`internal/api/admin_access_test.go`): setiap endpoint aplikasi untuk admin (403 dengan pesan tetap / 200) dan pengguna lain (404), termasuk unggah multipart dan WebSocket log/terminal; tidak ada `exec`; data tidak berubah; restart/stop/start/domain berhasil untuk admin; Aktivitas `target:andi/billing pelaku:admin` memuat semua aksi; pemilik menerima 5 notifikasi. Diverifikasi merah: menetralkan `loadOwn` menghasilkan 12 kegagalan "want 403".
- `TestAdminUserDetail`: isi `GET /api/users/{id}`, 403 non-admin, 404 tak dikenal, `owners` di `?all=1`.
- `TestAccessIsOwnerOrAdmin` (apps): admin menghapus → `ErrOwnerOnly`.
- `web/test/smoke-admin-view.mjs`: Pengguna → pengguna → aplikasi, banner, Restart dengan notifikasi, env tanpa rahasia, log live, timeline deployment, File dan Lanjutan baca-saja, Aktivitas, Semua pengguna, pengguna biasa ditolak; `SHOTS=dir` menyimpan tangkapan 1440/360 terang/gelap.
- E2E skenario 6 (isolation): admin melihat aplikasi andi, ditolak PATCH dan terminal (403), restart tercatat `andi/hello`.

## Risiko

- Admin tidak lagi bisa mengubah pengaturan aplikasi pengguna lain (memori, akses IP) dari panel; sebelumnya bisa lewat API. Bila itu dibutuhkan (mis. menurunkan memori aplikasi yang menghabiskan server), jalurnya: hentikan aplikasinya dan minta pemilik, atau turunkan kuota pengguna.
- `log.view` dideduplikasi di memori proses; restart Runova memulai hitungan dari awal (paling banyak satu entri tambahan).
