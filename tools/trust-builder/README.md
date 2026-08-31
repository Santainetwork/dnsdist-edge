# Trust-Builder — CDB Blacklist Generator (Go)

High-performance CDB blacklist generator untuk DNSDist & Unbound.
Disk-streaming, multi-part download, ETag cache, atomic replace, wire-format key.

## Build

```bash
cd tools/trust-builder
./build.sh        # atau: go build -o trust-builder main.go
```

## Usage

```bash
# Dari URL (multi-part download)
./trust-builder -o blacklist.db -u "https://trustpositif.komdigi.go.id/assets/db/domains_isp"

# Dari file lokal
./trust-builder -o blacklist.db domains.txt extra.txt

# Multi URL + whitelist
./trust-builder -u "url1,url2" -w whitelist.txt -o blacklist.db

# Kontrol koneksi & force re-download
./trust-builder -o blacklist.db domains.txt -c 8
./trust-builder -o blacklist.db -u URL -force
```

## Flags

| Flag | Deskripsi |
|------|-----------|
| `-o`  | Lokasi output CDB (default `blacklist.db`) |
| `-u`  | Satu atau lebih URL dipisah koma |
| `-w`  | File whitelist (domain/IP yang dikecualikan) |
| `-c`  | Jumlah koneksi paralel per URL (default 8) |
| `-force` | Paksa re-download meski ETag sama |

## Fitur

- Streaming ke disk, bukan RAM (hemat memori untuk jutaan domain)
- ETag/Last-Modified cache per-URL: skip jika tidak berubah
- Source gagal dilewati tanpa merusak file output
- Atomic replace: file hanya diganti bila build sukses
- Wire-format key via `miekg/dns` (lowercase + trailing dot)
- Whitelist exclusion (subdomain-aware)

Output dipakai DNSDist via `newCDBKVStore('/var/lib/dnsdist/blacklist.db', 5)`
(hot-reload otomatis).

---

Sumber asli: `../dnsdist-manager/golang/` (Trust-NG project).
