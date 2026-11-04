<p align="center">
  <img src="docs/brand/runova-app-icon.svg" width="128" height="128" alt="Runova">
</p>

<h1 align="center">Runova</h1>

<p align="center"><a href="README.md">English</a> · <b>Bahasa Indonesia</b></p>

<p align="center">
  Panel self-hosted untuk menjalankan hasil build, bukan source code.<br>
  Unggah <code>.jar</code> atau binary Linux, isi environment, dan aplikasinya jalan di server sendiri dengan domain dan HTTPS otomatis.
</p>

<p align="center">
  <a href="https://github.com/aribrilliantsyah/runova/releases/latest"><img src="https://img.shields.io/github/v/release/aribrilliantsyah/runova?label=rilis" alt="Rilis terbaru"></a>
  <a href="https://github.com/aribrilliantsyah/runova/actions/workflows/ci.yml"><img src="https://github.com/aribrilliantsyah/runova/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/lisensi-Apache--2.0-blue" alt="Lisensi Apache-2.0"></a>
</p>

<p align="center">
  <b>Status: lengkap secara fungsi; installer terbukti di Ubuntu 26.04 dengan systemd di container, belum di server atau VM sungguhan.</b> Binary <code>runova</code> membawa API JSON dan panel web tertanam; installer, pembaruan (<code>runova update</code>/<code>rollback</code>), skrip rilis, dan uji sistem sudah ada (lihat <a href="#status-pengujian">Status pengujian</a> untuk yang terbukti dan yang belum).
  <a href="preview-dashboard.html">Preview tampilan</a> (buka file itu di browser) adalah acuan desainnya.
</p>

<p align="center">
  <img src="docs/screenshots/desktop-apps.png" alt="Daftar aplikasi di Runova: kuota akun, kartu aplikasi dengan logo bahasa, status, domain, dan akses IP" width="860">
</p>

## Kenapa

Platform seperti Vercel atau Railway membangun aplikasi dari source code dan berjalan di cloud mereka. Banyak tim internal justru sudah punya artefak jadi dari pipeline sendiri (file `.jar` dari Maven, binary Go atau Rust dari CI) dan cukup butuh satu tempat di server kantor untuk menjalankannya dengan rapi:

- **Unggah, bukan build.** Runova tidak menyentuh source code. Ia membaca file yang diunggah: jenisnya, versi Java minimal, port, dan environment yang dirujuk.
- **Terpisah per pengguna.** Setiap aplikasi berjalan di container Docker sendiri dengan batas memori dan CPU. Pengguna tidak bisa melihat atau menyentuh aplikasi milik pengguna lain.
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

## Alur pengguna

<table>
  <tr>
    <td width="66%"><img src="docs/screenshots/desktop-deploy.png" alt="Wizard deploy di desktop: langkah di kiri, pilihan versi Java di tengah, ringkasan dan checklist di kanan"></td>
    <td><img src="docs/screenshots/mobile-deploy.png" alt="Wizard deploy di HP: progres langkah di atas, tombol Batal dan Lanjut di bawah"></td>
  </tr>
</table>

Deploy dibagi empat langkah:

1. **Unggah.** Tarik file ke halaman. Hasil pembacaan tampil sebagai checklist.
2. **Runtime.** Pilih versi Java (untuk `.jar`), opsi JVM, dan port yang didengarkan aplikasi.
3. **Environment.** Variabel yang dirujuk aplikasi muncul sebagai form. Nilai rahasia disimpan terenkripsi.
4. **Resource & akses.** Bagi memori dan CPU dari sisa kuota akun. Nyalakan akses IP untuk pengujian, dan isi domain kalau sudah siap.

Tombol **Deploy** baru aktif setelah semua yang wajib terpenuhi. Panel ringkasan menunjukkan apa yang masih kurang.

Setelah jalan, halaman aplikasi berisi kontrol (unggah versi baru, restart, stop, terminal), environment, domain, riwayat deployment dengan tombol kembalikan, log, monitoring, dan pengaturan lanjutan.

## Pengguna dan kuota

