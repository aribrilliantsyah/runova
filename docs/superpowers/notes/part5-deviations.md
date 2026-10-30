# Bagian 5: deviasi dan keputusan

Nama, signature, dan konstanta di blok Interfaces rencana tidak diubah. Hal-hal di bawah adalah tambahan atau koreksi kecil.

## Task 1

1. **`EnsureRole` pada role yang sudah ada hanya mengubah password dan batas koneksi.** Rencana menyuruh "set password & limit"; `ALTER ROLE … NOSUPERUSER NOREPLICATION NOBYPASSRLS` ditolak PostgreSQL untuk pemegang `CREATEROLE` non-superuser (`permission denied to alter role`), meski hanya untuk mematikan atribut itu. Atribut tersebut sudah ditetapkan saat `CREATE ROLE` dan tidak bisa diubah pengguna, jadi tidak perlu diulang.
2. **Grant dengan `CURRENT_USER`.** `GRANT "<role>" TO CURRENT_USER WITH INHERIT TRUE, SET TRUE` menggantikan nama `runova` yang ditulis keras, supaya DSN dengan `RUNOVA_PG_DSN` yang memakai role lain tetap konsisten. Dengan DSN bawaan hasilnya sama.
3. **Validasi sebelum SQL.** Semua metode `PgxAdmin` memvalidasi argumen sebelum menyentuh koneksi dan mengembalikan error yang membungkus `pg.ErrInvalid` (tambahan, tidak ada di rencana): role harus `^[a-z][a-z0-9]{1,30}$` dan bukan nama dicadangkan (`postgres`, `runova`, `root`, `public`, `template`); nama database harus `<username>_<nama>` dan `CreateDatabase` menolak bila awalan bukan `owner`; password tidak boleh kosong atau mengandung NUL; batas koneksi tidak boleh negatif. Ini mencegah `DropRole("runova")`, `DropDatabase("postgres")`, dsb.
4. **Literal password memakai `E'…'`** dengan `\` dan `'` digandakan, supaya benar baik `standard_conforming_strings` nyala maupun mati.
5. **`DropRole` dan `DropDatabase` idempoten**: role/database yang sudah tidak ada bukan error. `DropRole` juga memutus sesi role itu di database bersama (mis. `postgres`) sebelum `DROP ROLE`.
6. **`CreateDatabase` mengembalikan bersih bila `REVOKE` gagal**: database yang baru dibuat di-drop lagi agar tidak ada database yang terbuka untuk `PUBLIC`.
7. **`PgxAdmin.Close()`** ditambahkan (tidak ada di interface `Admin`); pemanggil yang memegang `*PgxAdmin` wajib menutup pool.
8. **DDL bersamaan pada role yang sama tidak aman.** PostgreSQL menjawab `tuple concurrently updated` / `duplicate key` bila dua `EnsureRole`/`ALTER ROLE` untuk role yang sama berjalan bersamaan. `PgxAdmin` tidak menyerialkan; `internal/dbs` (Task 2) harus memanggil operasi role satu per satu per pengguna. Pengguna berbeda aman paralel (diuji di `TestConcurrentEnsureRoleForDifferentUsers`).
9. **Helper `startPG`** juga memberi `GRANT EXECUTE ON FUNCTION pg_reload_conf() TO runova`, meniru langkah installer, sehingga `ReloadConfig` bisa diuji. Container memakai port host acak (`-p 127.0.0.1::5432`), `--rm`, dan dihapus di `t.Cleanup`.

## Task 2

