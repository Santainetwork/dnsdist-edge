# Changelog — DNSDist Edge Node (Trust-NG)

Semua perubahan signifikan dicatat di sini.
Format: [versi] — tanggal, deskripsi singkat.

---

## [2.6.0] — 2026-09-19

### 🌟 Web Node Enrollment & Storage Cleanup
- **Web enrollment:** Master membuat token sekali pakai 10 menit, membuka handoff aman ke panel Edge via URL fragment, lalu Edge meminta konfirmasi sebelum register.
- **Cluster validation:** Master dan Edge menerima telemetry setelah enrollment; token replay serta URL tidak aman ditolak.
- **Storage:** Edge updater dan Master publisher menghapus CDB hash lama setelah atomic symlink swap. DB aktif serta backup manual non-hash dipertahankan.
- **Release:** Panel, installer Edge/Master, dan telemetry runtime disinkronkan ke v2.6.0. `update-blacklist.sh` v3.1.1.

---

## [3.1.1] — update-blacklist.sh — 2026-09-19

- Hapus otomatis file CDB hash lama setelah atomic symlink swap berhasil.
- Berlaku pada Edge, publisher Master Go, dan fallback builder shell.
- File aktif dan backup manual dengan nama non-hash tidak disentuh.

---

## [2.5.1] — 2026-09-09

### 🌟 Centralized Multi-Node Monitoring (Mode A)
- **Master Cluster Backend**:
  - Pendaftaran node terpusat dengan token pendaftaran sementara (24 jam, sekali pakai) via CLI (`dnsdist-panel -enrollment-token`) dan Web UI modal.
  - Endpoint ingestion telemetri live (`POST /api/cluster/heartbeat`) dengan proteksi kunci node.
  - Snapshot status seluruh node tersimpan persisten di `/var/lib/dnsdist/cluster-nodes.json`.
  - Deteksi otomatis status node: `online`, `degraded` (dnsdist berhenti), dan `offline` (> 3 menit tanpa heartbeat).
  - Pelacakan dinamis perubahan alamat IP node edge saat mengirim heartbeat.
- **Edge Telemetry Push Agent**:
  - Agen latar belakang otomatis berjalan tiap 60 detik, mengirim data QPS, total query, total diblokir, persentase cache hit, CPU, RAM, uptime, dan hash SHA256 database blacklist aktif.
  - Siklus hidup agen andal: otomatis mengaktifkan ticker saat didaftarkan secara dinamis lewat Web UI tanpa restart service.
  - Graceful stop channel yang bersih dan bebas race condition.
- **Web UI Cluster Dashboard**:
  - Menu baru **Cluster Nodes** di mode master dengan 4 metrik agregat (Node Aktif/Total, Total Cluster QPS, Total Queries/Blocked, Rata-rata Cache Hit).
  - Tabel node live yang aman dari injection (sanitasi HTML `esc()`) dan fungsi penghapusan node berbasis ID.
  - Kartu koneksi master di menu **Pengaturan** pada node edge untuk menghubungkan node secara instan dari peramban.
- **Peningkatan Installer**:
  - `setup-edge.sh` mendukung opsi `--master-url`, `--enroll-token`, dan `--node-name`.
  - Injeksi variabel environment systemd yang aman tanpa pemotongan newline.
  - Konfigurasi cluster tersimpan otomatis pada `save_config` dan dimuat pada migrasi/upgrade.

---

## [2.5.0] — 2026-09-08

### 🌟 Minor Release Highlights
- **Installer Central Master Server (`setup-master.sh`)**:
  - Pilihan mode fleksibel: Standalone (`--no-dnsdist`, tanpa port 53, murni generator & distributor) atau Hybrid (`--with-dnsdist`).
  - Dilengkapi `build-master-cdb.sh` untuk kompilasi CDB otomatis dari Trust Positif, AdGuard, custom blacklist, dan whitelist.
  - Distribusi file biner CDB (`trust.db`) dan sidecar metadata `manifest.json` via Nginx (default port 8080).
  - Cronjob otomatis tiap 6 jam.
