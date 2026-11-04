# Pembaruan Runova: notifikasi, `runova update`, `runova rollback` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admin melihat banner versi baru di panel, memperbarui server dengan `sudo runova update`, dan kembali ke versi sebelumnya (beserta database) dengan `sudo runova rollback`.

**Architecture:** Paket baru `internal/update` (perbandingan semver, pembaca redirect rilis terbaru, `Checker` berjadwal di memori) dipakai bersama oleh `runova serve` (banner lewat `GET /api/update`) dan perintah `runova update`. `update` mengunduh `get.sh` dari rilis target, mencocokkan checksum, lalu menjalankannya; semua logika pemasangan tetap di `get.sh` → `install.sh`. `install.sh` menyimpan binary lama ke `/var/lib/runova/rollback/`; `runova rollback` menukar binary dan memulihkan cadangan database migrasi yang sudah ada.

**Tech Stack:** Go stdlib + `internal/store`, bash (`install.sh`, bats), JS ES module tanpa build, Playwright, `test/system/run.sh`.

**Spec:** `docs/superpowers/specs/2026-11-04-update-rollback-design.md`

## Global Constraints

- Banner hanya admin, semua halaman panel di bawah topbar, tidak bisa ditutup. Teks: "Runova v0.3.0 tersedia (terpasang: v0.2.0). Perbarui di server: `sudo runova update`" + tombol salin + tautan "Catatan rilis".
- Pengecekan di backend (CSP `connect-src 'self'` tidak berubah): pertama 1 menit setelah start, lalu tiap 6 jam; hasil di memori; gagal → log debug, tanpa banner. Versi `dev` tidak pernah mengecek.
- Rilis terbaru = ikuti redirect `<GitHub>/<repo>/releases/latest` ke `/releases/tag/vX.Y.Z` (bukan `api.github.com`). Repo bawaan `aribrilliantsyah/runova`; `RUNOVA_REPO` dan `RUNOVA_GITHUB` mengganti.
- Setelan `update_check` bawaan `true`, termasuk DB lama yang belum punya kuncinya.
- `GET /api/update` (admin; pengguna biasa 403): `{"current","latest","update_available","release_url","checked_at"(ms),"enabled"}`. Panel memanggilnya saat dimuat dan tiap 30 menit.
- `runova update [--version X.Y.Z] [--check] [--yes]`: bukan root → keluar 1 "Jalankan dengan sudo: sudo runova update"; `--check` keluar 0 (terbaru) / 10 (ada pembaruan) / 1 (gagal), tanpa root; `--version` lebih lama ditolak dan diarahkan ke `runova rollback`; tanpa terminal wajib `--yes`; checksum `get.sh` dicocokkan dengan `SHA256SUMS` rilis target sebelum dijalankan; perintah akhir `bash get.sh --version X --yes`.
- Semver `X.Y.Z`; pra-rilis `-…` lebih rendah dari rilis yang sama; tag tak terurai → tidak ada pembaruan.
- `install.sh` menyalin binary terpasang ke `/var/lib/runova/rollback/runova` (0755, root) dan versinya ke `rollback/VERSION` hanya bila ada dan versinya berbeda; satu versi saja.
- `runova rollback [--yes]`: urutan `systemctl stop runova` → salin `runova.db` ke `runova.db.before-rollback-<unix>` → pulihkan `runova.db.bak-v<skema binary lama>-*` terbaru bila skema DB > skema binary lama (hapus `-wal`/`-shm`; `runova:runova` 0600) → tukar binary → `systemctl start runova` → `runova check`. Tanpa cadangan yang cocok → keluar 1 tanpa mengubah apa pun. Ringkasan wajib memuat peringatan bahwa data panel sejak pembaruan hilang.
- Pesan pengguna Bahasa Indonesia, log Inggris. Jangan push, jangan merge.

## Review Focus

