# Changelog — DNSDist Edge Node (Trust-NG)

Semua perubahan signifikan dicatat di sini.
Format: [versi] — tanggal, deskripsi singkat.

---

## [3.3.0] — 2026-10-10

### Keamanan — node.conf dibaca sebagai DATA, bukan dieksekusi sebagai root (F17/F18)
- **Perubahan perilaku:** `setup-edge.sh` (4 titik) dan `update-blacklist.sh` tidak lagi `source`/`.` `node.conf`. Nilai dibaca oleh `_load_node_conf` sebagai data: hanya baris `SAVED_KEY="..."` yang diambil, `$(...)`/backtick/`;` tidak dieksekusi.
- **Penulisan:** `save_config` meng-escape setiap nilai `SAVED_*` dengan `_sq_escape` (termasuk `SAVED_VERSION` dan `SAVED_INSTALL_DATE`, yang sebelumnya belum). `do_set_cdb_sources` hanya memanggil `save_config` (jalur sed/echo dihapus).
- **Dampak operator:** Instalasi lama dengan `node.conf` format sebelumnya tetap terbaca (diuji: nilai dan urutan sama). Tidak perlu tindakan manual.
- **Tes:** `tests/setup-edge-node-conf-data.sh` dan `tests/setup-edge-node-conf-injection.sh` menjalankan `save_config`/`do_set_cdb_sources`/`_load_node_conf` asli dengan 15 payload hostile (tidak mengeksekusi) dan nilai legitimate (utuh). Mutasi: menghapus escape atau mengembalikan `source` membuat tes gagal.

### Panel — binary dibangun ulang
- `panel/dnsdist-panel` dan `tools/dnsdist-panel` dibangun ulang dari sumber HEAD dengan `panel/build.sh`. Binary lama tidak cocok dengan sumber. Kedua binary identik (sha256 `3baa51b2`).
- Konten: JWT exp wajib numerik (F3), tes SmartDist profile List/GET, handler error SmartDist (405/400/404), dan escape byte kontrol di literal Lua (`luaQuote`).

### SmartDist (fitur baru sejak v3.2.0)
- Panel master menyimpan profil per node, node menariknya via heartbeat, plugin dnsdist menerapkan on/off dan daftar aturan. Profil dibaca sebagai teks (tidak dieksekusi); profil dengan hash tidak cocok ditolak (fail closed).
- **Catatan produksi:** flag on/off berlaku langsung; perubahan daftar aturan memerlukan restart dnsdist. Plugin membaca file profil di setiap respons.

### Perbaikan lain sejak v3.2.0
- F15: password webserver di-escape untuk literal Lua dan sed.
- F17c: `trim_blocks` Ansible tidak lagi menggabungkan baris `SAVED_*`.
- F3: verifikasi JWT menolak `exp` yang tidak numerik.
- Tes dan CI: tes setup dan self-update.

### Diketahui belum selesai
- `task-007`: `clientIP` mempercayai `X-Forwarded-For` (perlu keputusan trusted-proxy).
- `tools/test-setup-contracts.sh` punya kontrak yang perlu diselaraskan dengan kode saat ini (lihat riwayat).
- `scratch/` pada host pengembangan terbuka ke LAN (F22); bukan bagian rilis.

---

## [3.2.0] — 2026-10-05

