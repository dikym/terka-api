# Terka: Auth di Server Go (Tahap 3) — Dokumen Desain

**Tanggal:** 2026-11-13 · **Status:** disetujui pemilik 2026-11-13
**Merinci:** `docs/superpowers/specs/2026-11-10-go-svelte-rewrite.md` bagian 6.6 (Auth dan Sesi) dan
6.8 (Keamanan). Yang tidak disebut di sini tetap mengikuti spec itu.

## 1. Maksud

Terka butuh login supaya riwayat bisa disinkronkan antarperangkat. Sampai sekarang login diurus
Supabase Auth; di sistem baru, server Go yang mengurusnya. Kode auth buatan sendiri adalah bagian
paling berisiko dari rewrite ini (spec bagian 11), dan pemilik meminta auth dibuat seaman mungkin dan
dibuktikan dengan banyak kasus uji. Karena itu dokumen ini dibangun di sekitar model ancaman: setiap
ancaman punya penjaga dan tes yang membuktikan penjaganya bekerja.

## 2. Tujuan, Bukan Tujuan, Anti-Tujuan

**Tujuan**

1. Login dengan Google dan dengan kode email 6 digit, tanpa password.
2. Sesi berupa cookie yang tidak bisa dibaca skrip, bisa dicabut seketika di server.
3. Setiap ancaman di bagian 5 punya tes otomatis yang gagal kalau penjaganya dilepas.
4. Kontrak API yang bisa dipetakan langsung ke status hasil klien lama (`ok`, `tooSoon`,
   `captchaFailed`, `invalid`, `cancelled`, `otherBrowser`, `failed`, `signedOut`).

**Bukan tujuan**

- Hapus akun, reset progres, hapus perangkat, dan penulisan persetujuan privasi (tahap 4).
- MFA, passkey, atau login dengan password.
- Panel admin untuk sesi atau pengguna.
- Menjalankan login sungguhan di staging; itu butuh kredensial dari pemilik (bagian 9).

**Anti-tujuan**

- Seseorang masuk ke akun orang lain, dengan cara apa pun di bagian 5.
- Email, kode, token sesi, atau cookie tertulis di log, trace, atau pesan kesalahan.
- Seseorang bisa mengetahui apakah sebuah email sudah terdaftar.
- Pengguna sah terkunci karena batas yang terlalu ketat dalam pemakaian wajar.

## 3. Batasan

- Spec rewrite 6.6: cookie `__Host-` `HttpOnly` `Secure` `SameSite=Lax`, token 256 bit, hanya hash
  yang disimpan, sesi 90 hari tanpa dipakai; kode 6 digit berlaku 15 menit, HMAC dengan kunci server,
  sekali pakai, hangus setelah 5 salah; 1 permintaan per alamat per 60 detik, 5 per alamat per jam,
  80 email per jam seluruh layanan; 20 verifikasi gagal per alamat per 24 jam; jawaban permintaan kode
  sama untuk alamat terdaftar maupun tidak; tautan email membuka `/masuk` dengan alamat dan kode di
  fragmen, dikirim lewat `POST`; Google memakai `golang.org/x/oauth2` dan
  `github.com/coreos/go-oidc/v3`.
- Spec rewrite 6.8: `http.CrossOriginProtection`, tidak ada `GET` yang mengubah data kecuali callback
  Google; IP pengguna dari `CF-Connecting-IP`.
- Satu origin (`terka.my.id`) di belakang Cloudflare dan Caddy tepi; UI dan API di container terpisah.
- PostgreSQL 18; role `anon` dan `authenticated` berlaku untuk seluruh klaster; role login aplikasi
  per lingkungan bersifat `NOINHERIT`.

## 4. Pendekatan

**Dipakai:** sesi opak di Postgres, kode email di Postgres, Google lewat OIDC dengan pustaka resmi,
semua langkah auth di dalam transaksi yang berjalan sebagai role khusus auth.

**Ditolak:**

- *JWT dengan refresh token* (cara Supabase): tidak bisa dicabut seketika tanpa daftar blokir, dan
  menambah dua jenis token yang bisa bocor. Satu server tidak butuh token yang bisa diverifikasi tanpa
  database.
