# Kebijakan Keamanan (Security Policy)

## Versi yang Didukung

| Versi | Didukung |
|-------|----------|
| 2.x (main) | ✅ Aktif |
| 1.x | ❌ End-of-life |

## Melaporkan Kerentanan

Jika Anda menemukan kerentanan keamanan pada project ini, **jangan buka public issue**. Laporkan langsung:

- **GitHub Security Advisory**: buka tab *Security* pada repositori, pilih *Report a vulnerability*
- **Email** (jika tersedia): gunakan alamat yang tertera pada profile maintainer

Mohon sertakan detail berikut agar mempercepat penanganan:

1. Versi yang terpengaruh (misal `setup-edge.sh` v2.1.0)
2. Deskripsi kerentanan dan dampaknya
3. Langkah reproduksi (proof of concept)
4. Saran mitigasi (opsional)

Tim akan merespons dalam **5 hari kerja**, dan penanganan diumumkan setelah perbaikan dirilis.

## Area yang Peka terhadap Keamanan

Project ini mengelola resolver DNS publik dan database blokir. Hal-hal berikut wajib diperhatikan:

### Kredensial (WAJIB diubah)
Default berikut **harus diganti** saat instalasi production:
- Password Web Console: `trust-ng-admin`
- API Key: `trust-ng-apikey-changeme`

Ubah dengan:
```bash
sudo ./setup-edge.sh --password 'SandiBaru' --apikey 'KunciBaru' --set-webserver
```

### Jangan Commit
File berikut **tidak boleh** di-commit ke git (sudah di `.gitignore`):
- `*.key` (private key sertifikat)
- `blacklist.db`, `*.db`, `*.bin`
- `ipinfo_lite.csv` / `*.csv`
- File rahasia apapun (`config/secrets.*`)

### Sertifikat TLS
- Jangan gunakan self-signed di production publik
- Gunakan Let's Encrypt (`--set-cert 2`) dan perbarui otomatis via certbot
- Jaga permission private key: `chmod 600 certs/server.key`

### Jaringan
- Batasi akses port 8083 (web console/API) hanya ke jaringan administrasi
- Jika public resolver: selalu aktifkan DoT/DoH, nonaktifkan plaintext DNS bila memungkinkan

## Tanda-tanda Kompromi (Indikator)

Perhatikan hal berikut sebagai potensi indikasi penyusupan:
- QPS (queries/second) melonjak tak wajar
- Perubahan `dnsdist.conf` atau `update-blacklist.sh` tanpa otorisasi
- File `blacklist.db` berukuran mengecil drastis
- Log berisi query ke domain tidak dikenal secara massal

## Proses Pelaporan & Pemberitahuan

1. Konfirmasi kerentanan oleh maintainer
2. Perbaikan dirilis di branch `main` (minor/patch)
3. Changelog diperbarui di `CHANGELOG.md`
4. Pengumuman keamanan dirilis setelah patch tersedia

---

*Terakhir diperbarui: 2026-08-31*
