# Terka: Rewrite ke Go + Svelte di VPS Sendiri — Dokumen Desain

**Tanggal:** 2026-11-10 · **Status:** menunggu persetujuan pemilik
**Pembaca:** builder yang akan menyusun rencana implementasi tanpa bisa bertanya lagi ke pemilik.
**Acuan di repo ini:** `docs/Terka_TRD.md` (arsitektur sekarang), `docs/Terka_SRD.md` (aturan perilaku), `docs/Terka_ERD.md` (skema), `docs/Terka_Tests.md` (katalog tes), `supabase/migrations/` (skema fisik).

Di dokumen ini, **repo lama** berarti repo ini (Next.js + Supabase + Vercel) dan **repo baru** berarti proyek hasil rewrite.

---

## 1. Maksud dan Alasan

Terka ditulis ulang supaya seluruh sistemnya dikelola pemilik sendiri: server Go, klien Svelte, PostgreSQL biasa, dan Docker di satu VPS. Perilaku produk tidak berubah. Yang berubah adalah dengan apa ia dibangun dan di mana ia berjalan.

Proyek baru ini juga proyek belajar. Pemilik ingin memakainya untuk tiga hal: proses deployment yang benar, observability, dan keterampilan Go. Karena itu beberapa bagian sengaja lebih lengkap dari yang dibutuhkan beban Terka; bagian itu ditandai.

Repo lama tetap hidup sebagai jalur cadangan. VPS disewa sampai 2027-11-01 tanpa perpanjangan otomatis. Kalau tidak diperpanjang, atau VPS mati, Terka harus bisa kembali berjalan di Vercel + Supabase tanpa kehilangan data.

## 2. Tujuan, Bukan Tujuan, Anti-Tujuan

**Tujuan**

1. Fitur dan perilaku setara dengan repo lama, diukur dengan tes yang sudah ada (bagian 9).
2. Tetap local-first: latihan berjalan penuh tanpa jaringan, dan jalur menjawab soal tidak pernah menunggu server.
3. Tidak bergantung pada Supabase atau Vercel saat berjalan normal.
4. Satu perintah deploy yang bisa diulang, dengan staging, health check, dan rollback.
5. Log, metrik, dan trace dari server bisa dilihat di satu tempat, dengan alert yang sampai ke pemilik.
6. Data di Supabase selalu cukup baru untuk dipakai jalur cadangan.

**Bukan tujuan**

- Fitur produk baru, perubahan aturan belajar, atau perubahan tampilan.
- Memindahkan logika belajar ke server.
- Banyak server, Kubernetes, atau ketersediaan tinggi.
- Deploy tanpa jeda sama sekali. Jeda satu-dua detik saat ganti versi diterima, karena klien mencoba ulang sinkronisasi sendiri.
- Pemantauan sisi browser (RUM). Layar diagnostik yang ada dipertahankan.
- Mengirim email dari server sendiri. Relay email tetap pihak ketiga.

**Anti-tujuan** (gagal walaupun secara teknis jalan)

- Pengguna kehilangan riwayat, atau harus memulai ulang, karena perpindahan.
- Jalur cadangan tidak bisa dipakai karena skema atau versi mesin di dua repo sudah berbeda diam-diam.
- Data satu akun terbaca atau tertulis oleh akun lain.
- Sinkronisasi ikut gagal karena Supabase (mirror) sedang mati.
- Tumpukan observability membuat Postgres kehabisan memori.

## 3. Keadaan Sekarang

Terka adalah PWA local-first. Seluruh logika belajar (FSRS, replay, penilaian, antrean soal) berjalan di browser dan disimpan di IndexedDB lewat Dexie. Riwayat berupa log event immutable ber-UUID buatan klien; semua status kartu dihitung ulang dari log. Server hanya menyimpan salinan, menegakkan kepemilikan dan kuota, dan menjembatani antarperangkat.

| Bagian | Ukuran | Ketergantungan |
| --- | --- | --- |
| `src/core` | 84 file, ~6.000 baris | hanya `zod` dan `ts-fsrs`; tidak mengimpor `src/lib` atau komponen |
| `src/lib` | 69 file, ~6.800 baris | 13 file memakai `@supabase/supabase-js`; 3 file memakai React; store memakai `zustand/vanilla` |
| `src/components` + `src/app` | 85 file, ~13.900 baris | React 19, Next.js 16 (static export) |
| `supabase/migrations` | 12 file, 864 baris | 17 tabel, 10 trigger, 8 policy RLS, `pg_cron`, Realtime |
| `tests` | 174 file, ~22.800 baris | `tests/core` 82, `tests/lib` 61, `tests/scripts` 11, `tests/ui` 6, `tests/e2e` 14 |
| `supabase/tests` + `supabase/checks` | 10 file pgTAP + 9 tes integrasi | stack Supabase lokal |

Permukaan Supabase yang dipakai klien: 13 tabel lewat Data API (11 tabel di `src/lib/db/schema.ts`, ditambah `privacy_policy_versions` dan `progress_resets`), RPC `delete_user`, `reset_progress`, `remove_device`, `session_removed`, login Google (PKCE) dan kode OTP email, serta satu channel Realtime privat `sync:<user_id>` yang hanya membawa sinyal "ada perubahan".

Produksi saat ini hanya punya satu pengguna ber-akun, yaitu pemilik.

## 4. Batasan