### Master CDB Publisher — Edge Mengunduh, Bukan Membangun
- **Rute publisher:** Panel Master kini melayani `GET /cdb/manifest.json` (version, sha256 nyata, size, `built_at`), `GET /cdb/blacklist.db` (CDB aktif), `GET /cdb/healthz` (200 bila DB terbaca, 500 bila tidak), dan `GET /cdb/sources.json` (daftar sumber terurut yang diiklankan ke edge). Sebelumnya roadmap sempat mengklaim tahap ini selesai padahal rutenya belum ada di kode.
- **Autentikasi `X-CDB-Token`:** Shared secret via flag `--cdb-token` / env `PANEL_CDB_TOKEN`, sengaja bukan JWT panel karena edge tidak memegang sesi panel. Nama file wajib lolos whitelist ber-hash (`trust.db`, `blacklist.db`, atau `trust|blacklist.<sha256>.db`), lalu path di-`EvalSymlinks` dan dicek ulang agar tetap berada di dalam `--files-dir` — path traversal dan symlink escape ditolak `403`.
- **Edge:** `setup/update-blacklist.sh` mengirim `X-CDB-Token` pada probe `If-Modified-Since` (curl) maupun unduhan (aria2c), membaca `SAVED_CDB_TOKEN` dari `node.conf`. Tanpa ini edge yang diarahkan ke publisher tidak akan pernah bisa autentikasi, sehingga jalur preferensi Master tak bisa berjalan tanpa awasi.
- **Test:** `TestCDBManifestSuccess`, `TestCDBManifestUsesContentAddressedHash`, `TestCDBPublisherRouteRegistration`, `TestCDBBlacklistServesActiveFile`, `TestCDBPathTraversalRejected`, `TestCDBSymlinkEscapeRejected`, `TestCDBUnknownFileRejected`, `TestCDBSourcesPrefersMasterThenFallbacks`, `TestCDBSourcesRejectsNonHTTPSchemes`.

### CDB Download Throttle — Bootstrap Tetap Bisa, Flood Dicegah
- **Kebijakan:** Edge tanpa token tetap boleh mengunduh untuk bootstrap, tetapi endpoint tidak boleh dipakai membanjiri Master. Token `X-CDB-Token` yang valid = trusted dan tidak pernah di-throttle (fast path); tanpa/salah token = tetap dilayani namun token-bucket per IP (burst 3, lalu satu permintaan per 30 detik) → `429` + `Retry-After` saat terlampaui.
- **Observability & batas memori:** Respons membawa `X-CDB-Untrusted: 1` agar operator dapat mengenali edge salah konfigurasi dari log. Token yang valid menghapus bucket IP tersebut (edge tidak dihukum karena trafik anonim sebelumnya), dan jumlah IP yang dilacak dibatasi 4096 dengan eviksi oldest-seen agar flood sumber palsu tidak menumbuhkan peta tanpa batas.
- **Test:** `TestCDBUntrustedIsAllowedButThrottled`, `TestCDBValidTokenBypassesThrottle`, `TestCDBWrongTokenIsThrottledLikeUntrusted`, `TestCDBNoTokenAllowsAccess`.

### dnstap Streaming Wiring + Generator CDB Go
- **dnstap dnsdist:** Blok opt-in `[5.7]` di `setup/dnsdist.conf` mendaftarkan `DnstapLogAction` pada rule blacklist hanya bila `DNSDIST_DNSTAP_ADDR` (atau `PANEL_DNSTAP_ADDR`) diset, jadi tetap mati secara default. Ditempatkan sebelum action terminal mana pun karena action terminal akan mencegah record terekam.
- **UI panel:** Halaman **dnstap Analytics** (nav + tabel + petunjuk aktivasi) kini mengonsumsi `/api/dnstap/top` yang sebelumnya ada tetapi tidak terpakai. Catatan privasi eksplisit: panel membuang client IP seketika saat decode.
- **Generator CDB Go:** `tools/gen-cdb-go` menulis key DNS **wire format**, selaras `tools/trust-builder` dan `KeyValueLookupKeyQName(true)` dnsdist. (Skrip Python lama menulis key plain-text sehingga CDB-nya diam-diam tidak memblokir apa pun; lihat perbaikan wire-format di bawah.)
- **Test:** `TestDnstapServerStreamingAndPrivacy`, `TestHandleDnstapTopEndpoint`, `TestTopBlockedIsReadOnly`, serta suite `tools/gen-cdb-go/main_test.go`.

