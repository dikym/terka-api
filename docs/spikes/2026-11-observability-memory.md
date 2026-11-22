# Uji Risiko: Memori Tumpukan Observability

**Menjawab:** risiko 3 di bagian 11 spec.
**Status:** pengukuran di VM lokal dan di VPS selesai (2026-11-22); Tempo tetap mati.

## Yang diuji

Prometheus (retensi 7 hari, batas 1 GB), Loki, Tempo, Grafana, dan Grafana Alloy sebagai kolektor,
semuanya dengan konfigurasi satu-proses dan image `latest` per 2026-11-12. Tiga container
`telemetrygen` mengirim beban sintetis lewat OTLP ke Alloy: 20 trace/detik, 50 metrik/detik,
20 log/detik.

Konfigurasi ada di `spikes/obs-memory/` (tidak di-commit). Satu perubahan dibutuhkan agar Tempo mau
jalan: blok `compactor` dihapus dari konfigurasinya, karena versi terbaru tidak mengenalnya lagi.

## Pengukuran awal: VM lokal

Mesin: VM Colima di Mac (aarch64, 2 GB RAM, tanpa swap), bersama Postgres 18 dev. Tidak ada
aplikasi atau Caddy yang ikut jalan. Diukur setelah sekitar 20 menit beban.

| Layanan | Batas | Pemakaian | Catatan |
| --- | --- | --- | --- |
| Grafana | 192 MiB, lalu 384 MiB | 281 MiB | **Dimatikan kernel di 192 MiB**; batas dinaikkan untuk melihat kebutuhannya |
| Tempo | 192 MiB | — | **Dimatikan kernel (OOM)** di bawah beban; ekspor trace gagal sejak itu |
| Alloy | 128 MiB | 98 MiB | Dekat batas |
| Loki | 192 MiB | 88 MiB | |
| Prometheus | 192 MiB | 48 MiB | |
| Postgres dev | tanpa batas | 159 MiB | Pembanding, bukan bagian tumpukan |

`free -m` di VM: `available` 594 MB.

Menurut aturan keputusan rencana (jumlah kelima layanan ≤ 700 MB, tanpa pembunuhan oleh kernel,
`available` ≥ 300 MB):

- **Tumpukan penuh tidak lolos.** Tempo tidak bertahan di 192 MiB. Dengan batas yang cukup untuk
  Tempo (perkiraan 250–320 MiB) jumlahnya sekitar 760–830 MiB.
- **Tanpa Tempo lolos:** 281 + 98 + 88 + 48 = 515 MiB.
- Batas yang direncanakan untuk Grafana (192 MiB) terlalu kecil; butuh sekitar 320 MiB.

## Pengukuran di VPS

Dilakukan 2026-11-22 (hasil di bawah tabel). Memakai **tumpukan sungguhan** (`deploy/compose.obs.yaml`, dipasang menurut
bagian "Observability" di `docs/runbooks/deploy.md`), bukan `spikes/obs-memory/`. Batas memori di compose:
Grafana 512 MiB (`GOMEMLIMIT` 435 MiB), Alloy 256 MiB (`GOMEMLIMIT` 218 MiB), Prometheus dan Loki 192 MiB,
Alertmanager 64 MiB, Tempo 320 MiB.

Start pertama di VPS (2026-11-22) dengan batas lama: Grafana 320 MiB tidak pernah membuka port 3000
(85–89% batas, membaca disk 9 GB dalam beberapa menit), dan Alloy 192 MiB di 87% membaca disk 231 GB.
Keduanya thrashing: kernel membuang halaman binary dari cache lalu membacanya ulang, sehingga
`/proc/pressure/io` `full avg10` sekitar 90 dan seluruh host menunggu disk. Grafana sehat di 384 MiB
(sekitar 84–108 MiB terpakai setelah start); Alloy dinaikkan ke 256 MiB.