- *Pustaka auth lengkap* (misalnya `goth`, `authboss`): membawa penyimpanan sesi dan alur sendiri yang
  harus diselaraskan dengan RLS dan skema Terka; permukaannya lebih besar dari yang dibutuhkan dua alur
  login.
- *Batas laju di memori saja*: hilang setiap deploy, sehingga penyerang mendapat jatah baru setiap kali
  server dimulai ulang. Batas yang melindungi kode (per alamat) harus ada di Postgres.

## 5. Model Ancaman

| # | Ancaman | Penjaga | Dibuktikan oleh |
| --- | --- | --- | --- |
| A1 | Menebak kode 6 digit | 5 salah per kode; 20 salah per alamat per 24 jam (dihitung di Postgres); 5 permintaan per alamat per jam; kode berlaku 15 menit | Tes batas per kode, per alamat, jendela 24 jam, kedaluwarsa |
| A2 | Memakai kode dua kali, termasuk dua permintaan bersamaan | Baris kode dikunci (`for update`) dan dihabiskan dalam transaksi yang sama dengan pembuatan sesi | Tes pakai ulang; tes 20 verifikasi bersamaan → tepat satu sesi |
| A3 | Kode lama tetap berlaku setelah kode baru diminta | Permintaan baru dari browser yang sama menghabiskan kode aktif browser itu. Kode browser lain tidak disentuh, supaya permintaan penyerang tidak menghanguskan kode korban | Tes kode lama browser yang sama ditolak; tes kode browser lain tetap berlaku |
| A4 | Mencuri kode dari database atau cadangan | Hanya HMAC-SHA256(kunci server, alamat ‖ kode) yang disimpan; kunci tidak ada di database | Tes tidak ada kolom berisi kode mentah; tes kunci berbeda → kode ditolak |
| A5 | Mengetahui email terdaftar | Jawaban `request` sama persis (status, isi, waktu tidak bergantung pada keberadaan pengguna); pengguna baru dibuat saat `verify` | Tes jawaban identik untuk alamat baru, terdaftar, dan terkunci |
| A6 | Banjir email ke alamat orang lain atau menghabiskan jatah Resend | Turnstile wajib; 60 detik dan 5 per jam per alamat; 80 per jam dan 90 per hari (UTC) seluruh layanan, di bawah jatah gratis Resend 100 per hari; batas laju per IP | Tes masing-masing batas; tes Turnstile gagal → tidak ada email |
| A7 | Lolos Turnstile dengan token palsu atau token dari situs lain | Verifikasi di server: `success`, `hostname` sesuai daftar, `action` = `email_request`; kegagalan jaringan ke Turnstile = ditolak | Tes token gagal, hostname salah, action salah, Turnstile tak terjangkau |
| A8 | Injeksi header email lewat alamat | Alamat dinormalkan (trim, huruf kecil), maks. 254 karakter, satu `@`, tanpa CR/LF/spasi/karakter kontrol; ditolak sebelum apa pun | Tes tabel alamat jahat; fuzz test normalisasi |
| A9 | Pemalsuan callback Google, CSRF login (korban dimasukkan ke akun penyerang) | `state`, `nonce`, dan pemverifikasi PKCE (S256) disimpan di cookie sementara `__Host-` terenkripsi, 10 menit, sekali pakai; callback wajib cocok | Tes state hilang, salah, kedaluwarsa, dipakai ulang; cookie diubah |
| A10 | Token ID palsu, kedaluwarsa, untuk aplikasi lain, atau email belum terverifikasi | `go-oidc`: tanda tangan, `iss`, `aud`, `exp`; `nonce` cocok; `email_verified` wajib benar | Tes terhadap penerbit OIDC tiruan untuk setiap kasus |
| A11 | Pengambilalihan akun lewat email yang dipakai ulang di akun Google lain | Pengguna dicari lewat `google_sub` dulu. Kalau alamatnya sudah milik pengguna dengan `google_sub` lain, login ditolak | Tes sub berbeda untuk email yang sama → ditolak, tidak ada sesi |
| A12 | Pengalihan setelah login ke situs lain | Tujuan setelah login tetap `/auth/callback`; tidak ada parameter tujuan | Tes callback selalu mengalihkan ke path tetap |
| A13 | CSRF pada endpoint yang mengubah data | `http.CrossOriginProtection` untuk semua metode tidak aman; `SameSite=Lax` | Tes POST lintas origin ditolak; `Sec-Fetch-Site: same-origin` diterima |
| A14 | Fiksasi sesi | Server tidak pernah menerima token dari klien selain lewat cookie miliknya; setiap login membuat sesi baru dan mencabut cookie lama di browser itu | Tes cookie lama sebelum login tidak berlaku setelah login |
| A15 | Pencurian token sesi lewat skrip atau database | Cookie `HttpOnly`; hanya SHA-256 token yang disimpan | Tes atribut cookie; tes kolom token berisi hash, bukan token |
| A16 | Sesi yang tidak pernah berakhir | 90 hari tanpa dipakai dan maksimum 400 hari sejak dibuat; sesi kedaluwarsa dihapus berkala | Tes sesi kedaluwarsa ditolak; tes batas mutlak |
| A17 | Keluar tidak benar-benar mencabut sesi | `logout` menghapus baris sesi di server dan menghapus cookie | Tes token yang sama ditolak setelah keluar |
| A18 | Identitas satu request bocor ke request lain lewat pool koneksi | Satu-satunya pintu transaksi memasang role dan `request.jwt.claims` secara lokal transaksi | Tes dua request berurutan di satu koneksi; tes paralel dengan banyak pengguna |
| A19 | Role aplikasi membaca atau mengubah tabel auth di luar alur auth, termasuk lewat SQL yang disusupkan yang menjalankan `set role` | Grant tabel `auth.*` hanya untuk role grup `terka_auth`. Alur auth memakai **login dan pool koneksi terpisah** (`terka_<env>_auth`) yang hanya anggota `terka_auth`; login aplikasi (`terka_<env>_app`) bukan anggotanya, jadi `set role terka_auth` dari transaksi aplikasi ditolak Postgres | pgTAP: `anon` dan `authenticated` ditolak di setiap tabel `auth.*`; tes Go: login aplikasi gagal `set role terka_auth` |
| A20 | Data rahasia di log | Log hanya mencatat `user_id`, hasil, dan kode kesalahan; tidak pernah email, kode, token, cookie | Tes menangkap log semua alur dan mencari setiap rahasia yang dipakai |
| A21 | Membedakan kode salah dari kode kedaluwarsa atau alamat terkunci | Satu kode kesalahan `invalid` untuk ketiganya, kecuali terkunci 24 jam yang dilaporkan `locked` hanya setelah verifikasi gagal ke-20 | Tes kode kesalahan per keadaan |
| A22 | Serangan waktu pada pencocokan kode | `hmac.Equal`; pencarian lewat alamat, bukan lewat kode | Tinjauan kode (tidak bisa diuji andal) |
| A23 | Isi request besar atau aneh | Batas 8 KiB untuk endpoint auth; JSON ketat (field tak dikenal, objek ganda, tipe salah → 400) | Tes isi terlalu besar, field asing, JSON rusak; fuzz test pengurai |
| A24 | Memalsukan IP untuk menghindari batas per IP | `CF-Connecting-IP` hanya dipercaya bila `TERKA_TRUST_CLOUDFLARE=true` (staging, produksi); selain itu alamat koneksi | Tes header diabaikan bila tidak dipercaya |
| A25 | Menanyakan "sesi ini dihapus?" untuk menebak sesi orang lain | `session_id` UUID acak; jawaban hanya boolean; batas laju per IP | Tes jawaban untuk sesi tak dikenal = `false`; tes batas laju |
| A27 | Membanjiri satu kotak surat lewat varian alamat (`nama+1@`, titik di Gmail) | Batas per alamat dan penguncian memakai kunci kotak surat kanonis: bagian `+…` dibuang, dan untuk `gmail.com`/`googlemail.com` titik di bagian lokal dibuang. Identitas akun tetap alamat yang dinormalkan biasa | Tes varian alamat berbagi satu kuota dan satu penguncian |
| A28 | Mengunci alamat orang lain dengan verifikasi asal-asalan | Verifikasi hanya dihitung gagal bila alamat punya kode aktif; tanpa kode aktif jawabannya `invalid` tanpa dicatat | Tes 100 verifikasi tanpa kode aktif tidak mengunci alamat |
| A29 | Login CSRF lewat tautan email: penyerang mengirim tautan kode untuk alamatnya sendiri, korban yang membukanya masuk ke akun penyerang dan riwayat lokalnya ikut terklaim | Kode terikat ke browser yang memintanya: `request` memasang cookie pengikat acak, `verify` (lewat tautan maupun kode yang diketik) hanya mencocokkan kode yang diminta dengan pengikat itu. Tanpa cookie pengikat, `verify` menjawab `other_browser` dan tidak membuat sesi | Tes tautan dari browser lain → `other_browser`, tanpa sesi; tes pengikat milik permintaan lain → `invalid` |
| A26 | Kunci rahasia lemah atau lupa diisi | Server menolak mulai di staging/produksi bila kunci HMAC < 32 byte atau kredensial wajib kosong | Tes konfigurasi |

