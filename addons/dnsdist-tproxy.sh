#!/usr/bin/env bash
# Trust-NG dnsdist transparent-DNS helper.
# Default behavior is status/plan only. Changes require --apply.
set -euo pipefail

MODE=off
INTERFACE=
SUBNET=
APPLY=false
STATUS=false

NFT_TABLE=trust_ng_tproxy
NFT_MARK=0x1
NFT_TABLE_ID=130
NFT_RULE_PREF=13001
DNSDIST_TPROXY_BIN=${DNSDIST_TPROXY_BIN:-/usr/local/bin/dnsdist-tproxy}
DNSDIST_CONF=${DNSDIST_CONF:-/etc/dnsdist/dnsdist.conf}
DNSDIST_INCLUDE=${DNSDIST_INCLUDE:-/etc/dnsdist/tproxy.conf}
SYSCTL_FILE=${SYSCTL_FILE:-/etc/sysctl.d/99-trust-ng-tproxy.conf}
UNIT_FILE=${UNIT_FILE:-/etc/systemd/system/trust-ng-dnsdist-tproxy.service}
PROC_SYS_ROOT=${PROC_SYS_ROOT:-/proc/sys}

usage() {
    cat <<'USAGE'
Usage: dnsdist-tproxy.sh [status] [options]

Modes:
  --mode off|auto|tproxy  Default: off
  --interface IFACE       Required by tproxy
  --subnet CIDR           Required by tproxy; IPv4 only
  --apply                 Permit changes; without it, plan/status only
  --status                Read-only status (same as the status command)
  -h, --help              Show this help

auto is read-only diagnostics. It cannot detect MikroTik rules remotely and
never enables TPROXY.
USAGE
}

log() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; exit 2; }

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

validate_interface() {
    [ -n "$INTERFACE" ] || err "--interface is required for --mode tproxy"
    [[ "$INTERFACE" =~ ^[A-Za-z0-9_.-]{1,15}$ ]] || \
        err "invalid interface name: $INTERFACE"
}

