# Runova Bagian 7: Installer dan Rilis — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Satu perintah memasang Runova di Ubuntu Server 26.04 polos sampai panel bisa dibuka dan aplikasi contoh bisa di-deploy; menjalankan ulang perintah yang sama memperbarui Runova tanpa menyentuh data.

**Architecture:** `install.sh` (bash, `set -euo pipefail`) hanya mengurus hal yang butuh root dan paket OS; setiap langkah berupa fungsi `step_*` yang memeriksa dulu apakah sudah beres (idempoten). Konfigurasi yang bisa dikerjakan binary (kunci, admin, alamat panel) didelegasikan ke subperintah `runova`. Rilis dibangun oleh `scripts/release.sh`. Uji end-to-end di VM Ubuntu 26.04 sungguhan (cloud image + QEMU/KVM lewat `scripts/e2e-vm.sh`), karena Docker-in-container tidak mewakili systemd, ufw, dan Caddy di port 80/443.

**Tech Stack:** bash, systemd, apt, ufw; `shellcheck` untuk lint; QEMU + cloud-init untuk uji.

**Spec:** `docs/spec.md` §2 (lokasi file, konfigurasi, capability), §6 (alamat panel), §7 (pg_hba, role `runova`), §10.

**Bergantung pada:** Bagian 1–6.

## Global Constraints

- Paket: `docker.io caddy postgresql-18 ufw` dari apt resmi Ubuntu; tanpa repo pihak ketiga.
- Rilis: `runova-<versi>-linux-amd64.tar.gz` berisi `runova`, `install.sh`, `runova.service`, `LICENSE`, `SHA256SUMS`. Versi disuntik lewat `-ldflags "-X main.version=<versi>"`; `runova version` mencetaknya.
- Opsi: `--domain <host>` atau `--port <n>` (bawaan `--port 8443` bila tidak ada keduanya), `--yes` (tanpa tanya), `--public-ip <ip>` (opsional, disimpan ke setelan).
- Unit systemd `runova.service`: `User=runova`, `Group=runova`, `SupplementaryGroups=docker`, `EnvironmentFile=/etc/runova/runova.env`, `ExecStart=/usr/local/bin/runova serve`, `AmbientCapabilities=CAP_CHOWN CAP_FOWNER CAP_DAC_OVERRIDE`, `CapabilityBoundingSet=` sama, `NoNewPrivileges=yes`, `ProtectSystem=strict`, `ReadWritePaths=/var/lib/runova /etc/postgresql/18/main/pg_hba.d`, `ProtectHome=yes`, `PrivateTmp=yes`, `Restart=on-failure`, `After=docker.service caddy.service postgresql.service`, `Wants=` yang sama.
- Caddy: `/etc/caddy/Caddyfile` diganti `{ admin localhost:2019 }` hanya bila file masih bawaan paket (cek checksum dari `dpkg-query`/`/var/lib/dpkg/info/caddy.md5sums`); bila sudah diubah admin → berhenti dengan pesan, kecuali `--yes`, yang menyalin cadangan `.runova-backup` dulu. `caddy.service` memakai `--resume` agar konfigurasi API bertahan restart (drop-in `/etc/systemd/system/caddy.service.d/runova.conf`).
- PostgreSQL: `CREATE ROLE runova LOGIN CREATEDB CREATEROLE` (peer), `GRANT EXECUTE ON FUNCTION pg_reload_conf() TO runova`; `listen_addresses = '*'` lewat `/etc/postgresql/18/main/conf.d/runova.conf`; baris `include_dir 'pg_hba.d'` ditambahkan di akhir `pg_hba.conf` bila belum ada; folder `pg_hba.d` milik `postgres:runova` 0770, file awal `runova.conf` berisi subnet Docker.
- ufw: `allow 22/tcp`, `80/tcp`, `443/tcp`, `20000:20999/tcp`, `5432/tcp`, port panel bila mode IP; `ufw --force enable` hanya bila ufw belum aktif dan SSH diizinkan lebih dulu. Docker mem-publish port container ke `127.0.0.1` saja sehingga aturan Docker-vs-ufw tidak membuka port aplikasi.
- Docker: `/etc/docker/daemon.json` digabung (bukan ditimpa) dengan `{"default-address-pools":[{"base":"172.31.0.0/16","size":24}]}` agar jaringan bawaan Docker tidak bentrok dengan `172.30.0.0/16` milik Runova.
- Output akhir (sekali): alamat panel, username `admin`, password. Pada pemasangan ulang password tidak dibuat ulang (admin sudah ada) dan tidak dicetak.
- Uninstall tidak disediakan di versi pertama; dokumentasikan langkah manual di README.

## Review Focus