- **VPS:** Tencent Cloud Lighthouse region Singapura, 2 vCPU, 2 GB RAM, 40 GB SSD. Kuota trafik keluar 512 GB per bulan, kecepatan puncak 20 Mbps. Sewa sampai 2027-11-01.
- **Domain:** `terka.my.id` tetap. IndexedDB terikat ke origin, jadi origin tidak boleh berubah.
- **Determinisme replay:** `ts-fsrs` tetap dikunci persis di 5.4.2 dan `@open-spaced-repetition/binding` di 0.6.0-beta.6. Menaikkannya bukan bagian rewrite ini. `ENGINE_VERSION` tetap 4 dan `formatVersion` ekspor tetap 4.
- **Versi:** Go 1.27 (terbaru saat dokumen ini ditulis: 1.27.3), Svelte 5, SvelteKit 3, PostgreSQL 18. SvelteKit 3 masih baru; API-nya wajib dicek ke dokumentasi paket yang terpasang, bukan dari ingatan.
- **Aturan kerja repo lama berlaku di repo baru:** Conventional Commits, tidak ada push atau commit tanpa persetujuan, linter tidak boleh dimatikan, komentar kode berbahasa Inggris dan hanya untuk alasan yang tidak terlihat dari kode.

## 5. Keputusan Pemilik

Ini diputuskan pemilik dalam percakapan desain dan tidak boleh diubah builder:

1. Go untuk server, Svelte untuk klien, PostgreSQL tanpa Supabase, Docker di VPS, versi Go terbaru.
2. Tetap local-first. Mesin belajar tetap TypeScript di browser; Go hanya server auth dan sinkronisasi.
3. Dua repo terpisah. Repo lama tidak diubah oleh rewrite dan tetap bisa di-deploy ke Vercel + Supabase.
4. Data di-mirror ke Supabase dalam dua tahap: mirror penuh tiap malam lebih dulu, lalu pekerja mirror langsung sebagai tahap terpisah. Keduanya harus sudah berjalan sebelum perpindahan produksi, supaya sistem baru tidak pernah menjadi server utama dengan jendela kehilangan 26 jam.
5. Server Go memakai router `net/http` bawaan, dengan sqlc + pgx untuk akses data. Fiber dan GORM tidak dipakai.
6. Cloudflare dipasang di depan VPS sebagai proxy dan cache.
7. Proyek ini dipakai untuk belajar deployment, observability, dan Go.
8. Kode email dibatasi 5 permintaan per alamat per jam dan 20 verifikasi gagal per alamat per 24 jam (bagian 6.6). Pemilik menerima bahwa batas kedua bisa dipakai orang lain untuk mengunci login email satu alamat selama sehari.

## 6. Arsitektur

### 6.1 Gambaran

```
Browser (PWA SvelteKit, statis)
  ├─ src/core  mesin belajar, fungsi murni         ← disalin dari repo lama
  ├─ Dexie / IndexedDB, antrean kirim, replay      ← disalin dari repo lama
  └─ fetch + EventSource ke origin yang sama
        │
   Cloudflare (proxy, cache aset, TLS tepi)
        │  443, hanya dari rentang IP Cloudflare
   VPS ── Caddy ── terka (satu binary Go)
                    ├─ berkas statis hasil build Svelte (di-embed)
                    ├─ /api/auth/*   Google OAuth, kode email, sesi cookie
                    ├─ /api/sync/*   kirim, tarik, SSE
                    ├─ /api/account/* reset, hapus akun, perangkat
                    └─ tugas latar: pembersihan kuota, mirror
                    │
                 PostgreSQL 18 (jaringan internal Docker saja)
                    │  keluar saja, terjadwal
                 Supabase (salinan untuk jalur cadangan)
```

Server tidak menghitung jadwal dan tidak menilai jawaban. Alasan keamanannya sama dengan TRD bagian 2: tidak ada data satu pengguna yang memengaruhi pengguna lain.

### 6.2 Repo Baru

Satu repo berisi seluruh sistem baru:

```
web/      SvelteKit: src/core, src/lib, src/components, src/routes, tests, scripts, data
server/   modul Go: cmd/terka, internal/..., migrations/, queries/
deploy/   Dockerfile, compose, Caddyfile, Ansible, konfigurasi observability
docs/     dokumen produk yang disalin, runbook
```

- Repo dimulai kosong; berkas disalin dari repo lama. Riwayat git repo lama tetap di repo lama.
- Di dalam `web/`, path `src/core` dan `src/lib` serta alias impor `@/` dipertahankan persis. Dengan begitu perubahan bisa dipindah antar-repo sebagai patch (`git apply --directory=web`).
- Dokumen di `docs/` disalin. TRD ditulis ulang untuk arsitektur baru sebagai bagian dari pekerjaan; SRD, PRD, Microcopy, dan Tests tetap berlaku apa adanya.

### 6.3 Klien

