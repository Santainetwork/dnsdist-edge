#!/bin/bash
# ============================================================
# DNSDist Edge Node - Auto Setup Script (Native OS)
# Versi: 2.3.0
# Terinspirasi dari proyek Trust-NG
# ============================================================

set -e

# --- Versi Script ---
SCRIPT_VERSION="2.7.0"

# --- Path Standar Produksi (Sumber Kebenaran Tunggal) ---
CONF_DIR="/etc/dnsdist"
CERTS_DIR="${CONF_DIR}/certs"
DB_DIR="/var/lib/dnsdist"
DB_FILE="${DB_DIR}/blacklist.db"
SCRIPT_UPDATE="/usr/local/bin/update-blacklist.sh"
CONFIG_SAVE_FILE="${CONF_DIR}/node.conf"
DNSDIST_CONF="${CONF_DIR}/dnsdist.conf"
UPSTREAMS_CONF="${CONF_DIR}/upstreams.conf"

# --- Default Variabel ---
EDGE_DIR=$(pwd)
CENTRAL_DB_URL="http://central-manager.local/blacklist.db"
PANEL_RELEASE_URL="${PANEL_RELEASE_URL:-https://github.com/Santainetwork/dnsdist-edge/releases/latest/download/dnsdist-panel}"
WITH_PANEL=${WITH_PANEL:-true}
WEBSERVER_PASSWORD="trust-ng-admin"
WEBSERVER_APIKEY="trust-ng-apikey-changeme"

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# --- Deteksi DNSDist user ---
detect_dnsdist_user() {
    if id -u _dnsdist >/dev/null 2>&1; then
        DNSDIST_USER="_dnsdist"
    elif id -u dnsdist >/dev/null 2>&1; then
        DNSDIST_USER="dnsdist"
    else
        DNSDIST_USER="root"
    fi
}

show_help() {
    echo -e "${CYAN}"
    echo "  ████████╗██████╗ ██╗   ██╗███████╗████████╗   ███████╗██████╗  ██████╗ ███████╗"
    echo "  ╚══██╔══╝██╔══██╗██║   ██║██╔════╝╚══██╔══╝   ██╔════╝██╔══██╗██╔════╝ ██╔════╝"
    echo "     ██║   ██████╔╝██║   ██║███████╗   ██║      █████╗  ██║  ██║██║  ███╗█████╗  "
    echo "     ██║   ██╔══██╗██║   ██║╚════██║   ██║      ██╔══╝  ██║  ██║██║   ██║██╔══╝  "
    echo "     ██║   ██║  ██║╚██████╔╝███████║   ██║      ███████╗██████╔╝╚██████╔╝███████╗"
    echo "     ╚═╝   ╚═╝  ╚═╝ ╚═════╝ ╚══════╝   ╚═╝      ╚══════╝╚═════╝  ╚═════╝ ╚══════╝"
    echo -e "${NC}"
    echo -e "  High Performance DNS Filtering Edge Node Installer v${SCRIPT_VERSION}"
    echo -e "  Terinspirasi dari proyek Trust-NG (https://github.com/trust-ng-replica)\n"
    
    echo "Penggunaan: sudo ./setup-edge.sh [opsi]"
    echo ""
    echo "Opsi Utama:"
    echo "  -i, --install         Install DNSDist, sertifikat, dan konfigurasi Edge Node"
    echo "  -u, --url <URL>       Set URL Central Manager untuk sinkronisasi blacklist.db"
    echo "  --master-url <URL>    Set URL Central Master untuk telemetri terpusat"
    echo "  --enroll-token <TOK>  Token pendaftaran node ke master cluster"
    echo "  --node-name <NAMA>    Nama node edge di dashboard cluster (default: hostname)"
    echo "  -s, --sync-only       Jalankan sinkronisasi database manual sekarang"
    echo "  -f, --force-update    Sama seperti sinkronisasi manual, tetapi melewati cache"
    echo "      --set-upstream    Ubah upstream DNS tanpa install ulang"
    echo "      --set-rpz         Ubah IP Sinkhole RPZ"
    echo "      --set-cdb-sources   Ubah daftar sumber CDB (central, mirror, peer) dipisah koma"
    echo "      --with-panel        Pasang/aktifkan DNSDist Panel (default: aktif)"
    echo "      --no-panel          Lewati pemasangan DNSDist Panel"
    echo "  -c, --check-config    Periksa status dan validitas konfigurasi saat ini"
    echo "      --update-config   Perbarui setting Mode, RPZ, dan Upstream secara interaktif"
    echo "      --upgrade         Upgrade script dan config ke versi terbaru (migrasi otomatis)"
    echo "      --password <PWD>  Set sandi Web Console & API"
    echo "      --apikey <KEY>    Set API Key untuk Web API"
    echo "      --set-webserver   Terapkan perubahan sandi/apikey baru ke dnsdist.conf"
    echo "  -V, --version         Tampilkan versi script"
    echo "  -h, --help            Tampilkan menu bantuan ini"
    echo "      --uninstall       Hapus instalasi DNSDist beserta konfigurasi Trust-NG"
    echo ""
    echo "Contoh:"
    echo "  ./setup-edge.sh --install --url http://central-manager.local:8080/files/trust.db"
    echo "  ./setup-edge.sh --sync-only"
    echo "  ./setup-edge.sh --upgrade"
    exit 0
}

check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        echo -e "${RED}[!] Skrip ini harus dijalankan sebagai root (Gunakan sudo).${NC}"
        exit 1
    fi
}

# --- Simpan konfigurasi user ke file agar bisa direstore saat reinstall/upgrade ---
save_config() {
    mkdir -p "$CONF_DIR"
    cat > "$CONFIG_SAVE_FILE" <<EOF
# Trust-NG Edge Node - Saved Config (auto-generated)
# Jangan edit manual kecuali Anda tahu apa yang Anda lakukan.
SAVED_VERSION="$SCRIPT_VERSION"
SAVED_CENTRAL_DB_URL="$CENTRAL_DB_URL"
SAVED_CDB_SOURCES="${CDB_SOURCES:-}"
SAVED_UPSTREAM_DNS="$UPSTREAM_DNS"
SAVED_BLOCK_MODE="${CHOSEN_MODE:-rpz}"
SAVED_RPZ_IPS="$RPZ_IPS"
SAVED_CERT_MODE="${CERT_MODE:-3}"
SAVED_CERT_DOMAIN="${CERT_DOMAIN:-}"
SAVED_CERT_EMAIL="${CERT_EMAIL:-}"
SAVED_WEBSERVER_PASSWORD="${WEBSERVER_PASSWORD}"
SAVED_WEBSERVER_APIKEY="${WEBSERVER_APIKEY}"
SAVED_MASTER_URL="${MASTER_URL:-}"
SAVED_ENROLL_TOKEN="${ENROLL_TOKEN:-}"
SAVED_NODE_NAME="${NODE_NAME:-}"
SAVED_INSTALL_DATE="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
EOF
    echo -e "${GREEN}[✓] Konfigurasi disimpan ke $CONFIG_SAVE_FILE (v${SCRIPT_VERSION})${NC}"
}

