# Domain hosting bersama (wildcard) — desain

Tanggal: 2026-11-03. Status: disetujui di percakapan, menunggu tinjauan spec.

## Tujuan

Admin mendaftarkan domain wildcard di panel, lalu setiap pengguna (admin maupun biasa) bisa memberi aplikasinya alamat `<nama>.<domain terdaftar>` tanpa mengurus DNS, sampai sebanyak kuota aplikasinya.

Contoh penerapan:
- Panel Runova di `runova.prodak.id` (domain panel biasa, tidak dipakai untuk hosting).
- Admin mendaftarkan `porto.devkriyanesia.my.id`; pengguna memakai `cv-budi.porto.devkriyanesia.my.id`.

## Keputusan

1. **Trafik: DNS langsung (DNS only).** Server punya IP publik; record wildcard menunjuk ke IP server; Caddy (paket apt Ubuntu, 2.6.2, tanpa modul DNS) menerbitkan **satu sertifikat Let's Encrypt per subdomain** lewat HTTP/TLS-ALPN challenge. Tidak ada sertifikat wildcard, tidak ada API token Cloudflare. Proxied/Tunnel di luar lingkup versi ini (Universal SSL Cloudflare tidak mencakup subdomain tingkat dua).
2. **Domain hosting didaftarkan eksplisit oleh admin.** Domain yang tidak terdaftar tidak pernah dianggap wildcard, termasuk domain induk panel. Panel di `runova.prodak.id` tidak membuat `*.prodak.id` bisa dipakai; bila kelak admin mendaftarkan `prodak.id`, barulah `<nama>.prodak.id` tersedia, dan nama host panel (`runova`) otomatis dicadangkan.
3. **Domain terdaftar boleh di tingkat mana pun:** `prodak.id`, `porto.devkriyanesia.my.id`, dst. Subdomain yang dibuat pengguna selalu tepat satu label di depannya.
4. **Kuota subdomain = kuota aplikasi pengguna** (`Quota.Apps`). Tidak ada kolom kuota baru. Dihitung atas semua subdomain milik pengguna di semua aplikasinya, termasuk yang domain dasarnya sedang dimatikan.
5. **Domain milik sendiri (bebas, mis. `budi.com`) hanya untuk admin.** Pengguna biasa hanya memilih dari domain terdaftar. Domain bebas yang sudah ada milik pengguna biasa (dari versi sebelumnya) tetap berjalan, tidak bisa ditambah lagi.
6. **Batas per aplikasi tetap 5 domain** (gabungan subdomain hosting dan domain bebas).

## Model data (migrasi `0002_hosting_domains.sql`)

```sql
CREATE TABLE hosting_domains(
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,          -- "porto.devkriyanesia.my.id", tanpa "*."
	enabled INTEGER NOT NULL DEFAULT 1,
	reserved TEXT NOT NULL DEFAULT '',  -- nama cadangan tambahan, dipisah koma
	dns_status TEXT NOT NULL DEFAULT 'unchecked',  -- unchecked | ok | error
	dns_detail TEXT NOT NULL DEFAULT '',
	dns_checked_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
ALTER TABLE domains ADD COLUMN hosting_domain_id INTEGER NULL REFERENCES hosting_domains ON DELETE RESTRICT;
ALTER TABLE domains ADD COLUMN label TEXT NOT NULL DEFAULT '';  -- "cv-budi"; kosong untuk domain bebas
CREATE INDEX domains_hosting ON domains(hosting_domain_id);
```

- `domains.host` tetap berisi nama lengkap dan tetap `UNIQUE`, sehingga bentrok antara subdomain hosting dan domain bebas tertangkap di satu tempat.
- Domain hosting tidak bisa dihapus selama masih dipakai (`RESTRICT`); admin mematikannya dulu, pemilik memindahkan aplikasinya, lalu admin menghapus. Respons 409 menyebut jumlah subdomain yang masih memakainya.

## Aturan nama

