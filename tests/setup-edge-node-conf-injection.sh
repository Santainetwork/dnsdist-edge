#!/bin/bash
set -euo pipefail
# Regression test for the node.conf shell-injection in setup-edge.sh.
#
# Single write path: save_config writes every SAVED_* value escaped with
# _sq_escape. do_set_cdb_sources only calls save_config (no sed/echo writer).
# node.conf is read as DATA by _load_node_conf, never sourced as root.
#
# What this test pins
# -------------------
#   1. No payload from an adversarial corpus executes code (safety), both when
#      read with _load_node_conf and when the written file is sourced in a
#      subshell.
#   2. Legitimate values round-trip EXACTLY (fidelity).
#   3. save_config escapes every SAVED_* line, and _sq_escape strips controls.
#
# Runs the REAL save_config and do_set_cdb_sources extracted from the installer.
# No privileged runtime, no network, /etc/dnsdist is never touched.
ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCRIPT="$ROOT/setup/setup-edge.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

# setup-edge.sh runs privileged dispatch at the bottom, so it cannot be sourced.
extract() {
    awk -v fn="$1" '$0 ~ "^" fn "\\(\\) \\{" {on=1} on {print} on && /^\}$/ {exit}' "$SCRIPT"
}
for fn in _sq_escape _load_node_conf save_config do_set_cdb_sources; do
    body=$(extract "$fn")
    [ -n "$body" ] || fail "could not extract $fn from setup-edge.sh"
    eval "$body"
done

# Variables save_config reads. Defaults mirror the installer's empty state.
GREEN=""; CYAN=""; RED=""; YELLOW=""; NC=""
SCRIPT_VERSION="3.2.0"
CONF_DIR="$TMP"
CONF="$TMP/node.conf"
CONFIG_SAVE_FILE="$CONF"
CENTRAL_DB_URL="http://central/trust.db"
CDB_SOURCES=""
UPSTREAM_DNS="1.1.1.1"
CHOSEN_MODE="rpz"
RPZ_IPS=""
CERT_MODE="3"
CERT_DOMAIN=""
CERT_EMAIL=""
WEBSERVER_PASSWORD="pw"
WEBSERVER_APIKEY="key"
MASTER_URL=""
ENROLL_TOKEN=""
WITH_BLOCKPAGE="false"
BLOCKPAGE_ADDR=""
BLOCKPAGE_WEBROOT=""
NODE_NAME=""
TRANSPARENT_MODE="off"
TRANSPARENT_INTERFACE=""
TRANSPARENT_SUBNET=""
MARKER="$TMP/MARK"

# ── 1 + 2. Safety and fidelity over an adversarial corpus ────────────────────
safety_fail=0
fidelity_fail=0
checked=0

check_payload() {
    local name="$1" payload="$2"
    # __M__ becomes an ABSOLUTE marker path so an injected touch is observable
    # regardless of the test's working directory.
    payload="${payload//__M__/$MARKER}"
    CDB_SOURCES="$payload"
    rm -f "$CONF" "$MARKER"

    do_set_cdb_sources >/dev/null 2>&1 || fail "[$name] do_set_cdb_sources returned non-zero"

    # Safety A: the reader must not execute anything.
    unset SAVED_CDB_SOURCES
    _load_node_conf "$CONF"
    if [ -f "$MARKER" ]; then
        echo "  FAIL [$name] reader executed payload: $payload" >&2
        safety_fail=$((safety_fail + 1))
    fi
    # Fidelity: the value must come back exactly.
    if [ "${SAVED_CDB_SOURCES:-}" != "$payload" ]; then
        echo "  FAIL [$name] value not preserved" >&2
        echo "       wrote:    $payload" >&2
        echo "       readback: ${SAVED_CDB_SOURCES:-<unset>}" >&2
        fidelity_fail=$((fidelity_fail + 1))
    fi

    # Safety B: the written file, if anything still sourced it, must not execute.
    rm -f "$MARKER"
    ( . "$CONF" ) >/dev/null 2>&1 || true
    if [ -f "$MARKER" ]; then
        echo "  FAIL [$name] sourced node.conf executed payload" >&2
        safety_fail=$((safety_fail + 1))
    fi
    unset SAVED_CDB_SOURCES
    checked=$((checked + 1))
}

check_payload "double-quote-inject" 'http://a";touch __M__;echo "'
check_payload "single-quote"        "http://a';touch __M__;echo '"
check_payload "dollar-paren"        'http://a$(touch __M__)b'
check_payload "dollar-brace"        'http://a${IFS}b'
check_payload "backtick"            'http://a`touch __M__`b'
check_payload "semicolon"           'http://a;touch __M__;'
check_payload "ampersand"           'http://a&&touch __M__'
check_payload "pipe"                'http://a|touch __M__'
check_payload "backslash"           'http://a\touch __M__'
check_payload "escaped-dollar"      'http://a\$HOME/b'
check_payload "sed-ampersand"       'http://a&b.example/db'
check_payload "sed-pipe-delim"      'http://a|b.example/db'
check_payload "normal-multi-url"    'http://central/trust.db,http://mirror:8084/cdb/blacklist.db'
check_payload "normal-with-port"    'http://10.0.0.5:8084/files/trust.db'
check_payload "normal-https"        'https://cdn.example.org/db/trust.db'

[ "$safety_fail" -eq 0 ] || fail "$safety_fail payload(s) executed code"
[ "$fidelity_fail" -eq 0 ] || fail "$fidelity_fail payload(s) lost fidelity"

# ── 3. save_config must escape EVERY value it writes ─────────────────────────
save_body=$(awk '/^save_config\(\) \{/,/^\}$/' "$SCRIPT")
unescaped=$(printf '%s\n' "$save_body" \
    | grep -oE '^SAVED_[A-Z_]+="[^"]*"' \
    | grep -v '_sq_escape' || true)
if [ -n "$unescaped" ]; then
    echo "FAIL: save_config writes unescaped value(s):" >&2
    printf '%s\n' "$unescaped" >&2
    exit 1
fi
total_saved=$(printf '%s\n' "$save_body" | grep -cE '^SAVED_[A-Z_]+=' || true)
[ "$total_saved" -ge 15 ] || fail "save_config appears truncated ($total_saved SAVED_ lines)"

# Control characters must be stripped, or a value could break out of its line.
esc=$(printf 'a\nb\tc' | { _sq_escape "$(cat)"; })
[ "$esc" = "abc" ] || fail "control characters not stripped: got '$esc'"

echo "PASS ($checked payloads, $total_saved escaped config values)"
