#!/bin/bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
installer="$root/setup/setup-edge.sh"

require() {
    grep -Fq -- "$1" "$installer" || {
        echo "missing installer contract: $1" >&2
        exit 1
    }
}

require 'PANEL_PASSWORD_FILE="${DB_DIR}/panel.password"'
require 'install_panel_password() {'
require 'if [ ! -f "$PANEL_PASSWORD_FILE" ] || [ "$PASSWORD_EXPLICIT" = true ]; then'
require 'install -m 0600 -o "$DNSDIST_USER" -g "$DNSDIST_USER" "$password_tmp" "$PANEL_PASSWORD_FILE"'
[ "$(grep -Fc 'install_panel_password' "$installer")" -ge 3 ] || {
    echo "password must sync during panel install and --password updates" >&2
    exit 1
}
require '--transparent-dns <off|auto|tproxy>'
require '--transparent-interface <IFACE>'
require '--transparent-subnet <CIDR>'
require 'do_configure_transparent_dns() {'
require '"$tproxy_helper" "${args[@]}"'
require 'do_configure_transparent_dns'
require 'SAVED_TRANSPARENT_MODE="${TRANSPARENT_MODE:-off}"'

dispatch=$(sed -n '/if \[ "$TRANSPARENT_EXPLICIT" = true \]/,$p' "$installer")
sync_line=$(grep -n 'if \[ "$SYNC_ONLY" = true \]' <<< "$dispatch" | head -1 | cut -d: -f1)
panel_line=$(grep -n 'if \[ "$WITH_PANEL" = true \]' <<< "$dispatch" | head -1 | cut -d: -f1)
[ -n "$sync_line" ] && [ -n "$panel_line" ] && [ "$sync_line" -lt "$panel_line" ] || {
    echo "sync-only dispatch must run before default panel dispatch" >&2
    exit 1
}

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
functions="$scratch/functions.sh"
sed '/^# --- Load Saved Config Silently ---/,$d' "$installer" > "$functions"
source "$functions"
DB_DIR="$scratch"
PANEL_PASSWORD_FILE="$scratch/panel.password"
DNSDIST_USER=$(id -un)
WEBSERVER_PASSWORD='initial-secret'
PASSWORD_EXPLICIT=false
install_panel_password
[ "$(cat "$PANEL_PASSWORD_FILE")" = 'initial-secret' ]
[ "$(stat -c %a "$PANEL_PASSWORD_FILE")" = 600 ]

WEBSERVER_PASSWORD='must-not-overwrite'
install_panel_password
[ "$(cat "$PANEL_PASSWORD_FILE")" = 'initial-secret' ]

WEBSERVER_PASSWORD='explicit-secret'
PASSWORD_EXPLICIT=true
install_panel_password
[ "$(cat "$PANEL_PASSWORD_FILE")" = 'explicit-secret' ]

echo "setup installer contracts PASS"