- **Domain terdaftar:** `caddy.ValidateDomain` (huruf kecil, minimal dua label, bukan IP), tidak boleh sama dengan domain panel, tidak boleh berada di bawah domain terdaftar lain atau menaungi domain terdaftar lain (`a.b.id` dan `b.id` tidak boleh berdampingan — menghindari dua cara menulis host yang sama). Domain bebas yang sudah ada yang berada tepat satu label di bawahnya tidak menghalangi pendaftaran; ia tetap domain bebas.
- **Label pengguna:** `^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$` (3–40 karakter, tanpa `-` di awal/akhir, tanpa `--` di posisi 3–4 agar tidak membentuk punycode `xn--`).
- **Label cadangan:** bawaan `www`, `api`, `mail`, `smtp`, `ftp`, `admin`, `panel`, `runova`, `ns1`, `ns2`, `localhost`; ditambah `reserved` per domain terdaftar; ditambah label paling kiri domain panel bila domain panel berada tepat satu label di bawah domain terdaftar itu.
- Siapa cepat dia dapat (keunikan `domains.host`).

## Perilaku

### Admin (halaman Server → kartu "Domain hosting")
- Daftar domain terdaftar: nama, aktif/mati, status DNS, jumlah subdomain terpakai.
- **Tambah:** isi nama dasar (boleh ditulis `*.porto.devkriyanesia.my.id`; `*.` dibuang). Petunjuk DNS yang ditampilkan: record `A`, nama `*.porto` (relatif terhadap zona), nilai = IP publik server, proxy **DNS only**.
- **Cek DNS:** resolve `runova-check-<6 hex acak>.<domain>`; hasil `ok` bila salah satu alamat A/AAAA sama dengan `Settings.PublicIP` (atau IP lokal server bila `PublicIP` kosong); selain itu `error` dengan alasan (tidak ada record / menunjuk ke IP lain / tampak seperti alamat Cloudflare → "matikan proxy"). Resolver yang dipakai: resolver sistem, timeout 5 detik. Cek dijalankan saat tambah dan saat tombol ditekan; tidak berkala.
- **Matikan/nyalakan:** mematikan mencabut route semua subdomainnya dari Caddy (tetap tercatat) dan mengirim notifikasi ke setiap pemilik aplikasi terdampak; menyalakan memasang kembali.
- **Nama cadangan tambahan** per domain (teks dipisah koma, divalidasi dengan aturan label).
- Peringatan **batas Let's Encrypt**: untuk tiap domain terdaftar, jumlah subdomain yang **dibuat dalam 7 hari terakhir** pada domain terdaftar yang berbagi domain utama yang sama (domain utama = eTLD+1 menurut `golang.org/x/net/publicsuffix`, ditambah domain panel bila sama). Peringatan kuning ≥ 40, merah ≥ 50 ("sertifikat baru bisa tertunda sampai minggu depan"). Ini perkiraan, bukan hitungan Let's Encrypt sebenarnya; tertulis demikian di UI.

### Pengguna (wizard langkah 4 dan tab Domain)
- Kontrol "Alamat": pilihan domain terdaftar yang aktif + kotak label, pratinjau `cv-budi.porto.devkriyanesia.my.id`, status ketersediaan langsung (cek ke API, tanpa menyimpan).
- Penanda kuota: "2 dari 3 subdomain". Kuota habis → kontrol nonaktif dengan alasan.
- Admin juga melihat pilihan "Domain sendiri" (perilaku domain bebas yang sudah ada, dengan panduan DNS tiga cara). Pengguna biasa tidak melihatnya.
- Tanpa domain terdaftar yang aktif: pengguna biasa melihat "Belum ada domain hosting; hubungi admin", admin melihat tautan ke kartu Domain hosting.

### Caddy
- Tidak ada perubahan bentuk konfigurasi: setiap subdomain hosting tetap satu route host biasa di server `runova-https`. `CaddyState` melewatkan domain yang `hosting_domain_id`-nya mengarah ke domain terdaftar yang `enabled = 0`.
- Status sertifikat per subdomain tetap lewat pemeriksaan TLS yang sudah ada (`pending` → `active`/`error`).

## API

