#!/bin/bash
# ============================================================
# DNSDist Central Master - CDB Blacklist Compiler & Publisher
# Auto-compiles blacklists into content-addressed CDB format
# ============================================================

set -e

MASTER_CONF_DIR="/etc/dnsdist-master"
SOURCES_FILE="${MASTER_CONF_DIR}/sources.txt"
WHITELIST_FILE="${MASTER_CONF_DIR}/whitelist.txt"
CUSTOM_BL_FILE="${MASTER_CONF_DIR}/custom-blacklist.txt"
SERVE_DIR="/var/www/html/files"
BUILDER_BIN="/usr/local/bin/trust-builder"

# Default sources jika file belum ada
mkdir -p "$MASTER_CONF_DIR" "$SERVE_DIR"
if [ ! -f "$SOURCES_FILE" ]; then
    cat > "$SOURCES_FILE" << 'EOF'
# Daftar URL sumber blacklist (satu per baris, # untuk komentar)
# Contoh Trust Positif Kominfo (Mirror raw domain list):
https://raw.githubusercontent.com/Santainetwork/trust-positif-mirror/main/domains.txt
EOF
fi

[ ! -f "$WHITELIST_FILE" ] && touch "$WHITELIST_FILE"
[ ! -f "$CUSTOM_BL_FILE" ] && touch "$CUSTOM_BL_FILE"

# Cek binary builder
if [ ! -f "$BUILDER_BIN" ]; then
    if [ -f "/home/arcelo/dnsdist-edge/tools/trust-builder/trust-builder" ]; then
        cp "/home/arcelo/dnsdist-edge/tools/trust-builder/trust-builder" "$BUILDER_BIN"
        chmod 0755 "$BUILDER_BIN"
    else
        echo "[!] trust-builder binary tidak ditemukan di $BUILDER_BIN"
        exit 1
    fi
fi

echo "[*] [$(date '+%Y-%m-%d %H:%M:%S')] Memulai build database CDB Central Master..."

# Kumpulkan URL dari sources.txt
URLS=""
while IFS= read -r line || [ -n "$line" ]; do
    line=$(echo "$line" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
    [[ "$line" =~ ^#.*$ ]] && continue
    [ -z "$line" ] && continue
    if [ -z "$URLS" ]; then
        URLS="$line"
    else
        URLS="$URLS,$line"
    fi
done < "$SOURCES_FILE"

TMP_OUT="/tmp/trust_master_build.tmp"
rm -f "$TMP_OUT"

BUILD_ARGS=("-o" "$TMP_OUT")
[ -f "$WHITELIST_FILE" ] && BUILD_ARGS+=("-w" "$WHITELIST_FILE")
[ -n "$URLS" ] && BUILD_ARGS+=("-u" "$URLS")

# Tambahkan custom local blacklist jika ada isi
if [ -s "$CUSTOM_BL_FILE" ]; then
    BUILD_ARGS+=("$CUSTOM_BL_FILE")
fi

echo "[*] Menjalankan trust-builder..."
"$BUILDER_BIN" "${BUILD_ARGS[@]}"

if [ ! -f "$TMP_OUT" ]; then
    echo "[!] Kompilasi gagal: file output $TMP_OUT tidak terbentuk"
    exit 1
fi

FILE_SIZE=$(stat -c %s "$TMP_OUT" 2>/dev/null || stat -f %z "$TMP_OUT" 2>/dev/null || echo 0)
if [ "$FILE_SIZE" -lt 2048 ]; then
    echo "[!] Ukuran file terlalu kecil ($FILE_SIZE bytes), dibatalkan demi keamanan."
    rm -f "$TMP_OUT"
    exit 1
fi

# Content-Addressed Storage
SHA=$(sha256sum "$TMP_OUT" | awk '{print $1}')
FINAL_HASHED="${SERVE_DIR}/trust.${SHA}.db"
FINAL_LINK="${SERVE_DIR}/trust.db"
MANIFEST_FILE="${SERVE_DIR}/manifest.json"

mv "$TMP_OUT" "$FINAL_HASHED"
chmod 0644 "$FINAL_HASHED"

# Atomic symlink update
ln -sfn "trust.${SHA}.db" "${FINAL_LINK}.tmp"
mv "${FINAL_LINK}.tmp" "$FINAL_LINK"

# Hitung versi manifest (jumlah DB historis)
VERSION_COUNT=$(ls -1 "${SERVE_DIR}"/trust.*.db 2>/dev/null | wc -l)
BUILT_AT=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

cat > "${MANIFEST_FILE}.tmp" << JSONEOF
{
  "version": ${VERSION_COUNT:-1},
  "sha256": "$SHA",
  "size": $FILE_SIZE,
  "built_at": "$BUILT_AT",
  "download_url": "/files/trust.db"
}
JSONEOF
mv "${MANIFEST_FILE}.tmp" "$MANIFEST_FILE"
chmod 0644 "$MANIFEST_FILE"

# Jika DNSDist juga aktif di server master ini, update local database
if [ -d "/var/lib/dnsdist" ]; then
    ln -sfn "$FINAL_HASHED" "/var/lib/dnsdist/blacklist.db.tmp"
    mv "/var/lib/dnsdist/blacklist.db.tmp" "/var/lib/dnsdist/blacklist.db"
    echo "[+] Local DNSDist database disinkronkan ke versi terbaru ($SHA)."
fi

echo "[✓] Build Selesai! Versi: #$VERSION_COUNT | Hash: ${SHA:0:16}... | Ukuran: $(du -h "$FINAL_HASHED" | awk '{print $1}')"
