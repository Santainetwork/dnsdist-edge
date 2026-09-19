# 🚀 Setup Guide — DNSDist Edge Node (Trust-NG)

Panduan lengkap deploy DNSDist Edge Node secara native (tanpa Docker).

---

## 📋 Prasyarat

| Item | Minimal | Rekomendasi |
|---|---|---|
| OS | Debian 11 / Ubuntu 20.04 | Debian 12 Bookworm |
| RAM | 2 GB | 8 GB |
| CPU | 2 core | 4 core |
| Storage | 20 GB SSD | 40 GB SSD |
| Python | 3.x (stdlib) | — |

```bash
sudo su -   # atau prefix tiap perintah dengan sudo
```

---

## 🔧 Instalasi Cepat

### 1. Download Repositori
```bash
cd /opt
git clone https://github.com/Santainetwork/dnsdist-edge.git
cd dnsdist-edge/setup
```

### 2. Jalankan Installer
```bash
# Install standar (interaktif)
sudo ./setup-edge.sh --install

# Non-interaktif dengan Central Manager URL
sudo ./setup-edge.sh --install --url http://central-manager.local:8080/files/trust.db

# Dengan password web console kustom
sudo ./setup-edge.sh --install --password mypassword --apikey myapikey

# Install + panel manajemen (HTTPS :8443)
sudo ./setup-edge.sh --install --with-panel
```

---

## 📝 Konfigurasi

### Mode Operasional

Saat install, pilih mode blokir:

| Mode | Return | Kegunaan |
|---|---|---|
| `rpz` | IP Sinkhole (halaman blokir) | ISP, operator |
| `adguard` | `0.0.0.0` / `::` (null route) | Privacy, internal |

### RPZ Sinkhole — Multi-IP + IPv6

Sejak v2.4.0, sinkhole mendukung **beberapa IP sekaligus**, termasuk IPv6:

```bash
# IPv4 tunggal
sudo ./setup-edge.sh --set-rpz "10.10.10.10"

# IPv4 + IPv6 (dipisah koma)
sudo ./setup-edge.sh --set-rpz "10.10.10.10, 2001:db8::1"

# Multiple IPv4
sudo ./setup-edge.sh --set-rpz "10.10.10.10, 10.10.10.11"
```

DNSDist otomatis memisahkan IPv4/IPv6:
- Query `A` → IP sinkhole **IPv4** saja
- Query `AAAA` → IP sinkhole **IPv6** jika ada, else `NXDOMAIN`
- Query `HTTPS/TXT/dll` → `NXDOMAIN`

Edit manual di `/etc/dnsdist/dnsdist.conf`:
```lua
SINKHOLE_IPS = {'10.10.10.10', '10.10.10.11', '2001:db8::1'}
```

### Upstream DNS

```bash
# IPv4
sudo ./setup-edge.sh --set-upstream "1.1.1.1, 8.8.8.8"

# IPv4 + IPv6
sudo ./setup-edge.sh --set-upstream "1.1.1.1, [2606:4700:4700::1111]:53"
```

### SafeSearch (via panel atau manual)

Panel menulis `/etc/dnsdist/safesearch.conf` yang di-load otomatis oleh `dnsdist.conf`.
Rewrite DNS ke IP paksa safesearch:

| Provider | IP Paksa |
|---|---|
| Google | `216.239.38.120` (A), `2001:4860:4802:32::78` (AAAA) |
| Bing | `204.79.197.220` |
| YouTube | `216.239.38.120` |

### DoT / DoH (via panel atau manual)

Panel menulis `/etc/dnsdist/dotdoh.conf`. Atau edit manual:

```lua
-- /etc/dnsdist/dotdoh.conf
addTLSLocal('0.0.0.0:853', '/etc/dnsdist/certs/server.crt', '/etc/dnsdist/certs/server.key',
  {provider='openssl', minTLSVersion='tls1.2'})
addTLSLocal('[::]:853', '/etc/dnsdist/certs/server.crt', '/etc/dnsdist/certs/server.key',
  {provider='openssl', minTLSVersion='tls1.2'})
```

---

## 🖥️ DNSDist Panel (Opsional — HTTPS :8443)

Panel web untuk manajemen node tanpa CLI. **Optional** — tanpa panel, semua fungsi tetap berjalan.

### Install via setup-edge.sh

```bash
sudo ./setup-edge.sh --install --with-panel
# atau setelah install:
sudo ./setup-edge.sh --with-panel
```

### Build dari Source

```bash
cd panel
bash build.sh
# Output: tools/dnsdist-panel (6.5 MB, stdlib only)
```

### Jalankan Manual

```bash
sudo /usr/local/bin/dnsdist-panel \
  -addr ":8443" \
  -config /etc/dnsdist/dnsdist.conf \
  -upstreams /etc/dnsdist/upstreams.conf
```

### Akses Panel

```
https://YOUR_SERVER_IP:8443
```

> ⚠️ Self-signed cert — browser akan warning, klik "Accept" / "Proceed".

Password default: sama dengan password web console dnsdist (`trust-ng-admin`).
Ganti di halaman **Settings** atau:
```bash
echo "passwordbaru" > /var/lib/dnsdist/panel.password
```