| Metode & path | Akses | Isi |
|---|---|---|
| `GET /api/hosting-domains` | login | pengguna: hanya yang aktif `{id,name}` + kuota `{used,limit}`; admin: semua, lengkap |
| `POST /api/hosting-domains` | admin | `{name, reserved?}` → 201, langsung cek DNS |
| `PATCH /api/hosting-domains/{id}` | admin | `{enabled?, reserved?}` |
| `POST /api/hosting-domains/{id}/check` | admin | cek DNS sekarang |
| `DELETE /api/hosting-domains/{id}` | admin | 409 bila masih dipakai |
| `GET /api/hosting-domains/{id}/available?label=` | login | `{available, reason}` |
| `POST /api/apps/{id}/domains` | pemilik/admin | `{hosting_domain_id, label}` **atau** `{host}`; bentuk `{host}` hanya admin (pengguna biasa → 403 "Domain sendiri hanya bisa ditambahkan admin") |

Aktivitas baru: `hosting_domain.add`, `hosting_domain.update`, `hosting_domain.remove`. Penambahan subdomain tetap `domain.add`.

## Kesalahan dan batas

| Kondisi | Respons |
|---|---|
| Label tidak valid / dicadangkan | 400 dengan alasan spesifik |
| Host sudah dipakai | 409 "Alamat sudah dipakai" |
| Kuota subdomain habis | 409 "Kuota subdomain habis (3 dari 3)" |
| Domain terdaftar mati atau tidak ada | 400 "Domain hosting tidak tersedia" |
| Caddy menolak konfigurasi | 502, baris domain dibatalkan (perilaku yang sudah ada) |
| Domain terdaftar masih dipakai saat dihapus | 409 "Masih dipakai N subdomain; matikan dulu" |
| Kuota aplikasi diturunkan di bawah subdomain terpakai | Subdomain yang ada tetap; penambahan ditolak sampai di bawah kuota (sama dengan aturan kuota lain) |

## Panel dan installer

- Installer: tidak berubah. `--domain runova.prodak.id` tetap cara memasang panel di domain.
- `docs/install-ubuntu.md`: bagian baru "Domain hosting untuk pengguna" (record A wildcard DNS only, contoh untuk `porto.devkriyanesia.my.id` di zona Cloudflare `devkriyanesia.my.id`: nama `*.porto`), dan catatan batas Let's Encrypt.
- `docs/spec.md` §6 diperbarui dan menautkan dokumen ini.

## Pengujian

- **Store:** migrasi 0002 pada DB versi 1 berisi data; keunikan host lintas jenis; `RESTRICT` saat hapus; hitung kuota lintas aplikasi.
- **Service/API:** label valid/invalid/cadangan (termasuk label panel otomatis), kuota = `Apps` (naik/turun), pengguna biasa ditolak untuk `{host}` tetapi domain bebas lamanya tetap terbaca, domain mati → route hilang dari `CaddyState` + notifikasi, aturan bersarang (`a.b.id` vs `b.id`), domain panel tidak bisa didaftarkan, `available` tidak menyimpan apa pun, kepemilikan (pengguna B tidak bisa memakai/menghapus subdomain A).
- **Cek DNS:** resolver yang bisa disuntik (`LookupHost`), kasus ok / tidak ada / IP lain / alamat Cloudflare (rentang IP Cloudflare yang tertanam, dari `https://www.cloudflare.com/ips-v4` dan `ips-v6`, dicatat tanggal ambilnya).
- **Batas Let's Encrypt:** pengelompokan per eTLD+1 (`porto.devkriyanesia.my.id` → `devkriyanesia.my.id`; `prodak.id` → `prodak.id`), jendela 7 hari.
- **Panel (Playwright):** admin mendaftarkan domain hosting → pengguna biasa memilihnya di wizard dengan label → aplikasi punya alamat itu; kuota 1 → kontrol kedua nonaktif; pengguna biasa tidak melihat "Domain sendiri"; tata letak 360–1440 px.
- **E2E lokal:** skenario baru — domain hosting `apps.localhost` (resolusi `*.localhost` ke 127.0.0.1 berlaku di curl dengan `--resolve`), subdomain `cv-budi.apps.localhost` menjawab lewat HTTPS dengan sertifikat internal Caddy.

## Di luar lingkup

Proxied/Tunnel untuk subdomain tingkat dua, sertifikat wildcard (DNS-01), domain hosting khusus per pengguna, kuota subdomain terpisah dari kuota aplikasi, mengganti label subdomain yang sudah ada (hapus lalu buat ulang).
