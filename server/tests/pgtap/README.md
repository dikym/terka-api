# Tes pgTAP

Dijalankan dengan `make pgtap`: database baru, skema dari `terka migrate`, lalu `pg_prove` di dalam
container Postgres dev. `00000_helpers.sql` dijalankan pertama dan sengaja commit; setiap berkas
`*.test.sql` berakhir dengan `rollback`.

## Perbedaan dari repo lama

Berkas di sini disalin dari `supabase/tests/database/` di repo `terka`. Yang berubah hanya:

| Berkas | Perubahan | Alasan |
| --- | --- | --- |
| `00000_helpers.sql` | `tests.create_user` menyisipkan `(id, email)` saja | `auth.users` di sini tidak punya `aud` dan `role` |
| `00000_helpers.sql` | `tests.add_session` menyisipkan `(id, user_id, token_hash, expires_at)` | Kolom `auth.sessions` milik sendiri |
| `performance.test.sql` | Sisipan ke `auth.users` memakai `(id, email)` saja | Sama dengan di atas |
| `rules.test.sql` | Asersi `cron.job` dihapus; `plan(53)` menjadi `plan(52)` | Pembersihan terjadwal dijalankan server Go |
| `rules.test.sql` | "an accepted policy version cannot be deleted" mengharapkan `23001`, bukan `23503` | PostgreSQL 18 melaporkan pelanggaran `on delete restrict` sebagai `restrict_violation` (`23001`); Postgres 17 di Supabase memakai `23503`. Penghapusan tetap ditolak |

`passwordless.test.sql` dan `sync_signal.test.sql` tidak disalin, karena migration yang diujinya
tidak ada di sini. Perilakunya diuji ulang sebagai tes Go saat auth dan SSE dibangun.

Harness memasang pgTAP di skema `extensions` dan memberi `usage` ke `anon` dan `authenticated`,
seperti di Supabase, karena tes memanggil fungsi pgTAP setelah berpindah role.
