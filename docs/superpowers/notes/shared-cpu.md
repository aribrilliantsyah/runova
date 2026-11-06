# CPU dipakai bersama

Permintaan: CPU satu sistem untuk semua aplikasi; hanya RAM dan disk yang dibagi. Kartu disk Server tidak boleh mencampur disk server dan disk aplikasi dalam satu kalimat.

## Keputusan

**Tanpa batas CPU, bobot bawaan.** Container dibuat tanpa `NanoCpus`, `CpuQuota`, `CpuPeriod`, dan tanpa `CpuShares` eksplisit. Bobot bawaan Docker sama untuk semua container (cgroup v2: `cpu.weight` 100, diuji di host ini dengan `--cpu-shares 0` dan `1024`, keduanya 100), jadi saat CPU penuh tiap aplikasi mendapat bagian setara. Menulis `CpuShares: 1024` eksplisit tidak menambah apa-apa dan hanya menambah satu angka yang harus dijaga. Panel Runova tidak di-container, jadi tidak ikut bersaing di bobot container. `checkSpec` hanya mewajibkan user non-root, batas memori, dan root fs read-only.

**Container lama dilepas di tempat saat start.** `Service.LiftCPULimits` (dipanggil `go` di `cmd/runova/main.go`, file baru `internal/apps/sharedcpu.go` supaya tidak menyentuh `monitor.go`/Reconcile yang sedang diubah di branch lain) memanggil `POST /containers/{id}/update` untuk setiap container berlabel `runova.app`. Body-nya `{"CpuQuota": -1}`, bukan `{"NanoCpus": 0}`: diuji pada Docker 29.8.2, `NanoCpus: 0` diabaikan (berarti "tidak berubah", `cpu.max` tetap `50000 100000`), sedangkan `CpuQuota: -1` membuat `cpu.max` menjadi `max 100000` seketika dan tetap setelah `docker restart` maupun stop/start. `HostConfig.NanoCpus` di inspect tetap menampilkan nilai lama; yang menentukan adalah cgroup. Idempoten, tidak me-restart, gagal hanya dicatat (container itu lepas saat deploy berikutnya).

**Health wait JAR rata 3 menit.** Dulu 90 s ÷ core (min 3, maks 15 menit). Tanpa core per aplikasi, kecepatan JVM bergantung pada beban server, bukan jatahnya. Pengukuran lama (Spring Boot + JPA: 57 s di 0,5 core, 70 s di samping JVM kedua) muat di 3 menit; perpanjangan sampai 2× (6 menit) selama log bergerak tetap ada untuk server yang sibuk, dan deteksi restart/OOM tidak berubah. Hint health timeout tidak lagi menyuruh "naikkan CPU", tetapi menyebut CPU dipakai bersama.

**CPU di halaman Server = beban host.** Belum ada sampler CPU host; membaca `/proc/stat` dua kali di dalam request menambah latensi. Dipakai `/proc/loadavg` (1/5/15 menit) terhadap `runtime.NumCPU()`: angka utama beban 1 menit, bar = beban ÷ core, tooltip menjelaskan ketiga angka. Bila tidak terbaca, kartu menampilkan jumlah core saja.

**Grafik CPU aplikasi terhadap seluruh core.** `docker.Stats` kini membawa `online_cpus`; batas grafik = `online_cpus × 100`.

**Migrasi 0006.** `ALTER TABLE … DROP COLUMN` pada `users.quota_cpu_milli` dan `apps.cpu_milli`. modernc.org/sqlite v1.61 membawa SQLite ≥ 3.35; kedua kolom tidak diindeks, tidak di CHECK/FK, jadi DROP COLUMN sah tanpa rebuild tabel. Cadangan otomatis `runova.db.bak-v5-*` dibuat oleh runner yang sudah ada. JSON setelan lama yang berisi `default_quota.cpu_milli` tetap terbaca (field tak dikenal diabaikan).

## Istilah

Satu kata untuk tiap konsep, di semua halaman:

- **terpakai**: yang dipakai host (RAM, disk filesystem).
- **dibagikan**: memori/disk yang diberikan ke aplikasi, dari kuota pengguna atau dari server.
- **terisi**: isi folder kerja aplikasi yang terukur.

"Dialokasikan"/"alokasi" dihapus dari UI.

## Format ukuran

`fmtMB` (ukuran yang diatur) memakai paling banyak satu desimal untuk GB. `fmtSize(bytes)` untuk ukuran terukur di samping MB/GB: `< 1 MB` di bawah 1 MB, MB bulat, GB selalu satu desimal (`8,0 GB`). `fmtBytes` tetap untuk ukuran file di tab File.

## Halaman Server

Grid 3×2 di desktop: baris atas kapasitas server (CPU server, Memori server, Disk server), baris bawah yang dibagikan ke aplikasi (Aplikasi, Memori aplikasi, Disk aplikasi), tiap kartu aplikasi tepat di bawah kartu server yang sesuai. Di ponsel 2 kolom: kartu server di kiri, kartu aplikasinya di kanan. Peringatan Disk aplikasi: sisa yang masih boleh diisi aplikasi (`dibagikan − terisi`) > sisa disk server. Peringatan Memori aplikasi: dibagikan > total memori server.