- **Admin** mendaftarkan pengguna. Tidak ada pendaftaran mandiri.
- Setiap pengguna hanya melihat dan mengelola aplikasinya sendiri.
- Admin menentukan kuota per pengguna: **jumlah aplikasi, memori, CPU, dan jumlah database** (bawaan 3). Pengguna membagi kuota itu ke aplikasinya.
- Storage tidak dikuota, supaya Runova jalan di Ubuntu Server apa adanya tanpa menyiapkan disk khusus. Admin tetap bisa memantau pemakaian disk server di halaman Server.
- Aplikasi yang memakai memori melebihi batasnya dihentikan dan dijalankan ulang oleh Docker tanpa mengganggu aplikasi lain.

## Database

Runova memasang **PostgreSQL 18** di server yang sama (paket resmi Ubuntu 26.04).

- Setiap pengguna mendapat **satu akun PostgreSQL** saat didaftarkan admin, misalnya `budi`.
- Pengguna membuat database sendiri di panel, dengan nama berawalan akunnya (`budi_billing`, `budi_auth`). Jumlahnya dibatasi admin, bawaannya **3 per pengguna**.
- Hanya pemilik yang bisa masuk ke database-nya. Akun pengguna tidak punya hak superuser, tidak bisa membuat role, dan dibatasi jumlah koneksinya.
- Saat deploy, pengguna memilih database untuk aplikasinya (atau tanpa database). Runova mengisi `DATABASE_URL`, `SPRING_DATASOURCE_URL`, dan `PGHOST`/`PGPORT`/`PGUSER`/`PGPASSWORD`/`PGDATABASE` otomatis. Beberapa aplikasi milik pengguna yang sama boleh memakai database yang sama.
- Host, port, nama database, dan password bisa dilihat dan disalin di halaman **Database**, misalnya untuk dibuka dari DBeaver dari jaringan yang diizinkan admin.

## Instalasi

Syarat: Ubuntu Server 26.04 LTS (amd64) polos, user dengan `sudo`, dan koneksi internet ke repositori apt Ubuntu. Installer memasang `docker.io`, `caddy`, `postgresql-18`, `ufw`, `jq`, dan `curl` dari apt resmi Ubuntu; tidak ada repositori pihak ketiga.

Panduan lengkap untuk admin server (spesifikasi, firewall cloud, DNS, cadangan, pemecahan masalah) ada di **[docs/install-ubuntu.id.md](docs/install-ubuntu.id.md)**. Ringkasnya, di server:

```bash
# panel di https://panel.contoh.id (arahkan DNS ke server dulu)
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --domain panel.contoh.id

# atau panel di https://<IP server>:8443
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --port 8443
```

