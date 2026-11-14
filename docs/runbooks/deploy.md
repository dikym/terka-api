# Runbook: VPS dan Deploy

VPS: Tencent Cloud Lighthouse, region Singapura, 2 vCPU, 2 GB RAM, 40 GB SSD.

## Laptop dan VPS

Setiap langkah di runbook ini diberi tanda tempat mengerjakannya:

- **[Laptop]**: terminal atau browser di laptop Anda.
- **[VPS]**: terminal di VPS, lewat `ssh ubuntu@<ip-vps>` (sebelum jalan pertama selesai) atau
  `ssh terka-admin@<ip-vps>` (sesudahnya).

| Tugas | Tempat |
| --- | --- |
| Menyalin `deploy/` ke VPS (`rsync`), mengubah kode, push ke `main` | Laptop |
| Membuat token GHCR, kunci CI, widget Turnstile, OAuth Google, domain Resend, pengaturan Cloudflare dan Lighthouse (browser) | Laptop |
| Menguji login SSH, `curl` dan `dig` dari luar | Laptop |
| Memasang Ansible, membuat dan mengedit vault, menjalankan playbook | VPS |
| Menyalakan Postgres, memasang sertifikat asal, menyalakan Caddy | VPS |
| Deploy manual dan rollback | VPS |

Ansible tidak perlu dipasang di laptop.

## Menyiapkan VPS

Ubuntu 26.04, login awal `ssh ubuntu@<ip-vps>`. Ansible dipasang dan dijalankan **di VPS sendiri**
(`ansible_connection=local`), bukan dari laptop. Laptop hanya dipakai untuk menyalin berkas dan
menguji login.

Docker boleh sudah terpasang. Playbook menambah repositori resmi Docker (suite `resolute`) dan
memasang paketnya; yang sudah ada tidak dihapus. Jika `apt update` mengeluh soal `Signed-By` ganda,
hapus berkas repositori Docker lama di `/etc/apt/sources.list.d/` yang dibuat saat pemasangan awal.

### Sekali saja

1. **[Laptop]**, siapkan kunci SSH yang akan menjadi login `terka-admin`, dan token GitHub:
   ```sh
   cat ~/.ssh/id_ed25519.pub
   ```
   Catat hasilnya (satu baris, `ssh-ed25519 AAAA... komentar`). Kunci ini wajib kunci laptop, bukan
   kunci VPS: playbook mematikan login password dan `root`, jadi kunci yang salah mengunci Anda
   dari luar. Lalu buat token GitHub (classic) dengan izin **`read:packages` saja**, untuk menarik
   image dari GHCR.
2. **[Laptop]**, salin folder `deploy/` ke VPS (dari root repo `terka-api`). Playbook membaca
   berkas compose dari `deploy/`, jadi seluruh folder harus ikut:
   ```sh
   ssh ubuntu@<ip-vps> 'mkdir -p ~/terka-api/deploy && chmod 700 ~/terka-api'
   rsync -av --exclude inventory.ini deploy/ ubuntu@<ip-vps>:~/terka-api/deploy/
   ```
   Baris pertama wajib: `rsync` hanya membuat folder terakhir tujuan (`deploy/`), bukan folder
   induknya (`terka-api/`), dan `chmod 700` menutup folder dari pengguna lain di VPS karena isinya
   `vault.yml` terenkripsi. Baris pertama cukup dijalankan sekali; ulangi hanya `rsync` setiap kali
   berkas di `deploy/` berubah. Tanda `/` di akhir `deploy/` wajib ada. Hanya `deploy/` yang
   disalin, bukan seluruh repo, sehingga token GitHub tidak ikut ke VPS.
3. **[VPS]**, pasang Ansible:
   ```sh
   ssh ubuntu@<ip-vps>   # dari laptop; perintah berikutnya jalan di VPS
   sudo apt update && sudo apt install -y ansible
   ansible --version
   ```
   Paket `ansible` dari Ubuntu sudah membawa `ansible.posix` dan `community.general`. Pemeriksaan:
   `ansible-galaxy collection list | grep -E 'ansible.posix|community.general'`. Jika kosong,
   jalankan `ansible-galaxy collection install -r requirements.yml`.
4. **[VPS]**, siapkan inventory dan vault:
   ```sh
   cd ~/terka-api/deploy/ansible
   cp inventory.example.ini inventory.ini
   ansible-vault create group_vars/all/vault.yml
   ```
   Isi vault sesuai `vault.example.yml`. Setiap kata sandi dibuat dengan `openssl rand -hex 24`.
   Simpan kata sandi vault di pengelola kata sandi, bukan di repo. Jika `vault.yml` sudah dibuat di
   laptop, langkah 2 sudah menyalinnya; lewati `ansible-vault create`.
   Nilai Google, Turnstile, dan Resend boleh diisi placeholder dulu, lalu diganti nanti dengan
   `ansible-vault edit group_vars/all/vault.yml` (lihat bagian Login).

