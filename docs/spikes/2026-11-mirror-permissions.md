# Uji Risiko: Izin Tulis Mirror di Supabase

**Tanggal:** 2026-11-13 · **Menjawab:** risiko 1 di bagian 11 spec rewrite.
**Status:** lolos di stack Supabase lokal (repo `terka`); belum diulang di proyek Supabase Free yang
dihosting.

**Kesimpulan:** rancangan mirror di spec rewrite bagian 6.11 bisa dipakai, dengan dua tambahan
(bagian "Akibat untuk tahap 7"). Tidak ada temuan yang memaksa desain diubah.

## Lingkungan

Stack Supabase lokal dari repo lama (`supabase start`), PostgreSQL 17.11, dengan kedua belas
migration repo lama terpasang. Koneksi sebagai role `postgres`, yang di stack ini **bukan superuser**
(`rolsuper = false`, `rolbypassrls = true`), sama seperti di proyek yang dihosting. Superuser
sebenarnya adalah `supabase_admin`, yang tidak tersedia bagi pemilik proyek.

Skrip: `spikes/mirror-permissions.sql` (A–F) dan `spikes/mirror-signin.sh` (G), keduanya tidak
di-commit. A–F berjalan dalam transaksi yang di-rollback; G membuat lalu menghapus pengguna uji.

## Hasil

| | Pemeriksaan | Hasil |
| --- | --- | --- |
| A | `insert into auth.users` langsung | Lulus |
| B | `set local session_replication_role = replica` (melewati trigger) | Lulus |
| C | `alter table … disable trigger user` | Lulus (tidak dibutuhkan karena B lulus) |
| D | Menyalin `devices` dan `review_logs` dengan `server_seq`, `received_at`, dan `last_seen_at` aslinya (`overriding system value`, trigger dilewati) | Lulus; ketiga nilai tersimpan persis |
| E | `setval` sequence identitas `review_logs` dan sequence `practice_runs_server_seq` | Lulus |
| F | `truncate public.review_logs, public.review_voids cascade` | Lulus |
| G | Pengguna yang disalin ke `auth.users` login dengan kode email lewat Supabase Auth | Lulus **setelah** kolom token diisi (lihat di bawah); `user_id` sesi sama dengan id salinan, dengan maupun tanpa baris `auth.identities` |
| H | Penyedia login dimatikan, lalu A–F diulang | Tidak diuji; penulisan lewat koneksi Postgres langsung tidak melewati Supabase Auth, jadi tidak terpengaruh |

### Temuan di G

Percobaan pertama gagal: Supabase Auth menjawab `500 Database error finding user`. Lognya:
`Scan error on column index 3, name "confirmation_token": converting NULL to string is unsupported`.

GoTrue membaca beberapa kolom `auth.users` sebagai string biasa, jadi kolom itu harus berisi string
kosong, bukan NULL: `confirmation_token`, `recovery_token`, `email_change_token_new`,
`email_change`, `email_change_token_current`, `phone_change`, `phone_change_token`,
`reauthentication_token`. Setelah diisi `''`, login berhasil.

## Akibat untuk tahap 7 (mirror)

1. Saat menyalin pengguna, mirror mengisi kolom yang dibutuhkan GoTrue: `instance_id` nol, `aud` dan
   `role` `authenticated`, `email_confirmed_at`, `raw_app_meta_data` dengan penyedia, `'{}'` untuk
   `raw_user_meta_data`, dan string kosong untuk kedelapan kolom token di atas.
2. Untuk pengguna yang punya `google_sub`, mirror juga menulis baris `auth.identities` dengan
   `provider = 'google'` dan `provider_id = google_sub`, supaya login Google di Supabase menemukan
   pengguna yang sama tanpa bergantung pada penautan otomatis lewat email. Ini belum diuji (tidak ada
   OAuth Google di stack lokal).
3. Trigger kuota dan persetujuan dilewati dengan `set local session_replication_role = replica` di
   dalam transaksi mirror, bukan dengan mematikan trigger per tabel.

## Yang masih perlu diulang di proyek yang dihosting

- A, B, D, dan E di proyek Supabase Free sungguhan. Di stack lokal, role `postgres` boleh menyetel
  `session_replication_role` walaupun bukan superuser; perlu dipastikan izin yang sama berlaku di
  proyek yang dihosting.
- Login Google untuk pengguna salinan (butir 2 di atas).
- H: penyedia login dimatikan di dasbor.

Tidak mendesak: jawabannya hanya dibutuhkan sebelum tahap 7, dan stack lokal sudah memakai image yang
sama dengan versi hosting.
