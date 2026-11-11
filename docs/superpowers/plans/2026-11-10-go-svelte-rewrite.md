# Terka Go + Svelte — Tahap 0 dan 1 (Uji Risiko dan Kerangka Berjalan) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Menjawab tiga risiko teknis desain, lalu membuat repo baru yang setiap commit ke `main`-nya sampai sendiri ke `staging.terka.my.id` sebagai binary Go dengan Postgres, migration, dan tes pgTAP yang hijau.

**Architecture:** Satu repo baru (`terka-go`) berisi `server/` (Go, router `net/http`), `web/` (SvelteKit statis, masih halaman pengganti), dan `deploy/` (Docker, Caddy, Ansible). Skema `public` disalin dari repo lama dan berjalan di atas lapisan pengganti platform Supabase (role `anon`/`authenticated`, skema `auth` buatan sendiri). Image dibangun di GitHub Actions, disimpan di GHCR, dan dipasang ke VPS lewat satu skrip deploy dengan health check dan rollback.

**Tech Stack:** Go 1.27.3, pgx v5, PostgreSQL 18, pgTAP, SvelteKit 3 + Svelte 5 + `adapter-static`, Bun 1.4, Docker + Compose, Caddy 2, Ansible, GitHub Actions, GHCR, Cloudflare.

**Spec:** `docs/superpowers/specs/2026-11-10-go-svelte-rewrite.md`. Rencana ini hanya mencakup tahap 0 dan 1 dari bagian 10 spec. Tahap 2–9 mendapat rencana sendiri.

## Global Constraints

- **Repo lama** = `/Users/dikym/Code/side/terka`. **Repo baru** = `/Users/dikym/Code/side/terka-go`, GitHub `dikym/terka-go` (privat), modul Go `github.com/dikym/terka-go/server`, image `ghcr.io/dikym/terka-go`. Nama `terka-go` keputusan perancang.
- Repo lama tidak diubah oleh rencana ini.
- DNS `terka.my.id` dan `www.terka.my.id` tidak disentuh. Produksi tetap di Vercel + Supabase sampai tahap 9.
- Versi: Go 1.27.3, PostgreSQL 18, Svelte 5, SvelteKit 3. API SvelteKit 3 dicek ke dokumentasi paket yang terpasang, bukan dari ingatan.
- Router `net/http` bawaan; tidak ada framework HTTP dan tidak ada ORM.
- Rahasia tidak pernah masuk repo (kecuali terenkripsi Ansible Vault) dan tidak pernah masuk image.
- Linter dan pemeriksa tipe tidak boleh dimatikan. Komentar kode berbahasa Inggris, hanya untuk alasan yang tidak terlihat dari kode, tanpa rujukan dokumen.
- Nama tes menjelaskan perilaku saja.
- **Commit:** Conventional Commits, huruf kecil, satu baris subject, tanpa body, tanpa `Co-Authored-By`. Setiap langkah "Commit" berarti: tampilkan pesannya, tunggu persetujuan pemilik, baru `git add` dan `git commit`.
- **Push tidak pernah dilakukan agen.** Langkah bertanda **(pemilik)** dikerjakan pemilik atau baru dikerjakan setelah pemilik memintanya secara eksplisit: `git push`, membuat repo GitHub, akses VPS, dasbor Cloudflare, dasbor dan kredensial Supabase.
- Hasil tiap uji risiko ditulis ke `docs/spikes/` di repo baru. Kode uji risiko dibuang setelah hasilnya ditulis.

## Review Focus

Lima keadaan yang tersirat di spec tetapi mudah terlewat; tiap baris punya tes di task pemiliknya.

1. Dua deploy menjalankan migration bersamaan → tiap berkas diterapkan tepat sekali (Task 6).
2. Image lama dijalankan setelah rollback dan menemukan versi migration yang tidak dikenalnya → tetap jalan, tidak gagal (Task 6).
3. Postgres tidak terjangkau saat server hidup → `/healthz` tetap 200, `/readyz` 503, proses tidak mati (Task 4).
4. Permintaan berkas statis dengan path aneh (`..`, garis miring di akhir, direktori) → tidak ada berkas bocor; jawabannya 404 atau 308 (Task 5).
5. Deploy pertama gagal sehat padahal belum ada versi sebelumnya → skrip berhenti dengan kode keluar sendiri, tidak mencoba rollback ke kosong (Task 13).

---

## Peta Berkas Repo Baru

```
CLAUDE.md, README.md, Makefile, .gitignore
docs/                      salinan docs/ repo lama + spec + rencana ini
docs/spikes/               hasil tiga uji risiko
docs/runbooks/             cloudflare.md, deploy.md
server/go.mod
server/cmd/terka/main.go   subperintah: serve, migrate, health, version
server/internal/config/    membaca environment
server/internal/httpx/     tipe handler, amplop kesalahan, penulis JSON
server/internal/server/    perakitan rute dan middleware
server/internal/web/       penyaji berkas statis; dist/ diisi hasil build web
server/internal/migrate/   penjalan migration
server/internal/dbtest/    database sekali pakai untuk tes
server/migrations/         SQL yang di-embed
server/tests/pgtap/        tes pgTAP
web/                       SvelteKit statis (halaman pengganti)
deploy/Dockerfile, deploy/postgres-test.Dockerfile
deploy/compose.dev.yaml, compose.infra.yaml, compose.app.yaml, Caddyfile
deploy/postgres/init/00-roles.sql
deploy/scripts/            create-env-db.sh, deploy.sh, deploy_test.sh, pgtap.sh
deploy/ansible/            site.yml, inventory.example.ini, roles/{base,docker,terka}
.github/workflows/         ci.yml, deploy.yml
```

---

# Tahap 0 — Uji Risiko

### Task 1: Repo baru dan aturan kerja

**Files:**
- Create: `CLAUDE.md`, `README.md`, `Makefile`, `.gitignore`, `docs/**`, `docs/spikes/.gitkeep`, `docs/runbooks/.gitkeep`

**Interfaces:**
- Produces: direktori repo; target `make` yang diisi task berikutnya: `web`, `build`, `test`, `pgtap`, `lint`.

- [ ] **Step 1:** Buat `/Users/dikym/Code/side/terka-go`, jalankan `git init -b main`.
- [ ] **Step 2:** Salin `docs/*.md` dan `docs/Terka_ERD.puml` dari repo lama ke `docs/`. Salin spec dan rencana ini ke `docs/superpowers/specs/` dan `docs/superpowers/plans/`.
- [ ] **Step 3:** Tulis `CLAUDE.md` dari `CLAUDE.md` repo lama dengan dua perubahan. Bagian "Code Formatting & Quality" menjadi:
  - Server (di `server/`): `gofmt -l .` harus kosong, `go vet ./...`, `golangci-lint run`, `go test ./...`
  - Web (di `web/`): `bun run check`

  Bagian Git, komentar kode, dan Superpowers disalin apa adanya.
- [ ] **Step 4:** Tulis `.gitignore`: `node_modules/`, `web/build/`, `web/.svelte-kit/`, `server/internal/web/dist/*` kecuali `.gitkeep`, `server/bin/`, `*.env`, `.env*`, `deploy/ansible/inventory.ini`, `spikes/`.
- [ ] **Step 5:** Verifikasi: `git status --short` menampilkan hanya berkas di atas; `grep -R "supabase.co" docs/ | wc -l` boleh lebih dari 0 (TRD lama), tetapi `git ls-files | grep -c '\.env'` harus `0`.
- [ ] **Step 6:** Commit: `chore: scaffold repository with docs and working rules`

