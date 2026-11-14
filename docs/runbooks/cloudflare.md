# Runbook: Cloudflare di Depan VPS

Berlaku untuk `staging.terka.my.id` sampai perpindahan produksi.

**Jangan mengerjakan langkah 1 sampai 9 berurutan sekaligus.** Beberapa langkah bergantung pada VPS atau pada deploy yang belum ada saat Anda tiba di sana. Urutan pengerjaan yang benar, bergantian dengan `docs/runbooks/deploy.md`, ada di bagian "Urutan pengerjaan" di runbook itu. Setiap langkah di bawah punya baris **Kapan** yang menunjuk fasenya. Setiap langkah juga punya cara memeriksanya.

Tanda di awal langkah menunjukkan tempat mengerjakannya: **[Cloudflare]** dasbor Cloudflare di browser, **[Lighthouse]** konsol Lighthouse di browser, **[Laptop]** terminal laptop, **[VPS]** terminal VPS (`ssh terka-admin@<ip-vps>`).

## 1. Jangan sentuh produksi

**Kapan:** fase A1, sebagai pemeriksaan awal.

Record `terka.my.id` dan `www` tetap **DNS only** (awan abu-abu) dan tetap menunjuk Vercel.

Periksa (**[Laptop]**): `dig +short terka.my.id` masih memberi alamat Vercel, dan `curl -sI https://terka.my.id/` memuat header `server: Vercel`.

## 2. Record staging

**Kapan:** fase A1.

**[Cloudflare]** DNS, tambah record `A`, nama `staging`, isi IP VPS, **Proxied** (awan oranye). Jangan membuat record `AAAA` untuk staging.

Periksa (**[Laptop]**): `dig +short staging.terka.my.id` memberi alamat Cloudflare, bukan IP VPS.

Karena record ini di belakang proxy, SSH tidak bisa lewat `staging.terka.my.id`. Untuk SSH, termasuk secret `DEPLOY_HOST` di GitHub, selalu pakai IP VPS.

## 3. Mode TLS

**Kapan:** fase A1.

**[Cloudflare]** SSL/TLS, Overview, **Full (strict)**. Record DNS only (produksi di Vercel) tidak terpengaruh, karena trafiknya tidak lewat Cloudflare.

## 4. Sertifikat asal

Dikerjakan dua tahap, di dua fase berbeda.

### Membuat

**Kapan:** fase A2.

**[Cloudflare]** SSL/TLS, Origin Server, Create Certificate: kunci RSA, host `terka.my.id` dan `*.terka.my.id`, masa berlaku 15 tahun.

Kunci privat hanya tampil sekali di dasbor, jadi salin sertifikat dan kunci privatnya ke pengelola kata sandi sebelum menutup halaman. Jangan simpan di repo.

### Memasang

**Kapan:** fase D, setelah playbook berjalan (folder `/opt/terka/infra/certs` dibuat oleh playbook) dan sebelum Caddy dinyalakan.

**[VPS]** (dari laptop: `ssh terka-admin@<ip-vps>`):

```sh
sudo install -o deploy -g deploy -m 600 /dev/stdin /opt/terka/infra/certs/origin.pem      # tempel sertifikat, Ctrl-D
sudo install -o deploy -g deploy -m 600 /dev/stdin /opt/terka/infra/certs/origin-key.pem  # tempel kunci privat, Ctrl-D
```

Periksa: `sudo ls -l /opt/terka/infra/certs` menampilkan kedua berkas, pemilik `deploy`, mode `-rw-------`. Baris `-----BEGIN ...-----` dan `-----END ...-----` harus ikut tertempel.

## 5. Aturan cache

**Kapan:** fase G, setelah API dan UI berjalan, supaya pemeriksaannya bisa dilakukan.

**[Cloudflare]** Caching, Cache Rules, **Create rule**. Buat dua aturan untuk host `staging.terka.my.id`, dengan urutan ini. Di kedua aturan, pilih **Custom filter expression**, klik **Edit expression**, lalu tempel ekspresinya.

1. **Lewati cache.** Nama `staging-bypass-api`. Ekspresi:
   ```
   (http.host eq "staging.terka.my.id" and (starts_with(http.request.uri.path, "/api/") or http.request.uri.path in {"/healthz" "/readyz"}))
   ```
   *Cache eligibility*: **Bypass cache**. Klik **Deploy**.
2. **Ikuti asal.** Nama `staging-follow-origin`. Ekspresi:
   ```
   (http.host eq "staging.terka.my.id" and not starts_with(http.request.uri.path, "/api/") and not http.request.uri.path in {"/healthz" "/readyz"})
   ```
   *Cache eligibility*: **Eligible for cache**. *Edge TTL*: **Use cache-control header if present, bypass cache if not**. Klik **Deploy**.

Ekspresi aturan 2 sengaja mengecualikan path yang ditangani aturan 1. Aturan yang cocok dijalankan berurutan dan aturan belakangan menimpa pengaturan aturan sebelumnya, jadi aturan 2 yang mencakup semua permintaan bisa membatalkan `Bypass cache` dari aturan 1. Dengan pengecualian ini, hasilnya benar di urutan mana pun. Biarkan *Browser TTL* pada nilai bawaan.

Server sudah mengirim `no-store` di setiap jawaban JSON, `no-cache` di HTML dan `sw.js`, dan cache satu tahun di aset ber-hash. Aturan pertama adalah pengaman kedua.

