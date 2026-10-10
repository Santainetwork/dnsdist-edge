# PRD: Panel Pengendali & Monitoring Multi-DNSDist (Agent-based)

Status: **DRAFT untuk review.** Tidak ada implementasi di dokumen ini.
Basis kode: `main` @ `411cfeb`. Fondasi yang sudah ada disebut di §4.

---

## 1. Ringkasan

Satu panel pusat mengendalikan dan memonitor banyak node dnsdist. Setiap node
menjalankan **agent** ringan yang menarik konfigurasi dari panel dan melaporkan
status. Node dikelompokkan ke **profil konfigurasi** (config A, config B, dst.);
satu profil bisa dipakai banyak node, dan setiap node memilih profil lewat
penugasan.

## 2. Masalah

- Operator menjalankan beberapa edge node. Setiap node dikonfigurasi terpisah,
  sehingga perubahan kebijakan harus diulang manual per host.
- Tidak ada satu tampilan kesehatan (up/down, QPS, error) lintas node.
- Ada node yang butuh konfigurasi berbeda (mis. resolver lokal vs. kantor),
  tetapi tidak ada cara menyebut "grup ini pakai config A, grup itu config B".

## 3. Tujuan dan non-tujuan

**Tujuan**
1. Satu panel melihat semua node: status, versi, profil aktif, metrik utama.
2. Profil konfigurasi bernama (A, B, ...) dibuat sekali, ditugaskan ke banyak node.
3. Ubah profil → node yang memakai profil itu sinkron otomatis.
4. Agent di setiap node: enroll, heartbeat, tarik profil, lapor status.
5. Node tetap berfungsi bila panel mati (last-known-good config).

**Non-tujuan (v1)**
- Tidak ada multi-tenant / RBAC bertingkat. Satu admin panel.
- Tidak ada rollback otomatis lintas node.
- Tidak mengganti Central Master (`setup-master.sh`) untuk distribusi blacklist CDB.
  Blacklist tetap Mode A (dari central). Profil di sini untuk konfigurasi
  dnsdist dan SmartDist, bukan isi blacklist.

## 4. Fondasi yang sudah ada (jangan dibangun ulang)

| Komponen | Lokasi | Keterangan |
|---|---|---|
| Enroll + heartbeat node | `panel/cluster.go` (`EdgeClusterAgent`) | sudah berjalan, `initEdgeAgent` di `main.go` |
| Endpoint cluster | `panel/main.go` `/api/cluster/*` | token, register, heartbeat, nodes, config |
| Profil SmartDist (store + assign) | `panel/smartdist_profile.go` | `Put/Get/List/Delete/Assign` |
| Sync profil di edge | `panel/smartdist_agent.go` | tulis `/etc/dnsdist/smartdist-profile.lua` atomik |
| Generator Lua | `panel/smartdist_lua.go` | fail-closed untuk tipe rule tak dikenal |
| Heartbeat membawa `profile_id`/`profile_hash` | `panel/cluster.go` | sudah |

Celah yang terlihat: **belum ada** UI untuk profil/assign, **belum ada**
agent generik (hanya agent SmartDist), dan **belum ada** konsep "konfigurasi
dnsdist" selain profil SmartDist.

## 5. Pengguna

- **Operator jaringan:** mendaftarkan node, membuat profil, menugaskan, memantau.
- **Node (agent):** bukan manusia; menjalankan enroll, pull, report.

## 6. Konsep inti

| Istilah | Arti |
|---|---|
| **Node** | satu instance dnsdist + agent-nya, identitas `node_id` + `node_key` |
| **Profil** | dokumen konfigurasi bernama (`id`, `name`, `version`, `hash`, isi) |
| **Penugasan** | relasi node → profil. Satu node = satu profil aktif |
| **Agent** | proses di node: enroll, heartbeat, pull profil bila hash berubah, apply, lapor |
| **Last-known-good** | salinan profil terakhir yang berhasil diterapkan; dipakai bila panel tak terjangkau |

## 7. Kebutuhan fungsional

**F1 Node**
- F1.1 Daftar node via token enroll (sudah ada, dipertahankan).
- F1.2 Tampilkan: nama, status (online/stale/offline), versi agent, profil aktif, last heartbeat.
- F1.3 Hapus node (cabut `node_key`).

**F2 Profil**
- F2.1 CRUD profil: nama, isi konfigurasi, dan hash canonical.
- F2.2 Validasi sebelum simpan. Isi tidak valid ditolak, tidak disimpan setengah.
- F2.3 Profil dipakai node mana pun; hapus profil yang masih dipakai ditolak (kecuali paksa).
- F2.4 Ubah profil menaikkan `version` dan `hash`.

**F3 Penugasan**
- F3.1 Assign satu atau banyak node ke satu profil (mis. "node 1–5 → config A").
- F3.2 Node tanpa penugasan memakai profil default (atau tidak ada perubahan).
- F3.3 Tampilkan jumlah node per profil.

