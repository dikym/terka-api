# Memisahkan Frontend dan Backend — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Memecah `terka-go` menjadi `terka-api` (Go + infrastruktur) dan `terka-web` (SvelteKit), masing-masing dengan image dan deploy sendiri di balik satu origin, dan bisa dijalankan bersama di laptop.

**Architecture:** `terka-go` diganti nama menjadi `terka-api` dan kehilangan penyaji berkas statisnya. `terka-web` dibuat dari riwayat `web/` dan menghasilkan image Caddy berisi hasil build. Caddy tepi di VPS membagi `/api`, `/healthz`, `/readyz` ke container API dan sisanya ke container web; `deploy.sh` mendapat argumen layanan.

**Tech Stack:** Go 1.27.4, SvelteKit 3 + Vite 8, Caddy 2, Docker Compose, Ansible, GitHub Actions, bash.

**Spec:** `docs/superpowers/specs/2026-11-13-split-web-and-api-design.md` (mengubah `docs/superpowers/specs/2026-11-10-go-svelte-rewrite.md`).

## Global Constraints

- Repo: `/Users/dikym/Code/side/terka-api` (dari `terka-go`, riwayat utuh) dan `/Users/dikym/Code/side/terka-web` (riwayat `web/`). Image `ghcr.io/dikym/terka-api` dan `ghcr.io/dikym/terka-web`.
- Satu origin: `/api` dan `/api/*`, `/healthz`, `/readyz` → `terka-<env>-api:8080`; selain itu → `terka-<env>-web:8080`.
- Container web: `caddy:2-alpine`, pengguna non-root, sistem berkas hanya-baca, `mem_limit: 32m`, health check `GET /` → 200.
- Perintah paksa SSH: `command="/opt/terka/bin/deploy.sh <api|web>"`; lingkungan dan SHA datang dari `SSH_ORIGINAL_COMMAND`.
- Berkas versi berjalan: `/opt/terka/<env>/api.current` dan `/opt/terka/<env>/web.current`.
- Lokal: API `:8080`, Vite `:5173`, mode mirip produksi `http://localhost:8000`, Postgres dev `:5433`, database `terka_dev`.
- Aturan urutan rilis ditulis di runbook dan `CLAUDE.md` kedua repo; tanpa pemeriksaan otomatis.
- Semua action GitHub dikunci ke SHA commit (SHA yang sama dengan `terka-go/.github/workflows/ci.yml`).
- Aturan kerja `CLAUDE.md` berlaku: Conventional Commits satu baris berbahasa Inggris, tanpa push, linter tidak dimatikan, komentar berbahasa Inggris hanya untuk alasan. Setiap langkah "Commit" menunggu persetujuan pemilik kecuali pemilik memberi persetujuan tetap untuk rencana ini.
- VPS belum disiapkan; tidak ada perpindahan dari nama lama.

## Review Focus

1. Kunci CI milik `terka-web` dipakai dengan `SSH_ORIGINAL_COMMAND="api staging <sha>"` atau `"staging <sha>; …"` → ditolak (Task 5).
2. `//evil.example/`, `/\evil.example/`, `/../404.html` ke container web → 404 tanpa `Location` (Task 3).
3. `/api` tanpa garis miring dan `/apix` → yang pertama ke API (404 JSON), yang kedua ke web (Task 6).
4. Deploy web yang gagal sehat → kembali ke `web.current` sebelumnya, `api.current` tidak berubah, migration tidak dijalankan (Task 5).
5. `make dev` dijalankan dua kali → tetap jalan; tes Go tidak menyentuh `terka_dev` (Task 7).

---

### Task 1: Dua repo

**Files:**
- Rename: `/Users/dikym/Code/side/terka-go` → `/Users/dikym/Code/side/terka-api`
- Create: `/Users/dikym/Code/side/terka-web` (isi `web/` di akarnya), `terka-web/CLAUDE.md`, `terka-web/README.md`, `terka-web/.gitignore`

