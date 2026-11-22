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

## Urutan pengerjaan

Runbook ini dan `docs/runbooks/cloudflare.md` dikerjakan **bergantian**, bukan satu selesai lalu yang lain. Ikuti tabel ini dari atas ke bawah; setiap baris menunjuk ke bagian atau langkah yang dimaksud, dan kolom terakhir menjelaskan kapan baris itu dianggap selesai.

| Fase | Tempat | Kerjakan | Selesai jika |
| --- | --- | --- | --- |
| A1 | Laptop (browser) | Cloudflare: record DNS `staging` dan mode TLS (`cloudflare.md` langkah 1 sampai 3) | `dig +short staging.terka.my.id` memberi alamat Cloudflare |
| A2 | Laptop (browser) | Cloudflare: buat sertifikat asal dan simpan sementara (`cloudflare.md` langkah 4, bagian "Membuat") | Sertifikat dan kunci privat tersimpan di pengelola kata sandi |
| A3 | Laptop (browser) | Cloudflare: widget Turnstile (`cloudflare.md` langkah 8) | *Secret key* Turnstile tercatat |
| A4 | Laptop (browser) | Google OAuth dan Resend (bagian "Login" di bawah) | Client ID, client secret, dan URL SMTP tercatat |
| A5 | Laptop (browser) | Token GitHub (bagian "Token GitHub" di bawah) | Token `ghp_...` tercatat |
| B | Laptop dan VPS | Bagian "Menyiapkan VPS", termasuk "Vault" | Playbook berakhir `failed=0` dan `ssh terka-admin@<ip-vps>` berhasil |
| C | VPS | Bagian "Postgres dan database lingkungan" | `create-env-db.sh staging` selesai tanpa galat |
| D | VPS | Pasang sertifikat asal (`cloudflare.md` langkah 4, bagian "Memasang"), lalu nyalakan Caddy (bagian "Caddy") | `docker compose ... ps` menunjukkan Caddy berjalan |
| E | Laptop (browser) | Firewall Lighthouse (`cloudflare.md` langkah 6) | Aturan 443 hanya untuk Cloudflare, 22 terbuka |
| F | Laptop dan VPS | Bagian "Deploy otomatis": kunci CI, secret GitHub, lalu deploy API, kemudian UI | `/healthz` dan `/_app/version.json` melaporkan SHA terbaru |
| G | Laptop (browser) | Aturan cache dan batas laju (`cloudflare.md` langkah 5 dan 9), lalu pemeriksaan (`cloudflare.md` langkah 7 dan "Pemeriksaan setelah deploy" di bagian "Login") | Semua pemeriksaan menghasilkan nilai yang diharapkan |
| H | Laptop dan VPS | Observability: Cloudflare Access dan record `grafana` (`cloudflare.md` bagian 10), webhook Discord ke vault dan secret GitHub, lalu bagian "Observability" | Alert uji sampai ke Discord staging, Grafana terbuka lewat Access, workflow `healthz` hijau |

Alasan urutannya:

- A1 mendahului Resend (A4) karena record SPF dan DKIM dimasukkan ke DNS Cloudflare.
- A2 sampai A5 mendahului B karena semua nilainya masuk ke vault. Jika ada yang belum siap, isi placeholder untuk Google, Turnstile, dan SMTP supaya playbook bisa jalan, tetapi login tidak akan bekerja sampai nilai aslinya diganti, dan penggantiannya harus sebelum F.
- C mendahului F karena `deploy.sh` menjalankan migration ke database yang dibuat di C.
- D memerlukan B (folder `/opt/terka/infra/certs`) dan sertifikat dari A2. Caddy gagal mulai tanpa kedua berkas sertifikat itu.
- E sesudah D, supaya Anda bisa memastikan situs menjawab sebelum 443 ditutup untuk selain Cloudflare.
- Langkah 5 dan 9 di `cloudflare.md` baru bisa diperiksa setelah F, karena pemeriksaannya butuh API dan UI yang sudah berjalan.
- H sesudah F: membuat ulang container API dan UI (langkah 4 di "Observability") butuh deploy yang berjalan. Di dalam H, bagian 10 `cloudflare.md` dikerjakan lebih dulu, karena nama tim dan AUD tag masuk vault sebelum playbook.
- Jangan mengerjakan `cloudflare.md` dari langkah 1 sampai 9 berurutan sekaligus.

## Menyiapkan VPS

Ubuntu 26.04, login awal `ssh ubuntu@<ip-vps>`. Ansible dipasang dan dijalankan **di VPS sendiri** (`ansible_connection=local`), bukan dari laptop. Laptop hanya dipakai untuk menyalin berkas dan menguji login.

Docker boleh sudah terpasang. Playbook menambah repositori resmi Docker (suite `resolute`) dan memasang paketnya; yang sudah ada tidak dihapus. Jika `apt update` mengeluh soal `Signed-By` ganda, hapus berkas repositori Docker lama di `/etc/apt/sources.list.d/` yang dibuat saat pemasangan awal.

### Token GitHub

Dipakai VPS untuk menarik image dari GHCR. **[Laptop]** (browser):

1. Buka <https://github.com/settings/tokens/new> (Settings, Developer settings, Personal access tokens, Tokens (classic), Generate new token (classic)).
2. **Note:** `terka-vps-ghcr`. **Expiration:** 90 hari atau 1 tahun, dan catat tanggal pembaruannya.
3. **Scopes:** centang hanya **`read:packages`**.
4. Klik **Generate token** dan salin `ghp_...` ke pengelola kata sandi. Token hanya tampil sekali.

Token harus tipe classic. Nilainya masuk ke `vault_ghcr_token`, dan username GitHub Anda ke `vault_ghcr_user`. Jika token kedaluwarsa, tarik image gagal saat deploy: buat token baru, perbarui vault (`ansible-vault edit`), lalu jalankan playbook lagi.

### Sekali saja

1. **[Laptop]**, catat kunci SSH yang akan menjadi login `terka-admin`:
   ```sh
   cat ~/.ssh/id_ed25519.pub
   ```
   Hasilnya satu baris, `ssh-ed25519 AAAA... komentar`. Kunci ini wajib kunci laptop, bukan kunci VPS: playbook mematikan login password dan `root`, jadi kunci yang salah mengunci Anda dari luar.
2. **[Laptop]**, salin folder `deploy/` ke VPS (dari root repo `terka-api`). Playbook membaca berkas compose dari `deploy/`, jadi seluruh folder harus ikut:
   ```sh
   ssh ubuntu@<ip-vps> 'mkdir -p ~/terka-api/deploy && chmod 700 ~/terka-api'
   rsync -av --exclude inventory.ini deploy/ ubuntu@<ip-vps>:~/terka-api/deploy/
   ```
   Baris pertama wajib: `rsync` hanya membuat folder terakhir tujuan (`deploy/`), bukan folder induknya (`terka-api/`), dan `chmod 700` menutup folder dari pengguna lain di VPS karena isinya `vault.yml` terenkripsi. Baris pertama cukup dijalankan sekali; ulangi hanya `rsync` setiap kali berkas di `deploy/` berubah. Tanda `/` di akhir `deploy/` wajib ada. Hanya `deploy/` yang disalin, bukan seluruh repo.