validate_subnet() {
    local address prefix a b c d value mask network
    [ -n "$SUBNET" ] || err "--subnet is required for --mode tproxy"
    [[ "$SUBNET" == */* ]] || err "IPv4 CIDR required: $SUBNET"
    address=${SUBNET%/*}
    prefix=${SUBNET#*/}
    [[ "$address" != "$SUBNET" && "$prefix" =~ ^[0-9]{1,2}$ ]] || \
        err "invalid IPv4 CIDR: $SUBNET"
    IFS=. read -r a b c d <<< "$address"
    [[ -n "$a" && -n "$b" && -n "$c" && -n "$d" ]] || err "invalid IPv4 CIDR: $SUBNET"
    for value in "$a" "$b" "$c" "$d"; do
        [[ "$value" =~ ^[0-9]{1,3}$ ]] || err "invalid IPv4 CIDR: $SUBNET"
        (( 10#$value <= 255 )) || err "invalid IPv4 CIDR: $SUBNET"
    done
    (( 10#$prefix <= 32 )) || err "invalid IPv4 prefix: $SUBNET"

    value=$(( (10#$a << 24) | (10#$b << 16) | (10#$c << 8) | 10#$d ))
    if (( 10#$prefix == 0 )); then
        mask=0
    else
        mask=$(( (0xffffffff << (32 - 10#$prefix)) & 0xffffffff ))
    fi
    network=$(( value & mask ))
    printf -v SUBNET '%d.%d.%d.%d/%d' \
        $(( (network >> 24) & 255 )) $(( (network >> 16) & 255 )) \
        $(( (network >> 8) & 255 )) $(( network & 255 )) "$((10#$prefix))"
}

parse_args() {
    while [ "$#" -gt 0 ]; do
        case "$1" in
            status|--status) STATUS=true ;;
            --mode) [ "$#" -ge 2 ] || err "--mode needs a value"; MODE=$2; shift ;;
            --mode=*) MODE=${1#*=} ;;
            --interface) [ "$#" -ge 2 ] || err "--interface needs a value"; INTERFACE=$2; shift ;;
            --interface=*) INTERFACE=${1#*=} ;;
            --subnet) [ "$#" -ge 2 ] || err "--subnet needs a value"; SUBNET=$2; shift ;;
            --subnet=*) SUBNET=${1#*=} ;;
            --apply) APPLY=true ;;
            -h|--help) usage; exit 0 ;;
            *) err "unknown argument: $1" ;;
        esac
        shift
    done
    case "$MODE" in off|auto|tproxy) ;; *) err "--mode must be off, auto, or tproxy" ;; esac
    if [ "$MODE" = tproxy ]; then
        validate_interface
        validate_subnet
    elif [ "$APPLY" = true ] && [ "$MODE" = auto ]; then
        err "auto is read-only and never enables TPROXY; remove --apply"
    fi
}

owned_file() {
    [ ! -e "$1" ] || grep -Fq '# Managed by dnsdist-tproxy.sh' "$1" || \
        err "refusing to replace non-owned file: $1"
}

owned_nft_table() {
    nft list table inet "$NFT_TABLE" 2>/dev/null | \
        grep -Fq 'comment "Managed by dnsdist-tproxy.sh"'
}

show_status() {
    log "dnsdist-tproxy status (read-only)"
    if command -v nft >/dev/null 2>&1 && nft list table inet "$NFT_TABLE" >/dev/null 2>&1; then
        log "nft table: present ($NFT_TABLE)"
    else
        log "nft table: absent or nft unavailable ($NFT_TABLE)"
    fi
    if command -v ip >/dev/null 2>&1; then
        if ip rule show | awk -v pref="$NFT_RULE_PREF:" '$1 == pref' | grep -q .; then
            log "ip rule: present (pref $NFT_RULE_PREF, mark $NFT_MARK, table $NFT_TABLE_ID)"
        else
            log "ip rule: absent (pref $NFT_RULE_PREF, mark $NFT_MARK, table $NFT_TABLE_ID)"
        fi
        if ip route show table "$NFT_TABLE_ID" | grep -Fq 'local 0.0.0.0/0 dev lo'; then
            log "local route: present (table $NFT_TABLE_ID)"
        else
            log "local route: absent (table $NFT_TABLE_ID)"
        fi
    else
        log "ip rule/route: ip unavailable"
    fi
    [ -e "$UNIT_FILE" ] && log "systemd unit: present ($UNIT_FILE)" || log "systemd unit: absent"
    [ -e "$SYSCTL_FILE" ] && log "sysctl drop-in: present ($SYSCTL_FILE)" || log "sysctl drop-in: absent"
    [ -e "$DNSDIST_INCLUDE" ] && log "dnsdist include: present ($DNSDIST_INCLUDE)" || log "dnsdist include: absent"
    log "dnsdist counters: dnsdist -c 'showStats()' and dnsdist -c 'showServers()'"
    log "packet hint: sudo tcpdump -ni <LAN_IFACE> 'udp port 53 or tcp port 53'"
    log "MikroTik hint: inspect /ip firewall nat and /ip dns, then compare packet counts"
}

show_plan() {
    log "PLAN: no changes will be made (use --apply to mutate)"
    log "mode: $MODE"
    if [ "$MODE" = tproxy ]; then
        log "interface: $INTERFACE"
        log "source subnet: $SUBNET"
        log "intercept: inbound UDP/TCP destination port 53 only"
        log "exclude: locally-destined Edge addresses via nft fib daddr type local"
        log "TPROXY target: :53, mark $NFT_MARK, local route table $NFT_TABLE_ID"
        log "backend: $DNSDIST_TPROXY_BIN -> 127.0.0.1:5353 with PROXYv2"
        log "files: $UNIT_FILE, $SYSCTL_FILE, $DNSDIST_INCLUDE"
    elif [ "$MODE" = off ]; then
        log "would remove only the owned $NFT_TABLE nft table, exact ip rule/route, unit, drop-in, and include"
    fi
}

write_nft_file() {
    local file=$1
    cat > "$file" <<EOF
 table inet $NFT_TABLE {
   comment "Managed by dnsdist-tproxy.sh"
   chain prerouting {
     type filter hook prerouting priority mangle; policy accept;
     fib daddr type local return
     iifname "$INTERFACE" ip saddr $SUBNET meta mark != $NFT_MARK ip protocol udp udp dport 53 tproxy ip to 127.0.0.1:53 meta mark set $NFT_MARK accept
     iifname "$INTERFACE" ip saddr $SUBNET meta mark != $NFT_MARK ip protocol tcp tcp dport 53 tproxy ip to 127.0.0.1:53 meta mark set $NFT_MARK accept
   }
 }
EOF
}

apply_nft() {
    local file had_table=false old_file
    need_cmd nft
    file=$(mktemp)
    trap 'rm -f "$file"' RETURN
    write_nft_file "$file"
    nft -c -f "$file"
    if nft list table inet "$NFT_TABLE" >/dev/null 2>&1; then
        owned_nft_table || err "refusing to replace non-owned nft table: $NFT_TABLE"
        had_table=true
        old_file=$(mktemp)
        nft list table inet "$NFT_TABLE" > "$old_file"
        nft delete table inet "$NFT_TABLE"
    fi
    if ! nft -f "$file"; then
        [ "$had_table" = true ] && nft -f "$old_file" || true
        rm -f "${old_file:-}"
        err "failed to apply nft table; previous owned table restored when available"
    fi
    rm -f "${old_file:-}"
    rm -f "$file"
    trap - RETURN
}

apply_ip() {
    need_cmd ip
    if ! ip rule show | awk -v pref="$NFT_RULE_PREF:" -v mark="$NFT_MARK" \
        -v table="$NFT_TABLE_ID" '$1 == pref && $0 ~ "fwmark " mark && $0 ~ "lookup " table' \
        | grep -q .; then
        ip rule add pref "$NFT_RULE_PREF" fwmark "$NFT_MARK" lookup "$NFT_TABLE_ID"
    fi
    ip route replace local 0.0.0.0/0 dev lo table "$NFT_TABLE_ID"
}

apply_files() {
    local unit_tmp include_tmp sysctl_tmp
    [ -x "$DNSDIST_TPROXY_BIN" ] || err "missing executable: $DNSDIST_TPROXY_BIN"
    owned_file "$UNIT_FILE"
    owned_file "$DNSDIST_INCLUDE"
    owned_file "$SYSCTL_FILE"
    mkdir -p "$(dirname "$DNSDIST_INCLUDE")" "$(dirname "$SYSCTL_FILE")" "$(dirname "$UNIT_FILE")"

    include_tmp=$(mktemp)
    cat > "$include_tmp" <<'EOF'
-- Managed by dnsdist-tproxy.sh
-- Loaded instead of the public port 53 listeners while TPROXY is enabled.
addLocal('127.0.0.1:5353')
setProxyProtocolACL({'127.0.0.2/32'})
EOF
    install -m 0644 "$include_tmp" "$DNSDIST_INCLUDE"
    rm -f "$include_tmp"

    sysctl_tmp=$(mktemp)
    cat > "$sysctl_tmp" <<EOF
# Managed by dnsdist-tproxy.sh
# Values are preflight prerequisites; this helper does not change live sysctls.
net.ipv4.ip_forward = 1
net.ipv4.conf.$INTERFACE.rp_filter = 0
EOF
    install -m 0644 "$sysctl_tmp" "$SYSCTL_FILE"
    rm -f "$sysctl_tmp"

    unit_tmp=$(mktemp)
    cat > "$unit_tmp" <<EOF
# Managed by dnsdist-tproxy.sh
[Unit]
Description=Trust-NG dnsdist transparent DNS proxy
Wants=network-online.target
After=network-online.target dnsdist.service

[Service]
Type=simple
ExecStart=$DNSDIST_TPROXY_BIN --listen 0.0.0.0:53 --backend 127.0.0.1:5353 --source 127.0.0.2
Restart=on-failure
RestartSec=3
User=root
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectControlGroups=true
ProtectKernelTunables=true
ProtectKernelModules=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
ReadWritePaths=/run

[Install]
WantedBy=multi-user.target
EOF
    install -m 0644 "$unit_tmp" "$UNIT_FILE"
    rm -f "$unit_tmp"
}

apply_on() {
    [ "$(id -u)" -eq 0 ] || err '--apply requires root'
    need_cmd install
    need_cmd systemctl
    need_cmd nft
    need_cmd ip
    need_cmd dnsdist
    [ -x "$DNSDIST_TPROXY_BIN" ] || err "missing executable: $DNSDIST_TPROXY_BIN"
    [ -r "$DNSDIST_CONF" ] || err "missing dnsdist configuration: $DNSDIST_CONF"
    grep -Fq "$DNSDIST_INCLUDE" "$DNSDIST_CONF" && grep -Eq 'io.open[[:space:]]*\(' "$DNSDIST_CONF" || \
        err "add an io.open existence guard for $DNSDIST_INCLUDE before --apply"
    grep -Eq 'dofile[[:space:]]*\(' "$DNSDIST_CONF" || \
        err "add dofile('$DNSDIST_INCLUDE') to $DNSDIST_CONF before --apply"
    [ "$(cat "$PROC_SYS_ROOT/net/ipv4/ip_forward")" = 1 ] || \
        err 'net.ipv4.ip_forward must be 1; configure it persistently before --apply'
    [ "$(cat "$PROC_SYS_ROOT/net/ipv4/conf/$INTERFACE/rp_filter")" = 0 ] || \
        err "net.ipv4.conf.$INTERFACE.rp_filter must be 0; configure it persistently before --apply"
    owned_file "$UNIT_FILE"
    owned_file "$DNSDIST_INCLUDE"
    owned_file "$SYSCTL_FILE"
    if nft list table inet "$NFT_TABLE" >/dev/null 2>&1; then
        owned_nft_table || err "refusing to replace non-owned nft table: $NFT_TABLE"
    fi
    dnsdist --check-config
    apply_files
    dnsdist --check-config
    systemctl restart dnsdist.service
    systemctl daemon-reload
    systemctl enable --now "$(basename "$UNIT_FILE" .service)"
    apply_ip
    apply_nft
    log "TPROXY enabled for $INTERFACE / $SUBNET"
}

remove_owned_file() {
    local file=$1
    [ ! -e "$file" ] && return 0
    grep -Fq '# Managed by dnsdist-tproxy.sh' "$file" || err "refusing to remove non-owned file: $file"
    rm -f "$file"
}

apply_off() {
    [ "$(id -u)" -eq 0 ] || err '--apply requires root'
    need_cmd nft
    need_cmd ip
    need_cmd systemctl
    if nft list table inet "$NFT_TABLE" >/dev/null 2>&1; then
        owned_nft_table || err "refusing to remove non-owned nft table: $NFT_TABLE"
        nft delete table inet "$NFT_TABLE"
    fi
    ip rule del pref "$NFT_RULE_PREF" fwmark "$NFT_MARK" lookup "$NFT_TABLE_ID" 2>/dev/null || true
    ip route del local 0.0.0.0/0 dev lo table "$NFT_TABLE_ID" 2>/dev/null || true
    if [ -e "$UNIT_FILE" ]; then
        grep -Fq '# Managed by dnsdist-tproxy.sh' "$UNIT_FILE" || \
            err "refusing to disable non-owned unit: $UNIT_FILE"
        systemctl disable --now "$(basename "$UNIT_FILE")" >/dev/null 2>&1 || true
    fi
    remove_owned_file "$UNIT_FILE"
    remove_owned_file "$SYSCTL_FILE"
    remove_owned_file "$DNSDIST_INCLUDE"
    systemctl daemon-reload
    if command -v dnsdist >/dev/null 2>&1 && [ -r "$DNSDIST_CONF" ]; then
        dnsdist --check-config
        systemctl restart dnsdist.service
    fi
    log "owned dnsdist-tproxy state removed; unrelated firewall rules were not changed"
}

auto_diagnostics() {
    log "AUTO: diagnostics only; cannot detect MikroTik rules remotely; never enables TPROXY"
    [ -n "$INTERFACE" ] && log "interface hint: $INTERFACE"
    [ -n "$SUBNET" ] && log "subnet hint: $SUBNET"
    show_status
    log "Check MikroTik locally: /ip firewall nat print detail and /ip dns print"
}

parse_args "$@"
if [ "$STATUS" = true ]; then
    [ "$MODE" = auto ] && auto_diagnostics || show_status
    exit 0
fi
if [ "$MODE" = auto ]; then
    auto_diagnostics
elif [ "$APPLY" = true ]; then
    if [ "$MODE" = tproxy ]; then
        apply_on
    else
        apply_off
    fi
else
    show_plan
fi