**Interfaces:**
- Produces: kedua path repo di atas; cabang `main` di `terka-web` berisi riwayat `web/`.

- [ ] **Step 1:** Di `terka-go`: `git subtree split --prefix=web -b web-history`.
- [ ] **Step 2:** `mv /Users/dikym/Code/side/terka-go /Users/dikym/Code/side/terka-api`.
- [ ] **Step 3:** `git init -b main /Users/dikym/Code/side/terka-web` lalu di sana `git pull /Users/dikym/Code/side/terka-api web-history`; hapus cabang `web-history` di `terka-api`.
- [ ] **Step 4:** Tulis `terka-web/CLAUDE.md` dari `terka-api/CLAUDE.md`: bagian kualitas kode hanya `bun run check`; bagian "Kontrak dengan Repo Lama" dipertahankan; tambah bagian "Urutan rilis" (teks persis dari spec 4.5, tiga butir pertama). Tulis `README.md` singkat (apa repo ini, `bun run dev` butuh API di `:8080`, tautan ke spec di `terka-api`). `.gitignore`: isi `web/.gitignore` ditambah `/.superpowers/`, `.idea/`.
- [ ] **Step 5:** Verifikasi: di `terka-web`, `git log --oneline | wc -l` ≥ 1 dan memuat commit `feat(server): serve the embedded web build`; `bun install --frozen-lockfile && bun run check && bun run build` keluar 0.
- [ ] **Step 6:** Commit di `terka-web`: `chore: add working rules and readme`

### Task 2: API tanpa berkas statis

**Files:**
- Delete: `server/internal/web/` seluruhnya, `web/` seluruhnya
- Modify: `server/internal/server/server.go`, `server/internal/server/server_test.go`, `server/cmd/terka/main.go`, `deploy/Dockerfile`, `.dockerignore`, `Makefile`, `.github/workflows/ci.yml`, `README.md`, `.gitignore`

**Interfaces:**
- Produces: `server.Deps` tanpa field `Static`. Semua path selain `GET /healthz`, `GET /readyz` menjawab 404 JSON `{"code":"not_found",...}`.

- [ ] **Step 1: Ganti `TestOtherPathsGoToStatic` dengan tes yang gagal**

```go
func TestEveryOtherPathIsJSONNotFound(t *testing.T) {
	for _, path := range []string{"/", "/sesi", "/api", "/sw.js"} {
		rec := get(server.New(deps("local", up)), path)
		if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" ||
			!strings.Contains(body(rec), `"code":"not_found"`) {
			t.Errorf("%s: got %d %q %s", path, rec.Code, rec.Header().Get("Content-Type"), body(rec))
		}
	}
}
```

`deps()` di berkas tes tidak lagi mengisi `Static`.
- [ ] **Step 2:** `make test` → gagal kompilasi (`Static` masih wajib) atau gagal pada `/` dan `/sesi`.
- [ ] **Step 3:** Hapus `Static` dari `Deps`; rute `/` memakai handler 404 JSON yang sama dengan `/api/`. Hapus pemasangan `web.Handler` di `main.go` dan paket `server/internal/web`.
- [ ] **Step 4:** Dockerfile: hapus tingkat `web` dan baris yang menyalin hasil build. Makefile: hapus `DIST` dan target `web`, dan target `image` memberi tag `terka-api:$(VERSION)`. `.gitignore`: hapus baris `web/…` dan `server/internal/web/dist`. CI: hapus job `web` dan hapus `web` dari `needs` job `image`. Hapus folder `web/`. Hapus `.idea/` dari indeks (`git rm -r --cached .idea`) dan tambahkan `.idea/` ke `.gitignore`.
- [ ] **Step 5:** `make lint test pgtap image` → keluar 0; `actionlint` → bersih; `docker run --rm terka-api:<sha> version` jalan.
- [ ] **Step 6:** Commit: `refactor(server)!: stop serving the web build from the api`

