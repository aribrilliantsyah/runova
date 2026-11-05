# Pengembangan

[English](development.md) · **Bahasa Indonesia**

Untuk yang mengerjakan Runova sendiri: status, menjalankan lokal, pengujian, dan merilis.

## Status proyek

**Status: lengkap secara fungsi; installer terbukti di Ubuntu 26.04 dengan systemd di container, belum di server atau VM sungguhan.** Binary `runova` membawa API JSON dan panel web tertanam; installer, pembaruan (`runova update`/`rollback`), skrip rilis, dan uji sistem sudah ada (lihat Status pengujian untuk yang terbukti dan yang belum).
[Preview tampilan](../preview-dashboard.html) (buka file itu di browser) adalah acuan desainnya.

## Status pengujian

Jujur soal apa yang terbukti. **Terbukti** (`test/system/run.sh`, diulang dengan satu perintah): `scripts/deploy.sh` memasang Runova lewat SSH ke Ubuntu 26.04 sungguhan berjalan dengan systemd sebagai PID 1 (container privileged, tanpa VM) dengan `apt-get install` nyata dari repositori Ubuntu (`docker.io` 29.1.3, `caddy` 2.6.2, `postgresql-18` 18.6, `ufw`), Docker-in-Docker, dan `ufw` aktif dengan aturannya. Di situ `runova check` ok semua, login API lewat Caddy berhasil, pengguna dan database dibuat di PostgreSQL apt, binary contoh berjalan di container dan terjangkau lewat port akses IP dari luar server, pemasangan kedua tidak mencetak password dan tidak mengubah kunci, data, atau aplikasi, `systemctl restart runova caddy postgresql` memulihkan panel dan aplikasi, dan `runova update` lalu `runova rollback` berpindah versi dengan login, pengguna, database, dan aplikasi utuh. Dengan rilis sungguhan dari GitHub: pemasangan lewat `curl`, pembaruan 0.1.0 → 0.2.0 → 0.3.0 (migrasi skema dan cadangan otomatis), `runova update --check`, dan `rollback` ke 0.2.0. Selain itu ada uji `bats` dengan stub dan `shellcheck` untuk semua skrip, serta alur produk end-to-end lengkap (`test/e2e`: deploy JAR dan binary, deploy tanpa jeda, isolasi antar-pengguna, backup dan restore, subdomain dari domain hosting lewat HTTPS) dengan Docker nyata, `caddy:2.6.2`, dan `postgres:18`. **Belum terbukti:** kernel dan boot server sungguhan (container memakai kernel mesin pengembang; `systemd-modules-load` gagal di container sehingga systemd melapor `degraded`), reboot server (unit memakai `Wants`/`After`, `Restart=on-failure`, dan Caddy `--resume`), pengaruh `ufw` terhadap aturan iptables Docker di kernel server, `ufw` di luar container, dan sertifikat Let's Encrypt untuk domain sungguhan (di container Caddy hanya menerbitkan sertifikat internal). Coba di server percobaan dulu.

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
node web/test/smoke-dbenv.mjs     # variabel database terisi otomatis, isi sendiri, petunjuk SSH tunnel
node web/test/smoke-app.mjs       # tab detail aplikasi, log, terminal
node web/test/smoke-admin.mjs     # pengguna, database, aktivitas, server
node web/test/smoke-layout.mjs    # 5 lebar layar, terang dan gelap, aksesibilitas
node web/test/smoke-lifecycle.mjs # terminal, dialog, dan soket tidak melewati umur halaman/sesi
node web/test/smoke-hosting.mjs   # domain hosting: admin mendaftarkan, pengguna memilih alamat, kuota subdomain
node web/test/smoke-update.mjs    # banner pembaruan (admin ya, pengguna biasa tidak), sakelar, tanpa scroll horizontal
```

Ikon dibuat dari `web/icons.txt` dengan `go run ./cmd/genicons` (hasilnya di-commit).

## Uji installer dan uji end-to-end lokal

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

Dengan stack menyala, `test/e2e/nginx.sh` menaruh nginx 1.18 di depannya dengan `docs/examples/nginx-runova.conf` (berkas yang dimuat panduan) dan memeriksa akses panel tanpa loop redirect, batas login per IP klien asli (dua container klien, `X-Forwarded-For` palsu), WebSocket, subdomain hosting baru tanpa sertifikat publik apa pun (CA internal, status domain aktif), IP di log aktivitas, serta port 80 diteruskan ke Runova dengan nginx memverifikasi CA internal Caddy.

`stack.sh up` memakai port panel 8443, Caddy admin 2019, PostgreSQL 55432, dan port akses IP 20000+ di mesin ini, jadi hentikan layanan lain yang memakainya. `RUNOVA_DEV_CHMOD=1` membuat folder data aplikasi 0777 sebagai pengganti `chown` (proses dev bukan root); ditolak untuk `RUNOVA_DATA=/var/lib/runova`. Tanpa `down`, sisa container `runova-*` dan folder `test/e2e/.cache/state` tertinggal; `run.sh` butuh stack yang baru dinaikkan. Pertama kali, `run.sh` membangun JAR Spring Boot contoh di `maven:3-eclipse-temurin-21` dan menyimpannya di `test/e2e/.cache/`.

Membuat arsip rilis: `scripts/release.sh 0.4.0` menghasilkan `dist/runova-0.4.0-linux-amd64.tar.gz` dan `dist/SHA256SUMS`.

## Tangkapan layar

Tangkapan layar di README dan `docs/screenshots/` diambil dari panel sungguhan (`node web/test/screenshots.mjs`, yang mengisi server berbackend palsu), bukan dari preview. Setiap tampilan diambil dua kali, ke `docs/screenshots/light/` dan `docs/screenshots/dark/` dengan nama file yang sama; README menampilkan set terang dan melipat set gelap di bawahnya. Bila stack lokal lain memakai port 20000+, setel `SHOT_PORT_MIN` (misalnya `SHOT_PORT_MIN=34000`). PNG dikompres dengan `optipng`.

## Merilis versi baru

Semua perubahan dicatat di [CHANGELOG.id.md](../CHANGELOG.id.md) (Inggris: [CHANGELOG.md](../CHANGELOG.md)). Untuk merilis, tambahkan bagian `## [X.Y.Z] - tanggal` di kedua berkas, lalu buat tag. Rilis dibangun GitHub Actions (`.github/workflows/release.yml`) setiap ada tag `v*.*.*`: catatan rilis diambil dari bagian changelog itu (`scripts/changelog.sh`; tag tanpa bagian itu gagal), lalu test, `scripts/release.sh`, attestation, dan GitHub Release berisi arsip, `SHA256SUMS`, dan `get.sh`.

```bash
git tag v0.4.0
git push origin v0.4.0
```

Setiap push ke `main` juga menjalankan CI (`.github/workflows/ci.yml`): gofmt, vet, test unit, test integrasi dengan Docker, PostgreSQL 18, dan Caddy sungguhan, serta bats dan shellcheck.
