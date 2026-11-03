# Domain Hosting Bersama (Wildcard) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admin mendaftarkan domain wildcard; setiap pengguna memberi aplikasinya alamat `<label>.<domain terdaftar>` dalam kuota aplikasinya, tanpa mengurus DNS.

**Architecture:** Tabel `hosting_domains` + dua kolom baru di `domains` (`hosting_domain_id`, `label`) lewat migrasi `0002`. Aturan nama murni di `internal/apps/hostnames.go`, pengecekan DNS di paket kecil `internal/hostdns` (resolver disuntik). `apps.Service` mengelola siklus hidup domain hosting; `CaddyState` melewatkan subdomain milik domain terdaftar yang mati, sehingga bentuk konfigurasi Caddy tidak berubah. Panel: kartu "Domain hosting" di Server dan komponen "Alamat" bersama untuk wizard langkah 4 dan tab Domain.

**Tech Stack:** Go stdlib, SQLite (modernc), `golang.org/x/net/publicsuffix`, JS ES module tanpa build, Playwright, bash e2e.

**Spec:** `docs/superpowers/specs/2026-11-03-hosting-domains-design.md`

## Global Constraints

- Migrasi: file baru `internal/store/migrations/0002_hosting_domains.sql`; `0001` tidak diubah; tanpa `BEGIN/COMMIT/PRAGMA`.
- `domains.host` tetap `UNIQUE` dan berisi nama lengkap; `hosting_domain_id` `ON DELETE RESTRICT`.
- Label pengguna `^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$` (3–40), tanpa `--` di posisi 3–4.
- Label cadangan bawaan: `www api mail smtp ftp admin panel runova ns1 ns2 localhost` + `reserved` per domain + label paling kiri domain panel bila domain panel tepat satu label di bawah domain terdaftar.
- Kuota subdomain = `Quota.Apps` pemilik aplikasi, dihitung atas semua subdomain hosting miliknya (termasuk yang domain dasarnya mati). Batas 5 domain per aplikasi tetap (gabungan).
- Domain bebas (`{host}`) hanya admin; pengguna biasa → 403 "Domain sendiri hanya bisa ditambahkan admin". Domain bebas lama pengguna biasa tetap jalan.
- Domain terdaftar: `caddy.ValidateDomain`, tidak sama dengan domain panel, tidak di bawah / menaungi domain terdaftar lain. `*.` di depan dibuang.
- Cek DNS: resolver sistem, timeout 5 detik, host `runova-check-<6 hex>.<domain>`; `ok` bila ada A/AAAA = `Settings.PublicIP` (kosong → IP non-loopback lokal server). Rentang Cloudflare tertanam (diambil 2026-11-03 dari `https://www.cloudflare.com/ips-v4` dan `ips-v6`).
- Peringatan Let's Encrypt: subdomain dibuat 7 hari terakhir, dikelompokkan per eTLD+1 (+1 bila domain panel berada di grup itu); kuning ≥ 40, merah ≥ 50; diberi label "perkiraan".
- Aktivitas: `hosting_domain.add|update|remove`; subdomain tetap `domain.add`/`domain.remove`.
- Pesan pengguna Bahasa Indonesia, log Inggris. Timestamp `created_at` dalam milidetik (seperti kolom lain).
- Caddy produksi 2.6.2; tidak ada perubahan bentuk konfigurasi Caddy.

## Review Focus

- Dua pengguna berebut label yang sama / label sama dengan domain bebas admin yang sudah ada → satu menang, lain 409 "Alamat sudah dipakai" (Task 1, 3).
- Menghapus aplikasi membebaskan kuota subdomainnya; menurunkan `Quota.Apps` di bawah pemakaian tidak menghapus apa pun, hanya menolak tambahan (Task 1, 3).
- Domain terdaftar dimatikan lalu dinyalakan: route hilang lalu kembali, status sertifikat subdomain tidak berubah jadi `error` selama mati (Task 3).
- Resolver DNS menggantung → cek berhenti di 5 detik dengan status `error`, tidak menahan request admin lebih lama (Task 2).
- Label cadangan tidak bisa dilewati lewat huruf besar, spasi, titik, atau bentuk punycode `xn--` (Task 2).
- Migrasi 0002 pada DB v1 berisi aplikasi dan domain bebas milik pengguna biasa → data utuh (Task 1).

