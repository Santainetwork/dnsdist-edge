# 🛠️ Dokumentasi CLI `setup-edge.sh`

`setup-edge.sh` adalah skrip instalasi, konfigurasi, sinkronisasi, dan manajemen operasional otomatis untuk **DNSDist Edge Node (Trust-NG)** pada sistem operasi turunan Debian/Ubuntu.

---

## 📊 Ringkasan Total Perintah & Opsi (19 Opsi CLI)

Skrip `setup-edge.sh` mendukung **19 opsi baris perintah (CLI options)** yang dikelompokkan ke dalam beberapa kategori operasi:

| No | Opsi / Flag | Argumen | Kategori | Deskripsi |
|:---:|---|---|---|---|
| 1 | `-i`, `--install` | - | **Instalasi** | Menginstal DNSDist, dependensi, sertifikat TLS, modul stats, cronjob, dan menyetel konfigurasi awal. |
| 2 | `-u`, `--url` | `<URL>` | **Konfigurasi DB** | Menentukan URL Central Manager untuk sinkronisasi file `blacklist.db`. |
| 3 | `-s`, `--sync-only` | - | **Sinkronisasi** | Menjalankan sinkronisasi database secara manual satu kali (memanfaatkan cache header). |
| 4 | `-f`, `--force-update` | - | **Sinkronisasi** | Menjalankan sinkronisasi database manual dengan mengabaikan cache lokal (`If-Modified-Since`). |
| 5 | `-c`, `--check-config` | - | **Diagnostik** | Memeriksa versi, mode blokir, IP sinkhole, upstream, status file DB, validitas syntax `dnsdist.conf`, dan status systemd. |
| 6 | `--update-config` | - | **Konfigurasi** | Membuka wizard interaktif untuk mengubah Mode Blokir (AdGuard / RPZ) dan IP Upstream DNS. |
| 7 | `--upgrade` | - | **Pemeliharaan** | Mengupgrade konfigurasi dan skrip ke versi terbaru dengan migrasi otomatis tanpa menghapus setelan lama. |
| 8 | `--set-upstream` | `<IP1, IP2...>` | **Konfigurasi** | Mengubah daftar IP Upstream DNS resolver tanpa perlu menginstal ulang node. |
| 9 | `--set-rpz` | `<IP1, IP2...>` | **Konfigurasi** | Mengubah IP Sinkhole target pemblokiran RPZ pada `dnsdist.conf`. |
| 10 | `--set-cert` | `<1\|2\|3>` | **Sertifikat** | Memilih mode sertifikat: `1` (Self-Signed), `2` (Let's Encrypt), `3` (Disable DoT/DoH / Plaintext only). |
| 11 | `--cert-domain` | `<domain>` | **Sertifikat** | Domain untuk request sertifikat Let's Encrypt (DoT/DoH). |
| 12 | `--cert-email` | `<email>` | **Sertifikat** | Alamat email pendaftaran sertifikat Let's Encrypt untuk notifikasi expiry. |
| 13 | `--password` | `<PWD>` | **Web Console** | Menyetel password login untuk Web Console DNSDist & API (Port 8083). |
| 14 | `--apikey` | `<KEY>` | **Web Console** | Menyetel API Key untuk autentikasi endpoint REST API (`X-API-Key`). |
| 15 | `--set-webserver` | - | **Web Console** | Menerapkan perubahan password dan API Key baru ke `dnsdist.conf` dan merestart service. |
| 16 | `--uninstall` | - | **Sistem** | Menghapus seluruh instalasi DNSDist, direktori `/etc/dnsdist`, `/var/lib/dnsdist`, dan cronjob terkait. |
| 17 | `--set-cdb-sources` | `<URL1, URL2...>` | **Cluster** | Mengubah daftar sumber CDB (central, mirror, peer) dipisah koma → `SAVED_CDB_SOURCES` di node.conf |
| 18 | `--with-panel` | - | **Panel** | Auto-install DNSDist Panel (download binary release + systemd unit) |
| 19 | `-V`, `--version` / `-h`, `--help` | - | **Informasi** | Menampilkan versi skrip atau menu bantuan CLI. |

---

## 💻 Panduan Penggunaan & Contoh Perintah

Semua perintah di bawah ini harus dijalankan dengan hak akses root (`sudo` atau user `root`).

### 1. Instalasi Node Baru

#### Instalasi Standar (Interaktif)
```bash
sudo ./setup-edge.sh --install
```

#### Instalasi Non-Interaktif / Otomatis dengan Central Manager URL
```bash
sudo ./setup-edge.sh --install --url "http://central-manager.local:8080/files/trust.db"
```

#### Instalasi Lengkap dengan Kredensial Web & Mode Sertifikat
```bash
sudo ./setup-edge.sh --install \
  --url "https://central-db.domain.id/trust.db" \
  --password "AdminSuperRahasia123" \
  --apikey "api-key-edge-node-01" \
  --set-cert "1"
```

---

### 2. Manajemen Sinkronisasi Database Blacklist

#### Sinkronisasi Manual (Normal)
```bash
sudo ./setup-edge.sh --sync-only
```

#### Paksa Download Database Baru (Bypass Cache)
```bash
sudo ./setup-edge.sh --force-update
```

---

### 3. Modifikasi Konfigurasi DNS & Upstream Tanpa Reinstall

#### Mengubah Upstream Resolver (IPv4 & IPv6 didukung)
```bash
sudo ./setup-edge.sh --set-upstream "1.1.1.1, 8.8.8.8, 9.9.9.9"
```

#### Mengubah IP Target Sinkhole (RPZ Mode)
```bash
sudo ./setup-edge.sh --set-rpz "10.10.10.10, 10.10.10.11"
```

#### Mengubah Password & API Key Webserver
```bash
sudo ./setup-edge.sh --password "SandiBaru" --apikey "ApiKeyBaru" --set-webserver
```

#### Update Konfigurasi Interaktif (Wizard CLI)
```bash
sudo ./setup-edge.sh --update-config
```

---

### 4. Pengecekan & Diagnostik Sistem

#### Cek Status & Validitas Syntax Konfigurasi
```bash
sudo ./setup-edge.sh --check-config
```

Output mencakup:
- Versi script & versi terpasang
- Mode pemblokiran (`rpz` / `adguard`)
- IP Sinkhole & Upstream DNS aktif
- Lokasi & ukuran file `blacklist.db`
- Validasi syntax DNSDist (`dnsdist --check-config`)
- Status service systemd (`dnsdist` & `nginx`)

---

### 5. Upgrade & Pemeliharaan

#### Upgrade Node ke Versi Terbaru
```bash
sudo ./setup-edge.sh --upgrade
```
*Proses ini secara otomatis membuat backup konfigurasi lama, memperbarui skrip `update-blacklist.sh` & `dnsdist.conf`, menjalankan migrasi path/format jika diperlukan, dan me-restart service secara aman.*

---

### 6. Menghapus Instalasi (Uninstall)

```bash
sudo ./setup-edge.sh --uninstall
```
*Akan meminta konfirmasi sebelum menghapus paket DNSDist, file konfigurasi di `/etc/dnsdist/`, database di `/var/lib/dnsdist/`, dan cronjob sync.*