10. **Helper test dipindah ke `internal/pg/pgtest`** (`pgtest.StartPG`, diekspor) dan `testpg_test.go` dihapus, supaya `internal/dbs` memakai helper yang sama. Tambahan: `internal/pg/pgfake` (`pg.Admin` palsu bersama untuk test `dbs` dan `api`), `pg.ClientEnv(dsn)` (env libpq untuk `pg_dump`/`pg_restore`), `pg.Port(dsn)`.
11. **Migrasi skema.** `CREATE TABLE IF NOT EXISTS` tidak menambah kolom ke tabel lama, jadi `users.pg_password` dan `apps.database_id` ditambahkan lewat `ALTER TABLE` bila belum ada (`migrateDatabases` di `store/databases.go`, dipanggil dari `store.Open`). Perubahan di `store.go` hanya satu blok pemanggilan.
12. **`DeleteUser` menghapus baris `apps` pengguna dulu.** `apps.database_id ... ON DELETE RESTRICT` membuat `DELETE FROM users` gagal bila aplikasi terikat ke database pengguna itu (cascade ke `databases` dan `apps` terjadi dalam urutan yang tidak ditentukan). Hasil akhirnya sama dengan sebelumnya (aplikasi ikut hilang).
13. **`Settings.PGAllowedNets`** bawaan `[]` (bukan `null`), validasi `netip.ParsePrefix`, maks 20 (`store.MaxPGAllowedNets`).
14. **Tipe error `dbs`**: `ErrNotFound`, `ErrQuota`, `ErrInUse`, `ErrValidation`, `ErrPGDown`, `ErrPG` (PostgreSQL menolak perintah saat terjangkau), ditambah `ErrBackupNotFound` dan `*ToolError` di Task 4. Setelah `pg.Admin` gagal, `Service` memanggil `Ping`: gagal → `ErrPGDown`, `pg.ErrInvalid` → `ErrValidation`, selain itu `ErrPG`.
15. **`Create` mencoba `EnsureRole` sekali** bila `CreateDatabase` gagal (role belum ada karena PostgreSQL mati saat pengguna dibuat), lalu mengulang `CreateDatabase`.
16. **Service mendapat field `PGEnv []string` dan `PGPort int`** (bukan di rencana): env libpq untuk `pg_dump`/`pg_restore` dari DSN admin, dan port yang dipakai `AppEnv`/`Connection` (bawaan 5432; `host.docker.internal` di container tidak berubah).
17. `Connection.Host` = `Settings.PublicIP`; kosong bila admin belum mengisinya (panel domain baru tersedia setelah Bagian 4, jadi tidak ada fallback ke domain).
18. **Operasi role per pengguna diserialkan** dengan mutex per username (`OnUserCreated`, `OnQuotaChanged`, `OnUserDeleted`, `RotatePassword`, `CreateDatabase`/`DropDatabase`), sesuai catatan 8 (`TestRoleOperationsSerializedPerUser`).
19. **`ListAll` dan `NotifyAdmins`** ditambahkan di `dbs` untuk `GET /api/admin/databases` dan notifikasi kegagalan hook.

## Task 3

20. **`SettingsInput.DatabaseID`**: `0` melepas database; angka lain harus milik pemilik aplikasi (400 "Database tidak ditemukan"). Perubahan ikatan menandai `redeploy_required`; no-op tidak.
21. **`Service.DBEnv`** nil berarti tidak ada env database. `rollout` memanggilnya sebelum `ContainerSpec`; kegagalan env (mis. password belum tersimpan) menggagalkan deploy dengan pesan jelas, PostgreSQL mati tidak berpengaruh karena env berasal dari password tersimpan.
22. **Hook pengguna** (`userRoleHook` di `api/databases.go`): kegagalan PostgreSQL tidak menggagalkan request pengguna; dicatat dan dikirim sebagai notifikasi `error` ke semua admin ("Role PostgreSQL untuk <u> belum dibuat", dst.). Hapus pengguna dihitung sama: baris SQLite sudah hilang, role yatim diberi tahu lewat notifikasi.
23. **Kode HTTP**: PostgreSQL mati 503 "PostgreSQL tidak bisa diakses"; perintah ditolak PostgreSQL 502 (`TestRotatePasswordOrder`); kuota/dipakai/nama ganda 409; nama tidak valid/konfirmasi salah 400.
24. Hanya pemilik yang memakai endpoint database (admin tidak mendapat jalan masuk ke database pengguna lain, kecuali daftar `GET /api/admin/databases`).
25. **Konfigurasi**: `config.Config.PGDSN` (`RUNOVA_PG_DSN`), `PGHBAPath` (`RUNOVA_PG_HBA`), `BackupDir()` = `$RUNOVA_DATA/backup`. `cmd/runova/postgres.go` (file baru) membuka admin; PostgreSQL tak terjangkau saat start hanya mencatat peringatan. Halaman Server (Bagian 6) belum ada, jadi peringatan itu baru ada di log.
26. `DBs` tidak ada → rute database tidak didaftarkan, dan hook pengguna tidak jalan (test lama tanpa PostgreSQL tidak berubah).

