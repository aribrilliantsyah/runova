# Aplikasi hidup lagi setelah server reboot

Branch `fix/reboot-autostart` (dari `main` = v0.3.12). Brief: `.brief-reboot.md`.

## Pertanyaan

"ketika server di restart runova ini auto up tidak?" Panel, Caddy, PostgreSQL ya (`systemctl enable`). Aplikasi: kebanyakan tidak.

## Akar masalah (terbukti)

Container dibuat dengan `RestartPolicy: on-failure`. Saat shutdown Docker mengirim SIGTERM; server yang punya signal handling (Spring Boot, Go) keluar dengan kode 0, dan `on-failure` tidak menyalakan exit 0 setelah daemon naik (diukur di docker:dind 29.9.3 oleh koordinator; yang mati karena sinyal, 143, dan `unless-stopped` naik lagi). `Reconcile` tidak pernah start container: hanya menandai `stopped` dan menunggu policy yang tidak akan menyalakannya. Itu pun membuat panel menampilkan aplikasi "Berhenti" padahal tidak ada yang menghentikannya.

## Keputusan: reconcile saja, policy tetap `on-failure`

Dipilih: Runova sendiri menyalakan di `Reconcile` saat start. Policy tidak diganti.

Alasan:
- Container lama (sudah dibuat dengan `on-failure`) tetap harus ditangani, dan container tidak dibuat ulang. Reconcile wajib ada apa pun yang terjadi; policy baru hanya menambah mekanisme kedua untuk hal yang sama.
- `unless-stopped` mengubah arti exit 0: Docker menyalakan lagi aplikasi yang keluar sendiri dan `Monitor` (`exited_clean` → `stopped`, notifikasi "berhenti sendiri") bertarung dengan Docker. Crash loop dengan exit code apa pun tidak berbatas di sisi Docker; hanya monitor yang menghentikan.
- Dengan reconcile, "ingin jalan" tetap satu sumber kebenaran: status di DB. Sumber kebenaran sudah ada, jadi **tidak ada kolom/migrasi baru**: `running`/`starting` = ingin jalan; `stopped` (Stop pengguna, atau exit 0 yang dilihat Monitor saat Runova hidup) dan `failed` = tidak. Saat reboot Runova mati bersama server, jadi tidak ada yang menulis `stopped` untuk container yang dimatikan Docker: statusnya masih `running`. Stop oleh pengguna menulis `stopped` lebih dulu, jadi reboot sesudahnya tidak menyalakan.
- Urutan start dan penantian port dikendalikan Runova (jeda, `awaitStart`, deteksi OOM), sesuatu yang tidak bisa Docker lakukan saat boot.

Risiko yang diterima: aplikasi baru naik setelah Runova naik (beberapa detik setelah Docker), bukan oleh Docker langsung; dan bila Runova sendiri gagal start, tidak ada yang menaikkan aplikasi (panel mati juga, jadi `systemctl status runova` yang pertama dilihat).

## Perilaku

- `Reconcile` = satu pass (`reconcilePass`, di bawah lock tiap aplikasi) + start. Hanya pass **pertama sejak proses Runova start** yang menyalakan (`Service.revived`). Reconcile sesudah reconnect Monitor tetap hanya menandai `stopped`: exit 0 saat Runova melihat adalah keputusan aplikasi, dan jangan dinyalakan terus.
- Kandidat: status `running`/`starting`, deployment aktif punya container, container tidak jalan. Statusnya langsung `starting` (tampil benar di panel dan tetap dirutekan Caddy, `Up()`).
- `reviveApps` berjalan di latar (`background`), satu aplikasi per `ReviveGap` (bawaan 5 detik; Reconcile tidak boleh menahan Monitor). Tiap aplikasi: lock diambil, status dibaca ulang (masih `starting`, deployment sama; Stop/deploy pengguna di antaranya menang), `startContainer`, lalu `awaitStart` yang sudah ada (menunggu port dengan batas waktu JAR, OOM, restart). Jeda memakai waktu mulai start, bukan menunggu port: JVM berikutnya mulai sementara yang pertama masih naik, tapi tidak serentak.
- Gagal start (mis. port dipakai): status `failed`, problem `start_failed` di deployment, notifikasi error.
- Notifikasi/aktivitas: satu notifikasi info per aplikasi per start Runova ("Aplikasi X dinyalakan lagi setelah server dimulai ulang") dan satu entri aktivitas `autostart`. Tidak berulang karena hanya pass pertama yang menyalakan dan sesudahnya aplikasi sudah jalan.
- Installer: `enable_docker` (`systemctl enable docker`, dan `containerd` bila unit ada) di `step_docker_daemon`, sebelum cabang yang bisa keluar lebih awal, jadi jalan di setiap pemasangan/pembaruan.

## Bukti

- Unit (`monitor_test.go`): merah di kode lama (aplikasi `running`/`starting` dengan container berhenti tidak dinyalakan, dilaporkan `stopped`), hijau sesudah. `stopped`/`failed` tidak dinyalakan; tiga aplikasi dinyalakan berjarak ≥ `ReviveGap`; start gagal → `failed` + notifikasi error; Reconcile kedua tidak start dan tidak notifikasi lagi.
- Integrasi Docker nyata (`RUNOVA_DOCKER_TEST=1 go test -p 1 ./internal/apps -run TestIntegrationRebootStartsAppAgain`): container Go yang keluar 0 pada SIGTERM, `docker stop`, Service baru + Reconcile → running, GET 200; aplikasi yang dihentikan pengguna tetap mati. Merah di kode lama.
- bats: tiga test baru merah tanpa `enable_docker`, hijau dengan.
- `test/system/run.sh` skenario "6b reboot server": lihat hasil di laporan.

## Batas uji reboot

`docker restart` pada container Ubuntu+systemd (SIGRTMIN+3 → systemd menghentikan semua unit berurutan, termasuk dockerd yang SIGTERM container aplikasi; naik lagi dengan disk yang sama) adalah yang paling dekat dengan reboot tanpa VM. Tidak tercakup: firmware, kernel, jaringan host sendiri, urutan boot host sungguhan.
