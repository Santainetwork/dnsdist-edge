#!/bin/bash
# ============================================================
# DNSDist Central Master - Auto Setup Script (Native OS)
# Versi: 1.0.0
# Mendukung mode Standalone (Tanpa DNSDist) atau Hybrid (Dengan DNSDist)
# ============================================================

set -e

SCRIPT_VERSION="3.0.0"
SELF_UPDATE_URL="${SELF_UPDATE_URL:-https://github.com/Santainetwork/dnsdist-edge/releases/latest/download/setup-master.sh}"
MASTER_DIR="${MASTER_DIR:-$(pwd)}"
CONF_DIR="/etc/dnsdist-master"
SERVE_DIR="/var/www/html/files"
BUILD_SCRIPT="/usr/local/bin/build-master-cdb.sh"
BUILDER_BIN="/usr/local/bin/trust-builder"
RPZ_BIN="/usr/local/bin/rpz-master"
RPZ_CONFIG="$CONF_DIR/rpz-master.json"
RPZ_UNIT="/etc/systemd/system/rpz-master.service"
PORT_HTTP="8080"
WITH_DNSDIST=false
WITH_PANEL=false
SOURCE_MODE="feeds"
RPZ_UPSTREAM=""
RPZ_ZONE=""
RPZ_BOOTSTRAP_URL=""
RPZ_DNS_LISTEN="0.0.0.0:5354"
RPZ_CHECK_INTERVAL="15m"
RPZ_TRANSFER_ACL="127.0.0.0/8,::1/128"
RPZ_TSIG_KEY=""
RPZ_TSIG_SECRET_FILE=""
RPZ_TSIG_INSTALLED_SECRET="$CONF_DIR/rpz-upstream.secret"
RPZ_MASTER_RELEASE_URL="${RPZ_MASTER_RELEASE_URL:-https://github.com/Santainetwork/dnsdist-edge/releases/latest/download/rpz-master}"
RPZ_STAGED_BINARY=""
RPZ_BINARY_SOURCE=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

show_help() {
    echo -e "${CYAN}"
    echo "  ============================================================"
    echo "   DNSDist Central Master Server Installer v${SCRIPT_VERSION}"
    echo "   CDB Blacklist Generator & Multi-Edge Distributor"
    echo "  ============================================================"
    echo -e "${NC}"
    echo "Penggunaan: sudo ./setup-master.sh [opsi]"
    echo ""
    echo "Opsi:"
    echo "  -i, --install          Instal dan konfigurasi Central Master Server"
    echo "      --with-dnsdist     Pasang juga DNSDist resolver lokal di Master Server"
    echo "      --no-dnsdist       Jangan pasang DNSDist (murni Central Generator & Server)"
    echo "      --source-mode <MODE>       Sumber data: feeds (default) atau rpz-slave"
    echo "      --rpz-upstream <HOST:PORT> Upstream RPZ (wajib untuk rpz-slave)"
    echo "      --rpz-zone <FQDN>          Zone RPZ (wajib untuk rpz-slave)"
    echo "      --rpz-bootstrap-url <URL>  URL bootstrap domain awal opsional"
    echo "      --rpz-dns-listen <ADDR>    Listen DNS RPZ (default: 0.0.0.0:5354)"
    echo "      --rpz-check-interval <DUR> Interval sinkronisasi (default: 15m)"
    echo "      --rpz-transfer-acl <CIDRS> ACL transfer, dipisahkan koma"
    echo "      --rpz-tsig-key <NAME>      Nama key TSIG"
    echo "      --rpz-tsig-secret-file <PATH> File secret TSIG mode root-only"
    echo "      --port <PORT>      Port HTTP untuk distribusi file CDB (default: 8080)"
    echo "      --with-panel       Pasang DNSDist Panel manajemen"
    echo "      --build-now        Jalankan kompilasi CDB blacklist sekarang"
    echo "      --upgrade          Perbarui installer ke versi terbaru dari GitHub"
    echo "  -h, --help             Tampilkan bantuan ini"
    echo ""
    echo "Contoh:"
    echo "  sudo ./setup-master.sh --install --no-dnsdist --with-panel"
    echo "  sudo ./setup-master.sh --install --with-dnsdist"
    echo "  sudo ./setup-master.sh --build-now"
    exit 0
}

die() {
    echo -e "${RED}[!] $*${NC}" >&2
    return 1
}

valid_address() {
    local value=$1 port
    [[ "$value" =~ ^(\[[0-9A-Fa-f:]+\]|[A-Za-z0-9.-]+):([0-9]+)$ ]] || return 1
    port=${BASH_REMATCH[2]}
    [ "$port" -ge 1 ] 2>/dev/null && [ "$port" -le 65535 ]
}

