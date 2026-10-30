# Bagian 3: deviasi dan keputusan

Nama, signature, dan konstanta di blok Interfaces rencana tidak diubah (`Service`, `ContainerSpec(..., dbEnv []string)`, `deployments.host_port`, `apps.deploy_mode`, `docker.Engine`). Hal-hal di bawah adalah tambahan atau koreksi kecil.

## Docker (Task 1)

1. **Badan `containers/create` memakai field `Config` di tingkat atas**, bukan di dalam kunci `Config` (itu bentuk yang dipahami Engine API). Test `TestCreateContainerHardening` membaca bentuk itu.
2. **`docker.Error`** (tambahan): error non-2xx membawa status dan pesan Engine; `errors.Is(err, ErrNotFound)` benar untuk 404. `StartContainer`/`StopContainer` menganggap 304 ("sudah") sukses.
3. **`docker/dockertest`** (tambahan): engine palsu bersama untuk test `apps` dan `api`, agar tidak ada dua versi.
4. **`Logs`** meminta `timestamps=1`; `ReadLogs` memisahkan stempel waktu dari teks. `tail < 0` berarti semua baris.

## Store (Task 2)

5. **`users.net_index`** (kolom baru, UNIQUE, NULL) dan `Store.NetIndex`: dialokasikan saat pertama dipakai (terendah yang bebas, 1–254; habis → `ErrNoNetIndex`).
6. Tambahan store yang dibutuhkan lapisan atas: `ActivateDeployment` (satu transaksi: yang lama `superseded`, yang baru `active`, `current_deployment_id`, status `running`), `DeleteDeployment`, `ClearCurrentDeployment`, `DeploymentPatch.ArtifactPath`, `ActEnvReveal = "env.reveal"` (disebut di tabel endpoint, tidak di daftar aksi), `NotificationLevel` (`info`/`warning`/`error`).
7. **`PruneDeployments(keep)`**: deployment `active` dihitung sebagai salah satu dari `keep` dan tidak pernah dihapus; sisa slot untuk yang terbaru.

## Logika aplikasi (Task 3–4)

8. **Kuota pada perubahan resource**: yang dibandingkan dengan sisa kuota adalah **selisih** kenaikan (`mem - mem_lama`), jadi menaikkan sedikit saat sisa cukup tetap diizinkan dan menurunkan selalu diizinkan, juga bila pemilik sudah melebihi kuota (`TestQuotaBelowUsage`).
9. **`Create` membuat aplikasi berstatus `stopped`** dengan satu deployment `pending`; baru `Deploy` yang menjalankannya. Bila langkah sesudah `CreateApp` gagal, baris aplikasi dihapus lagi.
10. **`Service.Chown`** nil → `apps.OSChown` (`os.Chown(path, uid, uid)`).
11. **Rollback memakai kembali file artefak lama** (deployment baru menunjuk path yang sama). Karena itu `removeArtifacts` tidak menghapus file yang masih dipakai deployment yang tersisa (`TestPruneKeepsFileSharedWithRollback`).
12. **Rollback/deploy ulang dipecah**: `PrepareRollback`/`PrepareRedeploy` (validasi + buat deployment, sinkron) dan `Deploy` (lambat). API memanggil yang pertama di dalam request supaya kesalahan validasi menjadi 400/409, lalu `Deploy` di goroutine. `Rollback`/`DeployLatest` menggabungkan keduanya.
13. **`deploy_mode`**: hanya `stopstart` yang diterima `UpdateSettings` di bagian ini; `zero` ditambahkan Bagian 4.
14. **Deploy gagal**: deployment lama tidak disentuh, tetapi karena mode stop-start container lama sudah dihentikan sebelum yang baru dibuat, status aplikasi menjadi `failed` (sesuai rencana).
15. **Env pengguna di `ContainerSpec`** berurutan: pengguna → `dbEnv` → wajib Runova (`PORT`, `RUNOVA_DATA_DIR`, `HOME`); nama yang sama ditimpa di tempat.
16. **Tmpfs `/tmp`**: `rw,size=64m,mode=1777` (tanpa `mode=1777` tmpfs milik root dan UID pengguna tidak bisa menulis).
17. `WorkingDir` container = `/data`.

