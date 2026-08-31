# 🔄 Rencana: CDB Redundancy & Cluster

Sistem distribusi `blacklist.db` (CDB) antar node dengan redundancy dan cluster.
Status: **PLAN — belum implementasi.**

---

## 1. Masalah yang Dipecahkan

Kondisi sekarang (single-source):
```
Central Manager ──HTTP──▶ Edge A ──▶ dnsdist
                    └────▶ Edge B ──▶ dnsdist
```

| Masalah | Dampak |
|---------|--------|
| Central Manager down | Semua edge tidak bisa sync DB baru (berhenti di versi lama) |
| Bandwidth terpusat | Satu server layani semua node → bottleneck |
| No cross-check | Tidak tahu DB edge mana yang versi tertua/beda |
| No cluster view | Tidak ada satu panel untuk kelola banyak node |

**Tujuan:** setiap node bisa jadi **sumber** CDB untuk node lain.
Tidak ada single point of failure; cluster tetap sinkron walau central mati.

---

## 2. Konsep: Content-Addressed CDB (Hash-Identified)

Setiap CDB diberi identitas unik via hash:

```
blacklist.db          = symlink → blacklist.<SHA256>.db
manifest.json         = metadata versi & sumber
```

```
/manifest.json  →  { "version": 123, "sha256": "a1b2c3...", "size": 401223344,
                     "built_at": "2026-08-31T06:00:00Z",
                     "source": "central" | "local" | "peer:edge-b" }
```

- File DB **immutable** per hash: kalau isinya beda, hash-nya beda.
- `blacklist.db` tinggal symlink ke file hash → **swap atomic** (tanpa copy besar).
- Kalau ada node punya hash yang sama → **dijamin konten identik** (dedup alami).

---

## 3. Topologi (KEPUTUSAN: T1 dulu)

### T1. Central + Mirrors (Hub-Spoke) — *START*
```
        Central Manager (build utama)
           │ HTTP
        ┌──┴───────────┐
    Mirror A         Mirror B
        │  HTTP/peer     │
    ┌───┴───┐       ┌───┴───┐
   Edge A  Edge B  Edge C  Edge D
```
Edge sync dari **central dulu**, fallback ke **mirror** bila central down.

### T2. Mesh / Peer-to-Peer — *lanjutan*
```
   Edge A ◄────► Edge B
      │  ▲          ▲  │
      ▼  │          │  ▼
   Edge C ◄────► Edge D
```
Tiap node daftar 1+ peer. Node sync dari peer mana pun yang punya hash terbaru.
Bila satu peer down, otomatis pindah ke peer lain.

### T3. Cluster Terkelola (via Panel)
Panel jadi koordinator:
```
        ┌─── Panel (koordinator) ───┐
        │  cluster membership, hash │
        ▼       registry, health    ▼
   Edge A ◄───► Edge B ◄───► Edge C
   (semua node peer satu sama lain, panel tahu siapa punya hash terbaru)
```

---

## 4. Arsitektur & Komponen

```
┌─────────────────────────────────────────────────────────────┐
│                       NODE (tiap edge)                      │
│                                                             │
│  ┌──────────────┐   ┌────────────────────────────────────┐ │
│  │ CDB Publisher │   │ CDB Sync (update-blacklist.sh v3) │ │
│  │ (HTTP :8091)  │   │                                    │ │
│  │ - serve DB    │   │ - coba sumber berurutan (failover)│ │
│  │ - serve hash  │   │ - verifikasi SHA256 setelah       │ │
│  │ - serve       │   │   download                        │ │
│  │   manifest    │   │ - simp an ke <hash>.db + symlink  │ │
│  └──────┬────────┘   └───────┬────────────────────────────┘ │
│         │                    │                              │
│         └────────┬───────────┘                              │
│                  ▼                                          │
│         /var/lib/dnsdist/blacklist.<hash>.db                │
│         blacklist.db ──symlink──▶ (file hash terbaru)       │
│                  │                                          │
│                  ▼                                          │
│         dnsdist hot-reload otomatis (CDB KV 5 detik)        │
└─────────────────────────────────────────────────────────────┘
```

### Komponen baru
| Komponen | Lokasi | Fungsi |
|----------|--------|--------|
| `cdb-publisher` (embedded) | **di dalam panel** (port 8084) | Serve `blacklist.<hash>.db` + `manifest.json` + `healthz` — bukan binary terpisah |
| `update-blacklist.sh` v3 | `setup/` | Multi-URL failover + SHA256 verify + symlink swap |
| `manifest.json` | `/var/lib/dnsdist/manifest.json` | Metadata versi & sumber DB saat ini |
| `node.conf` ext | `/etc/dnsdist/node.conf` | `SAVED_CDB_SOURCES="central,mirror1,peer-a"` |