Pengukuran awal di lokal dengan tumpukan sungguhan (tanpa Tempo): Grafana diam sekitar 309 dari 320 MiB,
dengan bagian anonim sekitar 255 MiB. Artinya batas Grafana tipis, dan hasil di VPS menentukan apakah
cukup.

Langkah (pemilik), **[VPS]**: dengan staging hidup (API dan UI ter-deploy, ada lalu lintas atau
`healthz` jalan) dan tumpukan menyala **tanpa Tempo**, selama minimal 2 jam. Catat pada menit 0, 30,
dan 120: `docker stats --no-stream`, `free -m`, dan `dmesg | grep -ci 'out of memory'`. Gunakan
`docker inspect -f '{{.State.OOMKilled}} {{.RestartCount}}' $(docker ps -q)` untuk memeriksa pembunuhan
dan restart per container.

| Menit | Grafana | Alloy | Loki | Prometheus | Alertmanager | `available` | OOM |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 0 | tidak dicatat | | | | | | |
| 30 | tidak dicatat | | | | | | |
| lebih dari 120 | 363 / 384 MiB (naik-turun 105–384) | 141 / 256 MiB | 148 / 192 MiB | 100 / 192 MiB | 17 / 64 MiB | 1099 MB | 0 |

Hasil setelah lebih dari dua jam, dengan Grafana di batas 384 MiB: tidak ada OOM kill, tidak ada
restart, `available` 1099 MB, swap terpakai 514 MB. Kriteria selesai terpenuhi. Grafana tidak stabil:
pemakaiannya naik-turun 105–384 MiB setiap sekitar satu menit, menyentuh batas (99,97%), dan membaca
disk sekitar 1 GB per menit (total 181 GB). Itu thrashing di batas cgroup; `GOMEMLIMIT` hanya batas
lunak. Keputusan: Grafana dinaikkan ke 512 MiB (`GOMEMLIMIT` 435 MiB). Perkiraan setelah produksi ikut
jalan: sekitar 1,1–1,15 GB terpakai, `available` sekitar 800–900 MB. Tempo tetap mati sampai ada
pengukuran dengan produksi atau VPS 4 GB.

### Aturan keputusan

Kriteria selesai tahap 2 (spec bagian 9): setelah dua jam dengan staging dan tumpukan hidup, tidak ada
OOM kill dan `available` minimal 300 MB.

- **Batas Grafana.** Jika Grafana dimatikan kernel atau dimulai ulang, atau pemakaiannya pada menit 120
  lebih dari 90% batas (di atas sekitar 288 MiB): naikkan `mem_limit` Grafana ke 384 MiB (dan
  `GOMEMLIMIT` ke 326MiB, sekitar 85%) di `deploy/compose.obs.yaml`, deploy ulang, dan ukur lagi. Naikkan
  hanya jika `available` tetap minimal 300 MB sesudahnya; jika tidak, kurangi hal lain lebih dulu
  (retensi Prometheus atau Loki) sebelum menaikkan batas.
- **Tempo.** Tetap mati (`obs_tempo_enabled: false`) kecuali `available` pada menit 120, dikurangi 320 MiB
  (batas Tempo), masih minimal 300 MB. Jika ya, nyalakan (runbook deploy, "Observability" langkah 10) dan
  ulangi pengukuran dua jam; jika tidak lagi memenuhi kriteria, matikan lagi. Jika `available` tanpa Tempo
  sudah di bawah 300 MB, tumpukan tanpa Tempo pun perlu dikurangi dulu.
- Catat keputusan dan angkanya di bagian berikut.

## Keputusan sementara untuk tahap 2

Kecuali pengukuran VPS berkata lain: metrik dan log masuk tumpukan (Prometheus, Loki, Grafana,
Alloy), Grafana diberi batas 320 MiB, dan **Tempo tidak dipasang**. Trace tetap dibuat aplikasi
lewat OpenTelemetry, sehingga Tempo bisa ditambahkan nanti tanpa mengubah kode, misalnya setelah VPS
dinaikkan ke 4 GB atau dengan sampling trace yang ketat.
