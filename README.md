# Terka (Go + Svelte)

Lihat pelatnya, terka asalnya. Versi Terka yang dikelola sendiri: server Go, klien Svelte,
PostgreSQL, dan Docker di satu VPS. Repo `terka` (Next.js + Supabase) tetap ada sebagai jalur
cadangan.

Desain lengkap: `docs/superpowers/specs/2026-11-10-go-svelte-rewrite.md`.

## Struktur

- `server/`: binary `terka` (Go): API, berkas statis, migration.
- `web/`: klien SvelteKit statis.
- `deploy/`: Docker, Compose, Caddy, Ansible, skrip deploy.
- `docs/`: dokumen produk, hasil uji risiko (`spikes/`), dan runbook (`runbooks/`).

## Perintah

Jalankan `make` untuk melihat daftar perintah.
