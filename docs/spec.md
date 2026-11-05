# Runova: spesifikasi

Dokumen ini mengunci keputusan desain. README menjelaskan produk; dokumen ini menjelaskan cara membangunnya. Preview tampilan: `preview-dashboard.html`.

## 1. Lingkup dan target

- Panel self-hosted untuk menjalankan **artefak hasil build** (bukan source code): `.jar`, binary Linux statis, binary Linux glibc.
- Server target: **Ubuntu Server 26.04 LTS, `linux/amd64`**, apa adanya (tanpa disk/partisi khusus).
- **Instalasi minimal:** semua dependensi dari apt resmi Ubuntu (`docker.io`, `caddy`, `postgresql-18`). Tidak ada Kubernetes, tidak ada registry, tidak ada repo apt pihak ketiga.
- Runova = **satu binary Go** dengan frontend tertanam (`embed`), HTML/CSS/JS biasa tanpa framework dan tanpa proses build.

## 2. Arsitektur

```
Browser ──HTTPS──► Caddy (:80/:443) ──► runova (127.0.0.1:7070, systemd)
                     │                     ├─ SQLite /var/lib/runova/runova.db
                     │                     ├─ Docker Engine API (/var/run/docker.sock)
                     │                     ├─ Caddy admin API (127.0.0.1:2019)
                     │                     └─ PostgreSQL 18 (peer auth, role `runova` CREATEDB CREATEROLE)
                     ├──► container aplikasi via 127.0.0.1:<port acak>  (domain)
                     └─ :20000–20999 (akses IP, listener Caddy) ──► container yang sama
```

- `runova` berjalan sebagai service systemd, user Linux `runova`, anggota grup `docker`, dengan `AmbientCapabilities=CAP_CHOWN CAP_FOWNER CAP_DAC_OVERRIDE` (untuk folder `/data` milik UID pengguna). Akses Docker setara root: panel harus dijaga (HTTPS, password kuat, batas percobaan login).
- **Semua lalu lintas aplikasi lewat Caddy.** Container hanya mem-publish port internalnya ke `127.0.0.1:<port acak>`. Domain dan akses IP sama-sama route Caddy ke port itu, sehingga deploy tanpa jeda cukup mengganti upstream.
- Caddy dikonfigurasi lewat admin API-nya (localhost saja). Runova adalah sumber kebenaran: konfigurasi lengkap dibangun dari SQLite dan dikirim dengan `POST /load` saat start, setiap perubahan, dan saat Runova mendeteksi Caddy restart (cek tiap 30 detik).
- Setiap pengguna punya jaringan Docker sendiri (`runova-u<id>`, subnet `172.30.<n>.0/24`, n = 1–254, sehingga maksimal 254 pengguna); container milik pengguna lain tidak saling terjangkau.
- Runova berbicara langsung dengan Docker Engine API lewat `/var/run/docker.sock` (API `v1.44`), tanpa SDK.

### Lokasi file

| Path | Isi | Izin |
|---|---|---|
| `/etc/runova/runova.env` | Konfigurasi (dibaca systemd `EnvironmentFile`) | root:runova 0640 |
| `/etc/runova/secret.key` | Kunci enkripsi 32 byte | runova 0600 |
| `/var/lib/runova/runova.db` | SQLite: pengguna, sesi, aplikasi, versi, domain, env terenkripsi, aktivitas | runova 0600 |
| `/var/lib/runova/artifacts/<user>/<app>/<versi>/` | File yang diunggah, 10 versi terakhir per aplikasi | runova |
| `/var/lib/runova/data/<user>/<app>/` | Folder kerja aplikasi (panel: `<app>/`), di-mount ke `/data` | UID pengguna |
| `/var/lib/runova/mounts/<user>/<app>/<folder>/` | Sumber folder tersimpan ber-path absolut; folder `<app>` milik runova 0700, `<folder>` milik UID pengguna | runova / UID pengguna |
| `/var/lib/runova/backup/<db>/<tanggal>.dump` | Backup PostgreSQL harian, 7 hari | runova |

