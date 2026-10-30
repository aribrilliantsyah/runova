# Bagian 1: hasil review

Cakupan: `939e0e6..c120084` (config, secret, store, auth, api, cmd). Diperiksa terhadap `docs/spec.md` §2/3/8/9, rencana fondasi (Global Constraints, Review Focus), dan `part1-deviations.md`. Tidak ada nama/signature di blok Interfaces yang berubah.

## Temuan yang diperbaiki

| # | Severity | Temuan | Perbaikan |
|---|---|---|---|
| 1 | tinggi | **Batas login bisa dilewati dengan permintaan paralel.** `login` memanggil `Locked`, lalu bcrypt (~50–100 ms), baru `Fail`. Banyak tebakan serentak lolos semua pemeriksaan sebelum satu pun tercatat (uji: 40 permintaan paralel → 40× 401, tanpa 429). Klien yang memutus koneksi saat bcrypt juga tidak pernah terhitung (konteks request batal → `Fail` gagal). | `server.reserveAttempt`: cek-kunci dan pencatatan satu percobaan dilakukan atomik (mutex) sebelum cek password; sukses tetap memanggil `Reset`. Test `TestLoginLockoutHoldsUnderConcurrency` gagal sebelum, lulus sesudah. Commit `fix(part1): count login attempt atomically…` |
| 2 | sedang | **Log aktivitas hilang bila klien memutus koneksi.** `record` memakai `r.Context()`; mutasi sudah commit tetapi catatan audit gagal ("context canceled"). | `context.WithoutCancel` di `record`. Test `TestRecordSurvivesCanceledRequest`. |
| 3 | rendah | Hash tiruan (`dummyHash`) dihitung malas: login pertama untuk username tak dikenal lebih lambat ~1 bcrypt, membocorkan keberadaan username lewat waktu (hanya sekali per proses). | Dihangatkan di `api.New`. |
| 4 | sedang | **Mode 0600 DB tidak dipaksakan untuk file yang sudah ada** (DB hasil restore/`touch` 0644 tetap 0644; spec §2 mensyaratkan 0600). | `Open` memanggil `Chmod(0600)`. Test `TestOpenTightensExistingFileMode`. Test `TestDatabaseFilesPrivate` memastikan `-wal`/`-shm` juga privat (sudah benar, kini dijaga). |
| 5 | sedang | **Kunci penghitung bisa dipecah.** `ClientIP` mengembalikan XFF mentah: `::ffff:1.2.3.4`, `1.2.3.4`, dan alamat ber-zona dianggap klien berbeda; peramban di belakang proxy dual-stack mendapat batas login ganda. Loopback IPv4-mapped (`::ffff:127.0.0.1`) juga tidak dikenali sebagai loopback. | `Unmap()` dan buang zona di `ClientIP`/`isLoopback`. Kasus baru di `TestClientIP`. |

## Diperiksa dan dinyatakan benar

- Token sesi: 32 byte `crypto/rand`, hanya hash SHA-256 di DB; cookie `HttpOnly`, `SameSite=Lax`, `Secure` hanya via TLS atau XFP dari loopback; logout menimpa cookie dengan atribut `Secure` yang sama.
- Ganti/reset password dan hapus pengguna menghapus semua sesi (satu transaksi / FK cascade); sesi pengguna terhapus → 401.
- Admin terakhir: dicek dalam transaksi `_txlock=immediate`. Dua admin saling menurunkan secara paralel menyisakan tepat satu admin (`TestLastAdminHoldsUnderConcurrency`, 20 putaran, `-race`).
- Enumerasi user: pesan sama, hash tiruan menyamakan waktu, username dipotong 64 rune sebelum disimpan.
- AES-GCM: nonce 12 byte acak per `Seal`, format `base64url(nonce‖ct)`, `Open` hanya mengembalikan `ErrDecrypt` (tanpa oracle), `GenerateKey` `O_EXCL` 0600 dan menghapus file bila gagal tulis, `LoadKey` tidak pernah membuat kunci.
- Password acak CLI: `crypto/rand` + `rand.Int` (tanpa modulo bias), alfabet tanpa `0OIl1`, 20 karakter.
- Origin check: `null` dan sufiks host ditolak; GET tidak diperiksa (sesuai spec). Body 1 MiB, 415/413 benar.
- Deviasi 1–9 di `part1-deviations.md` masuk akal; no. 2 (tidak boleh menurunkan diri sendiri) memang diwajibkan spec §3.

## Temuan yang tidak diperbaiki

Untuk Bagian lain (installer):
- **[sedang] Kepemilikan file.** `runova keygen` dan `runova admin create` yang dijalankan sebagai root membuat `secret.key` dan `runova.db` (+ `-wal`/`-shm`) milik root 0600; service `User=runova` lalu tidak bisa membacanya/menulisnya. Installer harus memanggil keduanya sebagai user `runova` (`runuser -u runova --`) atau `chown runova` sesudahnya, dan `/var/lib/runova` harus sudah milik `runova` sebelum `admin create`.
- **[rendah] Caddy dan XFF.** Kunci throttle bergantung pada elemen pertama XFF. Caddy menimpa XFF dengan alamat asli kecuali `trusted_proxies` diisi; rencana Caddy tidak boleh mengisinya (mis. saat Cloudflare Tunnel, XFF dari Cloudflare baru dipercaya bila sengaja dikonfigurasi).

Minor, tidak diubah:
- [rendah] Batas login per (username, IP): penyerang dengan banyak IP tetap bisa menebak satu akun tanpa batas global. Sesuai spec; pertimbangkan batas per-username di versi berikut.
- [rendah] `decode` menerima field tak dikenal dan data tambahan setelah objek JSON.
- [rendah] Pesan 429 memakai `until.Local()` (zona waktu server, bukan klien).
- [rendah] `Login` tidak menghapus sesi lama dari cookie yang dikirim klien; sesi lama baru hilang saat kedaluwarsa/logout.
- [rendah] `cleanupLoop` tidak ditunggu sebelum `st.Close()`; paling buruk satu query berjalan pada DB tertutup saat shutdown (error disenyapkan oleh `ctx.Err()`).
- [rendah] `admin create/reset-password` lewat CLI tidak menulis log aktivitas.
- [rendah] `store.CreateUser` tidak memvalidasi `Role`; nilai tak sah baru ditolak oleh `CHECK` sebagai error umum (API sudah memvalidasi).
- [rendah] `login_failures` tumbuh per username unik yang ditebak; dibersihkan harian, ukuran baris dibatasi oleh pemotongan 64 rune.

## Kualitas test

Umumnya menguji perilaku nyata (end-to-end lewat HTTP, jam yang bisa dimajukan, cookie jar). Celah yang ditutup: paralelisme throttle, paralelisme admin terakhir, mode file DB, kanonisasi IP, audit saat klien memutus. Celah yang tersisa: `serve` (start/shutdown/cleanupLoop) tidak diuji otomatis (hanya uji asap manual rencana Task 6).