---

### Task 1: Migrasi dan store

**Files:**
- Create: `internal/store/migrations/0002_hosting_domains.sql`, `internal/store/hosting.go`, `internal/store/hosting_test.go`
- Modify: `internal/store/domains.go`, `internal/store/activity.go` (tiga konstanta `ActHostingAdd = "hosting_domain.add"`, `ActHostingUpdate`, `ActHostingRemove`), `internal/store/migrate_test.go`

**Interfaces:**
- Produces:
  - `type HostingDomain struct{ ID int64; Name string; Enabled bool; Reserved string; DNSStatus string; DNSDetail string; DNSCheckedAt, CreatedAt int64 }` (JSON `id,name,enabled,reserved,dns_status,dns_detail,dns_checked_at,created_at`); konstanta `DNSUnchecked="unchecked"`, `DNSOK="ok"`, `DNSError="error"`.
  - `Domain` mendapat `HostingDomainID *int64 \`json:"hosting_domain_id"\`` dan `Label string \`json:"label"\``.
  - `func (s *Store) AddHostingDomain(ctx, name, reserved string, now int64) (HostingDomain, error)` — `ErrConflict` bila nama ada.
  - `HostingDomains(ctx) ([]HostingDomain, error)` (urut nama); `HostingDomain(ctx, id) (HostingDomain, error)` (`ErrNotFound`).
  - `UpdateHostingDomain(ctx, id int64, enabled *bool, reserved *string) (HostingDomain, error)`; `SetHostingDNS(ctx, id int64, status, detail string, now int64) error`.
  - `RemoveHostingDomain(ctx, id) error` — `*HostingInUseError{N int}` (`errors.Is(err, ErrConflict)`) bila masih dipakai.
  - `AddHostedDomain(ctx, appID, hostingID int64, label, host string, quota int, now int64) (Domain, error)` — satu transaksi: batas 5/aplikasi (`ErrDomainLimit`), domain terdaftar ada dan aktif (`ErrHostingUnavailable`), subdomain pemilik < `quota` (`*SubdomainQuotaError{Used,Limit int}`, `errors.Is(err, ErrSubdomainQuota)`), host unik (`ErrConflict`).
  - `SubdomainCount(ctx, userID) (int, error)`; `HostedDomains(ctx, hostingID) ([]Domain, error)`; `HostingUsage(ctx, since int64) (used map[int64]int, recent map[int64]int, err error)` (jumlah subdomain per domain terdaftar, dan yang `created_at >= since`).

- [ ] **Step 1: Write failing tests** di `hosting_test.go`: `TestHostingDomainCRUD` (nama unik → `ErrConflict`, update enabled/reserved, `SetHostingDNS`); `TestAddHostedDomainQuotaCountsAcrossAppsAndDisabledBase` (kuota 2, dua aplikasi, subdomain ketiga → `ErrSubdomainQuota{Used:2,Limit:2}`, tetap dihitung saat domain dasar dimatikan; hapus aplikasi → kuota bebas); `TestAddHostedDomainRejectsDisabledBase`; `TestHostedHostConflictsWithFreeDomain` (`AddDomain("x.porto.ex.id")` lalu hosted label `x` → `ErrConflict`); `TestAddHostedDomainLimitPerApp` (gabungan 5); `TestRemoveHostingDomainRestrict` (`*HostingInUseError{N:1}`, `errors.Is(ErrConflict)`, setelah subdomain dihapus bisa dihapus); `TestHostingUsageWindow`. Di `migrate_test.go`: `TestMigration0002KeepsVersion1Data` — buat DB pada versi 1 (jalankan hanya `0001` dari embed), isi user, aplikasi, dua domain bebas milik pengguna biasa, naikkan ke `0002`; semua baris utuh, `hosting_domain_id` NULL dan `label` `''`, `user_version = 2`, `foreign_key_check` bersih.
- [ ] **Step 2: Run** `go test ./internal/store -run 'Hosting|Hosted|Migration0002' -count=1` → FAIL (undefined).
- [ ] **Step 3: Implement** SQL persis spec (`CREATE TABLE hosting_domains`, dua `ALTER TABLE`, indeks `domains_hosting`) dan kode store. Ubah `AddDomain` dan `AddHostedDomain` agar berbagi satu fungsi privat (hitung batas 5, insert); `domainCols`/`scanDomain` memuat dua kolom baru.
- [ ] **Step 4: Run** `go test ./internal/store -count=1` → PASS.
- [ ] **Step 5: Commit** `feat(store): migration 0002 and hosting domains`.

