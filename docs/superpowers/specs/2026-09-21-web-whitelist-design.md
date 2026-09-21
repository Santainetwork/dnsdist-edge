# Web Whitelist v2.9.0 Design

## Tujuan

Administrator Central Master mengelola whitelist domain/IP melalui panel web. Whitelist mengecualikan entri tertentu dari database CDB hasil kompilasi. Ini Fase 1 dari Central Policy Hub.

## Pendekatan

Tiga opsi dipertimbangkan:

1. **Edit file `whitelist.txt` via CLI/editor**: ditolak. Tidak ada akses web panel, operator harus SSH.
2. **Expand `sources.txt` map**: ditolak. Memaksa domain whitelist tercampur dengan URL blacklist, menyulitkan validasi dan preview.
3. **Handler `/api/master/whitelist` + card editor**: dipilih. Pola sama seperti `handleMasterSources` (GET/POST, atomic write). Builder sudah baca `whitelistFile` dan skip domain/IP whitelisted. Storage tetap plain file, tanpa DB baru.

## Konvensi Proyek yang Dipakai

Pola handler sources existing:

- `handleMasterSources`: GET baca file → `{sources: string}`; POST decode JSON, `os.MkdirAll`, `atomicWriteString`.
- Builder: `loadWhitelistSet` baca per baris, trim, skip komentar `#`, trim trailing `.`, masukkan ke map.
- Setelah build: `if _, ok := whitelist[domainPlain]; ok { countWhitelisted++; continue }`.
- Whitelist path default: `/etc/dnsdist-master/whitelist.txt`, flag `-whitelist-file`, env `PANEL_WHITELIST_FILE`.
- `atomicWriteString(path, data, mode)`: tulis `.tmp` lalu rename (atomik).

## Arsitektur

### Backend handler: `handleMasterWhitelist`

Tambahkan route `GET` dan `POST /api/master/whitelist`, ditandai auth, sama seperti `handleMasterSources`:

- `GET`: baca `*flagWhitelistFile`, kembalikan `{"whitelist": "<isi file>", "count": n}`. File belum ada → string kosong dan count 0. Error baca lain → 500.
- `POST`: body maksimum 1 MiB, decode tepat satu objek `{"whitelist": str}`. Normalisasi dan validasi; saat invalid kembalikan 400 dengan nomor baris. Duplikat dihapus secara deterministik, komentar utuh dipertahankan.
- Sebelum perubahan, salin file existing ke backup `whitelist.txt.bak` bila file tersedia. Kegagalan backup membatalkan write.
- Tulis hasil normalisasi melalui `os.MkdirAll` dan `atomicWriteString` ke file. File lama tetap utuh jika validasi atau write gagal. Respons sukses mengembalikan `ok`, `count`, dan `removed_duplicates`.
- Metode lain → 405.
- Semua route `/api/master/*` hanya didaftarkan saat `*flagMaster`; mode Edge memperoleh 404, bukan akses write policy.

### Validasi dan normalisasi

Konsisten dengan parser builder:

- Abaikan baris kosong.
- Trim spasi pada setiap baris.
- Pertahankan komentar satu baris yang diawali `#`; komentar tidak dihitung sebagai entri.
- Lowercase domain dan hapus trailing `.`; IP dinormalisasi menggunakan `net.ParseIP`.
- Domain wajib maksimum 253 karakter. Setiap label 1–63 karakter, hanya ASCII huruf/angka/tanda hubung, tanpa tanda hubung pada awal/akhir. Punycode diterima; Unicode mentah ditolak.
- Tolak wildcard, URL/path, whitespace internal, dan format hosts/AdGuard. MVP menerima domain/IP polos saja.
- Deduplikasi entri setelah normalisasi, mempertahankan urutan kemunculan pertama. Komentar tidak dideduplikasi.

Setiap posting menerima `whitelist` sebagai teks mentah. Backend mengembalikan seluruh nomor baris invalid, bukan menulis sebagian input. Whitelist berlaku exact-match sesuai builder existing; subdomain harus ditulis terpisah.

