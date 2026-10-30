# Bagian 1: deviasi dan keputusan

Nama, signature, dan konstanta di blok Interfaces rencana tidak diubah. Hal-hal di bawah adalah koreksi atau tambahan kecil.

1. **Baris `export` uji asap (Task 6 Step 5).** `export RUNOVA_DATA=$(mktemp -d) RUNOVA_SECRET_KEY=$RUNOVA_DATA/k ...` memperluas `$RUNOVA_DATA` sebelum `export` berjalan, sehingga kunci menjadi `/k`. Dipecah jadi dua `export`. README memakai bentuk yang sudah dipecah.
2. **Menurunkan peran diri sendiri.** Spec bagian 3 melarang admin menurunkan perannya sendiri, padahal rencana hanya menguji kasus admin tunggal. Admin tunggal tetap dapat 409 "Minimal harus ada satu admin" (sesuai `TestLastAdminProtected`). Bila ada admin lain, `PATCH` peran diri sendiri menjadi `user` ditolak 409 "Tidak bisa menurunkan peran akun sendiri". Tambahan ini ada di lapisan API, bukan store.
3. **`SetSettings` menormalkan `Runtimes`.** Kunci runtime yang tidak dikirim disimpan sebagai `false`, sehingga `GET /api/settings` selalu memuat ketujuh `RuntimeIDs`. Rencana tidak menyebut perilaku ini.
4. **`_txlock=immediate` di DSN.** Transaksi mengambil kunci tulis di awal, jadi pengecekan admin terakhir (`SELECT count(*)` lalu ubah/hapus) tidak bisa berlomba dengan transaksi lain.
5. **Login pengguna tak dikenal.** Memakai hash tiruan agar waktu respons sama dengan pengguna yang ada (tidak membocorkan keberadaan username). Username percobaan dipotong 64 karakter sebelum masuk ke `login_failures` dan aktivitas.
6. **Validasi tambahan di API.** `role` harus `admin`/`user`, `email` harus alamat valid (atau kosong), `display_name` maksimal 100 karakter, kuota di `PATCH` divalidasi dengan `Quota.Validate`. Rencana hanya menyebut validasi username dan password.
7. **Aktivitas tambahan.** Reset password oleh admin dicatat sebagai `password.change` dengan detail `reset by admin`. `PATCH` yang mengubah kuota mencatat `quota.update` terpisah dari `user.update`.
8. **Cookie logout.** Cookie penghapus ikut membawa atribut `Secure` sesuai `IsHTTPS`, supaya browser mau menimpa cookie `Secure` yang lama.
9. **Perubahan password sendiri lewat reset admin.** Bila admin mereset password miliknya sendiri lewat `POST /api/users/{id}/password`, sesinya diganti sesi baru (sama dengan `POST /api/me/password`), jadi tidak ikut terlempar.
