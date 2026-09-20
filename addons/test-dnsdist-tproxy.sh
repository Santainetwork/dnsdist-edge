#!/usr/bin/env bash
set -u

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SCRIPT_DIR/dnsdist-tproxy.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
pass() { printf 'ok - %s\n' "$1"; }

[ -x "$SCRIPT" ] || fail "helper is executable"

for needle in \
  '--mode off|auto|tproxy' \
  'cannot detect MikroTik rules remotely' \
  'never enables' \
  'trust_ng_tproxy' \
  '127.0.0.1:5353' \
  "setProxyProtocolACL({'127.0.0.2/32'})" \
  'CAP_NET_ADMIN' \
  'nft' \
  'ip rule'; do
  grep -Fq -- "$needle" "$SCRIPT" || fail "helper contains $needle"
done
pass "required contracts are present"

output=$({ "$SCRIPT" --mode auto --interface eth0 --subnet 192.168.10.0/24; } 2>&1) || fail "auto diagnostics succeeds"
printf '%s\n' "$output" | grep -Fq 'cannot detect MikroTik rules remotely' || fail "auto explains remote limitation"
printf '%s\n' "$output" | grep -Fq 'never enables' || fail "auto never enables"
pass "auto is read-only diagnostics"

output=$({ "$SCRIPT" --mode tproxy --interface eth0 --subnet 192.168.10.0/24; } 2>&1) || fail "tproxy plan succeeds"
printf '%s\n' "$output" | grep -Fq 'PLAN' || fail "default tproxy mode is a plan"
printf '%s\n' "$output" | grep -Fq -- '--apply' || fail "plan requires explicit apply"
pass "tproxy default is non-mutating"

if "$SCRIPT" --mode tproxy --interface 'bad iface' --subnet 192.168.10.0/24 >/dev/null 2>&1; then
  fail "invalid interface rejected"
fi
if "$SCRIPT" --mode tproxy --interface eth0 --subnet 192.168.10.999/24 >/dev/null 2>&1; then
  fail "invalid IPv4 subnet rejected"
fi
pass "unsafe inputs are rejected"

ROOT="$TMP/root"
MOCK="$TMP/mock"
LOG="$TMP/commands.log"
mkdir -p "$ROOT/etc/dnsdist" "$ROOT/etc/sysctl.d" "$ROOT/etc/systemd/system" \
  "$ROOT/proc/net/ipv4/conf/eth0" "$MOCK"
printf '#!/bin/sh\nprintf 0\n' > "$MOCK/id"
printf '#!/bin/sh\nprintf "nft %%s\\n" "$*" >> "$MOCK_LOG"\n[ "$1 $2" = "list table" ] && exit 1\n[ "$1 $2" = "-c -f" ] && cat "$3" >> "$MOCK_LOG"\nexit 0\n' > "$MOCK/nft"
printf '#!/bin/sh\nprintf "ip %%s\\n" "$*" >> "$MOCK_LOG"\n[ "$1 $2" = "rule show" ] && exit 0\n[ "$1 $2 $3 $4" = "route show table 130" ] && exit 0\nexit 0\n' > "$MOCK/ip"
printf '#!/bin/sh\nprintf "systemctl %%s\\n" "$*" >> "$MOCK_LOG"\n' > "$MOCK/systemctl"
printf '#!/bin/sh\nprintf "dnsdist %%s\\n" "$*" >> "$MOCK_LOG"\n' > "$MOCK/dnsdist"
chmod +x "$MOCK"/*
printf '#!/bin/sh\nexit 0\n' > "$ROOT/dnsdist-tproxy"
chmod +x "$ROOT/dnsdist-tproxy"
printf "local tproxyFile = '%s'\nlocal tpf = io.open(tproxyFile, 'r')\nif tpf then tpf:close(); dofile(tproxyFile) end\n" "$ROOT/etc/dnsdist/tproxy.conf" > "$ROOT/etc/dnsdist/dnsdist.conf"
printf '1\n' > "$ROOT/proc/net/ipv4/ip_forward"
printf '0\n' > "$ROOT/proc/net/ipv4/conf/eth0/rp_filter"

PATH="$MOCK:$PATH" MOCK_LOG="$LOG" \
DNSDIST_TPROXY_BIN="$ROOT/dnsdist-tproxy" \
DNSDIST_CONF="$ROOT/etc/dnsdist/dnsdist.conf" \
DNSDIST_INCLUDE="$ROOT/etc/dnsdist/tproxy.conf" \
SYSCTL_FILE="$ROOT/etc/sysctl.d/tproxy.conf" \
UNIT_FILE="$ROOT/etc/systemd/system/tproxy.service" \
PROC_SYS_ROOT="$ROOT/proc" \
  "$SCRIPT" --mode tproxy --interface eth0 --subnet 192.168.10.7/24 --apply >/dev/null

grep -Fq "ip saddr 192.168.10.0/24" "$LOG" || fail "CIDR is canonicalized in nft rules"
grep -Fq "nft -c -f" "$LOG" || fail "nft rules are checked before apply"
grep -Fq -- "--source 127.0.0.2" "$ROOT/etc/systemd/system/tproxy.service" || fail "proxy source is isolated"
grep -Fq "setProxyProtocolACL({'127.0.0.2/32'})" "$ROOT/etc/dnsdist/tproxy.conf" || fail "dnsdist PROXY ACL is isolated"
grep -Fq 'net.ipv4.conf.eth0.rp_filter = 0' "$ROOT/etc/sysctl.d/tproxy.conf" || fail "interface sysctl drop-in rendered"
pass "mocked apply renders owned state without host mutation"

sed -i '/dofile/d' "$ROOT/etc/dnsdist/dnsdist.conf"
if PATH="$MOCK:$PATH" MOCK_LOG="$LOG" DNSDIST_TPROXY_BIN="$ROOT/dnsdist-tproxy" \
  DNSDIST_CONF="$ROOT/etc/dnsdist/dnsdist.conf" DNSDIST_INCLUDE="$ROOT/etc/dnsdist/tproxy.conf" \
  PROC_SYS_ROOT="$ROOT/proc" "$SCRIPT" --mode tproxy --interface eth0 --subnet 192.168.10.0/24 --apply >/dev/null 2>&1; then
  fail "missing dnsdist include hook rejected"
fi
pass "missing dnsdist include hook is rejected"

printf 'all dnsdist-tproxy tests passed\n'