## 6. Desain

### 6.1 Data

Migration baru (setelah yang ada), hanya di skema `auth`:

- `auth.users`: tambah `email_verified_at timestamptz` dan `last_sign_in_at timestamptz`.
- `auth.sessions`: sudah ada; tambah `absolute_expires_at timestamptz not null` dan `method text not
  null` (`google` atau `email`), karena layar akun klien menampilkan cara masuk sesi ini.
- `auth.email_codes`: `id uuid pk`, `email text not null` (sudah dinormalkan), `code_hmac bytea not null`,
  `binding_hash bytea not null` (SHA-256 pengikat browser, A29), `created_at`, `expires_at`, `used_at`
  (null selama aktif), `failed_attempts smallint not null default 0`, `request_ip inet`. Indeks pada
  `(email, created_at)` dan `(binding_hash)`.
- `auth.signin_failures`: `mailbox text` (kunci kotak surat kanonis, A27), `failed_at timestamptz`;
  satu baris per verifikasi gagal terhadap kode aktif (A28), untuk batas 20 per 24 jam. Indeks pada
  `(mailbox, failed_at)`. `auth.email_codes` juga menyimpan `mailbox` untuk batas permintaan.

Semua batas per alamat dan per layanan dihitung dari tabel ini, jadi tidak tereset saat deploy. Tugas
pembersih berkala menghapus kode, kegagalan, dan sesi yang sudah lewat masa berlakunya.

