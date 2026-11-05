# Penyimpanan file aplikasi dan kuota disk

Brief: `.brief-app-storage.md`. Branch `feat/app-storage`. Spec: `docs/spec.md` §2 (lokasi file), §3 (kuota disk), §4 (deteksi folder), §5 (folder tersimpan, tab File).

## Hasil

- **Folder kerja** = folder data yang sudah ada (`/var/lib/runova/data/<user>/<app>`), di container tetap `/data` + `WorkingDir=/data`. Panel menyebutnya `<app>/` (`starboom/storage`), tidak pernah path host.
- **Folder tersimpan** (`apps.folders`, JSON `[{path, dir}]`): relatif → sudah di folder kerja (dibuat saat deploy, milik UID app); absolut → bind mount dari `mounts/<user>/<app>/<dir>` ke `path` dan ke `/data/<dir>`.
- **Deteksi** (`internal/artifact/folders.go`) dari `.env.example`, `application.properties`, `application.yml/yaml` (kunci YAML bertitik; `yamlPairs` baru). Aturan di spec §4.
- **Kuota disk**: `users.quota_disk_mb` (bawaan 1024), `apps.disk_mb` (bawaan 256 atau sisa), hasil ukur `apps.disk_used_bytes/disk_scanned_at/disk_level`. `Service.WatchDisk` tiap 5 menit, plus sebelum deploy ulang/rollback/versi baru dan setelah unggah/hapus di tab File.
- **Tab File**: `internal/apps/storage.go` (`ListFiles`, `OpenFile`, `SaveFile`, `DeleteFile`), API `internal/api/files.go`.
- Migrasi `0005_app_storage.sql` (bila bentrok nomor saat merge, koordinator menomori ulang; test migrasi mengharapkan versi 5).

## Keputusan

- **Sumber mount di `mounts/`, bukan di dalam `data/`.** Folder `data/<user>/<app>` milik UID app, jadi app yang berjalan bisa mengganti `data/<app>/storage` dengan symlink ke mana saja di host; Docker mengikuti symlink sumber bind mount saat container berikutnya dibuat → app bisa me-mount `/etc` host. `mounts/<user>/<app>` milik runova 0700, hanya subfolder `<dir>` yang milik UID app (app tidak bisa mengganti `<dir>` sendiri karena induknya bukan miliknya). Biaya: folder kerja di host terbagi dua tempat; tab File dan pengukuran menyatukannya.
- **Mounted folder juga di-mount ke `/data/<dir>`** supaya isi folder kerja di dalam container sama dengan tab File (`starboom/storage` ada di `./storage` maupun `/app/storage`). Pengukuran menjalankan WalkDir per folder host, jadi tidak dihitung dua kali (diverifikasi di test integrasi Docker: 6 byte, bukan 9).
- **Folder relatif dibuat lewat `os.Root` tanpa mengikuti link**; komponen yang ternyata link/bukan folder menggagalkan deploy dengan pesan yang menyebut foldernya (bukan diam-diam di-chown di luar workspace). Chown memakai `Root.Lchown`.
- **Aplikasi lama mendapat `disk_mb = 0`**: diukur dan ditampilkan ("belum dibatasi"), tidak pernah "penuh", tidak memakan kuota. Alternatif (isi 256 MB saat migrasi) bisa langsung membuat pengguna lama melebihi kuota dan menolak deploy-nya tanpa aba-aba. Pemilik memilih ukuran di tab Lanjutan.
- **Penegakan lunak**, sesuai brief: tidak ada batas keras tanpa project quota. Deploy ulang/rollback/versi baru mengukur ulang dulu (ruang yang baru dibebaskan langsung dihitung), lalu menolak bila penuh (409 "Disk aplikasi penuh"). App yang berjalan tidak dihentikan. Unggahan lewat tab File dibatasi sisa disk (byte), file sementara dihapus bila melewati.
- **Notifikasi hanya saat level naik** (OK→warn, warn→full); turun tidak memberi notifikasi.
- **`PATCH /api/users/{id}` menggabungkan `quota` ke kuota sekarang**: body lama tanpa `disk_mb` tidak menolkan disk. Perubahan perilaku kecil untuk klien yang mengirim kuota sebagian (sebelumnya field kosong jadi 0).
- **Unduhan selalu `attachment` + `application/octet-stream` + `nosniff` + CSP sandbox**: file HTML unggahan pengguna tidak boleh jalan di origin panel.
- **`/tmp` ditolak** sebagai folder tersimpan: tmpfs sengaja dikosongkan; menyimpannya akan mengejutkan aplikasi JVM yang memakai `/tmp` untuk native lib.
- **Deteksi tidak memakai `.env.example` di dalam JAR**; Runova hanya membaca `.env.example` dari `.tar.gz` (seperti env).
- Ikon baru (`folder`, `folder-open`, `file`, `link`, `folder-input`) ditambahkan ke `web/icons.txt` dan sprite di-generate ulang.

## Server yang sudah berjalan

1. Pasang versi baru seperti biasa (`runova update`). Migrasi `0005` jalan otomatis (cadangan `runova.db.bak-v4-*` dibuat lebih dulu).
2. Semua pengguna mendapat kuota disk 1024 MB; admin ubah di Pengguna → Kuota atau kuota bawaan di Server.
3. Aplikasi lama: folder data tetap di tempatnya, tanpa folder tersimpan, disk "belum dibatasi" sampai pemilik memilih ukuran. Pengukuran pertama berjalan saat Runova start.
4. Aplikasi yang dulu gagal menulis ke path absolut: tambahkan path itu di tab Lanjutan → Folder tersimpan, lalu deploy ulang. Untuk versi baru yang diunggah setelah ini, deteksi berjalan di halaman Tinjau saat membuat aplikasi baru (aplikasi yang sudah ada tidak diubah otomatis; hasil deteksi versi terbaru ditampilkan tidak tercentang di tab Lanjutan).
5. Rollback ke binary lama: `runova rollback` memulihkan `runova.db.bak-v4-*`; folder `mounts/` dibiarkan (binary lama tidak memakainya).

## Pemeriksaan

- Go: `go test ./...`, `go vet ./...`; `RUNOVA_DOCKER_TEST=1 go test ./internal/apps -run TestIntegration` (termasuk `TestIntegrationSavedFoldersSurviveRedeployAndRestart`: tulis ke `./storage` dan `/app/storage` → ada di host, bertahan setelah redeploy dan restart).
- Web: `node web/test/unit.mjs`, `node web/test/smoke-storage.mjs` (baru; `SHOTS=/tmp/runova-storage` untuk screenshot 1550/360 terang/gelap), smoke lain.
- Catatan: `TestIntegrationWrongInternalPortFails` dan `smoke-app.mjs` sekali gagal karena timing (container `dead`/timeout) lalu lulus berulang kali tanpa perubahan; `TestIntegrationWrongInternalPortFails` lulus di `main` dua kali. Tidak terkait fitur ini.