### Task 3: Image web

Semua di `terka-web`.

**Files:**
- Create: `Dockerfile`, `Caddyfile`, `.dockerignore`, `scripts/image_test.sh`
- Modify: `package.json` (skrip `test:image`)

**Interfaces:**
- Produces: image yang mendengarkan `:8080`, membaca `TERKA_ENV` (`local`, `staging`, `production`), dengan `HEALTHCHECK` yang memanggil `GET /`.

- [ ] **Step 1: Tulis `scripts/image_test.sh` yang gagal.** Skrip membangun image dari repo (`docker build -t terka-web:test .`), menambahkan berkas uji ke salinan build lewat image turunan sementara (`plates.json`, `optimizer/fsrs.wasm`, `manifest.webmanifest`, `sesi.html`, `auth/callback.html`, `service-worker.js`) supaya tabel di bawah punya sasaran, menjalankannya dengan `TERKA_ENV=staging` dan `--read-only`, lalu memeriksa dengan `curl`. Pola `pass`/`fail` dan `if …; then` sama dengan `terka-api/deploy/scripts/deploy_test.sh`. Kasus (diturunkan dari `server/internal/web/web_test.go` yang dihapus di Task 2):

| Permintaan | Harapan |
| --- | --- |
| `GET /`, `/sesi`, `/sesi.html`, `/auth/callback?code=1` | 200, `Cache-Control: no-cache`, `ETag` ada |
| `GET /sesi/?x=1` | 308, `Location: /sesi?x=1` |
| `GET /nope/`, `//evil.example/`, `/%5Cevil.example/`, `///evil.example//` | 404, tanpa `Location` |
| `GET /nope`, `/../404.html`, `/_app`, `/auth` | 404, isi `404.html` |
| `GET /_app/immutable/<berkas js pertama>` | 200, `Cache-Control: public, max-age=31536000, immutable` |
| `GET /sw.js` | 200, isi `service-worker.js`, `no-cache` |
| `GET /plates.json` dua kali, kedua dengan `If-None-Match` dari yang pertama | 200 lalu 304 |
| Content-Type `/`, `/plates.json`, `/sw.js`, `/optimizer/fsrs.wasm`, `/manifest.webmanifest` | `text/html; charset=utf-8`, `application/json`, `text/javascript; charset=utf-8`, `application/wasm`, `application/manifest+json` |
| `HEAD /` | 200 tanpa isi |
| `POST /` | 405, `Allow: GET, HEAD` |
| header setiap jawaban | `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`, CSP memuat `frame-ancestors 'none'`, `Strict-Transport-Security: max-age=31536000`, `X-Robots-Tag: noindex` |
| `TERKA_ENV=production` | `X-Robots-Tag` tidak ada |
| `TERKA_ENV=local` | `Strict-Transport-Security` tidak ada |
| `docker inspect` | `Config.User` bukan kosong/`root`; status health menjadi `healthy` |

- [ ] **Step 2:** `bash scripts/image_test.sh` → gagal karena `Dockerfile` belum ada.
- [ ] **Step 3:** Tulis `Dockerfile` (tingkat `oven/bun:1.4` membangun; tingkat akhir `caddy:2-alpine` dengan `USER` non-root, `EXPOSE 8080`, `HEALTHCHECK` memakai `wget -qO- http://127.0.0.1:8080/`) dan `Caddyfile` untuk perilaku di tabel. Garis miring di akhir dialihkan hanya bila path tanpa garis miring menunjuk berkas atau `.html` yang ada dan tidak memuat garis miring ganda atau backslash. Caddy menyimpan datanya di `/tmp` atau mematikan penyimpanan admin agar bisa berjalan hanya-baca.
- [ ] **Step 4:** `bash scripts/image_test.sh` → `all passed`; `shellcheck scripts/*.sh` bersih.
- [ ] **Step 5:** Commit: `build: serve the web build from its own caddy image`