- `GET /releases/latest` mengembalikan 200 tanpa redirect ke `/tag/v…`, atau tag `v1.2` / `latest` → bukan pembaruan, tanpa banner (Task 1).
- Server tanpa internet: `Fetch` error tidak menghapus hasil cek sebelumnya dan tidak membuat banner muncul (Task 1).
- Mengaktifkan lagi `update_check` tidak menunggu 6 jam (Task 2).
- `install.sh` dijalankan dua kali dengan versi sama tidak menimpa `rollback/` (Task 3).
- `runova update` ketika `get.sh` hilang di rilis atau `SHA256SUMS` tidak memuat baris `get.sh` → ditolak, `get.sh` tidak dijalankan (Task 4).
- `rollback` dua kali berturut-turut kembali maju ke versi semula, dan DB tidak pernah ditimpa bila cadangan tidak ada (Task 5).

---

### Task 1: Paket `internal/update`

**Files:**
- Create: `internal/update/version.go`, `internal/update/source.go`, `internal/update/checker.go`
- Test: `internal/update/version_test.go`, `internal/update/source_test.go`, `internal/update/checker_test.go`
- Modify: `internal/config/config.go` (+ `config_test.go`)

**Interfaces:**
- Produces:
  - `func Compare(a, b string) (int, bool)` — -1/0/1; `false` bila salah satu tidak terurai. Menerima awalan `v`.
  - `type Source struct{ GitHub, Repo string; Client *http.Client }`; `func (s Source) Latest(ctx) (string, error)` (versi tanpa `v`, tervalidasi `Compare`-parsable); `func (s Source) ReleaseURL(version string) string`; `func (s Source) AssetURL(version, name string) string`.
  - `type Latester interface{ Latest(context.Context) (string, error); ReleaseURL(string) string }`.
  - `type Status struct{ Current, Latest string; UpdateAvailable bool; ReleaseURL string; CheckedAt int64; Enabled bool }` (JSON: `current`, `latest`, `update_available`, `release_url`, `checked_at`, `enabled`).
  - `type Checker struct{ Current string; Source Latester; Enabled func(context.Context) bool; Now func() time.Time; After func(time.Duration) <-chan time.Time; FirstDelay, Interval time.Duration }`; `Run(ctx)`, `Check(ctx)`, `Poke()`, `Status(ctx) Status`. Nol-nilai: `FirstDelay` 1 menit, `Interval` 6 jam, `Now` `time.Now`, `After` `time.After`.
  - `config.Config` + `GitHub string` (`RUNOVA_GITHUB`, bawaan `https://github.com`) dan `Repo string` (`RUNOVA_REPO`, bawaan `aribrilliantsyah/runova`).

- [ ] **Step 1: Tulis uji gagal.** `TestCompare` (tabel): `0.3.0>0.2.0`, `0.10.0>0.9.0`, `1.0.0>1.0.0-rc.1`, `1.0.0-rc.2>1.0.0-rc.1`, `1.0.0-rc.1>1.0.0-alpha`, `v0.2.0==0.2.0`, tak terurai (`dev`, `1.2`, `latest`, `""`, `1.2.3.4`) → ok=false. `TestSourceLatest` dengan `httptest.Server` meniru GitHub: `/me/runova/releases/latest` → 302 `/me/runova/releases/tag/v0.3.0` (hasil `0.3.0`); 200 tanpa redirect → error; redirect ke `/tag/vbogus` → error; 500 → error; server mati → error. `TestSourceURLs`: `ReleaseURL("0.3.0") == GitHub+"/me/runova/releases/tag/v0.3.0"`, `AssetURL("0.3.0","get.sh") == …/releases/download/v0.3.0/get.sh`. `TestChecker`: sumber palsu + `After` palsu yang mencatat durasi → `Run` meminta 1 menit lalu 6 jam, `Status` setelah cek: `latest`, `update_available` true, `checked_at` = `Now`; sama / lebih lama / `Current:"dev"` → `update_available` false dan sumber tidak dipanggil untuk `dev`; `Enabled` false → sumber tidak dipanggil dan `Status.UpdateAvailable` false meski ada hasil lama; error sumber setelah sukses → `Latest` lama bertahan; `Poke` memicu cek di luar jadwal.
- [ ] **Step 2: Jalankan** `go test ./internal/update/` — gagal (paket belum ada).
- [ ] **Step 3: Implementasikan** tiga file di atas. `Source.Latest` memakai `HEAD` dan membaca `resp.Request.URL` akhir; timeout klien 15 detik bila `Client` nil; `Checker` menyimpan `latest`/`checkedAt` di bawah mutex; kegagalan dicatat `slog.Debug`. Tambah `GitHub`/`Repo` ke `config.Config` dengan uji `Load`.
- [ ] **Step 4: Jalankan** `go test -count=1 ./internal/update/ ./internal/config/` — lulus.
- [ ] **Step 5: Commit** `feat(update): version compare, release source and scheduled checker`