- SvelteKit dengan `adapter-static`, `trailingSlash: 'never'`, semua halaman di-prerender sebagai kerangka. Tidak ada kode server SvelteKit saat runtime.
- **URL tidak berubah:** `/`, `/beranda`, `/sesi`, `/cakupan`, `/statistik`, `/statistik/peta`, `/statistik/sulit`, `/referensi`, `/setelan`, `/setelan/akun`, `/setelan/parameter`, `/papan-tombol`, `/masuk`, `/auth/callback`, `/privasi`, `/tentang`.
- **Dipindah tanpa perubahan logika:** `src/core`, `src/rules.ts`, `src/data/plates.json`, skrip konten dan peta, dan seluruh `src/lib` kecuali yang disebut di bawah.
- **Ditulis ulang di `src/lib`:** 13 file yang memakai `supabase-js` (`lib/auth/*`, `lib/sync/*`, `lib/app/auth.ts`, `lib/app/sync.ts`) diganti klien API tipis berbasis `fetch` dan `EventSource`. Bentuk baris di kabel dipertahankan (bagian 6.7), jadi `lib/db/wire.ts` dan `lib/sync/server.ts` hampir tidak berubah. Tiga file yang memakai React (`lib/app/use-runtime.ts`, `lib/platform.ts`, `lib/flags.ts`) disesuaikan.
- **Store:** store `zustand/vanilla` dipertahankan. Satu adaptor kecil menghubungkannya ke reaktivitas Svelte.
- **Penyimpanan lokal:** nama database Dexie, versi skema 1, dan seluruh store tetap sama. Klien baru harus membuka database yang dibuat klien lama tanpa migrasi.
- **UI:** 62 file komponen dan 23 file `src/app` ditulis ulang ke Svelte 5, layar demi layar, dengan kelas Tailwind dan `globals.css` yang sama. Teks antarmuka mengikuti `docs/Terka_Microcopy.md`.
- **Font:** Space Grotesk, IBM Plex Sans, IBM Plex Mono, dan Barlow Semi Condensed disajikan dari origin sendiri dengan bobot yang sama. Tidak ada permintaan ke Google Fonts.
- **Service worker:** memakai service worker bawaan SvelteKit dengan precache penuh, disajikan di `/sw.js` seperti sekarang; di path lain, pemasangan yang sudah ada tidak pernah menerima versi baru. Tiga perilaku lama wajib dipertahankan:
  1. Tidak memanggil `skipWaiting` saat install. Versi baru menunggu sampai halaman mengirim pesan string `"SKIP_WAITING"`. Halaman lama (Next.js) yang masih terbuka mengirim pesan yang sama, jadi protokol ini juga yang membuat perpindahan mulus.
  2. Query string diabaikan saat mencocokkan halaman, supaya `/auth/callback?...` terbuka tanpa jaringan.
  3. Cache `workbox-precache-*` milik versi lama dihapus saat aktivasi.
- **Optimizer:** Web Worker dan berkas WebAssembly tetap dimuat dari origin sendiri dan ikut precache. Pembundelan worker khusus di `scripts/build/worker.ts` boleh diganti worker bawaan Vite kalau tes `optimizer.e2e.ts` lolos di bawah service worker.
- **Dibuang:** `@vercel/analytics`, `next/*`, `workbox-*`, `@supabase/supabase-js`, `vercel.json`.

### 6.4 Server Go

Satu binary `terka` dengan subperintah:

| Subperintah | Kerja |
| --- | --- |
| `serve` | HTTP: berkas statis, API, SSE, tugas latar |
| `migrate` | menerapkan migration SQL yang di-embed |
| `mirror` | mirror penuh ke Supabase (tahap satu) |

Aturan:

- Router `net/http` bawaan dengan pola metode + path. Satu tipe handler yang mengembalikan `error` dan satu penulis JSON; tidak ada framework.
- Akses data lewat pgx. Query untuk auth, akun, dan tugas latar ditulis sebagai SQL dan dibangkitkan dengan sqlc.
- Pustaka yang wajib dipakai, bukan ditulis sendiri: `golang.org/x/oauth2` dan `github.com/coreos/go-oidc/v3` untuk Google, SDK OpenTelemetry resmi untuk observability.
- Berkas statis di-embed ke binary. Penyaji berkas menangani URL bersih (`/sesi` → `sesi.html`), `404.html`, `Cache-Control: no-cache` untuk HTML dan `sw.js`, dan cache lama untuk aset ber-hash.
- Batas waktu baca dan tulis dipasang di server HTTP. Saat berhenti, server menutup koneksi SSE dengan rapi dan menunggu request yang berjalan.
- `/healthz` menjawab tanpa menyentuh database; `/readyz` memeriksa koneksi database.
- Pembersihan `usage_daily` yang sekarang dijalankan `pg_cron` menjadi tugas terjadwal di dalam `serve`, memanggil `public.cleanup_usage_daily()` sekali sehari.

### 6.5 Database

PostgreSQL 18 dalam container, datanya di volume Docker, tidak punya port terbuka ke luar jaringan Docker.

**Skema `public` dipertahankan.** Tabel, domain, constraint, indeks, trigger kuota, trigger LWW, fungsi `security definer`, policy RLS, dan grant per kolom disalin dari `supabase/migrations`. Ini kontrak dengan repo lama (bagian 8).

**Lapisan pengganti platform Supabase** dibuat oleh migration pertama di repo baru, sebelum migration `public`:

- Role tanpa login `anon` dan `authenticated`, dan role login `terka_app` yang boleh berpindah ke keduanya. `terka_app` bukan pemilik tabel dan tidak punya `BYPASSRLS`.
- Skema `auth` milik sendiri:
  - `auth.users` (`id`, `email` unik tanpa peduli huruf besar-kecil, `google_sub` unik boleh kosong, `created_at`)
  - `auth.sessions` (`id`, `user_id`, hash token, `created_at`, `last_used_at`, `expires_at`)
  - `auth.email_codes` (email, hash kode, kedaluwarsa, jumlah percobaan)
- Fungsi `auth.uid()` dan `auth.jwt()` yang membaca setelan transaksi `request.jwt.claims`, sama seperti di Supabase. Dengan ini policy, default `auth.uid()`, trigger `device_seen` yang membaca `session_id`, dan helper pgTAP (`tests.as_user`, `tests.as_session`) tetap bekerja.