### Konfigurasi (`runova.env`)

| Variabel | Bawaan | Arti |
|---|---|---|
| `RUNOVA_LISTEN` | `127.0.0.1:7070` | Alamat HTTP panel (di belakang Caddy) |
| `RUNOVA_DATA` | `/var/lib/runova` | Akar data |
| `RUNOVA_SECRET_KEY` | `/etc/runova/secret.key` | File kunci enkripsi |

Semua setelan lain (kuota bawaan, batas unggahan, rentang port, runtime yang diizinkan, IP publik, alamat panel) disimpan di SQLite dan diubah admin lewat panel.

## 3. Pengguna, login, kuota

- Admin mendaftarkan pengguna; tidak ada pendaftaran mandiri. Peran: `admin`, `user`.
- **Username:** `^[a-z][a-z0-9]{1,30}$`, unik. Tanpa garis bawah, karena username menjadi awalan nama database (`budi_billing`). Dicadangkan: `postgres`, `runova`, `root`, `public`, `template`.
- **Password:** 10–200 karakter, bcrypt (input di-SHA-256 dulu agar >72 byte tetap berarti).
- **Sesi:** cookie `runova_session`, `HttpOnly`, `SameSite=Lax`, `Secure` bila diakses lewat HTTPS; berlaku 7 hari; token acak 32 byte, disimpan di SQLite sebagai hash SHA-256. Ganti password atau hapus pengguna menghapus semua sesinya.
- **Batas percobaan login:** 5 gagal dalam 15 menit untuk pasangan (username, IP) → dikunci 15 menit. IP diambil dari `X-Forwarded-For` hanya bila koneksi datang dari loopback (Caddy).
- **Admin pertama** dibuat installer lewat `runova admin create <username>`; password acak 20 karakter ditampilkan sekali di terminal.
- **Mutasi API** wajib `Content-Type: application/json` (atau multipart untuk unggahan) dan header `Origin` (bila ada) harus sama dengan host permintaan. Bersama `SameSite=Lax` ini menutup CSRF.
- Admin tidak bisa menghapus atau menurunkan peran dirinya sendiri, dan admin terakhir tidak bisa dihapus/diturunkan.
- 2FA tidak termasuk versi pertama.

### Kuota per pengguna

| Kuota | Bawaan pengguna baru |
|---|---|
| Jumlah aplikasi | 3 |
| Memori | 1024 MB |
| CPU | 1000 millicore (1 core) |
| Jumlah database | 3 |
| Koneksi PostgreSQL | 20 |
| Disk | 1024 MB |

- **Disk** dibagi ke aplikasi saat deploy seperti memori: tiap aplikasi punya ukuran disk (kelipatan 64 MB, minimal 64; bawaan 256 MB atau sisa kuota bila lebih kecil; preset Kecil/Sedang/Besar = 256/512/1024 MB). Yang dihitung: seluruh isi folder kerja aplikasi (folder data + sumber folder tersimpan), file biasa saja, link tidak diikuti. Artefak, log container, dan database PostgreSQL tidak dihitung. Aplikasi yang dibuat sebelum migrasi `0005` punya ukuran 0 = diukur, tidak dibatasi, sampai ukuran dipilih di tab Lanjutan.
- **Penegakan disk lunak:** Runova mengukur semua folder kerja tiap 5 menit (`filepath.WalkDir`, batas 1 menit per aplikasi), sebelum deploy ulang/kembalikan/versi baru, dan setelah perubahan lewat tab File. ≥80% → notifikasi peringatan; ≥100% → notifikasi error, status "Disk penuh", deploy baru aplikasi itu ditolak (409) sampai pemakaian turun atau ukuran dinaikkan. Notifikasi hanya saat level naik. Aplikasi yang berjalan tidak dihentikan. Batas keras (penulisan ditolak filesystem) butuh project quota XFS/ext4 dan menjadi pekerjaan lanjutan. Halaman Server menampilkan disk server dan total disk aplikasi (dialokasikan dan terpakai).
- Bawaan bisa diubah admin; nilai per pengguna diubah lewat tombol Kuota. Menurunkan kuota di bawah pemakaian diperbolehkan; yang sudah berjalan tetap, alokasi baru ditolak sampai pemakaian di bawah kuota.

