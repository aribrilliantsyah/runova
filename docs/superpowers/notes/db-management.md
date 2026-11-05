# Database management: tabel, query, restore dari file, export

Brief: `.brief-db-management.md` (branch `feat/db-management`). Keputusan yang diambil dan alasannya.

## Koneksi dengan role pengguna (keputusan wajib koordinator)

1. **Satu koneksi `pgx` baru per aksi** (`pg.PgxUsers`, `internal/pg/userdb.go`) dengan `user=<username>`, password dari store (`secret.Box`, sama dengan `connection/reveal`), `database=<db>`; ditutup di akhir aksi. Tidak ada `SET ROLE` di pool `runova`: `session_user` = role pengguna, jadi `RESET ROLE`, `SET ROLE runova`, `set_config('role'|'session_authorization', …)` gagal (dibuktikan `TestIntegrationUserRoleCannotEscape`).
2. **Jalur koneksi: TCP `127.0.0.1`, port dari DSN admin.** DSN admin produksi memakai socket Unix dengan *peer* auth, yang hanya cocok bila nama user OS = nama role, jadi tidak bisa dipakai role pengguna. `pg.NewUsers` mengganti host socket (atau kosong) dengan `127.0.0.1`; DSN TCP (dev/e2e) dipakai apa adanya.
3. **`pg_hba`:** baris bawaan Ubuntu `host all all 127.0.0.1/32 scram-sha-256` sudah ada, tetapi tidak dijamin (admin bisa mengubah `pg_hba.conf`). File milik Runova (`pg_hba.d/runova.conf`, ditulis `WriteHBA` dan seed installer) kini selalu memuat `host all all 127.0.0.1/32 scram-sha-256` di baris pertama. Ini tidak membuka akses baru: SSH tunnel yang didokumentasikan sudah memakai jalur itu, dan role tetap dibatasi password + `CONNECT` yang dicabut dari `PUBLIC`.
4. **Restore (dari backup internal maupun file) dan export juga memakai role pengguna** (`PGUSER`/`PGPASSWORD`/`PGHOST=127.0.0.1` untuk `pg_restore`/`pg_dump`, env `PG*` proses dibuang). Alasan: dump custom berisi SQL arbitrer (`SET ROLE`, `RESET ROLE`, dll. di TOC). Dengan `PGUSER=runova --role=<role>`, file unggahan bisa menulis `RESET ROLE` dan menjalankan sisanya sebagai `runova`, yang punya akses ke role semua pengguna. Restore dari backup internal ikut pindah karena isi backup juga dikendalikan pengguna (dibuat dari datanya sendiri, dan bisa diunduh/diganti lewat upload). Argumen `--clean --if-exists --no-owner --role=<role>` tetap sama persis dengan restore lama. `TerminateConnections` tetap lewat `runova` (butuh hak atas sesi lain). Backup terjadwal/manual tetap lewat `runova` (hanya membaca).

## Pagar query (`internal/sqlguard`)

