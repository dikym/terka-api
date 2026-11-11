# Uji Risiko: SvelteKit Statis, Service Worker, dan Optimizer

**Tanggal:** 2026-11-11 · **Menjawab:** risiko 2 di bagian 11 spec.

**Kesimpulan:** lolos. SvelteKit 3 statis bisa menjalankan optimizer WebAssembly tanpa jaringan di
Chromium dan WebKit (iPhone 13) di bawah CSP, dengan dua syarat yang dicatat di bawah. Enam tes
Playwright (tiga skenario × dua browser) lulus.

## Versi yang diuji

| Paket | Versi |
| --- | --- |
| `@sveltejs/kit` | 3.2.0 |
| `@sveltejs/adapter-static` | 4.0.1 |
| `svelte` | 5.57.3 |
| `vite` | 8.3.7 |
| `@sveltejs/vite-plugin-svelte` | 7.3.1 |
| `typescript` | 6.0.3 |
| `@open-spaced-repetition/binding` dan `binding-wasm32-wasip1` | 0.6.0-beta.6 |
| `@playwright/test` | 1.64.1 |
| Bun | 1.4.4 |

## Jawaban

**1. Nama berkas keluaran.** Rute `/sesi` menjadi `build/sesi.html` (`trailingSlash` bawaannya
`'never'`). Service worker ditulis sebagai `build/service-worker.js`. Karena pemasangan lama
mendaftarkan `/sw.js`, server harus menyajikan `service-worker.js` pada path `/sw.js`.

**2. CSP.** Mode `hash` menulis `<meta http-equiv="content-security-policy">` di tiap halaman
prerender, dengan hash SHA-256 untuk skrip inline SvelteKit. Contoh hasilnya:
`default-src 'self'; worker-src 'self'; connect-src 'self'; object-src 'none'; script-src 'self' 'wasm-unsafe-eval' 'sha256-…'; style-src 'self' 'unsafe-inline'`.

**3. Worker bawaan Vite.** Lolos di kedua browser saat offline, dengan dua syarat:

- **Berkas WebAssembly harus ada di `static/`.** Kalau diimpor dari dalam worker (`…/wasm?url`),
  Vite menaruhnya di `_app/immutable/workers/assets/`, dan berkas itu **tidak** masuk daftar
  `immutable` di `$app/manifest`. Akibatnya tidak ikut precache dan gagal dimuat saat offline
  (`Load failed`). Skrip worker-nya sendiri masuk daftar. Yang lolos: berkas disalin ke
  `static/optimizer/fsrs.wasm` dan diambil dengan `fetch('/optimizer/fsrs.wasm')`, seperti di repo
  lama.
- **Dua dependensi turunan harus dikunci ke versi repo lama:** `@napi-rs/wasm-runtime` 1.2.5 dan
  `@tybys/wasm-util` 0.10.3. Pemasangan baru mengambil 1.2.6 dan 0.10.4, dan dengan itu
  `instantiate()` mengembalikan modul tanpa ekspor (`FSRSBinding is not a constructor`), baik
  dengan worker Vite maupun worker yang dibundel Bun. Mana dari keduanya yang jadi penyebab belum
  dipisahkan. Repo lama terlindung selama `bun.lock`-nya tidak diperbarui.

Pembundelan worker terpisah seperti `scripts/build/worker.ts` di repo lama **tidak diperlukan
lagi**. Ia juga diuji dan lolos, jadi tetap tersedia sebagai jalan cadangan.

**4. Protokol `SKIP_WAITING`.** Bekerja. Versi service worker baru berhenti di `waiting`, halaman
tetap dikendalikan versi lama, dan baru berganti (`controllerchange`) setelah halaman mengirim
string `"SKIP_WAITING"`.

**5. Konfigurasi.** Di SvelteKit 3 tidak ada `svelte.config.js`; opsi diberikan ke
`sveltekit({...})` di `vite.config.ts`:

```ts
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			adapter: adapter(),
			csp: {
				mode: 'hash',
				directives: {
					'default-src': ['self'],
					'script-src': ['self', 'wasm-unsafe-eval'],
					'style-src': ['self', 'unsafe-inline'],
					'connect-src': ['self'],
					'worker-src': ['self'],
					'object-src': ['none']
				}
			},
			serviceWorker: { register: false }
		})
	]
});
```

Prerender seluruh aplikasi: `export const prerender = true` di `src/routes/+layout.ts`.

## Hal lain yang berbeda dari SvelteKit 2

- Service worker ada di `src/service-worker.ts` (atau `src/service-worker/index.ts`) dan mengimpor
  dari modul baru: `self` dari `$app/service-worker`, `version` dari `$app/env`, serta `immutable`,
  `assets`, dan `prerendered` dari `$app/manifest`. Modul `$service-worker` tidak dipakai.
- Entri `$app/manifest` berbentuk `{ path }` relatif terhadap base path; ubah dengan `resolve()`
  dari `$app/paths` sebelum dicocokkan dengan `url.pathname`.
- Templat memakai alias `#lib` lewat `imports` di `package.json`. Alias `@/` untuk kode dari repo
  lama belum diuji di sini; itu pekerjaan tahap 5.
- Favicon templat disisipkan sebagai gambar `data:` dan diblokir CSP di atas. Ikon aplikasi harus
  berupa berkas di `static/`, atau `img-src` harus mengizinkan `data:`.
- Halaman `404.html` tidak dibuat dengan konfigurasi di atas.

## Service worker yang diuji

Precache `immutable`, `assets`, dan `prerendered` saat install; menghapus cache lain saat aktivasi;
menjawab permintaan `GET` se-origin dari cache berdasarkan `url.pathname` saja (query string
diabaikan), lalu jaringan; tidak memanggil `skipWaiting` saat install; pesan `"SKIP_WAITING"`
memanggil `skipWaiting()`. Pendaftaran dilakukan halaman sendiri ke `/sw.js`.

## Yang tidak diuji

- iPhone sungguhan dan PWA terpasang; yang diuji WebKit milik Playwright.
- Pembersihan cache `workbox-precache-*` dari versi lama.
- Kode optimizer Terka yang sebenarnya; yang dijalankan satu panggilan `evaluate` dari pustakanya.
