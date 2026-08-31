#!/bin/bash
# ============================================================
# DNSDist Edge Node - DB Sync Script (v3.0.0)
# Konsep: Edge hanya menerima file blacklist.db (pre-compiled)
# Fitur baru: Multi-URL failover (central + mirror + peer)
# ============================================================
SCRIPT_VERSION="3.0.0"

# --- Konfigurasi Default ---
DB_DIR="${DB_DIR:-/var/lib/dnsdist}"
DB_FILE="${DB_FILE:-${DB_DIR}/blacklist.db}"
# Multi-source: pisahkan dengan koma. Dicoba berurutan sampai sukses.
: "${CENTRAL_DB_URLS:-http://central-manager.local:8080/files/trust.db}"
if [ -n "$CENTRAL_DB_URLS" ]; then
    CENTRAL_DB_URLS="$CENTRAL_DB_URLS"
fi

# Baca konfigurasi node (override dari setup-edge.sh)
NODE_CONF="/etc/dnsdist/node.conf"
if [ -f "$NODE_CONF" ]; then
    # shellcheck source=/dev/null
    . "$NODE_CONF"
    [ -n "$SAVED_CENTRAL_DB_URL" ] && CENTRAL_DB_URLS="${CENTRAL_DB_URLS:-$SAVED_CENTRAL_DB_URL}"
    [ -n "$SAVED_CDB_SOURCES" ] && CENTRAL_DB_URLS="$SAVED_CDB_SOURCES"
fi

FORCE_FLAG="${DB_FILE}.force"

if [ "$1" = "--force-update" ] || [ "$1" = "-f" ]; then
    echo "[*] Force update diaktifkan. Mengabaikan cache..."
    touch "$FORCE_FLAG"
fi

if [ "$1" = "--version" ] || [ "$1" = "-V" ]; then
    echo "update-blacklist.sh v${SCRIPT_VERSION}"
    exit 0
fi

echo "[*] Sinkronisasi blacklist.db (multi-source failover)..."
mkdir -p "$DB_DIR"

# --- Deteksi DNSDist user ---
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
    apt-get update -qq && apt-get install -y aria2 -qq 2>/dev/null
fi

# --- Fungsi: download satu URL dengan cache check ---
download_source() {
    local url="$1" tmp_file="$2" http_code
    http_code="200"
    # Cek If-Modified-Since (skip jika tidak force)
    if [ -f "$DB_FILE" ] && [ ! -f "$FORCE_FLAG" ]; then
        FILE_DATE=$(date -u -r "$DB_FILE" +"%a, %d %b %Y %H:%M:%S GMT")
        http_code=$(curl -s -o /dev/null -w "%{http_code}" \
            -H "If-Modified-Since: $FILE_DATE" \
            --connect-timeout 10 -m 15 "$url")
    fi

    if [ "$http_code" = "304" ]; then
        echo "   [=] Cache valid (304): $url"
        return 0  # success, no download needed
    fi

    if [ "$http_code" != "200" ] && [ "$http_code" != "000" ]; then
        echo "   [!] HTTP $http_code: $url"
        return 1
    fi

    echo "   [↓] Download: $url"
    rm -f "$tmp_file"
    aria2c -x 8 -s 8 -k 1M \
        --connect-timeout=10 --timeout=120 --max-tries=3 \
        --out="$(basename "$tmp_file")" \
        --dir="$(dirname "$tmp_file")" \
        --allow-overwrite=true --summary-interval=0 \
        "$url" >/dev/null 2>&1

    if [ $? -eq 0 ] && [ -f "$tmp_file" ]; then
        # Verifikasi file tidak kosong (file CDB minimal 2KB)
        local size
        size=$(stat -c %s "$tmp_file" 2>/dev/null || echo 0)
        if [ "$size" -lt 2048 ]; then
            echo "   [!] File terlalu kecil ($size bytes): $url"
            rm -f "$tmp_file"
            return 1
        fi
        echo "   [✓] Downloaded: $url ($size bytes)"
        return 0
    fi
    echo "   [!] Download gagal: $url"
    rm -f "$tmp_file"
    return 1
}

# --- Main: loop semua source ---
IFS=',' read -r -a SOURCES <<< "$CENTRAL_DB_URLS"
TMP_FILE="${DB_FILE}.tmp"
success=0
success_url=""
http_code="500"

for src in "${SOURCES[@]}"; do
    url=$(echo "$src" | xargs)  # trim whitespace
    [ -z "$url" ] && continue
    echo "[*] Mencoba source: $url"
    if download_source "$url" "$TMP_FILE"; then
        success=1
        success_url="$url"
        break
    fi
done
rm -f "$FORCE_FLAG"

# --- Generate halaman status HTML ---
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
    <title>Status Sinkronisasi DNSDist</title>
    <style>
        body{background:#0f172a;font-family:sans-serif;color:#f8fafc;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
        .box{background:#1e293b;padding:40px;border-radius:12px;max-width:500px;width:100%;text-align:center;border:1px solid #334155}
        .success{color:#34d399}.warning{color:#fbbf24}.error{color:#f87171}
    </style>
</head>
<body>
    <div class="box">
        <h2>📡 Status Sinkronisasi Edge</h2>
        <div>
            <strong>Terakhir Update:</strong> $time_now<br><br>
            <strong>Ukuran Database:</strong> $file_size<br><br>
            <strong>Status:</strong> <span class="$status_color">$http_code</span><br><br>
            <strong>Source:</strong> $success_url<br><br>
            <strong>Pesan:</strong> $sync_msg<br><br>
            <strong>Script Version:</strong> v$SCRIPT_VERSION
        </div>
    </div>
</body>
</html>
EOF
}

# --- Proses hasil ---
if [ "$success" = "1" ] && [ -f "$TMP_FILE" ]; then
    echo "[+] Database baru berhasil diunduh dari $success_url"
    sync_msg="Database baru berhasil diunduh dan dipasang dari $success_url"
    status_color="success"
    http_code="200"
    mv "$TMP_FILE" "$DB_FILE"
    chown "${DNSDIST_USER}:${DNSDIST_USER}" "$DB_FILE" 2>/dev/null || true

    # Manifest sidecar (untuk cluster/panel Fase 2)
    sha=$(sha256sum "$DB_FILE" | awk '{print $1}')
    built_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    cat > "${DB_FILE}.manifest.json" <<EOF
{
  "version": 1,
  "sha256": "$sha",
  "source": "$success_url",
  "built_at": "$built_at"
}
EOF
    chown "${DNSDIST_USER}:${DNSDIST_USER}" "${DB_FILE}.manifest.json" 2>/dev/null || true

    echo "[+] Update selesai. DNSDist akan memuat database baru otomatis."
    generate_status_html
elif [ "$success" = "1" ]; then
    echo "[=] Tidak ada pembaruan (304). Database sudah terbaru."
    sync_msg="Tidak ada pembaruan. Database Edge Node sudah terbaru."
    status_color="warning"
    http_code="304"
    rm -f "$TMP_FILE"
    generate_status_html
else
    echo "[!] Semua source gagal. Mempertahankan DB lama."
    sync_msg="Semua source gagal — DB lama dipertahankan."
    status_color="error"
    http_code="500"
    rm -f "$TMP_FILE"
    generate_status_html
    exit 1
fi

exit 0
