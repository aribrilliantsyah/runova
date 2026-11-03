# Listener Caddy 2.6.2 bocor setelah load ditolak

## Akar masalah (direproduksi dengan admin API langsung, tanpa Runova)

Caddy 2.6.2 (versi paket Ubuntu 26.04) dan `POST /load`:

1. `:L` dilayani oleh konfigurasi yang berjalan.
2. `/load` konfigurasi yang masih memuat `:L` ditambah `:T`, di mana `:T` dipegang proses lain, ditolak 400 (`http app module: start: listening on :T: … address already in use`).
3. `/load` tanpa server berikutnya menjawab 200, tetapi `:L` tetap melayani. Satu siklus load kosong lagi juga tidak menutupnya. Caddy 2.11.7 menutupnya.

Load yang gagal **saat start** meninggalkan listener yang tidak lagi dilepas oleh load berikutnya (penghitung rujukan listener `:L` tidak turun). Dampaknya keamanan: akses IP yang dimatikan di panel tetap terbuka.

Hasil probe tambahan di 2.6.2 (`:L` berjalan lebih dulu):

| Urutan | `:L` sesudah load kosong |
|---|---|
| tanpa penolakan, `:L` → `:M` → kosong | tertutup |
| ditolak (`:L`+`:T`), lalu kosong | **terbuka** |
| ditolak dengan `:L` ikut dimuat ulang (`:L`+`:T`, urutan apa pun) | **terbuka** |
| ditolak tanpa `:L` (`:M`+`:T` atau `:T`), lalu kosong | tertutup |
| ditolak saat provision (modul handler tak dikenal), lalu kosong | tertutup |
| ditolak, lalu ganti port `:L` → `:M` | `:L` tertutup, `:M` terbuka |

Jadi kebocoran hanya terjadi bila konfigurasi yang ditolak gagal **di tahap start** dan memuat ulang port yang sudah dilayani. Mengganti upstream, menghapus domain, dan mengganti port panel tanpa penolakan tidak membocorkan; penolakan tahap provision (validasi) juga tidak.

Test `TestCaddyIntegration/removing_the_listener_closes_the_port` menangkapnya: dengan `caddy:2.6.2` gagal 3 dari 5 kali sebelum perbaikan (urutan "tolak lalu hapus" di test sebelumnya).

## Perbaikan yang dipilih: A (cegah load yang akan ditolak)

`Syncer.apply` memanggil `checkPorts` sebelum `Load`: tiap port listener IP dan port IP panel yang **belum dipegang konfigurasi Caddy yang berjalan** (dibaca dari `GET /config/`) dicoba dengan `net.Listen("tcp", ":port")` lalu ditutup. Port terpakai → `port N sudah dipakai proses lain: …` dan Caddy tidak dipanggil, jadi konfigurasi lama tetap utuh dan tidak ada listener yatim. Ini penyebab umum yang terbukti (port bentrok, mis. proses lain mengambil port 20xxx).

`Syncer.PortFree` (bawaan `caddy.PortFree`) dapat diganti di test.

## B (stop + restart systemd) tidak diterapkan

Pemulihan B terbukti bekerja (`caddy:2.6.2`, `--restart always --resume`: setelah load ditolak, `POST /stop` → admin kembali ≈0,4 s, `:L` dilayani oleh konfigurasi terakhir yang berhasil, load kosong berikutnya menutup `:L`), tetapi tidak dipasang:

- Jalur yang tersisa hanya balapan antara pemeriksaan port dan bind Caddy (milidetik, dan hanya bila proses lain mengambil port persis di jendela itu). Tidak ada kegagalan start lain yang terbukti membocorkan.
- B memutus semua situs selama beberapa detik pada tiap penolakan tahap start dan memerlukan `Restart=always` di drop-in `install.sh` serta skenario `test/system/run.sh` baru. Biaya dan risiko (Caddy bisa mati terus bila konfigurasi `--resume` rusak) tidak sebanding dengan jendela balapan itu.

Bila jendela balapan perlu ditutup kelak: pada balasan 400 yang pesannya memuat `: start:`, panggil `POST /stop` lewat admin API, tambahkan `Restart=always` di drop-in, tunggu admin hidup, lalu `Apply`.

## Verifikasi

- `go test ./internal/caddy`: `TestApplyRefusesPortInUseWithoutLoading`, `TestApplyLoadsWhenPortsAreFree`, `TestPortFree`.
- Integrasi (`caddyImage = "caddy:2.6.2"`): port dipegang proses lain → ditolak sebelum Caddy, konfigurasi lama tetap melayani; lalu mematikan akses IP menutup port. Lulus 10 kali berturut-turut (sebelumnya flaky/selalu gagal).
