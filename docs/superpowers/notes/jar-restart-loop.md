# JAR start ulang berulang tanpa ketahuan + log viewer

Branch `fix/jar-restart-loop-logs` (dari `main` = v0.3.11). Brief: `.brief-restart-loop.md`.

## Laporan

Produksi v0.3.11, Spring Boot 4.1.2 + Hibernate 7.4 (`starboom`, Java 21). Setelah update JAR, log container menunjukkan aplikasi start dari awal kira-kira tiap 24 detik tanpa error atau stack trace, tepat setelah `HHH000001: Hibernate ORM core version …`. Timeline deploy tetap "Menunggu port 8080: 2 menit 55 detik dari paling lama 3 menit, percobaan 345".

## Akar masalah (terbukti)

Dua hal bertumpuk:

1. **Kernel OOM killer mematikan JVM saat start.** Diukur dengan JAR Spring Boot 4.1.2 + JPA + PostgreSQL yang sama dengan riset health wait (`/tmp/runova-health/starboom-jpa`), di bawah batas container Runova (`--memory` = `--memory-swap`, rootfs read-only, `/tmp` tmpfs, `--init`, pids 512, `--restart on-failure`), 1 core, skrip `/tmp/runova-oom/measure*.sh`:

   | memori | opsi JVM | hasil |
   |---|---|---|
   | 160 MB | bawaan | tidak pernah siap, 8 restart dalam 150 s, 9 banner di log |
   | 192 MB | bawaan | tidak pernah siap; `docker events`: `oom` + `die exitCode=137` 4× dalam 100 s |
   | 224 / 256 / 384 / 512 MB | bawaan | siap dalam 24–28 s, 0 restart; RSS setelah start 200–230 MB |
   | 192 MB | `-XX:MaxRAMPercentage=40` / `-XX:MaxMetaspaceSize=128m` / `-Xss512k` / `-XX:ReservedCodeCacheSize=32m` | tetap OOM loop |
   | 192 MB | `-XX:MaxRAMPercentage=60 -XX:+ExitOnOutOfMemoryError` | tetap OOM loop (6 restart) |
   | 192 MB | `-XX:TieredStopAtLevel=1` | siap 16 s, 0 restart |
   | 160 MB | kombinasi pangkas penuh (SerialGC, heap 50 %, code cache 32m, metaspace 128m, Xss512k, C1) | tetap OOM loop |

   Log terakhir sebelum mati sama persis dengan laporan (`HHH000001`, lalu pool `DataSourceConnectionProvider`), tanpa pesan apa pun: kernel membunuh proses, JVM tidak sempat menulis. Yang membengkak adalah memori native JVM (compiler C2, metaspace, thread), bukan heap: membatasi heap tidak menolong.

2. **Runova tidak melihat kematian itu.** `waitHealthy` hanya memeriksa `State.Running`. Dengan restart policy `on-failure`, Docker menyalakan container lagi dalam ~1 detik. Diamati dengan `docker inspect` tiap 250 ms pada container yang OOM berulang: hanya sekejap terlihat `Status=restarting, OOMKilled=true, ExitCode=137` (dan `Running` tetap `true` di status itu); sisanya `running, OOMKilled=false, ExitCode=0` dengan `StartedAt` baru dan `RestartCount` naik. Inspect tiap 500 ms hampir selalu jatuh di status "running", jadi health wait terus menunggu sampai batas 3 menit.

   Monitor crash loop: event `die` selama app `deploying` hanya mengonsumsi catatan stop (app belum `Up()`), jadi tidak menghitung. Setelah deploy aktif, monitor menghitung `die` dengan benar (`TestCrashLoopAfterDeployNamesOOM` lulus tanpa perubahan); sesudah Restart/Start yang gagal, app `failed` dan monitor mengabaikannya, sehingga loop berjalan terus di latar.

Bukti tambahan: `docker start` pada container bekas loop mereset `RestartCount` ke 0 (diuji), jadi `RestartCount > 0` selama health wait berarti restart sejak start Runova.

## Perbaikan backend

- `waitHealthy` (`internal/apps/deploy.go`): catat state inspect pertama; restart terdeteksi bila `RestartCount > 0`, `Status == "restarting"`, `OOMKilled`, atau `StartedAt` berubah → gagal SEGERA. Event `die`/`oom` container itu dikumpulkan selama menunggu (`watchDeaths`, filter label app) karena state sudah direset begitu container jalan lagi; `restartError` menunggu event `die` paling lama 1 detik untuk exit code. `stepError{restarted: true, state}` → `diagnose` → `exitProblem(…, restarted)`.
- Diagnosis: OOM → "Memori habis (OOMKilled)" + hint (untuk JAR: naikkan memori, 192 MB tidak cukup untuk Spring+JPA, heap bukan masalahnya, `-XX:TieredStopAtLevel=1` bila harus kecil). Bukan OOM → "Aplikasi berhenti dan dinyalakan ulang (exit code N)". Juga baru: `java.lang.OutOfMemoryError` di log → "Memori Java habis (OutOfMemoryError)".
- Alur gagal deploy tetap: container baru dihapus, log disimpan, versi lama (zero-downtime) tetap melayani, notifikasi.
- Restart/Start (`awaitStart`): restart terdeteksi → app `failed` dengan diagnosis, dan container **dihentikan** (sebelumnya dibiarkan loop tanpa batas karena monitor mengabaikan app `failed`). Log tetap bisa dibaca.
- WebSocket log (`GET /api/apps/{id}/logs`) mengirim `{"type":"start","started_at":…}` lebih dulu; `apps.Logs` mengembalikan `StartedAt`.

