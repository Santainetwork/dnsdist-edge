#!/bin/bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
DB_DIR="$TMP/db"
BIN="$TMP/bin"
mkdir -p "$DB_DIR" "$BIN"

printf '%2048s' active > "$DB_DIR/source.db"
OLD_SHA=$(printf old | sha256sum | awk '{print $1}')
KEEP_SHA=$(sha256sum "$DB_DIR/source.db" | awk '{print $1}')
printf old > "$DB_DIR/blacklist.$OLD_SHA.db"
printf keep > "$DB_DIR/blacklist.backup.db"

cat > "$BIN/aria2c" <<'ARIA'
#!/bin/sh
out=""; dir=""
for arg in "$@"; do
  case "$arg" in --out=*) out=${arg#--out=};; --dir=*) dir=${arg#--dir=};; esac
done
cp "$TEST_SOURCE" "$dir/$out"
ARIA
chmod +x "$BIN/aria2c"
cat > "$BIN/id" <<'ID'
#!/bin/sh
exit 1
ID
chmod +x "$BIN/id"

PATH="$BIN:$PATH" DB_DIR="$DB_DIR" DB_FILE="$DB_DIR/blacklist.db" TEST_SOURCE="$DB_DIR/source.db" CENTRAL_DB_URLS="http://example.test/trust.db" \
  bash "$ROOT/setup/update-blacklist.sh" >/dev/null

[ -L "$DB_DIR/blacklist.db" ]
[ "$(readlink "$DB_DIR/blacklist.db")" = "blacklist.$KEEP_SHA.db" ]
[ -f "$DB_DIR/blacklist.$KEEP_SHA.db" ]
[ ! -e "$DB_DIR/blacklist.$OLD_SHA.db" ]
[ -f "$DB_DIR/blacklist.backup.db" ]
echo PASS

# Static guard for the rarely-used standalone Master fallback.
grep -q '\[\[ "$OLD_HASH" =~ \^\[0-9a-f\]{64}\$ \]\]' "$ROOT/setup/build-master-cdb.sh"
grep -q 'rm -f -- "$OLD_DB"' "$ROOT/setup/build-master-cdb.sh"