**Tiga migration khusus Supabase diganti:** `jobs` (pg_cron) menjadi tugas di Go, `sync_signal` (policy `realtime.messages`) menjadi hub SSE, dan `passwordless` (trigger password di `auth.users`) tidak diperlukan karena tidak ada password.

**Satu-satunya pintu ke database untuk request pengguna** adalah fungsi pembuka transaksi yang, di dalam transaksi itu, berpindah ke role `authenticated` dan memasang `request.jwt.claims` (`sub`, `session_id`, `role`) secara lokal transaksi (`set_config(..., true)`). Memasangnya per koneksi dilarang: pool akan membawanya ke request pengguna lain. Endpoint tanpa login memakai role `anon` dengan cara yang sama.

**Role lain:** role pemilik hanya dipakai `terka migrate`; role baca-semua (`BYPASSRLS`, hanya `select`) hanya dipakai mirror dan cadangan.

**Migration:** berkas SQL polos tanpa anotasi alat, diurut menurut nama berkas, dijalankan apa adanya. Tidak ada migration turun; perubahan skema mengikuti pola tambah dulu, hapus belakangan (TRD bagian 7, "Migrasi bertahap"), supaya rollback image aplikasi tidak memerlukan rollback database.

### 6.6 Auth dan Sesi

Perilaku di sisi pengguna mengikuti TRD bagian 5.1, kecuali yang disebut di sini.

- **Google:** alur Authorization Code dengan PKCE, `state`, dan `nonce`, dijalankan server. `GET /api/auth/google/start` mengalihkan ke Google; `GET /api/auth/google/callback` memverifikasi token ID (tanda tangan, `iss`, `aud`, `exp`, `nonce`, `email_verified`), membuat sesi, lalu mengalihkan ke `/auth/callback`. Halaman `/auth/callback` di klien tetap menjalankan klaim data lokal dan penulisan persetujuan. Scope hanya `openid email profile`; nama dan foto tidak disimpan.
- **Kode email:** 6 digit, berlaku 15 menit, disimpan sebagai HMAC dengan kunci server, sekali pakai, hangus setelah 5 kali salah. Satu permintaan per alamat per 60 detik dan paling banyak 5 per alamat per jam; paling banyak 80 email per jam untuk seluruh layanan. Paling banyak 20 verifikasi gagal per alamat per 24 jam; setelah itu verifikasi kode untuk alamat itu ditolak sampai jendelanya lewat, sedangkan login Google tidak terpengaruh. Tanpa dua batas per alamat ini, penyerang yang terus meminta kode baru mendapat 7.200 tebakan per hari atas kode 6 digit; dengan keduanya, 20. Token Turnstile diverifikasi server sebelum email dikirim. Jawaban permintaan kode sama untuk alamat yang sudah maupun belum terdaftar.
- **Tautan di email:** email tetap memuat tautan dan kode. Tautannya membuka `/masuk` dengan alamat dan kode di fragmen URL, lalu halaman mengirimnya lewat `POST`. Tidak ada `GET` yang membuat sesi dari email, supaya pemindai email tidak menghanguskan kode.
- **Identitas:** pengguna dikenali lewat email terverifikasi. Login email lalu Google dengan alamat yang sama berakhir di `auth.users.id` yang sama.
- **Sesi:** token acak 256 bit di cookie `__Host-` dengan `HttpOnly`, `Secure`, `SameSite=Lax`. Server hanya menyimpan hash-nya. Sesi kedaluwarsa setelah 90 hari tanpa dipakai dan diperpanjang saat dipakai. Tidak ada JWT dan tidak ada refresh token.
- **`GET /api/auth/session`** mengembalikan `user_id`, email, dan `session_id` (bukan token). Klien menyimpan `session_id` seperti sekarang untuk deteksi perangkat yang dihapus.
- **Perangkat dihapus:** `remove_device()` tetap menghapus baris `auth.sessions` dalam transaksi yang sama, dan sekarang berlaku seketika. `GET /api/auth/session-removed?session=<id>` menggantikan RPC `session_removed`: tanpa login, hanya mengembalikan boolean, dan dibatasi lajunya.
- **Keluar, hapus akun, reset progres:** perilaku klien sama; pemanggilannya menjadi endpoint di bagian 6.7.
- **PWA di iOS:** batasan lama tetap (redirect Google bisa berakhir di Safari); kode email yang diketik di PWA tetap jalur utamanya.

### 6.7 API Sinkronisasi dan Sinyal

Aturan sinkronisasi di TRD bagian 5.2–5.4 tidak berubah: pemicu, urutan FK, batch 200 baris, idempotensi lewat UUID, cursor `server_seq` dengan riwayat overlap 5 menit, halaman 1.000 baris, LWW, karantina, dan dua jalur penerapan event.

Yang berubah hanya angkutannya. Daftar endpoint (nama boleh disesuaikan builder, operasinya tidak):

