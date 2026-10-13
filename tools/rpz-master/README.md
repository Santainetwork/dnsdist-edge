# rpz-master

Daemon Go tunggal yang berfungsi sebagai **RPZ secondary (slave)**, **kompilator CDB**, dan **open master (AXFR/IXFR + distributor HTTP)**. Dirancang untuk mengambil daftar blokir Trust Positif Komdigi dan/atau menarik zona RPZ dari master upstream, lalu menyajikannya ke node `dnsdist` — dengan footprint RAM rendah dan publikasi file yang atomik.

Dokumen ini cukup untuk mengadopsi tool ini di lingkungan lain dari nol, tanpa membaca kode.

---

## Daftar Isi

1. [Ringkasan fungsi](#1-ringkasan-fungsi)
2. [Arsitektur & alur data](#2-arsitektur--alur-data)
3. [Build](#3-build)
4. [Model aksi](#4-model-aksi)
5. [Referensi flag CLI](#5-referensi-flag-cli)
6. [Referensi konfigurasi](#6-referensi-konfigurasi)
7. [TSIG (autentikasi transfer)](#7-tsig-autentikasi-transfer)
8. [Resep adopsi](#8-resep-adopsi)
9. [Endpoint HTTP](#9-endpoint-http)
10. [Jebakan & batasan penting](#10-jebakan--batasan-penting)
11. [Integrasi dnsdist](#11-integrasi-dnsdist)
12. [Allowlist slave (siapa yang boleh transfer dari rpz-master)](#12-allowlist-slave-siapa-yang-boleh-transfer-dari-rpz-master)
13. [Operasional & pemecahan masalah](#13-operasional--pemecahan-masalah)
14. [Struktur kode](#14-struktur-kode)

---

## 1. Ringkasan fungsi

rpz-master punya tiga peran independen. Anda bisa memakainya hanya untuk salah satu:

| Peran | Arah | Butuh jaringan? | Output |
|---|---|---|---|
| **Slave/consumer** | Tarik dari upstream Komdigi (AXFR/IXFR) | DNS port 53 | `raw_domain_file`, `cdb_path`, `state_path` |
| **Kompilator CDB** | Baca file domain lokal | Tidak | `cdb_path` + `manifest.json` |
| **Open master/publisher** | Layani klien (AXFR/IXFR) + HTTP | Ya (listen) | Transfer DNS + endpoint HTTP |

**Penting:** rpz-master **tidak pernah** menyentuh atau me-restart dnsdist. Ia hanya menulis file di disk. Siapa pun yang memasang CDB ke dnsdist adalah langkah terpisah (panel, cron, operator).

---

## 2. Arsitektur & alur data

```
   ┌─────────────┐   HTTP (bootstrap)   ┌──────────────────────────────┐
   │  Komdigi    │ ───────────────────► │ raw_domain_file (domains_isp)│
   │  (URL)      │                      └──────────────┬───────────────┘
   └─────────────┘                                     │
                                                       │ CompileDomainsToCDB
   ┌─────────────┐   DNS AXFR/IXFR      ┌──────────────▼───────────────┐
   │  Upstream   │ ───────────────────► │ cdb_path (trust.db)          │
   │  master     │   (slave_client)     │ manifest.json                │
   └─────────────┘                      │ state_path (serial/sha256)   │
                                        └──────────────┬───────────────┘
                                                       │
                          ┌────────────────────────────┴────────────────┐
                          │                                             │
                   DNS AXFR/IXFR (publisher)                  HTTP distributor
                   :dns_listen                                :http_listen
                          │                                             │
                          ▼                                             ▼
                   slave rekanan / ISP                        edge dnsdist (CDB)
```

Dua jalur pembaruan:
- **Bootstrap/kompilasi** (jalur HTTP, murah): unduh `domains_isp` (`~200 MB`), kompilasi ke CDB. **Tanpa transfer DNS.**
- **Sync** (jalur DNS): tarik AXFR (saat serial lokal 0) atau IXFR delta (serial lokal ≠ 0) dari upstream, lalu publikasi CDB baru.

Semua penulisan file memakai **atomic rename** (tulis `.tmp` → rename). Lihat [§10](#10-jebakan--batasan-penting) untuk jaminan atomisitasnya.

---

## 3. Build

Prasyarat: Go 1.24+.

```bash
cd tools/rpz-master
./build.sh              # menghasilkan ./rpz-master
./rpz-master -version   # rpz-master v1.0.0 - ...
```

Isi `build.sh`:
```bash
go build -ldflags "-s -w" -o rpz-master .
```

Tes:
```bash
go test ./...
```

**Penting untuk adopsi lintas mesin:** binary dibangun untuk arsitektur lokal. Untuk host lain:
```bash
GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o rpz-master .
scp rpz-master root@host:/usr/local/bin/rpz-master
```

---

## 4. Model aksi

Dijalankan dengan `-action <nilai>`. Default `serve`.

### `bootstrap` — unduh (bila perlu) + kompilasi

```
kalau raw_domain_file belum ada:
    unduh dari source_domain_url  (wajib ada)
kompilasi raw_domain_file -> cdb_path
tulis state (total_domains, cdb_sha256, last_update)
```

- Kalau `raw_domain_file` **sudah ada**, download **dilewati** — langsung kompilasi.
- **Tidak** menyentuh DNS/Jaringan DNS.
- **Tidak** menaikkan `serial` jika `source_mode == "rpz-slave"` (lihat [§10](#10-jebakan--batasan-penting)).
- Tidak butuh `upstream_master`.

```bash
./rpz-master -action bootstrap -c config.json
```

### `build-cdb` — kompilasi saja

```
kompilasi raw_domain_file -> cdb_path   (tanpa jaringan apa pun)
```

```bash
./rpz-master -action build-cdb -c config.json
```

Cocok untuk refresh: unduh `domains_isp` manual, ganti file, lalu `build-cdb`.

### `sync` — tarik dari upstream (slave)

```
serial_upstream = query SOA ke upstream_master
kalau serial_upstream tidak lebih besar dari state.serial: selesai (up to date)
kalau state.serial == 0:  AXFR penuh
selain itu:               IXFR delta dari state.serial
parse transfer -> Delta
PublishDelta: terapkan delta ke raw + kompilasi ulang CDB (atomic)
state.serial = serial_upstream
```

- Butuh `upstream_master`.
- `sync` menyentuh DNS (port 53) ke upstream.
- **Sekali jalan**; tidak daemon. Cocok untuk cron.

```bash
./rpz-master -action sync -c config.json
```

### `serve` — daemon

```
kalau raw_domain_file belum ada: coba sync, fallback bootstrap
kalau cdb_path belum ada:        build-cdb
jalankan: DNS server (SOA/AXFR/IXFR keluar) + HTTP distributor
kalau upstream_master diisi:     periodicSync tiap check_interval
```

- **Selalu** menyalakan DNS server di `listen_dns` (http opsional).
- `serve` mensyaratkan `source_mode == "rpz-slave"` dan `upstream_master` terisi (`ValidateDaemon`).
- Berhenti bersih pada `SIGINT`/`SIGTERM`.

```bash
./rpz-master -action serve -c config.json
```

---

## 5. Referensi flag CLI

| Flag | Default | Arti |
|---|---|---|
| `-action` | `serve` | `serve` \| `bootstrap` \| `sync` \| `build-cdb` |
| `-c` | `""` | Path config JSON. Kosong = pakai `DefaultConfig()` |
| `-zone` | `""` | Override `zone` dari config |
| `-dns-listen` | `""` | Override `listen_dns` |
| `-http-listen` | `""` | Override `listen_http` |
| `-input` | `""` | Override `raw_domain_file` (untuk bootstrap/build-cdb) |
| `-v`, `-version` | | Cetak versi lalu keluar |

Flag override diterapkan **setelah** config dimuat, **sebelum** validasi.

---

## 6. Referensi konfigurasi

File JSON. Decoder memakai `DisallowUnknownFields` — **field tak dikenal = error**. Mulai dari default lalu overrides.

| Field | Default | Wajib | Catatan |
|---|---|---|---|
| `source_mode` | `feeds` | ya | `feeds` \| `rpz-slave` |
| `zone` | `rpz.trustpositif.` | ya | FQDN, dibutuhkan titik akhir |
| `listen_dns` | `127.0.0.1:5353` | ya | `host:port`; **tidak boleh kosong** |
| `listen_http` | `127.0.0.1:8088` | tidak | Boleh `""` (= HTTP mati) |
| `cname_target` | `blockpage.komdigi.go.id.` | ya | FQDN |
| `cdb_path` | `/var/lib/dnsdist/trustpositif.cdb` | ya | Output CDB |
| `state_path` | `/var/lib/dnsdist/rpz-master-state.json` | ya | State serial/hash |
| `source_domain_url` | `https://trustpositif.komdigi.go.id/assets/db/domains_isp` | bootstrap | harus http/https valid bila diisi |
| `upstream_master` | `""` | sync/serve | `host:port` (WAJIB ada `:53` dst.) |
| `tsig_key` | `""` | tidak | FQDN; harus berpasangan dengan `tsig_secret_file` |
| `tsig_secret_file` | `""` | tidak | File base64 mode `0600`, reguler |
| `tsig_secret` | `""` | — | **DILARANG** (error bila diisi); pakai file |
| `tsig_algorithm` | `hmac-sha256.` | tidak | FQDN; harus sama di kedua sisi |
| `check_interval` | `15m` | tidak | Interval `periodicSync` di `serve` |
| `max_transfers` | `10` | tidak | Batas transfer keluar konkuren |
| `max_transfer_records` | `40000000` | tidak | Batas RR per transfer masuk. Zona RPZ nasional > 10 juta didukung; naikkan bila perlu, turunkan bila RAM ketat |
| `raw_domain_file` | `/var/lib/dnsdist/domains_isp.txt` | ya | Daftar domain, 1 per baris |
| `transfer_acl` | `["127.0.0.0/8","::1/128"]` | ya | **Tidak boleh kosong**; CIDR |

### Aturan validasi

- `source_mode` harus `feeds` atau `rpz-slave`.
- `zone` & `cname_target`: harus nama domain valid (bila kosong → error).
- `listen_dns`: `host:port`, port 1–65535, host harus IP atau kosong.
- `transfer_acl`: setiap entri CIDR valid; daftar tidak boleh kosong.
- `tsig_key` dan `tsig_secret_file` harus **bersama-sama** ada atau bersama-sama kosong.
- `source_domain_url`: bila diisi, harus URL http/https valid.

### Contoh minimal (slave dari Komdigi, tanpa TSIG)

```json
{
  "source_mode": "rpz-slave",
  "zone": "trustpositifkominfo.",
  "upstream_master": "182.23.79.202:53",
  "tsig_key": "",
  "tsig_secret_file": "",
  "check_interval": "15m",
  "raw_domain_file": "/var/lib/rpz-master/domains.txt",
  "cdb_path": "/var/lib/rpz-master/trust.db",
  "state_path": "/var/lib/rpz-master/state.json",
  "listen_dns": "127.0.0.1:5354",
  "listen_http": ""
}
```

---

## 7. TSIG (autentikasi transfer)

TSIG = tanda tangan kunci bersama (RFC 2845) untuk membuktikan transfer datang dari pihak berhak.

**Dua arah, dua arti berbeda:**
- **Keluar (rpz-master → upstream):** TSIG dikirim bila `tsig_key` terisi. Access-control dari sisi upstream.
- **Masuk (klien → rpz-master):** diverifikasi via `transfer_acl` **dan** (bila `tsig_key` diisi) TSIG pada request. Lihat `transferAuthorized()`.

### Aturan file secret (dipaksa kode)

`ReadTSIGSecret()` gagal jika:
1. Bukan file **reguler** (symlink/dir ditolak).
2. Permission **buka ke group/other** (`mode & 077 != 0`) → harus `0600`.
3. Kosong.
4. Isi **bukan base64 standar**.

### Membuat secret

```bash
install -d -m 700 /etc/rpz-master
head -c 32 /dev/urandom | base64 > /etc/rpz-master/upstream.secret
chmod 600 /etc/rpz-master/upstream.secret
cat /etc/rpz-master/upstream.secret   # salin ke sisi upstream
```

Pasang di config:
```json
"tsig_key": "rpz-key.",
"tsig_secret_file": "/etc/rpz-master/upstream.secret",
"tsig_algorithm": "hmac-sha256."
```

Pasangan di sisi upstream harus **sama**: dnsdist `setKey("rpz-key.", "BASE64_SAMA")`; BIND blok `key`+`allow-transfer`; rpz-master lain dengan config identik.

### Tanpa TSIG

`tsig_key` kosong → transfer tanpa tanda tangan. Aman untuk data publik **asalkan** upstream membatasi lewat allow-IP. Untuk kasus **allow-IP Komdigi**: kosongkan `tsig_key`, dan pastikan IP sumber host Anda = IP terdaftar (lihat [§10](#10-jebakan--batasan-penting) — klien transfer keluar tidak bisa bind source IP dari config).

---

## 8. Resep adopsi

### Skenario A — hanya menghasilkan CDB (tanpa dnsdist, tanpa daemon)

```bash
./rpz-master -action bootstrap -c config.json   # unduh domains_isp -> trust.db
# atau, bila domains_isp sudah ada:
./rpz-master -action build-cdb -c config.json
```

Output: `cdb_path` + `manifest.json` + `state_path`. Selesai.

### Skenario B — slave murni, sekali jalan (cron)

```bash
# cron tiap 6 jam
./rpz-master -action sync -c /etc/rpz-master/config.json
```

Tarik AXFR/IXFR → publikasi CDB. Tidak daemon.

### Skenario C — daemon slave + publisher (cluster ISP)

```bash
./rpz-master -action serve -c /etc/rpz-master/config.json
# systemd unit, Restart=on-failure
```
Slave menarik dari upstream berkala (`check_interval`) **dan** melayani AXFR/IXFR ke rekanan + HTTP ke edge.

### Skenario D — refresh CDB murni via domains_isp (rekomendasi anti-jebakan)

```bash
curl -fsSL "$SOURCE_URL" -o /var/lib/rpz-master/domains.txt.new
mv /var/lib/rpz-master/domains.txt.new /var/lib/rpz-master/domains.txt
./rpz-master -action build-cdb -c config.json
```
Selalu dataset utuh, tidak ada ketergantungan serial, tidak ada risiko IXFR mismatch. **Tidak memakai `sync`.**

### Checklist adopsi di host baru

1. Go 1.24+ terpasang; `./build.sh` (atau salin binary + `.go` bila perlu).
2. Buat `raw_domain_file`, `cdb_path`, `state_path` (mode 0600/0700).
3. Tulis config lengkap (semua field wajib isi).
4. `mkdir -p` direktori parent tiap path.
5. Set TSIG **atau** pastikan allow-IP upstream sudah benar.
6. Jalankan aksi pertama (`bootstrap`/`build-cdb`/`sync`), cek `state.json`.
7. Kalau mau daemon: pasang sebagai systemd service.

---

## 9. Endpoint HTTP

Aktif only bila `listen_http` tidak kosong.

| Path | Isi |
|---|---|
| `/blacklist.db`, `/trust.db`, `/files/trust.db` | File CDB. Mengirim `ETag` (sha256), `Content-Type: application/octet-stream`, `Content-Disposition: attachment`. |
| `/manifest.json`, `/files/manifest.json`, `/status` | JSON: `{status, serial, last_update, total_domains, sha256, cdb_file}` |
| `/health` | `{"status":"healthy"}` |

Contoh konsumsi edge:
```bash
curl -s http://master:8088/manifest.json          # baca serial & sha256
curl -s http://master:8088/trust.db -o trust.db    # unduh CDB
# verifikasi sha256(trust.db) == manifest.sha256, lalu pasang atomik
```

---

## 10. Jebakan & batasan penting

Bagian ini yang paling menentukan keberhasilan adopsi. Dibaca dulu sebelum produksi.

### 10.1 Batas record transfer (kini bisa dikonfigurasi)

Batas jumlah RR per transfer masuk diatur `max_transfer_records` (default **40.000.000**). Transfer yang melebihi batas ditolak:

```
sync IXFR: transfer melebihi batas 40000000 record
```

- Zona RPZ nasional > 10 juta record **didukung**. Respons AXFR di-stream langsung ke file staging (`.axfr-stage-*`) dengan memori terbatas (~1 envelope), bukan ditahan sebagai daftar di RAM. Uji nyata: 10,5 juta record, sync 28 detik, RSS puncak ~190 MB.
- Turunkan `max_transfer_records` bila ingin pagar lebih ketat (mis. di host RAM kecil); naikkan bila zona Anda lebih besar.
- Respons IXFR (delta) masih diakumulasi di memori — tapi delta biasanya kecil; batas tetap berlaku.
- Bila transfer dibatalkan di tengah (limit/putus), file staging dihapus dan data lama tidak tersentuh.

### 10.2 Perangkap serial = 0 di mode `rpz-slave`

`bootstrap` dan `build-cdb` **tidak** menaikkan `serial` bila `source_mode == "rpz-slave"`:

```go
if next.Serial == 0 && a.cfg.SourceMode != "rpz-slave" {   // <-- rpz-slave DILEWATI
    next.Serial = ...
}
```

Akibat: setelah bootstrap di mode `rpz-slave`, `state.serial` tetap **0**. Lalu `sync`:
```go
if c.state.Serial == 0 { m.SetAxfr(zone) } else { m.SetIxfr(...) }
```
→ `serial == 0` memaksa **AXFR penuh** (bukan IXFR delta) → kena limit 10 juta.

**Artinya: bootstrap + sync = masih AXFR penuh.** Untuk memakai IXFR, `state.serial` harus ≠ 0.

### 10.3 Menyetel serial manual itu rapuh

Anda bisa edit `state_path` dan set `serial` = serial SOA upstream. Lalu `sync` akan:
- Bila `serial == upstream` → dianggap up-to-date (tidak transfer).
- Bila `serial < upstream` → IXFR delta.

**Risikonya:** IXFR mengirim **hanya perbedaan** dari serial yang Anda klaim. Kalau isi lokal sebenarnya bukan kondisi zona pada serial itu, hasil delta bisa **korup** (domain terlewat / duplikat). `ApplyDeltaToFile` menambah/ menghapus berdasarkan `add`/`deleted` delta, mengasumsikan basis lokal benar.

**Aturan aman:** jangan campur basis `domains_isp` dengan IXFR kecuali Anda yakin keduanya dataset identik pada serial itu.

### 10.4 Klien transfer keluar tidak bisa bind source IP

`dns.Transfer`/`client.Exchange` tidak memakai `Dialer.LocalAddr`. IP sumber ditentukan routing OS. Untuk kasus **allow-IP**, jalankan rpz-master di host ber-IP terdaftar, atau atur di OS:

```bash
ip route replace <UPSTREAM_IP>/32 via <GATEWAY> src <IP_PUBLIK_TERDAFTAR>
```

### 10.5 `serve` selalu menyalakan DNS server

Tidak ada opsi "slave saja tanpa publisher" di `serve`. `listen_dns` tidak boleh kosong. Untuk tidak jadi publisher publik, bind `127.0.0.1:<port>` + `transfer_acl` loopback. Untuk slave murni tanpa publisher sama sekali, pakai `sync` (bukan `serve`).

### 10.6 Refus vs izin

- `rcode=REFUSED` pada query SOA = upstream menolak (ACL IP / zone salah / butuh TSIG). **Bukan** bug rpz-master.
- `zone` di config harus **persis** sama dengan yang berhasil di `dig`.
- `upstream_master` wajib `host:port` (sertakan `:53`).

### 10.7 Atomisitas lintas file

Raw, CDB, manifest, dan state masing-masing atomic rename. **Tidak ada** transaksi tunggal lintas keempatnya — crash di antaranya bisa meninggalkan metadata lama bersama CDB baru. Delta idempoten; jalankan `-action sync`/`build-cdb` lagi untuk memulihkan generasi konsisten. Monitor `state.json`/manifest.

### 10.8 Format domain

- Satu domain per baris; baris kosong & `#komentar` diabaikan.
- Domain di-lowercase, suffix titik dibuang, maks 253 char, harus nama domain valid.
- Ekstraksi dari transfer RPZ (`policyOwners`): hanya owner `CNAME`/`A`/`AAAA` di dalam zone yang diambil sebagai domain (dipotong suffix zone).

### 10.9 `DisallowUnknownFields`

Config dengan field tak dikenal = **error** saat load. Jangan menambah field sembarangan.

---

## 11. Integrasi dnsdist

### Sebagai master (dnsdist melayani transfer ke rpz-master / slave)

```lua
newServer({ address = '127.0.0.1:5354', pool = 'rpz-master', name = 'rpz-master' })
addAction(
  AndRule({ TCPRule(true), OrRule({ QTypeRule(DNSQType.AXFR), QTypeRule(DNSQType.IXFR), QTypeRule(DNSQType.SOA) }) }),
  PoolAction('rpz-master')
)
-- bila pakai TSIG:
-- setKey("rpz-key.", "BASE64_SECRET")
```

### Sebagai edge (dnsdist memakai CDB dari rpz-master)

```lua
kvs = newCDBKVStore('/var/lib/dnsdist/trustpositif.cdb', 30)
addAction(
  AndRule({ KeyValueStoreLookupRule(kvs, KeyValueLookupKeyQName(true)), QTypeRule(DNSQType.A) }),
  SpawnCNAMEAction("blockpage.komdigi.go.id.")
)
```

**Penting:** kunci CDB memakai **wire-format** (`dns.PackDomainName`), cocok dengan `KeyValueLookupKeyQName(true)`. Jangan ubah `wireFormat` saat kompilasi.

---

## 12. Allowlist slave (siapa yang boleh transfer dari rpz-master)

Dua lapis pertahanan — pakai **keduanya**:

1. **Di rpz-master**: `transfer_acl` (daftar CIDR). Hanya IP di daftar yang boleh AXFR/IXFR. Bila `tsig_key` diisi, TSIG juga wajib valid.
2. **Di dnsdist** (bila dnsdist di depan rpz-master): tolak AXFR/IXFR dari luar netmask yang diizinkan, karena rpz-master melihat IP sumber = dnsdist (bukan IP klien asli).

### 12.1 Sisi rpz-master

```json
{
  "transfer_acl": ["192.168.0.0/16", "10.8.0.0/24", "2001:db8::/32"]
}
```

- Daftar CIDR; boleh IPv4 dan IPv6 bercampur; **tidak boleh kosong**.
- IP di luar daftar ditolak (REFUSED) untuk AXFR/IXFR. SOA dan kueri lain tidak dibatasi oleh ACL ini.
- `127.0.0.0/8` wajib ada bila dnsdist (di host yang sama) yang meneruskan transfer.
- Untuk keamanan lebih: isi `tsig_key` + `tsig_secret_file` — transfer tanpa tanda tangan yang valid ditolak.

### 12.2 Sisi dnsdist (contoh khusus allowlist)

Karena dnsdist menjadi gerbang, allowlist asli harus ditegakkan di sini. Contoh lengkap dan minimal:

```lua
-- 1. Backend rpz-master di loopback. Transfer selalu TCP.
newServer({address="127.0.0.1:5354", name="rpz-master", pool="rpz-master", tcpOnly=true})

-- 2. Jangan cache jawaban AXFR/IXFR (cegah cache bocor ke klien lain).
addAction(OrRule({QTypeRule(DNSQType.AXFR), QTypeRule(DNSQType.IXFR)}), SetSkipCacheAction())

-- 3. ALLOWLIST: netmask yang boleh meminta transfer.
local transferAllowed = newNMG()
transferAllowed:addMask("192.168.0.0/16")   -- rekanan/ISP Anda
transferAllowed:addMask("10.8.0.0/24")
transferAllowed:addMask("2001:db8::/32")

-- 4. Tolak AXFR/IXFR dari luar allowlist. WAJIB di atas rule pool.
addAction(
  AndRule({
    OrRule({QTypeRule(DNSQType.AXFR), QTypeRule(DNSQType.IXFR)}),
    NotRule(NetmaskGroupRule(transferAllowed))
  }),
  RCodeAction(DNSRCode.REFUSED)
)

-- 5. Route SOA/AXFR/IXFR zone kita ke rpz-master.
addAction(
  AndRule({
    QNameRule("trustpositifkominfo."),
    OrRule({QTypeRule(DNSQType.SOA), QTypeRule(DNSQType.AXFR), QTypeRule(DNSQType.IXFR)})
  }),
  PoolAction("rpz-master")
)
```

**Urutan penting:** rule tolak (4) harus **sebelum** rule pool (5). dnsdist memproses `addAction` berurutan.

Bila hanya satu rekanan, bisa lebih sederhana:

```lua
addAction(AndRule({OrRule({QTypeRule(DNSQType.AXFR), QTypeRule(DNSQType.IXFR)}), NotRule(NetmaskGroupRule("203.0.113.10/32"))}), RCodeAction(DNSRCode.REFUSED))
```

### 12.3 Bukti perilaku (terukur)

Diuji dengan dnsdist + rpz-master nyata:

| Skenario | Hasil |
|---|---|
| IP **di** allowlist minta AXFR | Sukses (zona terkirim) |
| IP **di luar** allowlist minta AXFR | Gagal/ditolak (REFUSED) |
| SOA dari IP di luar allowlist | Tetap NOERROR (rule tolak hanya AXFR/IXFR) |
| rpz-master tanpa IP di `transfer_acl` | REFUSED |

Verifikasi cepat sebelum produksi:

```bash
# check-config dulu
sudo dnsdist --check-config -C /etc/dnsdist/dnsdist.conf

# dari host yang TIDAK di allowlist -> harus gagal
dig +tcp @<dnsdist-ip> trustpositifkominfo. AXFR

# dari host yang di allowlist -> harus sukses
dig +tcp @<dnsdist-ip> trustpositifkominfo. AXFR | head
```

---

## 13. Operasional & pemecahan masalah

### Verifikasi hasil

```bash
cat /var/lib/rpz-master/state.json          # serial, total_domains, cdb_sha256
ls -la /var/lib/rpz-master/domains.txt /var/lib/rpz-master/trust.db
head -5 /var/lib/rpz-master/domains.txt
sort /var/lib/rpz-master/domains.txt | uniq -d | wc -l   # cek duplikat
curl -s http://127.0.0.1:8088/health
curl -s http://127.0.0.1:8088/manifest.json
```

### Pesan error umum

| Pesan | Arti | Tindakan |
|---|---|---|
| `respons SOA tidak dapat digunakan: rcode=REFUSED` | Upstream menolak query SOA | Cek `zone`, `upstream_master`, allow-IP, TSIG |
| `gagal query SOA ke ...` | Tidak bisa reach upstream | Cek jaringan/port/firewall |
| `transfer melebihi batas 10000000 record` | Transfer > 10 juta RR | Naikkan konstanta + rebuild, atau hindari `sync` |
| `upstream_master is required in rpz-slave mode` | `serve` tanpa `upstream_master` | Isi config |
| `tsig_key and tsig_secret_file must be configured together` | TSIG setengah | Isi keduanya atau kosongkan keduanya |
| `tsig_secret_file must be regular and inaccessible...` | Permission/tipe file secret salah | `chmod 600`, pastikan file reguler |
| `transfer_acl must not be empty` | ACL kosong | Isi minimal loopback |
| `unknown field "..."` | Field config tak dikenal | Hapus/benarkan nama field |
| `invalid zone` | `zone` kosong/bukan nama domain | Perbaiki (sertakan titik) |

### Diagnosa tau_dig

```bash
dig @<UPSTREAM> <zone> SOA                 # tahap 1 rpz-master
dig @<UPSTREAM> <zone> AXFR +noidnout      # transfer penuh
ip route get <UPSTREAM>                    # IP sumber yang dipakai
```

### systemd unit contoh

```ini
[Unit]
Description=RPZ Master
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/rpz-master -action serve -c /etc/rpz-master/config.json
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

---

## 14. Struktur kode

| File | Isi |
|---|---|
| `main.go` | Entrypoint CLI, dispatch aksi, `Serve`, `periodicSync` |
| `config.go` | Schema `Config`, `State`, `Delta`, load/validasi, `ReadTSIGSecret` |
| `builder.go` | `DownloadDomainsToFile`, `CompileDomainsToCDB`, `PublishDelta`, manifest |
| `slave_client.go` | `SlaveClient` (query SOA, AXFR/IXFR), parse transfer, `ApplyDeltaToFile`, `normalizePolicyDomain` |
| `dns_server.go` | `RPZServer`: SOA, streaming AXFR (200 RR/envelope), IXFR delta, ACL/TSIG |
| `http_server.go` | Distributor HTTP (CDB, manifest, health) |
| `*_test.go` | Unit test integrasi |

Konvensi: kunci CDB wire-format; publikasi selalu atomic rename; limiter `semaphore` untuk AXFR konkuren (`max_transfers`).

---

## Lisensi & asal

Bagian dari proyek `dnsdist-edge` (Trust-NG). Dapat diadopsi mandiri; cukup binary + config, tanpa ketergantungan ke panel.
