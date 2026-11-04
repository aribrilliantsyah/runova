# Pembaruan Runova: notifikasi, `runova update`, `runova rollback` — desain

Tanggal: 2026-11-04. Status: keputusan disetujui user di percakapan.

## Tujuan

Admin tahu ada versi baru dari panel, memperbarui server dengan satu perintah pendek, dan bisa kembali ke versi sebelumnya bila pembaruan bermasalah.

## Keputusan

1. **Banner hanya untuk admin**, tampil di semua halaman panel (di bawah topbar) selama versi yang terpasang lebih lama dari rilis terbaru. **Tidak bisa ditutup**; hilang sendiri setelah server diperbarui. Isi: "Runova v0.3.0 tersedia (terpasang: v0.2.0). Perbarui di server: `sudo runova update`" + tombol salin + tautan "Catatan rilis" ke halaman rilis GitHub.
2. **Pengecekan di backend**, bukan dari browser (CSP panel `connect-src 'self'` tetap). `runova serve` mengecek rilis terbaru **tiap 6 jam** (pertama 1 menit setelah start), menyimpan hasil di memori. Gagal (server tanpa internet, GitHub tidak terjangkau) → diam, dicatat di log level debug, banner tidak tampil. Versi `dev` tidak pernah mengecek.
3. **Cara membaca rilis terbaru** sama dengan `get.sh`: ikuti redirect `https://github.com/<repo>/releases/latest` ke `/releases/tag/vX.Y.Z` (tidak memakai `api.github.com`, jadi tidak terkena batas 60 permintaan/jam). Repo bawaan `aribrilliantsyah/runova`, bisa diganti `RUNOVA_REPO`.
4. **Bisa dimatikan** di halaman Server: setelan `update_check` (bawaan `true`).
5. **`sudo runova update` menjalankan alur installer resmi (opsi A)**: menentukan versi target, mengunduh `get.sh` **dari rilis versi itu** beserta `SHA256SUMS`, mencocokkan checksum `get.sh`, lalu menjalankan `bash get.sh --version X [opsi]`. Semua logika pemasangan tetap satu tempat (`get.sh` → `install.sh`), dan perilakunya sama dengan perintah `curl` yang sudah teruji.
6. **`sudo runova rollback`** mengembalikan versi sebelumnya beserta database-nya.

## `runova update`

```
sudo runova update [--version X.Y.Z] [--check] [--yes]
```

- Bukan root → keluar 1: "Jalankan dengan sudo: sudo runova update".
- `--check`: cetak versi terpasang dan terbaru, keluar 0 bila sudah terbaru, 10 bila ada pembaruan, 1 bila gagal mengecek. Tidak butuh root.
- Sudah terbaru (dan tanpa `--version`) → "Runova 0.2.0 sudah versi terbaru", keluar 0.
- `--version` lebih lama dari yang terpasang → tolak dan arahkan ke `runova rollback` (penurunan versi tanpa cadangan database akan gagal karena database lebih baru).
- Menampilkan versi terpasang → target dan tautan catatan rilis, lalu bertanya "Lanjutkan? [y/N]". Tanpa terminal → wajib `--yes`.
- Opsi lain tidak ada; alamat panel tidak berubah saat pembaruan.
- Perbandingan versi: semver `X.Y.Z` dengan pra-rilis `-…` lebih rendah dari rilis yang sama; tag yang tidak bisa diurai → anggap tidak ada pembaruan.

## `runova rollback`

```
sudo runova rollback [--yes]
```