### 6.2 Role

Role grup baru `terka_auth` (NOLOGIN), dibuat bersama `anon` dan `authenticated` di
`deploy/postgres/init/00-roles.sql`, `dbtest`, dan init stack lokal. Setiap lingkungan mendapat login
ketiga, `terka_<env>_auth` (NOINHERIT, anggota `terka_auth` saja), dibuat `create-env-db.sh` dengan
kata sandinya sendiri; server membuka pool koneksi kedua dengan URL `TERKA_AUTH_DATABASE_URL`. Login
aplikasi `terka_<env>_app` tetap anggota `anon` dan `authenticated` saja. Migration memberi
`terka_auth` hak yang dibutuhkan atas tabel `auth.*`, dan hak membaca `public.devices` untuk
`session-removed`; `anon` dan `authenticated` tidak mendapat hak apa pun atas tabel `auth.*` (fungsi
`auth.uid()` dan `auth.jwt()` tetap bisa dipanggil).

Pintu transaksi di Go:

```go
// Sebagai pengguna: set local role authenticated + request.jwt.claims
func (db *DB) AsUser(ctx context.Context, s Session, fn func(pgx.Tx) error) error
// Tanpa pengguna: set local role anon
func (db *DB) AsAnon(ctx context.Context, fn func(pgx.Tx) error) error
// Alur auth, lewat pool login auth: set local role terka_auth, tanpa klaim pengguna
func (db *AuthDB) AsAuth(ctx context.Context, fn func(pgx.Tx) error) error
```

