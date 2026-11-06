# Operasional: pembaruan, rollback, detail installer, uninstall

[English](operations.md) · **Bahasa Indonesia**

Rujukan harian untuk admin server. Cadangan dan pemecahan masalah ada di [Memasang di Ubuntu](install-ubuntu.id.md#8-cadangan).

## Detail installer

Syarat: Ubuntu Server 26.04 LTS (amd64) polos, user dengan `sudo`, dan koneksi internet ke repositori apt Ubuntu. Installer memasang `docker.io`, `caddy`, `postgresql-18`, `ufw`, `jq`, dan `curl` dari apt resmi Ubuntu; tidak ada repositori pihak ketiga.

Panduan langkah demi langkah (spesifikasi, firewall cloud, DNS, cadangan, pemecahan masalah) ada di [Memasang di Ubuntu](install-ubuntu.id.md).

`get.sh` mengunduh rilis terbaru dari [GitHub Releases](https://github.com/aribrilliantsyah/runova/releases), mencocokkan `SHA256SUMS` (arsip dan isinya), lalu menjalankan `install.sh` dengan opsi Anda. `--version 0.3.1` memasang versi tertentu. Setiap rilis juga punya [attestation](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) asal-usul build: `gh attestation verify runova-<versi>-linux-amd64.tar.gz -R aribrilliantsyah/runova`.

Dari checkout source (misalnya untuk mencoba perubahan yang belum dirilis), `scripts/deploy.sh admin@IP-SERVER [opsi]` membangun rilis lokal, menyalinnya lewat SSH, lalu menjalankan installer yang sama.

Opsi `install.sh`: `--domain <host>` atau `--port <n>` (tidak bisa bersamaan; tanpa keduanya bawaannya `--port 8443`), `--public-ip <ip>` (disimpan ke setelan; untuk server di belakang NAT), `--skip-firewall` (jangan menyentuh `ufw`), `--yes` (tanpa tanya, untuk instalasi tanpa terminal). Untuk `--domain`, arahkan DNS domain itu ke server dulu.

`--trusted-proxy <ip/cidr>` (bisa diulang) untuk server di belakang nginx yang meneruskan ke Runova; lihat [nginx di depan Runova](nginx-front-proxy.id.md).

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

## Setelah server reboot

Tidak ada yang perlu dilakukan manual. Installer mengaktifkan `docker`, `containerd` (bila ada), `caddy`, `postgresql`, dan `runova` dengan `systemctl enable`, jadi semuanya naik saat boot, dan `runova.service` start setelah Docker.

| Apa | Siapa yang menaikkan lagi |
|---|---|
| Panel, Caddy, PostgreSQL | systemd |
| Aplikasi berstatus **running** atau **starting** | Runova, saat start |
| Aplikasi yang **dihentikan** pengguna, atau **gagal** (crash loop, tidak pernah siap) | tidak ada: tetap mati sampai ada yang menekan Jalankan |

Docker menghentikan container saat shutdown dengan SIGTERM. Kebanyakan server (Spring Boot, server Go dengan signal handling) keluar dengan kode 0, dan restart policy `on-failure` tidak pernah menyalakan lagi exit 0, jadi Docker sendiri tidak menaikkannya. Saat Runova start, ia mencari setiap aplikasi yang semestinya jalan tapi container-nya berhenti, lalu menyalakannya: status `starting`, lalu `running` setelah port terbuka, dengan penantian yang sama seperti Jalankan (batas waktu JAR, deteksi OOM dan restart). Aplikasi dinyalakan satu per 5 detik, tidak serentak, supaya banyak JVM tidak berebut CPU. Tiap aplikasi yang dinyalakan lagi memunculkan satu notifikasi ("Aplikasi … dinyalakan lagi setelah server dimulai ulang") dan satu entri Aktivitas; aplikasi yang tidak bisa dinyalakan berakhir `failed` dengan notifikasi error. Container tidak pernah dibuat ulang: container yang terhapus tetap menandai aplikasi gagal ("deploy ulang").

Ini terjadi sekali setiap Runova start. Container yang keluar dengan kode 0 saat Runova sedang berjalan tetap dianggap keputusan aplikasinya sendiri dan ditandai berhenti.

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
