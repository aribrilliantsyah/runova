# Bagian 6: deviasi dan keputusan

Nama, signature, dan konstanta di blok Interfaces rencana tidak diubah (`web.FS`, `api.Config.Static`, `dom.js`, `router.js`, `api.js`, `session.js`). Hal-hal di bawah adalah tambahan atau koreksi.

## Penyajian dan keamanan

1. **Satu hash untuk semua berkas.** Rencana menyebut `app.<hash>.js` per berkas. Berkas JS/CSS saling merujuk lewat path literal (`/assets/js/dom.js`), jadi hash per berkas memaksa urutan penulisan ulang yang bergantung pada graf impor (dan gagal pada siklus). Sekarang satu hash 8 heks dihitung dari seluruh isi `web/` saat start dan dipakai semua URL; di JS/CSS/`index.html` path literal `/assets/...` ditulis ulang ke URL ber-hash. Berkas berubah → semua URL berubah, cache lama tidak pernah dipakai bersama berkas baru. Rujukan ke berkas yang tidak ada membuat `api.New` panic saat start (cacat build, bukan kondisi runtime); `TestStaticRejectsDanglingAssetReference`.
2. **Penulisan ulang hanya untuk path `/assets/...` literal.** Kode JS tidak boleh menyusun path aset dari potongan string; `kinds.js` menulis path logo lengkap karena itu.
3. **Rute `/` tanpa metode.** `GET /` bentrok dengan `/api/` di `ServeMux`; `/` dipakai dan metode selain GET/HEAD dijawab 405 JSON.
4. **xterm.js ditambal.** `xterm.mjs` menyisipkan tiga elemen `<style>`, yang ditolak CSP `style-src 'self'`. Tiga pemanggilan `createElement("style")` diganti `__runovaSheet(doc)` (di awal berkas) yang menulis `textContent` ke `CSSStyleSheet` + `adoptedStyleSheets` (CSSOM, diizinkan). Peta sumber dibuang. Versi 6.0.0, lisensi di `web/vendor/licenses/`. Harus diulang bila xterm diperbarui. Sudah diperiksa nyata: terminal tampil dan menerima ketikan di bawah CSP (`smoke-app.mjs`).
5. **`web/vendor/LICENSES.md` diganti folder `web/vendor/licenses/`** berisi teks lisensi asli (xterm, Lucide, Devicon, IBM Plex) dan ikon dari satu sprite `web/vendor/icons.svg`. `cmd/genicons` memeriksa integritas sha512 paket `lucide-static@1.65.0` sebelum memakainya.
6. **Hanya tiga logo Devicon** (Java, PostgreSQL, Linux/Tux untuk binary statis dan glibc). Rencana berbicara tentang "logo bahasa dari jenis/`report`", tetapi `report` tidak memuat bahasa; Go, Rust, dll tidak dapat dibedakan dari ELF-nya. Satu logo per `kind`.
7. **Font hanya subset Latin** (Plex Sans 400/500/600, Plex Mono 400/500, woff2).

## API yang ditambahkan

8. **`GET /api/server`** (admin) seperti rencana, ditambah bentuk `{docker, caddy, postgres, host, apps}`. Probe dikirim lewat `api.Config.Health` (`Check` per layanan); layanan mati tetap 200 dengan `ok:false` dan alasannya. `docker.Client.Version` dan `pg.PgxAdmin.Version` baru. Caddy "ada" = `GET /config/` berhasil.
9. **`GET /api/info`** (semua pengguna): batas unggah, IP publik, runtime yang diizinkan, domain panel. `GET /api/settings` hanya admin, padahal wizard butuh batas unggah dan runtime. Tidak membocorkan kuota bawaan atau jaringan PostgreSQL (diuji).
10. **`GET /api/usage`** (admin): pemakaian aplikasi/memori/CPU/database per pengguna untuk tabel Pengguna. `internal/api/users.go` sengaja tidak disentuh (sesi paralel).
11. **`GET /api/apps` mendapat `summary`** (peta ID aplikasi → `{domains, version, deployed_at}`) supaya kartu daftar menampilkan domain dan waktu deploy tanpa N+1 permintaan. Hanya aplikasi dalam daftar yang masuk (`TestListAppsSummary`).
12. **`RUNOVA_FAKE_BACKENDS=1`** (`config.Config.FakeBackends`, `cmd/runova/devbackends.go`): engine Docker `dockertest.Fake`, `pgfake`, Caddy loader dalam memori, `pg_dump`/`pg_restore` berupa skrip pengganti, health probe sukses, domain berawalan `pending.` tidak pernah mendapat sertifikat. Ditolak (exit 1, sebelum kunci dibaca) bila `RUNOVA_DATA` = `/var/lib/runova`.