Tidak ada kode lain yang membuka transaksi atau memasang role.

### 6.3 Endpoint

Semua jawaban JSON memakai amplop kesalahan yang ada (`{"code","message"}`) dan `no-store`.

| Endpoint | Masukan | Berhasil | Kesalahan (`code`) |
| --- | --- | --- | --- |
| `POST /api/auth/email/request` | `{email, turnstile}` | 202 `{}` | `invalid_email` 400 · `captcha_failed` 400 · `too_soon` 429 + `Retry-After` · `rate_limited` 429 (per IP / layanan) |
| `POST /api/auth/email/verify` | `{email, code}` + cookie pengikat | 200 `{user_id, session_id, email, method}` + cookie sesi | `other_browser` 400 (tanpa cookie pengikat) · `invalid` 400 (salah, kedaluwarsa, sudah dipakai, tidak ada, pengikat tidak cocok) · `locked` 429 + `Retry-After` · `rate_limited` 429 |
| `GET /api/auth/google/start` | — | 303 ke Google + cookie sementara | `rate_limited` 429 |
| `GET /api/auth/google/callback` | `code`, `state` dari Google | 303 ke `/auth/callback` + cookie sesi | 303 ke `/auth/callback?error=cancelled` (pengguna membatalkan) · `?error=other_browser` (cookie sementara tidak ada) · `?error=failed` (selain itu) |
| `GET /api/auth/session` | cookie | 200 `{user_id, session_id, email, method}` | `signed_out` 401 |
| `POST /api/auth/logout` | cookie | 204 + cookie dihapus | (selalu 204, juga tanpa sesi) |
| `GET /api/auth/session-removed?session=<uuid>` | — | 200 `{removed: bool}` | `invalid_session` 400 · `rate_limited` 429 |

Pemetaan ke klien lama: `other_browser` dari `verify` → halaman `/masuk` menampilkan kode itu dengan
pesan untuk mengetiknya di aplikasi atau browser tempat kode diminta, tanpa login; `too_soon` → `tooSoon`, `captcha_failed` → `captchaFailed`, `invalid` dan
`locked` → `invalid`, `error=cancelled` → `cancelled`, `error=other_browser` → `otherBrowser`,
`signed_out` → `signedOut`, lainnya → `failed`.

`session-removed` mengikuti arti RPC lama: benar bila baris perangkat dengan `session_id` itu ditandai
`removed`. Karena di sistem baru sesi bisa dicabut lewat baris sesi, endpoint juga menjawab benar bila
perangkatnya `removed` walaupun sesinya sudah terhapus.

### 6.4 Alur kode email

1. Normalkan dan validasi alamat (A8). Periksa batas per IP (memori), lalu Turnstile (A7).
2. Dalam `AsAuth`: kunci baris penanda kotak surat (`pg_advisory_xact_lock` pada hash kunci kotak surat),
   periksa 60 detik dan 5 per jam per kotak surat, 80 per jam dan 90 per hari layanan; habiskan kode
   aktif yang terikat ke cookie pengikat browser ini bila ada (A3); buat pengikat baru (32 byte acak);
   sisipkan kode baru (angka acak dari `crypto/rand`, seragam tanpa bias modulo) dengan hash
   pengikatnya. Jawaban memasang cookie `__Host-terka_email_binding` (`HttpOnly`, `Secure`,
   `SameSite=Strict`, `Path=/`, 15 menit).
3. Kirim email setelah transaksi commit. Kegagalan kirim dicatat (tanpa alamat) dan dijawab 202 yang
   sama, supaya jawaban tidak membedakan keadaan (A5). Email memuat kode dan tautan
   `https://<host>/masuk#email=<alamat>&code=<kode>`.
