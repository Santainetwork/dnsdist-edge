# 🚀 Setup Guide - DNSDist Edge Node (Trust-NG)

Panduan lengkap untuk部署 DNSDist Edge Node secara native (tanpa Docker).

## 📋 Prasyarat

### Sistem Operasi
- **Debian 11+** atau **Ubuntu 20.04+** (rekomendasi: Debian 12 Bookworm)
- Minimal requirements:
  - RAM: 2 GB (minimal), 8 GB (recommended)
  - CPU: 2 cores
  - Storage: 20 GB SSD

### Akses & Permissions
```bash
sudo su -  # Atau gunakan sudo untuk setiap command
```

## 🔧 Instalasi Cepat

### 1. Download Repositori
```bash
cd /tmp
git clone https://github.com/trust-ng-replica/dnsdist-edge.git
cd dnsdist-edge
```

### 2. Jalankan Installer
```bash
# Instal dengan konfigurasi default
sudo ./setup-edge.sh --install

# Atau dengan URL Central Manager custom
sudo ./setup-edge.sh --install --url http://central-manager.local:8080/files/trust.db

# Set password web console
sudo ./setup-edge.sh --install --password mypassword --apikey myapikey
```

## 📝 Konfigurasi Manual

### Jika Ingin Custom Configuration

#### Step 1: Edit Variabel di `setup-edge.sh`
```bash
# Default values (bisa diubah sebelum run)
CENTRAL_DB_URL="http://your-central-server.com/blacklist.db"
UPSTREAM_DNS="1.1.1.1, 8.8.8.8"
RPZ_IPS="10.10.10.10"
WEBSERVER_PASSWORD="changeme"
WEBSERVER_APIKEY="change-this-apikey"
```

#### Step 2: Pilih Mode Operasional
Saat run installer, Anda akan diminta memilih:
1. **Mode AdGuard (Privacy)** - Returns `0.0.0.0` untuk blocked domains
2. **Mode RPZ (ISP)** - Redirect ke IP Sinkhole (tampilkan halaman warning)

Rekomendasi:
- Production public resolver → **RPZ mode**
- Internal/privacy-focused → **AdGuard mode**

#### Step 3: Konfigurasi Upstream DNS
Masukkan IP upstream (comma-separated):
- Google: `8.8.8.8, 8.8.4.4`
- Cloudflare: `1.1.1.1, 1.0.0.1`
- Quad9: `9.9.9.9`
- Kombinasi: `1.1.1.1, 8.8.8.8, 9.9.9.9`

#### Step 4: SSL/TLS Certificate Option
Pilihan sertifikat:
1. Self-signed (default, ready-to-use)
2. Let's Encrypt (butuh domain + nginx)
3. Existing certificate (paste path ke cert/key)

## 🔄 Setelah Instalasi

### 1. Cek Status Service
```bash
systemctl status dnsdist
```

Expected output:
```
● dnsdist.service - DNS traffic distributor and blocker
   Loaded: loaded (/etc/systemd/system/dnsdist.service)
   Active: active (running)
```

### 2. Test DNS Resolution
```bash
# DNS plaintext (port 53)
dig @127.0.0.1 google.com

# DoT (DNS over TLS - port 853)
openssl s_client -connect 127.0.0.1:853 -starttls dns </dev/null 2>/dev/null | dig @127.0.0.1 google.com

# DoH (DNS over HTTPS - port 443)
curl -v --http2 https://127.0.0.1/dns-query -H "X-DNS-over-HTTPS: true"
```

### 3. Verifikasi Web Console
Access di browser: `http://YOUR_SERVER_IP:8083`
- Username: `trust-ng-admin`
- Password: (sesuai yang Anda set saat install)

API Key tersedia di header request:
```bash
curl -H "X-API-Key: YOUR_API_KEY" http://localhost:8083/api/v1/stats
```

### 4. Monitoring Logs
```bash
# Main service logs
tail -f /var/log/dnsdist.log

# Sync logs (blacklist database updates)
tail -f /var/log/dnsdist-sync.log

# Real-time query logging (if enabled in dnsdist.conf)
tail -f /var/log/dnsdist/queries.log
```

## ⏰ Auto-Sync Blacklist Database

Installer otomatis membuat cronjob yang berjalan setiap 3 jam:
```bash
0 */3 * * * /usr/local/bin/update-blacklist.sh >> /var/log/dnsdist-sync.log 2>&1
```

Manual sync anytime:
```bash
# Normal sync
sudo /usr/local/bin/update-blacklist.sh

# Force update (ignore cache)
sudo /usr/local/bin/update-blacklist.sh --force-update
```

Status page available at: `http://YOUR_SERVER_IP/status/`