---

## 5. Alur Sinkronisasi (Failover)

```
update-blacklist.sh v3
│
├─1. Baca daftar sumber (central + mirror + peer)
│      order = urutan di node.conf
│
├─2. Untuk tiap sumber (sampai sukses):
│     a. GET <source>/manifest.json
│     b. Bandingkan version/sha256 dengan manifest lokal
│     c. Kalau beda → download blacklist.<sha>.db
│     d. Verifikasi SHA256 (kalau gagal → coba sumber berikutnya)
│     e. Swap symlink + update manifest lokal
│     ✓ sukses → berhenti
│
├─3. Semua gagal → pertahankan DB lama (tidak patah), log warning
│
└─4. Beritahu panel: hash baru, sumber yang berhasil
```

**Kriteria "lebih baru":** version (integer) naik. Kalau version sama tapi sha beda
→ ambil yang hash-nya cocok dengan mayoritas peer (cross-check di T3).

---

## 6. CDB Publisher (HTTP :8091)

Daftar endpoint yang disediakan tiap node:

```
GET /manifest.json                    → metadata DB saat ini
GET /blacklist.db                     → DB aktif (symlink resolved)
GET /blacklist.<sha>.db               → DB spesifik (immutable)
GET /healthz                          → 200 jika sehat, 500 jika DB rusak
GET /peers.json                       → daftar peer node ini (untuk discovery)
```

Dibuat **ter-embed di dalam panel** (Go, satu binary), serve di port panel (8084)
pada route `/cdb/*` — tidak perlu proses/port tambahan.

---

## 7. Integrasi Panel (dashboard cluster)

Halaman baru di panel:

### 🌐 Cluster
- **Node list**: IP, status (sehat/turun), versi DB (hash), sumber terakhir
- **Hash consensus**: tampilkan hash mana yang dipakai mayoritas node → tandai node beda hash (stale)
- **Trigger sync**: paksa node tertinggal sync dari peer terbaru
- **Peer management**: tambah/hapus peer per node

### 🚫 Blacklist (ditambah)
- Mode baru **"Cluster"**: pilih sumber = `central | mirror | peer:<node> | auto`
- Tampilkan manifest (version, sha, built_at, source) DB aktif vs yang tersedia

---

## 8. Keamanan

- Publisher hanya serve file yang sudah diverifikasi (whitelist path hash)
- Akses publisher embedded di panel (port 8084, bind `127.0.0.1` atau admin network)
- **Auth antar node:** `X-CDB-Token` (shared secret di `node.conf`), atau **link-by-OTP**:
  - Panel generate OTP (one-time pairing code)
  - Node B masukkan OTP → panel beri token + peer URL
  - OTP expired setelah 5 menit / sekali pakai
- Verifikasi SHA256 wajib — node tidak pernah pasang DB yang gagal verifikasi
- Symlink swap atomic (`mv` di direktori yang sama)

---

## 9. Roadmap

| Fase | Isi | Estimasi |
|------|-----|----------|
| **1. Multi-URL failover** ✅ Selesai | `update-blacklist.sh` v3.0.0: multi-source failover + manifest sidecar (tested) | ✅ |
| **2. SHA256 + symlink** ✅ Selesai | `blacklist.<sha>.db` + symlink swap + manifest (v3.1.0, tested) | ✅ |
| **3. CDB Publisher** | Embed di panel (Go): route `/cdb/*` serve DB + manifest + healthz | ½ hari |
| **4. Peer & Cluster** | `node.conf` sumber peer, failover antar peer | ½ hari |
| **5. Panel cluster view** | Node list, hash consensus, trigger sync, peer mgmt | 1 hari |

---

## 10. Keputusan (FINAL)

| Variabel | Keputusan |
|----------|-----------|
| **Topologi awal** | T1 (central + mirrors) — hub-spoke dulu |
| **Kriteria terbaru** | Version integer (standar, ascending) |
| **Publisher** | Embed di panel (Go, route `/cdb/*`, port 8084) |
| **Auth antar node** | Token (`X-CDB-Token`) + link-by-OTP (panel generate pairing code 5 menit) |

---

## 11. Catatan

- Ini **pelengkap** dari PANEL-PLAN, bukan pengganti.
- Mode A (central) tetap default; cluster menambah failover tanpa mengubah
  filosofi "edge tidak proses TXT ke CDB".
- `trust-builder` (local gen) tetap relevan: bisa jadi salah satu "sumber"
  dalam daftar (sumber lokal).