4. `verify`: tanpa cookie pengikat → `other_browser`. Dalam `AsAuth`, kunci penanda kotak surat; tolak
   bila 20 kegagalan dalam 24 jam (`locked`); ambil kode aktif untuk alamat dan hash pengikat ini dengan
   `for update` (tidak ada → `invalid`, tidak dicatat sebagai kegagalan, A28); bandingkan HMAC dengan `hmac.Equal`; salah
   → tambah `failed_attempts` (hangus di 5) dan catat kegagalan; benar → tandai terpakai, cari atau buat
   pengguna (`email_verified_at` diisi), buat sesi, semuanya dalam transaksi yang sama (A2). Cookie
   pengikat dihapus setelah berhasil.

### 6.5 Alur Google

1. `start`: buat `state`, `nonce`, dan pemverifikasi PKCE acak; simpan ketiganya di cookie
   `__Host-terka_oauth` yang dienkripsi dan diautentikasi (AES-GCM, kunci dari konfigurasi),
   `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, 10 menit; alihkan ke Google dengan
   `code_challenge_method=S256` dan scope `openid email profile`.
2. `callback`: hapus cookie sementara apa pun hasilnya (sekali pakai); `error=access_denied` →
   `cancelled`; cookie tidak ada → `other_browser`; `state` tidak cocok atau kedaluwarsa → `failed`;
   tukar `code` dengan pemverifikasi PKCE; verifikasi token ID (A10); cari pengguna berdasarkan
   `google_sub`, lalu berdasarkan alamat dengan aturan A11. Penautan lewat alamat hanya ke pengguna yang
   `email_verified_at`-nya terisi; buat sesi.

### 6.6 Sesi

- Token 32 byte dari `crypto/rand`, dikodekan base64url, di cookie `__Host-terka_session` (`HttpOnly`,
  `Secure`, `SameSite=Lax`, `Path=/`, `Max-Age` 400 hari). Database menyimpan SHA-256-nya.
- Setiap request yang membawa cookie: cari hash, tolak bila lewat 90 hari tanpa dipakai atau lewat
  batas mutlak; perbarui `last_used_at` paling sering sekali per 10 menit.
- Login baru selalu membuat sesi baru; cookie sesi lama di browser yang sama dihapus dan barisnya
  dicabut (A14).
- Di lingkungan `local` (HTTP), atribut `Secure` dan awalan `__Host-` tetap dipakai; Chrome dan
  Firefox menerimanya di `http://localhost`. Bila Safari menolak, `local` memakai nama tanpa awalan
  (keputusan diambil saat implementasi, dicatat di kode).

### 6.7 Batas laju per IP

Token bucket di memori per IP (`golang.org/x/time/rate`): `email/request` 10 per jam, `email/verify`
30 per jam, `google/start` 30 per jam, `session-removed` 60 per jam. Peta dibersihkan berkala. Ini
lapisan kedua; batas yang melindungi kode ada di Postgres.

### 6.8 Email

SMTP dengan STARTTLS wajib ke Resend di staging dan produksi. Di lokal, `make dev` menambahkan
Mailpit ke `compose.dev.yaml` dan mengirim ke sana tanpa TLS. Templat teks dan HTML berbahasa
Indonesia diturunkan dari `supabase/templates/magic_link.html` repo lama, dirender dengan
`html/template`. Header dibangun dari nilai yang sudah divalidasi saja.

### 6.9 Konfigurasi

Variabel lingkungan baru: `TERKA_PUBLIC_URL`, `TERKA_AUTH_KEY` (≥ 32 byte acak, base64; diturunkan
menjadi kunci HMAC kode dan kunci enkripsi cookie sementara lewat HKDF dengan label berbeda),
`TERKA_GOOGLE_CLIENT_ID`, `TERKA_GOOGLE_CLIENT_SECRET`, `TERKA_TURNSTILE_SECRET`,
`TERKA_TURNSTILE_HOSTNAMES`, `TERKA_SMTP_URL`, `TERKA_MAIL_FROM`, `TERKA_TRUST_CLOUDFLARE`. Di
`local`, kunci Turnstile uji resmi dan Mailpit dipakai sebagai bawaan; di staging dan produksi
semuanya wajib (A26). Rahasia masuk ke `.env` lewat Ansible Vault.