### Task 2: Setelan, API, dan pengabelan `serve`

**Files:**
- Modify: `internal/store/settings.go`, `internal/api/api.go`, `internal/api/settings.go`, `cmd/runova/main.go`, `cmd/runova/devbackends.go`
- Create: `internal/api/update.go`, `cmd/runova/updatecheck.go`
- Test: `internal/store/store_test.go`, `internal/api/update_test.go`, `cmd/runova/updatecheck_test.go`

**Interfaces:**
- Consumes: Task 1 (`update.Checker`, `update.Status`, `update.Source`).
- Produces: `store.Settings.UpdateCheck bool` (`json:"update_check"`, bawaan true); `api.Config.Update *update.Checker` (rute `GET /api/update` hanya terdaftar bila non-nil); `newUpdateChecker(cfg config.Config, st *store.Store) *update.Checker` di `cmd/runova`.

- [ ] **Step 1: Tulis uji gagal.** store: DB berisi JSON setelan lama tanpa `update_check` → `UpdateCheck == true`; simpan `false` → terbaca `false`. api: admin `GET /api/update` mengembalikan `{current:"0.2.0",latest:"0.3.0",update_available:true,release_url:".../releases/tag/v0.3.0",enabled:true}`; pengguna biasa 403; tanpa sesi 401; setelan `update_check=false` → `enabled:false,update_available:false`; `PUT /api/settings` yang membalik `update_check` false→true memicu `Poke` (sumber palsu dipanggil). cmd: `newUpdateChecker` dengan `FakeBackends` melaporkan versi lebih baru (`update_available` true) dan `FirstDelay` kecil; tanpa fake, `Current` = `version`.
- [ ] **Step 2: Jalankan** `go test ./internal/store ./internal/api ./cmd/runova -run 'Update|Settings'` — gagal.
- [ ] **Step 3: Implementasikan.** `Settings.UpdateCheck` + `defaultSettings`; `putSettings` membaca setelan lama dan memanggil `s.update.Poke()` bila `update_check` berubah ke true; handler `GET /api/update` admin → `Checker.Status`; `serve` membuat checker (`go checker.Run(ctx)`), mode fake memakai sumber palsu yang melapor `0.3.0` dengan `Current: "0.2.0"`.
- [ ] **Step 4: Jalankan** `go test -count=1 ./internal/store ./internal/api ./cmd/runova` — lulus.
- [ ] **Step 5: Commit** `feat(update): update_check setting, GET /api/update and background checker`

### Task 3: `install.sh` menyimpan binary lama

**Files:**
- Modify: `install.sh` (`step_binary`, variabel `ROLLBACK_DIR`)
- Test: `test/install.bats`

**Interfaces:**
- Produces: `$DATA_DIR/rollback/runova` (0755) dan `$DATA_DIR/rollback/VERSION` (isi `<versi>\n`); dipakai Task 5.

- [ ] **Step 1: Tulis uji gagal (bats).** `instalasi pertama tidak membuat rollback/`; `memperbarui ke versi lain menyimpan binary lama dan VERSION-nya` (stub versi lama mencetak `runova 0.1.0`, baru `0.2.0`; isi `rollback/runova` = stub lama, mode 0755); `memasang ulang versi yang sama tidak mengubah rollback/` (rollback sebelumnya tetap); `update kedua menimpa dengan versi yang baru ditinggalkan`.
- [ ] **Step 2: Jalankan** `test/bats.sh` — empat test baru gagal.
- [ ] **Step 3: Implementasikan** `save_rollback` yang dipanggil dari `step_binary` sebelum `mv -f "$tmp" "$BIN"`: lewati bila `$BIN` tak ada / `version`-nya gagal / sama dengan `$VERSION`; salin lewat berkas sementara + `mv`; gagal → `warn`, pemasangan lanjut.
- [ ] **Step 4: Jalankan** `test/bats.sh` — lulus (bats dan shellcheck).
- [ ] **Step 5: Commit** `feat(install): keep the previous binary in rollback/ on update`