`get.sh` mengunduh rilis terbaru dari [GitHub Releases](https://github.com/aribrilliantsyah/runova/releases), mencocokkan `SHA256SUMS` (arsip dan isinya), lalu menjalankan `install.sh` dengan opsi Anda. `--version 0.3.1` memasang versi tertentu. Setiap rilis juga punya [attestation](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) asal-usul build: `gh attestation verify runova-<versi>-linux-amd64.tar.gz -R aribrilliantsyah/runova`.

Dari checkout source (misalnya untuk mencoba perubahan yang belum dirilis), `scripts/deploy.sh admin@IP-SERVER [opsi]` membangun rilis lokal, menyalinnya lewat SSH, lalu menjalankan installer yang sama.

Opsi `install.sh`: `--domain <host>` atau `--port <n>` (tidak bisa bersamaan; tanpa keduanya bawaannya `--port 8443`), `--public-ip <ip>` (disimpan ke setelan; untuk server di belakang NAT), `--skip-firewall` (jangan menyentuh `ufw`), `--yes` (tanpa tanya, untuk instalasi tanpa terminal). Untuk `--domain`, arahkan DNS domain itu ke server dulu.

Di akhir, installer mencetak alamat panel, username `admin`, dan password acak **sekali saja**. Bila hilang: `sudo -u runova env RUNOVA_DATA=/var/lib/runova runova admin reset-password admin`.

Yang dilakukan installer: membuat user `runova` (grup `docker`), kunci enkripsi, `/etc/runova/runova.env`, dan unit systemd `runova.service`; menulis `/etc/caddy/Caddyfile` berisi `{ admin localhost:2019 }` dan menjalankan Caddy dengan `--resume`; membuat role PostgreSQL `runova` dan mengaktifkan `listen_addresses = '*'` serta `include_dir pg_hba.d`; menggabungkan `default-address-pools` (`172.31.0.0/16`) ke `/etc/docker/daemon.json`; membuka port 22 (atau port `sshd` sebenarnya), 80, 443, 20000-20999, 5432, dan port panel di `ufw`, lalu mengaktifkannya bila belum aktif.

Bila `/etc/caddy/Caddyfile` sudah Anda ubah (bukan bawaan paket), installer berhenti sebelum mengubah apa pun. Pindahkan situs Anda dulu, atau jalankan ulang dengan `--yes` untuk menyalin cadangan ke `Caddyfile.runova-backup` lalu menggantinya.

`runova check` memeriksa Docker, `docker-init`, Caddy, PostgreSQL, kunci, dan izin folder (keluar 1 bila ada yang gagal); installer menjalankannya di akhir.

## Pembaruan

Admin diberi tahu di panel: bila ada rilis yang lebih baru, banner di bawah topbar semua halaman (hanya untuk admin, tidak bisa ditutup) berisi versi baru, perintah `sudo runova update` dengan tombol salin, dan tautan ke catatan rilis. Banner hilang sendiri setelah server diperbarui.

Pengecekan dilakukan `runova serve` (bukan peramban), pertama 1 menit setelah start lalu tiap 6 jam, dengan mengikuti redirect `github.com/<repo>/releases/latest` seperti `get.sh` (tanpa `api.github.com`). Server tanpa internet tidak menampilkan apa-apa. Matikan di halaman **Server** (sakelar "Periksa pembaruan", setelan `update_check`). Build `dev` tidak pernah mengecek. `RUNOVA_REPO` mengganti repositori (bawaan `aribrilliantsyah/runova`) dan `RUNOVA_GITHUB` mengganti `https://github.com` (dipakai uji).

Perbarui di server:

```bash
sudo runova update                 # ke rilis terbaru, bertanya dulu
sudo runova update --yes           # tanpa tanya (wajib bila tanpa terminal)
sudo runova update --version 0.3.1 # ke versi tertentu (tidak boleh lebih lama dari yang terpasang)
runova update --check              # hanya memeriksa; keluar 0 = terbaru, 10 = ada pembaruan, 1 = gagal
```

`runova update` mengunduh `get.sh` dari rilis target beserta `SHA256SUMS`, mencocokkan checksum `get.sh`, lalu menjalankan `bash get.sh --version X --yes`. Semua logika pemasangan tetap satu tempat (`get.sh` → `install.sh`), jadi hasilnya sama dengan perintah `curl` di bawah. Alamat panel tidak berubah. Perintah `curl` tetap dipakai untuk instalasi pertama dan untuk server Runova 0.2.0 ke bawah yang belum punya `runova update`:

```bash
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s --
```

Installer idempoten: langkah yang sudah beres dilewati, binary dan unit diganti, layanan di-restart. Data, kunci, admin, aplikasi, dan alamat panel tidak disentuh, dan tidak ada password baru dicetak. Beri `--domain`/`--port` hanya bila ingin mengganti alamat panel.

### Kembali ke versi sebelumnya

Saat memasang versi lain, `install.sh` menyimpan binary yang sedang terpasang ke `/var/lib/runova/rollback/runova` (versinya di `rollback/VERSION`). Hanya satu versi sebelumnya yang disimpan.

```bash
sudo runova rollback          # bertanya dulu
sudo runova rollback --yes
```

`rollback` menghentikan layanan, mengembalikan binary sebelumnya, lalu menjalankan `runova check`. Bila database sudah dimigrasi melebihi skema binary lama, ia memulihkan cadangan terbaru `runova.db.bak-v<skema binary lama>-*` (dibuat otomatis sebelum migrasi); tanpa cadangan yang cocok `rollback` berhenti tanpa mengubah apa pun. **Perubahan data panel sejak pembaruan (pengguna, aplikasi, domain, setelan) hilang** pada kasus itu; database sebelum rollback disimpan di `runova.db.before-rollback-<waktu unix>`. Container aplikasi dan database PostgreSQL pengguna tidak disentuh. Binary yang diganti disimpan di `rollback/`, jadi menjalankan `rollback` lagi (atau `runova update`) maju kembali.

Skema database ikut diperbarui otomatis saat Runova dibuka. Sebelum migrasi pertama yang tertunda, Runova menyalin database ke `/var/lib/runova/runova.db.bak-v<versi lama>-<waktu unix>` (mode 0600; 3 cadangan terbaru disimpan) dan mencatat path-nya di log. Bila migrasi gagal, perubahan dibatalkan dan Runova tidak start. Database dari versi Runova yang lebih baru ditolak tanpa disentuh, jadi mundur ke binary lama harus disertai pemulihan dari cadangan. `runova db version` mencetak versi skema database dan versi tertinggi yang dikenal binary:

```bash
sudo -u runova env RUNOVA_DATA=/var/lib/runova runova db version
```

## Merilis versi baru

Rilis dibangun GitHub Actions (`.github/workflows/release.yml`) setiap ada tag `v*.*.*`: test, `scripts/release.sh`, attestation, lalu GitHub Release berisi arsip, `SHA256SUMS`, dan `get.sh`.

```bash
git tag v0.4.0
git push origin v0.4.0
```

Setiap push ke `main` juga menjalankan CI (`.github/workflows/ci.yml`): gofmt, vet, test unit, test integrasi dengan Docker, PostgreSQL 18, dan Caddy sungguhan, serta bats dan shellcheck.

## Uninstall manual

Belum ada perintah uninstall. Langkah manual (menghapus **semua** data Runova, termasuk aplikasi dan database pengguna):

```bash
sudo systemctl disable --now runova
# hapus container aplikasi dan jaringan penggunanya
sudo docker ps -aq --filter label=runova.app | xargs -r sudo docker rm -f
sudo docker network ls --format '{{.Name}}' | grep -E '^runova-u[0-9]+$' | xargs -r sudo docker network rm
# hapus database dan role PostgreSQL (satu role per pengguna Runova, ditambah role runova)
sudo -u postgres psql -c '\l'    # catat database <pengguna>_<nama>, lalu DROP DATABASE satu per satu
sudo -u postgres psql -c '\du'   # DROP ROLE untuk tiap pengguna Runova, lalu DROP ROLE runova
sudo rm -f /etc/postgresql/18/main/conf.d/runova.conf
sudo rm -rf /etc/postgresql/18/main/pg_hba.d      # dan hapus baris "include_dir pg_hba.d" dari pg_hba.conf
sudo systemctl reload postgresql
sudo rm -f /etc/systemd/system/runova.service /etc/systemd/system/caddy.service.d/runova.conf /usr/local/bin/runova
sudo rm -rf /etc/runova /var/lib/runova
sudo systemctl daemon-reload
sudo userdel runova
```

Caddyfile (`{ admin localhost:2019 }`), `daemon.json`, aturan `ufw`, dan paket apt tidak dikembalikan; sesuaikan sendiri bila perlu.

## Status pengujian

Jujur soal apa yang terbukti. **Terbukti** (`test/system/run.sh`, diulang dengan satu perintah): `scripts/deploy.sh` memasang Runova lewat SSH ke Ubuntu 26.04 sungguhan berjalan dengan systemd sebagai PID 1 (container privileged, tanpa VM) dengan `apt-get install` nyata dari repositori Ubuntu (`docker.io` 29.1.3, `caddy` 2.6.2, `postgresql-18` 18.6, `ufw`), Docker-in-Docker, dan `ufw` aktif dengan aturannya. Di situ `runova check` ok semua, login API lewat Caddy berhasil, pengguna dan database dibuat di PostgreSQL apt, binary contoh berjalan di container dan terjangkau lewat port akses IP dari luar server, pemasangan kedua tidak mencetak password dan tidak mengubah kunci, data, atau aplikasi, `systemctl restart runova caddy postgresql` memulihkan panel dan aplikasi, dan `runova update` lalu `runova rollback` berpindah versi dengan login, pengguna, database, dan aplikasi utuh. Dengan rilis sungguhan dari GitHub: pemasangan lewat `curl`, pembaruan 0.1.0 → 0.2.0 → 0.3.0 (migrasi skema dan cadangan otomatis), `runova update --check`, dan `rollback` ke 0.2.0. Selain itu ada uji `bats` dengan stub dan `shellcheck` untuk semua skrip, serta alur produk end-to-end lengkap (`test/e2e`: deploy JAR dan binary, deploy tanpa jeda, isolasi antar-pengguna, backup dan restore, subdomain dari domain hosting lewat HTTPS) dengan Docker nyata, `caddy:2.6.2`, dan `postgres:18`. **Belum terbukti:** kernel dan boot server sungguhan (container memakai kernel mesin pengembang; `systemd-modules-load` gagal di container sehingga systemd melapor `degraded`), reboot server (unit memakai `Wants`/`After`, `Restart=on-failure`, dan Caddy `--resume`), pengaruh `ufw` terhadap aturan iptables Docker di kernel server, `ufw` di luar container, dan sertifikat Let's Encrypt untuk domain sungguhan (di container Caddy hanya menerbitkan sertifikat internal). Coba di server percobaan dulu.

## Domain hosting bersama

Admin membuka halaman **Server → Domain hosting** dan mendaftarkan domain wildcard, misalnya `porto.devkriyanesia.my.id`, lalu membuat satu record `A` `*.porto` ke IP server (proxy **DNS only**). Runova mengecek DNS-nya dan menampilkan hasilnya. Setelah itu setiap pengguna, admin maupun biasa, memilih domain itu di langkah 4 wizard atau tab Domain, mengetik nama (`cv-budi`), dan aplikasinya langsung beralamat `cv-budi.porto.devkriyanesia.my.id`.

- Jumlah subdomain pengguna dibatasi kuota aplikasinya; batas per aplikasi tetap 5 alamat.
- Nama 3–40 karakter (huruf kecil, angka, tanda hubung); `www`, `api`, `mail`, `admin`, `runova`, label panel, dan nama cadangan tambahan dari admin tidak bisa dipakai. Siapa cepat dia dapat.
- Mematikan domain hosting mencabut semua subdomainnya dari Caddy dan memberi tahu pemiliknya; domain hanya bisa dihapus bila tidak ada subdomain yang memakainya.
- Satu sertifikat Let's Encrypt diterbitkan per subdomain. Let's Encrypt membatasi 50 sertifikat baru per minggu per domain utama; kartu Domain hosting menampilkan perkiraan dan memperingatkan mendekati batas.
- Domain milik sendiri (mis. `budi.com`) hanya bisa ditambahkan admin; domain seperti itu yang sudah dimiliki pengguna biasa tetap berjalan.
- Proxy Cloudflare (awan oranye) dan Tunnel tidak didukung untuk subdomain tingkat dua.

## Menghubungkan domain sendiri (admin)

Saat domain diisi, Runova menampilkan panduan yang sudah terisi subdomain, domain, dan alamat server. Ada tiga cara, sesuai kondisi jaringan:

| Cara | Kapan dipakai | Yang dibuat di Cloudflare |
|---|---|---|
| **DNS langsung** | Server punya IP publik, port 80 dan 443 terbuka | Record `A` ke IP publik server, proxy **DNS only** |
| **Proxy Cloudflare** | Ingin IP server tersembunyi dan dapat proteksi Cloudflare | Record `A` yang sama. Setelah HTTPS aktif di Runova, ubah ke **Proxied** dan atur SSL/TLS ke **Full (strict)** |
| **Cloudflare Tunnel** | Server tanpa IP publik atau tanpa port terbuka | Networking → Tunnels → Routes → Published application, Service URL `http://localhost:80` (Caddy di server Runova) |

Untuk DNS langsung dan proxy, sertifikat diterbitkan Caddy di server. Untuk Tunnel, HTTPS diurus Cloudflare.

## Tampilan

<table>
  <tr>
    <td width="66%"><img src="docs/screenshots/desktop-app-dark.png" alt="Detail aplikasi dalam mode gelap: kontrol, deployment aktif, akses IP dan domain, log, resource"></td>
    <td><img src="docs/screenshots/mobile-apps.png" alt="Daftar aplikasi di HP dengan navigasi bawah: Aplikasi, Database, Deploy, Pengguna, Lainnya"></td>
  </tr>
</table>

<table>
  <tr>
    <td><img src="docs/screenshots/desktop-update-banner.png" alt="Banner pembaruan di bawah topbar, desktop terang"></td>
    <td width="260"><img src="docs/screenshots/mobile-update-banner-dark.png" alt="Banner pembaruan di HP, mode gelap"></td>
  </tr>
</table>

Tangkapan layar di atas diambil dari panel sungguhan (`node web/test/screenshots.mjs`), bukan dari preview.

- Mode terang dan gelap mengikuti sistem, bisa dipaksa dari menu akun, padat seperti Dokploy dan Railway, tanpa gradien.
- Panel tidak memuat apa pun dari luar server: font, ikon, logo, dan xterm.js ada di `web/vendor/`, dan header CSP menolak sumber lain. Jadi panel jalan di server tanpa internet keluar.
- Di HP, menu pindah ke navigasi bawah. Selama deploy, navigasi bawah diganti tombol **Kembali** dan **Lanjut**.
- Ikon: [Lucide](https://lucide.dev). Logo bahasa: [Devicon](https://devicon.dev).

## Rencana teknis

| Bagian | Pilihan |
|---|---|
| Server target | Ubuntu Server 26.04 LTS, `linux/amd64` |
| Runova | Satu binary Go, frontend tertanam |
| Isolasi aplikasi | Docker, satu container per aplikasi, batas memori dan CPU |
| Domain & HTTPS | Caddy, sertifikat otomatis |
| Database | PostgreSQL 18 dari apt Ubuntu, service systemd di host. Hanya menerima koneksi dari jaringan Docker dan jaringan yang diizinkan admin |
| Akses IP | Container hanya terbuka di `127.0.0.1`. Nyala: Caddy membuka port akses IP aplikasi dan meneruskannya. Mati: port itu ditutup |
| Port aplikasi | Dialokasikan otomatis dari rentang yang diatur admin |
| IP publik server | Diisi admin sekali. Kalau kosong, panduan domain hanya menawarkan Cloudflare Tunnel |

Rancangan rinci ada di [`docs/spec.md`](docs/spec.md). Rencana implementasi per bagian ada di `docs/superpowers/plans/`.

## Coba lokal

Butuh Go 1.27. Tanpa cgo, jadi tidak perlu compiler C.

```bash
CGO_ENABLED=0 go build -o runova ./cmd/runova

export RUNOVA_DATA=$(mktemp -d)
export RUNOVA_SECRET_KEY=$RUNOVA_DATA/secret.key RUNOVA_LISTEN=127.0.0.1:7071

./runova keygen                  # membuat kunci enkripsi (0600)
./runova admin create admin      # mencetak Username dan Password acak sekali
./runova serve &                 # API di http://127.0.0.1:7071

curl -s -c jar -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<password dari langkah admin create>"}' \
  127.0.0.1:7071/api/login
curl -s -b jar 127.0.0.1:7071/api/me
curl -s -b jar 127.0.0.1:7071/api/activity
```

Subperintah lain: `runova admin reset-password <username>` membuat password acak baru. `serve` menolak start bila file kunci tidak ada, jadi jalankan `keygen` dulu. Variabel lingkungan: `RUNOVA_LISTEN` (bawaan `127.0.0.1:7070`), `RUNOVA_DATA` (`/var/lib/runova`), `RUNOVA_SECRET_KEY` (`/etc/runova/secret.key`), `RUNOVA_DOCKER_HOST` (`unix:///var/run/docker.sock`). `serve` juga menolak start bila Docker tidak bisa diakses.

Panel web dilayani dari binary yang sama: buka `http://127.0.0.1:7071/` dan masuk dengan admin tadi. Tanpa Docker, Caddy, dan PostgreSQL di mesin pengembangan, jalankan dengan engine palsu (ditolak bila `RUNOVA_DATA=/var/lib/runova`):

```bash
RUNOVA_FAKE_BACKENDS=1 ./runova serve
```

Semua mutasi API memakai `Content-Type: application/json`. Jalankan `go test ./...` untuk menguji server. Uji panel (Chromium lewat Playwright, tanpa dependensi JS di produksi):

```bash
node web/test/unit.mjs            # halaman uji web/test/*.test.html
node web/test/smoke-deploy.mjs    # login, wizard, deploy
node web/test/smoke-app.mjs       # tab detail aplikasi, log, terminal
node web/test/smoke-admin.mjs     # pengguna, database, aktivitas, server
node web/test/smoke-layout.mjs    # 5 lebar layar, terang dan gelap, aksesibilitas
node web/test/smoke-lifecycle.mjs # terminal, dialog, dan soket tidak melewati umur halaman/sesi
node web/test/smoke-hosting.mjs   # domain hosting: admin mendaftarkan, pengguna memilih alamat, kuota subdomain
node web/test/smoke-update.mjs    # banner pembaruan (admin ya, pengguna biasa tidak), sakelar, tanpa scroll horizontal
```

Ikon dibuat dari `web/icons.txt` dengan `go run ./cmd/genicons` (hasilnya di-commit).

### Uji installer dan uji end-to-end lokal

Tanpa VM. Installer diuji dengan `bats` dan `shellcheck` di Docker (stub menggantikan `apt-get`, `systemctl`, `ufw`, dan lainnya, jadi tidak menyentuh mesin ini):

```bash
test/bats.sh
```

Uji sistem memasang Runova sungguhan di Ubuntu 26.04 dengan systemd sebagai PID 1 (container privileged; `apt-get`, `systemctl`, `ufw`, dan Docker-in-Docker nyata) lewat `scripts/deploy.sh`, lalu memeriksa `runova check`, login panel, pengguna dan database, deploy binary contoh lewat port akses IP, pemasangan ulang, restart layanan, serta `runova update` dan `runova rollback` terhadap server rilis lokal. Butuh Docker dan internet; sekitar 10 menit. Container dihapus di akhir (`KEEP=1` untuk menyimpannya):

```bash
test/system/run.sh
```

Alur produk (login, pengguna, database, deploy binary dan JAR, deploy tanpa jeda, isolasi antar-pengguna, restart Runova, backup dan restore, subdomain dari domain hosting lewat HTTPS) diuji dengan Runova dari source, Docker nyata, `caddy:2.6.2` (versi paket Ubuntu 26.04; ganti lewat `CADDY_IMAGE`) dan `postgres:18` di container, tanpa root:

```bash
test/e2e/stack.sh up && test/e2e/run.sh && test/e2e/stack.sh down
```

`stack.sh up` memakai port panel 8443, Caddy admin 2019, PostgreSQL 55432, dan port akses IP 20000+ di mesin ini, jadi hentikan layanan lain yang memakainya. `RUNOVA_DEV_CHMOD=1` membuat folder data aplikasi 0777 sebagai pengganti `chown` (proses dev bukan root); ditolak untuk `RUNOVA_DATA=/var/lib/runova`. Tanpa `down`, sisa container `runova-*` dan folder `test/e2e/.cache/state` tertinggal; `run.sh` butuh stack yang baru dinaikkan. Pertama kali, `run.sh` membangun JAR Spring Boot contoh di `maven:3-eclipse-temurin-21` dan menyimpannya di `test/e2e/.cache/`.

Membuat arsip rilis: `scripts/release.sh 0.4.0` menghasilkan `dist/runova-0.4.0-linux-amd64.tar.gz` dan `dist/SHA256SUMS`.

## Kredit

- Ikon antarmuka: [Lucide](https://lucide.dev), lisensi ISC.
- Logo bahasa pemrograman: [Devicon](https://devicon.dev), lisensi MIT. Merek dagang milik pemiliknya masing-masing.
- Font: [IBM Plex Sans dan IBM Plex Mono](https://www.ibm.com/plex/), SIL Open Font License.
- Logo Runova: roket yang meluncur dengan jendela berupa slot rak server, digambar khusus untuk proyek ini. Master dan variannya di `docs/brand/`; ikon app (`runova-app-icon.svg`, PNG 1024 px) menaruhnya di tile squircle gelap gaya macOS.

## Lisensi

Runova dirilis di bawah [Apache License 2.0](LICENSE). Lihat juga [`NOTICE`](NOTICE). Aset pihak ketiga di panel memakai lisensinya masing-masing (`web/vendor/licenses/`).
