# Arsitektur dan cara kerja

[English](architecture.md) · **Bahasa Indonesia**

Latar teknis: apa berjalan di mana, dan bagaimana isolasi, kuota, database, dan file bekerja. Untuk gambaran singkat berbahasa awam lihat [README](../README.id.md).

## Tujuan rancangan

Platform seperti Vercel atau Railway membangun aplikasi dari source code dan berjalan di cloud mereka. Banyak tim internal justru sudah punya artefak jadi dari pipeline sendiri (file `.jar` dari Maven, binary Go atau Rust dari CI) dan cukup butuh satu tempat di server kantor untuk menjalankannya dengan rapi:

- **Unggah, bukan build.** Runova tidak menyentuh source code. Ia membaca file yang diunggah: jenisnya, versi Java minimal, port, dan environment yang dirujuk.
- **Terpisah per pengguna.** Setiap aplikasi berjalan di container Docker sendiri dengan batas memori; semua aplikasi memakai core CPU server bersama dengan bobot sama. Pengguna tidak bisa melihat atau menyentuh aplikasi milik pengguna lain.
- **Uji dulu lewat IP, lalu pasang alamat.** Aplikasi bisa dibuka di `IP-server:port` untuk pengujian. Admin mendaftarkan domain wildcard sekali, lalu setiap pengguna memberi aplikasinya alamat `nama.domain-terdaftar` tanpa mengurus DNS; Caddy menerbitkan sertifikat HTTPS sendiri dan akses IP bisa ditutup.

## Yang boleh dijalankan

Admin memilih runtime mana yang diizinkan di server. Runova mendeteksi jenis file saat diunggah dan menampilkan logo bahasanya.

| Jenis | Contoh bahasa | Pilihan pengguna | Image dasar |
|---|---|---|---|
| **JAR** | Java, Kotlin, Scala, Clojure, Groovy | Java 8, 11, 17, 21, 25 | `eclipse-temurin:<versi>-jre` |
| **Binary statis** | Go, Rust (musl), Zig, Nim, Odin, V | – | `gcr.io/distroless/static-debian12` |
| **Binary glibc** | Rust, C, C++, .NET, Swift, Haskell, Crystal, Dart, OCaml, Bun, Deno | – | `ubuntu:26.04` |

- File `.jar` dibaca versi bytecode-nya. Versi Java yang terlalu lama untuk file itu tidak bisa dipilih, lengkap dengan alasannya.
- Binary dicek formatnya (harus ELF Linux) dan arsitekturnya (harus sama dengan server). File `.exe` Windows atau binary macOS ditolak dengan pesan yang jelas.

## Cara kerja deploy

Deploy dibagi empat langkah:

1. **Unggah.** Tarik file ke halaman. Hasil pembacaan tampil sebagai checklist.
2. **Runtime.** Pilih versi Java (untuk `.jar`), opsi JVM, dan port yang didengarkan aplikasi.
3. **Environment.** Variabel yang dirujuk aplikasi muncul sebagai form. Nilai rahasia disimpan terenkripsi.
4. **Resource & akses.** Bagikan memori dan disk dari sisa kuota akun (CPU dipakai bersama, tidak dipilih). Nyalakan akses IP untuk pengujian, dan isi domain kalau sudah siap.

Tombol **Deploy** baru aktif setelah semua yang wajib terpenuhi. Panel ringkasan menunjukkan apa yang masih kurang.

Setelah jalan, halaman aplikasi berisi kontrol (unggah versi baru, restart, stop, terminal), environment, domain, riwayat deployment dengan tombol kembalikan, log, monitoring, file, dan pengaturan lanjutan.

## File aplikasi

Setiap aplikasi punya **folder kerja**, yang di panel ditampilkan sebagai `<app>/` (misalnya `starboom/`). Folder ini adalah working directory container (`/data`, juga di `RUNOVA_DATA_DIR`), berada di server di `/var/lib/runova/data/<user>/<app>/`, dan isinya tetap ada saat deploy ulang, restart, maupun kembalikan versi. Bagian lain container read-only.

- Aplikasi yang menulis ke path relatif seperti `./storage` sudah menulis ke folder kerja (`starboom/storage`).
- Aplikasi yang menulis ke path absolut seperti `/app/storage` atau `/var/www/uploads` mendapat path itu **di-mount otomatis** dari subfolder folder kerja (`starboom/storage`). Runova menemukan folder semacam itu di `.env.example`, `application.properties`, dan `application.yml` (`spring.servlet.multipart.location`, `*.upload-dir`, `*.storage.*path*`, `STORAGE_PATH`, `UPLOAD_DIR`, …) tanpa menjalankan aplikasi; halaman Tinjau menampilkannya sudah tercentang di **Folder tersimpan**, dan bisa diubah di sana atau di tab **Lanjutan** aplikasi. Folder sistem (`/etc`, `/usr`, `/proc`, …), `/tmp`, dan file artefak sendiri ditolak.
- Tab **File** menampilkan isi folder kerja, mengunduh dan menghapus file, serta mengunggah file kecil.

## Pengguna dan kuota

