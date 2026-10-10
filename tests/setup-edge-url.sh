#!/bin/bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)

# Fresh install must export the CLI URL through the variable consumed by
# update-blacklist.sh. Static contract avoids executing privileged installer.
grep -q 'CENTRAL_DB_URLS="$CENTRAL_DB_URL"' "$ROOT/setup/setup-edge.sh"
install_exports=$(grep -c 'export CENTRAL_DB_URLS' "$ROOT/setup/setup-edge.sh")
[ "$install_exports" -ge 2 ]

# Saved config remains the durable source after first install.
# The value must pass through _sq_escape: node.conf is `source`d as root, so an
# unescaped value would be executable shell (see the CDB_SOURCES injection).
grep -qE '^SAVED_CENTRAL_DB_URL="\$\(_sq_escape "\$CENTRAL_DB_URL"\)"' "$ROOT/setup/setup-edge.sh"
grep -q 'SAVED_CENTRAL_DB_URL.*CENTRAL_DB_URLS=' "$ROOT/setup/update-blacklist.sh"

# Runtime proof: caller-provided URL wins over updater default.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/db"
printf '%2048s' valid > "$tmp/source.db"
cat > "$tmp/bin/aria2c" <<'ARIA'
#!/bin/sh
out=""; dir=""
for arg in "$@"; do
  case "$arg" in --out=*) out=${arg#--out=};; --dir=*) dir=${arg#--dir=};; esac
done
printf '%s\n' "$*" > "$CAPTURE"
cp "$TEST_SOURCE" "$dir/$out"
ARIA
chmod +x "$tmp/bin/aria2c"
cat > "$tmp/bin/id" <<'ID'
#!/bin/sh
exit 1
ID
chmod +x "$tmp/bin/id"

url='http://rpz-master.example:8080/files/trust.db'
PATH="$tmp/bin:$PATH" DB_DIR="$tmp/db" DB_FILE="$tmp/db/blacklist.db" \
  TEST_SOURCE="$tmp/source.db" CAPTURE="$tmp/aria.args" CENTRAL_DB_URLS="$url" \
  STATUS_WEBROOT="$tmp/www" \
  bash "$ROOT/setup/update-blacklist.sh" >/dev/null

grep -Fq -- "$url" "$tmp/aria.args"
echo PASS