### Task 4: CI dan deploy web

Semua di `terka-web`.

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/workflows/deploy.yml`

**Interfaces:**
- Consumes: `scripts/image_test.sh` (Task 3); perintah paksa `deploy.sh web` (Task 5).
- Produces: `ghcr.io/dikym/terka-web:<sha>` pada push ke `main` yang lulus.

- [ ] **Step 1:** `ci.yml`: job `check` (`bun install --frozen-lockfile`, `bun run check`, `bun run build`), job `image` (butuh `check`: `bash scripts/image_test.sh`, Trivy HIGH/CRITICAL ignore-unfixed, push ke GHCR hanya pada push ke `main`), job `pinned` (pemeriksaan `uses:` ber-SHA yang sama dengan di `terka-api`, ditambah `shellcheck`). Semua checkout `persist-credentials: false`.
- [ ] **Step 2:** `deploy.yml`: salin dari `terka-api/.github/workflows/deploy.yml`, perintah SSH mengirim `"$TARGET $SHA"` (layanan sudah dikunci perintah paksa), pemeriksaan akhir memastikan `GET $PUBLIC_URL/` menjawab 200 dan HTML memuat aset `/_app/immutable/` yang sama dengan image (bandingkan nama berkas `entry/start.*.js` antara `curl` publik dan image). Tidak ada lagi `/healthz` untuk web.
- [ ] **Step 3:** `actionlint .github/workflows/*.yml` bersih; pemeriksaan SHA keluar 0.
- [ ] **Step 4:** Commit: `ci: build, test and deploy the web image`

### Task 5: Deploy per layanan

Semua di `terka-api`.

**Files:**
- Rename: `deploy/compose.app.yaml` → `deploy/compose.api.yaml`
- Create: `deploy/compose.web.yaml`
- Modify: `deploy/scripts/deploy.sh`, `deploy/scripts/deploy_test.sh`, `deploy/scripts/compose_test.sh`

**Interfaces:**
- Produces: `deploy.sh <api|web> <staging|production> <sha>`; bila argumen hanya `<api|web>` (perintah paksa), dua kata sisanya dibaca dari `SSH_ORIGINAL_COMMAND`. Proyek compose `terka-<env>-<service>`, alias jaringan `terka-<env>-<service>`, berkas `/opt/terka/<env>/<service>.current`, image `ghcr.io/dikym/terka-<service>`. `compose.web.yaml`: layanan `web`, `environment: TERKA_ENV: ${TERKA_ENV_NAME}`, tanpa `env_file`, `read_only`, `mem_limit: 32m`.
- Kode keluar tidak berubah (0, 1, 2, 3, 64).

- [ ] **Step 1: Perbarui `deploy_test.sh` dulu.** Semua kasus lama memakai `deploy api staging …` dan `api.current`. Tambah:
  - `deploy web staging <NEW>` sehat → 0, `web.current` = NEW, tidak ada panggilan `run` (migration), `api.current` tidak dibuat.
  - web tidak sehat dengan `web.current`=OLD → 2, `up` dengan `terka-web:OLD`, `api.current` tidak berubah.
  - `deploy web` dengan `SSH_ORIGINAL_COMMAND="staging <NEW>"` → 0.
  - `deploy web` dengan `SSH_ORIGINAL_COMMAND="api staging <NEW>"` → 64, tanpa panggilan Docker.
  - `deploy db staging <NEW>` → 64.
  - argumen penuh `api staging <NEW>` dengan `SSH_ORIGINAL_COMMAND` terisi → argumen menang, `SSH_ORIGINAL_COMMAND` diabaikan.
  Tiruan `docker` mencocokkan `FAKE_NEW_IMAGE` dengan image layanan yang sedang diuji.
- [ ] **Step 2:** `bash deploy/scripts/deploy_test.sh` → kasus baru gagal.
- [ ] **Step 3:** Ubah `deploy.sh`, ganti nama `compose.app.yaml`, tulis `compose.web.yaml`; perbarui `compose_test.sh` ke `compose.api.yaml` dan tambah pemeriksaan bahwa layanan `web` tidak menerima variabel `TERKA_*DATABASE_URL` apa pun.
- [ ] **Step 4:** `shellcheck deploy/scripts/*.sh`, `deploy_test.sh`, `compose_test.sh` → semua lulus.
- [ ] **Step 5:** Commit: `feat(deploy): deploy the api and the web build separately`

### Task 6: Caddy tepi dan Ansible

Semua di `terka-api`.

**Files:**
- Modify: `deploy/Caddyfile`, `deploy/ansible/roles/terka/tasks/main.yml`, `deploy/ansible/group_vars/all/main.yml`, `.github/workflows/deploy.yml`
- Create: `deploy/scripts/edge_test.sh`

**Interfaces:**
- Produces: variabel Ansible `deploy_ci_public_keys: {api: "", web: ""}` (menggantikan `deploy_ci_public_key`); setiap kunci terisi dipasang dengan `restrict,command="/opt/terka/bin/deploy.sh <service>"`.

- [ ] **Step 1: Tulis `edge_test.sh` yang gagal.** Ia menjalankan Caddy tepi dari `deploy/Caddyfile` versi lokal (situs `http://:8443` dari blok yang sama lewat snippet, tanpa TLS) bersama dua container tiruan bernama `terka-staging-api` dan `terka-staging-web` di satu jaringan Docker sementara (`caddy:2-alpine` dengan `respond "api"` / `respond "web"`). Harapan: `/api` → api, `/api/x` → api, `/healthz` → api, `/readyz` → api, `/apix` → web, `/` → web, `/sesi` → web.
- [ ] **Step 2:** `bash deploy/scripts/edge_test.sh` → gagal (semua ke satu tujuan).
- [ ] **Step 3:** Caddyfile: satu snippet `(staging_routes)` berisi pembagian path, dipakai blok `staging.terka.my.id` (dengan TLS asal dan `encode`) dan oleh tes. Ansible: salin `compose.api.yaml` dan `compose.web.yaml`; dua kunci CI sesuai antarmuka. `deploy.yml` `terka-api`: tidak berubah selain komentar bahwa perintah paksanya `deploy.sh api`.
- [ ] **Step 4:** `edge_test.sh` → `all passed`; `ansible-check` (container alat Ansible) → lulus; `actionlint` bersih. Tambah `edge_test.sh` ke job `scripts` di CI.
- [ ] **Step 5:** Commit: `feat(deploy): route api and web paths at the edge`

### Task 7: Menjalankan di lokal

**Files:**
- Modify (`terka-api`): `Makefile` (target `dev`, `stack`, `stack-down`)
- Create (`terka-api`): `deploy/compose.local.yaml`, `deploy/Caddyfile.local`, `deploy/scripts/stack_test.sh`
- Modify (`terka-web`): `vite.config.ts`

**Interfaces:**
- `make dev`: menyalakan `compose.dev.yaml`; `create-env-db.sh dev` dengan `COMPOSE_FILE=deploy/compose.dev.yaml`, `COMPOSE_PROJECT_NAME=terka-dev`, kata sandi tetap `localdevonlyownerpw` dan `localdevonlyapppw1`; `terka migrate` sebagai `terka_dev_owner`; lalu `go tool air` (air v1.67.4 dicatat sebagai tool di `server/go.mod` lewat `go get -tool github.com/air-verse/air@v1.67.4`) dengan `server/.air.toml`: membangun `./cmd/terka` ke `tmp/terka`, menjalankan `tmp/terka serve`, mengawasi `*.go` dan `migrations/*.sql`, mengabaikan `tmp/` dan berkas `_test.go`. Variabel `TERKA_ENV=local`, `TERKA_ADDR=:8080`, URL `terka_dev_app`. `server/tmp/` masuk `.gitignore`.
- `vite.config.ts`: `server.proxy` untuk `/api`, `/healthz`, `/readyz` ke `process.env.TERKA_API_URL ?? 'http://localhost:8080'`.
- `compose.local.yaml`: proyek `terka-local`; `postgres` (dari `postgres-test.Dockerfile`, tanpa port ke host), `migrate` dan `api` dibangun dari `deploy/Dockerfile`, `web` dibangun dari `${TERKA_WEB_DIR:-../../terka-web}`, `caddy` dengan `Caddyfile.local` di `localhost:8000` memakai snippet pembagian yang sama dengan Task 6. Database dibuat lewat init SQL lokal dengan kata sandi tetap lokal.

- [ ] **Step 1: Tulis `stack_test.sh` yang gagal:** dengan stack berjalan, `GET http://localhost:8000/healthz` → JSON `"status":"ok"`; `/readyz` → 200; `/` → HTML memuat `Terka`; `/api/nope` → 404 JSON; `/nope` → 404 HTML.
- [ ] **Step 2:** `bash deploy/scripts/stack_test.sh` → gagal (tidak ada yang mendengarkan di `:8000`).
- [ ] **Step 3:** Tulis `compose.local.yaml`, `Caddyfile.local`, target `make stack` (`up -d --build --wait`) dan `make stack-down`.
- [ ] **Step 4:** `make stack && bash deploy/scripts/stack_test.sh` → `all passed`; `make stack-down`.
- [ ] **Step 5:** Mode pengembangan: `make dev` di latar belakang, lalu di `terka-web` `bun run dev` di latar belakang; `curl -s localhost:5173/healthz` → JSON dari API. Ubah satu string di jawaban `/healthz` sementara, simpan, tunggu air membangun ulang, dan pastikan `curl localhost:8080/healthz` memuat perubahan tanpa menjalankan ulang `make dev`; kembalikan perubahannya. Jalankan `make dev` kedua kali (setelah menghentikan yang pertama) → tetap jalan. `make test` setelahnya → lulus dan `terka_dev` masih ada.
- [ ] **Step 6:** Commit di `terka-api`: `feat(dev): run the api and web together locally`; di `terka-web`: `feat(dev): proxy api requests to the local api`

### Task 8: Dokumen

**Files:**
- Modify (`terka-api`): `CLAUDE.md`, `README.md`, `docs/runbooks/deploy.md`, `docs/runbooks/cloudflare.md`, `docs/superpowers/specs/2026-11-10-go-svelte-rewrite.md` (bagian 6.1–6.4, 6.9, 9, 10 sesuai tabel di spec pemisahan bagian 5)

- [ ] **Step 1:** `CLAUDE.md`: bagian "Urutan rilis" (sama dengan `terka-web`). README: struktur baru dan bagian "Menjalankan di lokal" (dua mode, perintahnya persis).
- [ ] **Step 2:** `deploy.md`: deploy manual `deploy.sh <api|web> <env> <sha>`, berkas `*.current`, dua kunci CI dan environment GitHub di kedua repo, urutan rilis, latihan rollback untuk web. `cloudflare.md`: pemeriksaan `/healthz` dan `/` setelah deploy kedua layanan.
- [ ] **Step 3:** Spec rewrite: ubah bagian yang tercantum agar tidak lagi menyebut binary yang menyajikan berkas statis atau satu repo.
- [ ] **Step 4:** `grep -rn "compose.app.yaml\|terka-go\|deploy_ci_public_key\b" docs deploy CLAUDE.md README.md` → hanya rujukan sejarah di spec dan rencana lama.
- [ ] **Step 5:** Commit: `docs: describe the split repositories and local setup`
