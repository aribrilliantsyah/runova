# Ikon aplikasi: keputusan dan deviasi

## Deteksi bahasa (`internal/artifact/language.go`)

1. **Binary.** `.go.buildinfo` / `.note.go.buildid` → Go (binary Go yang di-strip tetap punya section ini). Selain itu satu kali pindai aliran byte mencari penanda, urutan prioritas: Go (`\xff Go buildinf:`), Rust (`rustc version`, `/rustc/`), Zig, .NET (`System.Private.CoreLib`, `DOTNET_gcServer`), C++ (`GLIBCXX`, `libstdc++`), C (`GCC: (` / `clang version` di `.comment`). Dependensi dinamis `libstdc++*` juga dihitung C++. Urutan itu perlu karena binary membawa string toolchain lain (Rust membawa `.comment` GCC dari libgcc, C++ juga).
2. **Zig tanpa string penanda.** Build `ReleaseSmall`/`ReleaseFast -fstrip` membuang semua string Zig (`zig/std/` hanya ada di build debug; `ZIG_PROGRESS` ada bila program memakai `std.debug.print`). Yang tersisa hanya `.comment` berisi `Linker: LLD 21.x` tanpa baris kompiler. Rencana tidak menyebutnya; dipakai sebagai penanda Zig **setelah** C/C++ dicek (C yang di-link LLD tetap punya baris `GCC:`/`clang`). Diuji dengan binary Zig 0.16 sungguhan: debug, `ReleaseSmall`, `ReleaseFast`, program kosong, semua terdeteksi Zig. Risiko: binary lain yang hanya ditandai LLD (mis. Rust dengan `-C strip` tetap punya `rustc version`, jadi aman) akan dianggap Zig; dianggap dapat diterima.
3. **Penanda .NET** tidak diuji dengan binary NativeAOT sungguhan (tidak ada SDK .NET di mesin ini); hanya dengan ELF sintetis.
4. **Penanda sepanjang batas chunk.** Pemindai menyimpan ekor `maxLen-1` byte antar chunk 1 MiB; ada test khusus.
5. **JAR.** Kotlin: `kotlin/` atau `META-INF/<nama>.kotlin_module` langsung di bawah `META-INF/` (tidak di `META-INF/versions/...`); Scala: `scala/` atau jar `scala-library-*`/`scala3-library_3-*` di `BOOT-INF/lib`/`WEB-INF/lib`; Groovy: `groovy/` atau `org/codehaus/groovy/`; Clojure: `clojure/` atau jar `clojure-1.*`. Prioritas Clojure > Scala > Groovy > Kotlin > Java. **Kotlin dan Groovy tidak dihitung dari jar dalam `BOOT-INF/lib`**: banyak pustaka Java (OkHttp, Spring) membawa kotlin-stdlib, jadi aplikasi Java murni akan salah dikenali. Hanya kelas aplikasi sendiri yang menentukan. Scala/Clojure tetap dihitung dari jar runtime-nya karena program Java tidak punya alasan memakainya.
6. `Report.Language` memakai nilai `java kotlin scala groovy clojure go rust zig cpp c dotnet`; `""` = tidak dikenal. Laporan lama di database (tanpa field) terbaca sebagai `""`: panel menampilkan Java untuk JAR dan ikon netral untuk binary, tanpa migrasi.

## API

`GET /api/apps` → `summary[id].language` dari laporan deployment aktif. `GET /api/apps/{id}` tidak perlu diubah: `deployment.report.language` sudah ikut karena `Report` diserialisasi utuh.

## Panel

1. `kindLogo(kind, language)`; SVG Devicon `-original` (v2.16/2.18, identik kecuali `c-original.svg`, dipakai versi 2.16) di `web/vendor/devicon/`. `linux-original.svg` (Tux) dihapus, tidak ada pemakai lagi. Lisensi MIT Devicon sudah ada di `web/vendor/licenses/devicon.txt`.
2. Dipilih `dotnetcore-original` (bukan `csharp`): lebih mudah dikenali di 18 px dan cocok dengan penanda NativeAOT.
3. **Rust** hitam di atas transparan → kelas `inv` yang sudah ada di `base.css` (`filter: invert(1)` di tema gelap). Dicek di tangkapan layar gelap. Logo lain tidak perlu penyesuaian (Go dan Zig berwarna, Groovy punya latar terang sendiri dan terbaca).
4. **Tanpa bahasa dikenal:** ikon Lucide `binary` (statis) atau `box` (glibc, ditambahkan ke `web/icons.txt`, sprite digenerasi ulang dengan `go run ./cmd/genicons`), kelas `.neutral` berwarna `--muted`. JAR tanpa bahasa = Java.
5. Ukuran: `.lgbox` 28 px → logo 18 px; header 34 px → 22 px (sebelumnya 20); `object-fit: contain`. Kotak 34 px di uploader kini kelas `.lgbox.lg` (sebelumnya gaya inline) agar ukuran logonya ikut aturan header.
6. Subjudul kartu dan header: `runtimeSub(app, language)` → "Go · Binary statis", "Kotlin · Java 21"; JAR Java tetap "Java 21" (tidak "Java · Java 21"). `runtimeLabel` tidak berubah.
7. Mobile: crumb menyembunyikan semua `svg.lucide` kecuali `.neutral`, supaya ikon netral di breadcrumb tetap tampil di HP.

## Uji

- Go: `language_test.go` (penanda per bahasa, batas chunk, binary Go sungguhan dibangun dengan `go build` default dan `-s -w`, JAR per bahasa); `TestAppLanguageInSummaryAndDetail` di `internal/api`.
- Rust/C++/Zig/.NET di test Go memakai ELF sintetis (`buildELF`) sesuai brief. Binary asli (Go, Rust 1.90, Zig 0.16, gcc/g++ dinamis dan statis) dipindai manual, semua benar.
- JS: tiga test baru di `web/test/dom.test.html` (`kindLogo`, fallback tanpa Tux, `runtimeSub`); `smoke-app.mjs` memeriksa logo header/crumb/subjudul untuk binary Go.
- `web/test/lib.mjs`: `buildMarkedBinary` (ELF statis dengan string penanda) dan opsi `extraFiles` di `buildSampleJar`. `screenshots.mjs` sekarang memuat aplikasi Go, Java, Kotlin (JAR), Rust, dan binary tak dikenal.

## Tidak dikerjakan

- `RUNOVA_FAKE_BACKENDS` tidak diubah: bahasa datang dari upload yang sebenarnya diperiksa, jadi data variasi cukup dari berkas yang diunggah skrip tangkapan layar.
- Preview (`preview-dashboard.html`) hanya memakai Java dan PostgreSQL; tidak diubah.