| Endpoint | Menggantikan |
| --- | --- |
| `POST /api/sync/push/{table}` | upsert `ignoreDuplicates` + baca ulang baris kanonis untuk duplikat |
| `POST /api/sync/runs/{id}/close` | update kolom penutup `practice_runs` |
| `PUT /api/sync/settings`, `PUT /api/sync/notes` | upsert LWW |
| `POST /api/sync/devices/{id}/touch`, `.../leave` | update `last_seen_at` dan tanda `left` |
| `GET /api/sync/pull/{table}?after=&limit=` | tarik per cursor |
| `GET /api/sync/snapshot` | tarik utuh: pengaturan, catatan, perangkat, persetujuan, versi kebijakan, epoch reset |
| `GET /api/sync/runs/ids?after=` | daftar id sesi untuk rekonsiliasi penutupan |
| `GET /api/sync/events` | SSE, pengganti channel Realtime |
| `POST /api/account/reset`, `DELETE /api/account`, `DELETE /api/account/devices/{id}` | RPC `reset_progress`, `delete_user`, `remove_device` |

- **Bentuk baris tidak berubah.** JSON `snake_case` yang sama dengan yang sekarang dikirim ke Data API. Server meneruskan JSON ke Postgres (`jsonb_populate_recordset`) dan mengembalikan hasil `to_jsonb`, sama seperti cara kerja Data API. Tidak ada pemetaan kolom per kolom di Go untuk jalur ini.
- **Daftar putih:** nama tabel diperiksa terhadap daftar tetap di Go. Kolom yang diisi server tetap ditolak oleh grant per kolom, karena request berjalan sebagai role `authenticated`.
- **Kesalahan:** jawaban gagal berbentuk `{ "code", "message" }` dengan status HTTP yang sama maknanya dengan sekarang. Semua kode `TK…` dari trigger dan fungsi diteruskan apa adanya (saat ini `TK001`–`TK009`), supaya `classify()` di `lib/sync/errors.ts` tetap berlaku. `TK010` ikut hilang bersama trigger `passwordless`. `429` membawa `Retry-After`.
- **Sinyal:** setelah transaksi push yang menulis minimal satu baris selesai, server mengirim event `changed` ke koneksi SSE lain milik akun itu. Klien tidak lagi menyiarkan sendiri. Sinyal tidak membawa data.
- **SSE:** server mengirim detak tiap ~25 detik (Cloudflare memutus koneksi yang diam 100 detik). Saat `EventSource` tersambung ulang, klien menjalankan satu putaran sinkronisasi, sama seperti aturan "bergabung lagi ke channel" sekarang. Hub hidup di memori proses.

### 6.8 Keamanan

Semua aturan TRD bagian 9 tetap berlaku. Tambahan dan perubahan:

- **CSRF:** `http.CrossOriginProtection` bawaan Go dipasang untuk semua metode tidak aman. Tidak ada `GET` yang mengubah data, kecuali callback Google yang dijaga `state` dan PKCE. CORS tidak dibuka.
- **Batas di Go:** ukuran isi request, laju per IP untuk endpoint auth, laju per akun untuk sinkronisasi, dan jumlah koneksi SSE per akun.
- **IP pengguna** dibaca dari `CF-Connecting-IP`. Ini hanya sah karena firewall VPS menerima port 443 dari rentang IP Cloudflare saja.
- **Header:** CSP minimal seketat sekarang, dengan `connect-src` hanya origin sendiri dan `https://challenges.cloudflare.com` untuk Turnstile; `'wasm-unsafe-eval'` tetap untuk optimizer. `frame-ancestors 'none'`, `nosniff`, `Referrer-Policy`, `Permissions-Policy`, dan HSTS dikirim sebagai header.
- **Log dan trace** tidak boleh memuat email, kode OTP, token sesi, cookie, atau `raw_answer`. Pengguna dicatat lewat `user_id`.
- **Rahasia:** client secret Google, kunci HMAC, kredensial SMTP, kunci rahasia Turnstile, kata sandi role database, dan string koneksi Supabase tidak pernah masuk image atau repo.
- **Image:** berjalan sebagai pengguna non-root dengan sistem berkas hanya-baca.
- **Tinjauan keamanan** atas kode auth dan sesi wajib dilakukan sebelum perpindahan produksi (bagian 10, tahap 9).

### 6.9 Deployment

Bagian ini sengaja lebih lengkap dari kebutuhan Terka, karena deployment adalah tujuan belajar.

- **Lingkungan:** lokal (Docker Compose dengan Postgres dan penangkap email), staging (`staging.terka.my.id`), dan produksi (`terka.my.id`). Staging dan produksi berjalan di VPS yang sama, dengan container aplikasi terpisah dan database terpisah di satu instance Postgres.
- **Image:** satu Dockerfile bertingkat: build web, build Go dengan hasil web di-embed, lalu image akhir minimal. Image dibangun di GitHub Actions dan disimpan di GHCR dengan tag SHA commit. Tidak pernah dibangun di VPS.
- **CI:** lint dan cek tipe (Go dan web), `go test` terhadap Postgres sungguhan, pgTAP, `bun test`, Playwright (Chromium dan WebKit) terhadap binary Go, pemindaian kerentanan dependensi dan image.
- **CD:** `main` yang lolos CI ter-deploy ke staging otomatis. Produksi di-deploy dengan persetujuan manual. Langkahnya: tarik image, jalankan `terka migrate` sebagai langkah tersendiri, ganti container, tunggu `/readyz`, dan kembali ke image sebelumnya kalau gagal.
- **VPS:** disiapkan dengan playbook Ansible yang bisa diulang: pengguna dan SSH hanya-kunci, firewall, swap 2 GB, Docker, pembaruan keamanan otomatis, direktori dan berkas compose.
- **Memori:** setiap container diberi batas memori. Postgres disetel untuk mesin 2 GB.
- **Cloudflare:** proxy aktif untuk `terka.my.id` dan `staging.terka.my.id`; TLS mode Full (strict) dengan sertifikat Origin CA di Caddy. Aturan cache: aset ber-hash di-cache lama; HTML dan `sw.js` selalu divalidasi ulang; `/api/*` tidak pernah di-cache. `www` dialihkan ke apex.
- **Email:** Resend lewat SMTP dengan pengirim `no-reply@terka.my.id`, seperti sekarang. Template email berbahasa Indonesia dipindah dari `supabase/templates/magic_link.html`.
- **Statistik kunjungan:** dari dasbor Cloudflare. Tidak ada skrip analitik di klien.
- **Kebijakan privasi:** satu versi baru yang berlaku untuk kedua cara hosting. Isinya menyebut VPS (Tencent Cloud, Singapura), Cloudflare, Resend, salinan di Supabase, dan Vercel sebagai jalur cadangan. Versi ini dipasang di **kedua repo** sebelum perpindahan: konstanta `PRIVACY_POLICY_VERSION` di `src/rules.ts`, baris di `privacy_policy_versions` dengan `requires_reconsent = true`, dan halaman `/privasi`. Kalau hanya dipasang di repo baru, klien lama setelah pindah ke cadangan hanya menawarkan versi lama, dan `has_current_consent()` menahan sinkronisasinya.

