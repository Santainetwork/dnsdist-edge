# 🛠️ Dokumentasi CLI `setup-edge.sh`

`setup-edge.sh` adalah skrip instalasi, konfigurasi, dan manajemen operasional untuk **DNSDist Edge Node (Trust-NG)** pada Debian/Ubuntu.

---

## 📊 Ringkasan Opsi CLI

| No | Opsi | Argumen | Kategori | Deskripsi |
|:---:|---|---|---|---|
| 1 | `-i`, `--install` | — | **Instalasi** | Install DNSDist + deps + cert + stats + cronjob + config awal |
| 2 | `-u`, `--url` | `<URL>` | **Config DB** | URL Central Manager untuk sync `blacklist.db` |
| 3 | `-s`, `--sync-only` | — | **Sync** | Sync database manual (dengan cache header) |
| 4 | `-f`, `--force-update` | — | **Sync** | Sync database paksa (bypass cache) |
| 5 | `-c`, `--check-config` | — | **Diagnostik** | Cek versi, mode, sinkhole IPs, upstream, DB, syntax, service |
| 6 | `--update-config` | — | **Config** | Wizard interaktif: ubah mode + upstream |
| 7 | `--upgrade` | — | **Maintenance** | Upgrade ke versi terbaru, migrasi otomatis |
| 8 | `--set-upstream` | `<IP1,IP2,...>` | **Config** | Set IP upstream DNS (IPv4, IPv6, IPv4:port, [IPv6]:port) |
| 9 | `--set-rpz` | `<IP1,IP2,...>` | **Config** | Set IP Sinkhole RPZ — **multi-IP + IPv6 (v2.4.0)** |
| 10 | `--set-cert` | `<1\|2\|3>` | **Cert** | Mode cert: `1`=Self-signed, `2`=Let's Encrypt, `3`=Disable |
| 11 | `--cert-domain` | `<domain>` | **Cert** | Domain untuk Let's Encrypt |
| 12 | `--cert-email` | `<email>` | **Cert** | Email Let's Encrypt |
| 13 | `--password` | `<PWD>` | **Web Console** | Set password login web console dnsdist (port 8083) |
| 14 | `--apikey` | `<KEY>` | **Web Console** | Set API Key dnsdist |
| 15 | `--set-webserver` | — | **Web Console** | Terapkan password + apikey ke config + restart |
| 16 | `--uninstall` | — | **Sistem** | Hapus instalasi lengkap |
| 17 | `--set-cdb-sources` | `<URL1,URL2,...>` | **Cluster** | Set daftar sumber CDB (central, mirror, peer) |
| 18 | `--with-panel` | — | **Panel** | Install panel HTTPS :8443 (download binary + systemd) |
| 19 | `--master-url` | `<URL>` | **Cluster** | URL Central Master untuk telemetri terpusat |
| 20 | `--enroll-token` | `<TOK>` | **Cluster** | Token pendaftaran node ke master cluster |
| 21 | `--node-name` | `<NAMA>` | **Cluster** | Nama node edge di dashboard cluster (default: hostname) |
| 22 | `-V`, `--version` / `-h`, `--help` | — | **Info** | Tampilkan versi / bantuan |
| 23 | `--transparent-dns` | `<off\|auto\|tproxy>` | **Jaringan** | Rencana/atur transparent DNS; `auto` hanya diagnostik |
| 24 | `--transparent-interface` | `<IFACE>` | **Jaringan** | Interface LAN untuk TPROXY |
| 25 | `--transparent-subnet` | `<CIDR>` | **Jaringan** | Subnet klien IPv4 untuk TPROXY |
| 26 | `--apply-transparent` | — | **Jaringan** | Izinkan perubahan nftables/routing; tanpa ini hanya plan |

---

## 💻 Contoh Penggunaan

### 1. Instalasi

```bash
# Interaktif standar
sudo ./setup-edge.sh --install

# Non-interaktif
sudo ./setup-edge.sh --install --url "http://central.local/trust.db"

# Lengkap + panel
sudo ./setup-edge.sh --install \
  --url "https://central.domain.id/trust.db" \
  --password "AdminRahasia123" \
  --apikey "apikey-edge-01" \
  --with-panel

# Terhubung ke Central Master Cluster (Mode A)
sudo ./setup-edge.sh --install \
  --url "http://10.10.10.1:8084/files/trust.db" \
  --master-url "http://10.10.10.1:8084" \
  --enroll-token "enroll-abc123" \
  --node-name "edge-jakarta-01"
```