### Task 2: Aturan nama, cek DNS, batas Let's Encrypt

**Files:**
- Create: `internal/apps/hostnames.go`, `internal/apps/hostnames_test.go`, `internal/hostdns/hostdns.go`, `internal/hostdns/cloudflare.go`, `internal/hostdns/hostdns_test.go`
- Modify: `go.mod`, `go.sum` (`golang.org/x/net`)

**Interfaces:**
- Produces (`apps`, murni):
  - `func ValidateLabel(label string) error` — pesan Indonesia spesifik (terlalu pendek/panjang, karakter, tanda hubung di tepi, `xn--`/`--`).
  - `func ParseReserved(s string) ([]string, error)` — pisah koma, trim, huruf kecil, tiap entri lolos `ValidateLabel`, tanpa duplikat; `""` → kosong.
  - `func ReservedLabels(extra []string, panelDomain, hostingName string) []string` — bawaan + `extra` + label panel bila `panelDomain == label+"."+hostingName`.
  - `func NormalizeHostingName(s string) (string, error)` — buang `*.`, spasi, titik akhir, lalu `caddy.ValidateDomain`.
  - `func HostingNesting(name string, existing []string) (conflict string)` — nama yang berada di bawah atau menaungi domain terdaftar lain, `""` bila bebas.
  - `type LELevel string` (`""`, `"warn"`, `"crit"`); `func LEGroups(domains map[int64]string, recent map[int64]int, panelDomain string) map[int64]LEUsage` dengan `LEUsage{Count int; Level LELevel}`; kunci grup lewat `publicsuffix.EffectiveTLDPlusOne` (gagal → nama utuh).
- Produces (`hostdns`): `type Resolver interface{ LookupHost(ctx, host string) ([]string, error) }`; `type Result struct{ OK bool; Detail string }`; `type Checker struct{ Resolver Resolver; LocalIPs func() []netip.Addr; Token func() string; Timeout time.Duration }` dengan `Check(ctx, domain, publicIP string) Result`; `IsCloudflare(netip.Addr) bool`. Nilai nol `Checker` memakai `net.DefaultResolver`, antarmuka lokal non-loopback, 6 hex acak, 5 detik.

- [ ] **Step 1: Write failing tests.** `hostnames_test.go` (table): label valid `cv-budi`, `abc`, `a1b`; tidak valid `ab`, 41 karakter, `-abc`, `abc-`, `Abc`, `a.b`, `ab--cd`, `xn--abc`, `a b`; `ParseReserved("Blog, shop ,blog")` → `[blog shop]`, `"x"` → error; `ReservedLabels(nil, "runova.porto.ex.id", "porto.ex.id")` memuat `runova` dan `www`, tetapi dengan `panelDomain="runova.prodak.id"` dan `porto.ex.id` tidak menambah label; `HostingNesting("a.b.id", ["b.id"])` = `b.id`, `("b.id", ["a.b.id"])` = `a.b.id`, `("c.id", ["a.b.id"])` = `""`, `("ab.id", ["b.id"])` = `""` (bukan sufiks label); `LEGroups` (`porto.devkriyanesia.my.id` dan `dev.devkriyanesia.my.id` satu grup `devkriyanesia.my.id`, `prodak.id` terpisah; 39 → `""`, 40 → warn, 50 → crit; panel `runova.prodak.id` menambah 1 pada grup `prodak.id`). `hostdns_test.go` dengan `Resolver` palsu: ok (alamat = PublicIP), PublicIP kosong memakai `LocalIPs`, tidak ada record (`*net.DNSError{IsNotFound:true}`) → detail berisi "Belum ada record", IP lain → detail memuat kedua IP, `104.16.1.1` → detail memuat "matikan proxy", `2606:4700::1111` Cloudflare v6, resolver yang memblokir sampai ctx selesai → kembali ≤ `Timeout` (diset 50 ms) dengan `OK=false`; host yang di-resolve berawalan `runova-check-` + token dan berakhiran `.`+domain; `IsCloudflare` mencakup tepi `173.245.48.0/20`, `131.0.72.0/22` dan menolak `1.1.1.1`.
- [ ] **Step 2: Run** `go test ./internal/apps ./internal/hostdns -run 'Label|Reserved|Nesting|LEGroups|Check|Cloudflare' -count=1` → FAIL.
- [ ] **Step 3: Implement.** `go get golang.org/x/net@v0.60.0` (sudah ada di cache modul). Daftar Cloudflare = 15 IPv4 + 7 IPv6 CIDR apa adanya dengan komentar "diambil 2026-11-03" di atasnya, diparse sekali (`netip.MustParsePrefix`).
- [ ] **Step 4: Run** perintah yang sama → PASS.
- [ ] **Step 5: Commit** `feat(hosting): naming rules, DNS check, Let's Encrypt grouping`.

