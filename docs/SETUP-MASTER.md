# 👑 Setup Guide — DNSDist Central Master Server

Panduan instalasi dan konfigurasi **Central Master Server** untuk kompilasi dan distribusi database blacklist CDB (`trust.db`) ke seluruh Edge Node.

---

## 🌟 Fitur Utama Master Server

1. **Fleksibel**:
   - **Standalone Mode (`--no-dnsdist`)**: Murni sebagai Central Compiler & Publisher (tanpa DNSDist, port 53 bebas, sangat hemat resource).
   - **Hybrid Mode (`--with-dnsdist`)**: Berfungsi sebagai Central Compiler sekaligus DNS Resolver aktif di server tersebut.
2. **Kompilasi Cepat CDB**: Menggunakan `trust-builder` (Go high-performance streamer).
3. **Content-Addressed Storage**: Setiap hasil build memiliki hash SHA-256 unik (`trust.<sha>.db`) dengan atomic symlink swap ke `trust.db`.
4. **Manifest JSON Sidecar**: Menyediakan endpoint `/files/manifest.json` berisi hash, ukuran, dan waktu build untuk verifikasi edge node.
5. **Otomatisasi Cron**: Kompilasi terjadwal otomatis (default: setiap 6 jam).
6. **Panel Terintegrasi (Dual Mode)**: Web panel mendukung HTTPS (`:8443`) dan HTTP (`:8084`) secara bersamaan.

---

## 🚀 Instalasi Cepat

### 1. Masuk ke Direktori Setup
```bash
cd /opt/dnsdist-edge/setup
```

### 2. Jalankan Installer Master

#### Opsi A: Standalone Master (Rekomendasi Central Server — Tanpa DNSDist)
```bash
sudo ./setup-master.sh --install --no-dnsdist --with-panel
```

#### Opsi B: Hybrid Master (Dengan DNSDist)
```bash
sudo ./setup-master.sh --install --with-dnsdist --with-panel
```

#### Opsi C: Kustom Port HTTP (Misal Port 8088)
```bash
sudo ./setup-master.sh --install --no-dnsdist --port 8088
```

---

## ⚙️ Konfigurasi Sumber Blacklist & Whitelist

Semua konfigurasi sumber berada di `/etc/dnsdist-master/`:

| File | Fungsi |
|---|---|
| `/etc/dnsdist-master/sources.txt` | Daftar URL blacklist mentah (Trust Positif, AdGuard, dll) |
| `/etc/dnsdist-master/whitelist.txt` | Daftar domain yang dikecualikan (tidak akan diblokir) |
| `/etc/dnsdist-master/custom-blacklist.txt` | Daftar domain lokal tambahan yang ingin diblokir |

### API Whitelist Master

Panel Master menyediakan endpoint terautentikasi berikut:

- `GET /api/master/whitelist`: membaca isi `whitelist.txt` dan jumlah entri.
- `POST /api/master/whitelist`: menyimpan objek JSON `{"whitelist":"example.com\n192.0.2.1\n"}` setelah validasi dan normalisasi.

Format whitelist:

- Satu domain atau alamat IP polos per baris, dengan pencocokan exact-match.
- Baris yang diawali `#` dipertahankan sebagai komentar; baris kosong diabaikan.
- Subdomain tidak tercakup otomatis. Tulis setiap subdomain sebagai entri terpisah.
- Jangan masukkan credential, URL, format hosts/AdGuard, atau wildcard.
- Ukuran body `POST` maksimum 1 MiB. Input invalid ditolak tanpa mengubah file aktif.

Sebelum penyimpanan berhasil, file lama disalin secara atomik ke
`/etc/dnsdist-master/whitelist.txt.bak`. Hasil normalisasi kemudian ditulis secara
atomik ke `whitelist.txt`.

### Contoh Isi `sources.txt`:
```text
# Trust Positif Kominfo (Mirror)
https://raw.githubusercontent.com/Santainetwork/trust-positif-mirror/main/domains.txt

# AdGuard DNS Filter (opsional)
https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt
```

### Trigger Kompilasi Manual:
```bash
sudo /usr/local/bin/build-master-cdb.sh
# atau
sudo ./setup-master.sh --build-now
```

---

## 🌐 Menghubungkan Edge Node ke Master

Setelah Master Server aktif, daftarkan URL master di Edge Node:

### 1. Pada Saat Instalasi Edge Node Baru:
```bash
sudo ./setup-edge.sh --install --url http://IP_MASTER:8080/files/trust.db
```

### 2. Pada Edge Node yang Sudah Berjalan:
```bash
# Set central URL utama
sudo ./setup-edge.sh --url http://IP_MASTER:8080/files/trust.db

# Atau tambahkan sebagai sumber multi-source cluster
sudo ./setup-edge.sh --set-cdb-sources "http://IP_MASTER:8080/files/trust.db,http://mirror:8080/files/trust.db"

# Lakukan sinkronisasi langsung
sudo /usr/local/bin/update-blacklist.sh --force-update
```

---

## 🖥️ Panel Web Master

Jika opsi `--with-panel` diaktifkan:
- **HTTPS**: `https://IP_MASTER:8443` (SSL self-signed)
- **HTTP**: `http://IP_MASTER:8084` (Plain HTTP)
- Login default: `admin` / `trust-ng-admin`