3. **[VPS]**, pasang Ansible:
   ```sh
   ssh ubuntu@<ip-vps>   # dari laptop; perintah berikutnya jalan di VPS
   sudo apt update && sudo apt install -y ansible tmux
   ansible --version
   ```
   Paket `ansible` dari Ubuntu sudah membawa `ansible.posix` dan `community.general`. Pemeriksaan: `ansible-galaxy collection list | grep -E 'ansible.posix|community.general'`. Jika kosong, jalankan `ansible-galaxy collection install -r requirements.yml` di `~/terka-api/deploy/ansible`.
4. **[VPS]**, siapkan inventory, lalu isi vault (bagian berikutnya):
   ```sh
   cd ~/terka-api/deploy/ansible
   cp inventory.example.ini inventory.ini
   ```

Semua perintah `ansible`, `ansible-vault`, dan `ansible-playbook` dijalankan dari `~/terka-api/deploy/ansible`. `ansible.cfg` (yang menunjuk ke `inventory.ini`) dan `group_vars/` hanya dibaca dari folder kerja. Dari folder lain muncul `No inventory was parsed` dan `Could not match supplied host pattern`.

### Vault

Vault menyimpan semua rahasia dalam satu berkas terenkripsi, `group_vars/all/vault.yml`. Bentuknya ada di `vault.example.yml`: 17 kunci, semua diawali `vault_`. Enam kunci terakhir (observability) dijelaskan di bagian "Observability".

**Membuat.** Jalankan di dalam `tmux` supaya koneksi SSH yang putus tidak menghilangkan pekerjaan Anda (sambungkan lagi dengan `tmux attach -t vault`):

```sh
tmux new -s vault
cd ~/terka-api/deploy/ansible
ansible-vault create group_vars/all/vault.yml
```

Ansible meminta kata sandi vault dua kali, lalu membuka editor. Simpan kata sandi vault di pengelola kata sandi, bukan di repo. Berkas baru terbentuk dan terenkripsi hanya setelah editor ditutup dengan menyimpan.

Di Vim: tekan `i` untuk mengetik, `Esc` untuk keluar dari mode insert, lalu `:wq` dan Enter untuk menyimpan dan keluar (`:q!` keluar tanpa menyimpan). Untuk editor lain: `EDITOR=nano ansible-vault create group_vars/all/vault.yml` (simpan dengan `Ctrl+O`, keluar dengan `Ctrl+X`).

**Dari mana tiap nilai berasal:**

| Kunci | Asal |
| --- | --- |
| `vault_ghcr_user` | Username GitHub Anda |
| `vault_ghcr_token` | Bagian "Token GitHub" di atas |
| `vault_postgres_superuser_password` | `openssl rand -hex 24` |
| `vault_staging_owner_password` | `openssl rand -hex 24` |
| `vault_staging_app_password` | `openssl rand -hex 24` |
| `vault_staging_auth_password` | `openssl rand -hex 24` |
| `vault_staging_auth_key` | `openssl rand -base64 32` |
| `vault_google_client_id` | Google Cloud Console (bagian "Login") |
| `vault_google_client_secret` | Google Cloud Console (bagian "Login") |
| `vault_staging_turnstile_secret` | *Secret key* widget Turnstile (`cloudflare.md` langkah 8) |
| `vault_smtp_url` | Resend (bagian "Login"): `smtp+starttls://resend:<api key>@smtp.resend.com:587` |
| `vault_monitor_password`, `vault_grafana_admin_password`, `vault_discord_webhook_staging`, `vault_discord_webhook_production`, `vault_obs_access_team`, `vault_obs_access_aud` | Bagian "Observability", langkah 1. Playbook gagal (`is undefined`) selama keenamnya belum ada |

Empat kata sandi pertama dibuat dengan perintah di bawah. Setiap kata sandi harus berbeda dan hanya hex, karena nilainya masuk ke URL koneksi dan berkas `.env` tanpa tanda kutip:

```sh
for i in 1 2 3 4; do openssl rand -hex 24; done
openssl rand -base64 32
```

Contoh isi akhir (nilai asli jangan ditempel ke chat atau di-commit dalam bentuk teks biasa):

```yaml
vault_ghcr_user: dikym
vault_ghcr_token: ghp_...
vault_postgres_superuser_password: <hex>
vault_staging_owner_password: <hex>
vault_staging_app_password: <hex>
vault_staging_auth_password: <hex>
vault_staging_auth_key: <base64>
vault_google_client_id: xxx.apps.googleusercontent.com
vault_google_client_secret: <secret>
vault_staging_turnstile_secret: <secret>
vault_smtp_url: smtp+starttls://resend:re_...@smtp.resend.com:587
```

**Memeriksa.** Jalankan dari `~/terka-api/deploy/ansible`:

```sh
head -1 group_vars/all/vault.yml                                   # $ANSIBLE_VAULT;1.1;AES256
ansible-vault view group_vars/all/vault.yml | grep -c '^vault_'    # 17
ansible-vault view group_vars/all/vault.yml | cut -d: -f1 | cat -A # nama kunci saja
```

Baris pertama harus `$ANSIBLE_VAULT;1.1;AES256`; jika berisi teks biasa, berkas belum terenkripsi, jadi hapus dan buat ulang. Pada hasil `cat -A`, setiap nama harus rata kiri dan berakhir dengan `$` saja. `^M$` (akhir baris Windows), `^I` (tab), spasi di depan, atau `M-...` (karakter tak terlihat dari hasil menempel) membuat Ansible membaca nama kunci yang berbeda dari yang tampak.

Untuk memastikan Ansible membaca satu kunci, tanpa menampilkan nilainya:

```sh
ansible vps -m debug -a "msg={{ vault_postgres_superuser_password | length }}" --ask-vault-pass
```

Hasil yang benar adalah `"msg": 48`.

**Mengubah.** `ansible-vault edit group_vars/all/vault.yml`, lalu jalankan playbook lagi. Berkas `.env` di VPS ditulis ulang oleh playbook, tetapi container membaca `.env` saat dibuat, jadi deploy ulang layanan yang bersangkutan (bagian "Deploy manual") agar nilai barunya terpakai.

**Cadangan.** Jika VPS hilang, vault yang hanya ada di sana ikut hilang, dan kata sandi vault saja tidak cukup. Simpan salinan terenkripsi `vault.yml` di luar VPS, misalnya di laptop atau di repo (berkas terenkripsi boleh di-commit).

Kegagalan yang umum (`Decryption failed`, `is undefined`) dijelaskan di bagian "Masalah umum".

### Jalan pertama

**[VPS]**, sebagai `ubuntu`, dalam `~/terka-api/deploy/ansible`:

```sh
ADMIN_KEY='ssh-ed25519 AAAA... komentar'   # kunci publik laptop dari langkah 1
ansible-playbook site.yml --ask-vault-pass -e "admin_ssh_public_key='$ADMIN_KEY'"
```