### Task 2: Uji risiko — izin tulis mirror di Supabase

Menjawab risiko 1 di bagian 11 spec. Tanpa TDD; ini percobaan.

**Files:**
- Create: `spikes/mirror-permissions.sql` (tidak di-commit), `docs/spikes/2026-11-mirror-permissions.md`

**Interfaces:**
- Consumes: `SUPABASE_DB_URL`, string koneksi langsung role `postgres` ke proyek Supabase **bukan produksi** yang sudah diberi migration repo lama (`supabase db push`).
- Produces: tabel jawaban yang dipakai rencana tahap 7.

- [ ] **Step 1 (pemilik):** Sediakan proyek Supabase Free untuk uji (yang staging, atau buat baru), terapkan migration repo lama, dan berikan `SUPABASE_DB_URL` lewat environment, bukan lewat berkas di repo.
- [ ] **Step 2:** Tulis `spikes/mirror-permissions.sql`. Tiap pemeriksaan berjalan dalam transaksinya sendiri dan diakhiri `rollback`, kecuali disebut lain:
  - **A.** `insert into auth.users (instance_id, id, aud, role, email, email_confirmed_at, created_at, updated_at)` dengan `instance_id` nol, `aud` dan `role` `'authenticated'`.
  - **B.** `set local session_replication_role = replica`.
  - **C.** `alter table public.review_logs disable trigger user` (hanya dinilai kalau B gagal).
  - **D.** Dengan trigger dilewati: sisipkan satu `devices` dan satu `review_logs` memakai `overriding system value`, dengan `server_seq = 999999` dan `received_at` tetap; baca ulang dan pastikan keduanya tersimpan persis.
  - **E.** `select setval(pg_get_serial_sequence('public.review_logs','server_seq'), 1999999)`, dan `setval` untuk sequence biasa milik `practice_runs`.
  - **F.** `truncate public.review_logs, public.review_voids cascade`.
- [ ] **Step 3:** Jalankan `psql "$SUPABASE_DB_URL" -f spikes/mirror-permissions.sql` dan catat hasil tiap huruf.
- [ ] **Step 4 (pemilik):** Pemeriksaan **G**: commit-kan satu baris A (tanpa rollback) untuk email milik pemilik, lalu login dengan kode OTP ke proyek uji lewat klien repo lama. Lulus kalau `user.id` sesi sama dengan `id` yang disisipkan.
- [ ] **Step 5 (pemilik):** Pemeriksaan **H**: matikan penyedia Email dan Google di dasbor proyek uji. Lulus kalau login ditolak dan A–F tetap berhasil.
- [ ] **Step 6:** Tulis `docs/spikes/2026-11-mirror-permissions.md`: tabel A–H (lulus/gagal, pesan kesalahan persis), lalu kesimpulan. Aturan keputusan: kalau A gagal, atau B dan C sama-sama gagal, atau G gagal, **berhenti dan laporkan ke pemilik**; rancangan mirror di spec bagian 6.11 harus diubah sebelum tahap 7.
- [ ] **Step 7:** Hapus baris uji dari proyek Supabase; hapus `spikes/mirror-permissions.sql`.
- [ ] **Step 8:** Commit: `docs: record mirror write permission findings`

### Task 3: Uji risiko — SvelteKit statis, service worker, dan optimizer

Menjawab risiko 2 di bagian 11 spec. Batas waktu: satu hari kerja.

**Files:**
- Create: `spikes/sveltekit-pwa/**` (tidak di-commit), `docs/spikes/2026-11-sveltekit-pwa.md`

**Interfaces:**
- Produces: konfigurasi SvelteKit yang terbukti (dipakai Task 5 dan tahap 5), dan jawaban atas lima pertanyaan di Step 5.

- [ ] **Step 1:** Buat proyek SvelteKit minimal dengan TypeScript di `spikes/sveltekit-pwa` (`bunx sv create`; periksa `bunx sv --help` dulu). Pasang `@sveltejs/adapter-static`, `trailingSlash: 'never'`, prerender semua halaman, dan `kit.csp` mode hash dengan `script-src 'self' 'wasm-unsafe-eval'`, `object-src 'none'`.
- [ ] **Step 2:** Tambah dua rute: `/` dan `/sesi`. Di `/`, tombol "Jalankan" membuat `new Worker(new URL('./optimizer.worker.ts', import.meta.url), { type: 'module' })`. Worker memuat `@open-spaced-repetition/binding` versi persis `0.6.0-beta.6` (beserta `binding-wasm32-wasip1`), memanggil satu fungsi pustaka, dan mengirim hasilnya; halaman menampilkannya di elemen `data-testid="result"`. Cara memuat WebAssembly dicontoh dari `src/lib/optimizer/optimizer.worker.ts` dan `scripts/build/worker.ts` di repo lama.
- [ ] **Step 3:** Tulis service worker SvelteKit: precache `build`, `files`, dan `prerendered`; cache-first; cocokkan navigasi tanpa query string dan dengan URL bersih; **tanpa** `skipWaiting` saat install; `message` bernilai string `"SKIP_WAITING"` memanggil `skipWaiting()`. Daftarkan sendiri dari halaman (pendaftaran otomatis SvelteKit dimatikan).
- [ ] **Step 4:** Tulis satu tes Playwright dengan proyek `chromium` (Desktop Chrome) dan `webkit` (iPhone 13), terhadap hasil build yang disajikan server statis sederhana dengan `Cache-Control: no-cache` untuk berkas service worker:
  - muat `/`, tunggu service worker mengendalikan halaman, `context.setOffline(true)`, muat ulang, klik "Jalankan" → `result` terisi;
  - buka `/sesi?x=1` saat offline → halaman `/sesi` tampil;
  - tidak ada pesan konsol yang memuat `Content Security Policy`;
  - build kedua dengan isi berbeda → `registration.waiting` terisi dan halaman tidak berganti versi sampai mengirim `"SKIP_WAITING"`.
- [ ] **Step 5:** Tulis `docs/spikes/2026-11-sveltekit-pwa.md` yang menjawab:
  1. Nama berkas keluaran untuk rute `/sesi` dan nama berkas service worker.
  2. Apakah CSP hash muncul sebagai `<meta>` di halaman prerender.
  3. Apakah worker bawaan Vite lolos di kedua browser saat offline. Kalau gagal di salah satu, catat kesalahannya; tahap 5 memakai pembundelan worker terpisah seperti repo lama.
  4. Apakah protokol `SKIP_WAITING` bekerja.
  5. Isi `svelte.config.js` dan versi paket yang dipakai.
- [ ] **Step 6:** Hapus `spikes/sveltekit-pwa`.
- [ ] **Step 7:** Commit: `docs: record sveltekit offline and optimizer findings`

---

# Tahap 1 — Kerangka Berjalan

### Task 4: Kerangka server Go

**Files:**
- Create: `server/go.mod`, `server/cmd/terka/main.go`, `server/cmd/terka/main_test.go`
- Create: `server/internal/config/config.go`, `config_test.go`
- Create: `server/internal/httpx/httpx.go`, `httpx_test.go`
- Create: `server/internal/server/server.go`, `server_test.go`
- Create: `server/.golangci.yml`
- Modify: `Makefile` (target `build`, `test`, `lint`)