### Task 3: Service domain hosting dan Caddy

**Files:**
- Create: `internal/apps/hosting.go`, `internal/apps/hosting_test.go`
- Modify: `internal/apps/domains.go`, `internal/apps/caddystate.go`, `internal/apps/service.go` (field `DNS`), `internal/apps/validate.go` (`ErrForbidden`), `internal/apps/domains_test.go`, `internal/apps/zero_test.go` bila perlu

**Interfaces:**
- Consumes: Task 1 store, Task 2 aturan dan `hostdns`.
- Produces:
  - `Service.DNS interface{ Check(ctx, domain, publicIP string) hostdns.Result }` (nil → `hostdns.Checker{}`).
  - `var ErrForbidden`; `forbidden(msg)`.
  - `type DomainInput struct{ HostingDomainID int64 \`json:"hosting_domain_id"\`; Label string \`json:"label"\`; Host string \`json:"host"\` }`; `AddDomain(ctx, user, appID int64, in DomainInput) (store.Domain, error)` (menggantikan versi string; satu-satunya bentuk).
  - `type HostingView struct{ store.HostingDomain; Used int \`json:"used"\`; LECount int \`json:"le_count"\`; LELevel LELevel \`json:"le_level"\` }`.
  - `HostingDomains(ctx, user) ([]HostingView, error)` (admin semua; pengguna biasa hanya aktif), `AddHostingDomain(ctx, actor, name, reserved string) (HostingView, error)`, `UpdateHostingDomain(ctx, actor, id int64, enabled *bool, reserved *string) (HostingView, error)`, `CheckHostingDNS(ctx, actor, id) (HostingView, error)`, `RemoveHostingDomain(ctx, actor, id) error`, `HostingAvailable(ctx, user, id int64, label string) (ok bool, reason string, err error)`, `SubdomainQuota(ctx, user) (used, limit int, err error)`.
  - Pesan: kuota → `overQuota("Kuota subdomain habis (%d dari %d)")`; host dipakai → `conflict("Alamat sudah dipakai")`; domain mati/tidak ada → `invalid("Domain hosting tidak tersedia")`; masih dipakai → `conflict("Masih dipakai %d subdomain; matikan dulu")`; `{host}` oleh non-admin → `forbidden("Domain sendiri hanya bisa ditambahkan admin")`; `{host}` bersama `hosting_domain_id`, atau keduanya kosong → `invalid`.