### Task 4: `runova update`

**Files:**
- Create: `cmd/runova/update.go`, `cmd/runova/exec.go`
- Modify: `cmd/runova/main.go` (dispatch + `usage`)
- Test: `cmd/runova/update_test.go`

**Interfaces:**
- Consumes: Task 1 (`update.Source`, `Compare`), `config.Config.GitHub/Repo`.
- Produces: `type execFunc func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error` (`osExec` produksi); `type updateDeps struct{ IsRoot func() bool; Stdin io.Reader; Interactive bool; Exec execFunc; Version string }`; `func updateCmd(ctx context.Context, cfg config.Config, args []string, d updateDeps, stdout, stderr io.Writer) int`. Task 5 memakai `execFunc`.

- [ ] **Step 1: Tulis uji gagal** dengan `httptest.Server` meniru rilis (`/me/runova/releases/latest` → 302; `download/v0.3.0/get.sh` dan `SHA256SUMS`) dan `Exec` palsu yang merekam argumen serta isi skrip: `--check` → kode 0 (sama), 10 (ada pembaruan, stdout memuat dua versi), 1 (server mati); `--check` tanpa root jalan; tanpa root → 1 + "Jalankan dengan sudo: sudo runova update"; sudah terbaru → 0 + "Runova 0.2.0 sudah versi terbaru" dan `Exec` tidak dipanggil; `--version 0.1.0` saat 0.2.0 → 1, pesan menyebut `runova rollback`; `--version` tak valid → 1; tanpa terminal tanpa `--yes` → 1; terminal + jawaban `n` → 1 tanpa `Exec`, `y` → `Exec` dengan `bash <get.sh> --version 0.3.0 --yes`; `--yes` melewati tanya; ringkasan memuat versi lama → baru dan tautan catatan rilis; checksum `get.sh` salah → 1 dan `Exec` tidak dipanggil; `SHA256SUMS` tanpa baris `get.sh` → 1 serupa; `get.sh` 404 → 1; versi terpasang `dev` tanpa `--version` → 1.
- [ ] **Step 2: Jalankan** `go test ./cmd/runova -run Update` — gagal.
- [ ] **Step 3: Implementasikan** alur spec: urai flag (`flag.FlagSet`, `ContinueOnError`), `--check`, root, target, bandingkan, ringkasan + tanya `Lanjutkan? [y/N]`, unduh `get.sh` dan `SHA256SUMS` ke `os.MkdirTemp` (dihapus setelahnya), cocokkan SHA-256, `Exec(bash, get.sh, --version X, --yes)` dengan stdin/stdout/stderr terhubung; kode keluar `get.sh` diteruskan. `run` membuat `updateDeps` produksi (`os.Geteuid()==0`, `os.Stdin`, deteksi terminal lewat `os.ModeCharDevice`).
- [ ] **Step 4: Jalankan** `go test -count=1 ./cmd/runova` — lulus.
- [ ] **Step 5: Commit** `feat(cli): runova update`

### Task 5: `runova rollback`

**Files:**
- Create: `cmd/runova/rollback.go`
- Modify: `cmd/runova/main.go` (dispatch + `usage`)
- Test: `cmd/runova/rollback_test.go`

**Interfaces:**
- Consumes: `execFunc` (Task 4), `store.SchemaVersion`, polanya nama cadangan `runova.db.bak-v<N>-<unix>`.
- Produces: `type rollbackDeps struct{ IsRoot func() bool; Stdin io.Reader; Interactive bool; Exec execFunc; BinPath string; Chown func(path string) error; Now func() time.Time; Version string }`; `func rollbackCmd(ctx, cfg, args, d, stdout, stderr) int`.

