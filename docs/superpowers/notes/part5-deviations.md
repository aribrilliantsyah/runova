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
