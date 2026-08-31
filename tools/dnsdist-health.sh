#!/bin/bash
# ============================================================
# dnsdist-health.sh — Health Check DNSDist Edge Node
# Versi: 1.0.0
# Cek cepat kondisi node: service, config, DB, DNS resolution,
# port web console, dan usia sinkronisasi.
# ============================================================
set -u

GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[1;33m'; NC='\033[0m'
FAIL=0
VERBOSE=false
CRITICAL_ONLY=false

usage() {
    echo "Penggunaan: $0 [opsi]"
    echo "  -v, --verbose    tampilkan detail setiap cek"
    echo "  -c, --critical   hanya cek kritis (service + resolusi DNS)"
    echo "  -h, --help       tampilkan bantuan ini"
    exit 0
}

check() {  # check <nama> <pesan_ok> <pesan_gagal> <status>
    local name="$1" ok="$2" fail_msg="$3" status="$4"
    if [ "$status" = "0" ]; then
        [ "$VERBOSE" = true ] && echo -e "  ${GREEN}[✓]${NC} $name: $ok"
    else
        FAIL=$((FAIL+1))
        echo -e "  ${RED}[✗]${NC} $name: $fail_msg"
    fi
}

for arg in "$@"; do
    case "$arg" in
        -v|--verbose) VERBOSE=true ;;
        -c|--critical) CRITICAL_ONLY=true ;;
        -h|--help) usage ;;
        *) echo "[!] Opsi tidak dikenal: $arg"; usage ;;
    esac
done

echo -e "\n${YELLOW}=== DNSDist Edge Health Check ===${NC}\n"

# 1. Service systemd
if systemctl is-active --quiet dnsdist 2>/dev/null; then
    check "Service dnsdist" "active" "" 0
else
    check "Service dnsdist" "" "tidak aktif" 1
fi

# 2. Validasi konfigurasi
if command -v dnsdist >/dev/null 2>&1; then
    if dnsdist --check-config >/dev/null 2>&1; then
        check "Syntax dnsdist.conf" "valid" "" 0
    else
        check "Syntax dnsdist.conf" "" "error syntax" 1
    fi
else
    check "Binary dnsdist" "" "tidak terpasang" 1
fi

# 3. Blacklist DB
if [ -f /var/lib/dnsdist/blacklist.db ]; then
    SIZE=$(du -h /var/lib/dnsdist/blacklist.db | awk '{print $1}')
    AGE=$(($(date +%s) - $(stat -c %Y /var/lib/dnsdist/blacklist.db)))
    if [ "$AGE" -lt 86400 ]; then
        check "blacklist.db" "$SIZE (update <24 jam)" "" 0
    else
        check "blacklist.db" "" "$SIZE tapi update >24 jam lalu" 1
    fi
else
    check "blacklist.db" "" "file tidak ditemukan" 1
fi

# 4. Port web console
if timeout 2 bash -c "</dev/tcp/127.0.0.1/8083" 2>/dev/null; then
    check "Web console :8083" "terbuka" "" 0
else
    check "Web console :8083" "" "tidak merespons" 1
fi

# 5. Resolusi DNS dasar (kecuali hanya critical — ini tetap kritis)
if command -v dig >/dev/null 2>&1; then
    if dig @127.0.0.1 +time=2 +tries=1 google.com A +short >/dev/null 2>&1; then
        check "Resolusi DNS" "google.com OK" "" 0
    else
        check "Resolusi DNS" "" "gagal resolve google.com" 1
    fi
fi

# 6. Status sinkronisasi (non-critical)
if [ "$CRITICAL_ONLY" = false ] && [ -f /var/www/html/status/index.html ]; then
    if grep -q "success" /var/www/html/status/index.html 2>/dev/null; then
        check "Sync blacklist" "status: success" "" 0
    else
        check "Sync blacklist" "" "status bukan success" 1
    fi
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}Hasil: SEMUA CEK LULUS ✓${NC}"
    exit 0
else
    echo -e "${RED}Hasil: $FAIL cek GAGAL ✗${NC}"
    exit 1
fi