- [ ] **Step 1: Tulis uji gagal** di folder data sementara dengan `Exec` palsu (`systemctl …` dicatat; `<rollback>/runova db version` mencetak skema canned; `<BinPath> check` mencetak `ok`): tanpa root → 1; tanpa `rollback/runova` → 1 "Tidak ada versi sebelumnya untuk dikembalikan"; skema sama → DB tak disentuh, tidak ada `before-rollback`, binary tertukar, urutan panggilan `stop` → `start` → `check`; skema DB > skema lama dengan cadangan `bak-v<lama>-*` (dua cadangan, yang terbaru dipilih) → `before-rollback-<unix>` berisi DB lama, `runova.db` = cadangan, `-wal`/`-shm` terhapus, mode 0600, ringkasan menyebut waktu cadangan dan peringatan data hilang, binary tertukar dan `rollback/runova` kini binary sebelum tukar dengan `VERSION` baru; skema berbeda tanpa cadangan → 1, tidak ada panggilan `systemctl`, DB dan binary utuh; tanpa terminal tanpa `--yes` → 1; jawaban `n` → 1 tanpa perubahan; dua rollback berturut-turut mengembalikan binary semula; `systemctl stop` gagal → 1 tanpa perubahan berkas.
- [ ] **Step 2: Jalankan** `go test ./cmd/runova -run Rollback` — gagal.
- [ ] **Step 3: Implementasikan** urutan spec; tukar binary dengan menyalin ke berkas sementara sefolder lalu `os.Rename` (folder `/usr/local/bin` dan `/var/lib/runova` bisa beda filesystem); DB dipulihkan lewat berkas sementara + rename; kegagalan sesudah `stop` mencoba `systemctl start runova` dan menyebut lokasi salinan `before-rollback`. `run` membuat `rollbackDeps` produksi (`BinPath` `/usr/local/bin/runova`, `Chown` ke user `runova`).
- [ ] **Step 4: Jalankan** `go test -count=1 ./cmd/runova` — lulus.
- [ ] **Step 5: Commit** `feat(cli): runova rollback`

### Task 6: Panel — banner, halaman Server, uji, screenshot

**Files:**
- Modify: `web/js/shell.js`, `web/js/pages/server.js`, `web/css/components.css`, `web/test/screenshots.mjs`, `docs/screenshots/`
- Create: `web/test/smoke-update.mjs`

**Interfaces:**
- Consumes: `GET /api/update`, `Settings.update_check` (Task 2).
- Produces: elemen `.banner.info.upd` (role `status`) dengan `code` perintah, tombol "Salin perintah", tautan "Catatan rilis"; event `runova:update` (window) yang memuat ulang banner; kartu "Versi Runova" dengan sakelar `aria-label="Periksa pembaruan"`.

- [ ] **Step 1: Tulis uji asap gagal** `smoke-update.mjs` (server palsu, `RUNOVA_FAKE_BACKENDS=1`): admin melihat `.upd` dengan "Runova v0.3.0 tersedia (terpasang: v0.2.0)" di `/apps`, `/users`, `/server`, `/deploy`; salin menaruh `sudo runova update` ke clipboard; tidak ada tombol tutup; tautan Catatan rilis mengarah ke `…/releases/tag/v0.3.0`; pengguna biasa tidak punya `.upd` dan tidak memanggil `/api/update`; sakelar "Periksa pembaruan" mati → banner hilang tanpa muat ulang, hidup → muncul; lebar 360/390/768/1280/1440 terang+gelap: `scrollWidth <= clientWidth`, tanpa galat konsol/permintaan keluar origin.
- [ ] **Step 2: Jalankan** `node web/test/smoke-update.mjs` — gagal.
- [ ] **Step 3: Implementasikan** banner di `buildShell` (admin saja, disisipkan antara `top` dan `content`; muat sekali + `setInterval` 30 menit dibersihkan di `destroy`; galat diam), kartu "Versi Runova" (terpasang, terbaru, waktu cek terakhir via `fmtTime`, sakelar) di halaman Server dengan `dispatchEvent(new Event("runova:update"))` setelah simpan, dan CSS `.upd` (flex-wrap supaya tidak scroll horizontal di 360 px).
- [ ] **Step 4: Jalankan** `node web/test/smoke-update.mjs` lalu semua `web/test/*.mjs` (`unit`, `smoke-deploy`, `smoke-app`, `smoke-admin`, `smoke-layout`, `smoke-lifecycle`, `smoke-hosting`) — lulus.
- [ ] **Step 5: Screenshot.** `screenshots.mjs` mematikan `update_check` di seed (screenshot lama tidak berubah), menyalakannya sebelum dua shot baru `desktop-update-banner.png` (1440 terang, `/apps`) dan `mobile-update-banner-dark.png` (390 gelap, `/apps`); jalankan, lihat kedua gambar, periksa tak ada perubahan pada PNG lain (`git status`).
- [ ] **Step 6: Commit** `feat(web): update banner and Server version card`