- **Saat memasang/memperbarui**, `install.sh` menyalin binary yang sedang terpasang (bila ada dan berbeda versi) ke `/var/lib/runova/rollback/runova` (0755, milik root) dan mencatat versinya di `/var/lib/runova/rollback/VERSION`. Hanya satu versi sebelumnya yang disimpan.
- Saat dibuka binary baru yang memigrasi database, Runova sudah membuat `runova.db.bak-v<skema lama>-<unix>` (fitur yang ada).
- `rollback`:
  1. Tidak ada `rollback/runova` → keluar 1 "Tidak ada versi sebelumnya untuk dikembalikan".
  2. Baca skema tertinggi binary lama (`rollback/runova db version`, baris "Skema tertinggi binary ini") dan skema database sekarang.
  3. Bila skema database > skema binary lama: cari cadangan terbaru `runova.db.bak-v<skema binary lama>-*`; tidak ada → keluar 1 dengan penjelasan (tidak menyentuh apa pun).
  4. Tampilkan ringkasan: versi sekarang → versi lama, cadangan database yang dipakai beserta waktunya, dan **peringatan bahwa perubahan data panel sejak pembaruan (pengguna, aplikasi, domain, setelan) hilang**; container aplikasi dan database PostgreSQL pengguna tidak disentuh. Tanya "Lanjutkan? [y/N]" (atau `--yes`).
  5. `systemctl stop runova` → salin `runova.db` sekarang ke `runova.db.before-rollback-<unix>` → pulihkan cadangan (bila perlu; hapus `-wal`/`-shm` lama) dengan pemilik `runova:runova` 0600 → tukar binary (`/usr/local/bin/runova` ↔ `rollback/runova`, sehingga `runova update` atau `rollback` berikutnya bisa maju lagi) → `systemctl start runova` → `runova check`.
- Unit systemd dan konfigurasi lain tidak diubah (installer setiap versi idempoten dan kompatibel).

## API dan panel

- `GET /api/update` (admin): `{"current":"0.2.0","latest":"0.3.0","update_available":true,"release_url":"https://github.com/…/releases/tag/v0.3.0","checked_at":…,"enabled":true}`. Pengguna biasa → 403.
- Shell panel memanggilnya sekali saat dimuat dan tiap 30 menit untuk admin; banner di atas konten di semua halaman, desktop dan HP, tidak menyebabkan scroll horizontal di 360 px.
- Halaman Server: baris "Versi Runova" (terpasang, terbaru, waktu cek terakhir) dan sakelar "Periksa pembaruan".
- `RUNOVA_FAKE_BACKENDS=1`: pengecek palsu yang melaporkan versi lebih baru agar banner terlihat di uji dan screenshot.

## Pengujian

- **Go:** perbandingan versi (tabel), pengecek dengan server HTTP lokal yang meniru redirect GitHub (terbaru, sama, lebih lama, gagal jaringan, tag rusak, `dev`), jadwal pakai jam palsu, `update_check=false` tidak mengecek, `GET /api/update` admin vs pengguna.
- **`runova update`:** server rilis tiruan (pola `test/get.bats`): `--check` kode keluar 0/10/1, sudah terbaru, `--version` lebih lama ditolak, checksum `get.sh` salah ditolak sebelum dijalankan, bukan root ditolak, tanpa terminal tanpa `--yes` ditolak, opsi diteruskan ke `get.sh`.
- **`install.sh` (bats):** menyimpan binary lama ke `rollback/` hanya bila versinya berbeda; instalasi pertama tidak membuat `rollback/`.
- **`runova rollback`:** dengan stub `systemctl`: tanpa versi sebelumnya; skema sama (tanpa pemulihan DB); skema berbeda dengan cadangan (DB dipulihkan, salinan `before-rollback` dibuat, binary ditukar); skema berbeda tanpa cadangan (tidak ada yang diubah).
- **Panel (Playwright):** admin melihat banner dan bisa menyalin perintah; pengguna biasa tidak; layout 360–1440 px terang/gelap.
- **Sistem (`test/system/run.sh`):** skenario baru — pasang rilis lokal versi A, perbarui ke versi B dengan `runova update` terhadap server rilis lokal (`RUNOVA_GITHUB`), lalu `runova rollback` → `runova version` kembali ke A, panel login, data utuh.

## Dokumentasi

README dan `docs/install-ubuntu.md` bagian Pembaruan: `sudo runova update` jadi cara utama (perintah `curl` tetap untuk instalasi pertama dan untuk server versi 0.2.0 ke bawah yang belum punya perintah ini), bagian baru "Kembali ke versi sebelumnya".

## Di luar lingkup

Pembaruan dari panel (butuh root), pembaruan otomatis terjadwal, menyimpan lebih dari satu versi sebelumnya, rollback database PostgreSQL pengguna.