# --- Baca konfigurasi yang disimpan sebelumnya ---
load_config() {
    if [ -f "$CONFIG_SAVE_FILE" ]; then
        echo -e "${CYAN}[*] Ditemukan konfigurasi lama di $CONFIG_SAVE_FILE${NC}"
        # Tampilkan info penting saja
        echo "---"
        grep -E "^SAVED_(VERSION|CENTRAL_DB_URL|BLOCK_MODE|UPSTREAM_DNS|RPZ_IPS)" "$CONFIG_SAVE_FILE" | sed 's/^SAVED_/  /'
        echo "---"
        echo ""
        read -p "Gunakan konfigurasi yang tersimpan ini? (Y/n): " use_saved
        if [ -z "$use_saved" ] || [ "$use_saved" = "Y" ] || [ "$use_saved" = "y" ]; then
            # shellcheck source=/dev/null
            source "$CONFIG_SAVE_FILE"
            [ -n "$SAVED_CENTRAL_DB_URL" ] && [ "$URL_EXPLICIT" != true ] && CENTRAL_DB_URL="$SAVED_CENTRAL_DB_URL"
            [ -n "$SAVED_CDB_SOURCES" ] && CDB_SOURCES="$SAVED_CDB_SOURCES"
            [ -n "$SAVED_UPSTREAM_DNS" ] && UPSTREAM_DNS="$SAVED_UPSTREAM_DNS"
            [ -n "$SAVED_BLOCK_MODE" ] && CHOSEN_MODE="$SAVED_BLOCK_MODE"
            [ -n "$SAVED_RPZ_IPS" ] && RPZ_IPS="$SAVED_RPZ_IPS"
            [ -n "$SAVED_CERT_MODE" ] && CERT_MODE="$SAVED_CERT_MODE"
            [ -n "$SAVED_CERT_DOMAIN" ] && CERT_DOMAIN="$SAVED_CERT_DOMAIN"
            [ -n "$SAVED_CERT_EMAIL" ] && CERT_EMAIL="$SAVED_CERT_EMAIL"
            [ -n "$SAVED_WEBSERVER_PASSWORD" ] && [ "$PASSWORD_EXPLICIT" != true ] && WEBSERVER_PASSWORD="$SAVED_WEBSERVER_PASSWORD"
            [ -n "$SAVED_WEBSERVER_APIKEY" ] && [ "$APIKEY_EXPLICIT" != true ] && WEBSERVER_APIKEY="$SAVED_WEBSERVER_APIKEY"
            [ -n "$SAVED_MASTER_URL" ] && [ "$MASTER_URL_EXPLICIT" != true ] && MASTER_URL="$SAVED_MASTER_URL"
            [ -n "$SAVED_ENROLL_TOKEN" ] && [ "$ENROLL_TOKEN_EXPLICIT" != true ] && ENROLL_TOKEN="$SAVED_ENROLL_TOKEN"
            [ -n "$SAVED_NODE_NAME" ] && [ "$NODE_NAME_EXPLICIT" != true ] && NODE_NAME="$SAVED_NODE_NAME"
            echo -e "${GREEN}[✓] Konfigurasi lama berhasil dimuat.${NC}"
            return 0
        fi
    fi
    return 1
}

# --- Baca konfigurasi yang disimpan secara senyap (tanpa prompt) ---
load_config_silent() {
    if [ -f "$CONFIG_SAVE_FILE" ]; then
        # shellcheck source=/dev/null
        source "$CONFIG_SAVE_FILE"
        [ -n "$SAVED_CENTRAL_DB_URL" ] && CENTRAL_DB_URL="$SAVED_CENTRAL_DB_URL"
        [ -n "$SAVED_CDB_SOURCES" ] && CDB_SOURCES="$SAVED_CDB_SOURCES"
        [ -n "$SAVED_UPSTREAM_DNS" ] && UPSTREAM_DNS="$SAVED_UPSTREAM_DNS"
        [ -n "$SAVED_BLOCK_MODE" ] && CHOSEN_MODE="$SAVED_BLOCK_MODE"
        [ -n "$SAVED_RPZ_IPS" ] && RPZ_IPS="$SAVED_RPZ_IPS"
        [ -n "$SAVED_CERT_MODE" ] && CERT_MODE="$SAVED_CERT_MODE"
        [ -n "$SAVED_CERT_DOMAIN" ] && CERT_DOMAIN="$SAVED_CERT_DOMAIN"
        [ -n "$SAVED_CERT_EMAIL" ] && CERT_EMAIL="$SAVED_CERT_EMAIL"
        [ -n "$SAVED_WEBSERVER_PASSWORD" ] && WEBSERVER_PASSWORD="$SAVED_WEBSERVER_PASSWORD"
        [ -n "$SAVED_WEBSERVER_APIKEY" ] && WEBSERVER_APIKEY="$SAVED_WEBSERVER_APIKEY"
        [ -n "$SAVED_MASTER_URL" ] && MASTER_URL="$SAVED_MASTER_URL"
        [ -n "$SAVED_ENROLL_TOKEN" ] && ENROLL_TOKEN="$SAVED_ENROLL_TOKEN"
        [ -n "$SAVED_NODE_NAME" ] && NODE_NAME="$SAVED_NODE_NAME"
    fi
    return 0
}

# --- Migrasi: Perbaiki config lama yang salah path / format ---
do_migrate() {
    echo -e "${CYAN}[*] Memeriksa dan memperbaiki konfigurasi lama...${NC}"
    local migrated=false

    # 1. Fix path blacklist.db di dnsdist.conf
    if [ -f "$DNSDIST_CONF" ]; then
        # Fix: /etc/dnsdist/blacklist.db -> /var/lib/dnsdist/blacklist.db
        if grep -q "newCDBKVStore('/etc/dnsdist/blacklist.db'" "$DNSDIST_CONF"; then
            sed -i "s|newCDBKVStore('/etc/dnsdist/blacklist.db'|newCDBKVStore('${DB_FILE}'|g" "$DNSDIST_CONF"
            echo -e "  ${GREEN}[✓] Diperbaiki: path CDB dari /etc/dnsdist/ ke ${DB_DIR}/${NC}"
            migrated=true
        fi

        # Fix: /etc/dnsdist/dnsdist.db -> /var/lib/dnsdist/blacklist.db
        if grep -q "newCDBKVStore('/etc/dnsdist/dnsdist.db'" "$DNSDIST_CONF"; then
            sed -i "s|newCDBKVStore('/etc/dnsdist/dnsdist.db'|newCDBKVStore('${DB_FILE}'|g" "$DNSDIST_CONF"
            echo -e "  ${GREEN}[✓] Diperbaiki: path CDB dari dnsdist.db ke blacklist.db${NC}"
            migrated=true
        fi

        # Fix: path lain yang mengarah ke tempat salah
        if grep -q "newCDBKVStore('/root/" "$DNSDIST_CONF"; then
            sed -i "s|newCDBKVStore('/root/[^']*'|newCDBKVStore('${DB_FILE}'|g" "$DNSDIST_CONF"
            echo -e "  ${GREEN}[✓] Diperbaiki: path CDB dari /root/... ke ${DB_FILE}${NC}"
            migrated=true
        fi
    fi

    # 2. Fix update-blacklist.sh jika masih pakai path development
    if [ -f "$SCRIPT_UPDATE" ]; then
        if grep -q 'DB_FILE="/root/' "$SCRIPT_UPDATE"; then
            sed -i "s|DB_FILE=\"/root/[^\"]*\"|DB_FILE=\"${DB_FILE}\"|g" "$SCRIPT_UPDATE"
            echo -e "  ${GREEN}[✓] Diperbaiki: DB_FILE di update-blacklist.sh${NC}"
            migrated=true
        fi
        if grep -q 'DB_FILE="/etc/dnsdist/' "$SCRIPT_UPDATE"; then
            sed -i "s|DB_FILE=\"/etc/dnsdist/[^\"]*\"|DB_FILE=\"${DB_FILE}\"|g" "$SCRIPT_UPDATE"
            echo -e "  ${GREEN}[✓] Diperbaiki: DB_FILE di update-blacklist.sh dari /etc/dnsdist/${NC}"
            migrated=true
        fi
    fi

    # 3. Pindahkan file DB yang salah tempat
    for old_db in /etc/dnsdist/blacklist.db /etc/dnsdist/dnsdist.db /etc/dnsdist/trust.db; do
        if [ -f "$old_db" ] && [ "$old_db" != "$DB_FILE" ]; then
            local old_size new_size
            old_size=$(stat -c%s "$old_db" 2>/dev/null || echo 0)
            new_size=$(stat -c%s "$DB_FILE" 2>/dev/null || echo 0)
            # Pindahkan hanya jika file lama lebih besar (lebih baru)
            if [ "$old_size" -gt "$new_size" ]; then
                echo -e "  ${YELLOW}[*] Memindahkan $old_db (${old_size} bytes) -> $DB_FILE${NC}"
                mv "$old_db" "$DB_FILE"
                migrated=true
            else
                echo -e "  ${YELLOW}[*] Menghapus file DB lama: $old_db${NC}"
                rm -f "$old_db"
            fi
        fi
    done

    # 4. Fix ownership
    detect_dnsdist_user
    mkdir -p "$DB_DIR"
    chown "${DNSDIST_USER}:${DNSDIST_USER}" "$DB_DIR"
    [ -f "$DB_FILE" ] && chown "${DNSDIST_USER}:${DNSDIST_USER}" "$DB_FILE"
    chown -R "${DNSDIST_USER}:${DNSDIST_USER}" "$CONF_DIR" 2>/dev/null || true

    if [ "$migrated" = true ]; then
        echo -e "${GREEN}[✓] Migrasi selesai. Konfigurasi lama telah diperbaiki.${NC}"
    else
        echo -e "${GREEN}[✓] Tidak ada yang perlu diperbaiki. Semua path sudah benar.${NC}"
    fi
}