## 7. Katalog Tes

Tes Go memakai Postgres sungguhan (`dbtest`), Mailpit atau penangkap SMTP di memori, penerbit OIDC
tiruan (`httptest` dengan kunci RSA buatan tes), dan server Turnstile tiruan. Nama tes menjelaskan
perilaku.

- **Kode email:** jalur berhasil; kode salah; kedaluwarsa (jam dimajukan); dipakai dua kali; 20
  verifikasi bersamaan untuk kode yang sama → tepat satu sesi (dijalankan dengan `-race`); 5 salah
  menghanguskan kode; 20 salah per 24 jam mengunci alamat, dan pulih setelah jendela lewat; kode lama
  ditolak setelah kode baru; permintaan kedua dalam 60 detik; keenam dalam satu jam; ke-81 di layanan;
  alamat lain tidak terpengaruh batas alamat satu.
- **Pengikat browser:** tautan dibuka di browser lain → `other_browser`, tanpa sesi, tanpa kegagalan
  tercatat; pengikat milik permintaan lain → `invalid`; permintaan dari browser kedua tidak
  menghanguskan kode browser pertama; atribut cookie pengikat; cookie pengikat dihapus setelah berhasil.
- **Batas layanan:** email ke-81 dalam satu jam dan ke-91 dalam satu hari UTC ditolak `rate_limited`.
- **Enumerasi:** `request` untuk alamat baru, terdaftar, dan terkunci menghasilkan status, isi, dan
  header yang identik.
- **Penyimpanan:** tidak ada kolom yang berisi kode mentah atau token mentah; kunci HMAC berbeda
  menolak kode yang sah.
- **Validasi alamat:** tabel alamat jahat (CR/LF, spasi, tanpa `@`, dua `@`, 255 karakter, karakter
  kontrol, header terselip); fuzz test normalisasi tidak pernah menghasilkan alamat dengan karakter
  terlarang.
- **Turnstile:** gagal, hostname salah, action salah, server Turnstile 500 atau habis waktu → ditolak
  dan tidak ada email.
- **Google:** jalur berhasil (pengguna baru, pengguna lama lewat `google_sub`, penautan lewat email
  terverifikasi); `state` hilang/salah/kedaluwarsa/dipakai ulang; cookie sementara diubah satu byte;
  tanda tangan token salah; `iss`, `aud`, `exp`, `nonce` salah; `email_verified` salah; sub berbeda
  untuk email yang sama; pengguna membatalkan; cookie sementara tidak ada; pengalihan akhir selalu ke
  `/auth/callback`.
- **Sesi:** atribut cookie; token tersimpan sebagai hash; sesi kedaluwarsa (90 hari, batas mutlak)
  ditolak; `last_used_at` diperbarui paling sering sekali per 10 menit; keluar mencabut sesi; login baru
  mencabut sesi lama di browser yang sama; cookie dengan token acak ditolak tanpa membedakan dari tanpa
  cookie.
- **Pintu transaksi:** identitas tidak bocor ke transaksi berikutnya di koneksi yang sama; 50 request
  paralel dari 10 pengguna masing-masing hanya melihat dirinya.
- **Grant (pgTAP):** `anon` dan `authenticated` ditolak membaca dan menulis setiap tabel `auth.*`;
  `terka_auth` bisa; RLS tabel `public` tetap berlaku.
- **CSRF dan HTTP:** POST lintas origin ditolak; isi lebih dari 8 KiB ditolak; field JSON asing,
  objek ganda, dan JSON rusak ditolak; fuzz test pengurai isi; `CF-Connecting-IP` diabaikan bila tidak
  dipercaya.
- **Log:** semua alur dijalankan dengan log ditangkap; tidak ada alamat, kode, token, atau isi cookie
  di dalamnya.
- **Konfigurasi:** staging dan produksi menolak mulai tanpa kredensial atau dengan kunci pendek.
- **session-removed:** perangkat `removed` → benar; sesi tidak dikenal → salah; UUID rusak → 400;
  batas laju.
