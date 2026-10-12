# Desain: Profil v1 (dnsdist.conf penuh) — PRD §13.1, P1

Status: **DRAFT untuk review.** Belum ada kode. Basis: `main` @ `03a1264`.

## 1. Keputusan yang diusulkan

| Pertanyaan | Usulan | Alasan |
|---|---|---|
| Cakupan v1 | Subset aman dari `dnsdist.conf`, bukan seluruh file | Seluruh file = Lua bebas. Subset terstruktur bisa divalidasi dan di-escape |
| Format profil | JSON terstruktur (sama seperti SmartDist), bukan Lua mentah | Data, bukan kode (PRD F6.2, pelajaran F17/F18) |
| Generator | Panel mengubah JSON → `dnsdist-profile.lua` lewat generator Go dengan `luaQuote` | Pola sudah terbukti di `smartdist_lua.go` |
| Penerapan | Tulis atomik, lalu `restartDnsdist()` bila field butuh restart | `setup/dnsdist.conf` hanya dibaca saat start |
| Fallback | Last-known-good: simpan file lama sampai file baru tervalidasi | PRD §4 "Profil tidak valid tidak pernah menggantikan file yang berjalan" |
| SmartDist | Tetap di `smartdist-profile.lua`, jadi satu bagian dari profil v1 | Tidak memecah pekerjaan yang sudah jalan (PRD §12.3 migrasi) |

## 2. Field yang masuk v1

Dipilih karena sering berbeda antar node dan aman secara tipe.

| Field JSON | Directive dnsdist | Perlu restart |
|---|---|---|
| `block_mode` | `BLOCK_MODE` (`adguard` / `sinkhole`) | ya |
| `sinkhole_v4`, `sinkhole_v6` | `SINKHOLE_IPS` | ya |
| `acl` (CIDR list) | `setACL` | tidak (lihat §5) |
| `upstreams` (nama, address, pool) | `newServer` | ya |
| `ecs_v4`, `ecs_v6` | `setECSSourcePrefixV4/V6` | ya |
| `max_qps_per_ip` | `MaxQPSIPRule` | ya |
| `tcp_max_conns`, `tcp_max_queries` | `setMaxTCPConnectionsPerClient`, `setMaxTCPQueriesPerConnection` | ya |
| `smartdist` | `smartdist-profile.lua` (sudah ada) | ya (plugin baca saat load) |

**Di luar v1 (eksplisit):** listener DoT/DoH (sudah diatur `dotdoh.conf`), TLS
cert/key, webserver credential, dnstap, CDB blacklist path, `local-block.txt`,
safesearch, top-stats. Semua itu punya file atau jalur sendiri dan tidak
dipindahkan ke profil.

## 3. Skema (ringkas)

```json
{
  "id": "config-a",
  "name": "Config A",
  "version": 3,
  "dnsdist": {
    "block_mode": "adguard",
    "sinkhole_v4": [],
    "sinkhole_v6": [],
    "acl": ["127.0.0.0/8", "10.0.0.0/8"],
    "upstreams": [{"name": "cf-primary", "address": "1.1.1.1:53", "pool": "default"}],
    "ecs_v4": 24,
    "ecs_v6": 56,
    "max_qps_per_ip": 100,
    "tcp_max_conns": 50,
    "tcp_max_queries": 100
  },
  "smartdist": {"enabled": false, "rules": []}
}
```

Validasi (fail closed, sebelum disimpan):
- `block_mode` ∈ {`adguard`, `sinkhole`}.
- IP sinkhole/ACL/address: parse `net.ParseIP` / `netip`, port 1..65535.
- `name` upstream cocok `[a-z0-9-]{1,40}`; `pool` cocok sama.
- ECS v4 ∈ 0..32, v6 ∈ 0..128. QPS, TCP limit: batas positif wajar (mis. ≤ 100000).
- Array dibatasi (mis. ≤ 64 upstream, ≤ 256 ACL).
- Field tak dikenal ditolak (`DisallowUnknownFields`, seperti SmartDist).
- Tidak ada string yang masuk Lua tanpa `luaQuote`. Angka diformat `%d`.

## 4. Alur sinkron

