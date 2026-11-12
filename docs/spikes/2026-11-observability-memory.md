# Uji Risiko: Memori Tumpukan Observability

**Menjawab:** risiko 3 di bagian 11 spec.
**Status:** pengukuran awal di VM lokal selesai; pengukuran di VPS belum dilakukan.

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

Belum dilakukan. Langkahnya (pemilik): jalankan `spikes/obs-memory/compose.yaml` di VPS selama minimal
2 jam bersama tumpukan staging, lalu catat `docker stats --no-stream`, `free -m`, dan
`dmesg | grep -ci 'out of memory'` pada menit 0, 30, dan 120. Disarankan langsung dengan batas
Grafana 320 MiB.

| Menit | Grafana | Tempo | Alloy | Loki | Prometheus | `available` | OOM |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 0 | | | | | | | |
| 30 | | | | | | | |
| 120 | | | | | | | |

## Keputusan sementara untuk tahap 2

Kecuali pengukuran VPS berkata lain: metrik dan log masuk tumpukan (Prometheus, Loki, Grafana,
Alloy), Grafana diberi batas 320 MiB, dan **Tempo tidak dipasang**. Trace tetap dibuat aplikasi
lewat OpenTelemetry, sehingga Tempo bisa ditambahkan nanti tanpa mengubah kode, misalnya setelah VPS
dinaikkan ke 4 GB atau dengan sampling trace yang ketat.