valid_fqdn() {
    local value=${1%.} label
    [ "$1" != "$value" ] && [ -n "$value" ] && [ ${#value} -le 253 ] || return 1
    IFS=. read -r -a labels <<< "$value"
    for label in "${labels[@]}"; do
        [ ${#label} -le 63 ] || return 1
        [[ "$label" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$ ]] || return 1
    done
}

valid_cidr() {
    local value=$1 address prefix octet
    [[ "$value" == */* ]] || return 1
    address=${value%/*}
    prefix=${value##*/}
    [[ "$prefix" =~ ^[0-9]+$ ]] || return 1
    if [[ "$address" == *:* ]]; then
        [[ "$address" =~ ^[0-9A-Fa-f:]+$ ]] && [ "$prefix" -le 128 ]
        return
    fi
    [ "$prefix" -le 32 ] || return 1
    IFS=. read -r -a octets <<< "$address"
    [ "${#octets[@]}" -eq 4 ] || return 1
    for octet in "${octets[@]}"; do
        [[ "$octet" =~ ^[0-9]+$ ]] && [ "$octet" -le 255 ] || return 1
    done
}

validate_source_settings() {
    local cidr mode
    case "$SOURCE_MODE" in
        feeds) return 0 ;;
        rpz-slave) ;;
        *) die "Source mode tidak valid: $SOURCE_MODE (gunakan feeds atau rpz-slave)"; return 1 ;;
    esac

    [ -n "$RPZ_UPSTREAM" ] || { die "--rpz-upstream wajib untuk rpz-slave."; return 1; }
    valid_address "$RPZ_UPSTREAM" || { die "Alamat --rpz-upstream tidak valid: $RPZ_UPSTREAM"; return 1; }
    valid_fqdn "$RPZ_ZONE" || { die "--rpz-zone harus FQDN dengan titik akhir."; return 1; }
    valid_address "$RPZ_DNS_LISTEN" || { die "Alamat --rpz-dns-listen tidak valid: $RPZ_DNS_LISTEN"; return 1; }
    [[ "$RPZ_CHECK_INTERVAL" =~ ^([0-9]+(ns|us|ms|s|m|h))+$ && "$RPZ_CHECK_INTERVAL" =~ [1-9] ]] || {
        die "Durasi --rpz-check-interval tidak valid: $RPZ_CHECK_INTERVAL"; return 1;
    }
    if [ -n "$RPZ_BOOTSTRAP_URL" ] && [[ ! "$RPZ_BOOTSTRAP_URL" =~ ^https?://[^[:space:]]+$ ]]; then
        die "--rpz-bootstrap-url harus URL HTTP(S)."; return 1
    fi
    IFS=, read -r -a cidrs <<< "$RPZ_TRANSFER_ACL"
    [ "${#cidrs[@]}" -gt 0 ] || { die "--rpz-transfer-acl tidak boleh kosong."; return 1; }
    for cidr in "${cidrs[@]}"; do
        valid_cidr "$cidr" || { die "CIDR transfer tidak valid: $cidr"; return 1; }
    done
    if [ -n "$RPZ_TSIG_KEY" ] || [ -n "$RPZ_TSIG_SECRET_FILE" ]; then
        [ -n "$RPZ_TSIG_KEY" ] && [ -n "$RPZ_TSIG_SECRET_FILE" ] || { die "--rpz-tsig-key dan --rpz-tsig-secret-file harus dipakai bersama."; return 1; }
        valid_fqdn "$RPZ_TSIG_KEY" || { die "--rpz-tsig-key harus FQDN."; return 1; }
        [ -f "$RPZ_TSIG_SECRET_FILE" ] || { die "File secret TSIG tidak ditemukan: $RPZ_TSIG_SECRET_FILE"; return 1; }
        mode=$(stat -c %a "$RPZ_TSIG_SECRET_FILE" 2>/dev/null) || { die "Mode file secret TSIG tidak dapat dibaca."; return 1; }
        [ "${mode: -2}" = 00 ] || { die "File secret TSIG harus root-only (contoh: 0600)."; return 1; }
    fi
}

json_escape() {
    local value=$1
    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    value=${value//$'\n'/\\n}
    value=${value//$'\r'/\\r}
    value=${value//$'\t'/\\t}
    printf '%s' "$value"
}

json_acl() {
    local cidr output="" escaped
    IFS=, read -r -a cidrs <<< "$RPZ_TRANSFER_ACL"
    for cidr in "${cidrs[@]}"; do
        escaped=$(json_escape "$cidr")
        [ -z "$output" ] || output+=", "
        output+="\"$escaped\""
    done
    printf '%s' "$output"
}

render_rpz_config() {
    local output=$1 acl
    acl=$(json_acl)
    umask 077
    cat > "$output" << JSON
{
  "source_mode": "$(json_escape "$SOURCE_MODE")",
  "zone": "$(json_escape "$RPZ_ZONE")",
  "listen_dns": "$(json_escape "$RPZ_DNS_LISTEN")",
  "listen_http": "127.0.0.1:8088",
  "cname_target": "blockpage.komdigi.go.id.",
  "cdb_path": "$SERVE_DIR/trust.db",
  "state_path": "/var/lib/rpz-master/state.json",
  "source_domain_url": "$(json_escape "$RPZ_BOOTSTRAP_URL")",
  "upstream_master": "$(json_escape "$RPZ_UPSTREAM")",
  "transfer_acl": [$acl],
  "tsig_key": "$(json_escape "$RPZ_TSIG_KEY")",
  "tsig_secret_file": "$(json_escape "$([ -n "$RPZ_TSIG_SECRET_FILE" ] && printf '%s' "$RPZ_TSIG_INSTALLED_SECRET")")",
  "tsig_algorithm": "hmac-sha256.",
  "check_interval": "$(json_escape "$RPZ_CHECK_INTERVAL")",
  "max_transfers": 10,
  "raw_domain_file": "/var/lib/rpz-master/domains.txt"
}
JSON
    chmod 0600 "$output"
}

render_rpz_unit() {
    local output=$1
    cat > "$output" << 'UNIT'
[Unit]
Description=DNSDist RPZ Slave and CDB Publisher
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/rpz-master -c /etc/dnsdist-master/rpz-master.json -action serve
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/rpz-master /var/www/html/files

[Install]
WantedBy=multi-user.target
UNIT
    chmod 0644 "$output"
}

render_panel_env() {
    local output=$1 mode=${2:-$SOURCE_MODE} interval=6h
    [ "$mode" = rpz-slave ] && interval=0
    printf 'PANEL_SOURCE_MODE=%s\nPANEL_BUILD_INTERVAL=%s\n' "$mode" "$interval" > "$output"
}

persist_source_mode() {
    local mode=$1
    printf '%s\n' "$mode" > "$CONF_DIR/source-mode"
    render_panel_env "$CONF_DIR/panel.env" "$mode"
    chmod 0644 "$CONF_DIR/source-mode" "$CONF_DIR/panel.env"
}

find_rpz_binary() {
    local candidate
    for candidate in \
        "$MASTER_DIR/rpz-master" \
        "$MASTER_DIR/tools/rpz-master/rpz-master" \
        "$MASTER_DIR/../tools/rpz-master/rpz-master"; do
        [ -x "$candidate" ] && { printf '%s' "$candidate"; return 0; }
    done
    return 1
}

find_rpz_module() {
    local candidate
    for candidate in \
        "$MASTER_DIR/tools/rpz-master" \
        "$MASTER_DIR/../tools/rpz-master" \
        "$MASTER_DIR/rpz-master-src"; do
        [ -f "$candidate/go.mod" ] && [ -f "$candidate/main.go" ] && { printf '%s' "$candidate"; return 0; }
    done
    return 1
}

download_verified_rpz_binary() {
    local output=$1 checksum expected actual filename
    command -v curl >/dev/null 2>&1 || { die "curl diperlukan untuk mengambil binary RPZ terverifikasi."; return 1; }
    checksum="${output}.SHA256SUMS"
    filename=$(basename "$RPZ_MASTER_RELEASE_URL")
    curl -fsSL "$RPZ_MASTER_RELEASE_URL" -o "$output"
    curl -fsSL "$(dirname "$RPZ_MASTER_RELEASE_URL")/SHA256SUMS" -o "$checksum" || {
        rm -f "$output" "$checksum"
        die "SHA256SUMS RPZ tidak tersedia; instalasi dibatalkan."
        return 1
    }
    expected=$(awk -v file="$filename" '$2 == file || $2 == "*" file { print $1; exit }' "$checksum")
    if ! printf '%s\n' "$expected" | grep -Eq '^[[:xdigit:]]{64}$'; then
        rm -f "$output" "$checksum"
        die "Format checksum binary RPZ tidak valid."
        return 1
    fi
    actual=$(sha256sum "$output" | awk '{ print $1 }')
    if [ "$actual" != "$expected" ]; then
        rm -f "$output" "$checksum"
        die "Checksum binary RPZ tidak cocok."
        return 1
    fi
    rm -f "$checksum"
    chmod 0755 "$output"
}

preflight_rpz_binary() {
    local output=$1 source module
    mkdir -p "$(dirname "$output")"
    if source=$(find_rpz_binary); then
        cp "$source" "$output"
        chmod 0755 "$output"
        RPZ_BINARY_SOURCE="bundled"
        return 0
    fi
    if module=$(find_rpz_module); then
        if command -v go >/dev/null 2>&1 && (cd "$module" && CGO_ENABLED=0 go build -trimpath -o "$output" .); then
            chmod 0755 "$output"
            RPZ_BINARY_SOURCE="local-source"
            return 0
        fi
        rm -f "$output"
        echo -e "${YELLOW}[!] Build module RPZ lokal gagal; mencoba release terverifikasi.${NC}" >&2
    fi
    download_verified_rpz_binary "$output" || return 1
    RPZ_BINARY_SOURCE="verified-release"
}

render_install_plan() {
    local output=$1
    validate_source_settings || return 1
    mkdir -p "$output"
    render_panel_env "$output/panel.env"
    {
        echo "source-mode=$SOURCE_MODE"
        echo "dnsdist=$WITH_DNSDIST"
        if [ "$SOURCE_MODE" = feeds ]; then
            echo "feed-build-required"
            echo "rpz-disable-after-feed-build"
            echo "feed-cron-install"
        else
            echo "feed-cron-remove"
            preflight_rpz_binary "$output/rpz-master"
            echo "binary-source=$RPZ_BINARY_SOURCE"
            echo "rpz-service-enable"
        fi
    } > "$output/actions.txt"
    if [ "$SOURCE_MODE" = rpz-slave ]; then
        render_rpz_config "$output/rpz-master.json"
        render_rpz_unit "$output/rpz-master.service"
    fi
}

install_feed_cron() {
    local cron_cmd
    cron_cmd="0 */6 * * * $BUILD_SCRIPT >> /var/log/dnsdist-master-build.log 2>&1"
    if ! crontab -l 2>/dev/null | grep -Fq "$BUILD_SCRIPT"; then
        (crontab -l 2>/dev/null || true; echo "$cron_cmd") | crontab -
        echo -e "  ${GREEN}[✓] Cronjob terpasang: kompilasi otomatis setiap 6 jam.${NC}"
    else
        echo -e "  ${GREEN}[✓] Cronjob sudah ada.${NC}"
    fi
}

remove_feed_cron() {
    local current filtered
    current=$(crontab -l 2>/dev/null || true)
    filtered=$(printf '%s\n' "$current" | grep -Fv "$BUILD_SCRIPT" || true)
    if [ -n "$filtered" ]; then
        printf '%s\n' "$filtered" | crontab -
    elif [ -n "$current" ]; then
        crontab -r 2>/dev/null || true
    fi
    echo -e "  ${GREEN}[✓] Cronjob feed dinonaktifkan untuk mencegah dual writer.${NC}"
}

rpz_service_installed() {
    [ -f "$RPZ_UNIT" ] || systemctl is-enabled --quiet rpz-master 2>/dev/null || systemctl is-active --quiet rpz-master 2>/dev/null
}

activate_feeds_mode() {
    local switching=false
    rpz_service_installed && switching=true
	if [ "$switching" = true ]; then
		systemctl stop rpz-master >/dev/null 2>&1 || true
	fi
	persist_source_mode feeds

    echo -e "\n${CYAN}[*] Menjalankan kompilasi database awal...${NC}"
    if ! "$BUILD_SCRIPT"; then
        if [ "$switching" = true ]; then
			persist_source_mode rpz-slave
			systemctl start rpz-master >/dev/null 2>&1 || true
            die "Build feed awal gagal; rpz-master tetap aktif dan ownership tidak diubah."
            return 1
        fi
        echo -e "${YELLOW}[!] Peringatan: Kompilasi awal gagal atau sumber belum dapat diakses.${NC}"
    fi

    if [ "$switching" = true ]; then
        systemctl disable --now rpz-master >/dev/null 2>&1 || true
        rm -f "$RPZ_BIN" "$RPZ_CONFIG" "$RPZ_UNIT" "$RPZ_TSIG_INSTALLED_SECRET"
        systemctl daemon-reload
        echo -e "  ${GREEN}[✓] rpz-master dinonaktifkan setelah build feed berhasil.${NC}"
    fi

    install_feed_cron
}

activate_rpz_mode() {
    mkdir -p /var/lib/rpz-master "$CONF_DIR" "$SERVE_DIR"
    chmod 0700 /var/lib/rpz-master
    install -m 0755 "$RPZ_STAGED_BINARY" "$RPZ_BIN"
    if [ -n "$RPZ_TSIG_SECRET_FILE" ]; then
        install -m 0600 "$RPZ_TSIG_SECRET_FILE" "$RPZ_TSIG_INSTALLED_SECRET"
    else
        rm -f "$RPZ_TSIG_INSTALLED_SECRET"
    fi
    render_rpz_config "$RPZ_CONFIG"
    render_rpz_unit "$RPZ_UNIT"
	persist_source_mode rpz-slave
    remove_feed_cron
    systemctl daemon-reload
    systemctl enable rpz-master >/dev/null 2>&1
    systemctl restart rpz-master
    echo -e "  ${GREEN}[✓] RPZ slave aktif di ${RPZ_DNS_LISTEN}; writer feed dinonaktifkan.${NC}"
}

preflight_install() {
    validate_source_settings || return 1
    if [ "$SOURCE_MODE" = rpz-slave ]; then
        RPZ_STAGE_DIR=$(mktemp -d)
        RPZ_STAGED_BINARY="$RPZ_STAGE_DIR/rpz-master"
        trap 'rm -rf "$RPZ_STAGE_DIR"' EXIT
        preflight_rpz_binary "$RPZ_STAGED_BINARY" || return 1
    fi
}

check_root() {
    [ "${SKIP_ROOT_CHECK:-false}" = true ] && return 0
    if [ "$(id -u)" -ne 0 ]; then
        echo -e "${RED}[!] Skrip ini harus dijalankan sebagai root (Gunakan sudo).${NC}"
        exit 1
    fi
}

self_update() {
    command -v curl >/dev/null 2>&1 || {
        echo -e "${RED}[!] curl diperlukan untuk memperbarui installer.${NC}" >&2
        return 1
    }

    local self tmp checksum expected actual
    self=$(readlink -f "$0")
    tmp=$(mktemp "$(dirname "$self")/.setup-master.sh.tmp.XXXXXX")
    checksum="${tmp}.sha256"
    SELF_UPDATE_TMP="$tmp"
    SELF_UPDATE_CHECKSUM="$checksum"
    trap 'rm -f "$SELF_UPDATE_TMP" "$SELF_UPDATE_CHECKSUM"' EXIT

    echo "[*] Mengunduh installer terbaru dari GitHub..."
    curl -fsSL "$SELF_UPDATE_URL" -o "$tmp"
    bash -n "$tmp"

    curl -fsSL "$(dirname "$SELF_UPDATE_URL")/SHA256SUMS" -o "$checksum" 2>/dev/null || {
        echo -e "${RED}[!] SHA256SUMS tidak tersedia; upgrade dibatalkan.${NC}" >&2
        return 1
    }
    expected=$(awk -v file="$(basename "$SELF_UPDATE_URL")" '$2 == file { print $1; exit }' "$checksum")
    printf '%s\n' "$expected" | grep -Eq '^[[:xdigit:]]{64}$' || {
        echo -e "${RED}[!] Format checksum installer tidak valid.${NC}" >&2
        return 1
    }
    actual=$(sha256sum "$tmp" | awk '{ print $1 }')
    [ "$actual" = "$expected" ] || {
        echo -e "${RED}[!] Checksum installer tidak cocok.${NC}" >&2
        return 1
    }
    echo -e "  ${GREEN}[✓] Checksum SHA-256 valid.${NC}"

    chmod --reference="$self" "$tmp"
    chown --reference="$self" "$tmp"
    mv -f "$tmp" "$self"
    rm -f "$checksum"
    trap - EXIT
    echo -e "  ${GREEN}[✓] Installer diperbarui secara atomik.${NC}"
    export SELF_UPDATE_DONE=true
    exec bash "$self" "$@"
}

do_upgrade() {
    if [ "$SKIP_SELF_UPDATE" != true ]; then
        self_update "${ORIGINAL_ARGS[@]}"
    fi
    echo -e "${GREEN}[✓] Installer Master v${SCRIPT_VERSION} sudah terbaru.${NC}"
}

do_build_now() {
    if [ "$SOURCE_MODE" = rpz-slave ] || { [ -f "$CONF_DIR/source-mode" ] && [ "$(cat "$CONF_DIR/source-mode")" = rpz-slave ]; }; then
        echo -e "${RED}[!] --build-now adalah feed builder dan dinonaktifkan dalam mode rpz-slave.${NC}" >&2
        exit 1
    fi
    if [ -x "$BUILD_SCRIPT" ]; then
        "$BUILD_SCRIPT"
    else
        echo -e "${RED}[!] Skrip build tidak ditemukan di $BUILD_SCRIPT. Jalankan --install terlebih dahulu.${NC}"
        exit 1
    fi
}

do_install() {
    preflight_install

    echo -e "\n${CYAN}=== [1/5] Instalasi Dependensi Dasar Master ===${NC}"
    apt-get update
    apt-get install -y curl ca-certificates openssl nginx cron aria2

    # Pilihan mode jika tidak dispesifikasikan via flag
    if [ "$MODE_EXPLICIT" != true ]; then
        echo -e "\n${CYAN}Pilih Mode Master Server:${NC}"
        echo "  1) Standalone Master (Hanya Generator & Distributor CDB — Tanpa DNSDist)"
        echo "  2) Hybrid Master     (Generator CDB + DNSDist Resolver aktif di server ini)"
        read -p "Pilihan Anda [1/2] (Default: 1): " choice_dnsdist
        if [ "$choice_dnsdist" = "2" ]; then
            WITH_DNSDIST=true
        else
            WITH_DNSDIST=false
        fi
    fi

    echo -e "\n${CYAN}=== [2/5] Menyiapkan Engine Kompilasi Master ===${NC}"
    mkdir -p "$CONF_DIR" "$SERVE_DIR"

    local panel_bin="/usr/local/bin/dnsdist-panel"
    local src_panel=""
    if [ -f "$MASTER_DIR/dnsdist-panel" ]; then
        src_panel="$MASTER_DIR/dnsdist-panel"
    elif [ -f "$MASTER_DIR/tools/dnsdist-panel" ]; then
        src_panel="$MASTER_DIR/tools/dnsdist-panel"
    elif [ -f "$MASTER_DIR/../tools/dnsdist-panel" ]; then
        src_panel="$MASTER_DIR/../tools/dnsdist-panel"
    fi

    if [ -n "$src_panel" ]; then
        if systemctl is-active --quiet dnsdist-panel 2>/dev/null; then
            systemctl stop dnsdist-panel || true
        fi
        cp "$src_panel" "$panel_bin"
        chmod 0755 "$panel_bin"
        ln -sfn "$panel_bin" "$BUILDER_BIN"
        echo -e "  ${GREEN}[✓] Unified Master Engine terpasang di $panel_bin${NC}"
    elif [ -f "$BUILDER_BIN" ]; then
        echo -e "  ${GREEN}[✓] trust-builder terdeteksi di $BUILDER_BIN${NC}"
    fi

    # Pasang skrip builder wrapper
    local src_script=""
    if [ -f "$MASTER_DIR/build-master-cdb.sh" ]; then
        src_script="$MASTER_DIR/build-master-cdb.sh"
    elif [ -f "$MASTER_DIR/setup/build-master-cdb.sh" ]; then
        src_script="$MASTER_DIR/setup/build-master-cdb.sh"
    fi

    if [ -n "$src_script" ]; then
        cp "$src_script" "$BUILD_SCRIPT"
        chmod 0755 "$BUILD_SCRIPT"
        echo -e "  ${GREEN}[✓] Skrip compiler terpasang di $BUILD_SCRIPT${NC}"
    fi

    # Inisialisasi daftar sumber default jika belum ada
    if [ ! -f "$CONF_DIR/sources.txt" ]; then
        cat > "$CONF_DIR/sources.txt" << 'SOURCESEOF'
# Daftar URL sumber blacklist (satu per baris)
# AdGuard DNS Filter
https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt
SOURCESEOF
        echo -e "  ${GREEN}[✓] Template sumber blacklist dibuat di $CONF_DIR/sources.txt${NC}"
    fi

    [ ! -f "$CONF_DIR/whitelist.txt" ] && touch "$CONF_DIR/whitelist.txt"
    [ ! -f "$CONF_DIR/custom-blacklist.txt" ] && touch "$CONF_DIR/custom-blacklist.txt"

    echo -e "\n${CYAN}=== [3/5] Konfigurasi Nginx CDB Publisher (Port $PORT_HTTP) ===${NC}"
    cat > /etc/nginx/sites-available/dnsdist-master << NGINXEOF
server {
    listen ${PORT_HTTP} default_server;
    listen [::]:${PORT_HTTP} default_server;
    server_name _;

    root /var/www/html;

    location /files/ {
        alias ${SERVE_DIR}/;
        autoindex on;
        add_header Cache-Control "public, must-revalidate, proxy-revalidate";
        add_header Access-Control-Allow-Origin *;
    }

    location / {
        return 200 "DNSDist Central Master Server Active\\nDownload CDB: http://\$host:${PORT_HTTP}/files/trust.db\\nManifest: http://\$host:${PORT_HTTP}/files/manifest.json\\n";
        add_header Content-Type text/plain;
    }
}
NGINXEOF

    ln -sfn /etc/nginx/sites-available/dnsdist-master /etc/nginx/sites-enabled/dnsdist-master
    systemctl restart nginx
    echo -e "  ${GREEN}[✓] Nginx CDB Publisher aktif di port ${PORT_HTTP}${NC}"

    echo -e "\n${CYAN}=== [4/5] Mengaktifkan Source Mode: ${SOURCE_MODE} ===${NC}"
    if [ "$SOURCE_MODE" = rpz-slave ]; then
        activate_rpz_mode
    else
        activate_feeds_mode
    fi
    if [ -f /etc/systemd/system/dnsdist-panel.service ]; then
		mkdir -p /etc/systemd/system/dnsdist-panel.service.d
		cat > /etc/systemd/system/dnsdist-panel.service.d/source-mode.conf << DROPIN
[Service]
EnvironmentFile=-$CONF_DIR/panel.env
ExecStart=
ExecStart=/usr/local/bin/dnsdist-panel -build-interval \${PANEL_BUILD_INTERVAL}
DROPIN
        systemctl daemon-reload
        systemctl try-restart dnsdist-panel >/dev/null 2>&1 || true
    fi

    # Jika mode hybrid (dengan DNSDist)
    if [ "$WITH_DNSDIST" = true ]; then
        echo -e "\n${CYAN}=== [4.5/5] Mengonfigurasi DNSDist Resolver Lokal ===${NC}"
        apt-get install -y dnsdist
        mkdir -p /etc/dnsdist /var/lib/dnsdist
        if [ -f "$MASTER_DIR/dnsdist.conf" ]; then
            cp "$MASTER_DIR/dnsdist.conf" /etc/dnsdist/dnsdist.conf
        elif [ -f "$MASTER_DIR/setup/dnsdist.conf" ]; then
            cp "$MASTER_DIR/setup/dnsdist.conf" /etc/dnsdist/dnsdist.conf
        fi
        ln -sfn "${SERVE_DIR}/trust.db" /var/lib/dnsdist/blacklist.db
        systemctl enable dnsdist >/dev/null 2>&1
        systemctl restart dnsdist || true
        echo -e "  ${GREEN}[✓] DNSDist terpasang dan tersinkron ke CDB Master.${NC}"
    else
        echo -e "\n  ${YELLOW}[i] DNSDist dilewati (Mode Standalone Master). Port 53 bebas.${NC}"
    fi

    # Panel opsional
    if [ "$WITH_PANEL" = true ]; then
        echo -e "\n${CYAN}=== [5/5] Memasang DNSDist Management Panel ===${NC}"
        local panel_bin="/usr/local/bin/dnsdist-panel"
        local panel_src=""
        if [ -f "$MASTER_DIR/dnsdist-panel" ]; then
            panel_src="$MASTER_DIR/dnsdist-panel"
        elif [ -f "$MASTER_DIR/tools/dnsdist-panel" ]; then
            panel_src="$MASTER_DIR/tools/dnsdist-panel"
        elif [ -f "$MASTER_DIR/../tools/dnsdist-panel" ]; then
            panel_src="$MASTER_DIR/../tools/dnsdist-panel"
        fi

        if [ -n "$panel_src" ]; then
            # Hentikan service panel sementara jika sedang berjalan untuk mencegah 'Text file busy'
            if systemctl is-active --quiet dnsdist-panel 2>/dev/null; then
                echo "[*] Menghentikan service dnsdist-panel sementara sebelum update binary..."
                systemctl stop dnsdist-panel || true
            fi

            cp "$panel_src" "$panel_bin"
            chmod 0755 "$panel_bin"
            local panel_build_interval=6h
            [ "$SOURCE_MODE" = rpz-slave ] && panel_build_interval=0
            cat > /etc/systemd/system/dnsdist-panel.service << UNIT
[Unit]
Description=DNSDist Management Panel
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/dnsdist-panel -build-interval ${panel_build_interval}
Restart=on-failure
RestartSec=3
Environment=PANEL_ADDR=0.0.0.0:8443
Environment=PANEL_HTTP_ADDR=0.0.0.0:8084
Environment=PANEL_TLS=true
Environment=PANEL_MASTER=true
Environment=PANEL_FILES_DIR=/var/www/html/files
Environment=PANEL_SOURCES_FILE=/etc/dnsdist-master/sources.txt
Environment=PANEL_WHITELIST_FILE=/etc/dnsdist-master/whitelist.txt
Environment=PANEL_CUSTOM_BL_FILE=/etc/dnsdist-master/custom-blacklist.txt
Environment=PANEL_DB=/var/lib/dnsdist/panel.db
Environment=PANEL_SECRET_FILE=/var/lib/dnsdist/panel.secret
Environment=PANEL_CERT=/var/lib/dnsdist/panel-cert.pem
Environment=PANEL_KEY=/var/lib/dnsdist/panel-key.pem
Environment=DNSDIST_CONF=/etc/dnsdist/dnsdist.conf
Environment=DNSDIST_UPSTREAMS=/etc/dnsdist/upstreams.conf
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
            systemctl daemon-reload
            systemctl enable dnsdist-panel >/dev/null 2>&1
            systemctl restart dnsdist-panel || true
            echo -e "  ${GREEN}[✓] Panel aktif (Dual Mode: HTTPS :8443 / HTTP :8084)${NC}"
        fi
    fi

    local master_ip
    master_ip=$(hostname -I | awk '{print $1}')

    echo -e "\n${GREEN}============================================================${NC}"
    echo -e "${GREEN} Setup Master Server v${SCRIPT_VERSION} Berhasil! 🚀${NC}"
    echo -e " - Source Mode       : ${SOURCE_MODE}"
    echo -e " - Mode              : $([ "$WITH_DNSDIST" = true ] && echo 'Hybrid (Master + DNSDist)' || echo 'Standalone Master (Tanpa DNSDist)')"
    echo -e " - Publisher URL     : http://${master_ip}:${PORT_HTTP}/files/trust.db"
    echo -e " - Manifest Sidecar  : http://${master_ip}:${PORT_HTTP}/files/manifest.json"
    echo -e " - Config Sources    : ${CONF_DIR}/sources.txt"
    echo -e " - Whitelist File    : ${CONF_DIR}/whitelist.txt"
    echo -e " - Manual Build      : sudo ${BUILD_SCRIPT}"
    echo -e ""
    echo -e " Cara pasang di Edge Node (Sync Blacklist Saja):"
    echo -e "   sudo ./setup-edge.sh --install --url http://${master_ip}:${PORT_HTTP}/files/trust.db"
    echo -e ""
    echo -e " Cara pasang di Edge Node (Dengan Monitoring Terpusat Cluster):"
    echo -e "   1. Buat token di master:  sudo /usr/local/bin/dnsdist-panel -enrollment-token"
    echo -e "      (atau buka menu 'Cluster Nodes' di panel web: http://${master_ip}:8084)"
    echo -e "   2. Jalankan di edge node:"
    echo -e "      sudo ./setup-edge.sh --install \\"
    echo -e "        --url http://${master_ip}:${PORT_HTTP}/files/trust.db \\"
    echo -e "        --master-url http://${master_ip}:8084 \\"
    echo -e "        --enroll-token <TOKEN>"
    echo -e "${GREEN}============================================================${NC}\n"
}

# --- Main Args Parsing ---
INSTALL=false
BUILD_NOW=false
MODE_EXPLICIT=false
UPGRADE=false
SKIP_SELF_UPDATE="${SELF_UPDATE_DONE:-false}"
ORIGINAL_ARGS=("$@")

if [ "${1:-}" != --render-plan ]; then
    check_root
fi

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help)
            show_help
            ;;
        -i|--install)
            INSTALL=true
            ;;
        --with-dnsdist)
            WITH_DNSDIST=true
            MODE_EXPLICIT=true
            ;;
        --no-dnsdist)
            WITH_DNSDIST=false
            MODE_EXPLICIT=true
            ;;
        --source-mode|--rpz-upstream|--rpz-zone|--rpz-bootstrap-url|--rpz-dns-listen|--rpz-check-interval|--rpz-transfer-acl|--rpz-tsig-key|--rpz-tsig-secret-file)
            option=$1
            shift
            [ "$#" -gt 0 ] && [ -n "$1" ] || { die "$option memerlukan nilai."; exit 1; }
            case "$option" in
                --source-mode) SOURCE_MODE=$1 ;;
                --rpz-upstream) RPZ_UPSTREAM=$1 ;;
                --rpz-zone) RPZ_ZONE=$1 ;;
                --rpz-bootstrap-url) RPZ_BOOTSTRAP_URL=$1 ;;
                --rpz-dns-listen) RPZ_DNS_LISTEN=$1 ;;
                --rpz-check-interval) RPZ_CHECK_INTERVAL=$1 ;;
                --rpz-transfer-acl) RPZ_TRANSFER_ACL=$1 ;;
                --rpz-tsig-key) RPZ_TSIG_KEY=$1 ;;
                --rpz-tsig-secret-file) RPZ_TSIG_SECRET_FILE=$1 ;;
            esac
            ;;
        --render-plan)
            shift
            [ "$#" -gt 0 ] || { die "--render-plan memerlukan output directory."; exit 1; }
            PLAN_OUTPUT=$1
            shift
            while [ "$#" -gt 0 ]; do
                case "$1" in
                    --source-mode|--rpz-upstream|--rpz-zone|--rpz-bootstrap-url|--rpz-dns-listen|--rpz-check-interval|--rpz-transfer-acl|--rpz-tsig-key|--rpz-tsig-secret-file)
                        option=$1
                        shift
                        [ "$#" -gt 0 ] && [ -n "$1" ] || { die "$option memerlukan nilai."; exit 1; }
                        case "$option" in
                            --source-mode) SOURCE_MODE=$1 ;;
                            --rpz-upstream) RPZ_UPSTREAM=$1 ;;
                            --rpz-zone) RPZ_ZONE=$1 ;;
                            --rpz-bootstrap-url) RPZ_BOOTSTRAP_URL=$1 ;;
                            --rpz-dns-listen) RPZ_DNS_LISTEN=$1 ;;
                            --rpz-check-interval) RPZ_CHECK_INTERVAL=$1 ;;
                            --rpz-transfer-acl) RPZ_TRANSFER_ACL=$1 ;;
                            --rpz-tsig-key) RPZ_TSIG_KEY=$1 ;;
                            --rpz-tsig-secret-file) RPZ_TSIG_SECRET_FILE=$1 ;;
                        esac
                        ;;
                    --with-dnsdist) WITH_DNSDIST=true ;;
                    --no-dnsdist) WITH_DNSDIST=false ;;
                    *) die "Opsi tidak dikenali: $1"; exit 1 ;;
                esac
                shift
            done
            render_install_plan "$PLAN_OUTPUT"
            exit $?
            ;;
        --port)
            if [ -n "$2" ]; then
                PORT_HTTP="$2"
                shift
            fi
            ;;
        --with-panel)
            WITH_PANEL=true
            ;;
        --build-now)
            BUILD_NOW=true
            ;;
        --upgrade)
            UPGRADE=true
            ;;
        *)
            echo -e "${RED}[!] Opsi tidak dikenali: $1${NC}"
            exit 1
            ;;
    esac
    shift
done

if [ "$UPGRADE" = true ]; then
    do_upgrade
    exit 0
fi

if [ "$BUILD_NOW" = true ]; then
    do_build_now
    exit 0
fi

if [ "$INSTALL" = true ]; then
    do_install
    exit 0
fi

show_help