- **Tanpa password:** pengganti `passwordless.test.sql` repo lama: tidak ada endpoint atau kolom yang
  menerima password.

Setelah semua hijau, satu tinjauan keamanan terpisah atas kode auth oleh peninjau baru, sebelum tahap
ini dianggap selesai.

## 8. Risiko yang Diterima Pemilik

Penyerang yang lolos Turnstile bisa mengunci login email satu alamat selama 24 jam (20 kegagalan), dan
penyerang dari beberapa IP bisa menghabiskan batas email layanan sehingga login email tidak tersedia
selama serangan. Pembobolan akun dan kehilangan data tidak mungkin lewat jalur ini; sesi yang ada, login
Google, dan latihan tanpa jaringan tidak terpengaruh. Pemilik menerima risiko ini (2026-11-13) dengan
pengaman: alert saat alamat terkunci atau batas layanan tercapai (dipasang di tahap 2), dan aturan batas
laju Cloudflare untuk `/api/auth/email/*` (ditambahkan ke `docs/runbooks/cloudflare.md` di tahap ini).

## 9. Batas di Luar Tahap Ini

- **Trace (tahap 2):** instrumentasi HTTP mencatat URL lengkap. Saat OpenTelemetry dipasang, query
  string `/api/auth/google/callback` dan semua isi request `/api/auth/*` wajib diredaksi, dan tes log A20
  diperluas ke atribut trace.
- **Migrasi dari Supabase (tahap 9):** hanya pengguna dengan `email_confirmed_at` yang disalin;
  `email_verified_at` diisi dari situ, `google_sub` dari `auth.identities` penyedia `google`. Baris
  yang belum terkonfirmasi tidak disalin.

## 10. Diserahkan ke Builder

- Susunan paket Go (`internal/auth/...`) dan nama fungsi di luar pintu transaksi.
- Pustaka SMTP (pustaka standar atau pustaka kecil yang terawat).
- Struktur templat email, selama teksnya berbahasa Indonesia dan memuat kode serta tautan.
- Interval tugas pembersih.

## 11. Yang Dibutuhkan dari Pemilik (setelah tahap ini, sebelum login di staging)

Client OAuth Google (boleh yang dipakai Supabase sekarang, dengan redirect URI baru
`https://staging.terka.my.id/api/auth/google/callback` dan `http://localhost:5173/api/auth/google/callback`),
kunci situs dan rahasia Turnstile untuk `staging.terka.my.id`, dan kredensial SMTP Resend.

## 12. Keputusan Pemilik

1. Desain ini disetujui (2026-11-13), dengan permintaan auth dibuat seaman mungkin dan dibuktikan
   dengan banyak kasus uji.
2. Kode email terikat ke browser yang memintanya (A29).
3. Risiko di bagian 8 diterima, dengan batas harian 90 email dan dua pengamannya.

## 13. Keputusan Perancang

1. Role grup `terka_auth` untuk tabel `auth.*`, bukan fungsi `security definer`.
2. Batas per alamat dan per layanan di Postgres; batas per IP di memori.
3. `state`, `nonce`, dan PKCE di cookie terenkripsi, bukan di tabel.
4. Batas mutlak sesi 400 hari, tambahan atas 90 hari tanpa dipakai dari spec.
5. Satu `TERKA_AUTH_KEY` diturunkan dengan HKDF menjadi kunci-kunci terpisah.
6. Login Google untuk email yang sudah dimiliki `google_sub` lain ditolak (A11).
7. `session-removed` juga benar bila sesinya sudah terhapus tetapi perangkatnya `removed`.
8. Kegagalan kirim email dijawab sama dengan berhasil (A5); kegagalan itu terlihat di log dan metrik.
9. Login dan pool koneksi terpisah untuk auth (A19), dari temuan peninjau: dengan satu login,
   `set role terka_auth` dari SQL yang disusupkan bisa menulis sesi untuk akun mana pun.
10. Kunci kotak surat kanonis untuk batas dan penguncian (A27); kegagalan hanya dihitung terhadap kode
    aktif (A28).
11. `method` sesi dicatat, karena klien lama menampilkannya.