### Dashboard — Service Health Metric Tiles
- **Kesehatan Layanan:** Ditambahkan baris kartu metrik ringkasan di atas daftar status (kv-list) mencakup jumlah resolver upstream terkonfigurasi, mode pemblokiran RPZ aktif (`NXDOMAIN`, `REFUSED`, dsb.), serta peran klaster (`Master`/`Edge`) lengkap dengan jumlah total node terdaftar.
- **Fail-closed & Provenance:** Data diambil dari endpoint terautentikasi (`/api/upstream/status`, `/api/rpz/status`, `/api/cluster/tokens`), membedakan nilai 0 valid dari kegagalan probe, dan menampilkan tanda fallback `—` tanpa memalsukan status kesehatan resolver atau node.
- **Perbaikan lanjutan:** Hitungan 0 node yang valid kini ditampilkan eksplisit (`0 node terdaftar`), sedangkan di Edge jumlah node lokal ditampilkan `—` (tidak tersedia) alih-alih angka nol yang menyesatkan; penjagaan `typeof is_master === 'boolean'` dan `Number.isInteger(nodes)` mempertahankan perilaku fail-closed.
- **Test:** `TestDashboardHealthMetricsMarkup`.

### CDB Wire-Format & Parity Fix
- **gen-cdb.py:** Kini menulis DNS wire-format dengan bucket selection `h & 0xFF` dan probing `(h>>8) % slotCount`, kompatibel dengan dnsdist `KeyValueLookupKeyQName(true)` dan `tools/gen-cdb-go`.
- **Panel reader:** `cdbContainsDomain` serta test fixture `writeTestCDB` kini mengikuti CDB lookup semantics. `/api/rpz/test` melaporkan keputusan blokir yang sama dengan dnsdist. (Sebelumnya tester RPZ selalu menjawab `TIDAK DITEMUKAN` karena meng-hash key plain-text, bukan wire format.)
- **Regression guard:** `TestGenCDBPyWritesWireKeys` dan `TestPanelCDBAgreesWithDnsdist` melindungi format serta parity lookup; `TestCDBWireFormatKeys`/`TestWireDomainKeyEncoding` memaku encoding wire.
- **Collision handling:** Generator Python kini mempertahankan record berbeda yang memiliki hash CDB 32-bit identik (`TestGenCDBPyPreservesDistinctKeysWithHashCollision`); sebelumnya record dengan hash sama saling menimpa. Kasus ini juga diverifikasi manual dengan 10 nama domain dalam satu bucket memakai reader `github.com/colinmarc/cdb`.

### Test & CI
- Smoke test CDB di CI diselaraskan ke spec `cr.yp.to/cdb/cdb.txt` (bucket `h & 0xFF`, field kedua = jumlah slot, probe mulai `(h>>8) % slotCount`, key DNS wire format) setelah sebelumnya mengunci layout lama yang keliru; versi baru juga menolak output generator lama (CI-only).

### Rilis
- Panel, UI, installer Edge/Master (`setup-edge.sh`, `setup-master.sh`), dan `update-blacklist.sh` disinkronkan ke v3.2.0; binary `dnsdist-panel` di `tools/` dan `panel/` dibangun ulang dari sumber yang sama.
- `rpz-master` kini ikut dipublikasikan sebagai aset rilis, sehingga `setup-master.sh` dapat memakai jalur `verified-release` (unduh + verifikasi `SHA256SUMS`) di samping jalur `bundled` dari tarball.

### Catatan validasi
- Lulus: `gofmt` bersih, `go vet` bersih, `go test` panel lulus (termasuk `-race`), `go test` di `tools/gen-cdb-go` lulus, dan suite shell `tests/*.sh` 5/5 lulus (`dnsdist-panel-contracts.sh`, `setup-edge-url.sh`, `setup-master-modes.sh`, `smartdns-plugin-contracts.sh`, `update-blacklist-cleanup.sh`).
- Belum diverifikasi pada rilis ini: integrasi Postgres live (audit DB), alur deployment/upgrade produksi, DNS eksternal nyata, Trust+ blockpage end-to-end di host nginx, serta rendering piksel kartu kesehatan di peramban.
- Catatan kejujuran: klaim validasi live publisher/throttle di atas HTTPS+token berasal dari pesan commit; berkas bukti `validation/*.md` yang dirujuk tidak ada di dalam repo ini.