- [ ] **Step 1: Write failing tests** (`hosting_test.go`, memakai `newTestEnv`, `fakeCaddy`, `Service.DNS` palsu): `TestHostingAddValidates` (domain panel, bersarang, `*.` dibuang, duplikat 409, reserved buruk 400, DNS dicek dan disimpan, aktivitas `hosting_domain.add`); `TestAddHostedSubdomain` (host = `label.name`, `label` tersimpan, `domain.add`, Apply dipanggil); `TestAddHostedLabelRules` (`ab`, `Www`, `api`, reserved tambahan, label panel otomatis → 400 spesifik; host bebas admin yang sama → 409); `TestSubdomainQuotaFollowsAppsQuota` (kuota Apps 2 → ketiga 409 "Kuota subdomain habis (2 dari 2)"; naikkan kuota → lolos; turunkan ke 1 → subdomain tetap, tambahan 409); `TestUserCannotAddOwnDomain` (403, domain bebas lama tetap terbaca lewat `Domains`; admin boleh); `TestHostedOwnership` (pengguna B tak bisa menambah/menghapus pada aplikasi A → `ErrAppNotFound`); `TestHostedCaddyRejectsRollsBack`; `TestDisableHostingRemovesRoutesAndNotifies` (`CaddyState` tanpa subdomain, notifikasi ke tiap pemilik terdampak satu kali per aplikasi, nyalakan → route kembali, Caddy menolak → status dikembalikan); `TestCheckDomainsSkipsDisabledBase`; `TestRemoveHostingInUse` (409 pesan di atas, setelah subdomain hilang berhasil, `hosting_domain.remove`); `TestAvailableStoresNothing` (jumlah baris `domains` tak berubah; alasan untuk label buruk/dipakai/domain mati); `TestHostingListByRole` (pengguna biasa tidak melihat domain mati dan tak ada `le_*`; admin melihat `used`).
- [ ] **Step 2: Run** `go test ./internal/apps -run 'Hosting|Hosted|Subdomain|OwnDomain' -count=1` → FAIL.
- [ ] **Step 3: Implement.** Port `AddDomain` yang ada ke `DomainInput` (validasi `{host}` seperti sebelumnya). Pada `CaddyState` dan `CheckDomains`, himpun himpunan `disabled` dari `Store.HostingDomains`. `UpdateHostingDomain` meniru pola `SaveSettings`: simpan, `ApplyCaddy`, bila gagal kembalikan. `LEGroups` memakai `HostingUsage(now-7 hari)` dan domain panel dari setelan. Perbarui pemanggil lama di `domains_test.go` ke `DomainInput{Host: …}`; uji yang memakai domain bebas oleh pengguna biasa dialihkan ke admin.
- [ ] **Step 4: Run** `go test ./internal/apps ./internal/store -count=1` → PASS.
- [ ] **Step 5: Commit** `feat(apps): hosting domain service, subdomain quota, Caddy skip when disabled`.

### Task 4: API

**Files:**
- Create: `internal/api/hosting.go`, `internal/api/hosting_test.go`
- Modify: `internal/api/api.go` (`s.hostingRoutes(mux, user, admin)` bila `s.apps != nil`), `internal/api/apps.go` (`ErrForbidden` → 403), `internal/api/domains.go` (isi POST: `apps.DomainInput`), `internal/api/domains_test.go`

**Interfaces:**
- Consumes: Task 3 service.
- Produces rute dan bentuk JSON persis tabel spec §API. `GET /api/hosting-domains` → `{"domains":[…],"quota":{"used":N,"limit":N},"public_ip":"…"}`; pengguna biasa: tiap elemen hanya `{id,name}`. `GET …/available?label=` → `{available,reason}` (200). `PATCH` badan `{enabled?,reserved?}` → HostingView. `POST …/check` → HostingView. `POST /api/hosting-domains` → 201 HostingView. `DELETE` → `okBody`.

- [ ] **Step 1: Write failing tests** `hosting_test.go`: pengguna biasa ditolak (403) pada POST/PATCH/DELETE/check; daftar per peran (bentuk JSON elemen pengguna tepat dua kunci); alur admin tambah → patch mati → pengguna tak lagi melihat → hapus 409 saat dipakai; `available` 200 dengan `reason` dan tidak menambah baris; `POST /api/apps/{id}/domains` dengan `{hosting_domain_id,label}` 201, `{host}` oleh pengguna biasa 403 dengan pesan spec, kuota habis 409, domain mati 400; aktivitas `hosting_domain.*` tercatat.
- [ ] **Step 2: Run** `go test ./internal/api -run Hosting -count=1` → FAIL.
- [ ] **Step 3: Implement** handler tipis yang memanggil service; ID path via helper seperti `domainID` (`{id}`).
- [ ] **Step 4: Run** `go test ./internal/api -count=1` → PASS.
- [ ] **Step 5: Commit** `feat(api): hosting domain endpoints`.

### Task 5: Fake backend, kartu admin "Domain hosting"

