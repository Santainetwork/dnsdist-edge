# 👑 Setup Guide — DNSDist Central Master Server

Panduan instalasi dan konfigurasi **Central Master Server** untuk mengambil sumber `feeds` atau menjadi secondary RPZ, lalu mendistribusikan database CDB (`trust.db`) ke seluruh Edge Node.

---

## 🌟 Fitur Utama Master Server

1. **Mode resolver fleksibel**:
   - **Standalone Mode (`--no-dnsdist`)**: Murni sebagai Central Compiler & Publisher (tanpa DNSDist, port 53 bebas, sangat hemat resource).
   - **Hybrid Mode (`--with-dnsdist`)**: Berfungsi sebagai Central Compiler sekaligus DNS Resolver aktif di server tersebut.
2. **Mode sumber fleksibel**:
   - **`feeds` (default)**: `sources.txt`, whitelist, dan custom blacklist dikompilasi oleh panel/trust-builder.
   - **`rpz-slave`**: `rpz-master` menarik AXFR/IXFR dari authoritative RPZ upstream dan menjadi satu-satunya writer CDB.
3. **Kompilasi Cepat CDB**: Menggunakan wire-format key yang kompatibel dengan `KeyValueLookupKeyQName(true)`.
4. **Output kompatibel**: Kedua source mode menerbitkan `/files/trust.db` dan `/files/manifest.json`.
5. **Satu writer**: Cron/panel builder aktif hanya pada `feeds`; service RPZ aktif hanya pada `rpz-slave`.
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
sudo ./setup-master.sh --install --source-mode feeds --no-dnsdist --with-panel
```

#### Opsi B: Hybrid Master (Dengan DNSDist)
```bash
sudo ./setup-master.sh --install --source-mode feeds --with-dnsdist --with-panel
```

#### Opsi C: Kustom Port HTTP (Misal Port 8088)
```bash
sudo ./setup-master.sh --install --no-dnsdist --port 8088
```

#### Opsi D: Secondary RPZ tanpa resolver lokal
```bash
sudo ./setup-master.sh --install \
  --source-mode rpz-slave \
  --rpz-upstream 192.0.2.53:53 \
  --rpz-zone rpz.example. \
  --rpz-transfer-acl "127.0.0.0/8,2001:db8:100::/48" \
  --no-dnsdist --with-panel
```

#### Opsi E: Secondary RPZ sekaligus resolver lokal
```bash
sudo ./setup-master.sh --install \
  --source-mode rpz-slave \
  --rpz-upstream 192.0.2.53:53 \
  --rpz-zone rpz.example. \
  --with-dnsdist --with-panel
```

`--with-dnsdist` tidak memilih sumber data. Source mode dan resolver mode selalu independen.

## Mode `rpz-slave`

Konfigurasi utama tersimpan di `/etc/dnsdist-master/rpz-master.json`; state dan domain mentah di `/var/lib/rpz-master/`. Service:

```bash
systemctl status rpz-master
journalctl -u rpz-master -f
/usr/local/bin/rpz-master -version
```

Default downstream RPZ DNS adalah `0.0.0.0:5354`. Port `5353` tidak dipakai karena dapat dimiliki backend TPROXY dnsdist. AXFR/IXFR ditolak kecuali sumber klien masuk `--rpz-transfer-acl`; default aman hanya loopback.

Untuk upstream TSIG, simpan secret pada file root-only, lalu tambahkan:

```bash
sudo install -m 0600 /dev/null /etc/dnsdist-master/rpz-upstream.secret
sudoedit /etc/dnsdist-master/rpz-upstream.secret

sudo ./setup-master.sh --install \
  --source-mode rpz-slave \
  --rpz-upstream 192.0.2.53:53 \
  --rpz-zone rpz.example. \
  --rpz-tsig-key rpz-transfer. \
  --rpz-tsig-secret-file /etc/dnsdist-master/rpz-upstream.secret
```

Jangan menaruh secret TSIG langsung pada command line atau repository. Bootstrap HTTP opsional dapat diberikan dengan `--rpz-bootstrap-url`; transfer DNS tetap sumber pembaruan authoritative.

Installer menyalin secret ke `/etc/dnsdist-master/rpz-upstream.secret` mode `0600`; service tidak bergantung pada lokasi file sumber setelah instalasi.

Installer memverifikasi SHA-256 release binary terhadap `SHA256SUMS`. Unduhan keduanya memakai URL release yang sama, jadi checksum melindungi dari kerusakan transfer, bukan kompromi akun/release GitHub atau jalur unduhan. Untuk trust lebih kuat, gunakan source bundle lokal tepercaya atau salurkan binary/checksum melalui kanal rilis terverifikasi.

---

## ⚙️ Konfigurasi Mode `feeds`

Semua konfigurasi sumber berada di `/etc/dnsdist-master/`:

| File | Fungsi |
|---|---|
| `/etc/dnsdist-master/sources.txt` | Daftar URL blacklist mentah (Trust Positif, AdGuard, dll) |
| `/etc/dnsdist-master/whitelist.txt` | Daftar domain yang dikecualikan (tidak akan diblokir) |
| `/etc/dnsdist-master/custom-blacklist.txt` | Daftar domain lokal tambahan yang ingin diblokir |

### API Whitelist Master

Pada source mode `feeds`, Panel Master menyediakan endpoint terautentikasi berikut:

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
# AdGuard DNS Filter
https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt
```

### Trigger Kompilasi Manual:
```bash
sudo /usr/local/bin/build-master-cdb.sh
# atau
sudo ./setup-master.sh --build-now
```

Pada `rpz-slave`, build/source/whitelist feeds dan scheduler panel dinonaktifkan untuk mencegah dua proses menulis `trust.db` yang sama.

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

### Penyimpanan Cluster (JSON / SQLite)

Panel master menyimpan registry node dan token enroll lewat interface `ClusterStorage`:

- **JSON file** (default): `--cluster-nodes-file /var/lib/dnsdist/cluster-nodes.json`.
- **SQLite pure-Go** (tanpa CGO, mode WAL): cukup akhiri path dengan `.db` atau `.sqlite`, misalnya `--cluster-nodes-file /var/lib/dnsdist/cluster.db`.

Kedua engine kompatibel penuh; migrasi cukup mengganti path file.

### Analitik dnstap (tanpa client IP)

Panel bisa menerima streaming dnstap dari dnsdist dan menampilkan domain terblokir teratas:

```bash
dnsdist-panel --master --dnstap-addr 127.0.0.1:6000
```

Di `dnsdist.conf` tambahkan logger framestream ke alamat yang sama:

```lua
local fstrm = newFrameStreamTcpLogger("127.0.0.1:6000")
addAction(kvsRule, DnstapLogAction("master-01", fstrm))
```

- Endpoint terautentikasi: `GET /api/dnstap/top?limit=10`.
- Client IP dibuang seketika setelah parsing (kepatuhan UU PDP); entri unik berlebih dialihkan ke bucket `_other_` agar memori tetap terbatas.
