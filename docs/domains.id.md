# Domain dan HTTPS

[English](domains.md) · **Bahasa Indonesia**

Bagaimana aplikasi mendapat alamat: domain hosting bersama, domain sendiri, dan pilihan Cloudflare. Langkah untuk admin (record DNS, batas Let's Encrypt) ada juga di [Memasang di Ubuntu](install-ubuntu.id.md#domain-hosting-untuk-pengguna).

## Domain hosting bersama

Admin membuka halaman **Server → Domain hosting** dan mendaftarkan domain wildcard, misalnya `porto.devkriyanesia.my.id`, lalu membuat satu record `A` `*.porto` ke IP server (proxy **DNS only**). Runova mengecek DNS-nya dan menampilkan hasilnya. Setelah itu setiap pengguna, admin maupun biasa, memilih domain itu di langkah 4 wizard atau tab Domain, mengetik nama (`cv-budi`), dan aplikasinya langsung beralamat `cv-budi.porto.devkriyanesia.my.id`.

- Jumlah subdomain pengguna dibatasi kuota aplikasinya; batas per aplikasi tetap 5 alamat.
- Nama 3–40 karakter (huruf kecil, angka, tanda hubung); `www`, `api`, `mail`, `admin`, `runova`, label panel, dan nama cadangan tambahan dari admin tidak bisa dipakai. Siapa cepat dia dapat.
- Mematikan domain hosting mencabut semua subdomainnya dari Caddy dan memberi tahu pemiliknya; domain hanya bisa dihapus bila tidak ada subdomain yang memakainya.
- Satu sertifikat Let's Encrypt diterbitkan per subdomain. Let's Encrypt membatasi 50 sertifikat baru per minggu per domain utama; kartu Domain hosting menampilkan perkiraan dan memperingatkan mendekati batas. Di belakang nginx (proxy tepercaya diisi) Caddy tidak mengambil sertifikat Let's Encrypt dan perkiraan tidak ditampilkan.
- Domain milik sendiri (mis. `budi.com`) hanya bisa ditambahkan admin; domain seperti itu yang sudah dimiliki pengguna biasa tetap berjalan.
- Proxy Cloudflare (awan oranye) dan Tunnel tidak didukung untuk subdomain tingkat dua.

## Menghubungkan domain sendiri (admin)

Saat domain diisi, Runova menampilkan panduan yang sudah terisi subdomain, domain, dan alamat server. Ada tiga cara, sesuai kondisi jaringan:

| Cara | Kapan dipakai | Yang dibuat di Cloudflare |
|---|---|---|
| **DNS langsung** | Server punya IP publik, port 80 dan 443 terbuka | Record `A` ke IP publik server, proxy **DNS only** |
| **Proxy Cloudflare** | Ingin IP server tersembunyi dan dapat proteksi Cloudflare | Record `A` yang sama. Setelah HTTPS aktif di Runova, ubah ke **Proxied** dan atur SSL/TLS ke **Full (strict)** |
| **Cloudflare Tunnel** | Server tanpa IP publik atau tanpa port terbuka | Networking → Tunnels → Routes → Published application, Service URL `http://localhost:80` (Caddy di server Runova) |

Untuk DNS langsung dan proxy, sertifikat diterbitkan Caddy di server. Untuk Tunnel, HTTPS diurus Cloudflare.

Server Runova yang hanya punya IP lokal di belakang server lain ber-nginx dibahas di [nginx di depan Runova](nginx-front-proxy.id.md), termasuk sertifikat wildcard untuk domain hosting. Bila proxy tepercaya diisi, Caddy menerbitkan semua sertifikat dari CA internalnya (nginx memegang yang publik), sehingga subdomain baru langsung berfungsi dan nginx boleh mengalihkan port 80 sendiri.