---

---

## [3.2.0] — update-blacklist.sh — 2026-10-05

- Dukungan `X-CDB-Token` untuk mengunduh dari master CDB publisher: header dikirim pada probe `If-Modified-Since` (curl) dan unduhan (aria2c).
- Token dibaca dari `SAVED_CDB_TOKEN` di `node.conf`; bila kosong, tidak ada header (perilaku lama tetap).
- Selaras dengan rute panel `/cdb/*` (`--cdb-token` / `PANEL_CDB_TOKEN`).

---

## [3.1.0] — 2026-10-04

### Panel — PostgreSQL Cluster Storage (Central Master)
- Adapter `PostgresClusterStore` pure-Go via `pgx/v5` stdlib (tanpa CGO): skema `cluster_tokens` + `cluster_nodes` dibuat otomatis, kontrak method 1:1 dengan engine JSON/SQLite (token single-use, status dinamis, `Key` tidak bocor di `ListNodes`).
- Seleksi backend: `--database-url` / `PANEL_DATABASE_URL` (atau komponen `PANEL_DB_HOST/PORT/USER/PASSWORD/NAME`), atau `postgres://` di `--cluster-nodes-file`.
- Versi panel/UI/installer naik ke 3.1.0; test `pgstore_test.go` (lifecycle live bila `PANEL_TEST_DATABASE_URL` diset, `TestPostgresDSNFromEnv` selalu jalan).

### Panel — Obsidian Telemetry UI & endpoint status read-only
- Tampilan Dashboard, RPZ, Upstream, dan Cluster dibangun ulang memakai design system Obsidian Telemetry. Panel tetap 100% offline (tanpa CDN/Tailwind runtime).
- Endpoint read-only baru: `GET /api/rpz/status`, `/api/rpz/test`, `/api/upstream/status`, `/api/dnstap/status`, `/api/cluster/tokens`, dan liveness publik `/api/health`. Semua endpoint data tetap di balik autentikasi (401 tanpa token).
- **Perbaikan integritas data:** `/api/upstream/status` tidak lagi mengklaim `healthy: true` untuk setiap resolver tanpa probe apa pun, dan `/api/rpz/status` tidak lagi mengklaim `active: true` untuk setiap feed. Nilai diganti string jujur (`unknown`/`inactive`/`configured`) lengkap dengan provenance (`health_source`, `health_note`, `probe_support: false`). Ini mengoreksi badge hijau "UP/Aktif" yang sebelumnya tidak berdasar.
- `GET /api/rpz/test` melakukan pencarian CDB nyata dengan hash DJB yang sama seperti `tools/gen-cdb.py`.

### Panel — Perbaikan CSS
- Menghapus lima kelas CSS mati pasca-rebuild (`chart-card`, `chart-header`, `chart-title`, `node-table`, `node-table-wrap`).
- Memperbaiki `.modal-card` yang mereferensikan tiga custom property tak terdefinisi (`--bg-card`, `--radius-md`, `--shadow-modal`) sehingga setiap modal tampil transparan, bersudut siku, dan tanpa bayangan.

### Addon — SmartDNS plugin
- Guard input pada semua entry point publik (`smartdns_ip_set`, `smartdns_domain_set`, `smartdns_cname`, `smartdns_ip_rules_alias`) agar konfigurasi tidak valid dilewati dengan `errlog` alih-alih menghentikan pemuatan config dnsdist.
- Alias `ip-rules` mengisi setiap record yang cocok dari daftar target secara round-robin; mode speedcheck `first-ping` dipisahkan dari `fastest-response`; `SPEEDCHECK_SKIP` tidak lagi menulis ulang dari cache.