`ADMIN_KEY` hanya hidup selama sesi SSH itu. Setelah sesi baru, definisikan lagi sebelum menjalankan perintah `ansible-playbook` apa pun, atau tulis kuncinya langsung di perintah. Pengguna `ubuntu` di Lighthouse punya sudo tanpa kata sandi. Jika tidak, tambahkan `--ask-become-pass`.

Playbook membuat pengguna admin (`terka-admin`) dengan kunci laptop Anda, lalu di akhir mematikan login password dan login `root`. Hasil yang benar: `failed=0` di `PLAY RECAP`. Peringatan `DEPRECATION WARNING ... to_native` berasal dari collection `ansible.posix`, bukan dari playbook, dan tidak berbahaya. Memperbarui collection dengan `ansible-galaxy collection install -r requirements.yml --upgrade` menghilangkannya jika versi baru sudah memperbaikinya.

**Jangan menutup sesi SSH ini.** **[Laptop]** Buka terminal baru dan pastikan ini berhasil:

```sh
ssh terka-admin@<ip-vps>
```

Jika gagal, perbaiki dari sesi `ubuntu` yang masih terbuka (`/home/terka-admin/.ssh/authorized_keys` harus berisi kunci publik laptop). Jika sesi sudah tertutup, masuk lewat console web Lighthouse.

### Jalan berikutnya

Perintah yang sama, dari VPS, dengan `-e admin_ssh_public_key` selalu disertakan (VPS tidak punya `~/.ssh/id_ed25519.pub`, nilai bawaannya akan gagal). Jalan kedua tanpa perubahan apa pun harus berakhir dengan `changed=0`. Setelah mengubah berkas di `deploy/`, jalankan dulu `rsync` di langkah 2 dari laptop.

Salinan di `~/terka-api` milik `ubuntu` dan hanya dipakai saat playbook berjalan; yang dipakai saat runtime adalah `/opt/terka/`. Pengguna `ubuntu` tetap ada setelah jalan pertama, jadi cara termudah adalah terus menjalankan playbook sebagai `ubuntu`. Untuk menjalankannya sebagai `terka-admin`, salin `deploy/` ke home-nya (ulangi dua perintah di langkah 2 dengan `terka-admin@<ip-vps>`), karena home `ubuntu` tidak bisa dibaca `terka-admin`. Vault dan inventory ikut perlu disiapkan di salinan itu.

### Pemeriksaan

**[Laptop]**:

```sh
ssh terka-admin@<ip-vps> 'swapon --show; free -m; docker compose version; sudo stat -c %a /opt/terka/staging/.env'
ssh root@<ip-vps>                                                             # harus ditolak
ssh -o PasswordAuthentication=yes -o PubkeyAuthentication=no deploy@<ip-vps>  # harus ditolak
```

Harapan: `swapon --show` tidak kosong, Docker Compose terpasang, `.env` bermode `600`. Playbook membuat `/swapfile` 2G hanya jika VPS belum punya swap. Image Lighthouse sudah membawa `/swap.img` 2G, jadi di sana tidak ada `/swapfile` baru.

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
observability dipasang. Diambil 2026-11-15, setelah Postgres berjalan dan sebelum API dan UI.

```
               total        used        free      shared  buff/cache   available
Mem:            1962         709         133          33        1343        1253
Swap:           4095          27        4068
```

Swap 4 GB di VPS ini adalah `/swap.img` 2G bawaan image ditambah `/swapfile` 2G dari playbook versi
lama. Playbook sekarang tidak membuat `/swapfile` jika sudah ada swap.

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

**[VPS]** Menyalakan Caddy tepi, sebagai `deploy` (`ssh terka-admin@<ip-vps>`, lalu `sudo -iu deploy`). Dua syarat harus terpenuhi lebih dulu; tanpa salah satunya Caddy gagal mulai:

1. Image bisa ditarik. Versinya ada di `deploy/caddy/VERSION` dan di `deploy/compose.infra.yaml`:
   ```sh
   docker pull ghcr.io/dikym/terka-caddy:2.11.9-p1
   ```
   Jika galatnya `not found`, workflow `caddy` belum menerbitkannya. Workflow itu jalan otomatis hanya saat push ke `main` mengubah `deploy/caddy/**`, dan push pertama repositori tidak memicunya. **[Laptop]** (browser) terbitkan secara manual: Actions, workflow `caddy`, **Run workflow**, branch `main`. Job `publish` menolak menimpa tag yang sudah ada. Setelah sukses, paket `terka-caddy` muncul di tab Packages profil GitHub Anda.
2. Sertifikat asal terpasang (`cloudflare.md` langkah 4, bagian "Memasang"):
   ```sh
   ls -l /opt/terka/infra/certs   # origin.pem dan origin-key.pem, pemilik deploy, mode 600
   ```

3. Network `terka-edge` ada. Playbook membuatnya; Caddy hanya bergabung ke network ini:
   ```sh
   docker network inspect -f '{{.Name}}' terka-edge
   ```

Nyalakan dan periksa:

```sh
docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d caddy
docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml ps
docker logs --tail 20 terka-infra-caddy-1
```

Harapan: Caddy berstatus `running` dan log tidak berisi galat sertifikat. Sebelum API dan UI di-deploy, permintaan ke situs dijawab galat 5xx (biasanya `502`) karena belum ada yang dilayani; itu normal. Selama Caddy dimulai ulang, situs tidak bisa diakses beberapa detik.

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

Versi baru dijalankan di samping versi lama. Ia baru menerima lalu lintas setelah sehat, saat
`deploy.sh` menyambungkannya ke network `terka-edge`. Setelah itu versi lama dihentikan dengan
memberi permintaan yang sedang berjalan waktu untuk selesai.

Kode keluar: `0` berhasil, `1` deploy lain untuk layanan dan lingkungan yang sama sedang berjalan,
atau tarik image, migration, atau daftar container gagal (versi yang jalan tidak disentuh),
`2` versi baru gagal dijalankan, tidak sehat, gagal smoke test (khusus `api`: `terka smoke` membuat, membaca, dan mencabut sesi akun sintetis `smoke@terka.invalid` sebelum lalu lintas masuk), atau gagal bergabung ke `terka-edge`, lalu dihapus;
versi lama terus melayani tanpa jeda, `3` hal yang sama pada deploy pertama, dan tidak ada
container yang tersisa, `64` argumen salah. Migration hanya dijalankan untuk `api`.

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

**Batas rollback API.** `deploy.sh` menjalankan `/terka smoke` di container baru, dan subcommand itu baru
ada di image sejak commit `dd164a2`. Image API yang lebih tua menjawab `unknown command "smoke"`, sehingga
deploy-nya keluar `2` dengan `failed the smoke test` dan versi yang sedang jalan tetap melayani. Rollback
API hanya bisa ke SHA yang memuat `dd164a2`. Periksa di laptop sebelum rollback:

```sh
git merge-base --is-ancestor dd164a2 <sha sebelumnya> && echo "bisa di-rollback"
```

Jika SHA yang diinginkan lebih tua, perbaiki maju (commit perbaikan di atas `main`), atau rollback ke SHA
tertua yang masih memuat `dd164a2`. UI tidak terkena batas ini, karena smoke test hanya untuk API.