- **Panel Fleksibel Dual-Listen**:
  - Mendukung dua port secara bersamaan: HTTPS di `:8443` dan HTTP di `:8084` tanpa warning SSL.
  - Mendukung mode pure HTTP via flag `-tls=false` atau env `PANEL_TLS=false`.
- **Desain UI/UX Pro Max**:
  - Antarmuka web panel didesain ulang total dengan palet Cyber Dark, indikator status *pulse ring*, dan layout responsif.
  - Grafik aktivitas query per detik (QPS) interaktif real-time menggunakan Canvas API murni.
  - Ikon SVG modern menggantikan emoji.
  - Tombol one-click copy untuk daftar IP sinkhole dan upstream.
- **Peningkatan CLI & Kompatibilitas**:
  - `setup-edge.sh`: Pemasangan panel mandiri tanpa install ulang DNSDist (`--with-panel` / `--add-panel`).
  - Download cepat panel binary dengan `aria2c` multi-connection (8 koneksi paralel) dan fallback `curl`.
  - Auto-update panel saat menjalankan `setup-edge.sh --upgrade`.
  - Kompatibilitas penuh POSIX `sh` / Dash pada `update-blacklist.sh`.

---

## [2.4.2] — 2026-09-08

### 🚀 Added & Improved
- **Download Cepat aria2c**: `setup-edge.sh` kini menggunakan `aria2c` multi-connection (8 koneksi paralel) untuk download panel binary, dengan fallback otomatis ke `curl` jika `aria2c` belum ada.
- **Standalone `--with-panel` / `--add-panel`**: Bisa menambahkan panel ke node DNSDist yang sudah berjalan tanpa perlu instalasi ulang dari awal (`sudo ./setup-edge.sh --add-panel`).
- **Auto-upgrade Panel**: Menjalankan `sudo ./setup-edge.sh --upgrade` kini otomatis mendeteksi dan memperbarui binary panel jika panel sudah terpasang.
- **Auto-hook Config**: `do_install_panel` otomatis menyisipkan hook `dotdoh.conf` dan `safesearch.conf` ke `dnsdist.conf` yang sedang aktif.

### 🐛 Fixed
- **update-blacklist.sh**: Memperbaiki error `Syntax error: redirection unexpected` saat script dijalankan menggunakan `/bin/sh` (Dash pada Debian/Ubuntu) dengan mengganti loop bashism `<<<` dan `read -a` menjadi POSIX `sh` loop portabel.
- **setup-edge.sh**: Pemanggilan skrip sinkronisasi kini secara eksplisit menggunakan `bash "$SCRIPT_UPDATE"`.

---

## [2.4.1] — 2026-09-08

### 🐛 Bugfix (ditemukan via 18 integration test)
- **panel**: path `safesearch.conf`, `dotdoh.conf`, `panel.password` hardcoded ke `/etc/dnsdist/` → sekarang derive dari `filepath.Dir(flagConf)` / `filepath.Dir(flagSecret)` sehingga binary bisa dijalankan dengan path custom (`-config`, `-secret-file`)
- **panel**: `panel.password` write error sebelumnya silent drop (`_ = ...`) → sekarang return HTTP 500 dengan pesan error
- **panel**: default `cert_path` / `key_path` di `/api/config` sekarang relatif ke confDir, bukan hardcode

### Panel (v2.4.1)
- Binary `tools/dnsdist-panel` — Go 1.22, stdlib only, 6.5 MB
- HTTPS `:8443`, self-signed ECDSA P-256 cert auto-generate
- JWT HS256 manual (tanpa dependency eksternal), secret di `panel.secret`
- UI embed single-file `panel/static/index.html` (501 baris, dark theme)
- **API endpoints:**
  - `POST /api/login` — JWT auth
  - `GET  /api/stats` — QPS (UDP delta `/proc/net/snmp`), CPU, RAM, uptime, dnsdist status
  - `GET  /api/config` — parse `dnsdist.conf` + `upstreams.conf` + `safesearch.conf`
  - `POST /api/rpz` — validasi IP, jalankan `setup-edge.sh --set-rpz`
  - `POST /api/upstream` — jalankan `setup-edge.sh --set-upstream`
  - `POST /api/safesearch` — write `safesearch.conf` atomic + restart dnsdist
  - `POST /api/dotdoh` — write `dotdoh.conf` atomic + restart dnsdist
  - `POST /api/settings` — block_mode + ganti password panel

