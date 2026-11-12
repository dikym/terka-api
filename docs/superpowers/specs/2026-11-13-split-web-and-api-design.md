# Terka: Memisahkan Frontend dan Backend — Dokumen Desain

**Tanggal:** 2026-11-13 · **Status:** disetujui pemilik 2026-11-13
**Mengubah:** `docs/superpowers/specs/2026-11-10-go-svelte-rewrite.md` (selanjutnya "spec rewrite").
Bagian spec rewrite yang tidak disebut di sini tetap berlaku.

## 1. Maksud

Pemilik ingin bisa memperbarui UI tanpa me-redeploy backend. Perubahan tampilan jauh lebih sering
daripada perubahan server, dan sekarang setiap perbaikan UI ikut me-restart container Go dan
menjalankan langkah migration.

## 2. Tujuan, Bukan Tujuan, Anti-Tujuan

**Tujuan**

1. Frontend dan backend punya repo, CI, image, dan deploy sendiri. Deploy UI tidak menyentuh
   container backend maupun database, dan sebaliknya.
2. Keduanya bisa dijalankan bersama di laptop, dengan dua cara: mode pengembangan sehari-hari dan
   mode yang meniru produksi.
3. Rollback masing-masing berdiri sendiri.

**Bukan tujuan**

- Memindahkan frontend ke luar VPS (Cloudflare Pages dan sejenisnya).
- Mengubah perilaku aplikasi, API, atau skema.
- Penomoran versi API formal.

**Anti-tujuan**

- API pindah ke origin lain (misalnya `api.terka.my.id`), sehingga cookie, CSRF, CSP, dan service
  worker harus dirancang ulang.
- UI baru yang memanggil endpoint yang belum ada di backend produksi.
- Deploy UI yang gagal membuat API ikut mati, atau sebaliknya.

## 3. Keputusan Pemilik

1. Frontend dan backend dipisah ke dua repo dan di-deploy terpisah (2026-11-13).
2. Frontend tetap di VPS yang sama, bukan di layanan hosting lain (2026-11-13).
3. Urutan deploy UI dan API dijaga dengan aturan tertulis sampai tahap 5; pengaman otomatis menyusul
   bersama E2E (2026-11-13).
4. Mode pengembangan memakai `air` untuk hot reload Go; API dan UI berjalan langsung di Mac, hanya
   Postgres di Docker (2026-11-13).

## 4. Desain

### 4.1 Repo

| Repo | Isi | Image |
| --- | --- | --- |
| `terka-api` | `server/`, migration, tes pgTAP, seluruh `deploy/` (Ansible, compose, Caddy tepi, skrip deploy), runbook, dokumen produk, spec | `ghcr.io/dikym/terka-api` |
| `terka-web` | isi `web/` sekarang, dan nanti `src/core`, `src/lib`, komponen, tes klien, skrip konten | `ghcr.io/dikym/terka-web` |

`terka-api` adalah repo `terka-go` yang sekarang, diganti nama, dengan riwayat commit utuh dan `web/`
dikeluarkan. `terka-web` dibuat dari riwayat `web/` (`git subtree split`), sehingga riwayatnya ikut.
Keduanya belum pernah di-push, jadi tidak ada repo remote yang harus dipindah.

Infrastruktur bersama di VPS (Postgres, Caddy tepi, Ansible, skrip deploy) dimiliki `terka-api`.
`terka-web` hanya menghasilkan image dan memicu deploy.

### 4.2 Satu origin, dua container

Lalu lintas tetap lewat `terka.my.id` (dan `staging.terka.my.id`). Caddy tepi membagi per path:

| Path | Tujuan |
| --- | --- |
| `/api/*`, `/healthz`, `/readyz` | container API lingkungan itu (`terka-<env>-api:8080`) |
| selain itu | container web lingkungan itu (`terka-<env>-web:8080`) |

Karena origin tidak berubah, desain keamanan spec rewrite bagian 6.6 dan 6.8 tetap berlaku tanpa
perubahan: cookie `__Host-`, `http.CrossOriginProtection`, CSP `connect-src 'self'`, dan tanpa CORS.

### 4.3 Image web

Image berbasis `caddy:2-alpine` yang berisi hasil build SvelteKit dan Caddyfile-nya sendiri. Perilaku
yang sekarang diuji di paket Go `server/internal/web` dipindahkan ke Caddyfile itu, dan harus tetap
sama:

- URL bersih: `/sesi` menyajikan `sesi.html`; `/` menyajikan `index.html`.
- Garis miring di akhir: 308 ke path tanpa garis miring hanya bila path itu menunjuk berkas yang ada;
  selain itu 404. Tidak pernah ada `Location` ke situs lain.
- Path tak dikenal: isi `404.html` dengan status 404.
- `Cache-Control: public, max-age=31536000, immutable` untuk `/_app/immutable/*`; `no-cache` dengan
  `ETag` untuk berkas lain, termasuk HTML.
- `/sw.js` menyajikan `service-worker.js` hasil build.
- Hanya `GET` dan `HEAD`; selain itu 405.
- Header keamanan seperti API sekarang: `X-Content-Type-Options`, `Referrer-Policy`,
  `Permissions-Policy`, `Content-Security-Policy: frame-ancestors 'none'`, HSTS di luar lokal, dan
  `X-Robots-Tag: noindex` di luar produksi. Lingkungan dibaca dari variabel `TERKA_ENV`.
- CSP lengkap dari spec rewrite bagian 6.8 menjadi milik `terka-web`, karena hanya container web yang
  menyajikan HTML. Ia ditambahkan di tahap 5 bersama UI sungguhan, lewat konfigurasi CSP SvelteKit
  dan header `frame-ancestors` dari Caddyfile. API tetap mengirim header keamanannya sendiri.
- Health check container: `GET /` menjawab 200.

Container web berjalan dengan sistem berkas hanya-baca, pengguna non-root, dan batas memori 32 MiB.

Paket Go `server/internal/web` dan pemasangannya di `serve` dihapus dari `terka-api`. Rute selain
`/api/*`, `/healthz`, dan `/readyz` di API menjawab 404 JSON.

### 4.4 Deploy

- Satu skrip deploy di VPS, diperluas: `deploy.sh <api|web> <staging|production> <sha>`.
  Untuk `web` tidak ada langkah migration; selebihnya sama (tarik, ganti, tunggu sehat, rollback).
- Setiap layanan punya berkas compose dan proyek compose sendiri per lingkungan
  (`terka-<env>-api`, `terka-<env>-web`), dan berkas versi berjalan sendiri
  (`/opt/terka/<env>/api.current`, `/opt/terka/<env>/web.current`).
- Setiap repo punya kunci deploy CI sendiri. Perintah paksa SSH-nya diubah agar menyertakan
  layanannya (`command="/opt/terka/bin/deploy.sh web"`), sehingga kunci milik `terka-web` tidak bisa
  men-deploy API. `deploy.sh` saat ini hanya membaca `SSH_ORIGINAL_COMMAND` bila dipanggil tanpa
  argumen; aturannya diubah: layanan datang dari argumen perintah paksa, sedangkan lingkungan dan SHA
  tetap datang dari `SSH_ORIGINAL_COMMAND`.
- VPS belum disiapkan dan belum ada yang di-deploy, jadi ini pemasangan bersih. Nama proyek, alias,
  dan berkas `current` yang lama tidak perlu dipindahkan.
- Workflow `deploy` di masing-masing repo sama bentuknya dengan yang sekarang: staging otomatis
  setelah CI lulus di `main`, produksi manual dengan persetujuan.

### 4.5 Kompatibilitas antara UI dan API

- Backend wajib tetap melayani versi UI sebelumnya. PWA yang sudah terpasang bisa memakai UI lama
  berhari-hari sebelum service worker-nya diganti.
- Perubahan yang membutuhkan endpoint atau kolom baru di-deploy ke backend lebih dulu, lalu UI.
- Endpoint dihapus dari API hanya setelah UI yang memakainya tidak lagi beredar.
- Sampai tahap 5, aturan ini dijaga sebagai aturan tertulis di runbook deploy dan di `CLAUDE.md`
  kedua repo, tanpa pemeriksaan otomatis. Setelah E2E ada, deploy UI ke produksi menjalankan E2E
  terhadap SHA API yang sedang hidup lebih dulu (dirancang di rencana tahap 5).

### 4.6 Menjalankan di lokal

**Mode pengembangan** (sehari-hari, dengan hot reload):

