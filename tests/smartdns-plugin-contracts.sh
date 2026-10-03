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

echo "smartdns-plugin contracts OK"