---

## [2.4.0] — 2026-09-08

### 🚀 RPZ Multi-IP + IPv6 Support (`setup-edge.sh` v2.4.0)

#### Fitur Baru
- **`--set-rpz` multi-IP**: sekarang terima IPv4, IPv6, `[IPv6]:port`, campur koma/spasi
  ```bash
  sudo ./setup-edge.sh --set-rpz "10.10.10.10, 2001:db8::1"
  ```
- **`_rpz_normalize()`**: validasi + normalize input → bash array, filter IP tidak valid dengan warning
- **`_rpz_build_lua()`**: build Lua table string dari array
- **`_rpz_patch_conf()`**: patch `dnsdist.conf` via python3 (atomic, aman dari karakter IPv6 di `sed`)

#### Bugfix
- `sed -i "s|SINKHOLE_IPS={.*}|...|"` pecah saat nilai mengandung IPv6 (`:`, `[`, `]`) → seluruh operasi `SINKHOLE_IPS` kini pakai python3 atomic replace
- Semua `sed -i` pada `SINKHOLE_IPS` diganti (termasuk path upgrade, adguard mode, do_update_config)
- `--set-rpz` arg parser: join pakai koma bukan spasi (aman untuk IPv6 bare address)

#### `dnsdist.conf`
- Auto-split `_sinkhole_v4` / `_sinkhole_v6` dari `SINKHOLE_IPS` saat startup
- Query `A` → redirect ke IPv4 sinkhole saja
- Query `AAAA` → redirect ke IPv6 sinkhole jika ada, else `NXDOMAIN`
- Query `HTTPS/TXT/dll` → `NXDOMAIN` (sebelumnya lolos ke upstream)
- Tambah optional `dofile('/etc/dnsdist/safesearch.conf')` [section 6.5]
- Tambah optional `dofile('/etc/dnsdist/dotdoh.conf')` [section 1] — dikontrol panel

#### `setup-edge.sh`
- `do_check_config`: tampilkan IP Sinkhole satu per baris dengan bullet `•`
- `do_install_panel`: port `8084` → `8443` HTTPS, env vars sesuai binary baru

---

## [2.3.0] — 2026-09

### `setup-edge.sh` v2.3.0
- **`--with-panel`**: auto-download binary `dnsdist-panel` dari GitHub release + install systemd unit
- CLI options: 17 → 19 (`--set-cdb-sources`, `--with-panel`)

---

## [2.2.0] — 2026-09

### `setup-edge.sh` v2.2.0
- **`--set-cdb-sources`**: ubah daftar sumber CDB (central, mirror, peer) tanpa reinstall
- Cluster Phase 4 client-side selesai

---

## [3.1.0] — update-blacklist.sh — 2026-09

### `update-blacklist.sh` v3.1.0
- Content-addressed DB: simpan sebagai `blacklist.<sha256>.db` + symlink atomic swap
- Manifest sidecar `blacklist.db.manifest.json`
- Cluster Phase 2 selesai

---

## [2.1.0] — 2026-08

### Versi awal publik
- `setup-edge.sh` v2.1.0: 17 opsi CLI, RPZ/AdGuard mode, self-signed cert, cronjob
- `update-blacklist.sh`: ETag cache, multi-source failover, hot-reload CDB 5 detik
- `dnsdist.conf`: RPZ + AdGuard mode, blacklist CDB, top-stats module, rate limit
- `tools/trust-builder`: Go CDB generator, multi-URL, ETag, atomic replace