## Task 4

27. **ID backup** = nama file; waktu diambil dari nama file (zona waktu `Now()`), bukan mtime. Backup manual berakhiran `-m.dump`. Prune memakai waktu dari nama file dan mengabaikan file lain (mis. `.tmp-*`).
28. **`BackupPath` menolak symlink** (`Lstat` harus berkas biasa) selain pola nama.
29. **Backup dan restore satu database diserialkan** (mutex per nama database).
30. **`Service.Backup(ctx, user, dbID)`** (manual, memeriksa kepemilikan) ditambahkan; `BackupNow` tetap sesuai rencana. `BackupAll` dipanggil `RunSchedule` dan bisa dipanggil sendiri.
31. **Konfirmasi restore** diperiksa di handler API sebelum `Restore` menyentuh PostgreSQL. Saat restore, koneksi aplikasi diputus sengaja; kumpulan koneksi aplikasi gagal sekali lalu pulih, atau perlu deploy ulang bila aplikasi tidak menyambung ulang.
32. **Pesan galat alat**: `*ToolError` berisi hingga 4 KB stderr pertama; API menjawab 502 dengan teks itu.
33. **Berkas `testdata/dbapp/main.go`**: aplikasi uji asap (tabel `visits`, `GET /` jumlah baris, `POST /add`).

## Pengujian dan uji asap

- `RUNOVA_PG_TEST=1 go test ./internal/pg ./internal/dbs -v` dijalankan sungguhan (PostgreSQL 18 di Docker): semua lulus, termasuk `TestBackupRestoreRoundTrip` (pemilik tabel tetap `budi` sesudah restore).
- **Uji asap** dengan `postgres:18` (port host acak, `-p 0:5432`, mendengarkan semua antarmuka), `RUNOVA_PG_DSN`, server yang dibungkus sementara (hanya mengganti `Chown`, seperti Bagian 3): buat admin → buat pengguna `budi` (role dengan `CONNECTION LIMIT 20`) → database `budi_visits` → unggah `testdata/dbapp` → deploy → `POST /add` dua kali lalu `GET /` menampilkan `rows=2` (container mencapai PostgreSQL lewat `host.docker.internal` dengan `DATABASE_URL` otomatis) → backup manual → unduh (`PGDMP`, `pg_restore -l` terbaca) → tambah 3 baris (5) → restore dengan konfirmasi salah ditolak 400 → restore → 2 baris, pemilik `budi`. Path traversal pada ID backup 404. Hapus database yang dipakai 409 "Masih dipakai oleh: visits". Rotasi password memberi notifikasi "Deploy ulang visits …". Container, jaringan, dan berkas sementara dibersihkan.
- Temuan uji asap (bukan bug Bagian 5): setelah restore, kumpulan koneksi aplikasi contoh mendapat `FATAL: terminating connection` pada permintaan pertama (diputus sengaja oleh `TerminateConnections`) sehingga aplikasi contoh sempat tidak merespons sampai dideploy ulang. Aplikasi dengan penanganan koneksi putus (umum di pool) pulih sendiri.
- Subnet Docker: pada mesin dev ini jaringan `runova-u1` sudah dipakai sesi lain pada `172.30.1.0/24`; deploy awal gagal "Pool overlaps". Dihindari dengan mengubah `users.net_index` di SQLite uji. Bukan kode Bagian 5.