### 6.10 Observability

Juga sengaja lebih lengkap dari kebutuhan, karena ini tujuan belajar.

- **Di Go:** log terstruktur JSON lewat `log/slog` dengan `trace_id`; trace untuk setiap request HTTP dan query database; metrik laju, kesalahan, dan durasi per endpoint.
- **Metrik khusus Terka:** baris terkirim dan ditarik per tabel, penolakan per kode `TK`, koneksi SSE aktif, login berhasil dan gagal per metode, email terkirim dan gagal, umur mirror terakhir, umur cadangan terakhir.
- **Di VPS:** satu kolektor, Prometheus, Loki, Tempo, dan Grafana, ditambah metrik host, container, Postgres, dan Caddy. Retensi 7–15 hari dengan batas ukuran disk.
- **Grafana tidak dibuka ke publik.** Aksesnya lewat terowongan SSH atau lapisan akses Cloudflare.
- **Alert** ke pemilik: layanan mati, tingkat 5xx, latensi, disk di atas 80%, tekanan memori atau container dimatikan kernel, Postgres mati, cadangan atau mirror lebih tua dari 26 jam, kegagalan kirim email.
- **Pemantau dari luar VPS:** VPS tidak bisa melaporkan kematiannya sendiri. Satu pemeriksa terjadwal di luar VPS memanggil `/healthz`.
- **Kalau memori tidak cukup,** Tempo dilepas lebih dulu.

### 6.11 Cadangan, Mirror, dan Jalur Cadangan

**Cadangan.** `scripts/ops/backup.sh` (`pg_dump | age`) dipakai lagi dan berjalan harian di VPS. Tujuh salinan terakhir disimpan lokal, dan setiap salinan dikirim ke penyimpanan di luar VPS. Kunci privat `age` tidak disimpan di VPS.

**Mirror tahap satu (malam).** `terka mirror` berjalan terjadwal. Dalam satu transaksi di database Supabase, ia menyamakan `auth.users`, lalu mengganti isi seluruh tabel pengguna di skema `public` dan katalog `privacy_policy_versions` dengan isi dari VPS. Katalog itu ikut disalin karena `privacy_consents` merujuknya lewat foreign key. `user_id`, `id`, `received_at`, dan `server_seq` disalin persis; sequence disetel ke nilai tertinggi. Trigger kuota dan persetujuan dilewati selama penyalinan. Karena isinya diganti utuh, reset progres dan hapus akun ikut tersalin tanpa logika khusus. Penggantian utuh adalah jalan pintas yang sah untuk ukuran data sekarang; diganti penyalinan selisih kalau satu putaran melebihi beberapa menit.

**Mirror tahap dua (langsung).** Pekerja latar di dalam `serve` menyalin baris baru per tabel berdasarkan `server_seq`, dibangunkan oleh sinyal yang sama dengan SSE, dengan `on conflict do nothing` dan percobaan ulang. Kegagalannya tidak pernah menyentuh jalur sinkronisasi pengguna. Mirror malam tetap berjalan sebagai pencocok penuh.

**Supabase dalam keadaan siaga.** Selama VPS menjadi server utama, tidak ada klien yang menulis ke proyek Supabase.

**Runbook pindah ke cadangan** wajib ditulis dan dilatih di staging:

1. Jalankan mirror terakhir kalau VPS masih hidup.
2. Naikkan semua sequence `server_seq` di Supabase dengan jarak besar. Cursor di perangkat bisa lebih tinggi dari nomor terakhir di Supabase; tanpa jarak ini, baris baru mendapat nomor di bawah cursor dan tidak pernah ditarik.
3. Aktifkan lagi login di proyek Supabase.
4. Deploy repo lama ke Vercel.
5. Arahkan DNS `terka.my.id` ke Vercel tanpa proxy.
6. Login ulang di tiap perangkat. Data lokal tetap ada karena origin dan skema Dexie sama.

Runbook arah sebaliknya (kembali ke VPS) juga ditulis.

