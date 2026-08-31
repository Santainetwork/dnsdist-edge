# 🖥️ Rencana: DNSDist Management Panel

Panel web untuk mengelola DNSDist Edge Node secara visual.
Status: **PLAN — belum implementasi.**

---

## 1. Jawaban Pertanyaan Kunci

> "Panel total lengkap dengan CDB gen, atau cukup download dari tempat lain?"

**Dua-duanya didukung, via mode sumber blacklist:**

| Mode | Sumber Blacklist | Kapan Dipakai |
|------|------------------|---------------|
| **A. Central Download** (default) | Ambil `blacklist.db` dari Central Manager via `update-blacklist.sh` (HTTP + aria2, hot-reload) | Setup normal, multi-node, hemat CPU/RAM di edge |
| **B. Local CDB Gen** | Panel menulis daftar domain → `tools/gen-cdb.py` → `blacklist.db` → dnsdist hot-reload | Offline / mandiri / trial, atau saat central server mati |

> Rekomendasi: **default Mode A** (sesuai arsitektur "Edge tidak memproses TXT ke CDB").
> Mode B disediakan sebagai mode penuh (total lengkap) & fallback darurat.

---

## 2. Arsitektur

```
┌──────────────────────────────────────────────────────────────┐
│                        PANEL (port 8084)                     │
│                                                              │
│  Frontend: index.html + JS (tanpa build step, Tailwind CDN)  │
│       │ REST API (FastAPI/Flask, auth token)                 │
│       ▼                                                      │
│  Backend: panel/app.py                                       │
│    ├─ orkestrasi: update-blacklist.sh (Mode A)               │
│    ├─ orkestrasi: gen-cdb.py (Mode B)                        │
│    ├─ baca status dnsdist (API :8083 /api/v1/*)              │
│    ├─ health check (tools/dnsdist-health.sh)                 │
│    └─ control service (reload/restart via systemctl)         │
│                                                              │
│  Storage: /etc/dnsdist/panel/                                │
│    ├─ domains.txt        (daftar domain Mode B)              │
│    └─ source.conf        (mode aktif + URL central)          │
└──────────────────────────────────────────────────────────────┘
        │                      │                    │
        ▼                      ▼                    ▼
   dnsdist :8083      update-blacklist.sh     gen-cdb.py → blacklist.db
   (top-stats API)    (Mode A)                (Mode B, hot-reload)
```

- **Backend:** Python (FastAPI atau Flask) — konsisten karena toolchain repo sudah Python (`gen-cdb.py`, `build-asn-db.sh`).
- **Frontend:** 1 file HTML + JS polos, Tailwind via CDN. **Tanpa npm/build** (lazy).
- **Port:** 8084 (terpisah dari dnsdist 8083).
- **Systemd:** `dnsdist-panel.service`.

---

## 3. Fitur per Halaman

### 📊 Dashboard
- Status service dnsdist (active/inactive)
- QPS, total query, total blocked (dari API top-stats)
- Ukuran & umur `blacklist.db`, mode sumber aktif
- Top 10 blocked domain (singkat)
- Health check singkat (sumber: `tools/dnsdist-health.sh`)

### 🚫 Blacklist (inti, 2 mode)
- **Mode A:** lihat URL central, tombol *Sync Now* & *Force Update*, log sinkronisasi terakhir
- **Mode B:** kelola daftar domain (tambah/hapus/import file), tombol *Build CDB* → jalankan `gen-cdb.py` → reload otomatis
- Toggle mode A/B (dengan konfirmasi)

### ⚙️ Konfigurasi
- BLOCK_MODE (rpz/adguard), SINKHOLE_IPS
- Upstream DNS (baca/tulis `upstreams.conf` via `--set-upstream`)
- ACL, rate limit (tampilkan saja dulu)
- Tombol *Reload* / *Restart* service

### 📈 Statistik
- Top queries, top blocked, top ASN, top clients (proxy dari `:8083/api/v1/*`)

### 🛡️ Sistem
- Health check penuh (jalankan `dnsdist-health.sh`, tampilkan output)
- Log viewer (`journalctl -u dnsdist`, `dnsdist-sync.log`)
- Info node: RAM, CPU, disk, ukuran DB

---

## 4. Layout File (di repo)

```
panel/
├── app.py                 # FastAPI: routes + auth + orkestrasi
├── core.py                # wrapper: sync, build-cdb, baca dnsdist API
├── requirements.txt       # fastapi, uvicorn (atau flask)
├── static/
│   ├── index.html         # SPA panel
│   └── app.js             # logika frontend
└── dnsdist-panel.service  # unit systemd (port 8084)
```

---

## 5. API Backend (ringkas)

```
POST /api/login                 → token (panel)
GET  /api/dashboard             → status ringkas
GET  /api/sync/status           → mode, URL, last sync
POST /api/sync                  → jalankan update-blacklist.sh
POST /api/sync?force=true       → force update
GET  /api/blacklist/domains     → daftar domain Mode B
POST /api/blacklist/domains     → tambah domain
DELETE /api/blacklist/domains/{d} → hapus domain
POST /api/blacklist/import      → upload file daftar
POST /api/blacklist/build       → jalankan gen-cdb.py + reload
POST /api/config                → simpan mode/upstream/sinkhole
POST /api/service/reload        → systemctl reload dnsdist
POST /api/service/restart       → systemctl restart dnsdist
GET  /api/stats/top-queries     → proxy :8083
GET  /api/stats/top-blocked     → proxy :8083
GET  /api/health                → jalankan dnsdist-health.sh
GET  /api/logs?lines=100        → baca log
```

---

## 6. Keamanan

- Auth token wajib; bind default `127.0.0.1:8084` (admin via reverse proxy + TLS)
- Jangan expose :8084 publik; kalau perlu, pakai nginx + Let's Encrypt
- Panel menjalankan perintah sistem **hanya daftar aksi tetap** (sync/build/reload/restart) — tidak ada arbitrary shell
- Kredensial panel terpisah dari credential dnsdist (`PANEL_USER`/`PANEL_PASS` di `node.conf`)

---

## 7. Roadmap (bertahap)

| Fase | Isi | Estimasi |
|------|-----|----------|
| **1. MVP** | Backend + dashboard + Mode A (sync via tombol) + lihat top-stats | ~½ hari |
| **2. Mode B** | Kelola domain + gen-cdb lokal + hot-reload | ~½ hari |
| **3. Config & Sistem** | Editor config, reload/restart, health, log viewer | ~½ hari |
| **4. Polish** | UI, keamanan, systemd, docs, commit | ~½ hari |

---

## 8. Keputusan yang Perlu Diambil

1. **Backend:** FastAPI vs Flask? (saya rekomendasikan FastAPI)
2. **Scope:** single-node saja, atau mau *fleet* (banyak node dikontrol 1 panel)?
3. **Mode B (local CDB gen):** first-class, atau cukup fallback darurat?
4. **Frontend:** plain JS + Tailwind CDN cukup, atau mau framework (Vue/React)?

---

## 9. Catatan

- Mode A tetap **default** dan paling ringan — sesuai filosofi "Edge tidak proses TXT ke CDB".
- `gen-cdb.py` sudah teruji valid (smoke test lookup PASS), siap diintegrasikan untuk Mode B.
- Semua perintah eksternal yang dipakai panel sudah ada & teruji di repo (`update-blacklist.sh`, `gen-cdb.py`, `dnsdist-health.sh`).