**Urutan saat mengubah `deploy.sh` yang memanggil fitur baru di image.** Push dan deploy image yang membawa
fitur itu dulu, lalu jalankan playbook yang memasang `deploy.sh` baru. Urutan sebaliknya membuat setiap
deploy API ditolak sampai image barunya ada.

## Migrasi ke network edge (sekali)

Hanya untuk VPS yang disiapkan sebelum `terka-edge` ada, yaitu ketika Caddy masih di network `terka`
dan API/UI masih punya alias di sana. VPS baru tidak perlu langkah ini. Urutannya penting: jika
Caddy dibuat ulang sebelum langkah 2, situs menjawab `502` sampai setiap layanan di setiap
lingkungan di-deploy ulang.

Jeda deploy selama langkah 1 sampai 3. Langkah 1 sudah memasang `deploy.sh` baru, sedangkan Caddy
baru pindah ke `terka-edge` di langkah 3. Deploy di antara keduanya memasang container baru hanya di
`terka-edge`, lalu menghentikan yang lama, sehingga Caddy tidak menemukan apa pun dan menjawab `502`
sampai langkah 3. Ini juga berlaku untuk deploy staging otomatis setiap push ke `main`, dari kedua
repo.

0. **[Laptop]** (browser) Di `terka-api` dan `terka-web`: Actions, workflow `deploy`, **⋯**,
   **Disable workflow**. Jangan push ke `main` sampai langkah 3 selesai.
1. **[Laptop]** `rsync` versi terbaru `deploy/` ke VPS (langkah 2 "Sekali saja"). **[VPS]** sebagai
   `ubuntu`, dalam `~/terka-api/deploy/ansible`, periksa sintaks lalu jalankan playbook:
   ```sh
   ansible-playbook site.yml --syntax-check
   ansible-playbook site.yml --ask-vault-pass -e "admin_ssh_public_key='$ADMIN_KEY'"
   ```
   Harapan: `failed=0`, dan task "Create the edge network" berstatus `changed`. Playbook juga
   menyalin `deploy.sh`, berkas compose, dan `Caddyfile.routes` yang baru.
2. **[VPS]** sebagai `deploy`, sambungkan setiap container API/UI yang sedang berjalan ke
   `terka-edge` dengan aliasnya:
   ```sh
   for env in staging production; do
     for svc in api web; do
       for id in $(docker ps -q --filter "label=com.docker.compose.project=terka-$env-$svc"); do
         docker network connect --alias "terka-$env-$svc" terka-edge "$id"
       done
     done
   done
   ```
3. **[VPS]** sebagai `deploy`, buat ulang Caddy di `terka-edge`. Situs tidak bisa diakses beberapa
   detik. Restart ini wajib: playbook mengganti berkas dengan inode baru, sedangkan Caddy me-mount
   berkas tunggal, sehingga `caddy reload` masih membaca isi lama.
   ```sh
   docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d caddy
   ```
   **[Laptop]** Periksa: `curl -s -o /dev/null -w '%{http_code}\n' https://staging.terka.my.id/healthz`
   memberi `200`, begitu juga `https://terka.my.id/healthz` jika production sudah berjalan. Setelah
   itu aktifkan lagi workflow `deploy` di kedua repo (**Enable workflow**).
4. **[VPS]** Deploy ulang API dan UI sekali per lingkungan (bagian "Deploy manual"), supaya setiap
   container dibuat oleh alur baru. Container lama dari compose masih punya alias di network
   `terka`, tetapi tidak berpengaruh karena Caddy tidak lagi ada di network itu.

## Deploy otomatis

Workflow `deploy` di **masing-masing** repo men-deploy layanannya ke staging setiap kali `ci` lulus pada push ke `main`. Produksi hanya lewat "Run workflow" dengan SHA, dan menunggu persetujuan.

Prasyarat: fase B, C, dan D di "Urutan pengerjaan" selesai, dan `ci` sudah menerbitkan image `ghcr.io/dikym/terka-api:<sha>` (atau `terka-web`) untuk commit yang akan di-deploy.

Di `terka-api`, `ci` hanya menerbitkan image setelah job `stack` lulus. Job itu menjalankan stack lokal dengan web stand-in, lalu `stack_test.sh` lengkap termasuk login email. Setelah deploy dan langkah verify, workflow `deploy` di `terka-api` menjalankan bagian publik `stack_test.sh` terhadap URL lingkungan itu (langkah "Check the public pages and the signed-out API"). Bagian ini hanya mengirim `GET`. Kalau langkah ini merah, versi baru sudah melayani tetapi ada yang rusak. Pilihannya: "Rollback manual", dan SHA itu jangan dipromosikan ke produksi. Workflow `deploy` milik `terka-web` belum menjalankan langkah ini.

### Menyiapkan sekali

1. **[Laptop]** Buat dua kunci khusus CI, tanpa passphrase. Letaknya bebas, asal di luar repo dan tidak memakai nama bawaan seperti `id_ed25519`; `~/.ssh/` cocok:
   ```sh
   ssh-keygen -t ed25519 -N '' -C terka-ci-api -f ~/.ssh/terka-ci-api
   ssh-keygen -t ed25519 -N '' -C terka-ci-web -f ~/.ssh/terka-ci-web
   ```
   Hasilnya, per kunci, satu berkas privat (`terka-ci-api`) dan satu berkas publik (`terka-ci-api.pub`). Berkas privat dibuat di laptop dan tidak boleh ada di VPS atau di repo.
2. **[Laptop]** Isi kunci publik di `deploy/ansible/group_vars/all/main.yml`, dari `cat ~/.ssh/terka-ci-api.pub` dan `cat ~/.ssh/terka-ci-web.pub`:
   ```yaml
   deploy_ci_public_keys:
     api: "ssh-ed25519 AAAA... terka-ci-api"
     web: "ssh-ed25519 AAAA... terka-ci-web"
   ```
   Salin ulang `deploy/` dengan `rsync` (langkah 2 di "Sekali saja"), lalu **[VPS]** jalankan playbook lagi. Kunci `api` hanya bisa menjalankan `deploy.sh api`, kunci `web` hanya `deploy.sh web`. Pemeriksaan di VPS:
   ```sh
   sudo cat /home/deploy/.ssh/authorized_keys
   ```
   Harus berisi `restrict,command="/opt/terka/bin/deploy.sh api" ssh-ed25519 ...` untuk tiap kunci yang diisi.
3. **[Laptop]** Ambil sidik kunci host VPS, satu baris saja:
   ```sh
   ssh-keyscan -t ed25519 <IP VPS> 2>/dev/null
   ```
   Hasilnya `<IP VPS> ssh-ed25519 AAAA...`. Baris komentar yang diawali `#` tidak diperlukan. Pastikan itu benar-benar VPS Anda dengan membandingkan sidik jarinya: di VPS jalankan `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`, di laptop `ssh-keyscan -t ed25519 <IP VPS> 2>/dev/null | ssh-keygen -lf -`. Kedua hasil harus identik.
