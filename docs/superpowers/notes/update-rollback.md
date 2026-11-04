# Pembaruan, `runova update`, `runova rollback`: catatan implementasi

Spec: `docs/superpowers/specs/2026-11-04-update-rollback-design.md`. Rencana: `docs/superpowers/plans/2026-11-04-update-rollback.md`.

## Keputusan di luar spec

- `runova update` selalu menjalankan `get.sh --version X --yes`. Konfirmasi sudah ditanyakan `update` sendiri; `install.sh` tanpa `--yes` akan menanyakan lagi dan gagal tanpa terminal.
- `runova update` tanpa `--version` pada build `dev` atau versi terpasang yang tidak bisa diurai ditolak dengan petunjuk `--version`; `--check` pada kasus itu melaporkan versi terbaru dan keluar 0 (tidak mengklaim ada pembaruan).
- `--version` sama dengan versi terpasang diizinkan (pasang ulang/perbaikan); hanya yang lebih lama ditolak.
- `--check` dan `--version` tidak bisa digabung (pemakaian salah, keluar 2).
- Setelan `update_check` yang berubah dari mati ke hidup memanggil `Checker.Poke()` sehingga cek berikutnya segera, bukan 6 jam lagi. Setelan dibaca setiap siklus dan setiap `GET /api/update`, jadi mematikan sakelar langsung menyembunyikan banner.
- `GET /api/update` dengan `update_check=false` melaporkan `enabled:false` dan `update_available:false` meski ada hasil cek lama.
- Mode `RUNOVA_FAKE_BACKENDS=1`: terpasang `0.2.0`, terbaru `0.3.0`, cek pertama 100 ms. Tautan catatan rilis dibentuk dari `RUNOVA_GITHUB`/`RUNOVA_REPO`.
- `rollback` memeriksa `rollback/runova db version` sebelum bertanya: binary lama yang tidak bisa dijalankan atau tidak melaporkan skema membatalkan rollback sebelum ada yang berubah.
- Urutan berkas saat `rollback`: kedua binary disiapkan (disalin ke berkas sementara sefolder) lebih dulu, baru database dipulihkan, baru `rename` binary. Gagal sebelum `stop`: tidak ada perubahan. Gagal sesudahnya: layanan dinyalakan lagi dan lokasi salinan `before-rollback` disebut.
- `rollback/VERSION` ditulis ulang dengan versi yang baru ditinggalkan, sehingga `rollback` kedua maju kembali ke versi semula (diuji di Go dan di `test/system/run.sh`).
- Versi sekarang yang dicetak ringkasan `rollback` berasal dari `runova version` binary yang sedang berjalan.
- `install.sh` yang gagal menyimpan binary lama hanya memberi peringatan; pemasangan jalan terus (rollback hilang, bukan pemasangan).

## Pengujian

- Go: `internal/update` (perbandingan versi, sumber dengan server HTTP lokal, jadwal dengan jam palsu), `internal/api/update_test.go`, `cmd/runova/{update,rollback,updatecheck}_test.go`.
- bats: `test/install.bats` (rollback/ dibuat hanya saat versi beda).
- Panel: `web/test/smoke-update.mjs`.
- Sistem: `test/system/run.sh` skenario 7, dengan `test/system/relserver` meniru GitHub (`RUNOVA_GITHUB`) dan rilis `0.0.3-systest` dari `scripts/release.sh`.
