# Domain hosting bersama: catatan implementasi

Spec: `docs/superpowers/specs/2026-11-03-hosting-domains-design.md`. Rencana: `docs/superpowers/plans/2026-11-03-hosting-domains.md`. Tidak ada bagian spec yang tidak mungkin; berikut keputusan kecil yang spec biarkan terbuka.

- **Tes adopsi DB pra-migrasi** (`TestMigrateAdoptsPreMigrationDatabase`): DB yang diadopsi kini berakhir di versi 2 dan memicu cadangan `bak-v1` sebelum `0002` (aturan di `db-migrations.md`: adopsi → v1, migrasi tertunda → cadangan). Ekspektasi tes diperbarui, perilaku runner tidak berubah.
- **`{host}` oleh non-admin**: dicek setelah kepemilikan aplikasi (pengguna lain tetap 404), sehingga ID aplikasi orang lain tidak bocor lewat 403.
- **Domain bebas di tes lama** (`internal/apps`, `internal/api`): sekarang memakai admin, karena pengguna biasa tidak boleh menambahnya. Pengguna biasa dengan domain bebas lama diuji di `TestUserCannotAddOwnDomain` (data disisipkan lewat store).
- **Label panel dicadangkan** bila domain panel tepat satu label di bawah domain terdaftar (diperiksa saat label dipakai, bukan saat domain panel diubah).
- **Cek DNS** memakai alamat IP non-loopback dan non-link-local mesin bila `PublicIP` kosong; bila tidak ada, hasilnya `error` "IP publik server belum diketahui".
- **Hitungan Let's Encrypt** dikelompokkan per eTLD+1 atas semua domain terdaftar (termasuk yang dimatikan) dan menambah 1 untuk sertifikat panel bila domain panel serumpun. Bersifat perkiraan; UI menyebutnya begitu.
- **Teks UI kartu Server**: chip DNS "DNS benar"/"DNS salah" dan tombol Cek DNS hanya ikon (judul "Cek DNS"), agar tabel muat di 768 px tanpa gulir horizontal.
- **Daftar `/api/hosting-domains`** untuk admin menyertakan `public_ip` (untuk petunjuk DNS di kartu); pengguna biasa hanya `{id,name}` dan kuota.
- **Fake backend** (`RUNOVA_FAKE_BACKENDS=1`): cek DNS palsu, domain berawalan `bad.` → tidak ada record, `proxy.` → alamat Cloudflare, lainnya `ok`.
- **Tidak dikerjakan** (di luar lingkup spec): Proxied/Tunnel, wildcard DNS-01, kuota subdomain terpisah, ganti label.