4. **[Laptop]** (browser) Di **kedua** repo GitHub, Settings, Environments:
   - `staging`: tanpa aturan perlindungan.
   - `production`: Required reviewers = Anda sendiri.
5. **[Laptop]** (browser) Di setiap environment kedua repo (Settings, Environments, nama environment, Environment secrets), isi tiga secret. Job `staging` membaca environment `staging`, dan job `production` membaca `production`:

   | Secret | Isi |
   | --- | --- |
   | `DEPLOY_HOST` | **IP VPS**, bukan `staging.terka.my.id`. Domain itu di belakang proxy Cloudflare, dan Cloudflare tidak meneruskan SSH |
   | `DEPLOY_KNOWN_HOSTS` | Satu baris dari langkah 3. IP-nya harus sama persis dengan `DEPLOY_HOST`, karena workflow memakai `StrictHostKeyChecking=yes` |
   | `DEPLOY_SSH_KEY` | Isi lengkap kunci **privat**: `cat ~/.ssh/terka-ci-api` untuk `terka-api`, `cat ~/.ssh/terka-ci-web` untuk `terka-web`, dari `-----BEGIN OPENSSH PRIVATE KEY-----` sampai `-----END OPENSSH PRIVATE KEY-----` |

   Jika salah satu secret kosong atau belum dibuat, job tetap melewati langkah "Install the deploy key", lalu gagal di langkah "Deploy" dengan galat seperti `Could not resolve hostname :`.
6. Port 22 di firewall Lighthouse harus terbuka untuk runner GitHub, yang alamat IP-nya berubah-ubah (`cloudflare.md` langkah 6). Pengamannya adalah login dengan kunci saja.
7. **[Laptop]** Setelah ketiga secret tersimpan di GitHub, simpan kunci privat di pengelola kata sandi atau hapus dari laptop.
8. Deploy pertama: push ke `main`, atau di tab Actions pilih run `deploy` yang gagal lalu **Re-run jobs**. Deploy `terka-api` lebih dulu, lalu `terka-web` (bagian "Urutan rilis").

Periksa: setelah push ke `main` dan `ci` hijau, job `staging` di workflow `deploy` repo itu jalan sendiri, dan `/healthz` (API) atau `/_app/version.json` (UI) melaporkan SHA commit itu.

## Latihan rollback

Membuktikan bahwa versi rusak dibuang otomatis tanpa pernah melayani lalu lintas. Versi rusak berjalan di samping versi sehat sampai 60 detik, tetapi tidak disambungkan ke `terka-edge`, jadi Caddy tidak pernah meneruskan permintaan kepadanya. Latihan ini di staging, tidak memakai GitHub Actions: image dibangun di laptop, didorong langsung ke GHCR, dan di-deploy manual di VPS.

**Prasyarat**

- Layanan yang dilatih sudah ter-deploy dan sehat. `cat /opt/terka/staging/api.current` (atau `web.current`) berisi satu SHA, dan `/healthz` (API) atau `/_app/version.json` (UI) melaporkan SHA itu. Tanpa versi sebelumnya, hasilnya kode `3` dan tidak ada container yang tersisa.
- Token GitHub classic untuk mendorong image: nama `terka-drill-ghcr-write`, masa berlaku sependek mungkin, izin hanya `write:packages` (tambahkan `repo` jika `docker push` ditolak). Token VPS hanya `read:packages` dan tidak bisa dipakai di sini. Hapus token ini setelah latihan.

**Langkah**

1. **[Laptop]** Catat SHA sehat: `curl -s https://staging.terka.my.id/healthz`. Buat cabang sementara dari `main` yang merusak health check: untuk API, ubah `if err := d.Ping(ctx); err != nil {` di `server/internal/server/server.go` menjadi `... err != nil || true {` sehingga `/readyz` selalu menjawab 503; untuk UI, hapus `index.html` dari hasil build di Dockerfile. Jangan jalankan `make lint` di cabang ini, dan jangan `git push` cabangnya.
2. **[Laptop]** **Commit dulu**, baru hitung SHA rusak. SHA itu harus commit baru: tag image adalah SHA commit, dan jika SHA rusak sama dengan commit `main`, `docker push` menimpa image sehat milik CI dengan image rusak, lalu rollback pun gagal karena versi sebelumnya adalah tag yang sama.
   ```sh
   git commit -am "drill: break readyz"
   git status --porcelain                   # harus kosong
   export BAD=$(git rev-parse HEAD)
   git fetch -q && git branch -r --contains "$BAD"   # harus kosong
   docker manifest inspect ghcr.io/dikym/terka-api:$BAD   # harus gagal: "no such manifest"
   ```
   Jika `git branch -r --contains` menampilkan `origin/main`, atau `docker manifest inspect` berhasil, **berhenti**: SHA itu sudah ada di GHCR.
3. **[Laptop]** Bangun image-nya untuk VPS (Mac membangun arm64 secara bawaan) dan dorong ke GHCR, misalnya untuk API:
   ```sh
   echo "<token write:packages>" | docker login ghcr.io -u dikym --password-stdin
   docker build --platform linux/amd64 -f deploy/Dockerfile --build-arg VERSION=$BAD \
     -t ghcr.io/dikym/terka-api:$BAD .
   docker push ghcr.io/dikym/terka-api:$BAD
   ```
4. **[Laptop]** Di terminal kedua, pantau versi yang dilayani, dan biarkan berjalan sampai langkah 7:
   ```sh
   while sleep 1; do date +%T; curl -s https://staging.terka.my.id/healthz; echo; done
   ```
5. **[VPS]** sebagai `deploy` (`ssh terka-admin@<ip-vps>`, lalu `sudo -iu deploy`):
   ```sh
   /opt/terka/bin/deploy.sh api staging <SHA rusak>; echo "exit=$?"
   ```
6. **[VPS]** Harapan: keluaran memuat `<SHA rusak> did not turn healthy`, `<SHA rusak> was not admitted; <SHA sehat> keeps serving`, dan `exit=2`. `cat /opt/terka/staging/api.current` masih berisi SHA sehat. Hanya satu container yang tersisa, dengan image sehat:
   ```sh
   docker ps -a --filter label=com.docker.compose.project=terka-staging-api --format '{{.Names}} {{.Image}}'
   ```
7. **[Laptop]** Pantauan di langkah 4 **tidak pernah** menunjukkan `<SHA rusak>`. Jumlah detik ia muncul masuk kolom "Lama staging tidak sehat", dan harapannya `0`. Selain itu `curl -s https://staging.terka.my.id/healthz` melaporkan SHA sehat, dan `curl -s -o /dev/null -w '%{http_code}\n' https://staging.terka.my.id/readyz` memberi `200`.
8. **Bersihkan.** Hapus cabang sementara (`git switch main && git branch -D drill/rollback`) dan image lokal (`docker rmi ghcr.io/dikym/terka-api:$BAD`). Di GitHub, Packages, `terka-api`, hapus versi bertag SHA rusak, dan hapus token `write:packages`. `docker logout ghcr.io`.

**Hasil yang menandakan latihan gagal**