- **Admin** mendaftarkan pengguna. Tidak ada pendaftaran mandiri.
- Setiap pengguna hanya melihat dan mengelola aplikasinya sendiri.
- Admin menentukan kuota per pengguna: **jumlah aplikasi, memori, jumlah database** (bawaan 3) **dan disk** (bawaan 1 GB). Pengguna membagikan kuota itu ke aplikasinya; CPU tidak termasuk kuota, semua aplikasi memakai seluruh core; tiap aplikasi mendapat ukuran disk saat deploy (bawaan 256 MB, bisa diubah di **Lanjutan**).
- Disk menghitung seluruh isi folder kerja aplikasi (termasuk folder tersimpan); artefak, log, dan database PostgreSQL tidak dihitung. Runova mengukur setiap folder kerja tiap 5 menit dan sebelum deploy. Di 80% pemilik diberi peringatan; di 100% aplikasi bertanda **Disk penuh**, pemilik mendapat notifikasi error, dan deploy baru aplikasi itu ditolak sampai ada file yang dihapus atau ukurannya dinaikkan. Aplikasi yang berjalan **tidak** dihentikan, jadi tidak ada data yang hilang. Ini batas lunak: aplikasi bisa menulis melewatinya sampai pengukuran berikutnya. Batas keras butuh filesystem dengan project quota (XFS `prjquota`) yang tidak ada di instalasi Ubuntu bawaan; itu pekerjaan lanjutan. Admin melihat pemakaian disk server dan bagian aplikasi di halaman **Server**.
- Aplikasi yang memakai memori melebihi batasnya dihentikan dan dijalankan ulang oleh Docker tanpa mengganggu aplikasi lain.

## Database

Runova memasang **PostgreSQL 18** di server yang sama (paket resmi Ubuntu 26.04).

- Setiap pengguna mendapat **satu akun PostgreSQL** saat didaftarkan admin, misalnya `budi`.
- Pengguna membuat database sendiri di panel, dengan nama berawalan akunnya (`budi_billing`, `budi_auth`). Jumlahnya dibatasi admin, bawaannya **3 per pengguna**.
- Hanya pemilik yang bisa masuk ke database-nya. Akun pengguna tidak punya hak superuser, tidak bisa membuat role, dan dibatasi jumlah koneksinya.
- Saat deploy, pengguna memilih database untuk aplikasinya (atau tanpa database). Runova mengisi `DATABASE_URL`, `SPRING_DATASOURCE_URL`, dan `PGHOST`/`PGPORT`/`PGUSER`/`PGPASSWORD`/`PGDATABASE` otomatis, ditambah setiap variabel database yang disebut artefak (`DB_URL`, `DB_USER`, `DB_PASSWORD`, `POSTGRES_PASSWORD`, `JDBC_URL`, `SPRING_R2DBC_URL`, `TYPEORM_*`, …) termasuk password-nya; barisnya tampil "Otomatis" dan tidak perlu diketik. Nilai yang diisi pengguna sendiri untuk variabel itu yang dipakai. Host-nya `host.docker.internal`: server ini dilihat dari dalam container (sama dengan `localhost` di server). Beberapa aplikasi milik pengguna yang sama boleh memakai database yang sama.
- Host, port, nama database, dan password bisa dilihat dan disalin di halaman **Database**, misalnya untuk dibuka dari DBeaver dari jaringan yang diizinkan admin, atau lewat SSH tunnel (`ssh -N -L 5433:127.0.0.1:5432 <user>@<server>`, lalu `localhost:5433`).

## Rencana teknis

| Bagian | Pilihan |
|---|---|
| Server target | Ubuntu Server 26.04 LTS, `linux/amd64` |
| Runova | Satu binary Go, frontend tertanam |
| Isolasi aplikasi | Docker, satu container per aplikasi, batas memori, CPU dipakai bersama dengan bobot sama |
| Domain & HTTPS | Caddy, sertifikat otomatis |
| Database | PostgreSQL 18 dari apt Ubuntu, service systemd di host. Hanya menerima koneksi dari jaringan Docker dan jaringan yang diizinkan admin |
| Akses IP | Container hanya terbuka di `127.0.0.1`. Nyala: Caddy membuka port akses IP aplikasi dan meneruskannya. Mati: port itu ditutup |
| Port aplikasi | Dialokasikan otomatis dari rentang yang diatur admin |
| IP publik server | Diisi admin sekali. Kalau kosong, panduan domain hanya menawarkan Cloudflare Tunnel |

Rancangan rinci ada di [`docs/spec.md`](spec.md). Rencana implementasi per bagian ada di `docs/superpowers/plans/`.

## Catatan tampilan panel

- Mode terang dan gelap mengikuti sistem, bisa dipaksa dari menu akun, padat seperti Dokploy dan Railway, tanpa gradien.
- Panel tidak memuat apa pun dari luar server: font, ikon, logo, dan xterm.js ada di `web/vendor/`, dan header CSP menolak sumber lain. Jadi panel jalan di server tanpa internet keluar.
- Di HP, menu pindah ke navigasi bawah. Selama deploy, navigasi bawah diganti tombol **Kembali** dan **Lanjut**.
- Ikon: [Lucide](https://lucide.dev). Logo bahasa: [Devicon](https://devicon.dev).
