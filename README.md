<p align="center">
  <img src="docs/brand/runova-symbol.svg" width="96" height="96" alt="">
</p>

<h1 align="center">Runova</h1>

<p align="center">
  Panel self-hosted untuk menjalankan hasil build, bukan source code.<br>
  Unggah <code>.jar</code> atau binary Linux, isi environment, dan aplikasinya jalan di server sendiri dengan domain dan HTTPS otomatis.
</p>

<p align="center">
  <b>Status: tahap rancangan.</b> Belum ada kode yang bisa dijalankan. Yang ada baru keputusan desain dan
  <a href="preview-dashboard.html">preview tampilan</a> (buka file itu di browser).
</p>

<p align="center">
  <img src="docs/screenshots/desktop-apps.png" alt="Daftar aplikasi di Runova: kuota akun, kartu aplikasi dengan logo bahasa, status, domain, dan akses IP" width="860">
</p>

## Kenapa

Platform seperti Vercel atau Railway membangun aplikasi dari source code dan berjalan di cloud mereka. Banyak tim internal justru sudah punya artefak jadi dari pipeline sendiri (file `.jar` dari Maven, binary Go atau Rust dari CI) dan cukup butuh satu tempat di server kantor untuk menjalankannya dengan rapi:

- **Unggah, bukan build.** Runova tidak menyentuh source code. Ia membaca file yang diunggah: jenisnya, versi Java minimal, port, dan environment yang dirujuk.
- **Terpisah per pengguna.** Setiap aplikasi berjalan di container Docker sendiri dengan batas memori dan CPU. Pengguna tidak bisa melihat atau menyentuh aplikasi milik pengguna lain.
- **Uji dulu lewat IP, lalu pasang domain.** Aplikasi bisa dibuka di `IP-server:port` untuk pengujian. Setelah admin jaringan mengarahkan DNS, Caddy menerbitkan sertifikat HTTPS sendiri dan akses IP bisa ditutup.

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

## Menghubungkan domain

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
    <td><img src="docs/screenshots/mobile-apps.png" alt="Daftar aplikasi di HP dengan navigasi bawah: Aplikasi, Aktivitas, Deploy, Pengguna, Lainnya"></td>
  </tr>
</table>

- Mode terang dan gelap, padat seperti Dokploy dan Railway, tanpa gradien.
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

## Kredit

- Ikon antarmuka: [Lucide](https://lucide.dev), lisensi ISC.
- Logo bahasa pemrograman: [Devicon](https://devicon.dev), lisensi MIT. Merek dagang milik pemiliknya masing-masing.
- Font: [IBM Plex Sans dan IBM Plex Mono](https://www.ibm.com/plex/), SIL Open Font License.
- Logo Runova: roket yang meluncur dengan jendela berupa slot rak server, digambar khusus untuk proyek ini. Master dan variannya di `docs/brand/`.
