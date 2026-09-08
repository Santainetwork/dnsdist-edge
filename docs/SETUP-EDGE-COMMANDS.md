# 🛠️ Dokumentasi CLI `setup-edge.sh`

`setup-edge.sh` adalah skrip instalasi, konfigurasi, dan manajemen operasional untuk **DNSDist Edge Node (Trust-NG)** pada Debian/Ubuntu.

---

## 📊 Ringkasan Opsi CLI (19 Opsi)

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
| 19 | `-V`, `--version` / `-h`, `--help` | — | **Info** | Tampilkan versi / bantuan |

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
```

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

**Password panel:** sama dengan `--password` saat install. Ganti via Settings atau:
```bash
echo "passwordbaru" > /var/lib/dnsdist/panel.password
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
- Upstream DNS aktif
- Ukuran & path `blacklist.db`
- Hasil `dnsdist --check-config`
- Status service dnsdist + nginx

---

### 6. Cluster / Multi-source CDB

```bash
# Set sumber (central + mirror + peer)
sudo ./setup-edge.sh --set-cdb-sources \
  "http://central/trust.db,http://mirror/trust.db,http://peer:8443/cdb/blacklist.db"
```

Urutan failover: central → mirror → peer. DB lama dipertahankan jika semua gagal.

---

### 7. Upgrade

```bash
sudo ./setup-edge.sh --upgrade
```

Otomatis: update `update-blacklist.sh` + `dnsdist.conf` + migrasi path + restart.

---

### 8. Sync Database

```bash
# Normal (ETag cache)
sudo ./setup-edge.sh --sync-only

# Paksa re-download
sudo ./setup-edge.sh --force-update
```

---

### 9. Uninstall

```bash
sudo ./setup-edge.sh --uninstall
# Diminta konfirmasi sebelum hapus
```

---

## 📝 Catatan Versi

| Versi | Perubahan Utama |
|---|---|
| v2.5.0 | Central Master installer, Dual HTTP/HTTPS panel, Redesigned UI |
| v2.4.2 | Aria2c fast download, standalone --add-panel, update-blacklist dash fix |
| v2.4.1 | Panel HTTPS :8443 bugfix (path hardcode → configurable) |
| v2.4.0 | RPZ multi-IP + IPv6, python3 atomic patch, safesearch + DoT/DoH via panel |
| v2.3.0 | `--with-panel` auto-install panel binary |
| v2.2.0 | `--set-cdb-sources` cluster Phase 4 |
| v2.1.0 | Versi publik awal, 17 opsi CLI |
