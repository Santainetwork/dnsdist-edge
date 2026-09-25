# 📡 RPZ-Master — High-Performance Streaming RPZ Master & CDB Distributor

Daemon Go mandiri untuk bertindak sebagai **RPZ Secondary Slave** dari Komdigi (Trust Positif) sekaligus menjadi **Open Master Server (Streaming AXFR/IXFR)** dan **HTTP CDB Distributor** untuk edge node `dnsdist`.

---

## 🌟 Fitur Utama

1. **Bootstrap Instan (Zero-Jongkok):**
   * Mengunduh file mentah `domains_isp` (~200 MB) via HTTP stream tanpa membebani server master DNS Komdigi dengan koneksi TCP AXFR 1,4 GB.
2. **Kompilasi CDB Berkecepatan Tinggi (Wire-Format):**
   * Mengonversi jutaan domain langsung ke format Constant Database (CDB) dengan wire-format binary key (`miekg/dns`) yang kompatibel langsung dengan `dnsdist` (`KeyValueLookupKeyQName(true)`).
3. **Streaming Open Master (AXFR & IXFR):**
   * Melayani kueri transfer zona penuh (`AXFR`) secara streaming langsung dari disk/file tanpa memuat jutaan domain ke RAM. Memory footprint tetap rendah (<100 MB) meski dilayani bersamaan.
   * Mendukung `IXFR` (Incremental Zone Transfer - RFC 1995) dengan riwayat delta untuk memperbarui slave rekanan hanya dalam hitungan detik.
   * Built-in semaphore / concurrency limiter untuk mencegah kelebihan beban koneksi transfer.
4. **Dual Distribution (DNS & HTTP):**
   * Melayani transfer via protokol DNS TCP port 53 / 5353.
   * Melayani file biner `.cdb` via HTTP (dengan dukungan ETag dan sidecar `manifest.json`) untuk edge node `dnsdist-edge`.

---

## 🚀 Penggunaan

### 1. Build Binary
```bash
cd tools/rpz-master
./build.sh
```

### 2. Mode Bootstrap Awal
Mengunduh daftar domain dari URL Trust Positif Komdigi dan langsung mengompilasinya ke file CDB:
```bash
./rpz-master -action bootstrap -input /var/lib/dnsdist/domains_isp.txt
```

### 3. Mode Server (DNS + HTTP)
Menjalankan daemon open master:
```bash
./rpz-master -action serve -dns-listen 127.0.0.1:5353 -http-listen 127.0.0.1:8088
```

### 4. Mode Sinkronisasi Upstream (IXFR)
Menarik pembaruan inkremental dari master upstream:
```bash
./rpz-master -action sync -c /etc/dnsdist/rpz-master.json
```

---

## ⚙️ Integrasi dengan DNSDist

Tambahkan konfigurasi berikut ke `/etc/dnsdist/dnsdist.conf`:

```lua
-- 1. Definisikan backend RPZ Master lokal
newServer({ address = '127.0.0.1:5353', pool = 'rpz-master', name = 'rpz-master' })

-- 2. Belokkan kueri transfer zona (AXFR/IXFR/SOA) TCP ke backend RPZ Master
addAction(
    AndRule({
        TCPRule(true),
        OrRule({
            QTypeRule(DNSQType.AXFR),
            QTypeRule(DNSQType.IXFR),
            QTypeRule(DNSQType.SOA)
        })
    }),
    PoolAction('rpz-master')
)

-- 3. Kueri reguler tetap dicegat instan via CDB KVStore
kvs_trustpositif = newCDBKVStore('/var/lib/dnsdist/trustpositif.cdb', 30)
kvsRule = KeyValueStoreLookupRule(kvs_trustpositif, KeyValueLookupKeyQName(true))

addAction(
    AndRule({kvsRule, QTypeRule(DNSQType.A)}),
    SpoofCNAMEAction("blockpage.komdigi.go.id.")
)
```

---

## 📋 Contoh Konfigurasi (`config.json`)

```json
{
  "source_mode": "rpz-slave",
  "zone": "rpz.trustpositif.",
  "listen_dns": "127.0.0.1:5353",
  "listen_http": "0.0.0.0:8088",
  "cname_target": "blockpage.komdigi.go.id.",
  "cdb_path": "/var/lib/dnsdist/trustpositif.cdb",
  "state_path": "/var/lib/dnsdist/rpz-master-state.json",
  "source_domain_url": "https://trustpositif.komdigi.go.id/assets/db/domains_isp",
  "upstream_master": "103.x.x.x:53",
  "tsig_key": "rpz-key.",
  "tsig_secret_file": "/etc/dnsdist-master/rpz-upstream.secret",
  "tsig_algorithm": "hmac-sha256.",
  "check_interval": "15m",
  "max_transfers": 10,
  "raw_domain_file": "/var/lib/dnsdist/domains_isp.txt"
}
```

File `tsig_secret_file` harus file reguler mode `0600` dan berisi secret base64. Jangan simpan secret langsung di JSON atau repository.

## Batas Atomicity Publikasi

File raw, CDB, manifest, dan state masing-masing dipublikasikan dengan atomic rename. Filesystem tidak menyediakan satu transaksi lintas empat path, sehingga crash di antara rename dapat meninggalkan metadata lama bersama CDB baru. Update delta bersifat idempoten dan sinkronisasi berikutnya memulihkan generasi konsisten; monitor `state.json`/manifest dan ulangi `-action sync` setelah crash. Upgrade ke pointer generasi tunggal diperlukan bila konsistensi crash lintas-file tanpa jendela mismatch menjadi syarat.
