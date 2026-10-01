# Spesifikasi Desain: Adopsi Arsitektur DnsJos (Multi-DB SQLite & PostgreSQL + Streaming dnstap)

**Tanggal:** 2026-10-02  
**Status:** Draf Arsitektur / Bounded Implementation Spec  
**Referensi:** `https://github.com/billyriantono/dnsjos`, `docs/CLUSTER-PLAN.md`, `docs/PANEL-PLAN.md`

---

## 1. Konteks & Analisis Komparatif DnsJos

DnsJos (`github.com/billyriantono/dnsjos`) adalah control panel armada DNSDist modern berbasis:
- Go 1.25 + PostgreSQL >= 14 (pgx/v5)
- React 19 + Tailwind v4 + shadcn/ui
- Agent pull-model (node mengeksekusi config rollout aman dengan rollback otomatis)
- Streaming log query terblokir & analitik via protokol **dnstap** (protobuffer framestream)

### Analisis Kebutuhan Trust-NG (`dnsdist-edge`):
1. **Multi-Database (SQLite + PostgreSQL):**
   - DnsJos mengunci dependensi ke PostgreSQL >= 14. Hal ini sangat memberatkan edge node mandiri (VPS 1-2 GB RAM atau router edge mikro).
   - Trust-NG membutuhkan arsitektur **multi-engine**:
     - **Mode Edge / Standalone:** Menggunakan SQLite (pure-Go, zero-cgo via `modernc.org/sqlite` atau file JSON default saat ini) tanpa perlu menjalankan instance database server terpisah.
     - **Mode Central Master / Cluster:** Menggunakan PostgreSQL (driver `pgx/v5`) untuk konkurensi tinggi, pooling koneksi, dan analitik gabungan lintas node.
2. **Fungsi dnstap (Untuk Apa?):**
   - Mengapa dnsdist membutuhkan dnstap alih-alih Lua logging?
     - **Zero Lua Overhead:** dnsdist memproses ratusan ribu QPS di C++. Menjalankan callback Lua untuk setiap query menciptakan lock contention dan membebani CPU. `DnstapLogAction` di dnsdist melakukan buffer asynchronous di level socket (framestream TCP/UNIX) dengan latensi sub-milidetik.
     - **Privasi Penuh (Zero Client IP Logging):** Sesuai etika dan kepatuhan privasi (UU PDP / regulasi ISP), penerima dnstap hanya mengagregasi data `(tanggal, qname, qtype)` dan mengabaikan atau langsung membuang Client IP dari memori.
     - **Laporan Kepatuhan ISP / Kominfo (TrustPositif):** Memberikan agregasi harian domain terblokir yang paling sering diakses tanpa membebani disk IO (hanya batch counter).

---

## 2. Arsitektur Multi-Database (Dual-Engine Storage)

### 2.1 Interface Repository Terpadu (`panel/storage.go`)

Panel mendefinisikan interface penyimpanan data cluster:

```go
type ClusterRepository interface {
    // Token Manajemen Pendaftaran Node
    SaveToken(token string, expiresAt time.Time) error
    ConsumeToken(token string) bool
    
    // Node Registry & Telemetri
    UpsertNode(node *NodeRecord) error
    GetNode(id string) (*NodeRecord, bool)
    ListNodeRecords() []*NodeRecord
    DeleteNode(id string) bool
    
    // Lifecycle
    Close() error
}
```

### 2.2 Hirarki Engine:
1. **Engine JSON File (`FileClusterStore`):**
   - Default bawaan tanpa konfigurasi tambahan (`/etc/dnsdist/cluster_nodes.json`).
   - 100% backward compatible dengan implementasi v2.9.0 saat ini.
2. **Engine SQLite (`SQLiteClusterStore`):**
   - Aktif jika `DB_ENGINE=sqlite` atau `DATABASE_URL=sqlite:///var/lib/dnsdist/panel.db`.
   - Menggunakan pure-Go SQLite driver tanpa CGO (`modernc.org/sqlite`).
   - ACID transaction, file tunggal, konsumsi RAM < 15MB.
3. **Engine PostgreSQL (`PostgresClusterStore`):**
   - Aktif jika `DB_ENGINE=postgres` atau `DATABASE_URL=postgres://...`.
   - Menggunakan pool `pgx/v5`.
   - Cocok untuk Master Panel yang menampung telemetri ratusan edge nodes.

---

## 3. Arsitektur Streaming dnstap

```
┌────────────────────────────────────────────────────────┐
│                      DNSDist 1.9+                      │
│                                                        │
│  Query Masuk ──▶ Rule: CDB Match (TrustPositif)        │
│                        │                               │
│                   Match: Blocked!                      │
│                        │                               │
│       ┌────────────────┴─────────────────┐             │
│       ▼                                  ▼             │
│   SpoofAction (NXDOMAIN / CNAME)    DnstapLogAction    │
│                                          │             │
└──────────────────────────────────────────┼─────────────┘
                                           │ Unix/TCP Socket (:6000)
                                           ▼
┌────────────────────────────────────────────────────────┐
│             dnstap Collector (Go Subprocess)           │
│                                                        │
│  Framestream Reader ──▶ Protobuf Unmarshal             │
│                               │                        │
│                   Drop Client IP immediately!          │
│                               │                        │
│            Aggregate Map (In-Memory Ring/Cap)          │
│            Key: {Date, QName, QType} ──▶ Counter++     │
│                               │                        │
│                    Spool Flush (Tiap 60s)              │
│                               ▼                        │
│            Batch Post / Simpan ke SQLite/PG            │
└────────────────────────────────────────────────────────┘
```

### 3.1 Detail dnsdist.conf untuk dnstap
```lua
-- dnstap streaming logger ke port lokal collector
local fstrm = newFrameStreamTcpLogger("127.0.0.1:6000")
-- Log query terblokir saja ke dnstap collector
addAction(AndRule({OrRule({kvd_trust, kvd_custom})}), DnstapLogAction("edge-node-01", fstrm))
```

### 3.2 Keamanan & Pembatasan Memori:
- Aggregator menerapkan limit `Cap` (misal 50.000 entri per jendela flush).
- Entri setelah limit dialihkan ke bucket `_other_` untuk mencegah OOM bila terjadi flood query unik (misal random subdomain attack / DNS water torture).

---

## 4. Tahapan Eksekusi Bounded

1. **Fase 1 (Overnight Fondasi):**
   - Abstraksi interface `ClusterRepository` di `panel/cluster.go` agar storage decoupled.
   - Implementasi adapter interface untuk `ClusterStore` JSON file eksisting (zero breaking change).
   - Implementasi paket dnstap framestream decoder & aggregator mandiri di Go dengan verifikasi unit test.
2. **Fase 2 (Multi-DB Integration):**
   - Penyediaan SQLite driver pure-Go terisolasi.
   - Migrasi skema tabel `nodes`, `enrollment_tokens`, dan `blocked_analytics`.
3. **Fase 3 (End-to-End dnsdist dnstap):**
   - Penambahan opsi `--with-dnstap` di installer `setup/setup-edge.sh` dan baseline `setup/dnsdist.conf`.