- **Installer dijalankan dua kali** → kali kedua hanya memperbarui binary dan unit, layanan di-restart, data/kunci/admin/aplikasi utuh, tidak ada password baru dicetak. Test di Task 3 (`e2e: reinstall`).
- **Caddyfile sudah dipakai untuk situs lain** → installer berhenti sebelum mengubah apa pun dengan pesan yang menyebut file dan opsi `--yes`. Test di Task 2 (`bats: refuses modified Caddyfile`).
- **SSH di port non-standar** → ufw tidak mengunci admin keluar: installer membaca port `sshd` dari `sshd -T` dan mengizinkannya. Test di Task 2 (`bats: ssh port detected`).
- **Server tanpa IP publik / di belakang NAT** → instalasi tetap selesai; panel mode IP; pesan akhir menyarankan Cloudflare Tunnel untuk domain. Test di Task 3 (VM tanpa `--public-ip`).
- **Reboot server** → Docker, Caddy, PostgreSQL, Runova hidup kembali; aplikasi berjalan lagi; route Caddy kembali dalam ≤ 30 detik. Test di Task 3 (`e2e: reboot`).

---

### Task 1: Subperintah pendukung dan build rilis

**Files:**
- Create: `scripts/release.sh`, `packaging/runova.service`, `packaging/caddy-runova.conf`, `packaging/postgres-runova.conf`
- Modify: `cmd/runova/main.go` (`version`, `runova check` — memeriksa Docker, Caddy admin, PG, izin folder; keluar 0/1 dengan daftar ✓/✗), `cmd/runova/main_test.go`, `internal/store/settings.go` hanya bila `--public-ip` belum punya jalur tulis (`runova panel set --public-ip`)

**Interfaces:**
- Produces: `runova version` → `runova <versi>`; `runova check` → baris `ok docker 29.x` / `fail caddy: <err>`; `runova panel set --public-ip <ip>`.
- `scripts/release.sh <versi>` → `dist/runova-<versi>-linux-amd64.tar.gz` + `SHA256SUMS`, build `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=<versi>"`.

- [ ] **Step 1: Tulis test gagal**

```go
func TestVersion(t *testing.T) { /* run(["version"]) stdout "runova dev\n" when not injected */ }
func TestCheckReportsFailures(t *testing.T) { /* RUNOVA_DOCKER_HOST=unix:///nonexistent → exit 1, stdout has "fail docker" */ }
func TestPanelSetPublicIP(t *testing.T) { /* "--public-ip 203.0.113.5" stored; "abc" → exit 1 */ }
```

- [ ] **Step 2:** FAIL. **Step 3:** implementasi. **Step 4:** `go test ./cmd/runova` PASS; `scripts/release.sh 0.1.0` menghasilkan arsip, `tar tzf` berisi 5 file, `sha256sum -c` lulus.
- [ ] **Step 5: Commit** `git commit -m "release packaging, version and check commands"`

---

### Task 2: `install.sh`

**Files:**
- Create: `install.sh`, `test/install.bats`, `test/fixtures/` (stub `apt-get`, `systemctl`, `ufw`, `sshd`, `psql`, `dpkg-query`, `docker`, `runova` yang mencatat panggilan ke file log)

**Interfaces:**
- Produces fungsi (dapat diuji terpisah dengan `source install.sh --lib`): `parse_args`, `check_os` (`/etc/os-release` `VERSION_ID="26.04"`, `uname -m` `x86_64`), `step_packages`, `step_user_dirs`, `step_secret` (`runova keygen` bila kunci belum ada), `step_env_file`, `step_docker_daemon`, `step_caddy`, `step_postgres`, `step_firewall`, `step_binary` (salin ke `/usr/local/bin/runova` atomik via `install -m 0755` ke file sementara + `mv`), `step_service`, `step_panel`, `step_admin` (hanya bila belum ada admin: `runova admin create admin`), `summary`.
- Root dibutuhkan (`EUID` 0) kecuali `--lib`. Prefix path bisa diganti lewat `RUNOVA_ROOT` agar bats bisa menguji di folder sementara.

- [ ] **Step 1: Tulis test gagal** (`bats`)

