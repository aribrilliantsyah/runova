# Bagian 4: deviasi dan keputusan

Nama, signature, dan konstanta di blok Interfaces rencana tidak diubah. Hal-hal di bawah adalah tambahan atau koreksi.

## Caddy (Task 1–2)

1. **Server `runova-https` hanya mendengarkan `:443`, bukan `:80` + `:443`.** Rencana menyebut keduanya. Dengan `:80` di `listen`, Caddy menjadikannya server HTTP biasa dan **tidak** memasang pengalihan HTTP→HTTPS (diperiksa dengan Caddy 2.11: 200 di HTTP). Dengan `:443` saja Caddy membuat sendiri server `:80` untuk pengalihan 308 dan tantangan ACME (diperiksa: `308 → https://…`). Ini yang dimaksud "HTTPS otomatis (redirect HTTP→HTTPS) aktif" di rencana dan spesifikasi §6. Test `TestBuildSitesAndIP` memeriksa `[":443"]`.
2. **Sertifikat panel mode IP.** `tls_connection_policies` memakai `default_sni` = IP publik (atau `localhost` bila IP publik kosong) dan `apps.tls` membuat sertifikat internal untuk nama itu lewat `certificates.automate` + kebijakan `issuers: [{"module":"internal"}]`. Alasannya: peramban tidak mengirim SNI untuk alamat IP, jadi tanpa `default_sni` Caddy tidak punya sertifikat untuk dipilih. Diperiksa dengan Caddy nyata: `openssl s_client` ke `:port` tanpa SNI menerima sertifikat IP. `on_demand` sengaja tidak dipakai (tanpa `ask` ia membuat Caddy menerbitkan sertifikat untuk nama apa pun yang diminta klien).
3. **`State.AdminListen`** (field tambahan, kosong = `localhost:2019`): konfigurasi yang dikirim selalu memuat blok `admin`, dan bila `RUNOVA_CADDY_ADMIN` bukan bawaan, `POST /load` tanpa alamat itu akan memindahkan admin API ke `localhost:2019` dan memutus Runova dari Caddy. `config.Config` mendapat `CaddyAdmin` dan `CaddyAdminListen()`.
4. **`Build` menolak state yang tidak konsisten** (host ganda, port IP ganda, port panel sama dengan port aplikasi atau 80/443, domain/port panel tanpa upstream) dengan error, tidak diam-diam menghasilkan konfigurasi yang Caddy tolak atau tafsirkan lain.
5. **Rute panel** bernama `@id` `panel:<host>` dan `panel-ip` (rencana hanya menyebut `site:` dan `ip:`).
6. **`caddy.ProbeTLSAt(ctx, addr, host, roots)`** (tambahan): `ProbeTLS` memanggilnya dengan `127.0.0.1:443` dan root sistem; dipisah supaya bisa diuji dengan `httptest` TLS server. Pesan `detail` berbahasa Indonesia.
7. **Syncer** memuat saat start (`Run` memanggil `Apply` dulu), lalu tiap `Interval` memeriksa `GET /config/`; "kosong" = bukan JSON yang memuat server `runova-https`. Error dari latar dikirim ke `OnError`.
8. **Test integrasi memakai image `caddy:2` di Docker** (`--network host`, admin di port acak lewat konfigurasi awal), bukan binary lokal; `RUNOVA_CADDY_TEST_ADMIN` memakai Caddy yang sudah jalan (test restart dilewati untuk itu). Restart Caddy diuji dengan `docker restart`. Container `caddytest-<port>` dihapus di `t.Cleanup`. Test penolakan memakai listener pada port yang sedang terpakai: Caddy menerima upstream yang bentuknya salah saat `/load` dan baru gagal saat dipakai, jadi itu bukan contoh penolakan yang benar.

## Domain, akses IP, alamat panel (Task 3)