Tanggung jawab ini berada pada `normalizeWhitelist(raw string) (normalized string, invalid []string, count, duplicates int)`, yang bisa diuji unit terpisah.

### UI: card Whitelist di halaman Master

Card baru tepat setelah "Daftar URL Sumber Blacklist":

- `input#master-whitelist-search` memilih hasil berikutnya di textarea tanpa mengubah isi.
- `textarea#master-whitelist-input`.
- Hint: satu domain/IP per baris, `#` untuk komentar, exact-match.
- Counter jumlah entri serta preview ringkas `ditambah/dihapus` dibanding data terakhir dari server.
- Tombol **Simpan Whitelist**.
- Tombol **Simpan & Build CDB**, sehingga build tidak memakai perubahan yang belum tersimpan.

JS:

- `fetchMasterWhitelist()` → isi textarea dan baseline dari `d.whitelist`.
- `updateWhitelistPreview()` → hitung entri, added, removed tanpa mengklaim validasi server.
- `findWhitelistEntry()` → cari dan pilih kecocokan berikutnya di textarea.
- `saveMasterWhitelist(buildAfter)` → simpan, muat ulang hasil canonical, opsional panggil `/api/master/build` setelah save sukses.
- Jalankan `fetchMasterWhitelist()` saat navigasi ke Master, di samping `fetchMasterSources()`.

### API helper non-JSON

Helper `api()` sudah menangani respons non-JSON pada prior turn (`Respons server bukan JSON`). Pemeriksaan regression tetap dipertahankan.

## Error Handling

- File whitelist tidak ada: `GET` mengembalikan string kosong dan count 0.
- File whitelist gagal dibaca karena selain tidak ada: 500.
- Payload lebih dari 1 MiB atau JSON invalid/trailing: 400/413; file existing tidak berubah.
- Validasi gagal: 400 dengan semua nomor baris invalid; file existing tidak berubah.
- Backup atau write gagal: 500; file existing tetap menjadi policy aktif.
- Build hanya dimulai setelah save sukses; kegagalan build ditampilkan tanpa membatalkan whitelist yang sudah tersimpan.
- Mode Edge: kontrol tidak ditampilkan dan route Master tidak didaftarkan.

## Pengujian

- Unit normalisasi: domain, IPv4/IPv6, trailing dot, punycode, komentar, deduplikasi, urutan stabil, label/URL/wildcard/Unicode invalid.
- Handler GET/POST: missing file, save canonical, backup, invalid tanpa perubahan, payload terlalu besar, metode salah.
- Regression UI markup: search, textarea, preview, `saveMasterWhitelist`, `fetchMasterWhitelist`, Save & Build.
- Route registration: API Master tersedia pada mode Master dan 404 pada mode Edge.
- Builder acceptance: buat source lokal berisi domain blocked + allowed, build CDB, lalu baca CDB untuk memastikan blocked ada dan allowed whitelist tidak ada.
- Jalankan `go test ./...`, `go vet ./...`, build panel, `bash -n` untuk installer, dan smoke package.

## Versioning

- `setup-edge.sh` dan `setup-master.sh` naik ke `2.9.0` karena bundle panel ikut release.
- Tambah entri `v2.9.0` di `CHANGELOG.md`.
- Perbarui badge/versi dan `Last Updated` di `README.md`.
- Tambah API, format input, exact-match, backup, serta troubleshooting di `docs/SETUP-MASTER.md`.
- Update `docs/POLICY-HUB-ROADMAP.md`: tandai Fase 1 sebagai selesai setelah acceptance lulus.
- Buat artefak tar.gz/zip dan checksum pada release `v2.9.0` setelah seluruh acceptance lulus.

## Batasan

Tidak ada import/export, approval workflow, policy per grup, atau tab custom blacklist pada fase ini. Fase berikutnya menambah tab whitelist/blacklist/sources dan deteksi konflik. Storage tetap plain file `whitelist.txt`.