## 🆙 Upgrade Script

Untuk update ke versi terbaru tanpa kehilangan konfigurasi:
```bash
cd /root/trust-ng-replica/dnsdist-edge
sudo ./setup-edge.sh --upgrade
```

Script akan otomatis:
1. Backup konfigurasi lama
2. Update semua file config dan scripts
3. Preserve user settings (URL, password, upstreams, dll)
4. Restart service dengan aman

## 🛠️ Troubleshooting

### Issue: Service tidak start
```bash
# Cek konfigurasi syntax
dnsdist --check-config

# Lihat error logs
journalctl -u dnsdist -n 50 --no-pager

# Verify files ownership
ls -la /etc/dnsdist/
```

### Issue: DNS resolution lambat
```bash
# Check upstream connectivity
dig @8.8.8.8 google.com
dig @1.1.1.1 google.com

# Increase cache size di dnsdist.conf
maxCacheEntries 500000
```

### Issue: Blacklist tidak sinkron
```bash
# Test manual download
curl -I http://CENTRAL_MANAGER_URL/blacklist.db

# Check firewall rules
iptables -L -n | grep :80
systemctl status nginx  # if using nginx as central manager

# Enable verbose logging di update-blacklist.sh
bash -x /usr/local/bin/update-blacklist.sh
```

### Issue: No certificates found
```bash
# Regenerate self-signed certificates
cd /etc/dnsdist/certs
rm -f server.*
openssl req -x509 -newkey rsa:4096 \
  -keyout server.key \
  -out server.crt \
  -days 365 \
  -nodes \
  -subj "/CN=$(hostname)/O=Trust-NG/C=ID"

chmod 600 server.key && chown dnsdist:dnsdist server.key
systemctl restart dnsdist
```

## 📊 Monitoring Integration

### Prometheus Metrics
Scrape endpoint: `http://YOUR_SERVER_IP:8083/metrics`

Add to your `prometheus.yml`:
```yaml
scrape_configs:
  - job_name: 'dnsdist-edge'
    static_configs:
      - targets: ['localhost:8083']
    metrics_path: /metrics
```

### Grafana Dashboard
Import dari: `monitoring/grafana/provisioning/dashboards/dnsdist.json`

Dashboard menampilkan:
- QPS (Queries Per Second)
- Cache hit rate
- Top queries domains
- Top blocked domains
- Response times
- ASN distribution

## 🎯 Performance Tuning

### Kernel Parameters (untuk high-traffic)
Tambahkan ke `/etc/sysctl.conf`:
```ini
net.core.rmem_max=16777216
net.core.rmem_default=16777216
net.core.wmem_max=16777216
net.core.wmem_default=16777216
net.core.netdev_max_backlog=2000
net.ipv4.tcp_tw_reuse=1
net.ipv4.tcp_fin_timeout=15
net.ipv4.ip_local_port_range=1024 65535
net.netfilter.nf_conntrack_max=1048576
```

Apply:
```bash
sysctl -p
```

### DNSDist Lua Tuning
Edit `/etc/dnsdist/dnsdist.conf`:
```lua
-- Increase max threads
setMaxThreads(16)

-- Optimize TCP buffers
setMaxTCPCacheSize(100000)
setTCPCloseTimeout(2)

-- Tune UDP timeouts
setUDPTimeout(5)
```

## 📦 File Reference

| Path | Purpose |
|------|---------|
| `/etc/dnsdist/dnsdist.conf` | Main configuration |
| `/etc/dnsdist/top-stats.lua` | Statistics module |
| `/etc/dnsdist/node.conf` | User saved config |
| `/etc/dnsdist/certs/server.crt` | SSL certificate |
| `/etc/dnsdist/certs/server.key` | SSL private key |
| `/var/lib/dnsdist/blacklist.db` | Blacklist database (CDB format) |
| `/var/log/dnsdist.log` | DNSDist operational logs |
| `/var/log/dnsdist-sync.log` | Sync job logs |

## 🔐 Security Best Practices

1. **Change default credentials**: Update web console password immediately
2. **Restrict API access**: Use firewall to limit who can access port 8083
3. **TLS enforcement**: Always use DoT/DoH, disable plaintext DNS on public interfaces
4. **Regular updates**: Keep system packages updated
5. **Monitor disk space**: Blacklist.db can grow to hundreds of MB

## 📞 Support & Resources

- GitHub Repository: https://github.com/trust-ng-replica/dnsdist-edge
- Documentation: `docs/EDGE-README.md`, `docs/TOPSTATS-README.md`
- Community: Trust-NG Discord/Forum (link TBD)

---

**Version:** v2.0.0  
**Last Updated:** August 2026
