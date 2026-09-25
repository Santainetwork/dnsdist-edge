#!/bin/bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
INSTALLER="$ROOT/setup/setup-master.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

assert_file_has() {
    local file=$1 text=$2
    grep -Fq -- "$text" "$file" || fail "$file missing: $text"
}

assert_before() {
    local file=$1 first=$2 second=$3 first_line second_line
    first_line=$(grep -Fn -- "$first" "$file" | head -1 | cut -d: -f1)
    second_line=$(grep -Fn -- "$second" "$file" | head -1 | cut -d: -f1)
    [ -n "$first_line" ] && [ -n "$second_line" ] && [ "$first_line" -lt "$second_line" ] ||
        fail "$first must precede $second in $file"
}

make_bundle() {
    local dir=$1
    mkdir -p "$dir"
    printf '#!/bin/sh\nexit 0\n' > "$dir/rpz-master"
    chmod 0755 "$dir/rpz-master"
}

render() {
    local master_dir=$1 output=$2
    shift 2
    MASTER_DIR="$master_dir" bash "$INSTALLER" --render-plan "$output" "$@"
}

# Default and explicit feeds retain legacy ownership. Resolver choice stays independent.
make_bundle "$TMP/bundle"
render "$TMP/bundle" "$TMP/default"
assert_file_has "$TMP/default/actions.txt" 'source-mode=feeds'
assert_file_has "$TMP/default/actions.txt" 'dnsdist=false'
assert_file_has "$TMP/default/actions.txt" 'feed-build-required'
assert_file_has "$TMP/default/actions.txt" 'feed-cron-install'
assert_before "$TMP/default/actions.txt" 'feed-build-required' 'rpz-disable-after-feed-build'
assert_file_has "$INSTALLER" 'persist_source_mode rpz-slave'
assert_file_has "$TMP/default/panel.env" 'PANEL_SOURCE_MODE=feeds'
assert_file_has "$TMP/default/panel.env" 'PANEL_BUILD_INTERVAL=6h'
render "$TMP/bundle" "$TMP/feeds-dnsdist" --source-mode feeds --with-dnsdist
assert_file_has "$TMP/feeds-dnsdist/actions.txt" 'source-mode=feeds'
assert_file_has "$TMP/feeds-dnsdist/actions.txt" 'dnsdist=true'

# RPZ mode requires explicit upstream and FQDN, validates all source settings.
if render "$TMP/bundle" "$TMP/missing-upstream" --source-mode rpz-slave --rpz-zone rpz.example.; then
    fail 'rpz-slave accepted missing upstream'
fi
if render "$TMP/bundle" "$TMP/missing-zone" --source-mode rpz-slave --rpz-upstream 192.0.2.1:53; then
    fail 'rpz-slave accepted missing zone'
fi
if render "$TMP/bundle" "$TMP/bad-mode" --source-mode typo; then
    fail 'invalid source mode accepted'
fi
if render "$TMP/bundle" "$TMP/bad-upstream" --source-mode rpz-slave --rpz-upstream no-port --rpz-zone rpz.example.; then
    fail 'invalid upstream accepted'
fi
if render "$TMP/bundle" "$TMP/bad-zone" --source-mode rpz-slave --rpz-upstream 192.0.2.1:53 --rpz-zone not_fqdn; then
    fail 'invalid zone accepted'
fi

secret="$TMP/tsig.secret"
printf 'not-embedded-secret\n' > "$secret"
chmod 0600 "$secret"
render "$TMP/bundle" "$TMP/rpz" \
    --source-mode rpz-slave \
    --rpz-upstream 192.0.2.1:53 \
    --rpz-zone rpz.example. \
    --rpz-bootstrap-url https://bootstrap.example/domains \
    --rpz-check-interval 10m \
    --rpz-transfer-acl 127.0.0.0/8,192.0.2.0/24 \
    --rpz-tsig-key transfer-key. \
    --rpz-tsig-secret-file "$secret" \
    --with-dnsdist