### Test
- `tests/smartdns-plugin-contracts.sh` dan `tests/dnsdist-panel-contracts.sh` sebagai contract guard (permukaan API addon, registrasi endpoint panel, jaminan offline, integritas variabel CSS).
- Test CDB memvalidasi terhadap output nyata `tools/gen-cdb.py`, termasuk penolakan false-positive dan input terpotong.
- Test end-to-end menyajikan UI via HTTP dan memastikan layout terkirim, tetap offline, serta endpoint baru memerlukan autentikasi.

### Catatan validasi
- Lulus: `gofmt`, `go vet`, `go test` (termasuk `-race`), suite shell `tests/*.sh` 5/5, matriks dnsdist terisolasi `ALL_DNSDIST_CHECKS_PASSED`, serta verifikasi live binary di atas HTTPS+JWT.
- Belum diverifikasi pada rilis ini: integrasi Postgres live (audit DB), alur deployment/upgrade produksi, DNS eksternal nyata, dan Trust+ blockpage end-to-end di host nginx.

## [3.0.0] — 2026-10-02

### Panel — Multi-Database Cluster Storage
- Abstraksi `ClusterStorage` dengan backend SQLite pure-Go (`modernc.org/sqlite`, zero CGO, mode WAL); `ClusterStore` JSON lama tetap 100% kompatibel.
- Token enroll single-use dengan TTL, status node dinamis, migrasi skema otomatis.

### Panel — Streaming Analitik dnstap (zero-client-IP)
- Decoder framestream TCP/UNIX socket pure-Go, agregator bounded-memory dengan bucket overflow `_other_`.
- Client IP dibuang seketika saat parsing (kepatuhan UU PDP); endpoint `GET /api/dnstap/top`; flag `--dnstap-addr`.

### Panel — Auth Hardening & Trust+ Blockpage (Opsional)
- Password panel kini PBKDF2 (`pbkdf2$...`) dengan migrasi plaintext saat login; `/api/settings` hash password baru dan tolak password <8 karakter.
- Rate limiter login per-IP (5 gagal → kunci 15 menit, 429 + `Retry-After`); JWT tambah `iat/nbf/jti`.
- Halaman blokir Trust+ opsional: default `Akses Diblokir` dengan placeholder `{{domain}}` (strip port + HTML-escape); API `GET/POST/DELETE /api/blockpage` dukung JSON `{html}` dan multipart `page`, limit 1 MiB.
- Listener panel opt-in via `-blockpage-addr` / `PANEL_BLOCKPAGE_ADDR` (default mati agar tidak bentrok :80); tambah `-blockpage-webroot` / `PANEL_BLOCKPAGE_WEBROOT` untuk mirror upload ke nginx; reset tulis default ke mirror.
- Installer: default `WITH_BLOCKPAGE=false`; flag `--with-blockpage` (nginx :80 + mirror `/var/www/html/index.html`, sinkhole otomatis ke IP node) dan `--blockpage-addr <ADDR>`; nilai tersimpan `SAVED_BLOCKPAGE_*` di `node.conf` dan dihormati kecuali flag eksplisit diberikan; nginx hanya dipasang bila diminta dan mode bukan adguard.
- UI Settings: kartu Trust+ (badge aktif/off, info target listen/webroot, upload file, reset), tombol login busy state + pesan lockout dari header `Retry-After`, token gaya netral, login grid/glow.

## [2.9.1] — 2026-09-29

