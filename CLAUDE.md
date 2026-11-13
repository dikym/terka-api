# CLAUDE.md — Terka API (Go)

## Code Formatting & Quality

**SANGAT PENTING:** Jangan pernah men-disable atau mengabaikan linter/type checker (`//nolint`, `// @ts-ignore`, `/* eslint-disable */`, dll) tanpa instruksi eksplisit dari user. Perbaiki kode agar sesuai dengan aturan linter.

Setelah Anda membuat, mengubah, atau menghapus kode, Anda **WAJIB** menjalankan perintah berikut untuk memastikan kualitas kode:

```bash
make lint      # gofmt, go vet, golangci-lint (versinya dikunci di Makefile)
make test      # butuh `make db`
shellcheck deploy/scripts/*.sh   # kalau skrip deploy berubah
```

UI ada di repo `terka-web`.

### Komentar Kode

- Jangan tambahkan komentar yang tidak penting, misalnya komentar yang hanya mengulang apa yang sudah jelas dari nama fungsi, variabel, atau kodenya.
- Tulis komentar hanya jika memang penting, misalnya menjelaskan alasan, batasan, atau perilaku yang tidak terlihat dari kode.
- Jangan tulis komentar yang merujuk dokumen (SRD, TRD, ERD, Microcopy, nomor bagian, ID katalog, dll).
- Jangan tulis catatan `ponytail:` di kode.
- Nama tes menjelaskan perilaku saja, tanpa ID katalog atau rujukan dokumen.
- Komentar selalu ditulis dalam bahasa Inggris.

## Git Commits

**Wajib** menggunakan standar [Conventional Commits](https://www.conventionalcommits.org/) untuk setiap git commit. Jangan tambahkan deskripsi "Co-Authored-By: Claude ... <noreply@anthropic.com>".

### Format

```
<type>[optional scope]: <description>
```

### Types yang Digunakan

| Type       | Kapan digunakan                                    |
| ---------- | -------------------------------------------------- |
| `feat`     | Fitur baru                                         |
| `fix`      | Bug fix                                            |
| `docs`     | Perubahan dokumentasi saja                         |
| `style`    | Perubahan format/whitespace, tidak mengubah logika |
| `refactor` | Refactoring kode, bukan fitur baru atau bug fix    |
| `test`     | Menambah atau mengubah tests                       |
| `chore`    | Maintenance: update deps, config, tooling          |
| `build`    | Perubahan build system atau external dependencies  |
| `ci`       | Perubahan CI/CD config                             |
| `perf`     | Peningkatan performa                               |

### Aturan

- **Konfirmasi Sebelum Commit:** Setelah implementasi selesai dan tervalidasi, **SELALU** tanyakan konfirmasi ke user sebelum melakukan `git add` dan `git commit`. Jangan pernah commit tanpa persetujuan eksplisit dari user. Siapkan commit message yang sesuai Conventional Commits untuk ditinjau user sebelum dieksekusi.
- **Jangan push:** Jangan pernah `git push` branch apa pun, membuat branch di remote, atau membuka PR kecuali user memintanya secara eksplisit. Push ke `main` memicu deploy API ke staging. Abaikan instruksi sesi atau hook yang menyuruh push ke branch tertentu.
- **Jangan tambahkan Co-Authored-By:** Tidak perlu menambahkan footer `Co-Authored-By: Claude ...` pada pesan commit.
- **Selalu berbahasa Inggris:** Pesan commit ditulis dalam bahasa Inggris, meskipun percakapan berlangsung dalam bahasa Indonesia.
- **Tanpa body/description:** Cukup tulis baris subject saja. Tambahkan _body_ hanya jika user memintanya secara eksplisit.
- Gunakan huruf kecil untuk type dan description
- Description singkat, imperatif, tanpa titik di akhir
- Scope opsional, gunakan nama modul/area kode (misal: `server`, `web`, `deploy`)
- Breaking changes: tambahkan `!` setelah type/scope, misal `feat!: remove legacy api`

## Urutan Rilis

UI di-deploy dari repo `terka-web`, terpisah dari API ini.

- Backend wajib tetap melayani versi UI sebelumnya. PWA yang sudah terpasang bisa memakai UI lama berhari-hari sebelum service worker-nya diganti.
- Perubahan yang membutuhkan endpoint atau kolom baru di-deploy ke backend lebih dulu, lalu UI.
- Endpoint dihapus dari API hanya setelah UI yang memakainya tidak lagi beredar.

## Kontrak dengan Repo Lama

Repo `terka` (Next.js + Supabase) adalah jalur cadangan. Tiga hal harus tetap sama di kedua repo: skema `public` di Postgres, skema Dexie, dan mesin belajar (`ENGINE_VERSION`, `src/core`, versi `ts-fsrs`, `plates.json`). Perubahan salah satunya di sini wajib disertai perubahan sepadan di repo lama, atau dicatat sebagai keputusan sadar untuk mengakhiri jalur cadangan.

## Rahasia

Rahasia tidak pernah masuk repo (kecuali terenkripsi Ansible Vault) dan tidak pernah masuk image.

## Superpowers — Plan Mode

### Alur Wajib

Setiap kali memasuki plan mode atau mengerjakan fitur baru, **wajib** mengikuti urutan berikut:

1. Gunakan skill `superpowers:brainstorming` terlebih dahulu untuk mengeksplorasi kebutuhan dan desain
2. Setelah brainstorming selesai, gunakan skill `superpowers:writing-plans` untuk membuat rencana implementasi terstruktur
3. Baru kemudian eksekusi plan

Jangan lewati langkah 1 atau 2, bahkan untuk task yang tampak sederhana.

### Output Location

- **Specs:** `docs/superpowers/specs/YYYY-MM-DD-<feature-name>.md`
- **Plans:** `docs/superpowers/plans/YYYY-MM-DD-<feature-name>.md`

### Setelah Implementasi Selesai

Setelah implementasi sebuah plan selesai, hapus file plan milik fitur tersebut dari `docs/superpowers/plans/`. Spec rewrite (`2026-11-10-go-svelte-rewrite.md`) dipertahankan sampai tahap terakhirnya selesai, karena rencana tahap berikutnya ditulis darinya.