### Task 7: Uji sistem — update A→B lalu rollback

**Files:**
- Modify: `test/system/run.sh`
- Create: `test/system/relserver/main.go`

**Interfaces:**
- Produces: `relserver <dir> <addr> <latest>` — server rilis tiruan (file statis dari `<dir>`, `/<repo>/releases/latest` → 302 `/…/releases/tag/v<latest>`) untuk `RUNOVA_GITHUB`.

- [ ] **Step 1: Tulis skenario "7 runova update lalu rollback"** (setelah skenario 6): bangun rilis `0.0.3-systest` dengan `scripts/release.sh`, tambahkan `get.sh` ke `SHA256SUMS` seperti `release.yml`, taruh di `<dir>/me/runova/releases/download/v0.0.3-systest/`; kirim `relserver` terkompilasi ke container dan jalankan di latar; dengan `RUNOVA_GITHUB`/`RUNOVA_REPO` di container: `runova update --check` keluar 10; `runova update --yes` → `runova version` = `0.0.3-systest`, login admin berhasil, `/db` aplikasi tetap `rows=2`, `update --check` keluar 0, `rollback/VERSION` = `0.0.2-systest`; `runova rollback --yes` → `runova version` = `0.0.2-systest`, `runova check` lulus, login admin dan data utuh, `rollback/VERSION` = `0.0.3-systest`; tanpa `--yes` dan tanpa terminal ditolak.
- [ ] **Step 2: Jalankan** `test/system/run.sh` — semua skenario lulus (ubah ringkasan akhir menjadi "Semua 8 skenario lulus.").
- [ ] **Step 3: Commit** `test(system): update then rollback against a local release server`

### Task 8: Dokumentasi, notes, dan pemeriksaan akhir

**Files:**
- Modify: `README.md`, `docs/install-ubuntu.md`, `docs/superpowers/notes/db-migrations.md` (satu baris: dipakai `rollback`), `cmd/runova/main.go` (`usage` bila belum)
- Create: `docs/superpowers/notes/update-rollback.md`

- [ ] **Step 1: Perbarui** README bagian Pembaruan (`sudo runova update` jadi cara utama; `curl` tetap untuk instalasi pertama dan server ≤ 0.2.0; `--check`, `--version`, `--yes`; setelan "Periksa pembaruan" dan variabel `RUNOVA_REPO`/`RUNOVA_GITHUB`) dan sub-bagian "Kembali ke versi sebelumnya"; `docs/install-ubuntu.md` §7 sama; tambah `smoke-update.mjs` ke daftar uji web dan screenshot baru ke README.
- [ ] **Step 2: Tulis** notes (keputusan di luar spec, mis. `--yes` selalu diteruskan ke `get.sh`, `Poke`, `rollback` bergantung pada `db version` binary lama).
- [ ] **Step 3: Jalankan semua bukti:** `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...`, `CGO_ENABLED=0 go build ./...`, `test/bats.sh`, semua `web/test/*.mjs`, `test/system/run.sh`, `test/e2e/stack.sh up && test/e2e/run.sh && test/e2e/stack.sh down`.
- [ ] **Step 4: Commit** `docs: update and rollback`; `git status` bersih.
