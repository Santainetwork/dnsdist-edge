# Trust-NG Edge Node (Native OS) Architecture 🚀

Dokumen ini berisi panduan, konsep, dan rekomendasi operasional untuk melakukan deployment DNSDist Edge Node secara Native (Baremetal / LXC / VM) tanpa menggunakan Docker.

---

## 🏗️ 1. Konsep Pemisahan Beban (Central vs Edge)

Untuk menghemat RAM dan CPU di server *Edge*, proses filtering dibagi menjadi dua lapisan:
- **Central Manager (Pembuat CDB):** Mengkompilasi database puluhan juta domain menjadi `blacklist.db` (di-*host* via HTTP Nginx).
- **Edge Node (DNS Resolver):** Menjalankan script sinkronisasi (`update-blacklist.sh`) secara berkala untuk menarik file `.db` dan melakukan *hot-reload* tanpa henti. Edge tidak melakukan pengolahan TXT ke CDB.

---

## 🛠️ 2. Penjelasan Instalasi Skrip (`setup-edge.sh`)

Skrip otomatis `setup-edge.sh` dirancang untuk distro turunan Debian/Ubuntu. Skrip tersebut melakukan hal berikut:
1. Menginstal `dnsdist` menggunakan paket *apt* resmi.
2. Menyalin konfigurasi `dnsdist.conf` dari repositori ke `/etc/dnsdist/dnsdist.conf`.
3. Men-generate *Self-Signed Certificate* untuk DoT dan DoH di `/etc/dnsdist/certs/`.
4. Membuat direktori database khusus di `/var/lib/dnsdist/` untuk meletakkan `blacklist.db`.
5. Mengaktifkan *Systemd Service* (`systemctl enable dnsdist`).
6. Mendaftarkan **Cronjob** otomatis.

---

## 🔒 3. Rekomendasi Sertifikat Produksi (DoT & DoH)

Secara bawaan, DNS over TLS (853) dan HTTPS (443) menggunakan sertifikat *Self-Signed*. Klien dan browser OS modern **akan menolaknya**. Untuk Produksi, ganti menggunakan sertifikat **Let's Encrypt**:

1. Pastikan domain Edge Anda (contoh: `dns1.trust-ng.id`) telah diarahkan ke IP server LXC ini.
2. Instal Certbot:
   ```bash
   apt-get install -y certbot
   ```
3. Generate Sertifikat:
   ```bash
   certbot certonly --standalone -d dns1.trust-ng.id
   ```
4. Ubah file `/etc/dnsdist/dnsdist.conf` Anda untuk menunjuk ke sertifikat Certbot:
   ```lua
   -- Cari bagian addTLSLocal dan addDOHLocal, ganti path sertifikatnya menjadi:
   addTLSLocal('0.0.0.0:853', '/etc/letsencrypt/live/dns1.trust-ng.id/fullchain.pem', '/etc/letsencrypt/live/dns1.trust-ng.id/privkey.pem', { provider = 'openssl', minTLSVersion = 'tls1.2' })
   ```
5. **Penting:** DNSDist Native berjalan menggunakan *user system* bernama `_dnsdist`. User ini tidak bisa membaca isi `/etc/letsencrypt` secara bawaan. Anda harus memberikan akses baca:
   ```bash
   chmod 755 /etc/letsencrypt/live /etc/letsencrypt/archive
   chgrp -R _dnsdist /etc/letsencrypt/live /etc/letsencrypt/archive
   chmod -R g+rX /etc/letsencrypt/live /etc/letsencrypt/archive
   ```

---

## ⏰ 4. Crontab: Auto Sync Database 

Skrip cronjob diletakkan di `/usr/local/bin/update-blacklist.sh`.
Ia bertugas mengambil pembaruan file `blacklist.db` dari *Central Manager* setiap 3 Jam.
DNSDist akan memuat ulang (`hot-reload`) database 5 detik setelah file berubah.

Log sinkronisasi dapat dipantau melalui:
```bash
tail -f /var/log/dnsdist-sync.log
```

---

## 🚀 5. Tuning Kernel Linux (Untuk Skala Besar / High-Traffic)

Karena DNSDist sekarang berjalan langsung di Kernel OS (bukan Docker), melakukan *tuning Sysctl* akan memberikan dampak kecepatan (*throughput*) jaringan UDP yang masif.

Tambahkan teks di bawah ini ke file `/etc/sysctl.conf` dan terapkan dengan `sysctl -p`:

```ini
# Menangani puluhan ribu antrean DNS UDP socket
net.core.rmem_max=16777216
net.core.rmem_default=16777216
net.core.wmem_max=16777216
net.core.wmem_default=16777216
net.core.netdev_max_backlog=2000

# Optimalisasi TCP untuk DoT/DoH
net.ipv4.tcp_tw_reuse=1
net.ipv4.tcp_fin_timeout=15

# Menambah batas port yang bisa dibuka (jika terus-menerus mem-forward ke Upstream)
net.ipv4.ip_local_port_range=1024 65535

# Conntrack tuning (jika LXC mendukung akses ke conntrack module)
net.netfilter.nf_conntrack_max=1048576
```

---

## 🔍 6. Operasional & Monitoring

- **Cek Status Layanan:** `systemctl status dnsdist`
- **Melihat Log DNSDist:** `journalctl -fu dnsdist`
- **Restart Manual:** `systemctl restart dnsdist`

Untuk metrik Prometheus, endpoint dapat diakses dari Port `8083` (Path: `/metrics`). Tambahkan `X-API-Key` atau HTTP `Bearer Token` di *scrape config* Prometheus LXC monitoring Anda.
