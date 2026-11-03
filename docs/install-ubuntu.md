# Memasang Runova di Ubuntu Server 26.04

Panduan untuk admin server. Hasil akhirnya: panel Runova berjalan di server Anda, lengkap dengan Docker, Caddy (HTTPS otomatis), dan PostgreSQL 18, siap dipakai pengguna untuk men-deploy aplikasi.

## 1. Spesifikasi server

Runova hanya mendukung **Ubuntu Server 26.04 LTS, amd64**, dipasang polos (belum ada Docker, Caddy, atau PostgreSQL yang Anda atur sendiri).

| | Minimum | Disarankan |
|---|---|---|
| CPU | 2 vCPU | 4 vCPU |
| RAM | 4 GB | 8 GB atau lebih |
| Disk | 40 GB | 80 GB SSD atau lebih |

Asumsi angka ini: panel, Caddy, PostgreSQL, dan Docker sendiri memakai sekitar 1 GB RAM; sisanya dibagi ke aplikasi pengguna. Aplikasi Java biasanya butuh 256-512 MB per aplikasi, binary Go atau Rust jauh lebih kecil. Disk dipakai image Docker (image Java sekitar 200-300 MB per versi), artefak yang diunggah, data aplikasi, database, dan log. Storage pengguna tidak dikuota, jadi pantau disk di halaman **Server**.

Server butuh akses internet keluar ke repositori apt Ubuntu dan, saat deploy pertama, ke Docker Hub dan `gcr.io` untuk image dasar. Setelah image ada, panel tidak memuat apa pun dari luar.

## 2. Menyiapkan server baru

Masuk sebagai user yang disediakan penyedia server (biasanya `ubuntu` atau `root`), lalu:

```bash
# perbarui sistem
sudo apt-get update && sudo apt-get -y full-upgrade

# user admin dengan sudo (lewati bila sudah punya)
sudo adduser admin
sudo usermod -aG sudo admin
```

Dari **mesin Anda**, pasang kunci SSH supaya tidak perlu password untuk SSH:

```bash
ssh-keygen -t ed25519            # bila belum punya kunci
ssh-copy-id admin@IP-SERVER
ssh admin@IP-SERVER              # pastikan masuk tanpa password SSH
```

`sudo` tetap boleh meminta password; skrip deploy memakai terminal sehingga password bisa diketik. Hanya setelah kunci SSH berfungsi, Anda boleh mematikan login password SSH (`PasswordAuthentication no` di `/etc/ssh/sshd_config.d/`). Jangan menutup sesi SSH lama sebelum mencoba sesi baru.

Bila server memakai port SSH selain 22, installer mengenalinya dan mengizinkannya di `ufw`.

## 3. Firewall penyedia cloud

Selain `ufw` di server (diatur installer), banyak penyedia cloud punya firewall sendiri (security group, firewall jaringan). Buka di sana:

| Port | Untuk | Wajib |
|---|---|---|
| 22/tcp (atau port SSH Anda) | SSH | ya |
| 80/tcp | Caddy: tantangan sertifikat dan pengalihan ke HTTPS | ya, bila memakai domain |
| 443/tcp | Caddy: HTTPS aplikasi dan panel (mode domain) | ya, bila memakai domain |
| port panel, bawaan 8443/tcp | panel di `https://IP:8443` (mode IP) | ya, bila tanpa domain |
| 20000-20999/tcp | akses IP aplikasi untuk pengujian | opsional, untuk uji lewat IP |
| 5432/tcp | PostgreSQL dari luar (DBeaver dan sejenisnya) | opsional |

Port 5432 hanya menerima koneksi dari jaringan Docker dan jaringan yang Anda izinkan di panel; membukanya di firewall cloud tidak membuka database untuk semua orang, tetapi jangan buka bila tidak perlu.

## 4. DNS untuk domain panel

Lewati bagian ini bila panel diakses lewat `https://IP:8443`.

1. Buat record **A**: `panel.contoh.id` → IP publik server.
2. **Cloudflare:** saat pertama, set proxy ke **DNS only** (awan abu-abu) supaya Caddy bisa menerbitkan sertifikat. Setelah panel terbuka lewat HTTPS, boleh diubah ke **Proxied** dengan SSL/TLS **Full (strict)**.
3. Tunggu DNS menyebar (`dig +short panel.contoh.id` harus menampilkan IP server), baru jalankan installer dengan `--domain`.

**Server tanpa IP publik** (di belakang NAT atau jaringan kantor): domain tidak bisa diarahkan langsung. Pakai **Cloudflare Tunnel**: di Cloudflare Zero Trust, Networking → Tunnels, pasang `cloudflared` di server, lalu tambahkan Published application dengan Service URL `http://localhost:80` (Caddy). HTTPS diurus Cloudflare. Panel tetap bisa dipakai lewat `https://IP-LAN:8443` dari jaringan yang sama.