## Perilaku panel

13. **Variabel "wajib" di wizard.** `report.env` tidak menyatakan apakah `${NAMA}` di `application.properties` punya nilai bawaan (`default` kosong untuk semuanya). Wajib = entri `.env.example` tanpa bawaan, atau variabel rahasia yang dirujuk (nama berisi PASSWORD/SECRET/TOKEN/KEY). Variabel properties lain tidak wajib; hasil scan binary ("kemungkinan") tidak pernah wajib. Variabel yang diisi otomatis oleh pilihan database tidak wajib.
14. **Domain di wizard ditambahkan setelah aplikasi dibuat** (`POST /api/apps/{id}/domains`); bila ditolak, aplikasi tetap ada dan toast menjelaskan.
15. **Database baru dari wizard dibuat saat Deploy ditekan**, bukan saat dipilih. Bila pembuatan aplikasi gagal sesudahnya, database itu dipakai ulang pada percobaan berikutnya (tidak dibuat dua kali), tetapi tetap ada bila pengguna meninggalkan wizard.
16. **Banner "Deploy ulang untuk menerapkan"** diingat di `sessionStorage` per aplikasi dan per deployment aktif; hilang begitu deployment lain aktif. Server hanya mengembalikan `redeploy_required` pada respons PUT/PATCH, tidak menyimpannya.
17. **Rahasia dan "Lihat"**: nilai rahasia tidak pernah ada di halaman sampai "Lihat" (satu panggilan `reveal` per variabel, tercatat server). "Tampilkan nilai" memanggil `reveal` untuk tiap rahasia.
18. **Log hanya tersambung bila ada deployment aktif.** Tanpa container server menjawab 409, jadi tab Log dan kartu Log terbaru menunggu dan tidak membuka WebSocket; deployment gagal menampilkan `failure_log` terakhirnya.
19. **Paginasi aktivitas memakai `before=<at entri terakhir>`** (kontrak store: `at < before`). Entri yang berbagi milidetik yang sama persis di batas halaman bisa terlewat; tidak ada kursor ID di API.
20. **`Ctrl K` membuka pencarian aplikasi** (dialog dengan daftar), bukan palet perintah.
21. **Tab detail memakai segmen URL** `#/apps/<id>[/env|domains|deployments|logs|monitor|advanced]`; berpindah tab tidak memuat ulang halaman aplikasi (`router.update`).

## Uji

- `web/test/unit.mjs` menjalankan `dom.test.html` (16) dan `api.test.html` (7) di Chromium. Playwright dicari di `~/.npm/_npx` atau `PLAYWRIGHT_DIR`; Chromium penuh dari `~/.cache/ms-playwright` (headless shell tidak terpasang di mesin ini).
- Temuan uji asap yang diperbaiki: `Element.append(null)` menulis teks "null" (diganti `fill()`); aturan `.tab{display:none}` dari preview menyembunyikan isi tab; unggahan yang sudah dipakai dihapus lagi (404) saat meninggalkan halaman; deploy fake gagal "role belum dibuat" untuk admin hasil installer sampai `ReconcileRoles` dijalankan di jalur fake.
- Pemeriksaan tombol-ikon tanpa nama dan fokus terlihat di `smoke-layout.mjs` membaca DOM, bukan pembaca layar sungguhan.

## Review akhir

Self-review (satu reviewer segar, tanpa menjalankan kode). Temuan Important diperbaiki dalam satu putaran:

- **Penjaga `RUNOVA_FAKE_BACKENDS` membandingkan direktori sebenarnya** (`os.SameFile`), bukan hanya ejaan path: `RUNOVA_DATA=.` dari `/var/lib/runova` atau symlink ke sana kini ditolak (`TestFakeBackendsRefusedForAliasesOfProductionData`, gagal dulu, lulus sesudahnya).
- **Terminal, dialog, dan soket log tidak melewati halaman/sesi**: meninggalkan halaman aplikasi menutup laci terminal; logout/sesi berakhir menutup semua dialog dan terminal. Mulai-ulang log di tab Umum diberi nomor generasi sehingga mulai yang selesai sesudah cleanup dibuang. Buffer log tab Umum dibatasi 5 baris. `web/test/smoke-lifecycle.mjs` (gagal dulu pada laci terminal, lulus sesudahnya).
- **Aset statis hanya melayani GET/HEAD** (`TestStaticOnlyServesGetAndHead`); nama file panjang di unggahan dibungkus (tidak ada scroll horizontal di 360 px).
