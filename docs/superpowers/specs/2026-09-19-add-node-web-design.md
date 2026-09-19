# Add Node via Web Design

## Tujuan

Administrator dapat memulai enrollment Edge dari halaman **Cluster Nodes** di Master tanpa memasukkan kredensial Edge ke Master. Pendaftaran tetap dikonfirmasi di panel Edge.

## Pendekatan

Tiga opsi dipertimbangkan:

1. **Salin token manual**: paling sederhana, tetapi alur yang sudah ada masih mengharuskan perpindahan dan pengisian manual.
2. **Handoff browser melalui URL fragment**: dipilih. Master membuka panel Edge dengan data enrollment di fragment URL. Fragment tidak dikirim ke server HTTP. Edge mengisi formulir dan meminta konfirmasi administrator.
3. **Master mendorong konfigurasi ke Edge**: ditolak. Memerlukan kredensial/API Edge pada Master dan memperbesar risiko keamanan.

## Alur UI

1. Administrator membuka **Master → Cluster Nodes → Tambah Node**.
2. Modal meminta:
   - Nama node.
   - URL panel Edge, wajib `http://` atau `https://`, tanpa userinfo.
3. Master membuat token enrollment sekali pakai dengan masa berlaku 10 menit.
4. Master menampilkan tombol **Buka Panel Edge** dan fallback untuk menyalin token.
5. Tombol membuka URL Edge. `master_url`, token, dan nama node dibawa dalam URL fragment, bukan query string.
6. Setelah login, panel Edge membuka **Pengaturan**, mengisi form koneksi, lalu menampilkan data yang akan dipakai.
7. Administrator menekan **Hubungkan ke Master**.
8. Edge memanggil API register Master, menyimpan `node_id` dan `node_key`, menghapus token dari form dan fragment, lalu mulai heartbeat.
9. Master menampilkan node pada refresh/poll berikutnya.

## Perubahan Komponen

### Master Web UI

Modal token lama menjadi modal **Tambah Node**. JavaScript memvalidasi URL Edge, meminta token 10 menit melalui endpoint yang sudah ada, lalu membangun URL handoff. Tombol token manual dan contoh CLI tetap tersedia sebagai fallback.

### Edge Web UI

Saat aplikasi selesai autentikasi, JavaScript membaca fragment enrollment, memvalidasi data dasar, memilih halaman **Pengaturan**, dan mengisi form. Fragment segera dibersihkan dengan `history.replaceState` setelah data disalin ke memori/form.

### Backend

Endpoint dan format penyimpanan node tidak berubah. `POST /api/cluster/token` tetap digunakan, dengan `hours` diganti menjadi durasi menit agar TTL 10 menit dapat dinyatakan tepat. Demi kompatibilitas, field `hours` tetap diterima.

## Validasi dan Keamanan

- URL Edge hanya menerima skema HTTP/HTTPS dan menolak URL dengan username/password.
- Token tetap acak, sekali pakai, dan disimpan dengan izin file `0600`.
- Token handoff berada di URL fragment sehingga tidak masuk request, access log, atau `Referer` HTTP.
- Master tidak menerima atau menyimpan sandi/token sesi panel Edge.
- Pendaftaran tidak otomatis. Administrator Edge wajib menekan tombol konfirmasi.
- Pesan error tidak menampilkan `node_key`.

## Error Handling

- URL Edge tidak valid: modal menolak sebelum token dibuat.
- Token gagal dibuat: modal tetap terbuka dan menampilkan toast error.
- Edge tidak dapat dibuka: token dan perintah CLI tetap dapat disalin.
- Token kedaluwarsa/terpakai: API register menolak, Edge menampilkan error dan meminta token baru.
- Master tidak dapat dijangkau: input tetap tersedia agar dapat dicoba kembali.

## Pengujian

- Unit test TTL token dalam menit dan kompatibilitas `hours`.
- Unit test pembentukan/parsing payload handoff untuk karakter khusus dan URL tidak valid.
- Tes API memastikan token sekali pakai tetap berlaku.
- E2E yang ada tetap memverifikasi register, heartbeat, daftar node, dan penghapusan.
- `go test ./...` serta build panel wajib lulus.

## Batasan

Tidak ada discovery otomatis, SSH, push konfigurasi, atau penyimpanan kredensial Edge pada Master. HTTPS tetap direkomendasikan saat panel diakses melewati jaringan tidak tepercaya.
