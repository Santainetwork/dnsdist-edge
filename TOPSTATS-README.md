# Trust-NG DNSDist Top Stats Module

## Overview
Module untuk tracking query statistics, top blocked domains, dan top ASN di dnsdist dengan API web native.

## Fitur
- **Top Queries**: Domain paling sering di-query
- **Top Blocked**: Domain yang paling banyak diblokir (perlu SpoofAction/Blocking rule)
- **Top ASN**: Source IP berdasarkan ASN
- **Top Blocked ASN**: ASN mana yang paling sering kena blokir
- **Real-time Metrics**: Update setiap query dengan cleanup otomatis

## Instalasi

### File Yang Dibutuhkan
1. `/etc/dnsdist/top-stats.lua` - Module utama (sudah ada)
2. `/etc/dnsdist/dnsdist.conf` - Konfigurasi dnsdist (sudah include top-stats.lua)
3. `/etc/dnsdist/asn-db.bin` - Database ASN compact, **format biner** (Recommended, di-generate via `addons/build-asn-db.sh`)

### Database ASN

Modul ini mendukung dua format database ASN:
- **`.bin`** (Recommended) — file biner compact hasil kompilasi `addons/build-asn-db.sh` dari `ipinfo_lite.csv` (https://ipinfo.io/products/free-ip-database). Lookup O(log N) via binary search, penggunaan memori minimal (hanya ukuran file), mampu menangani 1+ juta prefix tanpa menurunkan QPS dnsdist.
- **`.csv`** — format text (subnet,asn_number,asn_name). Cocok untuk skala kecil (di bawah 10k prefix) atau unit test, tapi linear scan O(N) per query.

#### Cara Generate `.bin` dari `ipinfo_lite.csv`

Unduh dataset `ipinfo_lite.csv` dari https://ipinfo.io/products/free-ip-database, lalu kompilasi:

```bash
# Baca ipinfo_lite.csv dari direktori aktif, tulis langsung ke /etc/dnsdist/asn-db.bin
sudo bash addons/build-asn-db.sh

# Script juga me-restart dnsdist jika sedang aktif
```

Untuk dataset global penuh, file `asn-db.bin` biasanya berukuran 15-25 MB dan berisi ~1 juta entry.

#### Format CSV Manual (`asn-db.csv`, opsional, skala kecil)

```csv
subnet,asn_number,asn_name
127.0.0.0/8,127,Localhost Loopback
1.0.0.0/24,13335,Cloudflare Inc
8.8.8.0/24,15169,Google LLC
192.168.0.0/16,64512,Private LAN
```

### Cara Load Module ke dnsdist.conf

Tambahkan di file `dnsdist.conf` SETELAH setup KVS tetapi SEBELUM filtering rules:

```lua
-- [5] BLACKLIST CDB (Trust Positif — trust-builder)
kvs     = newCDBKVStore('/var/lib/dnsdist/blacklist.db', 5)
kvsRule = KeyValueStoreLookupRule(kvs, KeyValueLookupKeyQName(true))

-- ============================================================
-- [5.5] TOP STATS MODULE
-- ============================================================
dofile('/etc/dnsdist/top-stats.lua')
```

### Contoh Full Configuration
```lua
-- [5] Setup KVS dulu
kvs = newCDBKVStore('/var/lib/dnsdist/blacklist.db', 5)
kvsRule = KeyValueStoreLookupRule(kvs, KeyValueLookupKeyQName(true))

-- Load top-stats (akan auto-add tracking rules)
dofile('/etc/dnsdist/top-stats.lua')

-- [6] Filtering Rules (BLOKIR harus setelah top-stats)
if BLOCK_MODE == 'rpz' then
    addAction(kvsRule, SpoofAction(SINKHOLE_IPS))
end

-- [7] Default rules
addAction(AllRule(), PoolAction('default'))
```

## API Endpoints

### Base URL: `http://dnsdist-host:8083/api/v1/`

#### 1. Top Queries
```bash
curl -H "X-API-Key: YOUR_API_KEY" http://dnsdist:8083/api/v1/top-queries?n=20
```

Response example:
```json
{
  "status": "ok",
  "endpoint": "top-queries",
  "total_queries": 1523,
  "top": [
    {"rank": 1, "name": "google.com.", "count": 100},
    {"rank": 2, "name": "facebook.com.", "count": 85}
  ]
}
```

#### 2. Top Blocked
```bash
curl -H "X-API-Key: YOUR_API_KEY" http://dnsdist:8083/api/v1/top-blocked?n=20
```

```json
{
  "status": "ok",
  "endpoint": "top-blocked", 
  "total_blocked": 456,
  "top": [
    {"rank": 1, "name": "malware.evil.com.", "count": 200}
  ]
}
```

#### 3. Top ASN
```bash
curl -H "X-API-Key: YOUR_API_KEY" http://dnsdist:8083/api/v1/top-asn?n=20
```

```json
{
  "status": "ok",
  "endpoint": "top-asn",
  "asn_db_loaded": true,
  "total_queries": 1523,
  "top": [
    {"rank": 1, "name": "AS13335 Cloudflare Inc", "count": 500},
    {"rank": 2, "name": "AS15169 Google LLC", "count": 300}
  ]
}
```

#### 4. Top Blocked ASN
```bash
curl -H "X-API-Key: YOUR_API_KEY" http://dnsdist:8083/api/v1/top-blocked-asn?n=20
```

#### 5. All Stats Summary
```bash
curl -H "X-API-Key: YOUR_API_KEY" http://dnsdist:8083/api/v1/top-stats?n=20
```

```json
{
  "status": "ok",
  "uptime_seconds": 3600,
  "total_queries": 1523,
  "total_blocked": 456,
  "qps_avg": 0.42,
  "block_rate_pct": 29.94,
  "asn_db_loaded": true,
  "top_queries": [...],
  "top_blocked": [...],
  "top_asn": [...],
  "top_blocked_asn": [...]
}
```

## Parameter API

Semua endpoints mendukung parameter query string:
- `n=XX` - Jumlah hasil top-N (default: 50, max: 1000)

Contoh:
- `/api/v1/top-queries?n=10` - Tampilkan 10 terbesar
- `/api/v1/top-stats?n=50` - Tampilkan semua stat top-50

## Monitoring Integrasi

### Prometheus Export (Example)
Jika ingin export ke Prometheus, buat script scraper sederhana:

```python
#!/usr/bin/env python3
import requests
from prometheus_client import generate_latest, Counter

API_KEY = "trust-ng-apikey-changeme"
BASE_URL = "http://localhost:8083/api/v1"

def scrape_metrics():
    stats = requests.get(
        f"{BASE_URL}/top-stats",
        headers={"X-API-Key": API_KEY}
    ).json()
    
    if stats.get("status") == "ok":
        queries.set(stats["total_queries"])
        blocked.set(stats["total_blocked"])
        
for _ in range(1, 100):
    scrape_metrics()
```

### Grafana Dashboard Template
Dashboard JSON bisa dibuat dengan metrics dari endpoint `/api/v1/top-stats`:
- Query Rate over Time
- Block Rate Percentage
- Top 10 Domains Bar Chart
- Top 10 ASN Donut Chart

## Performance Considerations

- **Memory Usage**: ~2-5 MB per 10,000 unique entries
- **Cleanup Interval**: Setiap 50,000 query (configurable)
- **ASN Reload**: Setiap 500,000 query (configurable)
- **Max Entries**: Default 20,000 unique items per category

## Troubleshooting

### Blocked Tracking Not Working
**Symptom**: `total_blocked` selalu 0 padahal ada domain diblokir

**Causes**:
1. Rule order salah (top-stats harus load BEFORE blocking rules)
2. kvsRule tidak match wire-format keys
3. SpoofAction error silently

**Solution**:
```lua
# Pastikan urutannya seperti ini di dnsdist.conf:
kvs = newCDBKVStore(...)       # Line 102
kvsRule = ...                   # Line 103
dofile('/etc/dnsdist/top-stats.lua')  # Line 115 - LOAD FIRST!
# Blocking rules after top-stats:
addAction(kvsRule, SpoofAction(...))  # Line 143 - AFTER loading module
```

### API Returns 401 Unauthorized
**Cause**: X-API-Key header tidak cocok atau API key kosong

**Fix**: Pastikan header sesuai dengan `apiKey` di `setWebserverConfig()`
```bash
curl -H "X-API-Key: trust-ng-apikey-changeme" \
     http://localhost:8083/api/v1/top-stats
```

### ASN Database Tidak Loaded
**Check logs**: `[top-stats] ASN database: not loaded (file: disabled)`

**Solutions**:
1. Buat file `/etc/dnsdist/asn-db.csv` valid
2. Cek syntax CSV (harus: subnet,asn_number,asn_name)
3. Restart dnsdist setelah update file

## Maintenance

### Generate Blacklist CDB dengan Wire Format
```bash
python3 /usr/local/bin/gen-cdb.py /var/lib/dnsdist/blacklist.db evil.com blocked.domain.
```

### Hot Reload ASN DB
Modul akan auto-reload setelah 500,000 query (configurable via `CONFIG.asnReloadInterval`)

Untuk manual reload, restart dnsdist service:
```bash
systemctl restart dnsdist
```

## Compatibility

- **dnsdist Version**: >= 1.6 (tested on 1.7.3)
- **Lua Engine**: LuaJIT 2.1+
- **OS**: Debian bookworm (minimal requirement)
- **Port**: TCP 8083 (web API), 53/udp & 53/tcp (DNS)

## SmartDNS Compatibility Plugin (`addons/smartdns-plugin.lua`)

Plugin opsional yang di-vendor dari repo SmartDist (fitur ala SmartDNS), dimuat otomatis dari `setup/dnsdist.conf` [5.6] — aman bila file tidak ada. Tanpa `lua-socket`, speed check nonaktif otomatis; fitur berikut tetap jalan. **Butuh dnsdist >= 1.9** (`newDNSPacketOverlay` tidak ada di 1.7).

- `smartdns_ip_set(name, filepath)` — netmask group dari file (satu CIDR per baris, `#` = komentar).
- `smartdns_domain_set(name, filepath)` — suffix match node dari file domain (berlaku juga untuk subdomain).
- `smartdns_cname(domain_pattern, target_cname)` — alias CNAME (menerima prefix `-.` / `.`).
- `smartdns_ip_rules_alias(ip_set_name, target_ips, exclude_domain_set)` — setiap record yang cocok ip-set diisi dari daftar target secara round-robin; record lain utuh; domain di exclude set dilewati. Dukungan IPv4 & IPv6. Alias beda famili di-skip.
- `smartdns_enable_speedcheck()` — hook global. `SPEEDCHECK_MODE` mengikuti `response-mode` SmartDNS: `"first-ping"` (pemenang probe pertama), `"fastest-ip"` (reorder tercepat ke #1, default), `"fastest-response"` (overwrite top-N). Skip per-domain: `SPEEDCHECK_SKIP = {"local.example.com", ".internal"}` (prefix `.` = domain + subdomain); port override: `SPEEDCHECK_PORTS_OVERRIDE = {["api.example.com"] = {443}}`. Default probe TCP port 80/443, timeout 3 detik. Butuh `apt install lua-socket`; tanpa itu speed check nonaktif otomatis.

### Contoh Pemakaian (di `dnsdist.conf`)

```lua
smartdns_ip_set("cloudflare-ipv4", "/etc/smartdns/cdn-ips/cloudflare/ipv4.txt")
smartdns_domain_set("cf-exclude", "/etc/smartdns/cdn-ips/cloudflare/exclude.txt")
smartdns_cname("-.api.example.com", "api.example.com.cdn.cloudflare.net.")
smartdns_ip_rules_alias("cloudflare-ipv4", {"172.64.52.159", "172.64.87.224"}, "cf-exclude")
-- SPEEDCHECK_ENABLED = true
-- smartdns_enable_speedcheck()
```

Catatan: data ip-set/dataset CDN bukan bagian repo ini — unduh dari sumber publik (mis. Cloudflare IP ranges) sesuai kebutuhan.

## License

MIT License - Sama seperti proyek Trust-NG

---

Dibuat untuk Trust-NG Edge Node monitoring purposes
