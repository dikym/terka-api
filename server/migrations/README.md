# Migration

Berkas SQL polos, dijalankan `terka migrate` urut nama berkas, masing-masing dalam satu transaksi.
Versi yang sudah diterapkan dicatat di `terka.schema_migrations`. Tidak ada migration turun:
perubahan skema mengikuti pola tambah dulu, hapus belakangan.

Migration tidak pernah membuat role. `anon` dan `authenticated` sudah ada sebelum migration pertama
(lihat `deploy/postgres/init/00-roles.sql`), dan role pemilik database bukan superuser.

## Kontrak dengan repo lama

Skema `public` harus sama dengan `supabase/migrations/` di repo `terka`. Berkas bertanggal `2026…`
di sini adalah salinan dari sana. Perbedaannya hanya:

| Berkas di repo lama | Di sini | Alasan |
| --- | --- | --- |
| `20261004000001_foundation.sql` | Baris `create extension if not exists pg_cron;` dihapus | Pembersihan terjadwal dijalankan server Go |
| `20261004000005_jobs.sql` | Tidak disalin | Menjadwalkan lewat `pg_cron` |
| `20261030000001_sync_signal.sql` | Tidak disalin | Policy untuk Supabase Realtime; diganti hub SSE |
| `20261101000003_passwordless.sql` | Tidak disalin | Trigger password di `auth.users`; di sini tidak ada password |

`00000000000000_platform.sql` hanya ada di sini. Ia membuat apa yang di Supabase sudah tersedia:
skema `auth`, tabel `auth.users` dan `auth.sessions`, serta fungsi `auth.uid()` dan `auth.jwt()`.

Migration baru di repo ini yang mengubah skema `public` wajib punya padanan di repo lama.