Periksa setelah deploy pertama (**[Laptop]**):

```sh
curl -sI https://staging.terka.my.id/_app/version.json | grep -i cf-cache-status   # MISS lalu REVALIDATED/HIT
curl -sI https://staging.terka.my.id/healthz | grep -i cf-cache-status              # BYPASS atau DYNAMIC
```

## 6. Firewall Lighthouse

**Kapan:** fase E, setelah Caddy berjalan (fase D) dan sebelum pemeriksaan di langkah 7.

**[Lighthouse]** Konsol Lighthouse, Firewall:

- Port 443/TCP: satu aturan per rentang di <https://www.cloudflare.com/ips-v4>. Tidak ada aturan 443 untuk `0.0.0.0/0`.
- Port 22/TCP: **tetap terbuka untuk semua alamat** (`0.0.0.0/0`). Deploy otomatis berjalan dari runner GitHub, yang alamat IP-nya berubah-ubah, jadi membatasi 22 ke satu IP memutus deploy. Login hanya dengan kunci SSH (password dimatikan oleh playbook).
- Hapus aturan lain bawaan image (80, 3389, dan sebagainya).

Firewall host (`ufw`) tidak bisa menggantikan ini: port yang dipublikasikan Docker melewati `ufw`. Pembatas laju di server membaca IP pengguna dari `CF-Connecting-IP`, dan header itu hanya bisa dipercaya karena aturan ini.

Rentang Cloudflare jarang berubah; periksa ulang halaman itu setiap beberapa bulan.

**IPv6.** Caddy hanya dipublikasikan di `0.0.0.0:443`, jadi tidak ada pendengar IPv6 untuk dilewati. Kalau instance punya alamat IPv6 publik (**[VPS]** `ip -6 addr show scope global` tidak kosong), pastikan tetap tidak ada aturan 443 untuk `::/0` di firewall konsol, dan jangan buat record `AAAA` untuk staging.

## 7. Pemeriksaan dari luar

**Kapan:** fase G, setelah API dan UI di-deploy.

**[Laptop]**:

```sh
curl -s https://staging.terka.my.id/healthz                                    # API: {"status":"ok","version":"<sha api>"}
curl -s https://staging.terka.my.id/_app/version.json                          # UI: {"version":"<sha web>"}
curl -s -o /dev/null -w '%{http_code}\n' https://staging.terka.my.id/readyz     # 200
curl -sI https://staging.terka.my.id/ | grep -i '^server:'                     # cloudflare
curl -sI https://staging.terka.my.id/ | grep -i x-robots-tag                   # noindex
curl -sS -m 10 --resolve staging.terka.my.id:443:<IP VPS> https://staging.terka.my.id/healthz
                                                                               # harus: curl: (28) ... timed out
curl -sS -m 10 -6 --resolve 'staging.terka.my.id:443:[<IPv6 VPS>]' https://staging.terka.my.id/healthz
                                                                               # kalau ada IPv6: harus gagal juga
curl -sI https://terka.my.id/ | grep -i '^server:'                             # masih Vercel
```

## 8. Turnstile

**Kapan:** fase A3, sebelum vault diisi.

**[Cloudflare]** Dashboard, Turnstile, Add widget:

- *Widget name* `terka-staging`, mode **Managed**, toggle *Skip future security rule challenges* dibiarkan mati.
- *Hostname Management*: ketik `staging.terka.my.id`, lalu klik baris **Add "staging.terka.my.id" as a custom hostname** (atau tekan Enter) sampai hostname itu muncul sebagai chip. Teks yang sekadar diketik belum tersimpan. Jangan menambah `localhost`: server menolak token untuk hostname lain, dan di lokal memakai kunci uji Cloudflare.
- Klik **Create**. Cloudflare menampilkan *Site key* dan *Secret key*. *Secret key* hanya tampil sekali, jadi salin sekarang ke pengelola kata sandi. Jika hilang, buka widget dan rotasi kuncinya.
- *Secret key* masuk vault sebagai `vault_staging_turnstile_secret` (lihat bagian "Vault" di runbook deploy). *Site key* tidak masuk vault; kunci itu dipakai saat build UI di repo `terka-web`.
- UI memasang widget dengan `action` `email_request`. Server menolak token yang dibuat untuk hostname atau action lain, dan menolak kunci uji Cloudflare (`1x000…AA` dan saudaranya) di luar lokal.

Produksi nanti mendapat widget sendiri untuk `terka.my.id`, dengan secret sendiri.

## 9. Batas laju email

**Kapan:** fase G.

Lapisan pertama sebelum server: **[Cloudflare]** Security, WAF, Rate limiting rules, Create rule (paket gratis memberi satu aturan):

- Nama `auth-email`.
- Jika: *URI Path* `starts with` `/api/auth/email/` **dan** *Request Method* `equals` `POST`.
- Hitung per IP; 5 request per 10 detik.
- Tindakan **Block** selama 10 detik.

Server tetap membatasi sendiri (10 permintaan kode dan 30 verifikasi per IP per jam, 5 kode per alamat per jam, 80 email per jam dan 90 per hari seluruh layanan); aturan ini hanya meredam banjir sebelum sampai ke VPS.

Pemeriksaan (**[Laptop]**): kirim 6 `POST` cepat ke `/api/auth/email/request` dari satu mesin; yang keenam dijawab Cloudflare dengan 429 sebelum mencapai server.