### Central Master Multimode
- Menambahkan source mode `feeds` (default kompatibel) dan `rpz-slave`, terpisah dari pilihan resolver `--with-dnsdist` / `--no-dnsdist`.
- Mengintegrasikan daemon `rpz-master` untuk sinkronisasi AXFR/IXFR upstream, kompilasi CDB atomik, endpoint `/files/trust.db`, serta transfer downstream terbatas ACL/TSIG.
- Menetapkan satu writer CDB per source mode: cron/panel builder untuk `feeds`, service `rpz-master` untuk `rpz-slave`.
- Menambahkan hardening konfigurasi, serialisasi sync/build, graceful shutdown, dan alias CLI `-version`.
- Memperbaiki instalasi Edge agar `--url` diteruskan melalui `CENTRAL_DB_URLS` pada sinkronisasi pertama.
- Menambahkan test Go race/vet/build, test kontrak installer multimode, test URL Edge, dan CI terkait.
- Menyamakan versi panel/UI/telemetri ke 2.9.0, menyelaraskan dokumentasi port RPZ `:5354` dan path konfigurasi `/etc/dnsdist-master/`, serta membuat build binary panel statik dan reproducible.

---

## [2.9.0] — 2026-09-21

### Web Whitelist Master
- Menambahkan editor whitelist terautentikasi di panel Central Master dengan pencarian, jumlah entri, preview perubahan, serta alur Simpan dan Simpan & Build CDB.
- Menambahkan normalisasi domain/IP exact-match, pelaporan baris invalid, deduplikasi, batas payload 1 MiB, backup `whitelist.txt.bak`, dan atomic write.
- Menambahkan acceptance test yang memastikan whitelist dikecualikan dari CDB serta route Master tetap 404 pada Edge.

### Installer
- `setup-edge.sh --upgrade` dan `setup-master.sh --upgrade` mengunduh installer terbaru dari GitHub ke file sementara, memvalidasi sintaks serta checksum SHA-256 dari `SHA256SUMS`, lalu mengganti installer secara atomik dan melanjutkan proses upgrade lewat `exec`.
- Kegagalan unduhan, sintaks, atau checksum tidak mengubah installer, konfigurasi, maupun data yang aktif.
- Mengganti template source Master yang sudah tidak tersedia dengan AdGuard DNS Filter publik yang aktif.
- Perintah transparent DNS standalone berhenti setelah plan/apply sehingga tidak memicu instalasi panel default.
- Status policy route, ownership file Lua, reapply, cleanup, dan counter nftables TPROXY diperketat berdasarkan acceptance network namespace.

### Dokumentasi
- Memperbarui panduan Master, referensi upgrade, README, dan roadmap Central Policy Hub.

## [2.8.0] — 2026-09-21

### Transparent DNS E2
- Menambahkan proxy TPROXY IPv4 UDP/TCP port 53 dengan PROXY protocol v2 ke listener dnsdist terisolasi.
- Menambahkan mode `off`, diagnostik read-only `auto`, dan `tproxy` opt-in dengan `--apply-transparent`.
- Rule nftables, policy route, systemd unit, dan rollback hanya mengelola objek milik Trust-NG.
- Menambahkan panduan verifikasi force-DNS MikroTik tanpa mengklaim dapat membaca konfigurasi router dari Edge.

### Panel
- Installer kini menulis password panel ke `/var/lib/dnsdist/panel.password` dan mempertahankan password existing kecuali `--password` diberikan eksplisit.

### Validation
- Unit test Proxy v2/OOB, mocked installer/helper, build Go, dan acceptance TPROXY UDP/TCP dalam network namespace lulus.
- Memperbaiki dispatch `-f` / `--force-update` agar langsung sinkronisasi database, bukan memasang ulang panel.

## [2.7.0] — 2026-09-20

### 🛠️ Edge Settings UI
- Memulihkan struktur card halaman Pengaturan Edge setelah penambahan koneksi Central Master.
- Memisahkan card enrollment, mode pemblokiran, sandi panel, dan informasi sistem agar layout kembali rapi.
- Menambahkan regression test untuk memastikan kontrol Settings tetap berada di card yang benar.

### ✅ Validation
- Panel HTTP, login, API terproteksi, dan urutan card Settings lulus acceptance.
- Test Go, build panel, validasi shell, dan package check lulus.

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
