#!/bin/bash
set -euo pipefail
# Contract: shipped dnsdist-panel exposes the read-only status endpoints used by
# the Obsidian Telemetry UI, and the UI stays offline-capable. Static checks
# only; no privileged runtime and no network required.
ROOT=$(cd "$(dirname "$0")/.." && pwd)
MAIN="$ROOT/panel/main.go"
STATUS="$ROOT/panel/status.go"
UI="$ROOT/panel/static/index.html"

# Every new read endpoint must be registered on the real mux.
for route in \
  '/api/rpz/status' \
  '/api/rpz/test' \
  '/api/upstream/status' \
  '/api/dnstap/status' \
  '/api/cluster/tokens' \
  '/api/health'
do
  grep -Fq "mux.HandleFunc(\"$route\"" "$MAIN" || {
    echo "missing route registration: $route" >&2
    exit 1
  }
done

# Read endpoints must be auth-wrapped except the intentional liveness probe.
grep -Fq 'auth(handleRPZStatus)' "$MAIN"
grep -Fq 'auth(handleRPZTest)' "$MAIN"
grep -Fq 'auth(handleUpstreamStatus)' "$MAIN"
grep -Fq 'auth(handleDnstapStatus)' "$MAIN"
grep -Fq 'auth(handleClusterTokens)' "$MAIN"
grep -Fq 'HandleFunc("/api/health", handleHealth)' "$MAIN"

# Honesty contract: the status endpoints must not assert health or feed state
# they cannot prove. These symbols must be booleans-free projections.
grep -Fq 'Health  string `json:"health"`' "$STATUS"
grep -Fq 'Status   string `json:"status"`' "$STATUS"
grep -Eq '"probe_support":[[:space:]]+false' "$STATUS"
if grep -Fq 'Healthy  bool' "$STATUS"; then
  echo "regression: fabricated bool health returned" >&2
  exit 1
fi
if grep -Eq '"active":[[:space:]]+bool' "$STATUS"; then
  echo "regression: fabricated bool feed active returned" >&2
  exit 1
fi

# UI must stay offline: no external resource loading.
if grep -Eq '<(link|script|img)[^>]*(src|href)="https?://' "$UI"; then
  echo "regression: UI loads an external resource; panel must stay offline" >&2
  exit 1
fi

# UI must not reference CSS custom properties that are never defined.
missing=$(python3 - "$UI" <<'PY'
import re, sys
src = open(sys.argv[1], encoding='utf-8').read()
defined = set(re.findall(r'^\s*(--[a-z0-9-]+)\s*:', src, re.M))
used = set(re.findall(r'var\((--[a-z0-9-]+)\)', src))
print(" ".join(sorted(used - defined)))
PY
)
if [ -n "$missing" ]; then
  echo "regression: undefined CSS variables: $missing" >&2
  exit 1
fi

echo "dnsdist-panel status contracts OK"