**Interfaces:**
- Produces:
  ```go
  // config
  type Config struct{ Env, Addr, DatabaseURL, MigrateDatabaseURL string }
  func Load(getenv func(string) string) (Config, error)

  // httpx
  type Handler func(w http.ResponseWriter, r *http.Request) error
  type Error struct{ Status int; Code, Message string } // implements error
  func Handle(h Handler, log *slog.Logger) http.Handler
  func JSON(w http.ResponseWriter, status int, v any) error

  // server
  type Deps struct {
      Version, Env string
      Ping    func(context.Context) error
      Static  http.Handler
      Logger  *slog.Logger
  }
  func New(d Deps) http.Handler

  // main
  func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int
  ```
- Environment: `TERKA_ENV` (wajib; `local`, `staging`, atau `production`), `TERKA_ADDR` (bawaan `:8080`), `TERKA_DATABASE_URL`, `TERKA_MIGRATE_DATABASE_URL`. Versi disuntik saat build: `-ldflags "-X main.version=<sha>"`, bawaan `dev`.
- `TERKA_DATABASE_URL` wajib untuk `serve` di `staging` dan `production`. Di `local` boleh kosong; `/readyz` lalu menjawab 503.
- `make build` menghasilkan `server/bin/terka`.

- [ ] **Step 1: Tulis tes yang gagal**

```go
// config_test.go
func TestLoadRejectsUnknownEnv(t *testing.T)      // TERKA_ENV=prod → error memuat "TERKA_ENV"
func TestLoadDefaultsAddr(t *testing.T)           // tanpa TERKA_ADDR → cfg.Addr == ":8080"

// httpx_test.go
func TestHandleWritesErrorEnvelope(t *testing.T) {
	h := httpx.Handle(func(http.ResponseWriter, *http.Request) error {
		return &httpx.Error{Status: 409, Code: "TK006", Message: "the session is already closed"}
	}, slog.New(slog.DiscardHandler))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 409 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"code":"TK006","message":"the session is already closed"}` {
		t.Fatalf("body = %s", got)
	}
}
func TestHandleHidesUnknownErrors(t *testing.T)   // errors.New("secret detail") → 500, body {"code":"internal","message":"internal error"}, tanpa "secret"
func TestHandleRecoversPanics(t *testing.T)       // panic → 500 dengan amplop yang sama

// server_test.go
func TestHealthzIgnoresDatabase(t *testing.T)     // Ping selalu gagal → GET /healthz 200, body {"status":"ok","version":"abc"}
func TestReadyzReportsDatabase(t *testing.T)      // Ping gagal → 503 {"code":"not_ready",...}; Ping nil → 200
func TestReadyzBoundsThePing(t *testing.T)        // ctx yang diterima Ping punya deadline ≤ 2 detik
func TestUnknownAPIPathIsJSON(t *testing.T)       // GET /api/nope → 404 application/json {"code":"not_found",...}, bukan HTML
func TestSecurityHeaders(t *testing.T)            // setiap jawaban: X-Content-Type-Options nosniff; Referrer-Policy strict-origin-when-cross-origin;
                                                  // Permissions-Policy "camera=(), microphone=(), geolocation=()"; CSP memuat "frame-ancestors 'none'"
func TestNonProductionIsNotIndexed(t *testing.T)  // Env staging → X-Robots-Tag noindex; production → header tidak ada
func TestHSTSOutsideLocal(t *testing.T)           // staging dan production → Strict-Transport-Security max-age=31536000; local → tidak ada

// main_test.go
func TestServeStopsWhenContextEnds(t *testing.T)  // run(ctx, ["serve"]) dengan TERKA_ADDR=127.0.0.1:0; cancel → kembali 0 dalam 5 detik
func TestServeStartsWithoutDatabase(t *testing.T) // TERKA_DATABASE_URL menunjuk port tertutup → proses tetap melayani /healthz 200 dan /readyz 503
func TestServeRequiresDatabaseURLOutsideLocal(t *testing.T) // TERKA_ENV=staging tanpa TERKA_DATABASE_URL → kode 1, stderr memuat nama variabelnya
func TestUnknownCommandFails(t *testing.T)        // run(["nope"]) → 2, stderr memuat daftar subperintah
```

- [ ] **Step 2:** Jalankan `cd server && go test ./...` → gagal kompilasi karena paket belum ada.
- [ ] **Step 3:** Implementasikan `config`, `httpx`, dan `server` sesuai antarmuka. Rute: `GET /healthz`, `GET /readyz`, `/api/` (404 JSON), sisanya ke `Deps.Static`. Middleware berurutan: pemulih panik, log akses `slog` JSON (metode, pola rute, status, durasi), header keamanan.
- [ ] **Step 4:** Implementasikan `run` dengan subperintah `serve`, `version`, dan `health`. Sampai Task 5, `Deps.Static` diisi `http.NotFoundHandler()`. `serve` membuat `pgxpool` tanpa menunggu koneksi pertama, memasang `http.Server` dengan `ReadHeaderTimeout` 5 detik, `ReadTimeout` 30 detik, `IdleTimeout` 120 detik, dan berhenti lewat `Shutdown` dengan batas 20 detik saat `SIGINT`/`SIGTERM`. `health` memanggil `GET http://127.0.0.1<port dari TERKA_ADDR>/readyz` dan keluar 0 hanya untuk status 200 (dipakai health check container; image akhir tidak punya `curl`).
- [ ] **Step 5:** Jalankan `go test ./...` → PASS. Jalankan `gofmt -l .` → kosong; `go vet ./...` dan `golangci-lint run` → tanpa temuan.
- [ ] **Step 6:** Commit: `feat(server): add http skeleton with health endpoints`

### Task 5: Penyaji berkas statis dan halaman pengganti

**Files:**
- Create: `server/internal/web/web.go`, `web_test.go`, `server/internal/web/dist/.gitkeep`
- Create: `web/**` (kerangka SvelteKit dari konfigurasi yang terbukti di Task 3)
- Modify: `server/cmd/terka/main.go` (pasang `web.Handler`), `Makefile` (target `web`)

