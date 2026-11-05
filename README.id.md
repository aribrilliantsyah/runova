<p align="center">
  <img src="docs/brand/runova-app-icon.svg" width="128" height="128" alt="Runova">
</p>

<h1 align="center">Runova</h1>

<p align="center"><a href="README.md">English</a> · <b>Bahasa Indonesia</b></p>

<p align="center">
  Jalankan aplikasi jadi Anda di server sendiri.<br>
  Unggah <code>.jar</code> atau binary Linux, dan Runova menjalankannya, memberinya domain dengan HTTPS, dan menjaganya tetap hidup.
</p>

<p align="center">
  <a href="https://github.com/aribrilliantsyah/runova/releases/latest"><img src="https://img.shields.io/github/v/release/aribrilliantsyah/runova?label=rilis" alt="Rilis terbaru"></a>
  <a href="https://github.com/aribrilliantsyah/runova/actions/workflows/ci.yml"><img src="https://github.com/aribrilliantsyah/runova/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/lisensi-Apache--2.0-blue" alt="Lisensi Apache-2.0"></a>
</p>

<p align="center">
  <img src="docs/screenshots/light/desktop-apps.png" alt="Daftar aplikasi di Runova: kuota akun, kartu aplikasi dengan logo bahasa, status, domain, dan akses IP" width="860">
</p>

## Yang Anda dapat

- **Unggah hasil build, langsung jalan.** Taruh file `.jar` (Java, Kotlin, Scala, …) atau binary Linux (Go, Rust, C++, .NET, …). Runova membaca filenya, memilih runtime yang pas, lalu menjalankannya. Tanpa source code, tanpa proses build.
- **Domain sendiri, HTTPS sudah termasuk.** Beri aplikasi alamat seperti `billing.perusahaan.co.id`, atau biarkan tim memilih nama di bawah domain yang Anda daftarkan sekali. Sertifikat diurus otomatis.
- **Database untuk setiap pengguna.** PostgreSQL ikut terpasang. Tiap pengguna membuat database di panel, dan data koneksinya terisi sendiri ke aplikasi.
- **File dan query dari browser.** Lihat, unggah, dan unduh file aplikasi, intip isi tabel, dan jalankan SQL tanpa memasang apa pun.
- **Semua terpisah rapi.** Tiap aplikasi berjalan di container sendiri dengan batas memori, CPU, dan disk. Pengguna hanya melihat aplikasinya sendiri, dan admin mengatur kuota.
- **Terlihat apa yang terjadi.** Langkah deploy beserta waktunya, log langsung, terminal, kembali ke versi sebelumnya, dan log aktivitas.
- **Update dengan satu perintah.** `sudo runova update`, dan `sudo runova rollback` bila ingin kembali.

## Sekilas tampilan

### Mode terang

<table>
  <tr>
    <td width="50%"><img src="docs/screenshots/light/desktop-deploy.png" alt="Halaman Deploy: file yang diunggah terdeteksi, versi Java dan port dipilih, ringkasan di kanan menunjukkan semuanya siap"><br><sub><b>Deploy.</b> Unggah file, periksa hasil deteksi, tekan Deploy.</sub></td>
    <td width="50%"><img src="docs/screenshots/light/desktop-deployments.png" alt="Detail deployment dengan timeline langkah beserta durasinya"><br><sub><b>Timeline deploy.</b> Setiap langkah dengan durasinya, dan alasan yang jelas bila ada yang gagal.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/light/desktop-app.png" alt="Halaman aplikasi: kontrol, domain, log, dan resource"><br><sub><b>Halaman aplikasi.</b> Kontrol, domain, log, dan pemakaian resource di satu tempat.</sub></td>
    <td><img src="docs/screenshots/light/desktop-files.png" alt="Pengelola file di tab File dengan folder, folder tersimpan, dan ukuran file"><br><sub><b>Pengelola file.</b> Lihat dan kelola yang disimpan aplikasi Anda.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/light/desktop-database-tables.png" alt="Halaman Database dengan penjelajah tabel yang menampilkan baris tabel invoices"><br><sub><b>Tabel database.</b> Intip isi database tanpa alat terpisah.</sub></td>
    <td><img src="docs/screenshots/light/desktop-database-query.png" alt="Editor query SQL dengan tabel hasil"><br><sub><b>Query.</b> Jalankan SQL dan unduh hasilnya sebagai CSV.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/light/desktop-activity.png" alt="Log aktivitas dengan pencarian dan filter pelaku serta aksi"><br><sub><b>Aktivitas.</b> Siapa melakukan apa dan kapan, dengan pencarian dan filter.</sub></td>
    <td><img src="docs/screenshots/light/desktop-server-hosting.png" alt="Halaman Server dengan domain hosting: domain terdaftar dan record DNS yang perlu dibuat"><br><sub><b>Server dan domain.</b> Daftarkan domain sekali; panel menunjukkan record DNS yang tepat.</sub></td>
  </tr>
