# Spesifikasi Desain: Multi-Database Cluster Storage & Streaming dnstap

**Tanggal:** 2026-10-02  
**Status:** Implementasi Selesai (v3.0.0)  
**Referensi:** `docs/CLUSTER-PLAN.md`, `docs/PANEL-PLAN.md`

---

## 1. Konteks

Panel ini awalnya menyimpan data cluster (token enroll, node registry, telemetri) dalam satu file JSON. Itu cukup untuk 1-3 node, tetapi tidak skalabel untuk master dengan puluhan node: seluruh state di-muat ke RAM, ditulis penuh ke disk tiap heartbeat, dan tidak punya transaksi.

Kebutuhan yang muncul:

1. **Multi-engine storage** agar edge node tidak wajib menjalankan database server.
2. **Analitik domain terblokir** tanpa Lua callback per-query dan tanpa menyimpan Client IP.

---

## 2. Arsitektur Multi-Database (Dual-Engine Storage)

### 2.1 Interface Repository Terpadu (`panel/storage.go`)

Panel mendefinisikan interface penyimpanan data cluster:

```go
type ClusterStorage interface {
    GenerateEnrollmentToken(duration time.Duration) string
    ValidateAndConsumeToken(tok string) bool
    RegisterNode(req RegisterRequest, remoteIP string) (*NodeRecord, error)
    ProcessHeartbeat(req HeartbeatRequest, remoteIP string) error
    ListNodes() ([]*NodeRecord, ClusterAggregate)
    DeleteNode(id string) error
    Close() error
}
```

### 2.2 Hirarki Engine:

1. **Engine JSON File (`ClusterStore`):**
   - Default bawaan tanpa konfigurasi tambahan (`/var/lib/dnsdist/cluster_nodes.json`).
   - 100% backward compatible dengan implementasi sebelum v3.0.0.
2. **Engine SQLite (`SQLiteClusterStore`):**
   - Pure-Go tanpa CGO (`modernc.org/sqlite`).
   - Mode WAL, transaksi ACID, file tunggal, konsumsi RAM rendah.
   - Migrasi skema otomatis saat startup.
3. **Engine PostgreSQL (`PostgresClusterStore`):**
   - Berpegang pada interface yang sama via pool `pgx/v5`.
   - Cocok untuk Master Panel dengan telemetri ratusan edge node.
   - Antarmuka sudah tersedia; adapter lengkap dibangun saat mode cluster diaktifkan.

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
                                           │ TCP Socket (--dnstap-addr)
                                           ▼
┌────────────────────────────────────────────────────────┐
│          DnstapServer (panel/dnstap.go)                 │
│                                                        │
│  Framestream Reader ──▶ Protobuf Unmarshal             │
│                               │                        │
│                   Drop Client IP immediately!          │
│                               │                        │
│            Aggregate Map (bounded capacity)            │
│            Key: {QName, QType} ──▶ Counter++           │
│                               │                        │
│              Overflow ──▶ bucket "_other_"              │
└────────────────────────────────────────────────────────┘
                                           │
                                           ▼
                              GET /api/dnstap/top?limit=N
```

### 3.1 Contoh Konfigurasi dnsdist.conf

```lua
-- dnstap streaming logger ke collector lokal
local fstrm = newFrameStreamTcpLogger("127.0.0.1:6000")
-- Log query terblokir saja
addAction(kvsRule, DnstapLogAction("edge-node-01", fstrm))
```

Panel dijalankan dengan `--dnstap-addr 127.0.0.1:6000`.

### 3.2 Privasi & Pembatasan Memori

- **Zero Client IP:** `QueryAddress` dan `QueryPort` dibuang seketika setelah parsing protobuf. Tidak pernah disimpan, tidak masuk log, tidak masuk response API.
- **Bounded memory:** agregator punya batas entri. Query unik yang melebihi batas dialihkan ke bucket `_other_` sehingga flood subdomain acak tidak menyebabkan OOM.

---

## 4. Endpoint API

| Method | Path | Keterangan |
|---|---|---|
| `GET` | `/api/dnstap/top?limit=N` | Domain terblokir teratas dari jendela agregasi berjalan |

Endpoint memakai autentikasi yang sama dengan API panel lain.

---

## 5. Status Implementasi (v3.1.0)

- [x] Abstraksi `ClusterStorage` di `panel/storage.go`
- [x] Adapter backward-compatible pada `panel/cluster.go`
- [x] `SQLiteClusterStore` pure-Go dengan WAL + auto-migration (`panel/storage.go`)
- [x] TTL token enroll dan status node dinamis
- [x] Decoder framestream dnstap + agregator bounded (`panel/dnstap.go`)
- [x] Pengabaian Client IP seketika (UU PDP)
- [x] Flag `--dnstap-addr` dan route `/api/dnstap/top`
- [x] Test suite: `panel/storage_test.go`, `panel/dnstap_test.go`
- [x] Adapter `PostgresClusterStore` pure-Go via pgx/v5 stdlib (`panel/pgstore.go`): skema otomatis, DSN via `--database-url` / `PANEL_DATABASE_URL` / `PANEL_DB_*` / `postgres://` di `--cluster-nodes-file`
- [x] Test `panel/pgstore_test.go`: lifecycle live (skip bila `PANEL_TEST_DATABASE_URL` kosong) + `TestPostgresDSNFromEnv` (env/escaping, selalu jalan)
- [ ] Benchmark telemetri ratusan node & migrasi JSON/SQLite → Postgres