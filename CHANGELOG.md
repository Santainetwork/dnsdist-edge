# Changelog

Semua perubahan penting pada project ini akan didokumentasikan di file ini.

Format mengikuti [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
dan project ini mengikuti [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.1.0] - 2026-08-31

### Added
- `tools/gen-cdb.py`: generator CDB wire-format (dipakai docs, sebelumnya tidak ada)
- `tools/dnsdist-health.sh`: health check node (service, config, DB, resolusi, port)
- GitHub Actions CI (`.github/workflows/ci.yml`): shellcheck, yamllint, smoke test CDB
- `SECURITY.md`: kebijakan keamanan & panduan pelaporan kerentanan

### Changed
- Dedupe: `scripts/asn-toolkit/` hanya menyimpan data (`ipinfo_lite.csv`, `asn-db.bin`, `dnsdist.conf`); source script tunggal di `scripts/`
- Path resolver di `setup-edge.sh` diarahkan ke `../scripts/` (single source of truth)
- Versi script `setup-edge.sh` & `update-blacklist.sh` -> 2.1.0

## [2.0.0] - 2026-08-31

### Added
- Restrukturisasi folder: `setup/`, `config/`, `scripts/`, `docs/`, `templates/`, `monitoring/`, `tools/`, `certs/`
- Dokumentasi CLI `docs/SETUP-EDGE-COMMANDS.md` (17 opsi perintah `setup-edge.sh`)
- Panduan instalasi `docs/SETUP.md` dan cheatsheet `docs/QUICK_REFERENCE.md`
- File `LICENSE` (MIT) dan `CHANGELOG.md`
- Deteksi otomatis path modul `top-stats.lua`, `build-asn-db.sh`, `ipinfo_lite.csv` di `../scripts/`
  saat `setup-edge.sh` dijalankan dari folder `setup/`
- `templates/node.conf.j2` diperluas: `SAVED_CERT_MODE`, password/API key webserver opsional,
  dan `SAVED_INSTALL_DATE` via Ansible

### Changed
- `config/deploy-edge.yml` disesuaikan dengan struktur baru (path repo root, `setup/` folder,
  dukungan `--password`/`--apikey` opsional)
- README & dokumen lain: URL repo diperbarui ke `Santainetwork/dnsdist-edge`

## [1.0.0] - 2026-07-28

### Added
- Rilis awal: `setup-edge.sh` v2.0.0, `update-blacklist.sh`, `top-stats.lua`, `build-asn-db.sh`
- Arsitektur Central vs Edge (pembuatan CDB terpusat, sinkronisasi hot-reload di Edge)
- Monitoring stack Grafana + Prometheus