**Batas yang diketahui.** Kalau VPS mati sebelum mirror terakhir sempat jalan, baris yang belum tersalin tidak ada di Supabase. Perangkat yang membuatnya masih menyimpannya secara lokal, tetapi sudah mengeluarkannya dari antrean kirim, dan klien tidak punya jalur kirim ulang seluruh riwayat (satu-satunya kirim ulang adalah `requeueLostCloses` di `src/lib/sync/reconcile.ts`; kirim ulang penuh yang disebut TRD bagian 6 belum pernah dibuat). Baris itu tidak sampai ke perangkat lain. Dengan mirror malam saja, jendelanya sampai sekitar 26 jam; dengan mirror langsung, beberapa detik. Karena itu mirror langsung wajib berjalan sebelum perpindahan produksi (bagian 10). Jendela beberapa detik yang tersisa diterima; jalur kirim ulang seluruh riwayat tidak dibuat.

### 6.12 Migrasi Data dan Perpindahan Produksi

Hanya ada satu akun, jadi perpindahan dilakukan sekali, tanpa masa jalan ganda.

1. Latih seluruh urutan di staging dengan salinan data produksi sampai berulang tanpa kesalahan.
2. Berhenti memakai aplikasi dan matikan login di proyek Supabase, supaya klien lama yang masih terpasang tidak menulis lagi ke sana. Lalu ambil `pg_dump` data dari Supabase dan daftar `auth.users` (id, email, `sub` Google). Semua akun yang ada saat itu ikut disalin, berapa pun jumlahnya.
3. Muat ke Postgres baru dengan `user_id`, `received_at`, dan `server_seq` yang sama; setel sequence.
4. Cocokkan jumlah baris per tabel dengan sumbernya.
5. Tambahkan redirect URI baru di klien OAuth Google; redirect URI Supabase tidak dihapus.
6. Arahkan DNS ke Cloudflare + VPS.
7. Buka aplikasi di tiap perangkat: service worker baru terpasang lewat protokol `SKIP_WAITING`, pengguna login ulang, dan sinkronisasi berlanjut dari cursor yang sama.
8. Bandingkan ekspor JSON sebelum dan sesudah perpindahan.

Token Supabase yang tertinggal di `localStorage` dihapus klien baru saat pertama dibuka.

## 7. Diserahkan ke Builder

Builder bebas memutuskan hal berikut tanpa bertanya:

- Susunan paket di dalam `server/internal`, nama endpoint, dan bentuk jawaban sukses.
- Alat migration, selama berkas SQL dijalankan apa adanya dan terkunci terhadap dua proses sekaligus.
- Kerangka tes komponen Svelte untuk pengganti 6 tes `tests/ui`.
- Cara menyimpan rahasia di VPS dan di CI.
- Angka batas laju dan ukuran request, selama batch 200 baris yang sah tidak tertolak.
- Susunan kolektor dan dasbor observability.
- Alat penjadwal untuk cadangan dan mirror malam.
- Cara server uji E2E memalsukan versi service worker (pengganti `/__sw-version` di `tests/e2e/support/serve.ts`).

## 8. Kontrak Bersama Dua Repo

Selama jalur cadangan dianggap hidup, tiga hal harus sama di kedua repo:

1. **Skema `public` di Postgres:** tabel pengguna, kolom, tipe, dan constraint.
2. **Skema Dexie:** nama database, nomor versi, dan store.
3. **Mesin belajar:** `ENGINE_VERSION`, isi `src/core`, versi `ts-fsrs`, dan `plates.json`.

Perubahan salah satunya di repo baru wajib disertai perubahan sepadan di repo lama, atau dicatat sebagai keputusan sadar untuk mengakhiri jalur cadangan. Mirror malam yang gagal karena kolom tidak cocok adalah tanda pertama skema sudah menyimpang; alert-nya ada di bagian 6.10.

## 9. Pengujian dan Tolok Ukur Setara

Rewrite dianggap setara dengan repo lama ketika semua ini hijau:

| Tes | Nasib |
| --- | --- |
| `tests/core` (82), `tests/scripts` (11) | dipindah tanpa perubahan, tetap `bun test` |
| `tests/lib` (61) | dipindah; 10 file yang memakai `supabase-js` disesuaikan dengan klien API baru |
| `tests/ui` (6) | ditulis ulang untuk Svelte |
| `tests/e2e` (9 spesifikasi) | dipindah; dijalankan terhadap binary Go. `analytics.e2e.ts` dibuang |
| pgTAP (10 file) | 8 dipindah dan dijalankan terhadap Postgres dengan lapisan pengganti. `passwordless.test.sql` dan `sync_signal.test.sql` menguji migration yang diganti; perilakunya diuji ulang sebagai tes Go (auth tanpa password, isolasi sinyal SSE antar-akun) |
| `supabase/checks` (auth, push, pull, sync, removal, signal, ops) | ditulis ulang sebagai tes Go terhadap server sungguhan |
| `supabase/checks` (docs, enums) | dipindah; tetap membandingkan skema dengan kode dan dokumen |

Tambahan yang belum ada di repo lama:

- Tes bahwa identitas transaksi tidak bocor ke request berikutnya pada koneksi yang sama.
- Tes bahwa pengguna A tidak bisa membaca atau menulis baris pengguna B lewat setiap endpoint.
- Tes auth: kode salah, kedaluwarsa, dipakai dua kali, percobaan berlebih per kode, permintaan keenam dalam satu jam untuk satu alamat, verifikasi setelah 20 kegagalan dalam 24 jam, `state` atau `nonce` tidak cocok.
- Tes mirror dengan dua database Postgres.
- Uji manual di iPhone sungguhan untuk login di PWA terpasang.

Nama tes menjelaskan perilaku saja, tanpa ID katalog.

## 10. Urutan Pengerjaan

