# Runbook: Cloudflare di Depan VPS

Berlaku untuk `staging.terka.my.id` sampai perpindahan produksi. Kerjakan berurutan; setiap
langkah punya cara memeriksanya.
Tanda di awal langkah menunjukkan tempat mengerjakannya: **[Cloudflare]** dasbor Cloudflare di
browser, **[Lighthouse]** konsol Lighthouse di browser, **[Laptop]** terminal laptop, **[VPS]**
terminal VPS (`ssh terka-admin@<ip-vps>`).

## 1. Jangan sentuh produksi

Record `terka.my.id` dan `www` tetap **DNS only** (awan abu-abu) dan tetap menunjuk Vercel.

Periksa (**[Laptop]**): `dig +short terka.my.id` masih memberi alamat Vercel, dan `curl -sI https://terka.my.id/`
memuat header `server: Vercel`.

## 2. Record staging

**[Cloudflare]** DNS → tambah record `A`, nama `staging`, isi IP VPS, **Proxied** (awan oranye).

Periksa (**[Laptop]**): `dig +short staging.terka.my.id` memberi alamat Cloudflare, bukan IP VPS.

## 3. Mode TLS

**[Cloudflare]** SSL/TLS → Overview → **Full (strict)**. Record DNS only (produksi di Vercel) tidak terpengaruh,
karena trafiknya tidak lewat Cloudflare.

## 4. Sertifikat asal

**[Cloudflare]** SSL/TLS → Origin Server → Create Certificate: kunci RSA, host `terka.my.id` dan `*.terka.my.id`,
masa berlaku 15 tahun. Lalu simpan ke VPS (**[VPS]**):

```sh
# ssh terka-admin@<ip-vps> dari laptop, lalu:
sudo install -o deploy -g deploy -m 600 /dev/stdin /opt/terka/infra/certs/origin.pem      # tempel sertifikat, Ctrl-D
sudo install -o deploy -g deploy -m 600 /dev/stdin /opt/terka/infra/certs/origin-key.pem  # tempel kunci privat, Ctrl-D
```

Kunci privat hanya tampil sekali di dasbor. Jangan simpan di repo.

## 5. Aturan cache

**[Cloudflare]** Caching → Cache Rules, dua aturan untuk host `staging.terka.my.id`, urutan ini:

1. **Lewati cache:** URI path diawali `/api/`, atau sama dengan `/healthz` atau `/readyz` → Bypass
   cache.
2. **Ikuti asal:** sisa permintaan → Eligible for cache, Edge TTL "Use cache-control header if
   present, bypass cache if not".

Server sudah mengirim `no-store` di setiap jawaban JSON, `no-cache` di HTML dan `sw.js`, dan cache
satu tahun di aset ber-hash. Aturan pertama adalah pengaman kedua.

Periksa setelah deploy pertama (**[Laptop]**):

```sh
curl -sI https://staging.terka.my.id/_app/version.json | grep -i cf-cache-status   # MISS lalu REVALIDATED/HIT
curl -sI https://staging.terka.my.id/healthz | grep -i cf-cache-status              # BYPASS atau DYNAMIC
```

## 6. Firewall Lighthouse

**[Lighthouse]** Konsol Lighthouse → Firewall:

- Port 443/TCP: satu aturan per rentang di <https://www.cloudflare.com/ips-v4>. Tidak ada aturan
  443 untuk `0.0.0.0/0`.
- Port 22/TCP: tetap terbuka (atau dibatasi ke IP rumah kalau tetap).
- Hapus aturan lain bawaan image (80, 3389, dan sebagainya).

Firewall host (`ufw`) tidak bisa menggantikan ini: port yang dipublikasikan Docker melewati `ufw`.
Pembatas laju di server membaca IP pengguna dari `CF-Connecting-IP`, dan header itu hanya bisa
dipercaya karena aturan ini.

Rentang Cloudflare jarang berubah; periksa ulang halaman itu setiap beberapa bulan.

**IPv6.** Caddy hanya dipublikasikan di `0.0.0.0:443`, jadi tidak ada pendengar IPv6 untuk
dilewati. Kalau instance punya alamat IPv6 publik (**[VPS]** `ip -6 addr show scope global` tidak kosong),
pastikan tetap tidak ada aturan 443 untuk `::/0` di firewall konsol, dan jangan buat record `AAAA`
untuk staging.

## 7. Pemeriksaan dari luar
**[Laptop]**:

```sh
curl -s https://staging.terka.my.id/healthz                                    # API: {"status":"ok","version":"<sha api>"}
curl -s https://staging.terka.my.id/_app/version.json                          # UI: {"version":"<sha web>"}
curl -s -o /dev/null -w '%{http_code}\n' https://staging.terka.my.id/readyz     # 200
curl -sI https://staging.terka.my.id/ | grep -i '^server:'                     # cloudflare
curl -sI https://staging.terka.my.id/ | grep -i x-robots-tag                   # noindex
curl -s -m 10 --resolve staging.terka.my.id:443:<IP VPS> https://staging.terka.my.id/healthz
                                                                               # harus habis waktu
curl -s -m 10 -6 --resolve 'staging.terka.my.id:443:[<IPv6 VPS>]' https://staging.terka.my.id/healthz
                                                                               # kalau ada IPv6: harus gagal
curl -sI https://terka.my.id/ | grep -i '^server:'                             # masih Vercel
```

## 8. Turnstile

**[Cloudflare]** Dashboard → Turnstile → Add widget:

- Nama `terka-staging`, hostname `staging.terka.my.id`, mode **Managed**.
- *Site key* dipakai build UI (`terka-web`); *secret key* masuk vault sebagai
  `vault_staging_turnstile_secret` (lihat runbook deploy).
- UI memasang widget dengan `action` `email_request`. Server menolak token yang dibuat untuk
  hostname atau action lain, dan menolak kunci uji Cloudflare (`1x000…AA` dan saudaranya) di luar
  lokal.

Produksi nanti mendapat widget sendiri untuk `terka.my.id`, dengan secret sendiri.

## 9. Batas laju email

**[Cloudflare]** Lapisan pertama sebelum server: Security → WAF → Rate limiting rules → Create rule (paket gratis
memberi satu aturan):

- Nama `auth-email`.
- Jika: *URI Path* `starts with` `/api/auth/email/` **dan** *Request Method* `equals` `POST`.
- Hitung per IP; 5 request per 10 detik.
- Tindakan **Block** selama 10 detik.

Server tetap membatasi sendiri (10 permintaan kode dan 30 verifikasi per IP per jam, 5 kode per
alamat per jam, 80 email per jam dan 90 per hari seluruh layanan); aturan ini hanya meredam banjir
sebelum sampai ke VPS.

Pemeriksaan (**[Laptop]**): kirim 6 `POST` cepat ke `/api/auth/email/request` dari satu mesin; yang keenam
dijawab Cloudflare dengan 429 sebelum mencapai server.