#### Tambah Node melalui Web

1. Buka panel Master, lalu pilih **Cluster Nodes → Tambah Node**.
2. Isi URL panel Edge dan nama node. Master membuat token sekali pakai yang berlaku 10 menit.
3. Klik **Buat & Buka Panel Edge**, login ke panel Edge, lalu periksa data pada **Pengaturan**.
4. Klik **Hubungkan ke Master**. Node muncul pada daftar Master setelah heartbeat pertama.

Data enrollment dibawa melalui URL fragment agar tidak masuk access log HTTP. Jika popup gagal dibuka, gunakan tombol **Buka Panel Edge Lagi**, salin token manual, atau pakai perintah CLI yang ditampilkan.

---

### 2. RPZ Sinkhole — Multi-IP + IPv6 (v2.4.0)

```bash
# IPv4 tunggal
sudo ./setup-edge.sh --set-rpz "10.10.10.10"

# Multi IPv4
sudo ./setup-edge.sh --set-rpz "10.10.10.10, 10.10.10.11"

# IPv4 + IPv6 (dnsdist.conf auto-split A→IPv4, AAAA→IPv6)
sudo ./setup-edge.sh --set-rpz "10.10.10.10, 2001:db8::1"

# IPv6 saja (query A → NXDOMAIN, AAAA → IPv6 sinkhole)
sudo ./setup-edge.sh --set-rpz "2001:db8::1"
```

Format yang diterima:
- `10.10.10.10` — IPv4 bare
- `10.10.10.10:8080` — IPv4 dengan port
- `2001:db8::1` — IPv6 bare
- `[2001:db8::1]:5353` — IPv6 dengan port

Semua format boleh dicampur, dipisah koma atau spasi. IP tidak valid di-skip dengan warning.

---

### 3. Upstream DNS

```bash
# Standard
sudo ./setup-edge.sh --set-upstream "1.1.1.1, 8.8.8.8"

# Dengan IPv6
sudo ./setup-edge.sh --set-upstream "1.1.1.1, [2606:4700:4700::1111]:53, 8.8.8.8"

# Quad9 + Cloudflare
sudo ./setup-edge.sh --set-upstream "9.9.9.9, 1.1.1.1"
```

---

### 4. Panel Web (HTTPS :8443)

```bash
# Install saat pertama kali
sudo ./setup-edge.sh --install --with-panel

# Tambah panel ke node yang sudah ada
sudo ./setup-edge.sh --with-panel
```

Panel tersedia di `https://YOUR_IP:8443` dengan self-signed cert (otomatis).

**Fitur panel:**
- Dashboard: QPS, CPU, RAM, uptime, status dnsdist
- RPZ Sinkhole: set multi-IP, tampilkan aktif
- Upstream DNS: set upstream, tampilkan aktif
- SafeSearch: toggle Google/Bing/YouTube per provider
- DoT/DoH: enable/disable + cert path, restart otomatis
- Settings: block mode, ganti password panel

**Password panel:** sama dengan `--password` saat install. Jika opsi itu tidak diberikan pada instalasi baru, default-nya `trust-ng-admin`. File autentikasi panel berada di `/var/lib/dnsdist/panel.password`.

Reset dari shell lalu restart panel:
```bash
printf '%s\n' 'passwordbaru' | sudo tee /var/lib/dnsdist/panel.password >/dev/null
sudo chmod 600 /var/lib/dnsdist/panel.password
sudo systemctl restart dnsdist-panel
```

---

### 5. Diagnostik

```bash
sudo ./setup-edge.sh --check-config
```

Output:
- Versi terpasang vs script
- Mode blokir (`rpz` / `adguard`)
- IP Sinkhole aktif (bullet list, multi-IP)

### 6. Transparent DNS

