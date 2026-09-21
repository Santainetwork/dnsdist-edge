# Roadmap Central Policy Hub

Pusat pengelolaan kebijakan DNS di Central Master. Pengembangan bertahap, setiap fase memiliki versi, changelog, regression test, dan acceptance check sebelum dirilis.

## Prinsip

- `whitelist.txt` tetap menjadi storage utama pada fase awal.
- Central Master menghasilkan CDB; Edge hanya mengunduh, memverifikasi hash, lalu memasang DB.
- Perubahan policy tidak mengandung credential Edge.
- Write atomic, validasi input, backup sebelum perubahan.
- Setiap versi memperbarui binary, installer, dokumentasi, `README.md`, dan `CHANGELOG.md` sesuai dampaknya.
- Release hanya setelah `go test ./...`, build, smoke test CDB, dan acceptance flow lulus.

## Versioning

| Rilis | Scope | Status |
|---|---|---|
| `v2.8.0` | Baseline panel, builder, publisher, cluster | Selesai |
| `v2.9.0` | Web Whitelist MVP | Selesai setelah v2.9.0 acceptance |
| `v2.10.0` | Policy Management: whitelist, blacklist, sources | Rencana |
| `v2.11.0` | Versioning dan Audit | Rencana |
| `v2.12.0` | Policy per grup Edge dan distribusi | Rencana |
| `v3.0.0` | Intelligence dan approval workflow | Rencana |

Patch release (`v2.x.y`) hanya untuk bugfix, security fix, atau dokumentasi tanpa perubahan fitur. Minor release menambah fitur kompatibel. Major release mengubah kontrak policy, API, atau storage secara tidak kompatibel.

## Fase 1 — `v2.9.0`: Web Whitelist MVP

### Fitur

- Card **Whitelist** pada halaman Central Master.
- Editor domain multiline dengan pencarian dan jumlah entri.
- GET/POST `/api/master/whitelist`.
- Validasi domain, normalisasi, deduplikasi, komentar aman.
- Write atomic dan backup sebelum penyimpanan.
- Preview perubahan.
- Tombol **Simpan** dan **Build CDB**.
- Pesan error HTTP yang jelas, termasuk respons non-JSON.

### Acceptance

- Domain valid tersimpan dan muncul setelah reload.
- Domain duplikat tidak menggandakan data.
- Input invalid ditolak tanpa merusak file lama.
- Build tidak memasukkan domain whitelist ke CDB.
- Mode Edge tidak menampilkan kontrol Master.
- Test API, builder, UI markup, `go test ./...`, build panel lulus.

### Dokumentasi release

- Tambah `v2.9.0` ke `CHANGELOG.md`.
- Perbarui badge/versi pada `README.md`.
- Tambah API dan troubleshooting ke `docs/SETUP-MASTER.md`.
- Catat test dan artefak release.

## Fase 2 — `v2.10.0`: Policy Management

- Tab terpisah: Whitelist, Custom Blacklist, Sources.
- Import/export TXT.
- Preview diff sebelum commit.
- Deteksi konflik whitelist vs blacklist.
- Build policy dari satu snapshot konsisten.
- Changelog berisi format file dan perubahan API.

## Fase 3 — `v2.11.0`: Versioning dan Audit

- Revision integer dan hash policy.
- Riwayat perubahan: operator, waktu, diff, hasil build.
- Rollback satu klik.
- Backup otomatis sebelum commit.
- Manifest CDB mencatat revision dan policy hash.
- API audit read-only untuk observability.

## Fase 4 — `v2.12.0`: Cluster Policy

- Policy global, grup, dan node.
- Inheritance global → grup → node.
- Preview effective policy per node.
- Manifest bertanda revision/hash.
- Status node: up-to-date, behind, failed.
- Distribusi tetap memakai verifikasi SHA256 dan atomic symlink swap.

## Fase 5 — `v3.0.0`: Policy Intelligence

- Pencarian alasan domain diblokir.
- Temporary whitelist dengan expiry.
- Approval workflow untuk perubahan sensitif.
- Statistik whitelist hit dan false-positive.
- Rekomendasi domain berdasarkan telemetry tanpa mengirim credential.
- Major release hanya setelah kontrak API dan migrasi storage didokumentasikan.

## Checklist setiap versi

1. Tetapkan scope dan kontrak API.
2. Tambah regression test sebelum implementasi.
3. Implementasikan perubahan minimum.
4. Jalankan test, build, smoke, dan acceptance.
5. Perbarui `CHANGELOG.md`, README, dan dokumentasi terkait.
6. Sinkronkan versi binary/installer bila terdampak.
7. Buat artefak release dan checksum.
8. Tag versi setelah semua pemeriksaan lulus.