### Fitur Panel

| Halaman | Fungsi |
|---|---|
| Dashboard | QPS real-time, CPU, RAM, uptime, status dnsdist |
| RPZ Sinkhole | Set multi-IP sinkhole (IPv4+IPv6), tampilkan aktif |
| Upstream DNS | Set resolver upstream, tampilkan aktif |
| SafeSearch | Toggle Google/Bing/YouTube safe search per provider |
| DoT / DoH | Enable/disable + path cert/key, restart otomatis |
| Settings | Block mode, ganti password panel |

### Env Vars Systemd (untuk kustomisasi)

```ini
Environment=PANEL_ADDR=0.0.0.0:8443
Environment=DNSDIST_CONF=/etc/dnsdist/dnsdist.conf
Environment=DNSDIST_UPSTREAMS=/etc/dnsdist/upstreams.conf
Environment=PANEL_CERT=/var/lib/dnsdist/panel-cert.pem
Environment=PANEL_KEY=/var/lib/dnsdist/panel-key.pem
Environment=PANEL_SECRET_FILE=/var/lib/dnsdist/panel.secret
```

---

## 🔄 Setelah Instalasi

### Cek Status
```bash
sudo ./setup-edge.sh --check-config
systemctl status dnsdist
```

### Test DNS
```bash
# Plaintext
dig @127.0.0.1 google.com

# Test blokir (harus return sinkhole IP)
dig @127.0.0.1 evil-domain.com

# DoT
kdig @127.0.0.1 +tls google.com
```

### Web Console dnsdist
```
http://YOUR_SERVER_IP:8083
```
API: `curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/stats`

---

## ⏰ Auto-Sync Blacklist

Cronjob tiap 3 jam (dibuat otomatis saat install):
```cron
0 */3 * * * /usr/local/bin/update-blacklist.sh >> /var/log/dnsdist-sync.log 2>&1
```

Manual sync:
```bash
sudo /usr/local/bin/update-blacklist.sh           # normal
sudo /usr/local/bin/update-blacklist.sh --force   # bypass cache
```

---

## 🌐 CDB Cluster & Redundancy (update-blacklist v3.x)

Multi-source failover: central → mirror → peer.

```bash
sudo ./setup-edge.sh --set-cdb-sources \
  "http://central/trust.db,http://mirror/trust.db,http://peer:8443/cdb/blacklist.db"
```

Content-addressed storage: `blacklist.<sha256>.db` + symlink atomic swap.

---

## 🆙 Upgrade

```bash
cd /opt/dnsdist-edge/setup
sudo ./setup-edge.sh --upgrade
```

Otomatis: backup config → update scripts → migrasi path → restart.

---

## 🛠️ Troubleshooting

### Service tidak start
```bash
dnsdist --check-config
journalctl -u dnsdist -n 50 --no-pager
```

### Panel tidak bisa diakses
```bash
systemctl status dnsdist-panel
journalctl -u dnsdist-panel -n 30
# Cek port
ss -tlnp | grep 8443
```

### RPZ tidak bekerja
```bash
# Cek SINKHOLE_IPS di config
grep "SINKHOLE_IPS" /etc/dnsdist/dnsdist.conf
# Test blokir
dig @127.0.0.1 <domain-yang-ada-di-blacklist>
```

### Regenerate cert panel
```bash
rm /var/lib/dnsdist/panel-cert.pem /var/lib/dnsdist/panel-key.pem
systemctl restart dnsdist-panel  # auto-generate baru
```

---

## 📊 Monitoring

- **Prometheus**: scrape `http://YOUR_SERVER:8083/metrics`
- **Grafana**: import `monitoring/grafana/provisioning/dashboards/dnsdist.json`
- **Panel**: `https://YOUR_SERVER:8443` — dashboard QPS + resource real-time

---

## 📦 File Reference

| Path | Keterangan |
|---|---|
| `/etc/dnsdist/dnsdist.conf` | Config utama |
| `/etc/dnsdist/upstreams.conf` | Upstream (auto-generated) |
| `/etc/dnsdist/safesearch.conf` | SafeSearch rules (panel-generated, optional) |
| `/etc/dnsdist/dotdoh.conf` | DoT/DoH listeners (panel-generated, optional) |
| `/etc/dnsdist/node.conf` | Config tersimpan (URL, mode, dll) |
| `/etc/dnsdist/certs/server.{crt,key}` | TLS cert dnsdist |
| `/var/lib/dnsdist/blacklist.db` | Symlink ke CDB aktif |
| `/var/lib/dnsdist/panel-cert.pem` | TLS cert panel (auto-generated) |
| `/var/lib/dnsdist/panel.secret` | JWT secret panel |
| `/var/lib/dnsdist/panel.password` | Password login panel |
| `/usr/local/bin/dnsdist-panel` | Binary panel |
| `/usr/local/bin/setup-edge.sh` | Script manajemen utama |
| `/usr/local/bin/update-blacklist.sh` | Sync CDB |

---

**Version:** v2.6.0
**Last Updated:** September 2026