</table>

Di HP, panel berpindah ke navigasi bawah:

<table>
  <tr>
    <td width="25%"><img src="docs/screenshots/light/mobile-apps.png" alt="Daftar aplikasi di HP"></td>
    <td width="25%"><img src="docs/screenshots/light/mobile-app.png" alt="Halaman aplikasi di HP"></td>
    <td width="25%"><img src="docs/screenshots/light/mobile-deploy.png" alt="Halaman Deploy di HP"></td>
    <td width="25%"><img src="docs/screenshots/light/mobile-database-query.png" alt="Hasil query SQL di HP"></td>
  </tr>
</table>

<details>
<summary><b>Mode gelap (klik untuk membuka)</b></summary>
<br>

<table>
  <tr>
    <td width="50%"><img src="docs/screenshots/dark/desktop-deploy.png" alt="Halaman Deploy: file yang diunggah terdeteksi, versi Java dan port dipilih, ringkasan di kanan menunjukkan semuanya siap (mode gelap)"><br><sub><b>Deploy.</b> Unggah file, periksa hasil deteksi, tekan Deploy.</sub></td>
    <td width="50%"><img src="docs/screenshots/dark/desktop-deployments.png" alt="Detail deployment dengan timeline langkah beserta durasinya (mode gelap)"><br><sub><b>Timeline deploy.</b> Setiap langkah dengan durasinya, dan alasan yang jelas bila ada yang gagal.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/dark/desktop-app.png" alt="Halaman aplikasi: kontrol, domain, log, dan resource (mode gelap)"><br><sub><b>Halaman aplikasi.</b> Kontrol, domain, log, dan pemakaian resource di satu tempat.</sub></td>
    <td><img src="docs/screenshots/dark/desktop-files.png" alt="Pengelola file di tab File dengan folder, folder tersimpan, dan ukuran file (mode gelap)"><br><sub><b>Pengelola file.</b> Lihat dan kelola yang disimpan aplikasi Anda.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/dark/desktop-database-tables.png" alt="Halaman Database dengan penjelajah tabel yang menampilkan baris tabel invoices (mode gelap)"><br><sub><b>Tabel database.</b> Intip isi database tanpa alat terpisah.</sub></td>
    <td><img src="docs/screenshots/dark/desktop-database-query.png" alt="Editor query SQL dengan tabel hasil (mode gelap)"><br><sub><b>Query.</b> Jalankan SQL dan unduh hasilnya sebagai CSV.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/dark/desktop-activity.png" alt="Log aktivitas dengan pencarian dan filter pelaku serta aksi (mode gelap)"><br><sub><b>Aktivitas.</b> Siapa melakukan apa dan kapan, dengan pencarian dan filter.</sub></td>
    <td><img src="docs/screenshots/dark/desktop-server-hosting.png" alt="Halaman Server dengan domain hosting: domain terdaftar dan record DNS yang perlu dibuat (mode gelap)"><br><sub><b>Server dan domain.</b> Daftarkan domain sekali; panel menunjukkan record DNS yang tepat.</sub></td>
  </tr>
</table>

Di HP, panel berpindah ke navigasi bawah:

<table>
  <tr>
    <td width="25%"><img src="docs/screenshots/dark/mobile-apps.png" alt="Daftar aplikasi di HP (mode gelap)"></td>
    <td width="25%"><img src="docs/screenshots/dark/mobile-app.png" alt="Halaman aplikasi di HP (mode gelap)"></td>
    <td width="25%"><img src="docs/screenshots/dark/mobile-deploy.png" alt="Halaman Deploy di HP (mode gelap)"></td>
    <td width="25%"><img src="docs/screenshots/dark/mobile-database-query.png" alt="Hasil query SQL di HP (mode gelap)"></td>
  </tr>
