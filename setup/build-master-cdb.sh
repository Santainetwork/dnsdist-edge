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
PANEL_BIN="/usr/local/bin/dnsdist-panel"
BUILDER_BIN="/usr/local/bin/trust-builder"

mkdir -p "$MASTER_CONF_DIR" "$SERVE_DIR"

# Inisialisasi default sources jika belum ada
if [ ! -f "$SOURCES_FILE" ]; then
    cat > "$SOURCES_FILE" << 'EOF'
# Daftar URL sumber blacklist (satu per baris)
# 1. Trust Positif Kominfo (Mirror raw domain list):
https://raw.githubusercontent.com/Santainetwork/trust-positif-mirror/main/domains.txt

# 2. AdGuard DNS Filter (opsional)
# https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt
EOF
fi

[ ! -f "$WHITELIST_FILE" ] && touch "$WHITELIST_FILE"
[ ! -f "$CUSTOM_BL_FILE" ] && touch "$CUSTOM_BL_FILE"

echo "[*] [$(date '+%Y-%m-%d %H:%M:%S')] Memulai build database CDB Central Master..."

# Gunakan engine kompilasi bawaan dnsdist-panel jika tersedia
if [ -x "$PANEL_BIN" ]; then
    exec "$PANEL_BIN" \
        -build-now \
        -files-dir "$SERVE_DIR" \
        -sources-file "$SOURCES_FILE" \
        -whitelist-file "$WHITELIST_FILE" \
        -custom-bl-file "$CUSTOM_BL_FILE"
fi

# Fallback ke standalone trust-builder jika dnsdist-panel belum terpasang
if [ -x "$BUILDER_BIN" ]; then
    URLS=""
    while IFS= read -r line || [ -n "$line" ]; do
        line=$(echo "$line" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
        [[ "$line" =~ ^#.*$ ]] && continue
        [ -z "$line" ] && continue
        if [ -z "$URLS" ]; then URLS="$line"; else URLS="$URLS,$line"; fi
    done < "$SOURCES_FILE"

    TMP_OUT="/tmp/trust_master_build.tmp"
    rm -f "$TMP_OUT"

    BUILD_ARGS=("-o" "$TMP_OUT")
    [ -f "$WHITELIST_FILE" ] && BUILD_ARGS+=("-w" "$WHITELIST_FILE")
    [ -n "$URLS" ] && BUILD_ARGS+=("-u" "$URLS")
    [ -s "$CUSTOM_BL_FILE" ] && BUILD_ARGS+=("$CUSTOM_BL_FILE")

    "$BUILDER_BIN" "${BUILD_ARGS[@]}"

    FILE_SIZE=$(stat -c %s "$TMP_OUT" 2>/dev/null || stat -f %z "$TMP_OUT" 2>/dev/null || echo 0)
    if [ "$FILE_SIZE" -lt 2048 ]; then
        echo "[!] Ukuran file terlalu kecil ($FILE_SIZE bytes), dibatalkan."
        rm -f "$TMP_OUT"
        exit 1
    fi

    SHA=$(sha256sum "$TMP_OUT" | awk '{print $1}')
    FINAL_HASHED="${SERVE_DIR}/trust.${SHA}.db"
    FINAL_LINK="${SERVE_DIR}/trust.db"
    MANIFEST_FILE="${SERVE_DIR}/manifest.json"

    mv "$TMP_OUT" "$FINAL_HASHED"
    chmod 0644 "$FINAL_HASHED"
    ln -sfn "trust.${SHA}.db" "${FINAL_LINK}.tmp"
    mv "${FINAL_LINK}.tmp" "$FINAL_LINK"

    # Symlink aktif sudah menunjuk file baru. Hapus hanya file hash lama;
    # file non-hash seperti backup manual tidak disentuh.
    for OLD_DB in "${SERVE_DIR}"/trust.*.db; do
        [ -f "$OLD_DB" ] || continue
        [ "$OLD_DB" = "$FINAL_HASHED" ] && continue
        OLD_HASH=$(basename "$OLD_DB")
        OLD_HASH=${OLD_HASH#trust.}
        OLD_HASH=${OLD_HASH%.db}
        [[ "$OLD_HASH" =~ ^[0-9a-f]{64}$ ]] || continue
        rm -f -- "$OLD_DB"
    done

    VERSION_COUNT=1
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
    echo "[✓] Build Selesai! Versi: #$VERSION_COUNT | Hash: ${SHA:0:16}..."
    exit 0
fi

echo "[!] Tidak ditemukan binary compiler (dnsdist-panel atau trust-builder)."
exit 1