# --- Upgrade: update script + migrasi + restart ---
do_upgrade() {
    echo -e "\n${CYAN}=== Upgrade Trust-NG Edge ke v${SCRIPT_VERSION} ===${NC}"

    # Cek versi lama
    local old_version="unknown"
    if [ -f "$CONFIG_SAVE_FILE" ]; then
        # shellcheck source=/dev/null
        source "$CONFIG_SAVE_FILE"
        old_version="${SAVED_VERSION:-unknown}"
    fi
    echo "[*] Versi terpasang : v${old_version}"
    echo "[*] Versi baru      : v${SCRIPT_VERSION}"

    if [ "$old_version" = "$SCRIPT_VERSION" ]; then
        echo -e "${GREEN}[✓] Sudah menggunakan versi terbaru.${NC}"
    fi

    # 1. Copy script update-blacklist.sh terbaru
    if [ -f "$EDGE_DIR/update-blacklist.sh" ]; then
        echo "[*] Mengupdate $SCRIPT_UPDATE..."
        cp "$EDGE_DIR/update-blacklist.sh" "$SCRIPT_UPDATE"
        chmod +x "$SCRIPT_UPDATE"
        echo -e "  ${GREEN}[✓] update-blacklist.sh -> v${SCRIPT_VERSION}${NC}"
    fi

    # 2. Copy dnsdist.conf terbaru (jaga setting lama)
    if [ -f "$EDGE_DIR/dnsdist.conf" ] && [ -f "$DNSDIST_CONF" ]; then
        echo "[*] Mengupdate konfigurasi dnsdist.conf..."
        # Backup dulu
        cp "$DNSDIST_CONF" "${DNSDIST_CONF}.bak.$(date +%Y%m%d%H%M%S)"

        # Ambil setting lama dari config yang berjalan
        local old_block_mode old_sinkhole_ips
        old_block_mode=$(grep "^BLOCK_MODE" "$DNSDIST_CONF" | cut -d"'" -f2 2>/dev/null || echo "rpz")
        old_sinkhole_ips=$(grep "^SINKHOLE_IPS" "$DNSDIST_CONF" | sed "s/.*= //" 2>/dev/null || echo "{'10.10.10.10'}")

        # Copy config baru
        cp "$EDGE_DIR/dnsdist.conf" "$DNSDIST_CONF"

        # Terapkan kembali setting lama — pakai python3 agar IPv6 aman
        python3 - "$DNSDIST_CONF" "$old_block_mode" "$old_sinkhole_ips" <<'PYEOF'
import sys, re, os
path, mode, sinkhole = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path) as f: c = f.read()
c = re.sub(r"^BLOCK_MODE = '.*'", f"BLOCK_MODE = '{mode}'", c, flags=re.MULTILINE)
c = re.sub(r'^SINKHOLE_IPS = \{[^}]*\}', f'SINKHOLE_IPS = {sinkhole}', c, flags=re.MULTILINE)
tmp = path + '.tmp'
with open(tmp,'w') as f: f.write(c)
os.replace(tmp, path)
PYEOF
        echo -e "  ${GREEN}[✓] dnsdist.conf diupdate (mode: ${old_block_mode})${NC}"
    fi

    # 3. Jalankan migrasi otomatis
    do_migrate

    # 4. Set/restore webserver credentials & update node.conf
    do_set_webserver

    # 4.5 Sinkronisasi Database Awal
    echo -e "\n${CYAN}[*] Menjalankan sinkronisasi database awal...${NC}"
    export CENTRAL_DB_URL
    bash "$SCRIPT_UPDATE" || echo -e "${YELLOW}[!] Peringatan: Sinkronisasi awal gagal. Pastikan Central Manager URL ($CENTRAL_DB_URL) dapat diakses.${NC}"
    [ -f "$DB_FILE" ] && chown "${DNSDIST_USER}:${DNSDIST_USER}" "$DB_FILE" 2>/dev/null || true

    # 5. Restart DNSDist
    if systemctl is-active --quiet dnsdist; then
        systemctl restart dnsdist
        echo -e "${GREEN}[+] Service dnsdist telah direstart.${NC}"
    fi

    # 5.5 Update/Pasang Panel jika sebelumnya sudah terpasang atau jika --with-panel diaktifkan
    if [ -f "/usr/local/bin/dnsdist-panel" ] || [ "$WITH_PANEL" = true ]; then
        WITH_PANEL=true
        do_install_panel
    fi

    echo -e "\n${GREEN}============================================================${NC}"
    echo -e "${GREEN} Upgrade ke v${SCRIPT_VERSION} Berhasil! 🚀${NC}"
    echo -e "${GREEN}============================================================${NC}\n"
}