## API (Task 5)

18. **`api.Config`** mendapat `Apps`, `Context` (konteks server untuk deploy latar), `TerminalIdle` (bawaan 30 menit; dapat diperkecil di test). Tanpa `Apps` endpoint aplikasi tidak didaftarkan.
19. **Kesalahan WebSocket dijawab sebagai HTTP biasa sebelum upgrade** (404/409/401), jadi `TestAppOwnership` dan `TestTerminalStaticRejected` memeriksa penolakan handshake.
20. `GET /api/apps` mengembalikan `{apps, usage}` (`usage` = pemakaian + kuota pengguna yang login).
21. `PUT /api/apps/{id}/env` membalas `{"redeploy_required": true}` bila aplikasi sudah pernah berjalan.
22. `POST /api/apps/{id}/versions` membalas 201 dengan deployment baru; deploy jalan di latar.
23. Aplikasi tak ada, milik orang lain, atau ID rusak sama-sama 404 "Aplikasi tidak ditemukan".

## Monitor (Task 6)

24. **`die` dihitung hanya untuk container deployment aktif** dari aplikasi berstatus `running`; `die` dari container lama diabaikan.
25. **Stop oleh Runova** dicatat (`noteStop`) sebelum `StopContainer`; `die` pertama dalam 10 detik dipakai habis oleh catatan itu. Stop pengguna, restart, deploy (container lama), hapus, dan stop karena crash loop semuanya memanggil `noteStop`.
26. **Reconcile**: deployment berstatus `running` (deploy terpotong oleh restart Runova) → `failed` + notifikasi; aplikasi `deploying` tanpa deployment aktif → `failed`. Container hilang → deployment aktif menjadi `superseded`, `current_deployment_id` dikosongkan. Aplikasi yang sudah `failed` karena crash loop dan container-nya berhenti tetap `failed` (tidak diturunkan ke `stopped`). Bila ada aplikasi yang gagal direkonsiliasi, container yatim tidak dihapus (tidak diketahui mana yang yatim).
27. **`cmd/runova`**: `RUNOVA_DOCKER_HOST` dibaca di `config.Config.DockerHost`. Docker tak terjangkau → keluar 1 `Docker tidak bisa diakses: …` (diperiksa nyata). Kegagalan `Reconcile` hanya dicatat (log), tidak menghentikan panel.

## Pengujian

- **Label test integrasi dipisah.** `internal/docker` memakai label `runova.app=dkitest`/`runova.user=dkitest`, `internal/apps` memakai `itest`. Sebelumnya keduanya `itest`, dan karena `go test` menjalankan paket secara paralel, pembersihan test apps menghapus container test docker yang sedang berjalan (gejala: `stats` EOF, `409 not running`, keluaran exec kosong). Klien Docker tidak berubah; 6 putaran berurutan kedua test lulus sesudah pemisahan.

- **Folder data pada pengujian tanpa root**: proses dev bukan root, tidak punya CAP_CHOWN. Di test integrasi (`internal/apps/integration_test.go`) dan uji asap, `Service.Chown` diganti `os.Chmod(path, 0o777)`, sehingga container (UID `100000+id`) bisa menulis; file yang ditulis aplikasi memang berpemilik UID itu (diperiksa `stat`: `100001`). Produksi memakai `OSChown` + CAP_CHOWN dari unit systemd (Bagian 7).
- **`-race` pada `internal/api`** memakan ~190 detik (bcrypt di bawah race detector); tanpa `-race` ~25 detik. Itu sifat test Bagian 1, bukan tambahan Bagian 3.
- **Uji asap** memakai `testdata/hello/main.go` (binary Go statis; `/crash` → `os.Exit(1)`). Server dijalankan dengan pembungkus sementara yang hanya mengganti `Chown`; pembungkus dan binary dihapus sesudahnya.
