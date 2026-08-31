# ⚡ Quick Reference - DNSDist Edge Node

## 🚀 Common Commands

### Service Management
```bash
systemctl start dnsdist           # Start service
systemctl stop dnsdist            # Stop service
systemctl restart dnsdist         # Restart service
systemctl reload dnsdist          # Reload config (no disconnect)
systemctl status dnsdist          # Check status
journalctl -u dnsdist -f          # Live logs
```

### Configuration Updates
```bash
sudo nano /etc/dnsdist/dnsdist.conf    # Edit main config
sudo nano /etc/dnsdist/node.conf       # Edit saved config
dnsdist --check-config                 # Validate syntax
systemctl restart dnsdist              # Apply changes
```

### Database Management
```bash
sudo /usr/local/bin/update-blacklist.sh          # Normal sync
sudo /usr/local/bin/update-blacklist.sh --force  # Force update
ls -lh /var/lib/dnsdist/blacklist.db             # Check size
```

### Monitoring Access
```bash
# Web Console (Port 8083)
http://YOUR_SERVER:8083

# API Endpoints
curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/stats
curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/top-queries?n=20
curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/top-blocked?n=20
curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/top-asn?n=20
```

### DNS Testing
```bash
# Test basic resolution
dig @127.0.0.1 google.com

# Test DoT (TLS)
openssl s_client -connect 127.0.0.1:853 </dev/null | dig @127.0.0.1 google.com

# Test blocked domain (should return sinkhole IP)
dig @127.0.0.1 evil-domain.com
```

## 📁 Important Files Location

| Purpose | Path |
|---------|------|
| Main Config | `/etc/dnsdist/dnsdist.conf` |
| Saved Config | `/etc/dnsdist/node.conf` |
| Top Stats Module | `/etc/dnsdist/top-stats.lua` |
| SSL Certs | `/etc/dnsdist/certs/server.{crt,key}` |
| Blacklist DB | `/var/lib/dnsdist/blacklist.db` |
| Sync Script | `/usr/local/bin/update-blacklist.sh` |
| ASN Builder | `/usr/local/bin/build-asn-db.sh` |
| Query Logs | `/var/log/dnsdist/queries.log` |

## 🔧 Maintenance Tasks

### Daily Checks
```bash
# Check disk space
df -h /var/lib/dnsdist

# Check service health
systemctl is-active dnsdist

# Check last sync time
grep "Terakhir Update" /var/www/html/status/index.html
```

### Weekly Tasks
```bash
# Rotate logs if needed
logrotate -f /etc/logrotate.d/dnsdist

# Review top blocked domains
curl -s "http://localhost:8083/api/v1/top-blocked?n=50" \
  -H "X-API-Key: YOUR_KEY" | jq '.top[]'
```

### Monthly Tasks
```bash
# Update system packages
apt-get update && apt-get upgrade -y

# Upgrade dnsdist-edge scripts
cd /opt/dnsdist-edge/setup
sudo ./setup-edge.sh --upgrade

# Rebuild ASN database (optional)
sudo /usr/local/bin/build-asn-db.sh
```

## 🎯 Setup Options Explained

### install flag usage
```bash
# Masuk ke folder setup
cd dnsdist-edge/setup

# Minimal install (defaults)
sudo ./setup-edge.sh --install

# With custom Central Manager URL
sudo ./setup-edge.sh --install --url http://central.local:8080/db

# Set custom password and API key
sudo ./setup-edge.sh --install --password MyPass123 --apikey MyApiKey

# Install + immediate sync
sudo ./setup-edge.sh --install && sudo /usr/local/bin/update-blacklist.sh
```

### Sync modes
```bash
# Normal sync (checks cache, skips if recent)
sudo /usr/local/bin/update-blacklist.sh

# Force download (bypasses all caching)
sudo /usr/local/bin/update-blacklist.sh --force-update

# Run with verbose debugging
bash -x /usr/local/bin/update-blacklist.sh
```

## ❗ Troubleshooting Cheat Sheet

### Problem: Service won't start
```bash
# 1. Check config syntax
dnsdist --check-config

# 2. Check file permissions
ls -la /etc/dnsdist/
chmod 644 /etc/dnsdist/*.lua
chown -R dnsdist:dnsdist /etc/dnsdist/

# 3. Verify systemd unit exists
systemctl cat dnsdist

# 4. View detailed errors
journalctl -u dnsdist --no-pager -n 100
```

### Problem: No blacklist data
```bash
# 1. Test manual download
curl -I http://CENTRAL_URL/blacklist.db

# 2. Check firewall
firewall-cmd --list-ports  # Firewalld
ufw status                  # UFW
iptables -L -n              # Raw iptables

# 3. Verify network connectivity
ping CENTRAL_SERVER_IP
telnet CENTRAL_HOST 80
```

### Problem: DNS queries failing
```bash
# 1. Test each upstream individually
dig @8.8.8.8 example.com
dig @1.1.1.1 example.com

# 2. Check cache performance
dig @localhost +stats

# 3. Look for rate limiting
grep "MaxQPSIPRule" /var/log/dnsdist.log
```

## 🛡️ Security Checklist

- [ ] Change default web console password
- [ ] Change default API key
- [ ] Restrict port 8083 access via firewall
- [ ] Enable TLS (DoT/DoH) on public interfaces
- [ ] Use strong SSL/TLS certificates
- [ ] Regular security updates
- [ ] Monitor log files for suspicious activity

---

**For complete documentation:** See `SETUP.md`, `EDGE-README.md`, `TOPSTATS-README.md`