### Jalan pertama

**[VPS]**, sebagai `ubuntu`, dalam `~/terka-api/deploy/ansible`:

```sh
ADMIN_KEY='ssh-ed25519 AAAA... komentar'   # kunci publik laptop dari langkah 1
ansible-playbook site.yml --ask-vault-pass -e "admin_ssh_public_key='$ADMIN_KEY'"
```

Pengguna `ubuntu` di Lighthouse punya sudo tanpa kata sandi. Jika tidak, tambahkan
`--ask-become-pass`.
Playbook membuat pengguna admin (`terka-admin`) dengan kunci laptop Anda, lalu di akhir mematikan
login password dan login `root`. **Jangan menutup sesi SSH ini.** **[Laptop]** Buka terminal baru
dan pastikan ini berhasil:

```sh
ssh terka-admin@<ip-vps>
```

Jika gagal, perbaiki dari sesi `ubuntu` yang masih terbuka (`/home/terka-admin/.ssh/authorized_keys`).
Jika sesi sudah tertutup, masuk lewat console web Lighthouse.

### Jalan berikutnya

Perintah yang sama, lagi-lagi dari VPS dan `-e admin_ssh_public_key` selalu disertakan (VPS tidak
punya `~/.ssh/id_ed25519.pub`, nilai bawaannya akan gagal). Jalan kedua tanpa perubahan apa pun
harus berakhir dengan `changed=0`. Setelah mengubah berkas di `deploy/`, jalankan dulu `rsync` di
langkah 2 dari laptop.

Salinan di `~/terka-api` milik `ubuntu` dan hanya dipakai saat playbook berjalan; yang dipakai
saat runtime adalah `/opt/terka/`. Pengguna `ubuntu` tetap ada setelah jalan pertama, jadi cara
termudah adalah terus menjalankan playbook sebagai `ubuntu`. Untuk menjalankannya sebagai
`terka-admin`, salin `deploy/` ke home-nya (ulangi dua perintah di langkah 2 dengan
`terka-admin@<ip-vps>`), karena home `ubuntu` tidak bisa dibaca `terka-admin`. Vault dan
inventory ikut perlu disiapkan di salinan itu.

### Pemeriksaan

**[Laptop]**:

```sh
ssh terka-admin@<ip-vps> 'swapon --show; free -m; docker compose version; sudo stat -c %a /opt/terka/staging/.env'
ssh root@<ip-vps>                                                             # harus ditolak
ssh -o PasswordAuthentication=yes -o PubkeyAuthentication=no deploy@<ip-vps>  # harus ditolak
```

Harapan: swap 2G, Docker Compose terpasang, `.env` bermode `600`.

## Postgres dan database lingkungan

**[VPS]** Setelah playbook jalan, masuk sebagai `terka-admin` lalu pindah ke pengguna `deploy`:

```sh
ssh terka-admin@<ip-vps>   # dari laptop
sudo -iu deploy
docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d --wait postgres
set -a; . /opt/terka/staging/db.env; set +a
/opt/terka/bin/create-env-db.sh staging
```

Skrip boleh dijalankan ulang; ia juga menyetel ulang kedua kata sandi ke nilai di `db.env`.

Pemeriksaan: `docker port terka-infra-postgres-1` harus kosong (Postgres tidak punya port ke host),
dan `docker stats --no-stream` menunjukkan batas 512 MiB pada Postgres.

### Garis dasar memori

Catat keluaran `free -m` setelah jalan pertama di sini, sebagai pembanding saat tumpukan
observability dipasang.

```
(belum diisi)
```

## Login

Server menolak mulai di staging tanpa semua pengaturan login. Siapkan sekali per lingkungan.
Nilainya dibuat di **[Laptop]** (browser dan terminal), lalu dimasukkan ke vault dan diterapkan di
**[VPS]**, dalam `~/terka-api/deploy/ansible`:

```sh
ansible-vault edit group_vars/all/vault.yml
ansible-playbook site.yml --ask-vault-pass -e "admin_ssh_public_key='$ADMIN_KEY'"
```

Bentuk vault ada di `vault.example.yml`. Nilai yang dibuat:

- **Kunci auth:** `openssl rand -base64 32` → `vault_staging_auth_key`. Satu kunci per lingkungan.
  Mengganti kunci membatalkan kode email dan login Google yang sedang berjalan, tidak sesi yang
  sudah ada.
