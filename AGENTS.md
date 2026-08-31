# AGENTS.md — DNSDist Edge Node (Trust-NG)

Panduan untuk AI agents yang bekerja di repositori ini.

## Apa ini

Konfigurasi & script untuk DNSDist Edge Node: filtering DNS (RPZ/AdGuard mode),
blacklist CDB sync, top-stats module, monitoring (Grafana/Prometheus), dan
management panel (repo terpisah: `Santainetwork/dnsdist-panel`).

Struktur inti (jangan dipindah tanpa update path di script):

```
setup/          # CORE — self-contained, cukup ini untuk deploy node
  ├── setup-edge.sh        # installer CLI (17 opsi, lihat docs/SETUP-EDGE-COMMANDS.md)
  ├── update-blacklist.sh  # sync CDB dari central (Mode A)
  ├── dnsdist.conf         # config baseline
  ├── deploy-edge.yml      # Ansible playbook (opsional)
  └── templates/node.conf.j2
addons/         # OPSIONAL — top-stats.lua, build-asn-db.sh, asn-toolkit/
tools/          # gen-cdb.py (CDB kecil), dnsdist-health.sh, trust-builder/ (Go CDB gen)
monitoring/     # grafana + prometheus config
docs/           # SETUP.md, SETUP-EDGE-COMMANDS.md, PANEL-PLAN.md, CLUSTER-PLAN.md, QUICK_REFERENCE.md
```

## Build & verifikasi

```bash
# Script validation
bash -n setup/setup-edge.sh setup/update-blacklist.sh tools/dnsdist-health.sh

# trust-builder (Go) — mesin dev Go 1.22.5:
cd tools/trust-builder && GOTOOLCHAIN=local ./build.sh

# gen-cdb smoke
python3 tools/gen-cdb.py /tmp/t.db evil.com && rm /tmp/t.db
```

## Konvensi penting

- `setup/` **self-contained**: 3 file inti (`setup-edge.sh`, `dnsdist.conf`, `update-blacklist.sh`) cukup untuk deploy. Addon optional di `addons/` — `setup-edge.sh` deteksi otomatis, kalau tidak ada lewati.
- **Jangan pindahkan file antar folder** tanpa update semua referensi path:
  - `setup-edge.sh` mencari `dnsdist.conf`, `update-blacklist.sh` di `$EDGE_DIR` (sama folder)
  - addon dicari di `$EDGE_DIR/../addons/` fallback `$EDGE_DIR/`
- Versi script: `setup-edge.sh` dan `update-blacklist.sh` = `SCRIPT_VERSION` (2.1.0). Bump bersamaan + update CHANGELOG.
- Credential default (`trust-ng-admin` / `trust-ng-apikey-changeme`) — jangan commit secret asli.
- File besar (asn-db.bin, ipinfo_lite.csv, blacklist.db, *.key) di-ignore — jangan `git add -f`.
- Push via HTTPS (SSH deploy key tidak punya akses org repos): remote sudah `https://github.com/Santainetwork/dnsdist-edge.git`.

## Flow deployment

1. `cd setup && sudo ./setup-edge.sh --install --url <CENTRAL_URL>` (atau via Ansible `deploy-edge.yml`)
2. Cron tiap 3 jam: `/usr/local/bin/update-blacklist.sh` → hot-reload CDB 5 detik
3. Monitoring: dnsdist :8083 (web API + top-stats), panel :8084 (repo terpisah)

## Plan aktif (docs/)

- `docs/PANEL-PLAN.md` — panel web (Fase 1-3 ✅ di repo dnsdist-panel, Fase 4 clustermenunggu)
- `docs/CLUSTER-PLAN.md` — CDB redundancy: T1 central+mirror, hash-addressed CDB, publisher embed di panel, auth token/OTP. Keputusan final tercatat di sana.