1. Admin simpan profil (`PUT`). Panel validasi, hitung canonical hash.
2. Heartbeat membawa `profile_id`/`profile_hash` (sudah ada).
3. Agent: GET profil, cocokkan SHA-256 body dengan hash (sudah ada untuk SmartDist).
4. Agent: generate Lua ke **file sementara**, jalankan validasi (lihat §6).
5. Lolos → `rename` atomik ke `/etc/dnsdist/dnsdist-profile.lua`, simpan hash.
6. Bila field restart berubah → `restartDnsdist()`. Gagal → catat error, hash **tidak** diperbarui, file lama? Lihat §6.

## 5. Reload: restart vs. hot-reload

Usulan: **v1 selalu restart**, dan itu sudah cukup. Dengan restart,
daftar "aman di-reload" tidak perlu ditentukan sekarang (PRD §12.2 dijawab
dengan "tidak ada hot-reload di v1"). Konsekuensi: setiap perubahan profil
memutus query sekitar 1 detik. Untuk fleet yang sering berubah, ini masalah
dan jadi kandidat v2 (hot-reload per field).

Alternatif (tidak diusulkan): hot-reload untuk ACL dan rate limit saja. Butuh
pemisahan state di Lua yang belum ada.

## 6. Keamanan file dan rollback

- Generate ke `dnsdist-profile.lua.new`, lalu uji dengan
  `dnsdist --check-config`. Bila tidak tersedia di host, tolak penerapan
  (fail closed) dan catat, bukan lewati validasi.
- Lolos uji → `rename` atomik, simpan salinan lama ke `dnsdist-profile.lua.lkg`.
- `restartDnsdist()` gagal → kembalikan dari `.lkg`, restart lagi, catat
  `rollback`. Hash **tidak** diperbarui pada kegagalan.
- File dimiliki root, mode `0644`, dan **tidak** di-chown ke service user
  (pelajaran F18).

## 7. Migrasi dan kompatibilitas

- Node tanpa profil v1 tetap jalan dengan `dnsdist.conf` lama. `dnsdist.conf`
  memuat `dnsdist-profile.lua` **hanya bila file itu ada**, sama dengan pola
  `dotdoh.conf`.
- `smartdist-profile.lua` tidak berubah format dan tetap dimuat seperti sekarang.
- Riwayat F5.3 berlaku untuk profil v1 juga (pakai `recordLocked` yang sudah ada).

## 8. Uji yang harus ada (sebelum kode dianggap selesai)

- Tes merah dulu: profil valid → Lua yang diharapkan; profil tak valid → file lama tidak disentuh.
- Injeksi: string berisi `"`, `\`, `]]`, newline, `os.execute` di setiap field
  string → hasil Lua tetap literal, tidak dieksekusi (pakai `lua` untuk verifikasi).
- Mutasi: hapus `luaQuote` pada satu field → tes gagal.
- Rollback: `restartDnsdist` dibuat gagal → file kembali ke `.lkg`.
- Idempoten: profil sama dua kali → tidak ada restart kedua.
- Tes e2e: dua profil berbeda, dua kelompok node, ubah A tidak menyentuh B (PRD §10).

## 9. Di luar scope v1 (dicatat, bukan dikerjakan)

- Hot-reload per field (v2).
- Profil untuk listener TLS dan sertifikat.
- Multi-user / RBAC (PRD §12.5).
- Diff visual antar versi profil.

## 9b. Asumsi yang belum diverifikasi

- `dnsdist --check-config` dipakai di `setup/setup-edge.sh:746`,
  `tools/dnsdist-health.sh:53`, dan `addons/dnsdist-tproxy.sh:294`. Repo
  mengandalkannya, tapi **dnsdist tidak terpasang di host pengembangan ini**
  (`which dnsdist` gagal), sehingga perilakunya belum diuji di sini.
  Uji sebelum implementasi.

## 10. Pertanyaan yang butuh jawaban Anda

1. **Setuju v1 = subset di §2, bukan seluruh `dnsdist.conf`?** Kalau seluruh file
   dibutuhkan, saya perlu tahu field mana yang wajib, karena Lua bebas tidak bisa
   divalidasi aman.
2. **Setuju v1 selalu restart** (§5), atau Anda butuh hot-reload sejak awal?
3. **`dnsdist --check-config` tersedia di semua node?** Kalau tidak, validasi
   sebelum penerapan jadi lemah.
4. Target node awal (PRD §13.2) untuk menentukan batas array dan uji skala.