### Setelan server (bawaan)

| Setelan | Bawaan |
|---|---|
| Batas ukuran unggahan | 500 MB |
| Rentang port akses IP | 20000–20999 |
| Runtime diizinkan | Java 8, 11, 17, 21, 25; binary statis; binary glibc |
| IP publik server | kosong (panduan domain hanya menawarkan Cloudflare Tunnel) |

## 4. Membaca artefak

Unggahan diterima sebagai salah satu dari:

1. `.jar`
2. binary ELF
3. `.tar.gz` berisi tepat satu artefak (1 atau 2) di akarnya, opsional `.env.example`

Deteksi:

- **JAR:** ZIP valid dengan `META-INF/MANIFEST.MF` berisi `Main-Class` (atau Spring Boot `Start-Class`). Versi Java minimal = versi bytecode tertinggi dari kelas aplikasi (major 52=8, 55=11, 61=17, 65=21, 69=25; versi di antara dibulatkan ke LTS berikutnya). Versi Java di bawah minimal tidak bisa dipilih, dengan alasannya.
- **ELF:** harus `ELFCLASS64`, `EM_X86_64`, OS Linux. Tanpa `PT_INTERP` = statis; dengan interpreter glibc (`/lib64/ld-linux-x86-64.so.2`) = glibc. Interpreter musl atau lainnya ditolak dengan pesan jelas. PE (`MZ`) dan Mach-O ditolak dengan pesan jelas.
- **Env:** `.env.example` (sumber utama; komentar `# secret` atau nama berisi `PASSWORD|SECRET|TOKEN|KEY` = rahasia). Cadangan: properti di `application.properties`/`application.yml` di dalam JAR yang merujuk `${NAMA}`, lalu scan string binary untuk pola `[A-Z][A-Z0-9_]{2,}` yang muncul bersama `Getenv`/`env::var`/`getenv`; hasil scan ditandai "kemungkinan".
- **Folder tulis:** pasangan nama=nilai di `.env.example`, `application.properties`, dan `application.yml` (kunci bertitik). Nama dipecah per kata (`app.uploadDir` → APP UPLOAD DIR). Ditolak bila ada kata URL/URI/ENDPOINT/PREFIX/HOST/DRIVER/BUCKET/NAME/DB/DATABASE/DATASOURCE/PGDATA/LEVEL/PORT/MODE/TYPE/SIZE/MAX/MIN/LIMIT/ENABLED/KEY/SECRET/TOKEN/PASSWORD/USER/…. Nilai: tanpa kutip, `file:` dibuang, `${X:default}` utuh → default, `${user.dir}/x` → `./x`; placeholder lain, `:` (URL/JDBC), `.db` → bukan folder. Nilai harus lolos aturan folder tersimpan (§5). Diterima bila nama memuat STORAGE/UPLOAD(S)/FILE(S)/MEDIA/ATTACHMENT(S)/IMAGE(S)/DATA/BASEDIR/MULTIPART, atau (untuk nilai berisi `/`) salah satu segmen path adalah storage/upload(s)/files/media/attachment(s)/images/data. Kata tunggal tanpa `/` hanya di bawah nama folder dan bukan nilai seperti `local`/`s3`/`true`. Urutan sumber: `.env.example`, lalu file properti; path ganda atau bertumpuk dibuang; nama folder workspace bentrok diberi akhiran `-2`.
- **Port internal (port aplikasi di dalam container):** `server.port` di JAR; untuk binary dari `.env.example` (`PORT`); bawaan `8080`. Bila dikosongkan saat deploy, nilai terdeteksi/bawaan itu yang disimpan di aplikasi, dan setiap deploy ulang memakai nilai tersimpan. Runova selalu mengisi env `PORT` (JAR juga `SERVER_PORT`) dengan port internal. Port ini berbeda dari port akses IP (20000–20999, listener Caddy) dan port host internal `127.0.0.1:<30000–39999>` (tujuan Caddy, acak per deployment).