**Files:**
- Create: `web/js/components/hostingcard.js`
- Modify: `cmd/runova/devbackends.go` (`fakeApps`: `svc.DNS` palsu — `ok` kecuali nama berawalan `bad.` → "Belum ada record DNS untuk *.<nama>" dan `proxy.` → "Tampak alamat Cloudflare (proxy menyala): matikan proxy"), `cmd/runova/devbackends_test.go` bila ada, `web/js/pages/server.js` (kartu di bawah status, dimuat ulang tiap poll bila fokus tidak di input), `web/js/pages/activity.js` (tiga aksi baru), `web/js/components/guide.js` (ekspor `kv`, `cloud`), `web/js/validate.js` (`labelError`, `hostingNameError` dengan kata-kata server), `web/test/validate.test.html`, CSS di `web/css/components.css` bila perlu

**Interfaces:**
- Consumes: Task 4 API.
- Produces: `hostingCard({ onChange }) → { node, reload() }`; `labelError(label)` → `""`/pesan; `hostingNameError(name)`.

Kartu: tabel nama · sakelar aktif · chip DNS (ok/error/unchecked + `title` detail) · "N subdomain" · lencana batas LE (kuning/merah, teks "perkiraan; bukan hitungan Let's Encrypt") · tombol Cek DNS, Nama cadangan (dialog), Hapus (konfirmasi). Form tambah (nama + cadangan opsional) menampilkan petunjuk DNS: Type A, Name `*.<sub>`, IPv4 = `public_ip` (bila kosong: "IP publik server (isi di Setelan)"), Proxy DNS only. Mematikan memakai `confirm` yang menyebut jumlah subdomain terdampak.

- [ ] **Step 1: Write failing tests**: `validate.test.html` (label valid/invalid sama dengan Task 2 table, nama hosting menerima `*.porto.ex.id`). Uji Playwright untuk kartu ditulis di Task 7.
- [ ] **Step 2: Run** `node web/test/unit.mjs` → FAIL (validate.test.html gagal).
- [ ] **Step 3: Implement** modul dan kartu; ikuti gaya `server.js` (`h`, `icon`, `act`, `dialog`, `fill`).
- [ ] **Step 4: Run** `node web/test/unit.mjs` → PASS; `go build ./...`.
- [ ] **Step 5: Commit** `feat(web): hosting domain card on the Server page`.

### Task 6: Kontrol "Alamat" (wizard dan tab Domain)

**Files:**
- Create: `web/js/components/address.js`
- Modify: `web/js/pages/deploy.js` (langkah 4), `web/js/pages/app/domains.js`, `web/js/pages/app/general.js` (ringkasan domain bila perlu), CSS

**Interfaces:**
- Produces: `addressControl({ hosting, isAdmin, ownGuide, onChange }) → { node, value(), error(), pending() }`; `value()` = `null` | `{hosting_domain_id,label}` | `{host}`. Kontrol memuat: pilihan domain terdaftar aktif (+ "Domain sendiri" hanya admin), kotak label, pratinjau `label.domain`, status ketersediaan (debounce 300 ms ke `/available`, nilai: memeriksa/tersedia/alasan), penanda "N dari M subdomain", nonaktif beserta alasan saat kuota habis atau tidak ada domain (pengguna biasa: "Belum ada domain hosting; hubungi admin"; admin: tautan `#/server`). Mode "Domain sendiri" menampilkan input host + panduan `guide()` yang ada.

- [ ] **Step 1:** Perbarui `smoke-deploy.mjs` dan `smoke-app.mjs` ke selektor baru (lihat Task 7) — jalankan, gagal.
- [ ] **Step 2: Implement** komponen; wizard memanggil `POST /api/apps/{id}/domains` dengan `value()` setelah aplikasi dibuat dan `problems()` menambah kesalahan bila `error()`. Tab Domain: baris subdomain tampil `label` + chip "Domain hosting" (atau "Dimatikan admin" bila `hosting_domain_id` tak ada di daftar aktif), tanpa panduan DNS untuk subdomain; panduan tetap untuk domain bebas.
- [ ] **Step 3: Run** `node web/test/unit.mjs` dan `go build ./...`.
- [ ] **Step 4: Commit** `feat(web): address control for hosting subdomains in wizard and Domain tab`.

