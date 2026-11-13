# Terka API

Lihat pelatnya, terka asalnya. API Terka yang dikelola sendiri (Go + PostgreSQL), beserta
infrastruktur VPS-nya. UI ada di repo `terka-web`. Repo `terka` (Next.js + Supabase) tetap ada
sebagai jalur cadangan.

Desain: `docs/superpowers/specs/2026-11-10-go-svelte-rewrite.md` dan
`docs/superpowers/specs/2026-11-13-split-web-and-api-design.md`.

## Struktur

- `server/`: binary `terka` (Go): API dan migration.
- `deploy/`: Docker, Compose, Caddy, Ansible, skrip deploy untuk kedua layanan.
- `docs/`: dokumen produk, hasil uji risiko (`spikes/`), dan runbook (`runbooks/`).

## Menjalankan di lokal

Kedua repo diletakkan bersebelahan (`terka-api/` dan `terka-web/`).

**Sehari-hari**, dengan hot reload:

```sh
make dev                    # di terka-api: Postgres di Docker, API di :8080 lewat air
bun run dev                 # di terka-web: UI di :5173 (atau port bebas berikutnya)
```

Buka alamat yang dicetak Vite. Vite meneruskan `/api`, `/healthz`, dan `/readyz` ke API, jadi
browser melihat satu origin seperti di produksi. Setelah menambah migration, jalankan ulang
`make dev`.

**Mirip produksi**, semuanya di Docker dengan image yang sama seperti di VPS:

```sh
make stack                  # http://localhost:8800 (TERKA_STACK_PORT untuk mengganti port)
bash deploy/scripts/stack_test.sh
make stack-down             # ARGS=-v untuk sekalian menghapus databasenya
```

Image web dibangun dari `../terka-web`; ganti dengan `TERKA_WEB_DIR`.

## Perintah

Jalankan `make` untuk melihat daftar perintah.