5. **Lexer sendiri, bukan regex.** Memahami `'…'` (dengan `''`), `E'…'` (backslash), `U&'…'`, `"…"`, `$tag$…$tag$`, komentar `--` dan `/* */` bersarang, dengan `standard_conforming_strings=on` (bawaan PG ≥ 9.1). Titik koma di luar itu memisah perintah; lebih dari satu perintah bertubuh → ditolak. Titik koma di akhir dan komentar di akhir dibuang dari SQL yang dikirim; komentar di depan dipertahankan agar posisi galat cocok dengan baris editor.
6. **Kata pertama** (lewati `(` pembuka): baca = SELECT/WITH/EXPLAIN/SHOW; tulis + INSERT/UPDATE/DELETE/TRUNCATE dan CREATE/ALTER/DROP hanya bila objeknya TABLE atau INDEX (setelah `TEMP|TEMPORARY|UNLOGGED|GLOBAL|LOCAL|UNIQUE`). `CREATE FUNCTION/EXTENSION/VIEW/ROLE…`, `ALTER ROLE/SYSTEM/DEFAULT PRIVILEGES`, `DROP OWNED/ROLE/DATABASE…`, `VALUES`, `TABLE`, `MERGE`, transaksi, kursor, dan seluruh daftar terlarang brief ditolak di kedua mode dengan nama perintahnya. `EXPLAIN [ANALYZE] <x>` mengikuti aturan `<x>` (EXPLAIN ANALYZE menjalankannya).
7. **Batas sebenarnya tetap PostgreSQL.** Catatan koreksi brief: pagar UX boleh lebih ketat dari role (CREATE FUNCTION plpgsql ditolak panel padahal sah untuk owner; pengguna tetap bisa lewat DBeaver), tetapi panel tidak pernah memberi hak lebih dari role. `TestIntegrationUserOwnsTrustedFunctions` membuktikan role memang boleh; `TestIntegrationUserRoleCannotEscape` membuktikan daftar wajib-gagal ditolak oleh **PostgreSQL**, dijalankan langsung tanpa pagar: LANGUAGE c/plpython3u/internal, CREATE EXTENSION file_fdw/dblink, CREATE LANGUAGE, SET ROLE/SESSION AUTHORIZATION runova, `set_config` role/session_authorization, `pg_read_file`, `pg_read_binary_file`, `pg_ls_dir`, `lo_import`, COPY FROM/TO file dan TO PROGRAM, ALTER ROLE (diri sendiri dan orang lain), GRANT role ke/dari role lain, GRANT pg_read_server_files, CREATE ROLE/DATABASE, ALTER SYSTEM, `pg_reload_conf`, terminate sesi runova, `pg_authid`, referensi lintas database. `TestIntegrationUserCannotReachOtherDatabases` membuktikan koneksi ke database pengguna lain ditolak (`permission denied`).

## Eksekusi

8. **Satu statement lewat protokol extended** (`Parse`/`Bind`/`Describe`/`Execute`/`Sync` mentah di `pgconn`): `Parse` tanpa nama menolak teks berisi lebih dari satu perintah (`cannot insert multiple commands into a prepared statement`), lapisan kedua setelah sqlguard. Tidak pakai `Exec` simple protocol.
9. **Batas baris di sisi server, bukan injeksi `LIMIT`.** Brief meminta `LIMIT 1000` diinjeksi; menulis ulang SQL pengguna rawan (subquery, `FETCH FIRST`, `UNION`, CTE). Dipilih `Execute{MaxRows: 1001}`: server berhenti setelah 1001 baris (`PortalSuspended`) → `truncated: true`, 1000 baris dikirim. Hasil sama untuk pengguna, tanpa parsing. DML dengan `RETURNING` tetap jalan penuh; hanya keluarannya dipotong.
10. **Mode baca:** `default_transaction_read_only=on` sebagai parameter sesi + `BEGIN READ ONLY` … `ROLLBACK`. `set_config('transaction_read_only','off')` di dalam transaksi read-only gagal di PG; CTE `DELETE … RETURNING` gagal (test). Mode tulis: `BEGIN READ WRITE` … `COMMIT`.
11. **Timeout** sebagai parameter startup (`statement_timeout` 10 s / 30 s, juga `lock_timeout`, `idle_in_transaction_session_timeout` 60 s). Mengubah `statement_timeout` dari dalam query tidak memengaruhi statement yang sedang berjalan (test).
12. **5 MB:** ukuran JSON baris dihitung per baris (escape terburuk); baris yang melewati ~4,94 MB tidak dikirim dan `truncated` diset. Satu pesan protokol dibatasi 8 MB (`SetMaxBodyLen`) agar satu nilai raksasa tidak menghabiskan memori.
13. **Galat PostgreSQL bukan galat HTTP.** `POST …/query` menjawab 200 dengan `error` (severity, SQLSTATE, pesan, detail, hint) dan `error_line` (posisi PG dikonversi ke baris teks asli); timeout (57014) dan read-only (25006) diberi pesan Indonesia. Hanya kepemilikan/koneksi gagal yang menjadi 4xx/5xx.
14. **Tipe kolom** diambil dengan `format_type(oid, typmod)` setelah statement, sehingga tampil `numeric(10,2)`, bukan OID.

## Tabel browser