</table>

</details>

## Pasang

**Yang dibutuhkan:**

- Server dengan **Ubuntu Server 26.04 LTS** (64-bit, `amd64`) yang belum dipasangi Docker, Caddy, atau PostgreSQL. Minimal 2 core CPU, RAM 4 GB, dan disk 40 GB.
- User yang bisa menjalankan `sudo`, dan akses internet dari server.
- *Opsional:* nama domain, bila ingin panel di `https://panel.perusahaan.co.id`. Tanpa domain, panel terbuka di `https://<IP server>:8443`.

**Langkah:**

1. Masuk ke server lewat SSH.
2. Salin dan tempel **salah satu** perintah ini:

   ```bash
   # Panel di alamat IP (paling mudah, tidak ada yang perlu disiapkan dulu)
   curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --port 8443

   # Panel di domain sendiri (arahkan record DNS A domain itu ke server dulu)
   curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --domain panel.contoh.id
   ```

3. Installer menampilkan apa yang akan dilakukan dan bertanya **Continue? [y/N]**. Ketik `y`. Pemasangan pertama butuh beberapa menit karena memasang Docker, Caddy, dan PostgreSQL dari repositori resmi Ubuntu.
4. Di akhir, installer mencetak alamat panel, username `admin`, dan **password yang hanya tampil sekali**. Salin.
5. Buka alamat itu di browser dan masuk. Pada opsi alamat IP, browser menampilkan peringatan sertifikat karena server membuat sertifikatnya sendiri; lanjutkan saja.
6. Ganti password admin lewat menu akun, buka **Server** dan isi IP publik server, lalu buat pengguna pertama di **Pengguna**.

Lupa password? `sudo -u runova env RUNOVA_DATA=/var/lib/runova runova admin reset-password admin`

Membuka firewall di penyedia cloud, mengatur DNS, menaruh Runova di belakang nginx, dan mengatasi masalah ada di **[panduan pemasangan lengkap](docs/install-ubuntu.id.md)** dan **[nginx di depan Runova](docs/nginx-front-proxy.id.md)**.

## Perbarui

Di server:

```bash
sudo runova update      # minta konfirmasi, lalu memasang rilis terbaru
sudo runova rollback    # kurang cocok? kembali ke versi sebelumnya
```

<p><img src="docs/screenshots/light/desktop-update-banner.png" alt="Banner pembaruan di bawah topbar, tampil untuk admin saat ada versi baru" width="720"></p>

Admin juga melihat banner di panel saat ada versi baru. Aplikasi, pengguna, data, dan alamat panel tidak diubah. Detail lengkap di [Operasional](docs/operations.id.md).

## Pelajari lebih lanjut

- [Memasang di Ubuntu](docs/install-ubuntu.id.md): spesifikasi server, firewall, DNS, cadangan, pemecahan masalah
- [nginx di depan Runova](docs/nginx-front-proxy.id.md): untuk server di belakang web server lain
- [Domain dan HTTPS](docs/domains.id.md): domain hosting bersama, domain sendiri, Cloudflare
- [Operasional](docs/operations.id.md): pembaruan, rollback, detail installer, uninstall
- [Arsitektur](docs/architecture.id.md): cara kerja isolasi, kuota, database, dan file; apa saja yang bisa dijalankan
- [Pengembangan](docs/development.id.md): menjalankan lokal, pengujian, merilis
- [Spesifikasi](docs/spec.md) dan [Changelog](CHANGELOG.id.md)

## Lisensi dan kredit

Runova dirilis dengan [Apache License 2.0](LICENSE); lihat juga [`NOTICE`](NOTICE). Aset pihak ketiga di panel memakai lisensinya masing-masing (`web/vendor/licenses/`).

- Ikon UI: [Lucide](https://lucide.dev), lisensi ISC.
- Logo bahasa pemrograman: [Devicon](https://devicon.dev), lisensi MIT. Merek dagang milik pemiliknya masing-masing.
- Font: [IBM Plex Sans dan IBM Plex Mono](https://www.ibm.com/plex/), SIL Open Font License.
- Logo Runova: roket yang meluncur dengan jendela berupa slot rak server, digambar khusus untuk proyek ini. Master dan variannya di `docs/brand/`; ikon app (`runova-app-icon.svg`, PNG 1024 px) menaruhnya di tile squircle gelap gaya macOS.