1. Di `terka-api`: `make dev`. Target ini menyalakan Postgres dev yang sama dengan tes (port 5433),
   membuat database `terka_dev` beserta role pemilik dan role aplikasinya dengan
   `create-env-db.sh dev` (sekali, aman diulang), menjalankan `terka migrate` sebagai pemilik, lalu
   menjalankan API di `:8080` lewat `air`, yang membangun ulang dan memulai ulang server setiap kali
   berkas Go atau migration berubah. API berjalan sebagai role aplikasi dengan `TERKA_ENV=local`. Kata sandi
   dev tetap dan hanya untuk lokal. Database `terka_dev` tidak disentuh tes, yang selalu membuat
   database sekali pakai sendiri.
2. Di `terka-web`: `bun run dev` (Vite di `:5173`). Vite meneruskan `/api`, `/healthz`, dan `/readyz`
   ke `http://localhost:8080`; alamatnya bisa diganti lewat variabel lingkungan.
3. Buka `http://localhost:5173`. Bagi browser, UI dan API satu origin, jadi cookie bekerja seperti di
   produksi.

**Mode mirip produksi** (sebelum push, atau untuk menguji perubahan Caddy dan image):

- `terka-api` memuat `deploy/compose.local.yaml`: Postgres, image API, image web, dan Caddy dengan
  aturan pembagian path yang sama dengan Caddy tepi di VPS, melayani `http://localhost:8000`.
- Image web dibangun dari folder `terka-web` di sebelahnya (`../terka-web`, bisa diganti lewat
  variabel), atau ditarik dari GHCR dengan tag tertentu.
- Satu perintah menjalankan semuanya: `make stack` di `terka-api`.

### 4.7 Pengujian

- Tes Go di `server/internal/web` diganti tes HTTP terhadap container web di CI `terka-web`: kasus
  tabelnya sama (URL bersih, 308, 404, cache, `/sw.js`, metode, jalur yang mencoba keluar, pengalihan
  ke situs lain).
- `terka-api` menambah satu pemeriksaan bahwa Caddy tepi membagi path dengan benar, dijalankan
  terhadap `compose.local.yaml`.
- Tes E2E Playwright (tahap 5 dan 6 spec rewrite) dijalankan di CI `terka-web` terhadap mode mirip
  produksi, dengan image API yang ditarik dari GHCR. Paket GHCR `terka-api` memberi akses baca ke
  repo `terka-web` lewat pengaturan "Manage Actions access", sehingga tidak perlu token pribadi.

## 5. Dampak pada Spec Rewrite

| Bagian | Perubahan |
| --- | --- |
| 6.1, 6.2 | Dua repo; diagram mendapat container web di belakang Caddy tepi |
| 6.3 | Klien disajikan container web, bukan binary Go |
| 6.4 | Binary tidak lagi menyajikan berkas statis |
| 6.9 | Dua image, dua workflow, skrip deploy per layanan |
| 9 | Tes penyaji statis pindah ke `terka-web` |
| 10 | Tahap 1 mendapat pekerjaan pemisahan ini; tahap 5 dikerjakan di `terka-web` |

## 6. Diserahkan ke Builder

- Susunan berkas di dalam `terka-web` dan nama target `make`/skrip.
- Cara tes HTTP container web ditulis (skrip bash, Go, atau Playwright).
- Port lokal selain `8000`, `8080`, dan `5173` yang disebut di atas.

## 7. Keputusan Perancang

Diputuskan perancang, belum disetujui pemilik satu per satu:

1. Nama repo `terka-api` dan `terka-web`.
2. `terka-api` meneruskan riwayat `terka-go`; `terka-web` mendapat riwayat `web/` lewat
   `git subtree split`.
3. Satu origin dengan pembagian per path, bukan subdomain API.
4. Container web memakai Caddy, bukan server Go kedua.
5. Infrastruktur bersama dan skrip deploy tinggal di `terka-api`.
6. Kunci deploy terpisah per repo, masing-masing terkunci ke layanannya.
7. Berkas compose dan proyek compose terpisah per layanan.
8. Mode mirip produksi tinggal di `terka-api` dan membangun web dari folder di sebelahnya.
9. CSP lengkap menjadi milik `terka-web` dan ditambahkan di tahap 5.
10. E2E berjalan di CI `terka-web`, dengan image API ditarik dari GHCR tanpa token pribadi.
11. Mode pengembangan memakai database `terka_dev` di Postgres dev yang sama dengan tes.