```bash
@test "rejects non-ubuntu-26.04" { os_release 24.04; run check_os; [ "$status" -ne 0 ]; [[ "$output" == *"Ubuntu Server 26.04"* ]]; }
@test "refuses modified Caddyfile" { echo 'x.co { }' > "$RUNOVA_ROOT/etc/caddy/Caddyfile"; run step_caddy; [ "$status" -ne 0 ]; [[ "$output" == *"--yes"* ]]; }
@test "ssh port detected" { SSHD_PORT=2222; run step_firewall; grep -q "allow 2222/tcp" "$CALLS"; ! grep -q "ufw --force enable" <(head -1 "$CALLS"); }
@test "daemon.json merged" { echo '{"log-driver":"local"}' > "$RUNOVA_ROOT/etc/docker/daemon.json"; step_docker_daemon; jq -e '."log-driver"=="local" and ."default-address-pools"[0].base=="172.31.0.0/16"' "$RUNOVA_ROOT/etc/docker/daemon.json"; }
@test "pg_hba include added once" { step_postgres; step_postgres; [ "$(grep -c "include_dir 'pg_hba.d'" "$RUNOVA_ROOT/etc/postgresql/18/main/pg_hba.conf")" -eq 1 ]; }
@test "secret kept on rerun" { step_secret; sum1=$(sha256sum "$RUNOVA_ROOT/etc/runova/secret.key"); step_secret; [ "$sum1" = "$(sha256sum "$RUNOVA_ROOT/etc/runova/secret.key")" ]; }
@test "admin created only once" { step_admin; step_admin; [ "$(grep -c 'admin create' "$CALLS")" -eq 1 ]; }
@test "domain and port exclusive" { run parse_args --domain a.co --port 1; [ "$status" -ne 0 ]; }
```

- [ ] **Step 2:** `bats test/install.bats` → FAIL. **Step 3:** implementasi. **Step 4:** `bats test/install.bats` PASS dan `shellcheck install.sh` tanpa temuan.
- [ ] **Step 5: Commit** `git commit -m "install.sh: idempotent installer"`

---

### Task 3: Uji end-to-end di VM dan dokumentasi

**Files:**
- Create: `scripts/e2e-vm.sh`, `test/e2e/run.sh`, `test/e2e/hello/` (aplikasi Go contoh: `GET /` → "hello <versi>", `GET /db` → jumlah baris tabel di `DATABASE_URL`), `test/e2e/hello-spring/` (JAR Spring Boot kecil yang sudah dibangun, di-commit < 25 MB, atau dibangun di VM dengan Maven bila tersedia — pilih yang pertama)
- Modify: `README.md` (Instalasi, Pembaruan, Uninstall manual, status proyek)

**Interfaces:**
- `scripts/e2e-vm.sh`: unduh cloud image Ubuntu 26.04 (`https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img`, checksum dari `SHA256SUMS`), buat disk overlay, cloud-init dengan kunci SSH sementara, boot QEMU/KVM (4 GB, 2 vCPU, port forward SSH), salin arsip rilis, jalankan `test/e2e/run.sh` lewat SSH, hapus VM.
- `test/e2e/run.sh` (di dalam VM) menjalankan skenario berurutan dan berhenti di kegagalan pertama dengan pesan jelas.

- [ ] **Step 1: Tulis skenario** `test/e2e/run.sh`:
  1. `install` → pesan akhir berisi alamat panel dan password; `runova check` semua ok.
  2. `login` → API login dengan password itu (curl ke `https://127.0.0.1:8443` dengan `-k`).
  3. `user+db` → buat pengguna `budi`, login sebagai budi, buat database `app`.
  4. `deploy-binary` → unggah `hello` (`.tar.gz` dengan `.env.example`), pilih database, akses IP nyala → `curl <ip>:<ip_port>/db` 200.
  5. `deploy-jar` → unggah JAR Spring Boot, Java 21 → `curl` 200.
  6. `zero-downtime` → loop curl selama deploy versi baru `hello` → tidak ada kegagalan, respons berubah ke versi baru.
  7. `isolation` → dari container budi, `psql` ke database pengguna lain → ditolak; `curl` ke container pengguna lain di jaringan Docker → timeout.
  8. `reinstall` → jalankan installer lagi → tidak ada password baru dicetak, aplikasi tetap 200, admin login dengan password lama.
  9. `reboot` → `sudo reboot`, tunggu SSH, ≤ 60 detik kemudian kedua aplikasi 200.
  10. `backup` → backup manual → restore → data kembali.
- [ ] **Step 2:** `scripts/e2e-vm.sh dist/runova-0.1.0-linux-amd64.tar.gz` → catat kegagalan. **Step 3:** perbaiki di bagian terkait (installer, unit, atau kode). **Step 4:** semua skenario lulus.
- [ ] **Step 5:** Perbarui README: hapus "tahap rancangan", tambahkan Instalasi (`curl -LO …; tar xzf …; sudo ./install.sh --domain panel.contoh.id`), Pembaruan (jalankan ulang), Uninstall manual (hentikan service, hapus `/usr/local/bin/runova`, `/etc/runova`, `/var/lib/runova`, role & database PostgreSQL, container berlabel `runova.app`).
- [ ] **Step 6: Commit** `git commit -m "e2e on ubuntu 26.04 vm, installation docs"`