```bash
# MikroTik sudah force-DNS ke IP Edge: tidak perlu TPROXY
sudo ./setup-edge.sh --transparent-dns off

# Diagnostik lokal. Tidak membaca konfigurasi MikroTik dan tidak mengubah host
sudo ./setup-edge.sh --transparent-dns auto \
  --transparent-interface eth1 --transparent-subnet 192.168.88.0/24

# Lihat rencana, lalu terapkan hanya bila Edge menjadi gateway trafik klien
sudo ./setup-edge.sh --transparent-dns tproxy \
  --transparent-interface eth1 --transparent-subnet 192.168.88.0/24
sudo ./setup-edge.sh --transparent-dns tproxy \
  --transparent-interface eth1 --transparent-subnet 192.168.88.0/24 \
  --apply-transparent
```

Mode TPROXY awal mendukung IPv4 UDP/TCP port 53. Detail prasyarat, rollback, dan verifikasi MikroTik: [TRANSPARENT-DNS.md](TRANSPARENT-DNS.md).
- Upstream DNS aktif
- Ukuran & path `blacklist.db`
- Hasil `dnsdist --check-config`
- Status service dnsdist + nginx

---

### 7. Cluster / Multi-source CDB

```bash
# Set sumber (central + mirror + peer)
sudo ./setup-edge.sh --set-cdb-sources \
  "http://central/trust.db,http://mirror/trust.db,http://peer:8443/cdb/blacklist.db"
```

Urutan failover: central → mirror → peer. DB lama dipertahankan jika semua gagal.

---

### 8. Upgrade

#### Edge Node

```bash
sudo ./setup-edge.sh --upgrade
```

Installer mengunduh `setup-edge.sh` terbaru lebih dahulu, menjalankan `bash -n`,
lalu mencoba membaca `SHA256SUMS` dari direktori release yang sama. Jika checksum
tersedia, entri harus cocok dengan nama file installer, berformat SHA-256 valid,
dan sama dengan hash hasil unduhan. Format atau hash yang tidak cocok membatalkan
upgrade tanpa mengganti installer lama. Jika `SHA256SUMS` belum dipublikasikan,
upgrade tetap memakai hasil validasi `bash -n`. Installer diganti secara atomik,
mempertahankan mode dan owner, lalu argumen `--upgrade` dijalankan kembali.

Setelah self-update: update `update-blacklist.sh` + `dnsdist.conf` + migrasi path + restart.

#### Central Master

```bash
sudo ./setup-master.sh --upgrade
```

Perintah Master memakai validasi checksum dan penggantian atomik yang sama. Saat
ini `--upgrade` memperbarui installer Master saja; jalankan `--install` terpisah
jika perlu menerapkan ulang konfigurasi layanan.

---

### 9. Sync Database

```bash
# Normal (ETag cache)
sudo ./setup-edge.sh --sync-only

# Paksa re-download
sudo ./setup-edge.sh --force-update
```

---

### 10. Uninstall

```bash
sudo ./setup-edge.sh --uninstall
# Diminta konfirmasi sebelum hapus
```

---

## 📝 Catatan Versi

| Versi | Perubahan Utama |
|---|---|
| v2.9.0 | Web Whitelist Master dan self-update installer dengan verifikasi SHA-256 |
| v2.8.0 | Transparent DNS E2 (`off|auto|tproxy`), proxy IPv4 UDP/TCP, dan sinkronisasi password panel |
| v2.7.0 | Perbaikan layout kartu Pengaturan Edge dan regression test markup |
| v2.6.0 | Web enrollment node, cleanup DB hash lama, browser-validated cluster flow |
| v2.5.0 | Central Master installer, Dual HTTP/HTTPS panel, Redesigned UI |
| v2.4.2 | Aria2c fast download, standalone --add-panel, update-blacklist dash fix |
| v2.4.1 | Panel HTTPS :8443 bugfix (path hardcode → configurable) |
| v2.4.0 | RPZ multi-IP + IPv6, python3 atomic patch, safesearch + DoT/DoH via panel |
| v2.3.0 | `--with-panel` auto-install panel binary |
| v2.2.0 | `--set-cdb-sources` cluster Phase 4 |
| v2.1.0 | Versi publik awal, 17 opsi CLI |
