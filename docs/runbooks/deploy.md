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

Setelah sertifikat asal terpasang (`docs/runbooks/cloudflare.md` langkah 4), sebagai `deploy`:

```sh
docker compose -p terka-infra -f /opt/terka/infra/compose.infra.yaml up -d caddy
```

## Deploy manual

Image harus sudah ada di GHCR dengan tag SHA commit 40 karakter (dibuat CI pada push ke `main`).
Sebagai `terka-admin` di VPS:

```sh
sudo -iu deploy /opt/terka/bin/deploy.sh staging <sha>
```

Kode keluar: `0` berhasil, `1` tarik image atau migration gagal (aplikasi lama tetap jalan),
`2` versi baru tidak sehat dan sudah dikembalikan ke versi sebelumnya, `3` deploy pertama gagal dan
aplikasinya dihentikan, `64` argumen salah. Versi yang sedang jalan ada di
`/opt/terka/staging/current`.

## Rollback manual

Deploy ulang SHA sebelumnya dengan perintah yang sama. Ini aman karena migration hanya menambah;
image lama mengabaikan versi migration yang tidak dikenalnya. Migration tidak pernah dibatalkan.

```sh
sudo -iu deploy /opt/terka/bin/deploy.sh staging <sha sebelumnya>
```

SHA sebelumnya bisa dilihat di riwayat workflow `deploy` di GitHub atau dengan `git log` di repo.
