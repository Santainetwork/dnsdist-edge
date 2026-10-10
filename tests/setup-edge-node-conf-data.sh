#!/bin/bash
# F17/F18 (option C): node.conf is DATA, never executed.
# Runtime proof against the SHIPPED code (extracted from the real files, not copies):
#  1. the real save_config writes hostile values escaped; the real _load_node_conf
#     reads them back byte-for-byte
#  2. hostile payload never executes (canary absent) through the reader
#  3. negative control: the old `source` path DOES execute the same payload
#  4. update-blacklist.sh reader does not execute the payload either
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -f "$tmp"/canary* "$tmp"/*.conf "$tmp"/*.sh 2>/dev/null; rmdir "$tmp" 2>/dev/null || true' EXIT

# Static contract: no node.conf source/dot left in either shipped script.
if grep -nE 'source[[:space:]]+"?\$CONFIG_SAVE_FILE|^[[:space:]]*\.[[:space:]]+"?\$NODE_CONF' \
     "$ROOT/setup/setup-edge.sh" "$ROOT/setup/update-blacklist.sh" >/dev/null; then
  echo "F17/F18 FAIL: node.conf still sourced"; exit 1
fi

# Extract the REAL save_config + helpers from the shipped file.
sed -n '/^_sq_escape()/,/^}/p;/^_load_node_conf()/,/^}/p;/^save_config()/,/^}/p' \
  "$ROOT/setup/setup-edge.sh" > "$tmp/impl.sh"
test -s "$tmp/impl.sh"
grep -q '^save_config()' "$tmp/impl.sh" || { echo "F17/F18 FAIL: save_config not extracted"; exit 1; }
# shellcheck disable=SC1090
. "$tmp/impl.sh"
# save_config prints with color vars defined by the installer; define them for the harness.
GREEN=''; CYAN=''; RED=''; YELLOW=''; NC=''

canary="$tmp/canary"
payload='x"; touch '"$canary"'; echo "a\b$c`d`'\''z'

# Run the REAL save_config with hostile values, config path rebound to scratch.
CONFIG_SAVE_FILE="$tmp/new.conf" CONF_DIR="$tmp" \
  SCRIPT_VERSION="3.2.0" CENTRAL_DB_URL="$payload" CDB_SOURCES="http://x/a,\$(touch $canary)" \
  UPSTREAM_DNS="$payload" CHOSEN_MODE="rpz" RPZ_IPS="$payload" CERT_MODE="3" CERT_DOMAIN="$payload" CERT_EMAIL="$payload" \
  WEBSERVER_PASSWORD="$payload" WEBSERVER_APIKEY="$payload" MASTER_URL="$payload" ENROLL_TOKEN="$payload" \
  WITH_BLOCKPAGE="false" BLOCKPAGE_ADDR="$payload" BLOCKPAGE_WEBROOT="$payload" NODE_NAME="$payload" \
  TRANSPARENT_MODE="off" TRANSPARENT_INTERFACE="$payload" TRANSPARENT_SUBNET="$payload" \
  save_config

# Read back through the real reader.
unset SAVED_NODE_NAME SAVED_WEBSERVER_PASSWORD SAVED_CDB_SOURCES SAVED_CENTRAL_DB_URL \
      SAVED_UPSTREAM_DNS SAVED_WEBSERVER_APIKEY SAVED_CERT_DOMAIN SAVED_CERT_EMAIL \
      SAVED_MASTER_URL SAVED_ENROLL_TOKEN SAVED_BLOCKPAGE_ADDR SAVED_BLOCKPAGE_WEBROOT \
      SAVED_TRANSPARENT_INTERFACE SAVED_TRANSPARENT_SUBNET SAVED_RPZ_IPS
_load_node_conf "$tmp/new.conf"
[ ! -e "$canary" ] || { echo "F17/F18 FAIL: payload executed by reader"; exit 1; }
# Every escaped field must round-trip byte-for-byte.
for var in SAVED_NODE_NAME SAVED_WEBSERVER_PASSWORD SAVED_WEBSERVER_APIKEY SAVED_CENTRAL_DB_URL \
           SAVED_UPSTREAM_DNS SAVED_CERT_DOMAIN SAVED_CERT_EMAIL SAVED_MASTER_URL SAVED_ENROLL_TOKEN \
           SAVED_BLOCKPAGE_ADDR SAVED_BLOCKPAGE_WEBROOT SAVED_TRANSPARENT_INTERFACE SAVED_TRANSPARENT_SUBNET SAVED_RPZ_IPS; do
  [ "${!var}" = "$payload" ] || { echo "F17/F18 FAIL: $var round-trip [${!var}] != [$payload]"; exit 1; }
done
[ "$SAVED_CDB_SOURCES" = "http://x/a,\$(touch $canary)" ] || { echo "F17/F18 FAIL: CDB_SOURCES round-trip [$SAVED_CDB_SOURCES]"; exit 1; }

# The file save_config WROTE must also be safe to execute (a real shell sources it
# in any regressed deployment). If the writer fails to escape, this executes the canary.
rm -f "$canary"
( . "$tmp/new.conf" ) >/dev/null 2>&1 || true
[ ! -e "$canary" ] || { echo "F17/F18 FAIL: save_config output executes when sourced (writer not escaping)"; exit 1; }

# Negative control: the old raw-splice form, read with `source`, MUST execute.
printf 'SAVED_NODE_NAME="%s"\n' "$payload" > "$tmp/old.conf"
( . "$tmp/old.conf" ) >/dev/null 2>&1 || true
[ -e "$canary" ] || { echo "F17/F18 negative control failed: source did not execute payload"; exit 1; }
rm -f "$canary"

# update-blacklist.sh reader, real block from the shipped script.
ub_cfg="$tmp/ub-cfg.sh"
end=$(grep -n '^# Token untuk master CDB publisher' "$ROOT/setup/update-blacklist.sh" | cut -d: -f1)
head -n $((end-1)) "$ROOT/setup/update-blacklist.sh" | sed "s|NODE_CONF=\"/etc/dnsdist/node.conf\"|NODE_CONF=\"$tmp/new.conf\"|" > "$ub_cfg"
bash "$ub_cfg" >/dev/null 2>&1 || true
[ ! -e "$canary" ] || { echo "F17/F18 FAIL: update-blacklist executed payload"; exit 1; }

echo "F17/F18 node.conf data-only contract OK"