## 5. Menjalankan instalasi

Masuk ke server lewat SSH, lalu jalankan **satu perintah** ini:

```bash
# panel di https://panel.contoh.id (DNS sudah menunjuk ke server, lihat bagian 4)
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --domain panel.contoh.id

# atau panel di https://IP-SERVER:8443
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --port 8443 --public-ip IP-PUBLIK-SERVER
```

Installer menampilkan ringkasan dan bertanya **Lanjutkan? [y/N]**; ketik `y`. Instalasi pertama butuh beberapa menit (apt memasang Docker, Caddy, dan PostgreSQL).

`get.sh` mengunduh rilis terbaru dari GitHub Releases, mencocokkan `SHA256SUMS` untuk arsip dan setiap berkas di dalamnya, lalu menjalankan `install.sh`. Semua opsi setelah `--` diteruskan ke installer:

| Opsi | Arti |
|---|---|
| `--domain <host>` | panel di `https://<host>` |
| `--port <n>` | panel di `https://<IP>:<n>` (bawaan 8443; bukan 2019, 5432, 7070, 20000-20999) |
| `--public-ip <ip>` | IP publik server (dipakai panduan domain dan detail koneksi database) |
| `--skip-firewall` | jangan menyentuh `ufw` (firewall diatur di tempat lain) |
| `--yes` | tanpa pertanyaan (untuk otomasi, misalnya cloud-init) |
| `--version x.y.z` | pasang versi tertentu, bukan yang terbaru (opsi `get.sh`) |

`--domain` dan `--port` tidak bisa bersamaan. Tanpa keduanya, bawaannya `--port 8443`.

**Catat password admin yang tercetak di akhir.** Password itu hanya ditampilkan sekali. Contoh akhir keluaran:

```
Runova 0.1.0 siap.

Alamat panel : https://203.0.113.15:8443
Sertifikat dibuat Caddy sendiri, jadi peramban memberi peringatan; lanjutkan saja.
Username     : admin
Password     : ••••••••••••••••••••
```

### Lebih hati-hati: periksa dulu sebelum menjalankan

```bash
V=0.1.0
curl -fsSLO https://github.com/aribrilliantsyah/runova/releases/download/v$V/runova-$V-linux-amd64.tar.gz
curl -fsSLO https://github.com/aribrilliantsyah/runova/releases/download/v$V/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify runova-$V-linux-amd64.tar.gz -R aribrilliantsyah/runova   # opsional, butuh gh
tar xzf runova-$V-linux-amd64.tar.gz
cd runova-$V-linux-amd64
sudo ./install.sh --domain panel.contoh.id      # atau --port 8443
```

### Dari source di mesin Anda

Untuk mencoba perubahan yang belum dirilis, dari checkout source Runova (butuh Go, `ssh`, `scp`):

```bash
scripts/deploy.sh admin@IP-SERVER --domain panel.contoh.id
```

Skrip ini memeriksa SSH, Ubuntu 26.04 amd64, dan `sudo` di server; membangun rilis lokal; menyalinnya; lalu menjalankan installer yang sama. Opsi tambahan: `--version`, `-p/--ssh-port`, `-i/--identity`, dan `RUNOVA_SSH_OPTS` (misalnya `RUNOVA_SSH_OPTS="-o ProxyJump=bastion"`).