**F4 Agent**
- F4.1 Enroll sekali; simpan `node_id` dan `node_key` di file state (sudah ada).
- F4.2 Heartbeat berkala. Respons memuat `profile_id` dan `profile_hash` (sudah ada).
- F4.3 Bila hash berbeda dari hash lokal: tarik profil, validasi, tulis atomik, reload dnsdist.
- F4.4 Gagal tarik/validasi → pertahankan last-known-good, laporkan error.
- F4.5 Lapor metrik dasar: QPS, error, uptime, versi dnsdist (sumber: API dnsdist :8083).

**F5 Monitoring**
- F5.1 Dashboard ringkas: jumlah online/stale/offline, node yang error, profil tersinkron vs. belum.
- F5.2 Status sinkron per node: `synced` / `pending` / `error`.
- F5.3 Riwayat perubahan profil per node (minimal: waktu, hash lama → baru, hasil).

**F6 Keamanan**
- F6.1 Setiap node punya `node_key` sendiri; panel menolak heartbeat tanpa kunci valid.
- F6.2 Profil dan token tidak pernah dieksekusi sebagai shell. Data, bukan kode (pelajaran F17/F18).
- F6.3 Admin panel tetap di belakang `auth()` (sudah ada).

## 8. Kebutuhan non-fungsional

- **Ringan di edge:** agent satu binary Go, RAM kecil, tanpa dependensi runtime.
- **Tahan putus:** node berfungsi penuh tanpa panel; sinkron ulang saat panel kembali.
- **Konvergen:** node yang online mencapai profil yang ditugaskan dalam satu interval heartbeat (default 60 detik, bisa diatur).
- **Offline-capable UI:** tidak memakai CDN (sesuai konvensi panel).
- **Test:** tiap fitur punya tes; alur enroll → assign → sync → error-path diuji end-to-end.

## 9. Alur utama

1. Admin buat profil A dan B di panel.
2. Admin enroll node 1–3 (token) dan node 4–6.
3. Admin tugaskan node 1–3 → A, node 4–6 → B.
4. Tiap agent heartbeat, melihat hash berbeda, tarik profil, tulis atomik, reload.
5. Panel menampilkan semua node `synced`.
6. Admin ubah profil A → versi naik → node 1–3 sinkron di heartbeat berikutnya.
7. Panel mati: node tetap jalan dengan config terakhir yang sah.

## 10. Kriteria penerimaan

- [ ] Dua profil berbeda aktif bersamaan di dua kelompok node, dibuktikan lewat tes e2e.
- [ ] Ubah profil A tidak mengubah node yang memakai B.
- [ ] Profil tidak valid tidak pernah menggantikan file yang sudah berjalan.
- [ ] Panel mati → node tetap berjalan; saat panel kembali, node sinkron.
- [ ] Heartbeat tanpa `node_key` valid ditolak.
- [ ] Tidak ada input profil/token yang masuk ke shell atau `source`.
- [ ] Dashboard menampilkan status per node dan jumlah per profil.

## 11. Rencana bertahap

| Fase | Isi | Keluaran |
|---|---|---|
| **P1** | Generalisasi profil: dari profil SmartDist ke profil konfigurasi dnsdist umum | store + validasi + tes |
| **P2** | Penugasan multi-node + UI profil/assign | endpoint + halaman |
| **P3** | Agent generik: pull, validasi, tulis atomik, reload, last-known-good | `agent` tanpa SmartDist-spesifik |
| **P4** | Monitoring: status sinkron, metrik QPS/error, riwayat | dashboard + tes |
| **P5** | Hardening: rotasi `node_key`, uji putus-sambung, dokumentasi operator | runbook |

## 12. Risiko dan pertanyaan terbuka

1. **Profil SmartDist vs. profil dnsdist umum.** Apakah profil v1 hanya SmartDist, atau mencakup seluruh `dnsdist.conf`? Dampak: skala P1 berubah banyak.
2. **Reload dnsdist.** Perubahan yang butuh restart vs. hot-reload. Perlu daftar field mana yang aman di-reload.
3. **Migrasi.** Node yang sudah enroll dengan agent SmartDist harus tetap jalan saat agent generik masuk.
4. **Skala.** Berapa node maksimum yang ditargetkan? Mempengaruhi desain polling vs. push.
5. **Auth panel.** Saat ini satu admin. Perlu multi-user di v1 atau cukup v2?

## 13. Keputusan yang dibutuhkan dari Anda

1. Profil v1: hanya SmartDist, atau seluruh konfigurasi dnsdist? (rekomendasi: seluruh, dimulai dari subset yang aman di-reload)
2. Target jumlah node awal.
3. Interval heartbeat default (rekomendasi 60 detik).