9. **Logika di `apps.Service`**, bukan di handler: `AddDomain`, `RemoveDomain`, `Domains`, `CheckDomain`, `CheckDomains`, `WatchDomains`, `SaveSettings` (file baru `internal/apps/domains.go`, `panel.go`, `caddystate.go`). Handler API (`internal/api/domains.go`) tipis. `Service.Probe` (hook, bawaan `caddy.ProbeTLS`) dan `Service.Caddy` ditambahkan.
10. **Status domain**: `pending` selama ≤10 menit sejak dibuat bila probe gagal (Caddy mungkin masih menerbitkan sertifikat), sesudah itu `error` dengan pesan; `active` saat probe lolos. Pemeriksa 5 menit memeriksa semua yang belum `active`. Notifikasi "HTTPS aktif untuk <host>" hanya pada peralihan ke `active`.
11. **Kegagalan Caddy**: `apps.ErrCaddy` ("Caddy menolak konfigurasi: …") dipetakan API ke **502**. `AddDomain` menghapus baris lagi; `UpdateSettings` (hanya bila `ip_access` berubah) mengembalikan semua setelan aplikasi ke nilai lama (`revertApp`); `SaveSettings` mengembalikan setelan lama. Stop/Start/Delete/deploy memakai `refreshCaddy` yang hanya mencatat log bila Caddy menolak (kontainernya sudah berubah dan tidak bisa dibatalkan; Syncer menyusul pada perubahan berikutnya atau cek 30 detik).
12. **Domain panel yang sudah dipakai aplikasi → 409 "Domain sudah dipakai"** (dari `PUT /api/settings` dan `runova panel set`); sebaliknya domain aplikasi yang sama dengan domain panel juga 409 dengan pesan yang sama, tanpa menyebut pemilik.
13. **Validasi `Settings`**: `panel_domain` harus sudah ternormalisasi; `panel_port` 1024–65535, bukan 2019, dan tidak di dalam rentang port akses IP. (Rencana tidak menyebut aturan port; port yang bentrok dengan listener aplikasi membuat `/load` ditolak.)
14. **`runova panel set --domain|--port`** saling menggantikan (panel diakses lewat domain *atau* `IP:port`, spesifikasi §6): salah satunya mengosongkan yang lain. `PUT /api/settings` tetap boleh mengisi keduanya.
15. **Aktivitas `panel.address`** dicatat bila domain/port panel berubah lewat `PUT /api/settings`.
16. `PanelUpstream` mengubah `RUNOVA_LISTEN` (mis. `:7070`/`0.0.0.0:7070`) menjadi alamat yang bisa dihubungi Caddy (`127.0.0.1:7070`).
17. Goroutine `Syncer.Run` dan `WatchDomains` dijalankan di `serve`. Caddy tak terjangkau saat start hanya dicatat (log); panel tetap jalan dan Syncer mencoba lagi tiap 30 detik.

## Deploy tanpa jeda (Task 4)

18. **`ActivateDeployment` sebelum `Apply`**: `CaddyState` membaca host port dari deployment aktif di DB, jadi deployment baru harus aktif dulu. Bila `Apply` ditolak, deployment lama diaktifkan lagi (`ActivateDeployment(old)`), container baru dihapus, deployment baru `failed`, aplikasi tetap `running`. Ini tidak ada di rencana (rencana hanya menyebut kegagalan sehat); tanpa ini container lama akan dihentikan sementara Caddy masih menunjuk ke sana.
19. **Selama deploy, aplikasi berstatus `deploying`** namun `CaddyState` tetap merutekannya ke deployment aktif lama (sesuai rencana "`deploying` dengan deployment aktif"); diuji dengan `TestZeroDowntimeKeepsRouteWhileDeploying`.
20. **Deploy pertama (tanpa container lama) atau yang gagal pada mode `zero` tanpa versi sebelumnya** → status `failed`, seperti Bagian 3. Mode `stopstart` tetap membuat status `failed` saat gagal (container lama sudah dihentikan).
21. **`failDeployment` mendapat parameter `appStatus`**: `failed`, atau status sebelum deploy bila mode `zero` dan ada container lama.
22. **Container lama dihentikan tanpa `noteStop`** setelah alih: ia bukan lagi deployment aktif, jadi `die`-nya diabaikan monitor (`handleEvent` hanya menghitung container deployment aktif).
23. **`Service.DrainDelay`** (bawaan 5 detik, 1 ms di test). `deploy_mode` `zero` sekarang diterima `UpdateSettings`.
24. **Dua test Bagian 3 diubah** karena bawaan aplikasi baru kini `zero`: `TestDeployHappyPath` (urutan panggilan Docker pada deploy kedua) dan `TestDeployUnhealthy` (aplikasi tetap `running`). `TestStopStartStillWorks` mencakup urutan lama untuk `stopstart`.
25. `Stop`, `Start`, `Delete`, dan deploy sukses di mode `stopstart` memanggil `refreshCaddy`; `Restart` tidak (container dan host port sama).

## Uji asap (Task 4 Step 5)

Dijalankan dengan Docker nyata dan Caddy dari image `caddy:2` (host network, admin `localhost:2019`), server Runova memakai pembungkus sementara yang hanya mengganti `Chown` (pengguna dev bukan root; pembungkus dan binary sudah dihapus):

- Panel di `https://panel.localhost` lewat `runova panel set --domain`, sertifikat internal Caddy; `http://` dialihkan 308.
- Aplikasi `hello` dengan akses IP: `curl 127.0.0.1:20000` → 200.
- Loop `curl` ke `:20000` selama deploy versi baru (dua kali, v1→v2 dan v2→v3): 772 dan 1288 permintaan, **0 gagal**; jawaban beralih dari versi lama ke baru di tengah loop; container lama hilang sesudahnya.
- Domain `app.localhost`: `curl -k --resolve app.localhost:443:127.0.0.1 https://app.localhost` → 200; `http://app.localhost` → 308 ke `https://`; domain yang sama dengan domain panel → 409.
- Akses IP dimatikan → koneksi ditolak (curl exit 7), domain tetap jalan; dinyalakan lagi → 200.
- Caddy di-restart (`docker restart`) → Syncer memuat ulang, `:20000` dan domain kembali melayani dalam ≤8 detik.
- Pengalihan HTTP yang gagal pada putaran pertama (200 alih-alih 308) menemukan deviasi nomor 1.

Container `caddy`/`runova-*` uji dan jaringan `runova-u1` dihapus sesudahnya.
