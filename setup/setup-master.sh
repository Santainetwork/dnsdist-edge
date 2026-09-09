#!/bin/bash
# ============================================================
# DNSDist Central Master - Auto Setup Script (Native OS)
# Versi: 1.0.0
# Mendukung mode Standalone (Tanpa DNSDist) atau Hybrid (Dengan DNSDist)
# ============================================================

set -e

SCRIPT_VERSION="2.5.1"
MASTER_DIR=$(pwd)
CONF_DIR="/etc/dnsdist-master"
SERVE_DIR="/var/www/html/files"
BUILD_SCRIPT="/usr/local/bin/build-master-cdb.sh"
BUILDER_BIN="/usr/local/bin/trust-builder"
PORT_HTTP="8080"
WITH_DNSDIST=false
WITH_PANEL=false

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
    echo "      --port <PORT>      Port HTTP untuk distribusi file CDB (default: 8080)"
    echo "      --with-panel       Pasang DNSDist Panel manajemen"
    echo "      --build-now        Jalankan kompilasi CDB blacklist sekarang"
    echo "  -h, --help             Tampilkan bantuan ini"
    echo ""
    echo "Contoh:"
    echo "  sudo ./setup-master.sh --install --no-dnsdist --with-panel"
    echo "  sudo ./setup-master.sh --install --with-dnsdist"
    echo "  sudo ./setup-master.sh --build-now"
    exit 0
}

check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        echo -e "${RED}[!] Skrip ini harus dijalankan sebagai root (Gunakan sudo).${NC}"
        exit 1
    fi
}

do_build_now() {
    if [ -x "$BUILD_SCRIPT" ]; then
        "$BUILD_SCRIPT"
    else
        echo -e "${RED}[!] Skrip build tidak ditemukan di $BUILD_SCRIPT. Jalankan --install terlebih dahulu.${NC}"
        exit 1
    fi
}

do_install() {
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
# 1. Trust Positif Kominfo (Mirror)
https://raw.githubusercontent.com/Santainetwork/trust-positif-mirror/main/domains.txt

# 2. AdGuard DNS Filter (Opsional — hapus tanda pagar untuk mengaktifkan)
# https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt
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

    echo -e "\n${CYAN}=== [4/5] Mengatur Cronjob Kompilasi Otomatis (Tiap 6 Jam) ===${NC}"
    CRON_CMD="0 */6 * * * $BUILD_SCRIPT >> /var/log/dnsdist-master-build.log 2>&1"
    if ! crontab -l 2>/dev/null | grep -q "$BUILD_SCRIPT"; then
        (crontab -l 2>/dev/null; echo "$CRON_CMD") | crontab -
        echo -e "  ${GREEN}[✓] Cronjob terpasang: kompilasi otomatis setiap 6 jam.${NC}"
    else
        echo -e "  ${GREEN}[✓] Cronjob sudah ada.${NC}"
    fi

    # Menjalankan build pertama
    echo -e "\n${CYAN}[*] Menjalankan kompilasi database awal...${NC}"
    "$BUILD_SCRIPT" || echo -e "${YELLOW}[!] Peringatan: Kompilasi awal gagal atau sumber belum dapat diakses.${NC}"

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
            cat > /etc/systemd/system/dnsdist-panel.service << UNIT
[Unit]
Description=DNSDist Management Panel
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/dnsdist-panel
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
check_root

INSTALL=false
BUILD_NOW=false
MODE_EXPLICIT=false

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
        *)
            echo -e "${RED}[!] Opsi tidak dikenali: $1${NC}"
            exit 1
            ;;
    esac
    shift
done

if [ "$BUILD_NOW" = true ]; then
    do_build_now
    exit 0
fi

if [ "$INSTALL" = true ]; then
    do_install
    exit 0
fi

show_help