- **Google:** Google Cloud Console → APIs & Services → Credentials → OAuth client ID (Web
  application). *Authorized redirect URIs*:
  `https://staging.terka.my.id/api/auth/google/callback`, dan untuk lokal
  `http://localhost:5173/api/auth/google/callback` serta `http://localhost:8800/api/auth/google/callback`.
  Client ID dan secret → `vault_google_client_id`, `vault_google_client_secret`.
- **Turnstile:** lihat runbook Cloudflare bagian 8 → `vault_staging_turnstile_secret`.
- **Resend:** verifikasi domain `terka.my.id` (record SPF dan DKIM dari Resend dimasukkan ke DNS
  Cloudflare, *DNS only*), buat API key dengan izin *Sending access*, lalu
  `vault_smtp_url: smtp+starttls://resend:<api key>@smtp.resend.com:587`. Pengirim:
  `Terka <no-reply@terka.my.id>`.

Pemeriksaan setelah deploy, **[Laptop]**:

```sh
curl -s https://staging.terka.my.id/api/auth/session        # 401 {"code":"signed_out",...}
curl -sI https://staging.terka.my.id/api/auth/google/start | grep -i '^location:'
                                                              # https://accounts.google.com/...
```

Server menghapus sesi, kode, dan catatan kegagalan yang kedaluwarsa setiap jam (log `cleanup`).

## Caddy

Caddy tepi dan container web sama-sama memakai image dasar publik `ghcr.io/dikym/terka-caddy:<versi>`:
Caddy rilis resmi yang dibangun ulang dengan modul yang sudah ditambal. Sumbernya
`deploy/caddy/Dockerfile`, versinya `deploy/caddy/VERSION` (misalnya `2.11.9-p1`).

**[VPS]** Menyalakan Caddy tepi setelah sertifikat asal terpasang (`docs/runbooks/cloudflare.md`
langkah 4), sebagai `deploy`:

```sh
docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d caddy
```

Selama Caddy dimulai ulang, situs tidak bisa diakses beberapa detik.

### Sekali saja, setelah push pertama `terka-api`

**[Laptop]** (browser). Workflow `caddy` menerbitkan image dasar. GitHub membuat paket baru sebagai privat; ubah sekali di
GitHub → Packages → `terka-caddy` → Package settings → Change visibility → **Public**. Sebelum itu,
CI `terka-web` dan VPS tidak bisa menarik image-nya. Isinya hanya Caddy, tanpa kode atau rahasia
Terka.

### Pemindaian malam dan celah baru

Workflow `caddy` memindai image dasar setiap malam (04.00 WITA). Kalau gagal, GitHub mengirim
notifikasi; deploy API dan UI tidak terhalang. Untuk menanggapinya:

1. **[Laptop]** Di `deploy/caddy/Dockerfile`, tambah atau naikkan `--replace` modul yang disebut pemindai.
2. **[Laptop]** Naikkan tingkat tambalan di `deploy/caddy/VERSION` (`2.11.9-p1` → `2.11.9-p2`) dan tag di
   `deploy/compose.infra.yaml`. `caddy_version_test.sh` gagal kalau keduanya tidak cocok.
3. **[Laptop]** Push ke `main`; workflow `caddy` menerbitkan tag baru. Tag lama tidak pernah ditimpa.
4. **[Laptop]** Di `terka-web/Dockerfile`, naikkan tag `FROM ghcr.io/dikym/terka-caddy:…` dan push.
5. **[Laptop]** `rsync` ulang `deploy/` ke VPS. **[VPS]** Jalankan playbook (menyalin compose baru),
   lalu perintah `up -d caddy` di atas.

### Rilis Caddy baru

Dependabot membuka PR yang menaikkan `caddy:<rilis>-alpine` di `deploy/caddy/Dockerfile`. Di PR itu,
samakan juga `xcaddy build v<rilis>` dan `VERSION` (`<rilis>-p1`), dan hapus `--replace` yang modulnya
sudah sama atau lebih baru di `go.mod` rilis tersebut. Lalu lanjutkan langkah 3–5 di atas.

## Urutan rilis

API (`terka-api`) dan UI (`terka-web`) di-deploy terpisah. Aturannya:

- API wajib tetap melayani versi UI sebelumnya.
- UI yang butuh endpoint atau kolom baru di-deploy **setelah** API yang menyediakannya.
- Endpoint dihapus dari API hanya setelah UI yang memakainya tidak lagi beredar.

Belum ada pemeriksaan otomatis; aturan ini dijaga dengan disiplin sampai E2E ada.

## Deploy manual

Image harus sudah ada di GHCR dengan tag SHA commit 40 karakter (dibuat CI repo masing-masing pada
push ke `main`): `ghcr.io/dikym/terka-api:<sha>` atau `ghcr.io/dikym/terka-web:<sha>`.
**[VPS]**, sebagai `terka-admin`:

