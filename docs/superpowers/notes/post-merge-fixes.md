# Perbaikan pasca-merge (temuan review Bagian 2–3)

Delapan butir "Perlu diperbaiki setelah merge Bagian 4/5" di `part23-review.md`. Nama dan signature di blok Interfaces tidak diubah; semua tambahan berikut hanya menambah.

## Tambahan antarmuka

- `docker.Engine.RemoveNetwork(ctx, name)` (tidak ada → nil). `dockertest.Fake` mencatat `network rm <nama>` dan punya `RemoveNetworkErr`; `Fake.Stats` kini mencatat `stats <nama>`.
- `apps.Service.DeleteAllForUser(ctx, actor, owner)`, `Lock(ctx, user, appID)`, `DeployLocked(...)`, bidang `HealthReadWait`.
- `store.Settings.MaxPendingUploads` (JSON `max_pending_uploads`, bawaan 5, 1–50), `store.CountUploads`.
- `api.SweepUploadDirs(dataDir, st, now)`, `api.Config.Deploys *sync.WaitGroup`.

## Keputusan dan penyimpangan dari rencana

1. **Hapus pengguna.** Urutan di `deleteUser`: `DeleteAllForUser` (hentikan dan hapus semua aplikasi, lalu `artifacts/<user>`, `data/<user>`, jaringan) → `dbs.OnUserDeleted` → `store.DeleteUser` → `ApplyCaddy`. Gagal di langkah pertama → respons error, baris pengguna tetap, bisa diulang. Aplikasi yang sedang deploy membuatnya gagal dengan 409 (`ErrBusy`). `users.id` kini `AUTOINCREMENT` (skema diubah langsung, spec §11; database lama perlu dibuat ulang).
   `OnUserDeleted` dijalankan sebelum baris dihapus, bukan sesudah seperti dulu: role PostgreSQL gagal dihapus tetap hanya memberi notifikasi admin, tidak menggagalkan permintaan.
2. **Cek sehat.** `waitHealthy` kini menerima ID container. Tiap putaran: `InspectContainer` (tidak jalan → gagal segera, pesan memuat exit code), lalu dial dan baca 1 byte dengan batas 300 ms (`HealthReadWait`); EOF/reset berarti belum sehat, timeout atau data berarti sehat. Test unit yang memakai pipe tertutup diganti `silentConn()`.
3. **Batas unggahan.** Dicek sebelum body dibaca; 409 "Hapus unggahan lama dulu (maksimal N)". Pengguna lain tidak terpengaruh.
4. **Sapuan `tmp/*`.** Dijalankan dari `cleanupLoop` (start dan tiap jam). Folder tanpa baris `uploads` dan `ModTime` > 24 jam dihapus.
5. **Deploy latar.** `deployInBackground` ditandai di `WaitGroup`; `main` menunggu ≤ 30 s setelah `srv.Shutdown`. `redeploy`, `rollback`, dan `newVersion` memanggil `apps.Lock` sebelum `Prepare*`/`NewVersion` dan melepasnya setelah deploy latar selesai (atau bila persiapan gagal), jadi klien mendapat 409 dan tidak ada deployment `pending` sisa. `newVersion` ikut diperbaiki karena celahnya sama (dan ia menghabiskan unggahan).
6. **Reconnect Monitor.** `Monitor` subscribe lebih dulu, baru `Reconcile`, pada setiap sambungan; `main` tidak lagi memanggil `Reconcile` sendiri. Agar subscribe benar-benar mendahului, `docker.Client.Events` kini menunggu Engine menjawab sebelum kembali. Karena `Reconcile` sekarang berjalan berdampingan dengan deploy, ia mengambil kunci tiap aplikasi (aplikasi yang sibuk dilewati beserta container-nya) dan membaca daftar container sebelum daftar aplikasi.
7. **`HostConfig.Init: true`.** Diverifikasi di Docker 29.8.2 lokal: PID 1 `/sbin/docker-init -- sleep 300`, stop program tanpa handler SIGTERM 251 ms. **Belum diverifikasi di Ubuntu 26.04**: tidak ada mesin itu di sini; paket `docker.io` di sana harus menyertakan `/usr/bin/docker-init` (instalasi tanpa itu gagal saat membuat container).
8. **Cache `Stats`.** 3 s per aplikasi di `GET /api/apps/{id}`, hanya untuk aplikasi berstatus `running`; kegagalan dan "tidak berjalan" tidak di-cache.

## Belum teratasi

- Test integrasi `TestIntegrationWrongInternalPortFails` menunggu `HealthTimeout` penuh (5 s di test) karena container wrong-port tetap hidup; hanya container yang exit yang gagal segera.