15. Daftar dari `pg_class` skema `public` (`relkind r|p`), urut `COLLATE "C"`. Jumlah baris: `count(*)` bila heap ≤ 16 MB, selain itu `reltuples` (`rows_estimated: true`, UI menulis "≈"). Ukuran `pg_total_relation_size`.
16. Nama tabel dari URL harus ada di daftar tabel milik database itu (`knownTable`) **sebelum** dipakai, lalu selalu `pgx.Identifier{"public", t}.Sanitize()`. Preview data urut primary key (atau `ctid` bila tidak ada) dengan `OFFSET/LIMIT 101`, read-only.
17. Truncate/drop butuh konfirmasi nama tabel di backend (`confirm`), bukan hanya di dialog.

## Restore dari file dan export

18. Multipart: field `confirm` dikirim **sebelum** `file` (`api.upload` menerima field tambahan), sehingga pengecekan pemilik dan nama terjadi sebelum file dibaca. File disimpan dengan nama acak `.upload-*.dump` di folder backup (0600, bukan di folder database mana pun; nama klien tidak pernah dipakai, jadi path traversal tidak mungkin) dan dihapus dengan `defer`. Batas = `UploadLimitMB` setelan admin (bawaan 500 MB). Hanya file berawalan `PGDMP` (format custom) yang diterima; file `.sql` ditolak sebelum `pg_restore` atau `TerminateConnections`.
19. Export men-stream stdout `pg_dump` langsung ke response. Header dikirim saat byte pertama tiba, jadi kegagalan awal (koneksi) masih jadi 502 JSON; kegagalan di tengah membatalkan response (`http.ErrAbortHandler`) agar browser tidak menyimpan file terpotong yang tampak utuh.

## UI

20. Bagian **Tabel / Query** berada tepat di bawah kartu database (pekerjaan utama setelah memilih database), di atas Koneksi dan Backup. Ruang kerja dibangun sekali per database dan **tidak ikut di-render ulang oleh polling 5 detik** halaman, sehingga teks, fokus, dan hasil editor tidak hilang (smoke memeriksa ini).
21. Editor: `textarea` monospace dengan gutter nomor baris (baris galat ditandai), Ctrl/⌘+Enter, toggle Baca/Tulis (mode Tulis memberi tepi dan catatan warna peringatan: penanda status nyata), waktu eksekusi. Hasil: grid sel monospace dengan header lengket, nomor baris lengket, scroll horizontal/vertikal di dalam kotak (tidak pernah menggeser halaman: dicek di 360/768/1550), NULL dibedakan. Grid ini sengaja tidak memakai tabel bertumpuk mobile (`table.t`): data acak lebih terbaca sebagai kolom.
22. Riwayat: `localStorage` `runova.sqlhistory.<dbID>`, 50 terakhir, tanpa duplikat. CSV di browser dari data yang sudah ada (RFC 4180, NULL kosong, nilai yang diawali `= + - @ tab CR` dan bukan angka diberi `'` agar spreadsheet tidak menjalankannya sebagai formula).
23. Ikon baru: `table`, `eraser` (via `web/icons.txt` + `go run ./cmd/genicons`).

## Risiko tersisa

- Koneksi panel ke `127.0.0.1` menghitung kuota `CONNECTION LIMIT` pengguna; bila aplikasi memakai semua slot, panel menjawab "Tidak bisa masuk ke database…: too many connections".
- Bila admin menghapus baris `127.0.0.1` dari `pg_hba.conf` **dan** `include_dir pg_hba.d` dihapus, semua fitur panel database (termasuk restore) gagal dengan pesan koneksi; installer dan `WriteHBA` memulihkannya saat start.
- Restore sebagai role pengguna berarti dump yang membuat objek milik superuser (mis. `CREATE EXTENSION`) gagal di bagian itu; `pg_restore` melaporkan galat dan tetap memulihkan sisanya (perilaku sama dengan pengguna yang restore lewat DBeaver).
- Mode Tulis berjalan sebagai owner: `DROP TABLE` lewat query tidak meminta konfirmasi nama (hanya tombol Hapus tabel yang meminta). Riwayat aktivitas mencatatnya.