| Gejala | Artinya |
| --- | --- |
| `exit=0` | Perubahan tidak membuat `/readyz` gagal; periksa bahwa image berasal dari cabang latihan |
| `exit=1` | Tarik image atau migration gagal; periksa tag di GHCR dan token VPS. Versi yang berjalan tidak disentuh |
| `exit=3` | Tidak ada versi sebelumnya; deploy ulang SHA sehat dengan `deploy.sh api staging <SHA sehat>` |
| `/healthz` sempat melaporkan SHA rusak | Container rusak tersambung ke `terka-edge`; periksa bahwa `/opt/terka/bin/deploy.sh` sudah versi terbaru (jalankan playbook) |
| `exit=2` tetapi `/healthz` tidak sehat | Tag SHA sehat tertimpa. Lihat "Masalah umum" |

### Hasil

| Tanggal | Layanan | SHA sehat | SHA rusak | Kode keluar | Lama staging tidak sehat |
| --- | --- | --- | --- | --- | --- |
| (belum dilatih) | | | | | |

## Observability

Tumpukan observability (Alloy, Prometheus, Alertmanager, Loki, Grafana, dan Tempo yang opsional) berjalan di VPS yang sama untuk semua lingkungan, di `/opt/terka/obs/`. Playbook hanya menyalin berkasnya, seperti pada Postgres: Anda yang menyalakannya dengan tangan. Grafana hanya bisa dibuka lewat Cloudflare Access (`cloudflare.md` bagian 10); alert dikirim ke Discord, dan pemantau dari luar VPS berjalan di GitHub Actions.

Kerjakan setelah fase F (API dan UI sudah ter-deploy), karena langkah 4 membuat ulang container API dan UI.

### 1. Nilai baru di vault

Enam kunci baru di `group_vars/all/vault.yml` (total 17). Bentuknya ada di `vault.example.yml`.

| Kunci | Asal |
| --- | --- |
| `vault_monitor_password` | `openssl rand -hex 24`. Kata sandi role Postgres `terka_monitor` |
| `vault_grafana_admin_password` | `openssl rand -hex 24`. Admin lokal Grafana (jalan darurat, langkah 8) |
| `vault_discord_webhook_staging` | **[Laptop]** (Discord) Server Settings, Integrations, Webhooks, New Webhook di channel staging, **Copy Webhook URL** |
| `vault_discord_webhook_production` | Sama, untuk channel production |
| `vault_obs_access_team` | Awalan Team domain Zero Trust (bukan Team name), tanpa `.cloudflareaccess.com` (`cloudflare.md` bagian 10) |
| `vault_obs_access_aud` | AUD tag aplikasi Access (`cloudflare.md` bagian 10) |

URL webhook adalah kredensial: jangan ditempel ke chat atau repo. Playbook menuliskannya ke `/opt/terka/obs/secrets/discord-staging` dan `discord-production` (mode `0400`), dan `GF_AUTH_JWT_EXPECT_CLAIMS={"aud":"<AUD>"}` dirender Ansible ke `/opt/terka/obs/grafana.env`, jadi tidak ada yang perlu ditulis tangan.

**[VPS]** dalam `~/terka-api/deploy/ansible`:

```sh
ansible-vault edit group_vars/all/vault.yml
ansible-vault view group_vars/all/vault.yml | grep -c '^vault_'    # 17
```

### 2. Jalankan playbook

**[Laptop]** `rsync` ulang `deploy/` ke VPS (langkah 2 "Sekali saja"). **[VPS]** jalankan playbook (perintah di "Jalan berikutnya").

**Perhatian: jalan pertama sesudah perubahan ini me-restart Docker.** Playbook mengatur `{"log-driver": "journald"}` di `/etc/docker/daemon.json` lalu me-restart `docker`, sehingga **semua container berhenti sebentar** (Postgres, Caddy, API, UI): situs mati beberapa detik sampai container ber-`restart: unless-stopped` naik lagi. Lakukan di jam sepi. Playbook juga membuat jurnal sistem permanen dengan batas 1 GB (`/etc/systemd/journald.conf.d/terka.conf`) dan membuat network `terka-obs`.

Container yang sudah ada **tetap memakai driver log lama** (`json-file`) sampai dibuat ulang; hanya container baru yang masuk jurnal. Langkah 4 membuat ulang Caddy, API, dan UI.

Periksa (**[VPS]**):

```sh
docker info --format '{{.LoggingDriver}}'      # journald
docker network inspect -f '{{.Name}}' terka-obs
sudo ls -l /opt/terka/obs                      # compose.obs.yaml, .env, alloy.env, grafana.env, monitor.env, secrets/
docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml ps   # Postgres dan Caddy kembali running
```

### 3. Role Postgres untuk pemantauan

**[VPS]** sebagai `deploy` (`sudo -iu deploy`). Playbook menulis kata sandinya ke `/opt/terka/obs/monitor.env` sebagai `MONITOR_PASSWORD=...`; skrip membacanya dari lingkungan. Postgres harus sudah berjalan.

```sh
set -a; . /opt/terka/obs/monitor.env; set +a
/opt/terka/bin/create-monitor-role.sh
```

Harapan: `role terka_monitor is ready`. Skrip boleh dijalankan ulang; ia menyetel ulang kata sandi ke nilai di `monitor.env`, jadi jalankan lagi setiap kali `vault_monitor_password` berubah.

`terka_monitor` anggota `pg_monitor` dan tidak punya hak atas tabel mana pun, jadi tidak membaca data pengguna. Sifat yang perlu diketahui: `pg_monitor` membuat role ini melihat teks query semua database di `pg_stat_activity`.

### 4. Buat ulang Caddy, API, dan UI

Dikerjakan sebelum tumpukan menyala. Container API lama belum punya alamat Alloy di lingkungannya, jadi tidak mengirim metrik; jika tumpukan menyala lebih dulu, `ApiDown` menyala dan dikirim ke Discord. Sampai langkah 5 selesai, API mencatat peringatan `telemetry export failed` dan `grafana.terka.my.id` menjawab `502`: keduanya wajar.

**[VPS]** sebagai `deploy`. Caddy bergabung ke `terka-obs` (agar bisa meneruskan ke Grafana dan agar Alloy bisa mengambil metriknya) dan memakai `journald`; situs tidak bisa diakses beberapa detik.

```sh
docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d caddy
```

Lalu deploy ulang SHA yang sedang jalan, supaya container API dan UI baru dibuat dengan driver `journald` (ulangi per lingkungan):

```sh
/opt/terka/bin/deploy.sh api staging "$(cat /opt/terka/staging/api.current)"
/opt/terka/bin/deploy.sh web staging "$(cat /opt/terka/staging/web.current)"
```

Periksa:

```sh
docker inspect -f '{{.Name}} {{.HostConfig.LogConfig.Type}}' $(docker ps -q)   # journald untuk Caddy, API, dan UI
curl -s https://staging.terka.my.id/healthz                                     # SHA API
```

Postgres tetap `json-file` sampai dibuat ulang, dan lognya tidak masuk Loki. Itu boleh dibiarkan. Alloy membuang baris `DETAIL:` dari Postgres (isinya bisa memuat nilai kunci seperti email), jadi memindahkannya ke jurnal aman; kalau ingin, buat ulang di jam sepi dengan `docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d --force-recreate postgres` (koneksi API putus beberapa detik).

