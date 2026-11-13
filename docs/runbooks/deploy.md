# Runbook: VPS dan Deploy

VPS: Tencent Cloud Lighthouse, region Singapura, 2 vCPU, 2 GB RAM, 40 GB SSD.

## Menyiapkan VPS

Dikerjakan dari laptop pemilik, di `deploy/ansible/`.

### Sekali saja

1. Pasang Ansible: `brew install ansible` (paket lengkap; sudah membawa `ansible.posix` dan
   `community.general`). Dengan `ansible-core` saja, jalankan juga
   `ansible-galaxy collection install -r requirements.yml`.
2. Pastikan ada kunci SSH `~/.ssh/id_ed25519.pub`. Kunci lain: ubah `admin_ssh_public_key` di
   `group_vars/all/main.yml`.
3. Buat token GitHub (classic) dengan izin **`read:packages` saja**, untuk menarik image dari GHCR.
4. Buat vault: `ansible-vault create group_vars/all/vault.yml`, isi sesuai `vault.example.yml`.
   Setiap kata sandi dibuat dengan `openssl rand -hex 24`. Simpan kata sandi vault di pengelola
   kata sandi, bukan di repo.
5. Salin `inventory.example.ini` ke `inventory.ini` dan isi alamat VPS.

### Jalan pertama

Masuk sebagai pengguna bawaan image (`ubuntu` di Lighthouse, atau `root`):

```sh
ansible-playbook site.yml --ask-vault-pass
```

Playbook membuat pengguna admin (`terka-admin`) dengan kunci Anda, lalu di akhir mematikan login
password dan login `root`. Sebelum menutup sesi lama, buka terminal baru dan pastikan
`ssh terka-admin@<vps>` berhasil.

### Jalan berikutnya

Ganti `ansible_user` di `inventory.ini` menjadi `terka-admin`, lalu jalankan perintah yang sama.
Jalan kedua tanpa perubahan apa pun harus berakhir dengan `changed=0`.

### Pemeriksaan

```sh
ssh terka-admin@<vps> 'swapon --show; free -m; docker compose version; sudo stat -c %a /opt/terka/staging/.env'
ssh root@<vps>                                                             # harus ditolak
ssh -o PasswordAuthentication=yes -o PubkeyAuthentication=no deploy@<vps>  # harus ditolak
```

Harapan: swap 2G, Docker Compose terpasang, `.env` bermode `600`.

## Postgres dan database lingkungan

Setelah playbook jalan, masuk sebagai `terka-admin` lalu pindah ke pengguna `deploy`:

```sh
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

## Caddy

Caddy tepi memakai image `ghcr.io/dikym/terka-edge:<sha>` yang dibangun CI `terka-api` (job
`edge`). Image ini berisi Caddy yang dibangun ulang dengan modul yang sudah ditambal, dan build-nya
gagal kalau masih ada celah yang diketahui.

1. Isi `terka_edge_image` di `deploy/ansible/group_vars/all/main.yml` dengan tag dari push terakhir
   ke `main`, lalu jalankan playbook lagi (tag masuk ke `/opt/terka/infra/.env`).
2. Setelah sertifikat asal terpasang (`docs/runbooks/cloudflare.md` langkah 4), sebagai `deploy`:

```sh
docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d caddy
```

Memperbarui Caddy tepi: ganti `terka_edge_image`, jalankan playbook, lalu perintah yang sama.
Selama Caddy dimulai ulang, situs tidak bisa diakses beberapa detik.

Tambalan modul ada di `deploy/caddy/Dockerfile` (dan salinan identiknya di `terka-web/Dockerfile`).
Setiap kali Caddy merilis versi baru, naikkan versinya di kedua berkas dan hapus `--replace` yang
sudah tercakup rilis itu.

## Urutan rilis

API (`terka-api`) dan UI (`terka-web`) di-deploy terpisah. Aturannya:

- API wajib tetap melayani versi UI sebelumnya.
- UI yang butuh endpoint atau kolom baru di-deploy **setelah** API yang menyediakannya.
- Endpoint dihapus dari API hanya setelah UI yang memakainya tidak lagi beredar.

Belum ada pemeriksaan otomatis; aturan ini dijaga dengan disiplin sampai E2E ada.

## Deploy manual

Image harus sudah ada di GHCR dengan tag SHA commit 40 karakter (dibuat CI repo masing-masing pada
push ke `main`): `ghcr.io/dikym/terka-api:<sha>` atau `ghcr.io/dikym/terka-web:<sha>`. Sebagai
`terka-admin` di VPS:

```sh
sudo -iu deploy /opt/terka/bin/deploy.sh api staging <sha>
sudo -iu deploy /opt/terka/bin/deploy.sh web staging <sha>
```

Kode keluar: `0` berhasil, `1` tarik image atau migration gagal (versi yang jalan tidak disentuh),
`2` versi baru gagal dijalankan atau tidak sehat, dan versi sebelumnya sudah dijalankan lagi,
`3` hal yang sama pada deploy pertama, dan containernya dihentikan, `64` argumen salah. Migration
hanya dijalankan untuk `api`.

Versi yang sedang jalan: `/opt/terka/<env>/api.current` dan `/opt/terka/<env>/web.current`.
Dari luar: `curl -s https://staging.terka.my.id/healthz` (API) dan
`curl -s https://staging.terka.my.id/_app/version.json` (UI).

## Rollback manual

Deploy ulang SHA sebelumnya dengan perintah yang sama. Untuk API ini aman karena migration hanya
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

1. Buat dua kunci khusus CI, tanpa passphrase, di laptop:
   ```sh
   ssh-keygen -t ed25519 -N '' -C terka-ci-api -f ~/terka-ci-api
   ssh-keygen -t ed25519 -N '' -C terka-ci-web -f ~/terka-ci-web
   ```
2. Isi `deploy_ci_public_keys.api` dan `deploy_ci_public_keys.web` di
   `deploy/ansible/group_vars/all/main.yml` dengan isi berkas `.pub`-nya, lalu jalankan playbook lagi.
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