Rencana implementasi ditulis per tahap, sesaat sebelum tahap itu dikerjakan, supaya tahap belakangan memakai pelajaran dari tahap sebelumnya. Jalur deploy dan observability dibangun lebih dulu supaya dipakai sepanjang proyek.

| Tahap | Isi | Selesai ketika |
| --- | --- | --- |
| 0. Uji risiko | Tiga percobaan sekali buang (bagian 11) | Ketiganya punya jawaban tertulis |
| 1. Kerangka berjalan | Repo baru, Go minimal, Postgres + migration + lapisan pengganti, image, CI, Ansible, Cloudflare, staging | Commit ke `main` sampai ke staging sendiri; pgTAP hijau; rollback teruji |
| 2. Observability dasar | Instrumentasi, tumpukan di VPS, dasbor, alert, pemantau luar | Alert uji sampai ke pemilik; satu request terlihat sebagai trace |
| 3. Auth | Google, kode email, sesi, Turnstile, batas laju | Tes auth hijau; login dua metode jalan di staging |
| 4. Sinkronisasi | Kirim, tarik, SSE, operasi akun | Tes kontrak server hijau |
| 5. Klien dasar | Kerangka SvelteKit, pindah `core`/`lib`/tes, klien API baru, service worker | `bun test` hijau; dua browser saling sinkron lewat staging |
| 6. Port UI | Layar demi layar | E2E hijau di Chromium dan WebKit |
| 7. Cadangan dan mirror malam | Cadangan harian, `terka mirror`, runbook | Latihan pindah ke cadangan berhasil di staging |
| 8. Mirror langsung | Pekerja latar, metrik selisih, alert | Baris baru di staging muncul di salinannya dalam hitungan detik; Supabase mati tidak mengganggu sinkronisasi |
| 9. Perpindahan produksi | Tinjauan keamanan, migrasi data, DNS | Ekspor sebelum dan sesudah cocok; mirror langsung berjalan di produksi |

Tahap 3–4 (server) dan 5–6 (klien) tidak saling bergantung sampai integrasi dan boleh dikerjakan berselang.

## 11. Risiko dan Hal yang Belum Diverifikasi

Tiga yang pertama dijawab di tahap 0, karena jawabannya bisa mengubah desain.

1. **Izin tulis mirror di Supabase.** Belum diuji apakah role `postgres` di paket Free boleh mengisi `auth.users` langsung, menulis ke kolom identitas `generated always`, dan melewati trigger saat menyalin. Belum diuji juga apakah semua penyedia login bisa dimatikan tanpa mengganggu penulisan itu. Kalau tidak bisa, tahap 7 perlu dirancang ulang.
2. **SvelteKit statis di bawah service worker dan CSP.** Belum diuji apakah worker optimizer dan WebAssembly-nya termuat tanpa jaringan di WebKit dengan worker bawaan Vite.
3. **Memori tumpukan observability.** Perkiraan 400–700 MB belum diukur di VPS.
4. **Kode auth buatan sendiri** adalah bagian paling berisiko. Penangkalnya ada di bagian 6.6, 6.8, dan 9.
5. **Satu VPS.** Tidak ada server cadangan otomatis. Akibat VPS mati adalah sinkronisasi tertunda, bukan latihan terhenti.
6. **Kontrak dua repo dijaga dengan disiplin,** bukan dengan alat. Mirror hanya mendeteksi penyimpangan skema server, bukan skema Dexie atau versi mesin.
7. **Ukuran build Svelte** belum diketahui. Build Next.js sekarang sekitar 2,3 MB terkompresi untuk pemasangan penuh.

## 12. Keputusan Perancang

Ini diputuskan perancang, bukan pemilik. Pemilik belum menyetujuinya satu per satu:

1. Repo baru dimulai kosong, bukan dari riwayat git repo lama.
2. RLS dan trigger dipertahankan di Postgres, dengan skema `auth` buatan sendiri, alih-alih memindahkan aturan ke Go.
3. Jalur sinkronisasi meneruskan JSON ke Postgres tanpa struct Go per kolom. sqlc dipakai untuk query auth, akun, dan tugas latar.
4. Alat migration harus menjalankan SQL polos; goose tidak dipakai karena menuntut anotasi di tiap berkas.
5. Sesi berupa cookie dengan masa 90 hari tanpa dipakai.
6. Tautan di email membuka `/masuk` dengan kode di fragmen, bukan tautan yang langsung membuat sesi.
7. Server sendiri yang mengirim sinyal SSE setelah push, bukan klien.
8. Staging berbagi VPS dan instance Postgres dengan produksi.
9. Statistik kunjungan dari dasbor Cloudflare; tidak ada penghitung sendiri.
10. Mirror malam mengganti isi tabel secara utuh.
11. Rencana implementasi ditulis per tahap, bukan satu rencana besar.
12. PostgreSQL 18, bukan 17 seperti proyek Supabase sekarang.
13. Kebijakan privasi baru berupa satu versi untuk kedua repo dan meminta persetujuan ulang. Ini satu-satunya perubahan yang disentuhkan ke repo lama oleh rewrite.
14. Saat perpindahan, login di Supabase dimatikan sebelum data diambil.

## 13. Pertanyaan untuk Pemilik

Tidak ada yang terbuka. Dua pertanyaan dari peninjauan sudah dijawab pemilik pada 2026-11-10: mirror langsung dikerjakan sebelum perpindahan produksi (bagian 5 butir 4, bagian 10), dan VPS berada di region Singapura (bagian 4).