### 5. Nyalakan tumpukan

**[VPS]** sebagai `deploy`:

```sh
docker compose -p terka-obs -f /opt/terka/obs/compose.obs.yaml up -d --wait
docker compose -p terka-obs -f /opt/terka/obs/compose.obs.yaml ps
```

Harapan: Alloy, Prometheus, Alertmanager, Loki, dan Grafana `running`. Metrik API yang dibuat ulang di langkah 4 mulai masuk dalam satu menit, jadi `ApiDown` tidak menyala. Jika Prometheus menilai aturan sebelum kiriman pertama tiba, satu pesan `ApiDown` disusul pesan pulih bisa muncul di Discord pada menit pertama; abaikan. Tempo tidak ikut (butuh `--profile tempo`, langkah 10). Grafana dipublikasikan hanya di `127.0.0.1:3000`. Pemeriksaan: `docker stats --no-stream` menunjukkan batas memori tiap container (Grafana 320 MiB).

Grafana membaca kata sandi admin **hanya saat pertama kali membuat databasenya**. Mengubah `vault_grafana_admin_password` sesudahnya tidak berpengaruh. Untuk menggantinya, jalankan di container (sintaks menurut dokumentasi Grafana; periksa dengan `grafana cli admin reset-admin-password --help` di versi 13.3.1):

```sh
docker exec terka-obs-grafana-1 grafana cli admin reset-admin-password '<kata sandi baru>'
```

### 6. Buka Grafana lewat Access

**[Laptop]** (browser) buka <https://grafana.terka.my.id>, masukkan email pemilik, lalu kode PIN dari email. Harapan: masuk sebagai Admin; sumber data Prometheus dan Loki ada; dashboard Terka terisi. Pemeriksaan keamanan ada di `cloudflare.md` bagian 10.

Satu request bisa ditelusuri: di Explore, sumber data Loki, kueri `{job="journal"} | trace_id="<id>"` (ambil `trace_id` dari satu baris log API) menampilkan baris log akses dan log auth untuk request itu.

### 7. Alert uji

**[VPS]** sebagai `deploy`. `amtool` ada di container `alertmanager` dan butuh alamat Alertmanager lewat `--alertmanager.url`:

```sh
docker exec terka-obs-alertmanager-1 amtool alert add Test env=staging \
  --annotation=summary='Alert uji dari amtool' --alertmanager.url=http://localhost:9093
```

Harapan: pesan muncul di channel Discord staging dalam sekitar satu menit (`group_wait` bawaan 30 detik). Label `env=production` mengirim ke channel production. Alert uji berakhir sendiri setelah beberapa menit. Jika pesan tidak datang, lihat "Masalah umum".

### 8. Jalan darurat ke Grafana

Jika Cloudflare Access atau DNS bermasalah. **[Laptop]**:

```sh
ssh -L 3000:127.0.0.1:3000 terka-admin@<ip-vps>
```

Biarkan sesi terbuka, lalu buka <http://localhost:3000> dan masuk sebagai `admin` dengan `vault_grafana_admin_password`. Port 3000 hanya di loopback VPS, jadi tidak ada jalan lain dari internet.

### 9. Pemantau dari luar dan secret GitHub

Dua workflow terjadwal di repo `terka-api`: `healthz` (setiap 5 menit, memeriksa `https://staging.terka.my.id/healthz`, mengirim pesan ke Discord hanya saat keadaan berubah: mati atau pulih) dan `dns-check` (Senin 01:00 UTC, SPF, DKIM, dan DMARC email).

**[Laptop]** (browser) di repo `terka-api`, Settings, Secrets and variables, Actions, **New repository secret**:

| Secret | Isi |
| --- | --- |
| `DISCORD_WEBHOOK_STAGING` | URL webhook channel staging (sama dengan nilai vault); dipakai `healthz` |
| `DISCORD_WEBHOOK_PRODUCTION` | URL webhook channel production; dipakai `dns-check` |

Periksa: tab Actions, workflow `healthz`, **Run workflow** (cabang `main`); harapan hijau tanpa pesan. Uji pesan: hentikan container API staging sebentar (`docker stop <container api>` di VPS, lalu `docker start`) dan tunggu jadwal berikutnya: pesan "Critical · Staging · Health check failing", lalu "Resolved · Staging · Health check failing" setelah dinyalakan lagi.

Batasan jadwal GitHub:

- Workflow terjadwal hanya jalan dari **cabang bawaan** (`main`).
- Jadwal bisa terlambat 5 sampai 30 menit, jadi "setiap 5 menit" adalah niat, bukan jaminan.
- Di repo **publik**, jadwal dimatikan otomatis setelah 60 hari tanpa aktivitas di repo. Buka tab Actions dan aktifkan lagi (**Enable workflow**); aktivitas apa pun, seperti satu push, memulai hitungan baru.
- Selama staging mati, GitHub juga mengirim email ke pemilik pada **setiap** run terjadwal yang gagal, selain pesan Discord yang hanya sekali.

### 10. Menyalakan Tempo (opsional)

Trace tetap dibuat aplikasi, tetapi kolektor membuangnya. Nyalakan Tempo hanya jika aturan keputusan di `docs/spikes/2026-11-observability-memory.md` terpenuhi. **[Laptop]** ubah `obs_tempo_enabled: true` di `deploy/ansible/group_vars/all/main.yml`, `rsync` ke VPS. **[VPS]** jalankan playbook, lalu sebagai `deploy`:

```sh
docker compose -p terka-obs -f /opt/terka/obs/compose.obs.yaml --profile tempo up -d --wait
docker compose -p terka-obs -f /opt/terka/obs/compose.obs.yaml restart alloy grafana
```

Playbook mengganti `alloy/traces-drop.alloy` dengan `traces-tempo.alloy` dan memasang sumber data Tempo. Alloy dan Grafana membaca berkas itu saat mulai, jadi keduanya di-restart. Tempo punya batas 320 MiB.

Mematikan Tempo: `obs_tempo_enabled: false`, `rsync`, playbook, lalu `docker compose -p terka-obs -f /opt/terka/obs/compose.obs.yaml --profile tempo rm -sf tempo` dan `restart alloy grafana`. Sumber data Tempo **tetap ada di database Grafana**; hapus lewat UI (Connections, Data sources) jika mau.

### 11. Uji memori dua jam

Langkah pengukuran memakai tumpukan sungguhan dan staging hidup; caranya, tabel hasil, dan aturan keputusan ada di `docs/spikes/2026-11-observability-memory.md` ("Pengukuran di VPS").

## Masalah umum