### Task 7: Uji Playwright dan screenshot

**Files:**
- Create: `web/test/smoke-hosting.mjs`
- Modify: `web/test/smoke-deploy.mjs`, `smoke-app.mjs`, `smoke-admin.mjs`, `smoke-layout.mjs`, `screenshots.mjs`, `docs/screenshots/*`

- [ ] **Step 1: Write** `smoke-hosting.mjs`: admin membuka Server → tambah `apps.contoh.id` lewat kartu (petunjuk DNS `*.apps` terlihat, chip DNS ok) → buat budi (kuota Apps 1) → budi masuk, wizard langkah 4: tanpa "Domain sendiri", memilih domain, label `ab` → kesalahan, `www` → "dicadangkan", `cv-budi` → tersedia, pratinjau `cv-budi.apps.contoh.id` → deploy → tab Domain memuat alamat itu; kontrol nonaktif dengan "Kuota subdomain habis (1 dari 1)"; admin mematikan domain → budi melihat "Dimatikan admin", notifikasi; pengguna tanpa domain terdaftar aktif melihat "Belum ada domain hosting; hubungi admin"; admin di wizard melihat "Domain sendiri". Tata letak 360, 768, 1440 px tanpa overflow horizontal di tiga layar ini (tambahan pada `smoke-layout.mjs`).
- [ ] **Step 2: Run** semua `web/test/*.mjs` (unit, smoke-deploy, smoke-app, smoke-admin, smoke-lifecycle, smoke-layout, smoke-hosting) → PASS.
- [ ] **Step 3:** `screenshots.mjs` mendaftarkan domain hosting dan membuat subdomain contoh; tambah `desktop-server.png` (kartu Domain hosting, terang) dan `mobile-deploy.png`/`desktop-deploy.png` langkah 4 (gelap/terang) dan regenerasi; lihat sendiri hasilnya.
- [ ] **Step 4: Commit** `test(web): hosting domain flows and screenshots`.

### Task 8: E2E lokal

**Files:**
- Modify: `test/e2e/run.sh` (skenario 9)

- [ ] **Step 1: Write** skenario `9 hosting-domain`: admin set `public_ip` 127.0.0.1, `POST /api/hosting-domains {"name":"apps.localhost"}`; budi menambah `cv-budi` pada app hello; `curl --resolve cv-budi.apps.localhost:443:127.0.0.1 -k https://cv-budi.apps.localhost/` menjawab `hello v2`; penerbit sertifikat dari `openssl s_client` memuat "Caddy Local Authority"; andi mencoba memakai aplikasi budi → 404; budi `{host}` → 403; label dipakai andi → 409; matikan domain → curl gagal/bukan 200; nyalakan → kembali 200. Perbarui kalimat penutup jadi "Semua 9 skenario lulus."
- [ ] **Step 2: Run** `test/e2e/stack.sh up && test/e2e/run.sh && test/e2e/stack.sh down` → PASS.
- [ ] **Step 3: Commit** `test(e2e): shared hosting domain scenario`.

### Task 9: Dokumen dan pemeriksaan akhir

**Files:**
- Modify: `docs/spec.md` §6, `README.md` (bagian domain), `docs/install-ubuntu.md` (bagian baru "Domain hosting untuk pengguna"), `docs/superpowers/notes/hosting-domains.md` (penyimpangan dari spec bila ada)

- [ ] **Step 1:** Perbarui dokumen sesuai spec (`docs/spec.md` §6 menautkan spec baru; install guide: record A `*.porto` DNS only di zona `devkriyanesia.my.id`, catatan batas Let's Encrypt 50 sertifikat/minggu per domain terdaftar).
- [ ] **Step 2: Run** `gofmt -l .`, `go vet ./...`, `CGO_ENABLED=0 go build ./...`, `go test -count=1 ./...`, integrasi `RUNOVA_DOCKER_TEST=1 RUNOVA_PG_TEST=1 RUNOVA_CADDY_TEST=1 go test -count=1 ./internal/docker ./internal/apps ./internal/pg ./internal/dbs ./internal/caddy`, `test/bats.sh` bila tersedia → bersih/PASS.
- [ ] **Step 3: Commit** `docs: shared hosting domains`.
