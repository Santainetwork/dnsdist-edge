# 🚀 DNSDist Edge Node (Trust-NG)

**High-Performance DNS Filtering & Blocking Resolver** untuk deployment production-ready tanpa Docker.

[![Version](https://img.shields.io/badge/version-2.1.0-blue.svg)](CHANGELOG.md)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Debian%20%7C%20Ubuntu-lightgrey.svg)]()

## 📖 Overview

Project ini menyediakan solusi **DNS filtering edge node** dengan fitur-fitur advanced:

- ✅ **DNS over TLS (DoT)** - Port 853
- ✅ **DNS over HTTPS (DoH)** - Port 443  
- ✅ **Real-time blacklist filtering** dengan CDB database (hingga puluhan juta domains)
- ✅ **Auto-sync database** setiap 3 jam via cronjob
- ✅ **Web API monitoring** untuk top queries, top blocked, ASN tracking
- ✅ **Prometheus & Grafana integration** untuk observability
- ✅ **Rate limiting** anti-DDoS protection
- ✅ **Zero-downtime hot-reload** saat update blacklist
- ✅ **Multi-mode filtering**: AdGuard (privacy) atau RPZ (ISP-style)

## 🎯 Quick Start

### Instalasi Otomatis
```bash
# Clone repository
git clone https://github.com/Santainetwork/dnsdist-edge.git
cd dnsdist-edge

# Jalankan installer dari folder setup (sebagai root)
cd setup
sudo ./setup-edge.sh --install --url http://central-manager.local:8080/files/trust.db
```

Lihat panduan lengkap di **[SETUP.md](docs/SETUP.md)**.

### Quick Reference
Untuk command-finding cepat dan troubleshooting, lihat:
- **[QUICK_REFERENCE.md](docs/QUICK_REFERENCE.md)** - Common commands & cheat sheet

## 📚 Dokumentasi Lengkap

| Dokumentasi | Deskripsi |
|-------------|-----------|
| [**SETUP.md**](docs/SETUP.md) | Panduan instalasi lengkap dari awal sampai production |
| [**SETUP-EDGE-COMMANDS.md**](docs/SETUP-EDGE-COMMANDS.md) | Panduan lengkap CLI `setup-edge.sh` & 17 daftar command |
| [**QUICK_REFERENCE.md**](docs/QUICK_REFERENCE.md) | Command cheatsheet & troubleshooting tips |
| [**EDGE-README.md**](EDGE-README.md) | Architecture overview & konsep pemisahan beban Central vs Edge |
| [**TOPSTATS-README.md**](TOPSTATS-README.md) | Dokumentasi Top Stats module untuk monitoring |

## 🏗️ Arsitektur Sistem

```
┌─────────────────────────────────────────────────────────────┐
│                    CENTRAL MANAGER                           │
│  ┌──────────────────────┐    ┌──────────────────────────┐  │
│  │ trust-builder        │    │ HTTP Nginx Server        │  │
│  │ (Build millions of   │───▶│ (Hosts blacklist.db)     │  │
│  │  domain rules)       │    │                          │  │
│  └──────────────────────┘    └──────────────────────────┘  │
└──────────────────────┬──────────────────────────────────────┘
                       │ HTTP sync every 3 hours
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                    EDGE NODE (YOUR SERVER)                  │
│                                                               │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              DNSDist Resolver                        │   │
│  │  ┌────────────┐  ┌──────────────┐  ┌────────────┐  │   │
│  │  │ DoT (853)  │  │ DoH (443)    │  │ Plain DNS  │  │   │
│  │  │ (TLS)      │  │ (HTTPS)      │  │ (53)       │  │   │
│  │  └─────┬──────┘  └──────┬───────┘  └─────┬──────┘  │   │
│  └────────┼─────────────────┼────────────────┼────────┘   │
│           │                 │                │             │
│           └─────────────────┴────────────────┘             │
│                           │                                │
│          ┌────────────────┼────────────────┐               │
│          │                │                │               │
│          ▼                ▼                ▼               │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐       │
│  │ Blacklist DB │ │  Top Stats   │ │ Upstream     │       │
│  │ (CDB format) │ │  Module      │ │ Resolvers    │       │
│  │ Synced auto  │ │ (API/v1/*)   │ │ (Cloudflare, │       │
│  │              │ │              │ │  Google, etc)│       │
│  └──────────────┘ └──────────────┘ └──────────────┘       │
│                                                           │
│  ┌─────────────────────────────────────────────────────┐ │
│  │              Monitoring Stack                        │ │
│  │  Prometheus ←→ scrape metrics → Grafana dashboards  │ │
│  └─────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## ✨ Fitur Utama

### 🔒 Security Features
- **Full DNS encryption** via DoT & DoH
- **Rate limiting** per client IP (anti-DDoS)
- **Refused ANY queries** (anti-amplification)
- **Private IP blocking** untuk RFC1918 ranges
- **Secure credentials** untuk web console & API

### 📊 Advanced Monitoring
- **Top queries** - Domain paling banyak diminta
- **Top blocked** - Domain yang diblokir
- **Top clients** - Client dengan traffic tertinggi
- **ASN intelligence** - Distribusi berdasarkan ASN/IP info
- **Real-time QPS & latency** metrics

### ⚡ Performance Optimizations
- **CDB KV Store** untuk lookup super cepat (<1ms)
- **Smart caching** dengan ECS support untuk CDN
- **Lua scripting** untuk custom logic
- **Connection pooling** ke upstreams
- **Kernel tuning** ready untuk high-throughput

### 🔄 Auto Maintenance
- **Scheduled sync** blacklist database setiap 3 jam
- **Hot reload** tanpa restart service
- **Config backup** otomatis sebelum upgrade
- **Version migration** seamless

## 🛠️ Komponen Script

| File | Fungsi | Criticality |
|------|--------|-------------|
| `setup/setup-edge.sh` | Master installer untuk deployment penuh | 🔴 CRITICAL |
| `setup/update-blacklist.sh` | Sync otomatis blacklist dari central manager | 🔴 CRITICAL |
| `scripts/build-asn-db.sh` | Compile ASN database dari CSV | 🟡 MODERATE |

## 📦 Struktur Folder

```
dnsdist-edge/
├── setup/                       # Master installer & sync script
│   ├── setup-edge.sh           # Main installer script
│   ├── update-blacklist.sh     # Database sync script
│   └── dnsdist.conf            # Baseline DNSDist config
├── config/                      # Additional configuration files
│   └── deploy-edge.yml         # Ansible deployment playbook
├── scripts/                     # Executable scripts & modules
│   ├── build-asn-db.sh         # ASN compiler
│   ├── top-stats.lua           # Statistics tracking module
│   └── asn-toolkit/            # ASN toolkit bundle
├── certs/                       # SSL/TLS certificates
├── templates/                   # Jinja2/Ansible templates
│   └── node.conf.j2
├── monitoring/                  # Grafana & Prometheus configs
│   ├── grafana/provisioning/
│   └── prometheus/
├── tools/                       # Utility scripts
│   ├── gen-cdb.py           # CDB generator (wire-format)
│   └── dnsdist-health.sh    # Health check node
├── docs/                        # Documentation
│   ├── SETUP.md                # Full setup guide
│   ├── SETUP-EDGE-COMMANDS.md  # CLI command guide for setup-edge.sh
│   └── QUICK_REFERENCE.md      # Command cheatsheet
├── EDGE-README.md               # Edge architecture docs
├── TOPSTATS-README.md           # Top stats module docs
├── .gitignore                   # Git exclusion rules
├── README.md                    # Main documentation
```
```

## 🧪 Testing & Validation

Setelah instalasi, test dengan commands berikut:

```bash
# 1. Cek service status
systemctl status dnsdist

# 2. Test basic DNS resolution
dig @127.0.0.1 google.com

# 3. Verify blocked domain returns sinkhole
dig @127.0.0.1 evil-domain.com

# 4. Access web console
curl -u trust-ng-admin:YOUR_PASSWORD http://localhost:8083

# 5. Check API endpoints
curl -H "X-API-Key: YOUR_KEY" http://localhost:8083/api/v1/stats
```

## 🔐 Default Credentials (Change Immediately!)

| Service | Username | Password/Key |
|---------|----------|--------------|
| Web Console | `trust-ng-admin` | `trust-ng-admin` |
| API Key | - | `trust-ng-apikey-changeme` |

⚠️ **PENTING:** Selalu ubah default credentials setelah instalasi!

## 📊 Dashboard & Monitoring

### Access Points
- **Grafana**: Import dashboard dari `monitoring/grafana/provisioning/dashboards/dnsdist.json`
- **Prometheus**: Scrape endpoint `http://your-server:8083/metrics`
- **Web API**: `http://your-server:8083/api/v1/top-*`

### Metrics Available
- Total queries per second
- Cache hit ratio
- Blocked requests count
- Response latencies
- Per-client statistics
- ASN distribution

## 🆙 Upgrade Path

Untuk upgrade ke versi terbaru tanpa kehilangan data:

```bash
cd /opt/dnsdist-edge/setup
sudo ./setup-edge.sh --upgrade
```

Script akan secara otomatis:
1. Backup konfigurasi user
2. Update semua scripts & configs
3. Preserve saved settings
4. Restart service dengan safe rolling

## 🛠️ Troubleshooting

### Problem umum & solusi:

1. **Service tidak mau start**
   ```bash
   dnsdist --check-config
   journalctl -u dnsdist -n 50
   ```

2. **Blacklist tidak ter-update**
   ```bash
   curl -I http://CENTRAL_URL/blacklist.db
   sudo /usr/local/bin/update-blacklist.sh --force
   ```

3. **DNS lambat/fail**
   ```bash
   dig @8.8.8.8 example.com  # Test upstream individual
   dig @localhost +stats     # Check cache performance
   ```

Lihat **[QUICK_REFERENCE.md](docs/QUICK_REFERENCE.md)** untuk troubleshooting lengkap.

## 📞 Support & Resources

- **GitHub Repository**: [Santainetwork/dnsdist-edge](https://github.com/Santainetwork/dnsdist-edge)
- **Issue Tracker**: Submit bugs/suggestions via GitHub Issues
- **Documentation**: Semua docs ada di folder `docs/`

## 📜 License

MIT License - see [LICENSE](LICENSE) for details.

---

**Built with ❤️ for Trust-NG Project**

*Last Updated: August 2026 | Version: v2.1.0*