Installer memasang `docker.io`, `caddy`, `postgresql-18`, `ufw`, `jq`, dan `curl` dari apt Ubuntu; membuat user `runova`, kunci enkripsi, dan layanan systemd; mengatur Caddy dan PostgreSQL; membuka port di `ufw` (SSH diizinkan lebih dulu sebelum `ufw` diaktifkan); lalu menjalankan `runova check`. Ringkasan lengkap ada di [README](../README.md#instalasi).

## 6. Setelah instalasi

1. Buka alamat panel yang dicetak installer. Pada mode IP, sertifikat dibuat Caddy sendiri sehingga peramban memberi peringatan; lanjutkan saja.
2. Masuk sebagai `admin` dengan password yang tercetak, lalu **ganti password admin** dari menu akun.
3. Buka halaman **Server** dan isi **IP publik** server. Tanpa ini, panduan domain di aplikasi hanya menawarkan Cloudflare Tunnel.
4. Di halaman **Pengguna**, buat pengguna beserta kuotanya (jumlah aplikasi, memori, CPU, database).
5. Uji deploy: masuk sebagai pengguna itu, unggah binary Linux kecil atau `.jar`, jalankan wizard deploy dengan **akses IP** menyala, lalu buka `http://IP-SERVER:<port akses IP>`. Bila terbuka, Docker, Caddy, dan alurnya sehat. Deploy `.jar` pertama menarik image `eclipse-temurin` sehingga butuh internet dan waktu.

Password admin hilang: `sudo -u runova env RUNOVA_DATA=/var/lib/runova runova admin reset-password admin`.

## 7. Pembaruan

Jalankan perintah instalasi lagi. Tanpa `--domain`/`--port`, alamat panel tetap:

```bash
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s --
```

Installer idempoten: langkah yang sudah beres dilewati, binary dan unit diganti, Runova di-restart. Data, kunci, admin, aplikasi, dan alamat panel tidak disentuh, dan tidak ada password baru dicetak. Skema database diperbarui otomatis; sebelumnya Runova menyalin `runova.db` ke `/var/lib/runova/runova.db.bak-v<versi>-<waktu>`. Versi terpasang: `runova version`.

## 8. Cadangan

Yang harus dicadangkan:

| Yang | Isi |
|---|---|
| `/etc/runova/secret.key` | **Kunci enkripsi.** Semua rahasia (environment rahasia, password database) terenkripsi dengan kunci ini. **Tanpa kunci ini rahasia tidak bisa dibuka**, dan tidak ada cara memulihkannya. Simpan salinannya di tempat terpisah dari server. |
| `/var/lib/runova` | Database Runova (`runova.db`), artefak yang diunggah, data aplikasi (`data/`), dan backup harian database (`backup/`). |
| PostgreSQL | Database pengguna. |

Contoh cadangan folder data (hentikan Runova sebentar agar `runova.db` konsisten):

```bash
sudo systemctl stop runova
sudo tar czf /root/runova-data-$(date +%F).tar.gz -C / var/lib/runova etc/runova/secret.key
sudo systemctl start runova
```

Aplikasi yang berjalan tidak ikut berhenti; Runova menyambung kembali saat menyala.

**PostgreSQL:** Runova sudah membuat `pg_dump` per database tiap pukul 02:00 dan menyimpannya 7 hari di `/var/lib/runova/backup/<database>/`, tetapi itu di disk yang sama dengan datanya. Salin folder itu ke luar server, dan/atau buat dump seluruh klaster:

```bash
sudo -u postgres pg_dumpall | gzip > /root/pg-semua-$(date +%F).sql.gz
```

Pulihkan ke server baru: pasang Runova dengan installer, hentikan `runova`, kembalikan `/var/lib/runova` dan `/etc/runova/secret.key` (pemilik `runova:runova`), jalankan `sudo systemctl start runova`, lalu pulihkan database dari halaman **Database** (backup) atau `pg_restore`.

## 9. Pemecahan masalah

**Pemeriksaan pertama:**

```bash
sudo -u runova env RUNOVA_DATA=/var/lib/runova runova check
sudo journalctl -u runova -u caddy -u postgresql -n 100 --no-pager
```

`runova check` mencetak `ok` atau `fail <nama>: <alasan>` untuk Docker, `docker-init`, Caddy, PostgreSQL, kunci, folder data, dan `pg_hba.d`; keluar dengan kode 1 bila ada yang gagal.

**Port 80/443 bentrok.** Installer atau Caddy gagal start dengan `address already in use`. Cari pemakainya dan hentikan:

```bash
sudo ss -ltnp 'sport = :80 or sport = :443'
```

Biasanya Apache atau nginx bawaan: `sudo systemctl disable --now apache2` atau `nginx`. Lalu `sudo systemctl restart caddy`.

**Sertifikat belum terbit.** Lihat `journalctl -u caddy`. Penyebab umum: DNS belum menunjuk ke server (`dig +short domain`), port 80/443 tertutup di firewall cloud atau `ufw`, record Cloudflare masih **Proxied** saat pertama kali (ubah ke DNS only), atau batas penerbitan Let's Encrypt tercapai karena terlalu banyak percobaan. Pesan `failed to install root certificate ... sudo` dari Caddy di journal tidak berbahaya: Caddy mencoba memasang sertifikat internalnya ke sistem.

**`ufw` mengunci SSH.** Installer mengizinkan port SSH sebelum mengaktifkan `ufw`, dengan membaca `sshd -T`, `ss`, dan `ssh.socket`. Bila tetap terkunci, masuk lewat konsol penyedia server dan jalankan:

```bash
sudo ufw allow <port-ssh>/tcp
sudo ufw status verbose
```

Bila firewall diatur di tempat lain, pasang dengan `--skip-firewall`.

**Aplikasi tidak bisa dijangkau lewat IP:port.** Pastikan port 20000-20999 terbuka di `ufw` dan di firewall cloud, dan akses IP aplikasi menyala di panel.

**Docker memakai jaringan yang bentrok dengan jaringan kantor.** Installer memindahkan jaringan bawaan Docker ke `172.31.0.0/16`, dan Runova memakai `172.30.0.0/16` untuk jaringan pengguna. Bila jaringan Anda memakai rentang itu, atur `/etc/docker/daemon.json` dan jangan lupa `default-address-pools`.

**Installer berhenti di tengah.** Baca pesan galatnya, perbaiki, lalu jalankan perintah yang sama; langkah yang sudah selesai dilewati.
