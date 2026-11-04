# Halaman daftar: Aktivitas gaya ELK dan Pengguna dengan paging/filter/sorting

Brief: `.brief-list-pages.md`. Referensi pola: repo nineguard (`views/logs.js`, `filters.js`, `chart.js`, `views/users.js`), diadaptasi ke token Runova (`tokens.css`, IBM Plex, Lucide).

## API

### `GET /api/activity/search` (baru, semua pengguna)

| Param | Arti |
|---|---|
| `q` | Sintaks pencarian (di bawah) |
| `from`, `to` | Unix ms, rentang setengah terbuka `[from, to)`; 0/kosong = tanpa batas |
| `limit`, `offset` | Paging; `limit` bawaan 50, maksimal 100 |
| `tz` | Offset UTC klien dalam menit (UTC+7 = `420`), untuk bucket harian yang mulai tengah malam lokal |
| `actor` | ID pelaku, hanya untuk admin (diabaikan untuk pengguna biasa) |

Jawaban: `{items, total, limit, offset, facets: {action, actor?, target, outcome: {ok, failed}}, histogram: {from, to, bucket_ms, buckets: [{start, count, failed}]}}`.

Sintaks `q` (parser Go `store.ParseActivityQuery`, cermin JS `web/js/components/query.js`):

- kata dipisah spasi di-AND, cocok substring tanpa beda huruf besar/kecil di pelaku, aksi, target, detail, IP;
- `"frasa persis"`; `-kata` mengecualikan;
- `aksi:` `pelaku:` `target:` `ip:` cocok persis satu kolom, `nilai*` = awalan, beberapa nilai field yang sama di-OR, `-field:nilai` mengecualikan; `pelaku:sistem` = entri tanpa pelaku;
- `hasil:gagal` / `hasil:berhasil` (`-hasil:gagal` = berhasil); nilai lain dan keduanya sekaligus → 400;
- `%` dan `_` selalu literal (LIKE di-escape); field tak dikenal (`x:y`) jadi kata biasa.

### `GET /api/users` (diubah, admin)

`q` (username, nama, email), `role` (`admin`|`user`), `sort` (`username`, `role`, `apps`, `memory`, `cpu`, `databases`, `created`, `last_login`), `order` (`asc`|`desc`), `limit` (bawaan 25, maks 100), `offset`. Jawaban: `{items: [user + usage + last_login_at], total, limit, offset, roles: {all, admin, user}}`.

### `GET /api/usage` dihapus

Pemakaian sekarang digabung ke `GET /api/users` dalam satu query (subquery agregat `apps`/`databases` + `MAX(at)` login), tanpa N+1. Satu-satunya pemakai adalah halaman Pengguna. `GET /api/activity` lama tetap (dipakai `test/e2e/nginx.sh` dan banyak test Go); panel tidak memakainya lagi.

## Keputusan