## 5. Menjalankan aplikasi

- Satu container per aplikasi, image dasar:
  - JAR: `eclipse-temurin:<versi>-jre`
  - statis: `gcr.io/distroless/static-debian12`
  - glibc: `ubuntu:26.04`
- Artefak di-mount read-only; folder kerja aplikasi di-mount ke `/data` (rw) dan menjadi `WorkingDir`; env `RUNOVA_DATA_DIR=/data`. Path relatif (`./storage`) jatuh ke folder kerja.
- **Folder tersimpan** (per aplikasi, kolom `apps.folders`): daftar `{path, dir}`. `path` relatif (atau di bawah `/data`) = sudah di folder kerja, `dir` = path itu. `path` absolut = bind mount tambahan `mounts/<user>/<app>/<dir>` → `path` dan → `/data/<dir>` (supaya tampil di folder kerja dan di tab File). Ditolak: `..`, karakter selain `[A-Za-z0-9._/-]`, `/`, `/var`, `/opt`, `/srv`, `/home`, `/mnt`, `/media`, `/app`, `/data`, `/tmp` dan isinya, `/proc`, `/sys`, `/dev`, `/etc`, `/usr`, `/bin`, `/sbin`, `/lib*`, `/boot`, `/run`, `/root`, `/var/run`, `/opt/java` beserta isinya, path artefak (`/app/<file>`) atau induknya, path/folder yang saling bertumpuk, maksimal 20. Saat deploy Runova membuat foldernya milik UID pengguna lewat `os.Root` tanpa mengikuti link; komponen yang berupa link/bukan folder menggagalkan deploy di langkah persiapan. Mengubah folder tersimpan = deploy ulang (`redeploy_required`). Rootfs tetap read-only.
- **Tab File** (pemilik/admin), pengelola file folder kerja. Path relatif terhadap folder kerja, dibuka lewat `os.Root` (`..`, path absolut, `\`, NUL, dan link keluar ditolak; link sendiri tidak pernah diikuti). Aplikasi orang lain → 404.
  - `GET /api/apps/{id}/files?path=` daftar isi (`items` = jumlah isi tiap folder).
  - `GET …/files/download?path=` selalu `attachment`, `application/octet-stream`, `nosniff`, CSP sandbox.
  - `GET …/files/info?path=` ukuran, waktu, tipe MIME hasil sniff (bukan dari nama; SVG dikenali), `preview` = `image`/`text`/kosong.
  - `GET …/files/preview?path=` hanya gambar `image/png|jpeg|gif|webp` hasil sniff, atau teks ≤ 1 MB sebagai `text/plain`; `inline`, `nosniff`, CSP `default-src 'none'; sandbox`. SVG, HTML, PDF, dan lainnya 400 (unduh saja).
  - `GET …/files/zip?path=a&path=b` ZIP streaming dari satu folder (atau `path=` kosong untuk seluruh folder kerja); link dilewati; total > 4 × batas unggahan (maks 2 GB) ditolak 400 sebelum dikirim. `&check=1` hanya memeriksa dan mengembalikan `{name, size}`.
  - `POST …/files?path=` multipart, satu atau banyak field `file` → `{saved}`; batas = batas unggahan server untuk seluruh permintaan, tidak boleh melewati sisa disk aplikasi, ditulis ke file sementara lalu di-rename, milik UID pengguna; tidak menimpa folder atau folder tersimpan.
  - `POST …/files/folder` `{path, name}` folder baru milik UID pengguna; nama yang sudah ada ditolak.
  - `POST …/files/move` `{from, to}` ganti nama/pindah di dalam folder kerja (juga antara folder data dan folder ter-mount) dengan `renameat2(RENAME_NOREPLACE)`: tujuan yang sudah ada ditolak, tidak pernah menimpa.
  - `DELETE …/files` `{paths: [...]}` hapus banyak; semua path diperiksa dulu, satu yang ditolak membatalkan semuanya.
  - Folder kerja, folder tersimpan, dan folder yang berisi folder tersimpan tidak bisa dihapus, diganti namanya, atau dipindah. Kuota disk diukur ulang setelah unggah, hapus, dan pindah. Aktivitas: `file.upload`, `file.delete`, `file.rename` (`a → b`), `file.mkdir`.
- Container berjalan sebagai UID pengguna (`100000 + user.id`), `--read-only` rootfs dengan `/tmp` tmpfs, `--cap-drop ALL`, `--security-opt no-new-privileges`, batas `--memory` dan `--cpus` dari alokasi aplikasi, `--restart on-failure`.
- **Akses IP:** port tetap dari rentang 20000–20999, dialokasikan saat aplikasi dibuat dan tidak berubah. Nyala: Caddy mendengarkan `:<port>` (HTTP, tanpa HTTPS otomatis) dan meneruskan ke aplikasi; mati: listener itu dihapus.
- Container mendapat `host.docker.internal` → gateway host (untuk PostgreSQL), env `HOME=/tmp`.
- **Deploy:** mode *tanpa jeda* (bawaan: container baru jalan dan sehat dulu, lalu Caddy dialihkan, lalu container lama dihentikan) atau *stop-start* per aplikasi. Sehat = port internal menerima koneksi TCP (dan tidak langsung ditutup) dalam batas waktu: binary 60 detik; JAR 90 detik ÷ core CPU (minimal 3 menit, maksimal 15 menit; 0,5 core = 3 menit, 0,25 core = 6 menit). JAR yang log-nya masih bertambah dan belum menulis `Started …` ditunggu terus sampai dua kali batas itu.
- **Restart/Jalankan:** status "Memulai…" (`starting`) sampai port internal sehat dengan aturan yang sama, lalu "Berjalan"; bila tidak pernah sehat → "Gagal" dengan diagnosis di deployment aktif. Container tidak dihapus.
- **Versi dan rollback:** 10 versi terakhir disimpan; kembalikan = deploy ulang versi lama dengan env saat itu.
- **Crash:** Docker menjalankan ulang. Lebih dari 5 restart dalam 10 menit → status "Gagal", container dihentikan, notifikasi di panel (ikon lonceng). Email/Telegram tidak termasuk versi pertama.
- **Log:** dari Docker (`json-file`, maks 3×10 MB per container), ditampilkan live di panel.
- **Terminal web:** `docker exec` `/bin/sh` sebagai UID pengguna (bukan root) ke container milik pengguna sendiri lewat WebSocket. Hanya untuk JAR dan binary glibc; image distroless (binary statis) tidak punya shell, tombolnya nonaktif dengan alasan itu. Setiap sesi tercatat di Aktivitas.

## 6. Domain dan HTTPS

- Caddy dikelola lewat admin API (`127.0.0.1:2019`). Server `runova-https` (`:80`, `:443`) berisi satu route per domain → `127.0.0.1:<port acak aplikasi>`; server `runova-ip-<port>` per aplikasi yang akses IP-nya nyala.
- Domain: nama host valid (huruf kecil, label 1–63, total ≤253, minimal dua label), unik di seluruh server, maksimal 5 per aplikasi. Status sertifikat diambil dari Caddy (`/pki` tidak cukup; Runova mencoba koneksi TLS ke domain di 127.0.0.1:443 dengan SNI).
- Panel sendiri: saat instalasi pilih domain (HTTPS otomatis) atau `IP:port`. Bisa diubah di Pengaturan; flag installer `--domain` / `--port`, disimpan lewat `runova panel set`.
- Panduan domain tiga cara (DNS langsung, Proxy Cloudflare, Cloudflare Tunnel) sesuai `docs/domains.id.md`. Hanya admin yang boleh menambah domain sendiri (bebas); pengguna biasa memakai domain hosting di bawah.
- **Domain hosting bersama.** Admin mendaftarkan domain wildcard (mis. `porto.devkriyanesia.my.id`, record `A` `*.porto` ke IP server, DNS only); setiap pengguna memberi aplikasinya `<nama>.<domain terdaftar>` sebanyak kuota aplikasinya (`Quota.Apps`), tanpa mengurus DNS. Satu sertifikat Let's Encrypt per subdomain (HTTP/TLS-ALPN, tanpa sertifikat wildcard); halaman Server memperingatkan mendekati batas 50 sertifikat per minggu. Tabel `hosting_domains` (migrasi `0002`), kolom `domains.hosting_domain_id` dan `domains.label`. Desain lengkap: [`docs/superpowers/specs/2026-11-03-hosting-domains-design.md`](superpowers/specs/2026-11-03-hosting-domains-design.md).

## 7. Database PostgreSQL

- PostgreSQL 18 dari apt Ubuntu, di host. `listen_addresses = '*'`. Gerbangnya `pg_hba.conf`: baris `include_dir pg_hba.d` mengarah ke `/etc/postgresql/18/main/pg_hba.d/runova.conf` yang ditulis Runova (`127.0.0.1/32` untuk koneksi panel sendiri, subnet `172.30.0.0/16` + jaringan yang diizinkan admin, semuanya `scram-sha-256`), lalu `SELECT pg_reload_conf()` (EXECUTE di-grant ke `runova`). Jaringan lain ditolak sebelum autentikasi.
- Runova terhubung lewat socket Unix dengan peer auth sebagai role `runova` (`LOGIN CREATEDB CREATEROLE`, bukan superuser).
- Satu role per pengguna Runova (nama = username), dibuat saat pengguna dibuat: `LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT CONNECTION LIMIT <kuota>`, lalu `GRANT <role> TO runova WITH INHERIT TRUE, SET TRUE` agar Runova bisa membuat, mem-backup, dan menghapus database milik role itu. Password acak 32 karakter, disimpan terenkripsi.
- Pengguna membuat database di panel: nama `<username>_<nama>`, `<nama>` = `^[a-z][a-z0-9_]{0,30}$`. Runova menjalankan `CREATE DATABASE … OWNER <role>` lalu `REVOKE CONNECT, TEMPORARY ON DATABASE … FROM PUBLIC`. Jumlah dibatasi kuota.
- Saat deploy pengguna memilih satu database miliknya (atau tanpa). `PGHOST=host.docker.internal` (server ini dari dalam container; `localhost` di container adalah container itu sendiri), `PGPORT=5432`. Env yang selalu diisi: `DATABASE_URL`, `SPRING_DATASOURCE_URL`, `SPRING_DATASOURCE_USERNAME`, `SPRING_DATASOURCE_PASSWORD`, `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE`. Ditambah setiap nama dari laporan artefak atau env aplikasi yang dikenali `internal/dbenv` (`KindOf`, tabel uji di `dbenv_test.go`): nama berawalan/berkata `DB`, `DATABASE`, `POSTGRES(QL)`, `PG`, `DATASOURCE`, `JDBC`, `R2DBC`, `TYPEORM`, `SQL` (boleh didahului `SPRING`/`QUARKUS`/`APP`, kata lain = ambigu, tidak diisi) dan diakhiri `URL|URI|DSN|CONNECTION_STRING` → URL (`jdbc:postgresql://…` bila nama memuat `JDBC|DATASOURCE|SPRING|QUARKUS` atau artefak JAR, `r2dbc:postgresql://…` bila `R2DBC`, selain itu `postgres://user:pass@host:port/db`), `HOST|HOSTNAME|SERVER` → host, `PORT` → port, `USER|USERNAME|LOGIN` → user, `PASS|PASSWORD|PWD|PASSWD` → password, `NAME|DATABASE|DBNAME|DB` → nama database. `SCHEMA`, `MYSQL_*`, `REDIS_*`, `MONGO_*` dsb. tidak disentuh. Nilai yang diisi pengguna (tidak kosong) menang; nilai kosong diserahkan ke database. API (`db_env` di unggahan dan detail aplikasi) hanya mengirim nama, jenis, dan apakah diisi sendiri, tanpa nilai.
- Hapus database hanya oleh pemilik dengan konfirmasi nama. Hapus pengguna → semua aplikasi, data, database, dan role-nya ikut dihapus.
- **Backup:** `pg_dump -Fc` tiap malam pukul 02:00 waktu server per database, disimpan 7 hari. Pemilik bisa mengunduh, membuat backup manual, dan me-restore (`pg_restore --clean --if-exists --no-owner --role=<role>`) dari halaman Database. Restore (dari backup maupun dari file) dan export berjalan **sebagai role pengguna** (`PGUSER=<role>`, password tersimpan, TCP `127.0.0.1`), jadi isi dump tidak pernah dijalankan dengan hak `runova`; backup terjadwal dan manual tetap lewat `runova`.
- **Restore dari file:** `POST /api/databases/{id}/restore-upload` (multipart, field `confirm` lalu `file`), batas = batas unggah admin. File disimpan dengan nama acak di folder backup, harus diawali `PGDMP` (format custom), lalu `pg_restore` seperti di atas dan dihapus. **Export:** `GET /api/databases/{id}/export` men-stream `pg_dump -Fc --no-owner` langsung ke browser (`<db>-<YYYYMMDD-HHMMSS>.dump`), tanpa file di disk.
- **Tabel dan Query di panel.** Setiap aksi membuka koneksi `pgx` baru ke `127.0.0.1` dengan role dan password pengguna (bukan `SET ROLE` di pool `runova`: `session_user` tetap role pengguna, jadi `RESET ROLE`/`set_config('role',…)` tidak bisa kembali ke `runova`), lalu ditutup. `GET …/tables` (tabel skema `public`, kolom, baris tepat bila heap ≤ 16 MB selain itu `reltuples`, ukuran), `GET …/tables/{t}/data?offset&limit≤100` (urut primary key, atau `ctid`), `POST …/tables/{t}/truncate` dan `DELETE …/tables/{t}` (konfirmasi nama; nama tabel harus ada di daftar dan selalu lewat `pgx.Identifier{"public", t}`). `POST …/query` `{"sql","mode":"read"|"write"}`: pagar `internal/sqlguard` (satu perintah, kata pertama SELECT/WITH/EXPLAIN/SHOW; mode tulis + INSERT/UPDATE/DELETE/TRUNCATE dan CREATE/ALTER/DROP TABLE|INDEX), lalu satu `Parse` protokol extended (PostgreSQL menolak banyak perintah) di dalam `BEGIN READ ONLY`+`ROLLBACK` (baca; `default_transaction_read_only=on`) atau `BEGIN READ WRITE`+`COMMIT`; `statement_timeout` 10 s / 30 s; `Execute` maksimal 1001 baris (lebih → `truncated`), JSON hasil ≤ 5 MB. Aktivitas `db.query` (mode, SQL 200 karakter, durasi, jumlah baris atau galat), `db.table_truncate`, `db.table_drop`, `db.restore_upload`, `db.export`. Batas sebenarnya hak role; test integrasi `internal/pg/userdb_test.go` membuktikan yang wajib gagal.
- Detail koneksi untuk alat luar (DBeaver): host = IP publik server (atau domain panel), port 5432; hanya dari jaringan yang diizinkan admin. Alternatif tanpa membuka port: SSH tunnel `ssh -N -L 5433:127.0.0.1:5432 <user>@<server>` lalu `localhost:5433` (pg_hba bawaan Ubuntu mengizinkan `127.0.0.1` dengan `scram-sha-256`).

## 8. Rahasia

- Env bertanda rahasia dan password PostgreSQL dienkripsi AES-256-GCM dengan kunci `/etc/runova/secret.key` (dibuat installer, 0600). Nonce acak per nilai.
- API tidak pernah mengirim nilai rahasia kecuali pemiliknya meminta "lihat" secara eksplisit.

## 9. Aktivitas

Dicatat dengan pelaku, target, dan waktu: login berhasil/gagal, pengguna dibuat/diubah/dihapus, kuota diubah, deploy, rollback, restart/stop, domain diubah, database dibuat/dihapus/di-restore, sesi terminal dibuka. Pengguna melihat aktivitasnya sendiri; admin melihat semua. Halaman Aktivitas mencari di server (kata, frasa, pengecualian, field `aksi:` `pelaku:` `target:` `ip:` `hasil:`), dengan rentang waktu, histogram, facet, dan paging; batas pengguna biasa berlaku untuk semua bagian itu (`GET /api/activity/search`, lihat `docs/superpowers/notes/list-pages.md`).

## 10. Installer

Rilis = `runova-<versi>-linux-amd64.tar.gz` berisi `runova` dan `install.sh`. Jalankan `sudo ./install.sh [--domain panel.contoh.id | --port 8443]`:

1. Cek Ubuntu 26.04 amd64 dan root.
2. `apt install docker.io caddy postgresql-18 ufw`.
3. Buat user `runova` (grup `docker`), direktori, `secret.key` (`runova keygen`), `runova.env`, unit systemd.
4. Caddy: Caddyfile minimal `{ admin localhost:2019 }`; Runova mengirim konfigurasi lengkap.
5. PostgreSQL: role `runova`, `include_dir pg_hba.d`, `listen_addresses='*'`, grant `pg_reload_conf`.
6. Firewall (`ufw`): izinkan 22, 80, 443, 20000–20999, 5432, dan port panel bila mode IP.
7. `runova panel set …`, `systemctl enable --now runova`, `runova admin create admin`; tampilkan alamat panel dan password sekali.

Menjalankan ulang installer aman (idempoten): langkah yang sudah beres dilewati, binary diperbarui, data tidak disentuh.

## 11. Urutan pembangunan

Setiap bagian punya rencana sendiri di `docs/superpowers/plans/` dan bisa diuji sendiri. Skema SQLite berversi lewat migrasi (`internal/store/migrations/NNNN_nama.sql`, versi di `PRAGMA user_version`): setiap perubahan skema = file migrasi baru, dan migrasi yang sudah dirilis tidak pernah diubah. Cara menulisnya ada di `docs/superpowers/notes/db-migrations.md`.

| # | Bagian | Rencana |
|---|---|---|
| 1 | Fondasi: binary, konfigurasi, SQLite, rahasia, login, pengguna dan kuota, aktivitas, API JSON | `2026-10-30-fondasi.md` |
| 2 | Membaca artefak dan unggahan | `2026-10-30-artefak.md` |
| 3 | Menjalankan aplikasi di Docker: deploy, versi, rollback, log, crash, terminal | `2026-10-30-docker.md` |
| 4 | Caddy, domain, akses IP, deploy tanpa jeda | `2026-10-30-caddy.md` |
| 5 | PostgreSQL per pengguna dan backup | `2026-10-30-postgres.md` |
| 6 | Panel web | `2026-10-30-panel.md` |
| 7 | Installer, rilis, uji end-to-end lokal (tanpa VM) | `2026-10-30-installer.md` |

Bagian 2 dan Bagian 5 Task 1 (admin PostgreSQL) hanya bergantung pada Bagian 1, jadi bisa dikerjakan paralel dengan Bagian 3. Bagian 4 butuh 3; Bagian 6 butuh 1–5; Bagian 7 butuh semuanya.
