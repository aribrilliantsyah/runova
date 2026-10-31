# Instalasi ke server: catatan dan temuan

Branch `feat/server-install`. Tidak ada perubahan di `cmd/runova/**`, `internal/**`, `web/**`, atau `docs/spec.md`; README hanya bagian Instalasi, banner status, Status pengujian, dan Uji installer (bagian Pembaruan tidak disentuh).

## Yang dibuat

1. **`scripts/deploy.sh user@host [opsi install.sh...]`.** Memeriksa SSH, Ubuntu 26.04, amd64, dan sudo (pesan Indonesia dengan langkah perbaikan) sebelum membangun apa pun; lalu `scripts/release.sh`, `scp` arsip + `SHA256SUMS`, `sha256sum -c` dan `tar xzf` di `mktemp -d /tmp/runova-<versi>.XXXXXX`, `ssh -t ... sudo ./install.sh <opsi>`, dan folder sementara dihapus (juga saat gagal). Satu koneksi SSH dipakai ulang (`ControlMaster`) sehingga password SSH ditanya sekali. `--version`, `-p/--ssh-port`, `-i/--identity`, `RUNOVA_SSH_OPTS`. Opsi tak dikenal setelah `user@host` diteruskan ke `install.sh`.
2. **Versi dari git.** `git describe --tags --always --dirty` tidak selalu cocok dengan regex `release.sh` (`X.Y.Z[-sufiks]`): `v0.1.0-3-gabc-dirty` menjadi `0.1.0-3.gabc.dirty`; tanpa tag, `abc-dirty` menjadi `0.0.0-dev.abc.dirty`.
3. **`install.sh --skip-firewall`** (untuk firewall yang diatur di tempat lain). Dipakai juga bila `ufw` gagal memuat iptables di lingkungan tertentu; di container uji ini tidak perlu (lihat bawah).
4. **`docs/install-ubuntu.md`**, README Instalasi diringkas dan menautkannya. Pemasangan `curl | bash` sengaja tidak dibuat; README menyebut itu menyusul saat ada halaman rilis.
5. **`test/system/`** (`Dockerfile`, `run.sh`): Ubuntu 26.04 + systemd PID 1 + `openssh-server` + user `deploy` ber-sudo; `run.sh` memasang lewat `scripts/deploy.sh` ke `127.0.0.1:2222` dan menjalankan 7 skenario. `test/deploy.bats` (stub `ssh`/`scp`/`go`/`git` di `test/fixtures/deploy-bin`) menguji pemetaan versi, parsing opsi, dan semua pemeriksaan awal; `test/bats.sh` menjalankannya dan `shellcheck` untuk `scripts/*.sh` dan `test/system/*.sh`.

## Temuan saat uji nyata

- **Installer berjalan tanpa perubahan** di Ubuntu 26.04 dengan systemd: `apt-get install` utuh (`docker.io` 29.1.3, `caddy` 2.6.2, `postgresql-18` 18.6 dengan klaster `18/main` dibuat oleh paket), `systemctl enable/restart`, drop-in Caddy `--resume`, `include_dir pg_hba.d`, Docker-in-Docker (storage driver `overlayfs` containerd snapshotter), `ufw --force enable` dan aturannya. Tidak ada bug installer yang ditemukan. `ufw` **berhasil** memuat aturan di container privileged (kernel mesin pengembang), jadi `--skip-firewall` tidak dibutuhkan oleh uji.
- **Sumber galat uji, bukan galat produk:**
  - Container berlapis perlu volume untuk `/var/lib/docker` dan `/var/lib/containerd`; tanpa itu dockerd di dalam overlay gagal `failed to mount ... overlay` saat membuat container aplikasi (deploy pertama gagal). Dockerfile/`run.sh` kini memasang `-v /var/lib/docker -v /var/lib/containerd`.
  - `policy-rc.d` bawaan image dihapus di Dockerfile agar `apt` menjalankan layanan seperti server asli.
  - `runova check` dijalankan sebagai root gagal `Peer authentication failed for user "runova"`: wajar, check harus dijalankan sebagai user `runova` (README/panduan memakai `sudo -u runova env RUNOVA_DATA=/var/lib/runova runova check`; `run.sh` memakai `runuser`).
- **Cacat kecil, belum diubah (di `cmd/runova/**`, area sesi lain):** `runova check` mencetak `ok caddy ` dengan detail kosong. Usulan untuk koordinator: isi detail (misalnya versi atau alamat admin) di `newCaddyLoader`/probe `caddy`.
- **Log Caddy di container:** `failed to install root certificate ... sudo ... user NOT in sudoers`. Caddy mencoba memasang CA internalnya ke trust store sistem; tidak berbahaya (sertifikat tetap diterbitkan). Dicatat di bagian pemecahan masalah panduan.
- **Bug uji yang diperbaiki di `run.sh`:** menunggu panel naik setelah restart memakai `curl` tanpa `-f`, sehingga 502 dari Caddy (Runova belum siap) dianggap "menjawab". Kini menunggu 200/401.
- **`systemctl is-system-running` = `degraded`** di container karena `systemd-modules-load.service` gagal (tidak ada akses modul kernel); tidak memengaruhi Runova. Uji menerima `running` atau `degraded`.

## Belum terbukti

Kernel dan boot server sungguhan (container memakai kernel host), reboot, interaksi `ufw` dengan aturan iptables Docker di server, `ufw` di luar container, dan sertifikat publik (Caddy hanya menerbitkan sertifikat internal di uji). Uji sistem butuh internet (apt, `gcr.io/distroless`); sekitar 7 menit.

## Hasil `scripts/deploy.sh`

`run.sh` skenario 1 dan 5 menjalankan `scripts/deploy.sh --version 0.0.1-systest deploy@127.0.0.1 --yes` dan `--version 0.0.2-systest`: password tercetak sekali, pemasangan ulang tanpa password, `secret.key` identik, `/tmp/runova-*` bersih di server.