assert_file_has "$TMP/rpz/actions.txt" 'source-mode=rpz-slave'
assert_file_has "$TMP/rpz/actions.txt" 'dnsdist=true'
assert_file_has "$TMP/rpz/actions.txt" 'binary-source=bundled'
assert_before "$TMP/rpz/actions.txt" 'feed-cron-remove' 'rpz-service-enable'
assert_file_has "$INSTALLER" 'systemctl restart rpz-master'
assert_file_has "$TMP/rpz/rpz-master.json" '"source_mode": "rpz-slave"'
assert_file_has "$TMP/rpz/rpz-master.json" '"upstream_master": "192.0.2.1:53"'
assert_file_has "$TMP/rpz/rpz-master.json" '"zone": "rpz.example."'
assert_file_has "$TMP/rpz/rpz-master.json" '"listen_dns": "0.0.0.0:5354"'
assert_file_has "$TMP/rpz/rpz-master.json" '"source_domain_url": "https://bootstrap.example/domains"'
assert_file_has "$TMP/rpz/rpz-master.json" '"check_interval": "10m"'
assert_file_has "$TMP/rpz/rpz-master.json" '"transfer_acl": ["127.0.0.0/8", "192.0.2.0/24"]'
assert_file_has "$TMP/rpz/rpz-master.json" '"tsig_secret_file": "/etc/dnsdist-master/rpz-upstream.secret"'
! grep -Fq 'not-embedded-secret' "$TMP/rpz/rpz-master.json" || fail 'TSIG secret embedded in JSON'
[ "$(stat -c %a "$TMP/rpz/rpz-master.json")" = 600 ] || fail 'RPZ JSON is not mode 0600'
assert_file_has "$TMP/rpz/rpz-master.service" 'ExecStart=/usr/local/bin/rpz-master -c /etc/dnsdist-master/rpz-master.json -action serve'
assert_file_has "$TMP/rpz/panel.env" 'PANEL_SOURCE_MODE=rpz-slave'
assert_file_has "$TMP/rpz/panel.env" 'PANEL_BUILD_INTERVAL=0'
assert_file_has "$INSTALLER" 'dnsdist-panel.service.d/source-mode.conf'
assert_file_has "$INSTALLER" 'EnvironmentFile=-$CONF_DIR/panel.env'
assert_file_has "$INSTALLER" 'ExecStart=/usr/local/bin/dnsdist-panel -build-interval \${PANEL_BUILD_INTERVAL}'
assert_file_has "$ROOT/setup/build-master-cdb.sh" 'source mode aktif adalah rpz-slave'

# Standalone release fallback requires SHA256SUMS verification before privileged work.
mkdir -p "$TMP/empty" "$TMP/release" "$TMP/stubs"
printf '#!/bin/sh\nexit 0\n' > "$TMP/release/rpz-master"
chmod 0755 "$TMP/release/rpz-master"
(
    cd "$TMP/release"
    sha256sum rpz-master > SHA256SUMS
)
RPZ_MASTER_RELEASE_URL="file://$TMP/release/rpz-master" \
    render "$TMP/empty" "$TMP/release-plan" --source-mode rpz-slave \
    --rpz-upstream 192.0.2.1:53 --rpz-zone rpz.example.
assert_file_has "$TMP/release-plan/actions.txt" 'binary-source=verified-release'
[ -x "$TMP/release-plan/rpz-master" ] || fail 'verified release binary not staged'

# Missing or mismatched checksum must fail before apt-get/systemctl.
cat > "$TMP/stubs/apt-get" <<'STUB'
#!/bin/sh
echo apt-get >> "$SIDE_EFFECTS"
STUB
cat > "$TMP/stubs/systemctl" <<'STUB'
#!/bin/sh
echo systemctl >> "$SIDE_EFFECTS"
STUB
chmod +x "$TMP/stubs/apt-get" "$TMP/stubs/systemctl"
printf '%064d  rpz-master\n' 0 > "$TMP/release/SHA256SUMS"
if PATH="$TMP/stubs:$PATH" SIDE_EFFECTS="$TMP/side-effects" \
    RPZ_MASTER_RELEASE_URL="file://$TMP/release/rpz-master" MASTER_DIR="$TMP/empty" \
    bash "$INSTALLER" --install --source-mode rpz-slave \
    --rpz-upstream 192.0.2.1:53 --rpz-zone rpz.example. --no-dnsdist; then
    fail 'RPZ install accepted mismatched release checksum'
fi
[ ! -e "$TMP/side-effects" ] || fail 'failed preflight ran apt-get/systemctl'

# Generated binary stays untracked. Source/module files remain trackable.
assert_file_has "$ROOT/.gitignore" '/tools/rpz-master/rpz-master'

echo PASS
