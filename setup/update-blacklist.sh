#!/bin/bash
# ============================================================
# DNSDist Edge Node - DB Sync Script
# Versi: 2.0.0
# Konsep: Edge hanya menerima file blacklist.db (pre-compiled)
# ============================================================
SCRIPT_VERSION="2.0.0"

# --- Konfigurasi Default (Bisa di-override oleh setup-edge.sh) ---
# Path standar produksi — tidak perlu sed lagi
DB_DIR="/var/lib/dnsdist"
DB_FILE="${DB_DIR}/blacklist.db"
CENTRAL_DB_URL="${CENTRAL_DB_URL:-http://central-manager.local:8080/files/trust.db}"

# Baca konfigurasi node jika ada (override URL dari setup-edge.sh)
NODE_CONF="/etc/dnsdist/node.conf"
if [ -f "$NODE_CONF" ]; then
    # shellcheck source=/dev/null
    . "$NODE_CONF"
    [ -n "$SAVED_CENTRAL_DB_URL" ] && CENTRAL_DB_URL="$SAVED_CENTRAL_DB_URL"
fi

# Force flag file
FORCE_FLAG="${DB_FILE}.force"

if [ "$1" = "--force-update" ] || [ "$1" = "-f" ]; then
    echo "[*] Force update diaktifkan. Mengabaikan cache..."
    touch "$FORCE_FLAG"
fi

if [ "$1" = "--version" ] || [ "$1" = "-V" ]; then
    echo "update-blacklist.sh v${SCRIPT_VERSION}"
    exit 0
fi

echo "[*] Sinkronisasi blacklist.db dari Central Manager..."
echo "[*] URL : $CENTRAL_DB_URL"
echo "[*] Dest: $DB_FILE"

# Pastikan direktori target ada
mkdir -p "$DB_DIR"

# --- Deteksi DNSDist user (bisa _dnsdist atau dnsdist) ---
DNSDIST_USER="_dnsdist"
if ! id -u _dnsdist >/dev/null 2>&1; then
    if id -u dnsdist >/dev/null 2>&1; then
        DNSDIST_USER="dnsdist"
    else
        DNSDIST_USER="root"
    fi
fi

# --- Pastikan aria2 terinstal ---
if ! command -v aria2c >/dev/null 2>&1; then
    echo "[*] Menginstal aria2 untuk download multi-koneksi..."
    apt-get update -qq && apt-get install -y aria2 -qq
fi

# --- Cek pembaruan dengan Caching (If-Modified-Since) ---
http_code="200"
if [ -f "$DB_FILE" ] && [ ! -f "$FORCE_FLAG" ]; then
    FILE_DATE=$(date -u -r "$DB_FILE" +"%a, %d %b %Y %H:%M:%S GMT")
    http_code=$(curl -s -o /dev/null -w "%{http_code}" \
        -H "If-Modified-Since: $FILE_DATE" \
        --connect-timeout 10 -m 15 "$CENTRAL_DB_URL")
    echo "[*] Cek pembaruan: HTTP $http_code"
fi
rm -f "$FORCE_FLAG"

# --- Download jika ada pembaruan ---
if [ "$http_code" = "200" ] || [ "$http_code" = "000" ]; then
    echo "[*] Mengunduh database menggunakan Aria2 (8 Koneksi Paralel)..."
    TMP_FILE="${DB_FILE}.tmp"
    rm -f "$TMP_FILE"

    aria2c -x 8 -s 8 -k 1M \
        --connect-timeout=10 --timeout=120 --max-tries=3 \
        --out="$(basename "$TMP_FILE")" \
        --dir="$(dirname "$TMP_FILE")" \
        --allow-overwrite=true --summary-interval=0 \
        "$CENTRAL_DB_URL"

    if [ $? -eq 0 ] && [ -f "$TMP_FILE" ]; then
        http_code="200"
    else
        http_code="500"
    fi
fi

# --- Fungsi: Generate halaman status HTML ---
generate_status_html() {
    [ ! -d "/var/www/html" ] && return

    mkdir -p /var/www/html/status

    local time_now file_size
    time_now=$(date +"%Y-%m-%d %H:%M:%S")
    file_size="0 KB"
    [ -f "$DB_FILE" ] && file_size=$(du -h "$DB_FILE" | awk '{print $1}')

    cat > /var/www/html/status/index.html <<EOF
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <title>Status Sinkronisasi DNSDist</title>
    <style>
        body{background:#0f172a;font-family:sans-serif;color:#f8fafc;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
        .box{background:#1e293b;padding:40px;border-radius:12px;box-shadow:0 10px 25px rgba(0,0,0,0.5);max-width:500px;width:100%;text-align:center;border:1px solid #334155}
        h2{color:#38bdf8;margin-top:0}
        .info{margin:20px 0;text-align:left;background:#0f172a;padding:15px;border-radius:8px;font-family:monospace;font-size:14px;border:1px solid #334155}
        .footer{font-size:12px;color:#64748b;margin-top:20px}
        .success{color:#34d399}
        .warning{color:#fbbf24}
        .error{color:#f87171}
    </style>
</head>
<body>
    <div class="box">
        <h2>📡 Status Sinkronisasi Edge</h2>
        <p>Edge Node ini terhubung ke Central Manager.</p>
        <div class="info">
            <strong>Terakhir Update:</strong> $time_now<br><br>
            <strong>Ukuran Database:</strong> $file_size<br><br>
            <strong>Status HTTP    :</strong> <span class="$status_color">$http_code</span><br><br>
            <strong>Pesan          :</strong> $sync_msg<br><br>
            <strong>Script Version :</strong> v$SCRIPT_VERSION
        </div>
        <div class="footer">Trust-NG Edge Sync Daemon</div>
    </div>
</body>
</html>
EOF
}

# --- Proses Hasil Download ---
if [ "$http_code" = "200" ]; then
    echo "[+] Database baru berhasil diunduh!"
    sync_msg="Database baru berhasil diunduh dan dipasang!"
    status_color="success"

    # Atomic Replace
    mv "${DB_FILE}.tmp" "$DB_FILE"

    # Hak akses ke DNSDist
    chown "${DNSDIST_USER}:${DNSDIST_USER}" "$DB_FILE" 2>/dev/null || true

    echo "[+] Update selesai. DNSDist akan memuat database baru secara otomatis."
    generate_status_html

elif [ "$http_code" = "304" ]; then
    echo "[=] Tidak ada pembaruan. Database di Edge sudah yang terbaru."
    sync_msg="Tidak ada pembaruan. Database Edge Node sudah setara dengan server pusat."
    status_color="warning"
    rm -f "${DB_FILE}.tmp"
    generate_status_html

else
    echo "[!] Gagal menghubung server pusat. HTTP Code: $http_code"
    sync_msg="Koneksi gagal ke Central Manager."
    status_color="error"
    rm -f "${DB_FILE}.tmp"
    generate_status_html
    exit 1
fi

exit 0