1. **Offset + total, bukan cursor.** Brief meminta "x–y dari z" dan ukuran 25/50/100. Offset dengan total sederhana, benar (tidak ada masalah entri berbagi milidetik di batas halaman, catatan part6 #19), dan cukup cepat: halaman maksimal 100 baris, `COUNT(*)` memakai indeks yang sama dengan filter. Biaya: offset dalam (halaman ratusan) membaca baris yang dilewati; untuk tabel aktivitas panel self-hosted ini dapat diterima. Bila suatu saat jutaan baris, ganti ke keyset `(at, id)`.
2. **Isolasi pengguna di satu tempat.** Handler mengisi `ActorID = me` untuk non-admin sebelum apa pun; `ActivitySearch.where()` memakai `actor_id = ?` sebagai syarat AND pertama untuk daftar, total, facet, dan histogram. Facet pelaku hanya dihitung untuk admin (`WithActors`). `TestActivitySearchScope` mencoba `pelaku:admin`, `-pelaku:budi`, `actor=<admin>`, `user.create` sebagai budi; `smoke-admin` mengulang `pelaku:admin` lewat UI.
3. **Hasil "gagal" = `login_failed`.** Itu satu-satunya aksi yang dicatat untuk sesuatu yang tidak terjadi. Daftar di `failedActions` (Go) dan `FAILED` (JS); tambah keduanya bila ada aksi gagal baru.
4. **Histogram ≤ 60 batang** dengan ukuran "bagus" (1 d … 30 hari), batang kosong ikut, bucket diselaraskan ke zona klien (`tz`). Tanpa `from` ("Semua waktu") histogram mulai dari entri tertua yang cocok.
5. **Facet maksimal 20 nilai per kelompok**, tampil 6 dengan "N lainnya"; nilai yang sedang difilter selalu terlihat.
6. **Migrasi `0003_activity_search.sql`:** `activity(actor_id, at)` untuk pengguna biasa dan filter pelaku, `activity(action, actor_id, at)` untuk filter aksi dan "login terakhir" di daftar pengguna. Pencarian teks bebas tetap scan (LIKE `%…%` tidak bisa memakai indeks); dibatasi rentang waktu (bawaan 24 jam, pakai `activity_at`).
7. **Sort whitelist di store** (`userSortCols`), error `store.ErrBadFilter` → 400. Sort sekunder selalu `username` agar urutan stabil antarhalaman; "Belum pernah login" di akhir untuk kedua arah.
8. **Jumlah peran mengikuti `q`, bukan `role`** (pola nineguard): angka di tombol peran menjawab "berapa yang cocok bila saya pilih peran ini".
9. **Aksi massal tidak ditambahkan.** API tidak punya aksi massal; brief melarang aksi berbahaya baru. Aksi per baris (Kuota + menu ⋯: reset password, ubah peran, hapus) sama seperti sebelumnya, dengan navigasi panah di menu.
10. **State di hash** (`#/activity?q=…&range=7d&page=2`, `#/users?sort=memory&order=desc&role=admin`), nilai bawaan tidak ditulis. Halaman memakai `update()` router sehingga ganti filter tidak membangun ulang halaman.
11. **Live** = muat ulang diam-diam tiap 5 detik (`poll`, berhenti saat tab tersembunyi), hanya di halaman 1 dan rentang relatif; baris baru diberi tanda tepi hijau. Bukan WebSocket: aktivitas jarang, polling lebih sederhana.
12. **Kartu, bukan tabel, ditentukan lebar kartu** (container query `lcard`), bukan lebar layar: di 768 px dengan sidebar, area isi hanya ~480 px. Aktivitas: ringkasan hilang < 1020 px, IP < 860 px, kartu < 720 px. Pengguna: "Dibuat" < 1180, "Login terakhir" < 1020, kartu < 860.
13. **Shell (`shell.js`)** hanya diubah satu baris: hitungan Pengguna di sidebar membaca `total` dari `/api/users?limit=1`. Sesi paralel lain juga menyentuh `shell.js` (ikon nav); konflik merge mungkin tetapi satu baris.
14. **Ikon baru** (Lucide 1.65.0 lewat `cmd/genicons`): `chevron-up`, `chevron-left`, `funnel`, `clock`, `circle-plus`, `circle-minus`, `braces`, `radio`, `arrow-down-up`, `log-in` (`log-in` sudah dipakai `activity.js` sebelumnya tetapi tidak ada di sprite).

## Komponen bersama (`web/js/components/`)

- `list.js`: `dataTable` (header sortable dengan `aria-sort`, `data-label` per sel untuk kartu), `pager` ("x–y dari z", ukuran 25/50/100, sebelumnya/berikutnya), `searchBox` (debounce, Enter langsung, Esc kosongkan), `segFilter` (segmen dengan jumlah), `chips` (filter aktif, bisa dihapus), `emptyState`, `skeletonRows`.
- `hashstate.js`: `readState`/`writeState`/`stateHash`.
- `timerange.js`: pemilih rentang + `timeWindow`.
- `histogram.js`: grafik SVG (tooltip, klik/seret untuk zoom, panah kiri/kanan + Enter).
- `query.js`: parser sintaks pencarian sisi klien (chip, toggle facet, highlight).
- CSS di `web/css/lists.css` (kelas baru saja; kelas bersama tidak diubah).

## Data dev

`RUNOVA_DEV_SEED=1` (bersama `RUNOVA_FAKE_BACKENDS=1`, ditolak tanpa fake atau di data produksi lewat `onProductionData`) mengisi DB baru dengan 64 pengguna (8 admin), aplikasi dan database bervariasi, dan ±430 event selama sebulan, password semua akun seed `contoh-password-1`. Berjalan sekali (dilewati bila pengguna seed pertama sudah ada). `cmd/runova/devseed.go`, `TestDevSeed*`.

## Uji

- Go: `internal/store/activity_search_test.go` (parser, filter per sintaks, rentang, paging, facet, histogram + zona), `internal/store/users_list_test.go` (sort tiap kolom, whitelist, filter, paging, jumlah peran, pemakaian, login terakhir), `internal/api/activity_search_test.go` (isolasi di semua bagian jawaban, validasi param), `internal/api/users_list_test.go` (filter, sort, paging, total, 400, 403, `/api/usage` 404), `cmd/runova/devseed_test.go`.
- Web: `web/test/list.test.html` (parser JS, toggle facet, hash state, rentang waktu, `aria-sort`, pager); `smoke-admin.mjs` diperbarui untuk facet pelaku, sintaks, baris terbuka, reload; sort/filter/reset Pengguna.