| Gejala | Penyebab | Perbaikan |
| --- | --- | --- |
| `Decryption failed (no vault secrets were found that could decrypt)` | Kata sandi vault yang dimasukkan tidak cocok dengan yang dipakai saat membuat berkas | Coba `ansible-vault view group_vars/all/vault.yml` dan masukkan kata sandi dengan hati-hati. Jika benar-benar hilang, isi vault tidak bisa dipulihkan: hapus berkas, buat ulang, isi semua nilai lagi, lalu jalankan `create-env-db.sh` setelah playbook supaya kata sandi database yang baru berlaku |
| `'vault_xxx' is undefined` | Kunci tidak ada di vault, salah eja, atau memuat karakter tersembunyi | Bagian "Vault", "Memeriksa" (`cat -A` dan `msg=...length`); perbaiki dengan `ansible-vault edit` |
| `No inventory was parsed` dan `Could not match supplied host pattern` | Perintah dijalankan dari folder selain `~/terka-api/deploy/ansible` | `cd ~/terka-api/deploy/ansible` |
| `src (or content) is required` pada "Install the CI deploy keys" | Kedua kunci CI kosong dan isi berkas jadi kosong. Sudah diperbaiki di repo | `rsync` versi terbaru `deploy/` ke VPS, lalu jalankan playbook lagi |
| `rsync: ... No such file or directory` | Folder induk tujuan belum ada | Jalankan baris `mkdir -p` di langkah 2 "Sekali saja" |
| `ssh terka-admin@<ip-vps>` ditolak | Kunci publik laptop belum terpasang atau yang terpasang kunci lain | Dari sesi `ubuntu` yang masih terbuka, periksa `/home/terka-admin/.ssh/authorized_keys`; jalankan playbook lagi dengan `-e admin_ssh_public_key` yang benar |
| CI: `ssh: Could not resolve hostname :` | Secret `DEPLOY_HOST` kosong atau tidak ada di environment yang dipakai job | Isi di Settings, Environments, nama environment, Environment secrets |
| CI: `Permission denied (publickey)` | Kunci publik CI belum terpasang di VPS, atau kunci privat di secret bukan pasangannya | `sudo cat /home/deploy/.ssh/authorized_keys` di VPS; ulangi langkah 2 "Menyiapkan sekali" |
| CI: `Host key verification failed` | `DEPLOY_KNOWN_HOSTS` salah, atau IP-nya berbeda dari `DEPLOY_HOST` | Ambil ulang dengan `ssh-keyscan -t ed25519 <IP VPS> 2>/dev/null` |
| Deploy berhenti dengan kode keluar `1` | Tarik image gagal atau migration gagal | Periksa token GHCR (belum kedaluwarsa, izin `read:packages`) dan log migration; versi yang berjalan tidak disentuh |
| Deploy keluar `2` dengan `could not join terka-edge` dan `network terka-edge not found` | Playbook belum dijalankan sejak `terka-edge` diperkenalkan | Jalankan playbook (bagian "Jalan berikutnya"), lalu ikuti "Migrasi ke network edge". Versi lama tetap melayani selama itu |
| `another deploy of api in staging is running`, padahal tidak ada deploy yang berjalan | Deploy sebelumnya dimatikan paksa (`SIGKILL`, VPS reboot) sehingga kuncinya tertinggal | **[VPS]** Pastikan tidak ada `deploy.sh` yang berjalan (`pgrep -af deploy.sh`), lalu sebagai `deploy`: `rmdir /opt/terka/staging/api.lock` (sesuaikan lingkungan dan layanan) |
| Workflow `deploy` merah di langkah "Check the public pages and the signed-out API" | Versi baru (atau UI yang sedang jalan) melayani, tetapi salah satu pemeriksaan publik gagal. Baris `FAIL` di log menyebut yang mana | Ulangi dari laptop: `TERKA_STACK_URL=https://staging.terka.my.id TERKA_STACK_SIGNIN=0 bash deploy/scripts/stack_test.sh` (produksi: `https://terka.my.id`). Jika tetap gagal, "Rollback manual" atau perbaiki lalu deploy ulang. Jangan promosikan SHA itu. Pengecualian: SHA dari sebelum langkah ini ada membawa `stack_test.sh` lama yang mengabaikan `TERKA_STACK_URL` dan selalu gagal di sini; untuk SHA seperti itu, perintah laptop di atas yang menentukan |
| CI `ci` merah di job `stack` | Routing, login, cookie sesi lewat Caddy, atau migrasi rusak pada commit itu. Image tidak diterbitkan, jadi tidak ada yang di-deploy | Baca langkah "Show the stack's logs"; ulangi lokal dengan `make stack STAND_IN=1 && bash deploy/scripts/stack_test.sh` |
| Deploy API keluar `2` dengan `failed the smoke test` | Versi baru sehat (`/readyz` lulus) tetapi tidak bisa membuat, membaca lewat cookie, atau mencabut sesi. Versi lama terus melayani | Baris `smoke: ...` tepat di atasnya menyebut langkah yang gagal dan status HTTP-nya. Ulangi di container yang sedang jalan untuk membandingkan: `docker exec <container api> /terka smoke; echo $?` |
| Situs menjawab `502` setelah Caddy dibuat ulang | Container yang sedang berjalan belum bergabung ke `terka-edge` | Langkah 2 di "Migrasi ke network edge" |
| `DEPRECATION WARNING ... to_native` | Berasal dari collection `ansible.posix` | Abaikan, atau perbarui collection (bagian "Jalan pertama") |
| API tidak sehat setelah latihan rollback, padahal commitnya sama dengan `main` | Image latihan didorong dengan SHA commit yang sudah ada di `main`, sehingga tag sehat milik CI tertimpa | **[Laptop]** (browser) GitHub, Actions, `ci`, run untuk commit itu, **Re-run all jobs**: membangun ulang image bersih dan memicu `deploy`. Sementara itu, di VPS jalankan `deploy.sh api staging <SHA commit lain yang lulus ci>`. Cegah dengan langkah 2 di "Latihan rollback" |
| `swapon --show` menampilkan dua swap | `/swap.img` bawaan image ditambah `/swapfile` dari playbook versi lama | Aman dibiarkan; playbook sekarang tidak membuat `/swapfile` jika sudah ada swap |
| Grafana mati atau tidak sehat | Memori Grafana habis di batas 512 MiB, atau pengaturan JWT Access salah | `docker logs terka-obs-grafana-1`; cari `out of memory` di `dmesg`. Aturan menaikkan batas ada di spike memori |
| Alert uji tidak sampai ke Discord | URL webhook salah, atau Alertmanager tidak bisa membaca berkasnya | `docker logs terka-obs-alertmanager-1`; `sudo ls -l /opt/terka/obs/secrets` (pemilik `65534`, mode `0400`); perbaiki vault, jalankan playbook, lalu `docker restart terka-obs-alertmanager-1` |
| `https://grafana.terka.my.id` menjawab `403` | Permintaan tidak membawa header Access, atau JWT tidak lolos | Buka lewat browser dengan login Access; periksa `vault_obs_access_team` dan `vault_obs_access_aud`; jalan darurat di "Observability" langkah 8 |
| Log container tidak muncul di Loki | Container masih memakai driver `json-file` | `docker inspect -f '{{.HostConfig.LogConfig.Type}}' <container>`; buat ulang (langkah 4 "Observability") |
| Metrik Postgres kosong atau `PostgresDown` menyala | Role `terka_monitor` belum dibuat atau kata sandinya berbeda | Langkah 3 "Observability" |
