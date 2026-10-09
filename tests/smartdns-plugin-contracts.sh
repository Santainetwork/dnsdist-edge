#!/bin/bash
set -euo pipefail
# Contract: shipped addon exposes the SmartDNS API surface used by docs and
# dnsdist.conf examples, and documents the current behavior. No privileged
# runtime required.
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PLUGIN="$ROOT/addons/smartdns-plugin.lua"

grep -Fq 'function smartdns_ip_set(' "$PLUGIN"
grep -Fq 'function smartdns_domain_set(' "$PLUGIN"
grep -Fq 'function smartdns_cname(' "$PLUGIN"
grep -Fq 'function smartdns_ip_rules_alias(' "$PLUGIN"
grep -Fq 'type(target_ips) ~= "table" or #target_ips == 0' "$PLUGIN"  # invalid config must be skipped safely
grep -Fq 'type(tip) ~= "string" or #tip == 0' "$PLUGIN"  # invalid entries must be skipped safely
grep -Fq 'ip-set butuh name dan filepath string tidak kosong' "$PLUGIN"
grep -Fq 'domain-set butuh name dan filepath string tidak kosong' "$PLUGIN"
grep -Fq 'cname butuh domain_pattern dan target_cname string tidak kosong' "$PLUGIN"
grep -Fq 'function smartdns_enable_speedcheck()' "$PLUGIN"

# Current SmartDNS response-mode values must be present.
grep -Fq 'first-ping' "$PLUGIN"
grep -Fq 'fastest-ip' "$PLUGIN"
grep -Fq 'fastest-response' "$PLUGIN"

# Per-domain skip/ports override must exist (domain-rules -speed-check-mode none analog).
grep -Fq 'SPEEDCHECK_SKIP' "$PLUGIN"
grep -Fq 'SPEEDCHECK_PORTS_OVERRIDE' "$PLUGIN"
grep -Fq '_speedcheck_ports' "$PLUGIN"  # helper function must exist

# Graceful no-socket path must be explicit.
grep -Fq 'SPEEDCHECK_ENABLED = false' "$PLUGIN"

# Docs must match the implemented alias semantics (per-record, round-robin).
grep -Fq 'setiap record yang cocok ip-set diisi dari daftar target secara round-robin' "$ROOT/TOPSTATS-README.md"

# Ordering: an active (uncommented) smartdns_cname call is a terminal action.
# It must not be registered before blocking ([7] kvsRule). Only the commented
# example may precede it. Passes today because no active call exists.
CONF="$ROOT/setup/dnsdist.conf"
active_cname_line=$(grep -n '^[[:space:]]*smartdns_cname(' "$CONF" | head -1 | cut -d: -f1 || true)
block_line=$(grep -n '^-- \[7\] FILTERING RULES' "$CONF" | cut -d: -f1)
if [ -n "$active_cname_line" ] && [ "$active_cname_line" -lt "$block_line" ]; then
  echo "FAIL: active smartdns_cname (line $active_cname_line) before blocking [7] (line $block_line)" >&2
  exit 1
fi

# Harness smoke: plugin must load and register via stubbed dnsdist API.
lua5.1 "$ROOT/tests/smartdns-order-harness.lua" "$PLUGIN" >/dev/null

echo "smartdns-plugin contracts OK"