**Interfaces:**
- Consumes: jawaban 1 dan 5 dari `docs/spikes/2026-11-sveltekit-pwa.md`.
- Produces:
  ```go
  // dist di-embed dengan `//go:embed all:dist`
  func Dist() fs.FS
  func Handler(files fs.FS, version string) http.Handler
  ```
  `make web` menjalankan `bun install --frozen-lockfile && bun run build` di `web/`, lalu mengganti isi `server/internal/web/dist/` dengan `web/build/`.

- [ ] **Step 1: Tulis tes yang gagal** (tabel, dengan `fstest.MapFS` berisi `index.html`, `sesi.html`, `auth/callback.html`, `404.html`, `plates.json`, `_app/immutable/a.js`, dan berkas service worker dengan nama dari Task 3)

| Permintaan | Status | Isi / header |
| --- | --- | --- |
| `GET /` | 200 | isi `index.html`; `Cache-Control: no-cache` |
| `GET /sesi` | 200 | isi `sesi.html` |
| `GET /auth/callback?code=1` | 200 | isi `auth/callback.html` |
| `GET /sesi/` | 308 | `Location: /sesi` |
| `GET /nope` | 404 | isi `404.html` |
| `GET /_app/immutable/a.js` | 200 | `Cache-Control: public, max-age=31536000, immutable` |
| `GET /sw.js` | 200 | isi berkas service worker; `Cache-Control: no-cache` |
| `GET /plates.json` | 200 | `Cache-Control: no-cache`; `ETag: "v1"` |
| `GET /plates.json` + `If-None-Match: "v1"` | 304 | tanpa isi |
| `GET /../404.html`, `GET /_app` (direktori) | 404 | isi `404.html` |
| `HEAD /` | 200 | tanpa isi |
| `POST /` | 405 | — |

- [ ] **Step 2:** Jalankan `go test ./internal/web/` → gagal.
- [ ] **Step 3:** Implementasikan `Handler`. Aturan pencarian: `/` → `index.html`; `/x` → berkas `x` kalau ada dan bukan direktori, lalu `x.html`; `/sw.js` → berkas service worker hasil build kalau `sw.js` tidak ada. `ETag` untuk berkas di luar `_app/immutable/` adalah `version` dalam tanda kutip, karena berkas hasil embed tidak punya waktu ubah.
- [ ] **Step 4:** Jalankan `go test ./internal/web/` → PASS.
- [ ] **Step 5:** Buat `web/` sebagai SvelteKit statis dengan satu halaman `/` bertuliskan "Terka" dan satu halaman `404`. Skrip `package.json`: `build`, `check` (`svelte-check`). Belum ada Tailwind, service worker, atau kode dari repo lama; itu tahap 5.
- [ ] **Step 6:** Jalankan `make web && make build`, lalu `TERKA_ENV=local TERKA_ADDR=127.0.0.1:8080 ./server/bin/terka serve` dan `curl -s localhost:8080/ | grep -c Terka` → `1`; `curl -s -o /dev/null -w '%{http_code}' localhost:8080/nope` → `404`.
- [ ] **Step 7:** Commit: `feat(server): serve the embedded web build`

### Task 6: Lapisan pengganti, migration, dan penjalannya

**Files:**
- Create: `server/migrations/00000000000000_platform.sql`, `server/migrations/README.md`, `server/migrations/embed.go`
- Create (salinan dari `supabase/migrations/` repo lama): `20261004000001_foundation.sql`, `20261004000002_user_tables.sql`, `20261004000003_access.sql`, `20261004000004_rules.sql`, `20261026000001_recheck.sql`, `20261028000001_device_removal.sql`, `20261030000002_reset_progress.sql`, `20261101000001_device_sessions.sql`, `20261101000002_usage_bytes.sql`
- Create: `server/internal/migrate/migrate.go`, `migrate_test.go`
- Create: `server/internal/dbtest/dbtest.go`
- Create: `deploy/postgres-test.Dockerfile`, `deploy/compose.dev.yaml`, `deploy/postgres/init/00-roles.sql`
- Modify: `server/cmd/terka/main.go` (subperintah `migrate`)

**Interfaces:**
- Produces:
  ```go
  // migrations
  var Files embed.FS // semua *.sql di direktori ini

  // migrate
  func Apply(ctx context.Context, conn *pgx.Conn, files fs.FS) (applied []string, err error)

  // dbtest
  type DB struct{ OwnerURL, AppURL string }
  func New(t *testing.T) DB // database baru per tes, dihapus saat tes selesai
  ```
- `dbtest.New` membaca `TERKA_TEST_DATABASE_URL` (superuser). Tes gagal, bukan dilewati, kalau variabel itu kosong. Ia memastikan role `anon` dan `authenticated` ada, membuat role login pemilik (bukan superuser) dan role login aplikasi (`noinherit`, anggota `anon` dan `authenticated`), lalu database milik role pemilik. Role berlaku untuk seluruh klaster, jadi nama role login dibuat unik per tes dan dihapus bersama databasenya.
- `deploy/compose.dev.yaml`: layanan `postgres` dari `deploy/postgres-test.Dockerfile` (`postgres:18` + paket pgTAP dan `pg_prove`), port host `5433`, kata sandi `postgres`, volume di `/var/lib/postgresql` (image 18 memindahkan lokasinya dari `/var/lib/postgresql/data`; periksa README image), `deploy/postgres/init/` dipasang ke `/docker-entrypoint-initdb.d`. Nilai lokal: `TERKA_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5433/postgres`.

**Keputusan yang tidak bisa ditebak:**
- Migration **tidak pernah membuat role**; role pemilik bukan superuser. `anon` dan `authenticated` dibuat `00-roles.sql` (klaster) atau `dbtest` (tes).
- Catatan versi disimpan di `terka.schema_migrations (version text primary key, applied_at timestamptz not null default now())`, bukan di skema `public`, supaya `public` tetap sama dengan repo lama.
- Versi = nama berkas tanpa `.sql`. Berkas dijalankan urut nama, masing-masing dalam satu transaksi, lewat protokol sederhana supaya satu berkas boleh berisi banyak pernyataan.
- Kunci: `pg_advisory_lock` dengan satu kunci tetap, dipegang sepanjang `Apply`.
- Versi yang tercatat tetapi berkasnya tidak ada **diabaikan**: itu keadaan normal setelah rollback image.
- Perbedaan salinan dari repo lama, dicatat di `server/migrations/README.md`:
  1. `foundation`: baris `create extension if not exists pg_cron;` dihapus.
  2. `jobs`, `sync_signal`, `passwordless`: tidak disalin.
- Isi `00000000000000_platform.sql`:
  - `create schema auth`; `grant usage on schema auth to anon, authenticated`
  - `auth.users (id uuid primary key default gen_random_uuid(), email text not null, google_sub text unique, created_at timestamptz not null default now())` dengan indeks unik pada `lower(email)`
  - `auth.sessions (id uuid primary key default gen_random_uuid(), user_id uuid not null references auth.users (id) on delete cascade, token_hash bytea not null unique, created_at timestamptz not null default now(), last_used_at timestamptz not null default now(), expires_at timestamptz not null)` dengan indeks pada `user_id`
  - `auth.jwt() returns jsonb`: isi setelan `request.jwt.claims`, atau `'{}'` kalau kosong
  - `auth.uid() returns uuid`: `auth.jwt() ->> 'sub'`, atau null
  - `anon` dan `authenticated` tidak mendapat grant apa pun pada tabel `auth.*`
  - `auth.email_codes` belum dibuat; ia datang bersama auth di tahap 3.

- [ ] **Step 1: Tulis tes yang gagal** (`migrate_test.go`, memakai `dbtest.New` dan `fstest.MapFS`)

```go
func TestApplyRunsFilesInNameOrder(t *testing.T)        // 2_b.sql bergantung pada tabel dari 1_a.sql → applied == ["1_a","2_b"]
func TestApplyIsIdempotent(t *testing.T)                // Apply kedua → applied kosong, err nil
func TestApplyRollsBackAFailedFile(t *testing.T)        // berkas: create table t1(); select 1/0; → err memuat nama berkas;
                                                        // to_regclass('t1') null; versi tidak tercatat
func TestApplyIgnoresUnknownRecordedVersions(t *testing.T) // sisipkan versi "9_future" ke tabel catatan → Apply tetap nil
func TestApplySerializesConcurrentRuns(t *testing.T)    // dua koneksi memanggil Apply bersamaan atas 3 berkas → keduanya nil,
                                                        // tabel catatan berisi tepat 3 baris