Test (merah sebelum perbaikan, semua di `internal/apps/restartloop_test.go`): `TestDeployFailsAtOnceWhenContainerRestarts` (fake: RestartCount naik + event oom/die sementara Running tetap true), `TestDeployRestartNamesExitCode`, `TestDeployRestartSeenWithoutEvents` (hanya StartedAt berubah), `TestDeployRestartingStateIsOOM`, `TestRestartThatLoopsFailsAndStops`; `TestCrashLoopAfterDeployNamesOOM` mengunci jalur monitor. Docker nyata: `TestIntegrationOOMRestartLoopFailsDeploy` (binary Go yang mengalokasi melewati 128 MB di bawah `on-failure`): sebelum perbaikan `deploy took 1m1.2s` (timeout), sesudah gagal dalam 3,6 s dengan "Memori habis (OOMKilled) / … (exit code 137, OOMKilled)".

## Keputusan: tidak ada default `JAVA_TOOL_OPTIONS`

Brief meminta default aman hanya "bila terbukti perlu". Angka di atas menunjukkan tidak:

- Di 256 MB (preset Kecil) dan 512 MB (preset Sedang, bawaan JAR) Spring Boot + JPA start bersih tanpa opsi apa pun. JVM 21 sudah membaca batas cgroup.
- Di batas yang benar-benar terlalu kecil, `MaxRAMPercentage`/`ExitOnOutOfMemoryError` (yang disarankan brief) tidak mencegah OOM: kematiannya native, bukan heap. Satu-satunya yang menyelamatkan 192 MB adalah `-XX:TieredStopAtLevel=1`, yang menurunkan kinerja puncak secara permanen: bukan default yang pantas untuk semua app.
- Yang memang hilang adalah diagnosis. Sekarang muncul dalam ~detik dengan saran yang terukur (naikkan memori; atau C1-only lewat Opsi JVM yang sudah ada). User tetap pegang kendali di Opsi JVM.

## Log viewer

Satu komponen (`web/js/components/logview.js`) di tab Log dan detail deployment; data di `logmodel.js`, parsing di `logparse.js` (fungsi murni, unit test di `web/test/logview.test.html`).

- **Halus**: hanya baris yang terlihat (+400 px) ada di DOM; tinggi baris diukur dan dikoreksi tanpa menggeser yang sedang dibaca; baris baru diterapkan sekali per animation frame; buffer 20.000 baris, yang tertua dibuang dengan baris penanda.
- **Follow**: menempel di bawah selama user di bawah (toleransi 24 px). Scroll ke atas menghentikan follow; tombol "N baris baru" kembali ke ekor (juga tombol End). Tombol Jeda/Lanjut dihapus: scroll ke atas sudah menjeda tanpa kehilangan baris.
- **Timestamp**: satu kolom rata (waktu terima Docker, jam pengguna, milidetik), bisa disembunyikan (diingat di `localStorage` `runova.logview`). Timestamp milik aplikasi di awal baris (Spring/logback/log4j/logrus/zap/slog, JSON `time`) dipotong dari teks dan ditaruh di tooltip. Keputusan: potong dari teks, bukan sembunyikan kolom Docker, karena kolom tetap rata untuk baris tanpa timestamp (banner, stack trace, stderr) dan semua baris memakai zona waktu yang sama. Baris kosong tidak diberi waktu.
- **Level**: badge ERR/WRN/INF/DBG/TRC, gutter tipis untuk WARN/ERROR, latar halus untuk ERROR; level dibaca dari kata level setelah timestamp, `level=…`, JSON (`level`/`severity`/angka pino), `panic:`. Filter per level dengan jumlah.
- **Cari**: highlight semua, Enter/Shift+Enter, jumlah "3/120", "Hanya cocok"; kecocokan di dalam stack trace terlipat membukanya.
- **Stack trace**: `\tat …`, `Caused by:`, `... N more`, frame Python/Go dilipat di bawah baris induknya ("N baris stack trace"); header exception tetap terlihat.
- **Banner/whitespace**: `white-space: pre`, wrap bisa dimatikan.
- **Penanda restart**: "Container dimulai ulang 23:12:25" dari pesan `start` WebSocket (StartedAt berubah saat sambung ulang; Docker mengakhiri stream log ketika container mati, termasuk saat restart policy menyalakannya), atau dari baris Spring `Starting … using Java N` kedua (penanda diletakkan sebelum banner run baru). Stream menyambung ulang dengan riwayat dan melewati baris yang sudah tampil.
- **Aksi**: klik nomor baris memilih (Shift = rentang, Ctrl = tambah), salin baris terpilih / semua, unduh .log, bersihkan tampilan, ukuran huruf 11–14 px; ANSI SGR (16 warna, 256 warna untuk 16 pertama, bold/dim/italic/underline) jadi warna palet panel.
- **Mobile**: toolbar cari + level + menu (ikon), menu sebagai sheet bawah, kolom waktu disembunyikan.
- **Aksesibilitas**: `role="log"`, `aria-live="off"` di badan; ringkasan "N baris baru, M error" di region polite paling sering tiap 5 detik.

## Dev

Fake backend: `RUNOVA_FAKE_FAIL=restartloop` (deploy yang OOM-loop saat start) dan `restartlive` (loop setelah deploy sehat), `LOG_FLOOD=n` (n baris cepat). Fake Docker kini mengalirkan baris baru saat follow dan mengakhiri stream ketika container berhenti atau start ulang, seperti Docker.