```sh
sudo -iu deploy /opt/terka/bin/deploy.sh api staging <sha>
sudo -iu deploy /opt/terka/bin/deploy.sh web staging <sha>
```

Kode keluar: `0` berhasil, `1` tarik image atau migration gagal (versi yang jalan tidak disentuh),
`2` versi baru gagal dijalankan atau tidak sehat, dan versi sebelumnya sudah dijalankan lagi,
`3` hal yang sama pada deploy pertama, dan containernya dihentikan, `64` argumen salah. Migration
hanya dijalankan untuk `api`.

Versi yang sedang jalan: `/opt/terka/<env>/api.current` dan `/opt/terka/<env>/web.current`.
**[Laptop]** Dari luar: `curl -s https://staging.terka.my.id/healthz` (API) dan
`curl -s https://staging.terka.my.id/_app/version.json` (UI).

## Rollback manual

**[VPS]** Deploy ulang SHA sebelumnya dengan perintah yang sama. Untuk API ini aman karena migration hanya
menambah; image lama mengabaikan versi migration yang tidak dikenalnya. Migration tidak pernah
dibatalkan.

```sh
sudo -iu deploy /opt/terka/bin/deploy.sh api staging <sha sebelumnya>
```

SHA sebelumnya bisa dilihat di riwayat workflow `deploy` di GitHub atau dengan `git log` di repo.

## Deploy otomatis

Workflow `deploy` di **masing-masing** repo men-deploy layanannya ke staging setiap kali `ci` lulus
pada push ke `main`. Produksi hanya lewat "Run workflow" dengan SHA, dan menunggu persetujuan.

### Menyiapkan sekali

1. **[Laptop]** Buat dua kunci khusus CI, tanpa passphrase:
   ```sh
   ssh-keygen -t ed25519 -N '' -C terka-ci-api -f ~/terka-ci-api
   ssh-keygen -t ed25519 -N '' -C terka-ci-web -f ~/terka-ci-web
   ```
2. **[Laptop]** Isi `deploy_ci_public_keys.api` dan `deploy_ci_public_keys.web` di
   `deploy/ansible/group_vars/all/main.yml` dengan isi berkas `.pub`-nya, salin ulang `deploy/` ke VPS dengan `rsync`, lalu **[VPS]** jalankan playbook lagi.
   Kunci `api` hanya bisa menjalankan `deploy.sh api`, kunci `web` hanya `deploy.sh web`.
3. Ambil sidik kunci host: `ssh-keyscan -t ed25519 <IP VPS>`.
4. Di **kedua** repo GitHub → Settings → Environments:
   - `staging`: tanpa aturan perlindungan.
   - `production`: Required reviewers = Anda sendiri.
5. Di setiap environment kedua repo, isi secret `DEPLOY_HOST` (IP VPS), `DEPLOY_KNOWN_HOSTS`
   (keluaran langkah 3), dan `DEPLOY_SSH_KEY`: isi `~/terka-ci-api` untuk `terka-api`, isi
   `~/terka-ci-web` untuk `terka-web`.
6. Hapus kedua kunci privat dari laptop setelah tersimpan di GitHub.

Periksa: setelah push ke `main` dan `ci` hijau, job `staging` di workflow `deploy` repo itu jalan
sendiri, dan `/healthz` (API) atau `/_app/version.json` (UI) melaporkan SHA commit itu.

## Latihan rollback

Membuktikan bahwa versi rusak dikembalikan otomatis. Versi rusak tetap melayani lalu lintas sekitar
20 detik (tidak sehat) sampai 60 detik (tidak pernah sehat) sebelum dikembalikan.

1. Buat cabang sementara yang merusak health check: untuk API, `/readyz` selalu menjawab 503; untuk
   UI, hapus `index.html` dari hasil build di Dockerfile.
2. Bangun image-nya untuk VPS (Mac membangun arm64 secara bawaan) dan dorong ke GHCR tanpa
   menggabungkannya ke `main`, misalnya untuk API:
   ```sh
   docker build --platform linux/amd64 -f deploy/Dockerfile --build-arg VERSION=$(git rev-parse HEAD) \
     -t ghcr.io/dikym/terka-api:$(git rev-parse HEAD) .
   docker push ghcr.io/dikym/terka-api:$(git rev-parse HEAD)
   ```
3. Di VPS: `sudo -iu deploy /opt/terka/bin/deploy.sh api staging <sha rusak>`.
4. Harapan: kode keluar `2`, dan `/healthz` staging tetap melaporkan SHA sebelumnya.
5. Hapus cabang dan image-nya dari GHCR.

### Hasil

| Tanggal | Layanan | SHA sehat | SHA rusak | Kode keluar | Lama staging tidak sehat |
| --- | --- | --- | --- | --- | --- |
| (belum dilatih) | | | | | |
