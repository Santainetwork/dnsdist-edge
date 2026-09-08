# ⚡ Quick Reference — DNSDist Edge Node

## 🚀 Common Commands

### Service Management
```bash
systemctl start dnsdist           # Start
systemctl stop dnsdist            # Stop
systemctl restart dnsdist         # Restart
systemctl status dnsdist          # Status
journalctl -u dnsdist -f          # Live logs

systemctl status dnsdist-panel    # Panel status
journalctl -u dnsdist-panel -f    # Panel logs
```

### Setup Script
```bash
sudo ./setup-edge.sh --install              # Install baru
sudo ./setup-edge.sh --check-config         # Cek status & syntax
sudo ./setup-edge.sh --upgrade              # Upgrade ke versi terbaru
sudo ./setup-edge.sh --update-config        # Wizard update config
```

### RPZ Sinkhole (v2.4.0 — multi-IP + IPv6)
```bash
# IPv4 tunggal
sudo ./setup-edge.sh --set-rpz "10.10.10.10"

# Multi IPv4
sudo ./setup-edge.sh --set-rpz "10.10.10.10, 10.10.10.11"

# IPv4 + IPv6 (auto-split: A→IPv4, AAAA→IPv6)
sudo ./setup-edge.sh --set-rpz "10.10.10.10, 2001:db8::1"

# IPv6 saja
sudo ./setup-edge.sh --set-rpz "2001:db8::1, 2001:db8::2"
```

### Upstream DNS
```bash
# IPv4
sudo ./setup-edge.sh --set-upstream "1.1.1.1, 8.8.8.8"

# IPv4 + IPv6
sudo ./setup-edge.sh --set-upstream "1.1.1.1, [2606:4700:4700::1111]:53, 8.8.8.8"
```

### Database Sync
```bash
sudo /usr/local/bin/update-blacklist.sh           # Normal sync
sudo /usr/local/bin/update-blacklist.sh --force   # Force download
ls -lh /var/lib/dnsdist/blacklist.db              # Cek ukuran
cat /var/lib/dnsdist/blacklist.db.manifest.json   # Hash & sumber
```

### Cluster / Multi-source
```bash
sudo ./setup-edge.sh --set-cdb-sources \
  "http://central/trust.db,http://mirror/trust.db,http://peer:8443/cdb/blacklist.db"
```

---

## 🖥️ Panel Web (HTTPS :8443)

```bash
# Install
sudo ./setup-edge.sh --with-panel

# Akses
https://YOUR_SERVER_IP:8443
# Password default: trust-ng-admin (ganti di Settings)

# Manual start
sudo /usr/local/bin/dnsdist-panel -addr :8443

# Cek
ss -tlnp | grep 8443
systemctl status dnsdist-panel
```

Panel API (butuh token dari `POST /api/login`):
```bash
TOKEN=$(curl -sk -X POST -H "Content-Type: application/json" \
  -d '{"password":"admin"}' https://localhost:8443/api/login | \
  python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

curl -sk -H "Authorization: Bearer $TOKEN" https://localhost:8443/api/stats
curl -sk -H "Authorization: Bearer $TOKEN" https://localhost:8443/api/config
```

---

## 📊 Monitoring

```bash
# dnsdist web console
http://YOUR_SERVER:8083

# API stats
curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/stats
curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/top-queries?n=20
curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/top-blocked?n=20

# Panel stats (real-time, no dnsdist dependency)
curl -sk -H "Authorization: Bearer $TOKEN" https://localhost:8443/api/stats
```

---

## 🧪 DNS Testing

```bash
# Plaintext
dig @127.0.0.1 google.com

# Test blokir (return sinkhole IP)
dig @127.0.0.1 evil-domain.com

# DoT
kdig @127.0.0.1 +tls google.com

# DoH
curl -sk "https://127.0.0.1/dns-query?name=google.com&type=A" \
  -H "Accept: application/dns-json"
```

---

## 📁 File Penting

| Path | Keterangan |
|---|---|
| `/etc/dnsdist/dnsdist.conf` | Config utama |
| `/etc/dnsdist/upstreams.conf` | Upstream (auto-generated) |
| `/etc/dnsdist/safesearch.conf` | SafeSearch (panel, optional) |
| `/etc/dnsdist/dotdoh.conf` | DoT/DoH (panel, optional) |
| `/etc/dnsdist/node.conf` | Config tersimpan |
| `/var/lib/dnsdist/blacklist.db` | Symlink CDB aktif |
| `/var/lib/dnsdist/panel-cert.pem` | TLS cert panel |
| `/var/lib/dnsdist/panel.secret` | JWT secret panel |
| `/var/lib/dnsdist/panel.password` | Password panel |
| `/usr/local/bin/dnsdist-panel` | Binary panel |
| `/usr/local/bin/setup-edge.sh` | Script utama |
| `/usr/local/bin/update-blacklist.sh` | Sync CDB |

---

## ❗ Troubleshooting Cepat

```bash
# Syntax config
dnsdist --check-config

# Service error
journalctl -u dnsdist -n 50 --no-pager
journalctl -u dnsdist-panel -n 30

# Panel tidak jalan
ss -tlnp | grep 8443
rm /var/lib/dnsdist/panel-cert.pem /var/lib/dnsdist/panel-key.pem
systemctl restart dnsdist-panel   # regenerate cert

# Blacklist tidak sync
bash -x /usr/local/bin/update-blacklist.sh

# Permission reset
chown -R dnsdist:dnsdist /etc/dnsdist /var/lib/dnsdist
chmod 600 /etc/dnsdist/certs/server.key
```

## 🛡️ Security Checklist

- [ ] Ganti default password web console (port 8083)
- [ ] Ganti default API key dnsdist
- [ ] Ganti password panel (halaman Settings, port 8443)
- [ ] Restrict port 8083 & 8443 via firewall (hanya akses admin)
- [ ] Enable DoT/DoH untuk query publik
- [ ] Pasang cert valid (bukan self-signed) jika exposed publik

---

**Docs lengkap:** `docs/SETUP.md` · `docs/SETUP-EDGE-COMMANDS.md` · `EDGE-README.md`
