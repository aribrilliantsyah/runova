# Migrasi database SQLite

Menggantikan skema "langsung ubah `schema`" (spec §11). Kode: `internal/store/migrate.go`, `internal/store/migrations/`.

## Desain

- Migrasi berurutan `internal/store/migrations/NNNN_nama.sql` (di-`embed`). Nomor harus 1..N tanpa celah atau duplikat; nama `^\d{4}_[a-z0-9_]+\.sql$`. Pelanggaran membuat `Open` gagal.
- Versi skema = `PRAGMA user_version`. `Open` memanggil `migrate(ctx, db, fsys)`; `fsys` bisa disuntik di test.
- `migrate` mengambil satu `*sql.Conn` khusus. `PRAGMA foreign_keys = OFF` dipasang di koneksi itu selama migrasi (pragma ini tidak berlaku di dalam transaksi) dan dinyalakan lagi di akhir; bila gagal dinyalakan, koneksi dibuang dari pool.
- Tiap migrasi = satu transaksi `BEGIN IMMEDIATE` (`_txlock=immediate`) berisi isi file + `PRAGMA foreign_key_check` + `PRAGMA user_version = N`. Gagal → rollback, error memuat nama file (`migration 0002_x.sql: ...`). Versi dibaca ulang di dalam transaksi, jadi dua `Open` bersamaan (service dan `runova admin create`) tidak menjalankan migrasi dua kali: yang kedua menunggu kunci tulis, lalu melihat versi sudah naik.
- DB dengan `user_version` lebih besar dari migrasi tertinggi ditolak sebelum menyentuh apa pun: `database dibuat oleh versi Runova yang lebih baru (skema N, binary ini M)`. Tidak ada cadangan, tidak ada perubahan.
- Cadangan: sebelum migrasi tertunda pertama pada DB yang sudah berisi (versi ≥ 1, atau DB pra-migrasi yang baru diadopsi), `VACUUM INTO` ke `runova.db.bak-v<versi lama>-<unix>` (0600), path dicatat di log. Hanya 3 cadangan terbaru (urut waktu unix di nama) yang disimpan; file lain berawalan `runova.db.bak-` yang tidak cocok pola dibiarkan. DB kosong baru tidak dicadangkan.
- `runova db version` → `store.SchemaVersion(path)`: membuka DB read-only, tidak pernah membuat atau mengubah file; DB belum ada dilaporkan sebagai versi 0.

## DB pra-migrasi (satu-satunya kode kompatibilitas)

DB dengan `user_version = 0` tetapi tabel `users` sudah ada (build pengembangan/e2e sebelum fitur ini) diadopsi: `adoptLegacy` membuat tabel `databases` + indeks bila belum ada, menambah `users.pg_password` dan `apps.database_id` bila belum ada, lalu `user_version = 1` tanpa menjalankan `0001`. Adopsi bukan migrasi: tidak ada cadangan. Bila setelahnya masih ada migrasi tertunda (`0002`+), cadangan v1 dibuat sebelum itu. Fixture skema lama ada di `internal/store/testdata/legacy_schema.sql` (salinan konstanta `schema` sebelum perubahan ini).

`0001_init.sql` memakai `CREATE TABLE` biasa tanpa `IF NOT EXISTS` (konsisten; DB berisi tidak pernah menjalankannya). Urutan kolom `users` dan `apps` berbeda dari DB hasil `ALTER TABLE` (kolom baru di akhir untuk `apps.database_id`), tidak berpengaruh karena semua query menyebut nama kolom.

## Menambah migrasi

1. Buat `internal/store/migrations/000N_<nama>.sql` dengan N = versi tertinggi + 1. Jangan ubah file yang sudah dirilis.
2. Isi: pernyataan SQL biasa. Tanpa `BEGIN`/`COMMIT`/`PRAGMA`; runner yang mengurus transaksi, `foreign_keys`, `foreign_key_check`, dan `user_version`.
3. Menambah kolom/tabel/indeks cukup `ALTER TABLE ... ADD COLUMN` / `CREATE ...`.
4. Mengubah atau menghapus kolom, atau perubahan lain yang tak bisa dengan `ALTER TABLE`: bangun ulang tabel (pola 12 langkah SQLite):
   ```sql
   CREATE TABLE x_new(...);
   INSERT INTO x_new(a, b) SELECT a, b FROM x;
   DROP TABLE x;
   ALTER TABLE x_new RENAME TO x;
   CREATE INDEX x_a ON x(a);   -- indeks, trigger, view ikut dibuat ulang
   ```
   Jangan mengganti nama `x` lebih dulu (itu menulis ulang `REFERENCES` tabel anak). Karena `foreign_keys` OFF, `DROP TABLE x` tidak men-cascade ke tabel anak; `foreign_key_check` sebelum commit menggagalkan migrasi yang meninggalkan baris yatim.
5. Tambah test di `internal/store/migrate_test.go` bila migrasinya mengubah data. Test runner memakai `fstest.MapFS`, tidak bergantung pada isi `migrations/`.

## Catatan keputusan

- Ruling: signature `migrate(ctx, db *sql.DB, fsys fs.FS)`, bukan `migrate(conn, fsys)` dari brief. `migrate` mengambil `*sql.Conn` sendiri supaya pemanggil tidak bisa lupa menonaktifkan `foreign_keys` pada koneksi yang tepat dan supaya cadangan (`VACUUM INTO`, tidak bisa di dalam transaksi) bisa lewat koneksi lain dari pool sementara transaksi migrasi memegang kunci tulis. Biaya bila salah: test memanggil `migrate` dengan `*sql.DB`, bukan `*sql.Conn`.
- Ruling: cadangan dibuat di dalam transaksi migrasi pertama yang tertunda (kunci tulis sudah dipegang), sehingga salinan konsisten dengan versi lama dan tidak ada penulis lain yang menyela di antara cadangan dan migrasi. `VACUUM INTO` dari koneksi kedua boleh karena mode WAL (pembaca tidak diblokir penulis).
- Ruling: file `*.sql` dengan nama salah di folder migrasi menggagalkan `Open` (bukan diabaikan), supaya typo nama tidak diam-diam melewatkan migrasi. File non-`.sql` diabaikan.
- Ruling: `TestOpenMigratesOldDatabase` (hanya membuka dua kali) dihapus; perilaku buka-ulang-tanpa-efek kini diuji di `TestMigrateAppliesInOrderAndSetsVersion`.
