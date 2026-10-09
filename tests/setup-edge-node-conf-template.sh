#!/bin/bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMPL="$ROOT/setup/templates/node.conf.j2"

# F17c: node.conf.j2 must render one SAVED_* assignment per line even when a
# password/apikey is set. Ansible's Jinja environment defaults trim_blocks=True
# (verified: ansible-core 2.21.5 _jinja_bits.py "trim_blocks: bool = True"), so an
# inline `{% if %}...{% endif %}` glued the following assignment onto the same line,
# dropping SAVED_WEBSERVER_APIKEY / SAVED_INSTALL_DATE when node.conf is sourced.

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 not available; skipping Jinja render check"
  exit 0
fi
if ! python3 -c 'import jinja2' 2>/dev/null; then
  echo "jinja2 not available; skipping Jinja render check"
  exit 0
fi

out=$(python3 - "$TMPL" <<'PY'
import sys, jinja2
src = open(sys.argv[1]).read()
ctx = dict(central_db_url="http://c", upstream_dns="1.1.1.1", block_mode="adguard",
           rpz_ips="0.0.0.0", cert_mode="self", webserver_password="p&q",
           webserver_apikey="k", ansible_date_time={"iso8601": "2026-10-09T00:00:00Z"})
# trim_blocks=True mirrors Ansible's default.
env = jinja2.Environment(trim_blocks=True, lstrip_blocks=False)
sys.stdout.write(env.from_string(src).render(**ctx))
PY
)

# Exactly one SAVED_* per line: no line may contain two assignments glued together.
if printf '%s\n' "$out" | grep -qE '"[[:space:]]*SAVED_'; then
  echo "F17c FAIL: two SAVED_ assignments glued on one line:"
  printf '%s\n' "$out" | grep -nE '"[[:space:]]*SAVED_'
  exit 1
fi

# Each expected key must appear on its own line with the value intact.
for kv in \
  'SAVED_WEBSERVER_PASSWORD="p&q"' \
  'SAVED_WEBSERVER_APIKEY="k"' \
  'SAVED_INSTALL_DATE="2026-10-09T00:00:00Z"'; do
  key=${kv%%=*}
  line=$(printf '%s\n' "$out" | grep -E "^${key}=" || true)
  if [ "$line" != "$kv" ]; then
    echo "F17c FAIL: expected line [$kv], got [$line]"
    exit 1
  fi
done

# Empty password/apikey must render zero such lines (and not an empty assignment).
out2=$(python3 - "$TMPL" <<'PY'
import sys, jinja2
src = open(sys.argv[1]).read()
ctx = dict(central_db_url="http://c", upstream_dns="1.1.1.1", block_mode="adguard",
           rpz_ips="0.0.0.0", cert_mode="self", webserver_password="",
           webserver_apikey="", ansible_date_time={"iso8601": "2026-10-09T00:00:00Z"})
env = jinja2.Environment(trim_blocks=True, lstrip_blocks=False)
sys.stdout.write(env.from_string(src).render(**ctx))
PY
)
if printf '%s\n' "$out2" | grep -qE '^SAVED_WEBSERVER_(PASSWORD|APIKEY)='; then
  echo "F17c FAIL: empty password/apikey still rendered an assignment"
  exit 1
fi
if ! printf '%s\n' "$out2" | grep -q '^SAVED_INSTALL_DATE='; then
  echo "F17c FAIL: SAVED_INSTALL_DATE missing when password empty"
  exit 1
fi

echo "node.conf.j2 trim_blocks contract OK"