func TestEmbeddedMigrationsApplyAsOwner(t *testing.T) {
	// Apply(migrations.Files) lewat OwnerURL (bukan superuser) → nil. Lalu:
	//   to_regclass('public.review_logs') tidak null
	//   to_regclass('auth.sessions') tidak null
	//   select auth.uid() → null
	//   select count(*) from pg_namespace where nspname in ('cron','realtime') → 0
	//   select count(*) from pg_roles where rolname like 'terka%' and rolsuper → 0
}
func TestClaimsDriveAuthUid(t *testing.T)               // lewat AppURL: begin; set local role authenticated;
                                                        // set_config('request.jwt.claims','{"sub":"<uuid>","session_id":"<uuid>"}',true)
                                                        // → auth.uid() == uuid; auth.jwt()->>'session_id' terisi; setelah commit → auth.uid() null
```

- [ ] **Step 2:** `docker compose -f deploy/compose.dev.yaml up -d --wait postgres`, lalu `go test ./internal/migrate/` → gagal.
- [ ] **Step 3:** Tulis `dbtest`, `migrate.Apply`, `00000000000000_platform.sql`, dan salin sembilan migration dengan perbedaan di atas.
- [ ] **Step 4:** Jalankan `go test ./internal/migrate/` → PASS. Kalau `TestEmbeddedMigrationsApplyAsOwner` gagal karena migration salinan membutuhkan hak superuser, jangan ubah berkas salinan; laporkan pernyataannya ke pemilik.
- [ ] **Step 5:** Tambah subperintah `migrate` di `main.go`: membuka satu koneksi ke `TERKA_MIGRATE_DATABASE_URL`, memanggil `Apply(migrations.Files)`, mencetak tiap versi yang diterapkan, keluar 0 atau 1. Tes: `TestMigrateCommandRequiresItsURL` (tanpa variabel → kode 1, stderr memuat `TERKA_MIGRATE_DATABASE_URL`).
- [ ] **Step 6:** `go test ./...` → PASS.
- [ ] **Step 7:** Commit: `feat(server): add platform shim, schema migrations and runner`

### Task 7: Tes pgTAP

**Files:**
- Create (salinan dari `supabase/tests/database/` repo lama): `server/tests/pgtap/00000_helpers.sql`, `access.test.sql`, `device_sessions.test.sql`, `foundation.test.sql`, `performance.test.sql`, `reset.test.sql`, `rules.test.sql`, `user_tables.test.sql`
- Create: `deploy/scripts/pgtap.sh`, `server/tests/pgtap/README.md`
- Modify: `Makefile` (target `pgtap`)

**Interfaces:**
- Consumes: `terka migrate` (Task 6), layanan `postgres` di `deploy/compose.dev.yaml`.
- Produces: `make pgtap`, keluar 0 hanya kalau semua tes lulus.

**Perbedaan dari repo lama** (dicatat di `README.md`; berkas lain disalin tanpa perubahan):
- `00000_helpers.sql`: `tests.create_user` menyisipkan `(id, email)` saja; `tests.add_session` menyisipkan `(id, user_id, token_hash, expires_at)` dengan `token_hash = sha256(id::text::bytea)` dan `expires_at = now() + interval '90 days'`.
- `performance.test.sql`: sisipan ke `auth.users` memakai `(id, email)` saja.
- `rules.test.sql`: asersi `cron.job` ("the cleanup runs daily") dihapus dan `plan(53)` menjadi `plan(52)`.
- `passwordless.test.sql` dan `sync_signal.test.sql` tidak disalin.

- [ ] **Step 1:** Tulis `deploy/scripts/pgtap.sh`:
  1. hapus dan buat ulang database `terka_pgtap` sebagai superuser `postgres`;
  2. `create schema extensions` dan `alter database terka_pgtap set search_path = "$user", public, extensions` (berkas helper memasang pgTAP di skema `extensions`, seperti di Supabase);
  3. jalankan `terka migrate` terhadap `terka_pgtap` **sebagai `postgres`**, supaya `tests.as_owner()` (`set role postgres`) menunjuk pemilik tabel;
  4. jalankan `pg_prove -U postgres -d terka_pgtap --ext .sql /tests` di dalam container, dengan `server/tests/pgtap` dipasang ke `/tests`.
- [ ] **Step 2:** Salin delapan berkas dan terapkan empat perbedaan di atas.
- [ ] **Step 3:** Jalankan `make pgtap`. Harapan: `All tests successful.` dengan `Files=8, Tests=157` (1 + 21 + 20 + 11 + 2 + 12 + 52 + 38).
- [ ] **Step 4:** Kalau ada tes gagal dengan `permission denied` untuk sequence, tabel, atau fungsi: penyebab yang paling mungkin adalah hak bawaan Supabase yang diberikan diam-diam ke `anon`/`authenticated`. Perbaikannya **migration baru** `server/migrations/20261110000001_explicit_grants.sql` berisi grant yang persis dibutuhkan, bukan mengubah berkas salinan atau tes. Catat tiap grant di `server/migrations/README.md`. Kegagalan jenis lain: berhenti dan laporkan ke pemilik.
- [ ] **Step 5:** Jalankan `make pgtap` lagi → `All tests successful.`
- [ ] **Step 6:** Commit: `test(server): port database tests to plain postgres`

### Task 8: Image Docker

**Files:**
- Create: `deploy/Dockerfile`, `.dockerignore`
- Modify: `Makefile` (target `image`)

**Interfaces:**
- Consumes: `make web` (Task 5), `run` dengan subperintah `serve`, `migrate`, `health` (Task 4, 6).
- Produces: image dengan `ENTRYPOINT ["/terka"]`, `CMD ["serve"]`, `HEALTHCHECK` yang menjalankan `/terka health` tiap 5 detik. Argumen build `VERSION` mengisi `main.version`.

- [ ] **Step 1:** Tulis `deploy/Dockerfile` tiga tingkat: `oven/bun:1.4` membangun `web/`; `golang:1.27.3` menyalin hasilnya ke `server/internal/web/dist` dan membangun dengan `CGO_ENABLED=0`; tingkat akhir `gcr.io/distroless/static-debian12:nonroot` berisi satu binary.
- [ ] **Step 2:** Jalankan `docker build -f deploy/Dockerfile --build-arg VERSION=test -t terka-go:test .`
- [ ] **Step 3:** Verifikasi:
  - `docker run --rm terka-go:test version` → `test`
  - `docker run -d --name t -e TERKA_ENV=local -p 8080:8080 terka-go:test`, lalu `curl -s localhost:8080/healthz` → `{"status":"ok","version":"test"}`
  - `docker inspect -f '{{.Config.User}}' terka-go:test` → pengguna non-root
  - `docker run --rm --read-only -e TERKA_ENV=local terka-go:test version` → `test` (jalan dengan sistem berkas hanya-baca)
  - `docker history --no-trunc terka-go:test | grep -ci 'password\|secret'` → `0`
  - `docker images terka-go:test --format '{{.Size}}'` dicatat di pesan PR/ringkasan
- [ ] **Step 4:** Commit: `build: add multi-stage image for the server and web build`

### Task 9: CI

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: target `make` dari task sebelumnya.
- Produces: pada push ke `main` yang lolos semua job, image `ghcr.io/dikym/terka-go:<sha 40 karakter>`.

- [ ] **Step 1:** Tulis `ci.yml`, dipicu `push` ke `main` dan `pull_request`, `permissions: contents: read` (job `image` menambah `packages: write`):

| Job | Isi |
| --- | --- |
| `server` | Go 1.27.3; `gofmt -l .` kosong; `go vet`; `golangci-lint`; Postgres dijalankan sebagai langkah (`docker compose -f deploy/compose.dev.yaml up -d --wait postgres`), bukan lewat `services:`, karena image-nya dibangun dari Dockerfile di repo; `go test ./...` dengan `TERKA_TEST_DATABASE_URL`; `govulncheck ./...` |
| `web` | Bun 1.4; `bun install --frozen-lockfile`; `bun run check`; `bun run build` |
| `pgtap` | `make pgtap` |
| `image` | butuh ketiga job di atas; `docker build`; pemindai image gagal pada temuan HIGH atau CRITICAL yang sudah punya perbaikan; push ke GHCR hanya pada push ke `main` |

  Job `scripts` (shellcheck dan tes skrip deploy) ditambahkan di Task 13, saat skripnya ada.

- [ ] **Step 2:** Verifikasi lokal sebelum push: `make lint test pgtap image` → semuanya keluar 0.
- [ ] **Step 3:** Commit: `ci: add lint, test, database and image jobs`
- [ ] **Step 4 (pemilik):** Buat repo privat `dikym/terka-go`, tambahkan remote, `git push -u origin main`.
- [ ] **Step 5 (pemilik):** Verifikasi di GitHub: kelima job hijau, dan `ghcr.io/dikym/terka-go:<sha>` muncul di Packages.

### Task 10: Penyiapan VPS dengan Ansible

**Files:**
- Create: `deploy/ansible/site.yml`, `inventory.example.ini`, `ansible.cfg`, `group_vars/all/main.yml`, `group_vars/all/vault.yml` (terenkripsi), `roles/base/tasks/main.yml`, `roles/docker/tasks/main.yml`, `roles/terka/tasks/main.yml`, `roles/terka/templates/env.j2`
- Create: `docs/runbooks/deploy.md` (bagian "Menyiapkan VPS")

**Interfaces:**
- Produces di VPS: pengguna `deploy` (anggota grup `docker`); direktori `/opt/terka/{infra,staging,production,bin,backups}`; `/opt/terka/staging/.env` mode `600`; Docker dengan rotasi log.

**Keputusan yang tidak bisa ditebak:**
- Sistem operasi yang didukung: Ubuntu LTS. Langkah pertama memeriksa `ansible_distribution`; kalau bukan Ubuntu atau Debian, berhenti dan tanyakan pemilik.
- Dua pengguna: pengguna admin (nama dari variabel `admin_user`, kunci publik pemilik, `sudo`) dan `deploy` (grup `docker`, tanpa `sudo`). Jalan pertama playbook masuk sebagai pengguna bawaan image (`-u root` atau `-u ubuntu`); jalan berikutnya sebagai pengguna admin.
- Pengerasan SSH dijalankan **setelah** kunci pengguna admin terpasang, dan `sshd` baru dimuat ulang di akhir playbook. Urutan sebaliknya mengunci pemilik dari VPS.
- `roles/base`: SSH hanya dengan kunci (`PasswordAuthentication no`, `PermitRootLogin no`); swap berkas 2 GB dengan `vm.swappiness=10`; `unattended-upgrades` untuk pembaruan keamanan; `ufw` mengizinkan 22 dan 443, menolak sisanya.
- **`ufw` tidak membatasi port yang dipublikasikan Docker.** Pembatasan port 443 ke rentang IP Cloudflare dipasang di firewall konsol Lighthouse (Task 14), bukan di host.
- `roles/docker`: Docker Engine dan plugin Compose dari repositori apt resmi; `/etc/docker/daemon.json` dengan `log-driver: json-file`, `max-size: 10m`, `max-file: 3` (disk hanya 40 GB).
- `roles/terka`: direktori di atas; `.env` per lingkungan dari `env.j2` dengan nilai rahasia dari `vault.yml`; `~deploy/.docker/config.json` berisi login baca GHCR.
- Kunci SSH untuk CI dipasang di `~deploy/.ssh/authorized_keys` dengan perintah paksa `command="/opt/terka/bin/deploy.sh",no-port-forwarding,no-pty`. Kunci itu hanya bisa menjalankan deploy.
- Isi `vault.yml`: kata sandi `terka_staging_owner` dan `terka_staging_app`, kata sandi superuser Postgres, token baca GHCR. Kata sandi vault tidak disimpan di repo.

- [ ] **Step 1:** Tulis playbook dan role sesuai keputusan di atas. Setiap tugas idempoten.
- [ ] **Step 2:** `ansible-lint deploy/ansible` → tanpa temuan. `ansible-playbook -i inventory.example.ini site.yml --syntax-check` → lulus.
- [ ] **Step 3 (pemilik):** Salin `inventory.example.ini` ke `inventory.ini` (tidak di-commit), isi alamat VPS, buat `vault.yml` dengan `ansible-vault create`.
- [ ] **Step 4 (pemilik):** Jalankan `ansible-playbook -i inventory.ini site.yml --ask-vault-pass`.
- [ ] **Step 5 (pemilik):** Jalankan lagi perintah yang sama. Harapan: `changed=0`.
- [ ] **Step 6 (pemilik):** Verifikasi di VPS:
  - `swapon --show` menampilkan 2G
  - `ssh root@<vps>` ditolak; `ssh -o PasswordAuthentication=yes -o PubkeyAuthentication=no deploy@<vps>` ditolak
  - `docker compose version` berjalan sebagai `deploy`
  - `stat -c %a /opt/terka/staging/.env` → `600`
  - `free -m` dicatat di `docs/runbooks/deploy.md` sebagai garis dasar memori
- [ ] **Step 7:** Commit: `chore(deploy): provision the server with ansible`

### Task 11: Uji risiko — memori tumpukan observability

Menjawab risiko 3 di bagian 11 spec. Dikerjakan setelah Task 10 karena butuh Docker di VPS, dan paling bermakna setelah Task 12 (Postgres dan aplikasi staging sudah berjalan).

**Files:**
- Create: `spikes/obs-memory/**` (tidak di-commit), `docs/spikes/2026-11-observability-memory.md`

- [ ] **Step 1:** Tulis `spikes/obs-memory/compose.yaml` berisi Prometheus (retensi 7 hari, batas ukuran 1 GB), Loki, Tempo, Grafana, dan satu kolektor (Grafana Alloy), semuanya dengan konfigurasi bawaan satu-proses. Batas memori: 192 MB untuk empat yang pertama, 128 MB untuk kolektor. Tambah satu container pembangkit telemetri sintetis yang mengirim trace, metrik, dan log lewat OTLP ke kolektor.
- [ ] **Step 2 (pemilik):** Jalankan di VPS selama minimal 2 jam, dengan tumpukan staging dari Task 12 tetap berjalan.
- [ ] **Step 3 (pemilik):** Catat `docker stats --no-stream`, `free -m`, dan `dmesg | grep -ci 'out of memory'` pada menit 0, 30, dan 120.
- [ ] **Step 4:** Tulis `docs/spikes/2026-11-observability-memory.md` dengan tabel pengukuran dan keputusan. Aturan keputusan: tumpukan penuh dipakai di tahap 2 kalau jumlah memori kelima container ≤ 700 MB, tidak ada pembunuhan oleh kernel, dan `available` di `free -m` ≥ 300 MB. Kalau tidak, ulangi tanpa Tempo dan catat hasilnya; tahap 2 mengikuti konfigurasi yang lolos.
- [ ] **Step 5 (pemilik):** `docker compose down -v` di VPS; hapus `spikes/obs-memory`.
- [ ] **Step 6:** Commit: `docs: record observability stack memory findings`

### Task 12: Postgres dan Caddy di VPS

**Files:**
- Create: `deploy/compose.infra.yaml`, `deploy/compose.app.yaml`, `deploy/Caddyfile`, `deploy/scripts/create-env-db.sh`
- Modify: `deploy/ansible/roles/terka/tasks/main.yml` (menyalin berkas compose, Caddyfile, dan skrip ke `/opt/terka`)

**Interfaces:**
- Produces:
  - Proyek compose `terka-infra`: layanan `postgres` (`postgres:18`, tanpa port ke host, volume di `/var/lib/postgresql`, `deploy/postgres/init/` terpasang) dan `caddy` (port 443 saja), di jaringan Docker bernama `terka`.
  - Proyek compose `terka-<env>` dari `compose.app.yaml`: layanan `app` (image `${TERKA_IMAGE}`, `env_file: /opt/terka/<env>/.env`, alias jaringan `terka-<env>`, `read_only: true`, `restart: unless-stopped`) dan layanan `migrate` (profil `tools`, perintah `migrate`).
  - `compose.app.yaml` membaca dua variabel: `TERKA_IMAGE` dan `TERKA_ENV_NAME`.
  - `create-env-db.sh <env>`: membuat role dan database satu lingkungan. Idempoten. Kata sandi dibaca dari variabel `OWNER_PASSWORD` dan `APP_PASSWORD`; di VPS keduanya datang dari `/opt/terka/<env>/db.env` (mode `600`, dari Ansible Vault).
  - Letak di VPS: `/opt/terka/infra/{compose.infra.yaml,Caddyfile,.env,certs/}`, `/opt/terka/compose.app.yaml`, `/opt/terka/bin/{deploy.sh,create-env-db.sh}`. Kata sandi superuser Postgres ada di `/opt/terka/infra/.env`.
- Isi `.env` staging: `TERKA_ENV=staging`, `TERKA_ADDR=:8080`, `TERKA_DATABASE_URL=postgres://terka_staging_app:…@postgres:5432/terka_staging`, `TERKA_MIGRATE_DATABASE_URL=postgres://terka_staging_owner:…@postgres:5432/terka_staging`.

**Keputusan yang tidak bisa ditebak:**
- Role di Postgres berlaku untuk seluruh klaster, sedangkan staging dan produksi berbagi klaster. Karena itu role login dibuat **per lingkungan**: `terka_<env>_owner` (pemilik database, bukan superuser) dan `terka_<env>_app` (`noinherit`, anggota `anon` dan `authenticated`). `create-env-db.sh` menjalankan `revoke connect on database terka_<env> from public`, lalu `grant connect` hanya ke role aplikasi lingkungan itu.
- Batas memori: `postgres` 512 MB, tiap `app` 128 MB, `caddy` 64 MB.
- Setelan Postgres untuk mesin 2 GB: `shared_buffers=192MB`, `effective_cache_size=512MB`, `max_connections=40`.
- Caddy memakai sertifikat Origin CA dari `/opt/terka/infra/certs/` (dipasang di Task 14) dan meneruskan `staging.terka.my.id` ke `terka-staging:8080`. Blok untuk `terka.my.id` belum ditambahkan.

- [ ] **Step 1:** Tulis keempat berkas sesuai antarmuka dan keputusan di atas.
- [ ] **Step 2:** Uji `create-env-db.sh` secara lokal terhadap `deploy/compose.dev.yaml` untuk dua lingkungan, `a` dan `b`:
  - menjalankannya dua kali untuk `a` → keluar 0 keduanya;
  - `psql` sebagai `terka_a_app` ke `terka_b` → ditolak dengan `permission denied for database`;
  - `psql` sebagai `terka_a_app` ke `terka_a` lalu `create table x()` → ditolak;
  - `terka migrate` sebagai `terka_a_owner` terhadap `terka_a` → keluar 0.
- [ ] **Step 3:** `docker compose -f deploy/compose.infra.yaml config` dan `-f deploy/compose.app.yaml config` (dengan `TERKA_IMAGE` terisi) → sah. `shellcheck deploy/scripts/create-env-db.sh` → bersih.
- [ ] **Step 4 (pemilik):** Jalankan ulang playbook Ansible; di VPS jalankan `docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d postgres` lalu `/opt/terka/bin/create-env-db.sh staging`.
- [ ] **Step 5 (pemilik):** Verifikasi di VPS: `docker port terka-infra-postgres-1` kosong (tidak ada port ke host); `docker stats --no-stream` menunjukkan batas 512 MB pada Postgres.
- [ ] **Step 6:** Commit: `chore(deploy): add shared postgres, caddy and per-environment databases`

### Task 13: Skrip deploy dengan rollback

**Files:**
- Create: `deploy/scripts/deploy.sh`, `deploy/scripts/deploy_test.sh`
- Modify: `.github/workflows/ci.yml` (job `scripts`: `shellcheck deploy/scripts/*.sh` dan `bash deploy/scripts/deploy_test.sh`; job `image` ikut bergantung padanya)

**Interfaces:**
- Consumes: `compose.app.yaml` beserta variabel `TERKA_IMAGE` dan `TERKA_ENV_NAME` (Task 12), image dengan `HEALTHCHECK` (Task 8).
- Produces: `deploy.sh <env> <tag>`; juga membaca argumen dari `SSH_ORIGINAL_COMMAND` kalau dipanggil sebagai perintah paksa SSH.

**Perilaku:**
1. `env` harus `staging` atau `production`; `tag` harus cocok `^[0-9a-f]{40}$`. Selain itu keluar `64` tanpa menjalankan apa pun.
2. Baca versi berjalan dari `/opt/terka/<env>/current` (boleh belum ada).
3. `docker pull ghcr.io/dikym/terka-go:<tag>`. Gagal → keluar `1`.
4. Jalankan layanan `migrate` dengan image baru. Gagal → keluar `1`; container `app` tidak disentuh.
5. Jalankan `app` dengan image baru; tunggu status `healthy` paling lama 60 detik.
6. Sehat → tulis `tag` ke `current`, keluar `0`.
7. Tidak sehat dan ada versi sebelumnya → jalankan `app` dengan versi sebelumnya, tunggu sehat, keluar `2`. `current` tidak berubah.
8. Tidak sehat dan tidak ada versi sebelumnya → hentikan `app`, keluar `3`.

Direktori dasar dan nama perintah Docker bisa diganti lewat `TERKA_ROOT` dan `DOCKER`, supaya tes bisa memakai tiruan.

- [ ] **Step 1: Tulis tes yang gagal** (`deploy_test.sh`, bash murni; `DOCKER` menunjuk skrip tiruan yang mencatat tiap panggilan dan menjawab sesuai skenario)

| Skenario | Kode keluar | Yang diperiksa |
| --- | --- | --- |
| `deploy.sh prod <tag>` | 64 | tidak ada panggilan Docker |
| `deploy.sh staging abc` | 64 | tidak ada panggilan Docker |
| semuanya berhasil | 0 | `current` berisi tag baru |
| `migrate` gagal | 1 | tidak ada panggilan `up`; `current` tidak berubah |
| image baru tidak sehat, ada versi sebelumnya | 2 | `up` dipanggil lagi dengan tag sebelumnya; `current` tidak berubah |
| image baru tidak sehat, belum ada `current` | 3 | tidak ada `up` kedua; berkas `current` tidak dibuat |
| argumen datang dari `SSH_ORIGINAL_COMMAND="staging <tag>"` | 0 | sama dengan skenario berhasil |
| `SSH_ORIGINAL_COMMAND="staging <tag>; rm -rf /"` | 64 | tidak ada panggilan Docker |

- [ ] **Step 2:** Jalankan `bash deploy/scripts/deploy_test.sh` → gagal karena `deploy.sh` belum ada.
- [ ] **Step 3:** Tulis `deploy.sh` dengan `set -euo pipefail` sesuai delapan butir perilaku. Batas tunggu sehat bisa dipendekkan lewat `TERKA_HEALTH_TIMEOUT` (bawaan `60`) supaya tes cepat.
- [ ] **Step 4:** Jalankan `bash deploy/scripts/deploy_test.sh` → semua skenario lulus. `shellcheck deploy/scripts/deploy.sh deploy/scripts/deploy_test.sh` → bersih.
- [ ] **Step 5:** Commit: `feat(deploy): add deploy script with health check and rollback`

### Task 14: Cloudflare, firewall, dan staging pertama

**Files:**
- Create: `docs/runbooks/cloudflare.md`
- Modify: `docs/runbooks/deploy.md` (bagian "Deploy manual" dan "Rollback manual")

**Interfaces:**
- Consumes: Task 10, 12, 13, dan satu image di GHCR (Task 9).
- Produces: `https://staging.terka.my.id` hidup di belakang Cloudflare.

- [ ] **Step 1:** Tulis `docs/runbooks/cloudflare.md` sebagai daftar periksa berurutan:
  1. **Jangan ubah** record `terka.my.id` dan `www`; keduanya tetap DNS only ke Vercel.
  2. Record `A staging` → IP VPS, proxy aktif.
  3. Mode SSL/TLS Full (strict). Record DNS only tidak terpengaruh setelan ini.
  4. Buat sertifikat Origin CA untuk `terka.my.id` dan `*.terka.my.id`; simpan di `/opt/terka/infra/certs/` mode `600`.
  5. Aturan cache untuk host `staging.terka.my.id`: path diawali `/api/` → lewati cache; sisanya → boleh di-cache dengan mengikuti header asal.
  6. Firewall konsol Lighthouse: port 443 hanya dari rentang IPv4 Cloudflare yang diterbitkan di `https://www.cloudflare.com/ips-v4`; port 22 terbuka; sisanya ditutup.
  7. Cara memeriksa tiap butir.
- [ ] **Step 2 (pemilik):** Kerjakan daftar periksa itu, lalu jalankan Caddy: `docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d caddy`.
- [ ] **Step 3 (pemilik):** Deploy manual pertama di VPS sebagai `deploy`: `/opt/terka/bin/deploy.sh staging <sha dari Task 9>` → keluar 0.
- [ ] **Step 4 (pemilik):** Verifikasi dari luar:
  - `curl -s https://staging.terka.my.id/healthz` → `{"status":"ok","version":"<sha>"}`
  - `curl -s -o /dev/null -w '%{http_code}' https://staging.terka.my.id/readyz` → `200`
  - `curl -sI https://staging.terka.my.id/ | grep -i '^server:'` → `cloudflare`
  - `curl -sI https://staging.terka.my.id/ | grep -i x-robots-tag` → `noindex`
  - `curl -s -m 10 --resolve staging.terka.my.id:443:<IP VPS> https://staging.terka.my.id/healthz` dari jaringan rumah → habis waktu (asal hanya menerima Cloudflare)
  - `curl -s https://terka.my.id/` masih dilayani Vercel
- [ ] **Step 5:** Tulis bagian "Deploy manual" dan "Rollback manual" di `docs/runbooks/deploy.md` dari perintah yang benar-benar dipakai.
- [ ] **Step 6:** Commit: `docs: add cloudflare and deploy runbooks`

### Task 15: Deploy otomatis ke staging dan latihan rollback

**Files:**
- Create: `.github/workflows/deploy.yml`
- Modify: `docs/runbooks/deploy.md` (bagian "Latihan rollback", dengan hasilnya)

**Interfaces:**
- Consumes: workflow `ci` (Task 9), perintah paksa SSH (Task 10), `deploy.sh` (Task 13).
- Rahasia GitHub di environment `staging`: `DEPLOY_HOST`, `DEPLOY_SSH_KEY`, `DEPLOY_KNOWN_HOSTS`.

- [ ] **Step 1:** Tulis `deploy.yml`:
  - Job `staging`: dipicu `workflow_run` dari `ci` yang selesai sukses di `main`; environment `staging`; `concurrency: deploy-staging` tanpa membatalkan yang sedang jalan; menjalankan `ssh deploy@$DEPLOY_HOST "staging <sha>"` dengan `StrictHostKeyChecking=yes`; lalu memeriksa bahwa `/healthz` staging melaporkan `<sha>` itu.
  - Job `production`: hanya `workflow_dispatch` dengan masukan `sha`, environment `production` yang mewajibkan persetujuan. Didefinisikan sekarang, baru dipakai di tahap 9.
- [ ] **Step 2:** Commit: `ci: deploy main to staging after checks pass`
- [ ] **Step 3 (pemilik):** Buat environment `staging` dan `production` di GitHub, isi rahasianya, lalu push.
- [ ] **Step 4 (pemilik):** Verifikasi: setelah `ci` hijau, job `staging` jalan sendiri dan `curl -s https://staging.terka.my.id/healthz` melaporkan SHA commit itu.
- [ ] **Step 5 (pemilik): Latihan rollback.** Di cabang sementara, buat `/readyz` selalu menjawab 503, bangun image-nya dengan `--platform linux/amd64` (Mac membangun untuk arm64 secara bawaan, dan image itu tidak jalan di VPS) dan push ke GHCR dengan tag SHA cabang itu, tanpa menggabungkannya ke `main`, lalu di VPS jalankan `/opt/terka/bin/deploy.sh staging <sha rusak>`. Harapan: kode keluar `2`, dan `/healthz` staging tetap melaporkan SHA sebelumnya. Hapus cabang dan image-nya.
- [ ] **Step 6:** Catat hasil latihan (tanggal, kedua SHA, kode keluar, lama staging tidak sehat) di `docs/runbooks/deploy.md`.
- [ ] **Step 7:** Commit: `docs: record the rollback drill`

---

## Selesai Ketika

Sesuai bagian 10 spec:

- **Tahap 0:** tiga berkas di `docs/spikes/` berisi jawaban tertulis, dan tidak ada yang memicu aturan "berhenti dan laporkan".
- **Tahap 1:** commit ke `main` sampai ke staging tanpa campur tangan; `make pgtap` hijau di CI; latihan rollback tercatat berhasil.

## Diteruskan ke Rencana Berikutnya

Hal yang sengaja belum dikerjakan di sini, supaya tidak hilang:

- `auth.email_codes`, `http.CrossOriginProtection`, batas ukuran request, dan batas laju → tahap 3.
- Fungsi pembuka transaksi per pengguna, dan tes bahwa identitas tidak bocor antar-request → tahap 3.
- Tugas terjadwal `public.cleanup_usage_daily()` beserta tes pengganti asersi `cron.job` yang dihapus di Task 7 → tahap 4.
- Tes Go pengganti `passwordless.test.sql` dan `sync_signal.test.sql` → tahap 3 dan 4.
- CSP penuh, Tailwind, service worker, dan kode dari repo lama di `web/` → tahap 5.
- Role baca-semua untuk cadangan dan mirror → tahap 7.
- Blok `terka.my.id` di Caddy, aturan cache produksi, dan database produksi → tahap 9.
