#!/bin/bash
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)

# F15: do_set_webserver writes the password/apikey into a Lua string literal in
# dnsdist.conf. A valid password containing Lua/sed metacharacters (', &, |, \)
# used to corrupt the config: sed replaced with the whole match on '&', broke on
# '|', and an unescaped ' produced invalid Lua, so dnsdist could not start.
#
# Contract: the two writes must escape for BOTH the Lua literal and the sed
# replacement, inline in do_set_webserver (no external helper dependency).
grep -qF "password = '\${_pw}'" "$ROOT/setup/setup-edge.sh"
grep -qF "apiKey   = '\${_key}'" "$ROOT/setup/setup-edge.sh"
grep -qF '_pw=${WEBSERVER_PASSWORD//' "$ROOT/setup/setup-edge.sh"
grep -qF '_pw=${_pw//|/' "$ROOT/setup/setup-edge.sh"

# Runtime proof: run the real helpers against a scratch config and read the
# value back through the Lua interpreter. The stored value must equal the input
# byte-for-byte for every metacharacter case.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
conf="$tmp/dnsdist.conf"

# Extract the real write block from the installer so this test exercises the
# shipped code, not a copy. Only the DNSDIST_CONF target is rebound to a scratch file.
sed -n '/_pw=\${WEBSERVER_PASSWORD/,/apiKey   = /p' "$ROOT/setup/setup-edge.sh" \
  | sed 's|\$DNSDIST_CONF|$conf|g' > "$tmp/write.sh"
test -s "$tmp/write.sh"
write_webserver() {
  local _pw _key
  # shellcheck disable=SC1090
  source "$tmp/write.sh"
}

readback="$tmp/readback.lua"
cat > "$readback" <<'LUA'
local t = {}
setWebserverConfig = function(c) t = c end
local f, e = loadfile(arg[1])
if not f then io.write("LUA_ERR: " .. tostring(e)); os.exit(3) end
f()
io.write((t.password or "<nil>") .. "\n" .. (t.apiKey or "<nil>"))
LUA

cases=('p&q' "it's" 'a|b' 'back\slash' 'mix&|\'"'"'x' 'plain')
for pw in "${cases[@]}"; do
  printf "setWebserverConfig({password = 'oldpass', apiKey   = 'oldkey'})\n" > "$conf"
  WEBSERVER_PASSWORD="$pw" WEBSERVER_APIKEY='k&y' write_webserver
  out=$(lua5.1 "$readback" "$conf")
  got=$(printf '%s\n' "$out" | sed -n 1p)
  key=$(printf '%s\n' "$out" | sed -n 2p)
  if [ "$got" != "$pw" ] || [ "$key" != 'k&y' ]; then
    echo "F15 FAIL: input=[$pw] stored password=[$got] apikey=[$key]"
    exit 1
  fi
done

# Negative control: the OLD unescaped write must corrupt at least one case, so
# this test would actually catch the regression it guards.
printf "setWebserverConfig({password = 'oldpass', apiKey   = 'oldkey'})\n" > "$conf"
sed -i "s|password = '[^']*'|password = 'p&q'|g" "$conf"
bad=$(lua5.1 "$readback" "$conf" 2>/dev/null | sed -n 1p || true)
if [ "$bad" = 'p&q' ]; then
  echo "F15 negative control failed: unescaped write looked correct"
  exit 1
fi

echo "smartdns webserver-escape contract OK"