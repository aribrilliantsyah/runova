# File manager di tab File dan kartu Folder tersimpan

Brief: `.brief-file-manager.md`. Branch `feat/file-manager` (dari v0.3.9). Spec: `docs/spec.md` §5 (Tab File).

## Hasil

- **Kartu Folder tersimpan** (`web/js/components/folders.js`, dipakai di Lanjutan dan halaman Tinjau): keadaan kosong dengan ikon, kalimat, dan contoh path; tiap baris "path di aplikasi → folder di workspace" dengan chip asal (`terdeteksi`/`manual`, tooltip menyebut file dan kuncinya) dan tombol hapus. Komponen mengembalikan `addButton` dan pemanggil yang menaruhnya: header kartu di Lanjutan, di bawah daftar di Tinjau. "Simpan folder" di footer kanan, aktif hanya bila ada perubahan dan valid, dengan penanda "Belum disimpan".
- **Kartu Lanjutan** memakai satu helper `card(title, desc, body, {aside, foot})`: judul dan deskripsi bertumpuk di kiri, aksi sekunder di kanan header, tombol simpan di kanan `footer.cfoot` (pola yang sama dengan kartu di halaman Server).
- **Tab File** (`web/js/pages/app/files.js`, helper murni di `fileutil.js` dengan unit test `web/test/files.test.html`): breadcrumb, `?path=` di URL, naik satu folder, cari, daftar/kisi, urut per kolom (folder di atas), pilih banyak + aksi massal, keyboard, menu baris + klik kanan, pratinjau di panel samping, unggah banyak/drag-and-drop dengan progres per file, folder baru, ganti nama, pindahkan (pemilih folder), salin path, footer jumlah item + ukuran + bar disk. Ponsel: baris ringkas, menu jadi bottom sheet.
- **API** (`internal/api/files.go`, logika `internal/apps/storage.go` dan `filemanager.go`): `POST …/files/folder`, `POST …/files/move`, `GET …/files/zip` (+ `check=1`), `GET …/files/info`, `GET …/files/preview`, unggah banyak file dalam satu multipart, `DELETE …/files {paths}`. Aturan lengkap di spec §5.

## Keputusan

- **Rename/pindah memakai `renameat2(RENAME_NOREPLACE)`** pada dua folder induk yang dibuka lewat `os.Root` (`rename_linux.go`). `os.Root.Rename` menimpa tujuan dan tidak bisa menyeberang antara folder data dan sumber mount (dua root berbeda). Dengan file descriptor induk dan nama tanpa `/`, tidak ada link yang diikuti, dan "tujuan sudah ada" ditolak secara atomik, bukan cek-lalu-rename yang bisa balapan dengan aplikasi yang berjalan. Pindah antara folder data dan folder ter-mount tetap satu filesystem di server biasa (keduanya di bawah `/var/lib/runova`); bila beda disk (`EXDEV`) pesan menyebutnya, tidak menyalin diam-diam. `rename_other.go` hanya fallback pengembangan non-Linux.
- **Folder yang berisi folder tersimpan** (mis. `public` untuk `public/uploads`) diperlakukan seperti folder tersimpan: tidak bisa dihapus, diganti nama, atau dipindah. Menghapusnya akan menghapus folder tersimpan; memindahkannya membuat deploy berikutnya membuat ulang folder kosong di tempat lama.
- **Hapus massal memeriksa semua path dulu**, lalu menghapus; satu path terlarang membatalkan semuanya, supaya pilihan yang tidak sengaja memuat folder tersimpan tidak menghapus setengahnya.
- **`DELETE …/files` ganti body ke `{paths}`** (clean cutover; satu-satunya klien adalah panel). Endpoint lama `{path}` dihapus, bukan dipertahankan.
- **ZIP**: dari satu folder saja (path induk sama) atau seluruh workspace (`path=` kosong), supaya nama di dalam arsip jelas. Ukuran dijumlah dulu; batas `min(4 × batas unggahan, 2 GB)`, sesuai brief. Saat menulis, total dibatasi lagi dan tiap file disalin sebesar ukurannya saat dibaca, jadi file yang tumbuh selama unduhan tidak membuat arsip tanpa batas. Link dilewati (tidak diikuti). Panel memanggil `check=1` dulu supaya alasan penolakan tampil sebagai toast, bukan sebagai "file" JSON yang terunduh.
- **Pratinjau**: tipe dari `http.DetectContentType` (512 byte pertama), bukan ekstensi; teks yang memuat `<svg` dianggap `image/svg+xml`. Hanya PNG/JPEG/GIF/WebP dirender sebagai gambar; teks (termasuk HTML yang menyamar `.png`) dikirim sebagai `text/plain` dan ditampilkan lewat `textContent` di `<pre>`. Respons pratinjau selalu `nosniff` + `default-src 'none'; sandbox`. CSP panel (`img-src 'self' data:`) tidak diubah karena gambar datang dari origin yang sama.
- **Thumbnail kisi** memakai endpoint pratinjau yang sama (`loading=lazy`); berkas yang bukan gambar whitelist jatuh ke ikon lewat `onerror`.
- **URL**: folder pindah dari sub-path (`files/<path>`) ke query (`files?path=…`) seperti diminta brief; tautan lama `files/<path>` masih dibuka lalu ditulis ulang ke bentuk baru dengan `replaceState`.
- **Folder di-drop**: tidak diunggah rekursif (API unggah per folder tujuan); dilewati dengan pesan agar pengguna membuat foldernya dulu. Menjaga endpoint unggah tetap sederhana.
- **Ukuran folder** di kolom Ukuran adalah jumlah item (satu tingkat), bukan ukuran total: menghitung total butuh walk rekursif per baris pada setiap daftar, tidak murah. Total disk tetap di bar footer.
- **Pilihan tampilan dan urutan** disimpan di `localStorage` (bukan URL), karena itu preferensi, bukan lokasi yang dibagikan.
- Ikon baru lewat `web/icons.txt` + `go run ./cmd/genicons`: `file-text`, `file-image`, `file-code`, `file-audio`, `file-video`, `file-spreadsheet`, `folder-plus`, `folder-output`, `image`, `layout-grid`, `list`, `arrow-up`, `corner-left-up`, `text-cursor-input`.

## Pemeriksaan

- Go: `go test ./...`, `go vet ./...`, `gofmt -l` kosong. Test baru: `TestFilesMakeDir`, `TestFilesMove`, `TestFilesDeleteMany`, `TestFilesZip`, `TestFileInfoDetectsTypeAndPreview`, `TestListFilesCountsFolderItems` (apps), `TestFileManagerAPI`, `TestZipDownloadLimit` (api): tabel traversal/link/isolasi tiap endpoint, isi zip dan link tidak diikuti, batas ukuran, rename ke nama yang ada ditolak, whitelist MIME pratinjau.
- Docker: `RUNOVA_DOCKER_TEST=1 go test -p 1 ./internal/apps -run TestIntegrationSavedFolders` lulus (pola `-run Storage` di brief tidak cocok dengan nama test mana pun).
- Web: `node web/test/unit.mjs`, `smoke-storage` (diperluas: unggah banyak dan drag-and-drop, folder baru, ganti nama, pindah, pratinjau gambar dan teks, unduh zip, hapus massal, shift-klik, breadcrumb, keyboard, reload di subfolder, sheet ponsel, kartu Folder tersimpan kosong/berisi di Lanjutan dan Tinjau), `smoke-layout`, `smoke-app`, `smoke-deploy`, `smoke-admin`.
- Screenshot: `SHOTS=/tmp/runova-files node web/test/smoke-storage.mjs` (1550/768/360, terang dan gelap).