do_set_upstream() {
    echo -e "${CYAN}=== Mengonfigurasi Upstream DNS ===${NC}"
    if [ -z "$UPSTREAM_DNS" ]; then
        echo -e "${RED}[!] Harap berikan IP upstream, misal: --set-upstream \"1.1.1.1, 8.8.8.8\"${NC}"
        exit 1
    fi
    mkdir -p "$CONF_DIR"
    echo "-- Auto-generated upstreams (v${SCRIPT_VERSION})" > "$UPSTREAMS_CONF"
    
    local OLD_IFS="$IFS"
    IFS=','
    local entries=()
    read -ra entries <<< "$UPSTREAM_DNS"
    IFS="$OLD_IFS"
    
    for entry in "${entries[@]}"; do
        local clean_ip
        clean_ip=$(echo "$entry" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
        [ -z "$clean_ip" ] && continue

        if [[ "$clean_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:[0-9]+$ ]]; then
            echo "newServer({ address = '${clean_ip}', pool = 'default' })" >> "$UPSTREAMS_CONF"
        elif [[ "$clean_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "newServer({ address = '${clean_ip}:53', pool = 'default' })" >> "$UPSTREAMS_CONF"
        elif [[ "$clean_ip" =~ ^\[.*\]:[0-9]+$ ]]; then
            echo "newServer({ address = '${clean_ip}', pool = 'default' })" >> "$UPSTREAMS_CONF"
        elif [[ "$clean_ip" =~ .*:.* ]]; then
            local bare_ipv6
            bare_ipv6=$(echo "$clean_ip" | sed 's/[][]//g')
            echo "newServer({ address = '[${bare_ipv6}]:53', pool = 'default' })" >> "$UPSTREAMS_CONF"
        else
            echo -e "${YELLOW}[!] Upstream '${clean_ip}' tidak dikenali formatnya, dilewati.${NC}"
        fi
    done
    
    echo -e "${GREEN}[+] Konfigurasi upstream ($UPSTREAMS_CONF) telah diperbarui.${NC}"
    cat "$UPSTREAMS_CONF"
    if systemctl is-active --quiet dnsdist; then
        systemctl restart dnsdist
        echo -e "${GREEN}[+] Service dnsdist telah direstart.${NC}"
    fi
}

# _rpz_normalize: ubah RPZ_IPS (koma/spasi campur) jadi array bash RPZ_IPS_ARR
# Support IPv4, IPv4:port, IPv6 (dengan atau tanpa []), IPv6:port [::1]:53
_rpz_normalize() {
    RPZ_IPS_ARR=()
    # Ganti koma+spasi jadi newline, lalu baca per baris
    local normalized
    normalized=$(echo "$RPZ_IPS" | tr ',' '\n' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' | grep -v '^$')
    while IFS= read -r entry; do
        [ -z "$entry" ] && continue
        # Validasi: IPv4, IPv4:port, bare IPv6, [IPv6], [IPv6]:port
        if echo "$entry" | grep -qE \
            '^([0-9]{1,3}\.){3}[0-9]{1,3}(:[0-9]+)?$|^\[?[0-9a-fA-F:]+\]?(:[0-9]+)?$'; then
            RPZ_IPS_ARR+=("$entry")
        else
            echo -e "${YELLOW}[!] IP tidak valid, dilewati: '$entry'${NC}"
        fi
    done <<< "$normalized"
}

# _rpz_build_lua: build string Lua table dari RPZ_IPS_ARR
_rpz_build_lua() {
    local lua="{"
    local first=true
    for ip in "${RPZ_IPS_ARR[@]}"; do
        if [ "$first" = true ]; then
            lua="$lua'$ip'"
            first=false
        else
            lua="$lua, '$ip'"
        fi
    done
    lua="$lua}"
    echo "$lua"
}

# _rpz_patch_conf: tulis SINKHOLE_IPS ke dnsdist.conf dengan python3 (regex-safe untuk IPv6)
_rpz_patch_conf() {
    local lua_val="$1"
    python3 - "$DNSDIST_CONF" "$lua_val" <<'PYEOF'
import sys, re
path, val = sys.argv[1], sys.argv[2]
with open(path, 'r') as f:
    content = f.read()
content = re.sub(r"^BLOCK_MODE = '.*'", "BLOCK_MODE = 'rpz'", content, flags=re.MULTILINE)
content = re.sub(r'^SINKHOLE_IPS = \{[^}]*\}', 'SINKHOLE_IPS = ' + val, content, flags=re.MULTILINE)
tmp = path + '.tmp'
with open(tmp, 'w') as f:
    f.write(content)
import os; os.replace(tmp, path)
print('[python] SINKHOLE_IPS patched OK')
PYEOF
}

do_set_rpz() {
    echo -e "${CYAN}=== Mengonfigurasi RPZ Sinkhole ===${NC}"
    if [ -z "$RPZ_IPS" ]; then
        echo -e "${RED}[!] Harap berikan IP RPZ, misal: --set-rpz \"10.10.10.10, 2001:db8::1\"${NC}"
        exit 1
    fi
    mkdir -p "$CONF_DIR"

    _rpz_normalize
    if [ ${#RPZ_IPS_ARR[@]} -eq 0 ]; then
        echo -e "${RED}[!] Tidak ada IP valid yang diberikan.${NC}"
        exit 1
    fi

    local LUA_SINKHOLE
    LUA_SINKHOLE=$(_rpz_build_lua)

    _rpz_patch_conf "$LUA_SINKHOLE"

    echo -e "${GREEN}[+] RPZ Sinkhole diset ke: $LUA_SINKHOLE${NC}"
    echo -e "${GREEN}[+] IP terdaftar (${#RPZ_IPS_ARR[@]}):${NC}"
    for ip in "${RPZ_IPS_ARR[@]}"; do
        echo -e "    • $ip"
    done

    if systemctl is-active --quiet dnsdist; then
        systemctl restart dnsdist
        echo -e "${GREEN}[+] Service dnsdist telah direstart.${NC}"
    fi
}

do_set_webserver() {
    echo -e "${CYAN}=== Mengonfigurasi Web Server & API Key ===${NC}"
    if [ -f "$DNSDIST_CONF" ]; then
        sed -i "s|password = '[^']*'|password = '${WEBSERVER_PASSWORD}'|g" "$DNSDIST_CONF"
        sed -i "s|apiKey   = '[^']*'|apiKey   = '${WEBSERVER_APIKEY}'|g" "$DNSDIST_CONF"
        echo -e "  ${GREEN}[✓] Web Console Password & API Key telah diset.${NC}"
        
        if [ -d "$CONF_DIR" ]; then
            save_config
        fi
        
        if systemctl is-active --quiet dnsdist; then
            systemctl restart dnsdist
            echo -e "${GREEN}[+] Service dnsdist telah direstart.${NC}"
        fi
    else
        echo -e "${RED}[!] File konfigurasi dnsdist.conf tidak ditemukan.${NC}"
    fi
}

do_check_config() {
    echo -e "\n${CYAN}=== Status Konfigurasi Edge Node ===${NC}"

    # Versi
    local installed_version="unknown"
    if [ -f "$CONFIG_SAVE_FILE" ]; then
        # shellcheck source=/dev/null
        source "$CONFIG_SAVE_FILE"
        installed_version="${SAVED_VERSION:-unknown}"
    fi
    echo "Versi Terpasang: v${installed_version} (script: v${SCRIPT_VERSION})"
    if [ "$installed_version" != "$SCRIPT_VERSION" ]; then
        echo -e "${YELLOW}  ⚠ Versi tidak cocok! Jalankan --upgrade untuk memperbarui.${NC}"
    fi

    echo -n "Mode Blokir   : "
    if [ -f "$DNSDIST_CONF" ]; then
        grep "^BLOCK_MODE" "$DNSDIST_CONF" | cut -d"'" -f2
    else
        echo "N/A (dnsdist.conf tidak ditemukan)"
    fi
    
    echo "IP Sinkhole   :"
    if [ -f "$DNSDIST_CONF" ]; then
        # Ekstrak isi tabel Lua {..} lalu cetak satu IP per baris
        grep "^SINKHOLE_IPS" "$DNSDIST_CONF" \
            | sed "s/.*= {//;s/}//" \
            | tr "," "\n" \
            | sed "s/'//g;s/^[[:space:]]*//;s/[[:space:]]*$//" \
            | grep -v '^$' \
            | while read -r _ip; do echo "    • $_ip"; done
    else
        echo "  N/A"
    fi
    
    echo "Upstream DNS  : "
    if [ -f "$UPSTREAMS_CONF" ]; then
        grep "^newServer" "$UPSTREAMS_CONF" | sed -n "s/.*address = '\([^']*\)'.*/  - \1/p"
    else
        echo -e "  - 1.1.1.1\n  - 8.8.8.8 (default fallback)"
    fi

    echo -n "DB File       : "
    if [ -f "$DB_FILE" ]; then
        echo "$DB_FILE ($(du -h "$DB_FILE" | awk '{print $1}'))"
    else
        echo -e "${RED}TIDAK ADA${NC}"
    fi

    echo -n "DB Path Config: "
    if [ -f "$DNSDIST_CONF" ]; then
        grep "newCDBKVStore" "$DNSDIST_CONF" | grep -oP "'\K[^']*" | head -1
    else
        echo "N/A"
    fi
    
    echo -e "\n${CYAN}=== Memeriksa Validitas File Konfigurasi ===${NC}"
    if command -v dnsdist >/dev/null 2>&1; then
        dnsdist --check-config && echo -e "${GREEN}[✓] Syntax dnsdist.conf valid.${NC}" \
            || echo -e "${RED}[!] Terdapat error pada syntax konfigurasi!${NC}"
    else
        echo -e "${YELLOW}[!] dnsdist belum terinstal.${NC}"
    fi
    
    echo -e "\n${CYAN}=== Status Layanan ===${NC}"
    systemctl is-active --quiet dnsdist && echo -e "DNSDist       : ${GREEN}Aktif${NC}" || echo -e "DNSDist       : ${RED}Mati${NC}"
    systemctl is-active --quiet nginx && echo -e "Nginx (RPZ)   : ${GREEN}Aktif${NC}" || echo -e "Nginx (RPZ)   : ${YELLOW}Mati / Tidak Diinstal${NC}"
    
    echo ""
}

do_update_config() {
    echo -e "\n${CYAN}=== Update Konfigurasi Edge Node ===${NC}"
    echo "Pilih mode pemblokiran DNS untuk Node ini:"
    echo "  1) Mode AdGuard (Privacy) - Null Routing (0.0.0.0 / ::)"
    echo "  2) Mode RPZ (ISP)         - Redirect ke IP Sinkhole"
    read -p "Masukkan pilihan Anda [1/2]: " mode_choice

    if [ "$mode_choice" = "1" ]; then
        sed -i "s|BLOCK_MODE = '.*'|BLOCK_MODE = 'adguard'|g" "$DNSDIST_CONF"
        python3 - "$DNSDIST_CONF" <<'PYEOF'
import sys, re, os
path = sys.argv[1]
with open(path) as f: c = f.read()
c = re.sub(r"^BLOCK_MODE = '.*'", "BLOCK_MODE = 'adguard'", c, flags=re.MULTILINE)
c = re.sub(r'^SINKHOLE_IPS = \{[^}]*\}', "SINKHOLE_IPS = {'0.0.0.0'}", c, flags=re.MULTILINE)
tmp = path + '.tmp'
with open(tmp,'w') as f: f.write(c)
os.replace(tmp, path)
PYEOF
    else
        read -p "Masukkan alamat IP Sinkhole (pisahkan dengan koma): " sink_ip
        if [ -n "$sink_ip" ]; then
            RPZ_IPS="$sink_ip"
            do_set_rpz
        fi
    fi

    echo ""
    read -p "Masukkan IP Upstream DNS (pisahkan dengan koma, biarkan kosong untuk tidak mengubah): " upstreams_in
    if [ -n "$upstreams_in" ]; then
        UPSTREAM_DNS="$upstreams_in"
        do_set_upstream
    fi
    
    echo -e "${GREEN}[+] Konfigurasi berhasil diupdate.${NC}"
    if systemctl is-active --quiet dnsdist; then
        systemctl restart dnsdist
        echo -e "${GREEN}[+] Service dnsdist telah direstart.${NC}"
    fi
}

do_install_nginx() {
    echo -e "${CYAN}=== Menginstal Nginx Web Server untuk Trust Positif ===${NC}"
    apt-get install -y nginx
    
    cat > /var/www/html/index.html <<'EOF'
<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Akses Ditolak - Trust Positif</title>
<style>body{background:#f3f4f6;font-family:sans-serif;color:#1f2937;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;text-align:center}.box{background:#fff;padding:40px;border-radius:12px;box-shadow:0 10px 25px rgba(0,0,0,0.05);max-width:500px}h1{color:#ef4444;font-size:24px;margin-bottom:10px}p{color:#6b7280;font-size:15px;line-height:1.5}</style></head>
<body><div class="box"><h1>🚫 Akses Ditolak</h1><p>Situs web ini telah diblokir karena mengandung konten yang melanggar peraturan perundang-undangan (Trust Positif).</p></div></body></html>
EOF
    
    cat > /etc/nginx/sites-available/default <<'EOF'
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
    root /var/www/html;
    index index.html;
    location / { try_files $uri $uri/ /index.html; }
    error_page 404 /index.html;
}
EOF
    systemctl enable nginx
    systemctl restart nginx
    echo -e "${GREEN}[+] Nginx berhasil dikonfigurasi sebagai halaman blokir.${NC}"
}

create_self_signed() {
    if [ ! -f "$CERTS_DIR/server.key" ]; then
        echo "[*] Membuat Self-Signed Certificate untuk DoT & DoH (Berlaku 10 Tahun)..."
        openssl req -x509 -newkey rsa:4096 \
          -keyout "$CERTS_DIR/server.key" \
          -out "$CERTS_DIR/server.crt" \
          -sha256 -days 3650 -nodes \
          -subj "/C=ID/ST=Jakarta/L=Jakarta/O=Trust-NG Edge/CN=dns.trust-ng.local" >/dev/null 2>&1
    else
        echo -e "${GREEN}[✓] Sertifikat TLS sudah ada, melewati pembuatan.${NC}"
    fi
}

do_install_cert() {
    echo -e "\n${CYAN}=== Konfigurasi Sertifikat TLS (DoH/DoT) ===${NC}"
    
    if [ -n "$CERT_MODE" ]; then
        cert_choice="$CERT_MODE"
        echo "[*] Mode sertifikat telah diset otomatis ke: $cert_choice"
    else
        echo "Pilih jenis sertifikat / DoH & DoT:"
        echo "  1) Self-Signed Certificate (Mudah, cocok untuk IP saja)"
        echo "  2) Let's Encrypt (Sertifikat asli untuk Domain, butuh port 80 terbuka)"
        echo "  3) Matikan fitur DoH & DoT (Hanya DNS Plaintext port 53)"
        read -p "Pilihan Anda [1/2/3] (Default: 3): " cert_choice
    fi
    
    if [ -z "$cert_choice" ]; then
        cert_choice="3"
    fi
    
    # Uncomment baris TLS di dnsdist.conf jika pengguna memilih 1 atau 2
    if [ "$cert_choice" = "1" ] || [ "$cert_choice" = "2" ]; then
        sed -i 's/^-- addTLSLocal/addTLSLocal/g' "$DNSDIST_CONF"
        sed -i 's/^--   provider/  provider/g' "$DNSDIST_CONF"
        sed -i 's/^--   minTLSVersion/  minTLSVersion/g' "$DNSDIST_CONF"
        sed -i 's/^-- })/})/g' "$DNSDIST_CONF"
        sed -i 's/^-- addDOHLocal/addDOHLocal/g' "$DNSDIST_CONF"
    fi
    
    if [ "$cert_choice" = "2" ]; then
        apt-get install -y certbot
        echo ""
        cert_domain="$CERT_DOMAIN"
        if [ -z "$cert_domain" ]; then
            read -p "Masukkan Domain Edge Node (contoh: dns.domain.com): " cert_domain
        fi
        
        cert_email="$CERT_EMAIL"
        if [ -z "$cert_email" ]; then
            read -p "Masukkan Email Anda (untuk notifikasi expired): " cert_email
        fi
        
        systemctl stop nginx 2>/dev/null || true
        
        echo "[*] Meminta sertifikat Let's Encrypt untuk $cert_domain..."
        certbot certonly --standalone --agree-tos --no-eff-email -m "$cert_email" -d "$cert_domain" \
            --pre-hook "systemctl stop nginx 2>/dev/null || true" \
            --post-hook "systemctl start nginx 2>/dev/null || true; systemctl restart dnsdist"
            
        if [ -f "/etc/letsencrypt/live/$cert_domain/fullchain.pem" ]; then
            echo "[*] Menyalin sertifikat Let's Encrypt..."
            cp "/etc/letsencrypt/live/$cert_domain/fullchain.pem" "$CERTS_DIR/server.crt"
            cp "/etc/letsencrypt/live/$cert_domain/privkey.pem" "$CERTS_DIR/server.key"
            
            mkdir -p /etc/letsencrypt/renewal-hooks/deploy
            cat > /etc/letsencrypt/renewal-hooks/deploy/dnsdist.sh <<EOF
#!/bin/bash
cp /etc/letsencrypt/live/$cert_domain/fullchain.pem $CERTS_DIR/server.crt
cp /etc/letsencrypt/live/$cert_domain/privkey.pem $CERTS_DIR/server.key
chown -R ${DNSDIST_USER}:${DNSDIST_USER} $CERTS_DIR 2>/dev/null || true
chmod 600 $CERTS_DIR/server.key 2>/dev/null || true
chmod 644 $CERTS_DIR/server.crt 2>/dev/null || true
systemctl restart dnsdist
EOF
            chmod +x /etc/letsencrypt/renewal-hooks/deploy/dnsdist.sh
            
            echo -e "${GREEN}[+] Let's Encrypt berhasil dipasang!${NC}"
        else
            echo -e "${RED}[!] Let's Encrypt gagal. Kembali menggunakan Self-Signed Certificate...${NC}"
            create_self_signed
        fi
        
        systemctl start nginx 2>/dev/null || true
    elif [ "$cert_choice" = "1" ]; then
        create_self_signed
    else
        echo -e "${GREEN}[*] DoH dan DoT dimatikan. DNSDist hanya akan melayani port 53 (Plaintext).${NC}"
    fi

    # Berikan permission ke user dnsdist
    if [ "$cert_choice" = "1" ] || [ "$cert_choice" = "2" ]; then
        detect_dnsdist_user
        chown -R "${DNSDIST_USER}:${DNSDIST_USER}" "$CERTS_DIR" 2>/dev/null || true
        chmod 600 "$CERTS_DIR/server.key" 2>/dev/null || true
        chmod 644 "$CERTS_DIR/server.crt" 2>/dev/null || true
    fi
}

do_set_cdb_sources() {
    if [ -z "$CDB_SOURCES" ]; then
        echo -e "${RED}[!] Harap berikan daftar URL dipisah koma, misal: --set-cdb-sources "http://central/trust.db,http://mirror/trust.db,http://peer:8084/cdb/blacklist.db"${NC}"
        exit 1
    fi
    echo -e "${CYAN}[*] Mengatur daftar sumber CDB...${NC}"
    echo "[*] Sumber: $CDB_SOURCES"
    # Update/append SAVED_CDB_SOURCES di node.conf
    if grep -q "^SAVED_CDB_SOURCES=" "$CONFIG_SAVE_FILE" 2>/dev/null; then
        sed -i "s|^SAVED_CDB_SOURCES=.*|SAVED_CDB_SOURCES=\"$CDB_SOURCES\"|" "$CONFIG_SAVE_FILE"
    else
        echo "SAVED_CDB_SOURCES=\"$CDB_SOURCES\"" >> "$CONFIG_SAVE_FILE"
    fi
    save_config
    echo -e "${GREEN}[✓] Sumber CDB disimpan. Jalankan sinkronisasi: update-blacklist.sh --force-update${NC}"
}

do_install_panel() {
    echo -e "\n${CYAN}=== [6/6] DNSDist Management Panel ===${NC}"
    local panel_bin="/usr/local/bin/dnsdist-panel"
    if [ "$WITH_PANEL" != "true" ]; then
        echo -e "  ${YELLOW}[i] Panel dilewati (dinonaktifkan via --no-panel)${NC}"
        return
    fi

    # Hentikan service panel sementara jika sedang berjalan untuk mencegah 'Text file busy'
    if systemctl is-active --quiet dnsdist-panel 2>/dev/null; then
        echo "[*] Menghentikan service dnsdist-panel sementara sebelum update binary..."
        systemctl stop dnsdist-panel || true
    fi

    # Deteksi binary panel lokal terlebih dahulu (untuk paket bundle/offline)
    local panel_source=""
    if [ -f "$EDGE_DIR/dnsdist-panel" ]; then
        panel_source="$EDGE_DIR/dnsdist-panel"
    elif [ -f "$EDGE_DIR/tools/dnsdist-panel" ]; then
        panel_source="$EDGE_DIR/tools/dnsdist-panel"
    elif [ -f "$EDGE_DIR/../tools/dnsdist-panel" ]; then
        panel_source="$EDGE_DIR/../tools/dnsdist-panel"
    fi

    local installed=false
    if [ -n "$panel_source" ]; then
        echo -e "  ${GREEN}[*] Menggunakan panel binary lokal: $panel_source${NC}"
        cp "$panel_source" "$panel_bin"
        chmod 0755 "$panel_bin"
        installed=true
    elif command -v aria2c >/dev/null 2>&1; then
        echo "[*] Mengunduh panel binary menggunakan aria2c (8 koneksi paralel)..."
        if aria2c -x 8 -s 8 -k 1M --connect-timeout=15 --timeout=60 --max-tries=3 \
            --dir="$(dirname "$panel_bin")" --out="$(basename "$panel_bin")" \
            --allow-overwrite=true --summary-interval=0 "$PANEL_RELEASE_URL"; then
            chmod 0755 "$panel_bin"
            installed=true
        else
            echo -e "  ${YELLOW}[!] Download via aria2c gagal, mencoba fallback ke curl...${NC}"
            if command -v curl >/dev/null 2>&1 && curl -fsSL --connect-timeout 15 -o "$panel_bin" "$PANEL_RELEASE_URL"; then
                chmod 0755 "$panel_bin"
                installed=true
            else
                echo -e "  ${YELLOW}[!] Gagal mengunduh panel binary. Panel dilewati.${NC}"
            fi
        fi
    elif command -v curl >/dev/null 2>&1; then
        echo "[*] Mengunduh panel binary dari: $PANEL_RELEASE_URL"
        if curl -fsSL --connect-timeout 15 -o "$panel_bin" "$PANEL_RELEASE_URL"; then
            chmod 0755 "$panel_bin"
            installed=true
        else
            echo -e "  ${YELLOW}[!] Gagal mengunduh panel binary. Panel dilewati.${NC}"
        fi
    else
        echo -e "  ${YELLOW}[!] aria2c/curl tidak tersedia dan panel binary lokal tidak ditemukan. Panel dilewati.${NC}"
    fi

    if [ "$installed" = true ]; then
        mkdir -p /var/lib/dnsdist
        detect_dnsdist_user
        chown -R "${DNSDIST_USER}:${DNSDIST_USER}" /var/lib/dnsdist 2>/dev/null || true

        # Pastikan hook safesearch.conf dan dotdoh.conf ada di dnsdist.conf jika node sudah terpasang
        if [ -f "$DNSDIST_CONF" ]; then
            local conf_reloaded=false
            if ! grep -q "dotdoh.conf" "$DNSDIST_CONF"; then
                python3 - "$DNSDIST_CONF" << 'PYEOF'
import sys
path = sys.argv[1]
with open(path) as f: c = f.read()
hook = """
-- DoT/DoH: dikontrol via panel atau edit manual di /etc/dnsdist/dotdoh.conf
local dotdohFile = "/etc/dnsdist/dotdoh.conf"
local _dd = io.open(dotdohFile, "r")
if _dd then
  _dd:close()
  dofile(dotdohFile)
end
"""
if "dotdoh.conf" not in c:
    c = c.replace("addLocal('[::]:53')", "addLocal('[::]:53')\n" + hook)
    with open(path, 'w') as f: f.write(c)
PYEOF
                conf_reloaded=true
            fi
            if ! grep -q "safesearch.conf" "$DNSDIST_CONF"; then
                python3 - "$DNSDIST_CONF" << 'PYEOF'
import sys
path = sys.argv[1]
with open(path) as f: c = f.read()
hook = """
-- [6.5] SAFESEARCH (optional — dikontrol via panel)
local ssFile = "/etc/dnsdist/safesearch.conf"
local _ss = io.open(ssFile, "r")
if _ss then
  _ss:close()
  dofile(ssFile)
end
"""
if "safesearch.conf" not in c:
    c = c.replace("-- [7] FILTERING RULES", hook + "\n-- [7] FILTERING RULES")
    with open(path, 'w') as f: f.write(c)
PYEOF
                conf_reloaded=true
            fi
            if [ "$conf_reloaded" = true ] && systemctl is-active --quiet dnsdist; then
                systemctl restart dnsdist
                echo -e "  ${GREEN}[✓] Hook SafeSearch & DoT/DoH ditambahkan ke dnsdist.conf${NC}"
            fi
        fi

        # Install systemd unit (baris Environment dari template)
        local extra_env=""
        if [ "$MASTER_URL_EXPLICIT" = true ] && [ -n "$MASTER_URL" ]; then
            extra_env="${extra_env}
Environment=PANEL_MASTER_URL=${MASTER_URL}"
        fi
        if [ "$ENROLL_TOKEN_EXPLICIT" = true ] && [ -n "$ENROLL_TOKEN" ]; then
            extra_env="${extra_env}
Environment=PANEL_ENROLL_TOKEN=${ENROLL_TOKEN}"
        fi
        if [ "$NODE_NAME_EXPLICIT" = true ] && [ -n "$NODE_NAME" ]; then
            extra_env="${extra_env}
Environment=PANEL_NODE_NAME=${NODE_NAME}"
        fi

        cat > /etc/systemd/system/dnsdist-panel.service <<UNIT
[Unit]
Description=DNSDist Management Panel
Wants=dnsdist.service
After=network.target dnsdist.service

[Service]
Type=simple
ExecStart=/usr/local/bin/dnsdist-panel
Restart=on-failure
RestartSec=3
Environment=PANEL_ADDR=0.0.0.0:8443
Environment=PANEL_HTTP_ADDR=0.0.0.0:8084
Environment=PANEL_TLS=true
Environment=PANEL_DB=/var/lib/dnsdist/panel.db
Environment=PANEL_SECRET_FILE=/var/lib/dnsdist/panel.secret
Environment=PANEL_CERT=/var/lib/dnsdist/panel-cert.pem
Environment=PANEL_KEY=/var/lib/dnsdist/panel-key.pem
Environment=DNSDIST_CONF=/etc/dnsdist/dnsdist.conf
Environment=DNSDIST_UPSTREAMS=/etc/dnsdist/upstreams.conf${extra_env}
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
        systemctl daemon-reload
        systemctl enable dnsdist-panel >/dev/null 2>&1
        systemctl restart dnsdist-panel || true
        local node_ip
        node_ip=$(hostname -I | awk '{print $1}')
        echo -e "  ${GREEN}[✓] Panel terinstall & aktif (Dual Mode):${NC}"
        echo -e "       • HTTPS : https://${node_ip}:8443 (SSL self-signed)"
        echo -e "       • HTTP  : http://${node_ip}:8084 (Plain HTTP)"
        echo -e "       Password: ${WEBSERVER_PASSWORD}"
    fi
}

do_install() {
    echo -e "\n${CYAN}=== [1/5] Instalasi DNSDist (Debian/Ubuntu) ===${NC}"
    apt-get update
    apt-get install -y curl gnupg ca-certificates openssl dnsdist aria2
    
    detect_dnsdist_user
    
    # === Cek apakah ada konfigurasi tersimpan dari install sebelumnya ===
    local skip_interactive=false
    if load_config; then
        skip_interactive=true
        echo -e "${GREEN}[*] Reinstall menggunakan konfigurasi tersimpan.${NC}"
    fi

    if [ "$skip_interactive" = false ]; then
        echo -e "\n${CYAN}=== [1.5/5] Konfigurasi Mode Operasional Edge ===${NC}"
        echo "Pilih mode pemblokiran DNS untuk Node ini:"
        echo "  1) Mode AdGuard (Privacy) - Null Routing (0.0.0.0 / ::)"
        echo "  2) Mode RPZ (ISP)         - Redirect ke IP Sinkhole (Halaman Peringatan)"
        echo ""
        read -p "Masukkan pilihan Anda [1/2]: " mode_choice

        CHOSEN_MODE="rpz"
        RPZ_IPS="10.10.10.10"

        if [ "$mode_choice" = "1" ]; then
            CHOSEN_MODE="adguard"
            RPZ_IPS="0.0.0.0"
            echo -e "${GREEN}[*] Menggunakan Mode AdGuard (0.0.0.0)${NC}"
        else
            echo ""
            read -p "Apakah Anda ingin menginstal Nginx lokal di Edge Node ini sebagai halaman Trust Positif? (y/n) [n]: " install_nx
            if [ "$install_nx" = "y" ] || [ "$install_nx" = "Y" ]; then
                do_install_nginx
                local_ip=$(hostname -I | awk '{print $1}')
                echo -e "${GREEN}[*] Memilih IP Node ini ($local_ip) sebagai target Sinkhole RPZ otomatis.${NC}"
                RPZ_IPS="$local_ip"
            else
                echo ""
                read -p "Masukkan alamat IP Sinkhole (pisahkan dengan koma jika lebih dari 1): " sink_ip
                if [ -n "$sink_ip" ]; then RPZ_IPS="$sink_ip"; fi
            fi
        fi

        echo ""
        read -p "Masukkan IP Upstream DNS (pisahkan dengan koma, default: 1.1.1.1, 8.8.8.8): " upstreams_in
        if [ -z "$upstreams_in" ]; then
            upstreams_in="1.1.1.1, 8.8.8.8"
        fi
        UPSTREAM_DNS="$upstreams_in"
    fi

    echo -e "\n${CYAN}=== [2/5] Persiapan Konfigurasi & Sertifikat TLS ===${NC}"
    mkdir -p "$CERTS_DIR" "$DB_DIR"
    chown "${DNSDIST_USER}:${DNSDIST_USER}" "$DB_DIR"
    
    if [ ! -f "$EDGE_DIR/dnsdist.conf" ]; then
        echo -e "${RED}[!] File dnsdist.conf tidak ditemukan di $EDGE_DIR.${NC}"
        echo -e "Pastikan Anda menjalankan skrip ini dari dalam folder dnsdist-edge."
        exit 1
    fi

    echo "[*] Menyalin konfigurasi dnsdist.conf..."
    cp "$EDGE_DIR/dnsdist.conf" "$DNSDIST_CONF"
    
    # Set mode dan rpz
    if [ "$CHOSEN_MODE" = "adguard" ]; then
        sed -i "s|BLOCK_MODE = '.*'|BLOCK_MODE = 'adguard'|g" "$DNSDIST_CONF"
        python3 - "$DNSDIST_CONF" <<'PYEOF'
import sys, re, os
path = sys.argv[1]
with open(path) as f: c = f.read()
c = re.sub(r"^BLOCK_MODE = '.*'", "BLOCK_MODE = 'adguard'", c, flags=re.MULTILINE)
c = re.sub(r'^SINKHOLE_IPS = \{[^}]*\}', "SINKHOLE_IPS = {'0.0.0.0'}", c, flags=re.MULTILINE)
tmp = path + '.tmp'
with open(tmp,'w') as f: f.write(c)
os.replace(tmp, path)
PYEOF
    else
        do_set_rpz
    fi
        
    do_set_upstream
    do_set_webserver
    
    do_install_cert

    # Hak akses
    chown -R "${DNSDIST_USER}:${DNSDIST_USER}" "$CONF_DIR"
    [ -f "$CERTS_DIR/server.key" ] && chmod 600 "$CERTS_DIR/server.key" || true
    
    # Resolve path file addon (top-stats, ASN) — cari di addons/ atau bareng setup/
    local top_stats_src="$EDGE_DIR/../addons/top-stats.lua"
    [ ! -f "$top_stats_src" ] && top_stats_src="$EDGE_DIR/top-stats.lua"

    local build_asn_src="$EDGE_DIR/../addons/build-asn-db.sh"
    [ ! -f "$build_asn_src" ] && build_asn_src="$EDGE_DIR/build-asn-db.sh"

    local ipinfo_src="$EDGE_DIR/../addons/asn-toolkit/ipinfo_lite.csv"
    [ ! -f "$ipinfo_src" ] && ipinfo_src="$EDGE_DIR/asn-toolkit/ipinfo_lite.csv"

    echo -e "\n${CYAN}=== [2.5/5] Top Stats Module (Top Queries, Top Clients, Top ASN) ===${NC}"
    if [ -f "$top_stats_src" ]; then
        cp "$top_stats_src" "$CONF_DIR/top-stats.lua"
        chmod 644 "$CONF_DIR/top-stats.lua"
        echo -e "  ${GREEN}[✓] top-stats.lua terinstall di $CONF_DIR/${NC}"
    else
        echo -e "  ${YELLOW}[!] top-stats.lua tidak ditemukan, modul top-stats dilewati.${NC}"
    fi
    if [ -f "$build_asn_src" ]; then
        cp "$build_asn_src" /usr/local/bin/build-asn-db.sh
        chmod +x /usr/local/bin/build-asn-db.sh
        echo -e "  ${GREEN}[✓] build-asn-db.sh terinstall di /usr/local/bin/${NC}"
        # Auto-build database jika ipinfo_lite.csv tersedia
        if [ -f "$ipinfo_src" ] || [ -f "$CONF_DIR/ipinfo_lite.csv" ]; then
            echo "[*] ipinfo_lite.csv terdeteksi, mengompilasi database ASN..."
            if [ -f "$ipinfo_src" ]; then
                cp "$ipinfo_src" "$CONF_DIR/ipinfo_lite.csv"
            fi
            cd "$CONF_DIR" && sh /usr/local/bin/build-asn-db.sh || echo -e "  ${YELLOW}[!] Gagal mengompilasi asn-db.bin, lewati.${NC}"
            cd - >/dev/null 2>&1 || true
        else
            echo -e "  ${YELLOW}[i] ipinfo_lite.csv tidak ditemukan, asn-db.bin dilewati (Top ASN/Clients akan kosong sampai di-build manual).${NC}"
        fi
    fi
    
    echo -e "\n${CYAN}=== [3/5] Menyiapkan Skrip Sinkronisasi (Cronjob) ===${NC}"
    echo "[*] Mengkopi update-blacklist.sh ke $SCRIPT_UPDATE"
    cp "$EDGE_DIR/update-blacklist.sh" "$SCRIPT_UPDATE"
    chmod +x "$SCRIPT_UPDATE"

    CRON_JOB="0 */3 * * * $SCRIPT_UPDATE >> /var/log/dnsdist-sync.log 2>&1"
    if crontab -l 2>/dev/null | grep -q "$SCRIPT_UPDATE"; then
        echo -e "${GREEN}[✓] Cronjob sudah terpasang.${NC}"
    else
        echo "[*] Menambahkan cronjob (Auto-Sync setiap 3 jam)..."
        (crontab -l 2>/dev/null; echo "$CRON_JOB") | crontab -
        echo -e "${GREEN}[✓] Cronjob berhasil ditambahkan.${NC}"
    fi

    echo -e "\n${CYAN}=== [4/5] Sinkronisasi Database Awal ===${NC}"
    # Export URL agar update-blacklist.sh bisa membacanya sebelum node.conf tersimpan
    export CENTRAL_DB_URL
    bash "$SCRIPT_UPDATE" || echo -e "${YELLOW}[!] Peringatan: Sinkronisasi awal gagal. Pastikan Central Manager URL ($CENTRAL_DB_URL) dapat diakses.${NC}"
    [ -f "$DB_FILE" ] && chown "${DNSDIST_USER}:${DNSDIST_USER}" "$DB_FILE" 2>/dev/null || true

    echo -e "\n${CYAN}=== [5/5] Memulai Layanan DNSDist ===${NC}"
    systemctl daemon-reload
    systemctl enable dnsdist >/dev/null 2>&1
    systemctl restart dnsdist
    
    # Simpan konfigurasi untuk reinstall/upgrade berikutnya
    save_config

    echo -e "\n${GREEN}============================================================${NC}"
    echo -e "${GREEN} Setup Edge Node v${SCRIPT_VERSION} Berhasil Selesai! 🚀${NC}"
    echo -e " - Service OS       : systemctl status dnsdist"
    echo -e " - Central DB URL   : $CENTRAL_DB_URL"
    echo -e " - Database File    : $DB_FILE"
    echo -e " - Auto-sync DB     : Setiap 3 Jam"
    echo -e " - Layanan DNS Aktif: UDP/TCP (53), DoT (853), DoH (443)"
    echo -e "${GREEN}============================================================${NC}\n"
}

do_sync() {
    echo -e "${CYAN}[*] Menjalankan sinkronisasi database manual...${NC}"
    if [ -f "$SCRIPT_UPDATE" ]; then
        if [ "$FORCE_SYNC" = true ]; then
            bash "$SCRIPT_UPDATE" --force-update
        else
            bash "$SCRIPT_UPDATE"
        fi
        detect_dnsdist_user
        [ -f "$DB_FILE" ] && chown "${DNSDIST_USER}:${DNSDIST_USER}" "$DB_FILE" 2>/dev/null || true
    else
        echo -e "${RED}[!] Skrip sinkronisasi tidak ditemukan. Harap jalankan instalasi (--install) terlebih dahulu.${NC}"
        exit 1
    fi
}

do_uninstall() {
    echo -e "${YELLOW}[!] MEMULAI PROSES UNINSTALL${NC}"
    read -p "Apakah Anda yakin ingin menghapus DNSDist dan konfigurasinya? (y/N) " confirm
    if [ "$confirm" = "y" ] || [ "$confirm" = "Y" ]; then
        systemctl stop dnsdist || true
        systemctl disable dnsdist || true
        apt-get remove -y --purge dnsdist
        apt-get autoremove -y
        rm -rf "$CONF_DIR" "$DB_DIR"
        rm -f "$SCRIPT_UPDATE"
        crontab -l | grep -v "$SCRIPT_UPDATE" | crontab - || true
        echo -e "${GREEN}[✓] Uninstalasi selesai.${NC}"
    else
        echo "Dibatalkan."
    fi
}

# --- Load Saved Config Silently ---
load_config_silent || true

# --- Parsing Arguments ---
if [ "$#" -eq 0 ]; then
    show_help
fi

INSTALL=false
SYNC_ONLY=false
UNINSTALL=false
UPGRADE=false
URL_EXPLICIT=false
PASSWORD_EXPLICIT=false
APIKEY_EXPLICIT=false
SET_WEBSERVER=false

MASTER_URL_EXPLICIT=false
ENROLL_TOKEN_EXPLICIT=false
NODE_NAME_EXPLICIT=false
MASTER_URL=""
ENROLL_TOKEN=""
NODE_NAME=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help)
            show_help
            ;;
        -V|--version)
            echo "setup-edge.sh v${SCRIPT_VERSION}"
            exit 0
            ;;
        -i|--install)
            INSTALL=true
            ;;
        -s|--sync-only)
            SYNC_ONLY=true
            ;;
        -f|--force-update)
            SYNC_ONLY=true
            FORCE_SYNC=true
            ;;
        -c|--check-config)
            CHECK_CONFIG=true
            ;;
        --update-config)
            UPDATE_CONFIG=true
            ;;
        --upgrade)
            UPGRADE=true
            ;;
        -u|--url)
            if [ -n "$2" ]; then
                CENTRAL_DB_URL="$2"
                URL_EXPLICIT=true
                shift
            else
                echo -e "${RED}[!] Argumen --url membutuhkan sebuah URL.${NC}"
                exit 1
            fi
            ;;
        --master-url)
            if [ -n "$2" ]; then
                MASTER_URL="$2"
                MASTER_URL_EXPLICIT=true
                shift
            else
                echo -e "${RED}[!] Argumen --master-url membutuhkan URL master.${NC}"
                exit 1
            fi
            ;;
        --enroll-token)
            if [ -n "$2" ]; then
                ENROLL_TOKEN="$2"
                ENROLL_TOKEN_EXPLICIT=true
                shift
            else
                echo -e "${RED}[!] Argumen --enroll-token membutuhkan token.${NC}"
                exit 1
            fi
            ;;
        --node-name)
            if [ -n "$2" ]; then
                NODE_NAME="$2"
                NODE_NAME_EXPLICIT=true
                shift
            else
                echo -e "${RED}[!] Argumen --node-name membutuhkan nama.${NC}"
                exit 1
            fi
            ;;
        --uninstall)
            UNINSTALL=true
            ;;
        --set-upstream)
            if [ -z "$2" ] || [[ "$2" == -* ]]; then
                echo -e "${RED}[!] Argumen --set-upstream membutuhkan daftar IP.${NC}"
                exit 1
            fi
            UPSTREAM_DNS=""
            while [ -n "$2" ] && [[ "$2" != -* ]]; do
                if [ -n "$UPSTREAM_DNS" ]; then
                    UPSTREAM_DNS="$UPSTREAM_DNS $2"
                else
                    UPSTREAM_DNS="$2"
                fi
                shift
            done
            UPSTREAM_DNS=$(echo "$UPSTREAM_DNS" | sed 's/,[[:space:]]*$//;s/,$//')
            SET_UPSTREAM=true
            ;;
        --with-panel|--add-panel)
            WITH_PANEL=true
            ;;
        --no-panel)
            WITH_PANEL=false
            ;;
        --set-cdb-sources)
            if [ -z "$2" ] || [[ "$2" == -* ]]; then
                echo -e "${RED}[!] Argumen --set-cdb-sources membutuhkan daftar URL dipisah koma.${NC}"
                exit 1
            fi
            CDB_SOURCES="$2"
            SET_CDB_SOURCES=true
            shift
            ;;
        --set-rpz)
            if [ -z "$2" ] || [[ "$2" == -* ]]; then
                echo -e "${RED}[!] Argumen --set-rpz membutuhkan daftar IP.${NC}"
                echo -e "${YELLOW}    Contoh: --set-rpz \"10.10.10.10,2001:db8::1\"${NC}"
                exit 1
            fi
            # Terima 1 argumen (koma-separated) atau multiple argumen
            RPZ_IPS=""
            while [ -n "$2" ] && [[ "$2" != --* ]]; do
                if [ -n "$RPZ_IPS" ]; then
                    RPZ_IPS="$RPZ_IPS,$2"
                else
                    RPZ_IPS="$2"
                fi
                shift
            done
            SET_RPZ=true
            ;;
        --set-cert)
            if [ -n "$2" ]; then
                CERT_MODE="$2"
                SET_CERT=true
                shift
            else
                echo -e "${RED}[!] Argumen --set-cert membutuhkan opsi (1=Self-Signed, 2=LetsEncrypt, 3=None).${NC}"
                exit 1
            fi
            ;;
        --cert-domain)
            if [ -n "$2" ]; then
                CERT_DOMAIN="$2"
                shift
            fi
            ;;
        --cert-email)
            if [ -n "$2" ]; then
                CERT_EMAIL="$2"
                shift
            fi
            ;;
        --password)
            if [ -n "$2" ]; then
                WEBSERVER_PASSWORD="$2"
                PASSWORD_EXPLICIT=true
                SET_WEBSERVER=true
                shift
            else
                echo -e "${RED}[!] Argumen --password membutuhkan sandi.${NC}"
                exit 1
            fi
            ;;
        --apikey)
            if [ -n "$2" ]; then
                WEBSERVER_APIKEY="$2"
                APIKEY_EXPLICIT=true
                SET_WEBSERVER=true
                shift
            else
                echo -e "${RED}[!] Argumen --apikey membutuhkan key.${NC}"
                exit 1
            fi
            ;;
        --set-webserver)
            SET_WEBSERVER=true
            ;;
        *)
            echo -e "${RED}[!] Opsi tidak dikenali: $1${NC}"
            echo "Gunakan -h atau --help untuk melihat panduan."
            exit 1
            ;;
    esac
    shift
done

check_root

if [ "$UNINSTALL" = true ]; then
    do_uninstall
    exit 0
fi

if [ "$UPGRADE" = true ]; then
    do_upgrade
    exit 0
fi

if [ "$INSTALL" = true ]; then
    do_install
    do_install_panel
    exit 0
fi

if [ "$CHECK_CONFIG" = true ]; then
    do_check_config
    exit 0
fi

if [ "$UPDATE_CONFIG" = true ]; then
    do_update_config
    exit 0
fi

if [ "$SET_UPSTREAM" = true ]; then
    do_set_upstream
fi

if [ "$SET_RPZ" = true ]; then
    do_set_rpz
fi

if [ "$SET_CDB_SOURCES" = true ]; then
    do_set_cdb_sources
fi

if [ "$SET_CERT" = true ]; then
    do_install_cert
fi

if [ "$SET_WEBSERVER" = true ]; then
    do_set_webserver
fi

if [ "$WITH_PANEL" = true ]; then
    do_install_panel
    exit 0
fi

if [ "$SYNC_ONLY" = true ]; then
    do_sync
    exit 0
fi
