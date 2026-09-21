#!/bin/bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/bin"

cat > "$scratch/bin/curl" <<'MOCK'
#!/bin/bash
set -e
url=""
out=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) out=$2; shift ;;
        http*) url=$1 ;;
    esac
    shift
done
    case "$url" in
        */SHA256SUMS)
        [ -n "${CHECKSUM_SOURCE:-}" ] || exit 22
        cp "$CHECKSUM_SOURCE" "$out"
        ;;
    *) cp "$DOWNLOAD_SOURCE" "$out" ;;
esac
MOCK
chmod +x "$scratch/bin/curl"

assert_preserved_on_bad_update() {
    local name=$1
    local target="$scratch/$name"
    cp "$root/setup/$name" "$target"
    cp "$target" "$target.before"
    printf '#!/bin/bash\nif then\n' > "$scratch/download"

    if PATH="$scratch/bin:$PATH" DOWNLOAD_SOURCE="$scratch/download" \
        SELF_UPDATE_URL="https://example.test/$name" bash "$target" --upgrade >/dev/null 2>&1; then
        echo "$name accepted invalid shell" >&2
        exit 1
    fi
    cmp -s "$target.before" "$target" || {
        echo "$name replaced itself before validation" >&2
        exit 1
    }
    if compgen -G "$scratch/.${name}.tmp.*" >/dev/null; then
        echo "$name left a temporary file after failed validation" >&2
        exit 1
    fi
}

assert_checksum_and_reexec() {
    local name=$1
    local target="$scratch/$name"
    cp "$root/setup/$name" "$target"
cat > "$scratch/download" <<'UPDATED'
#!/bin/bash
[ "${1:-}" = "--upgrade" ]
[ "${SELF_UPDATE_DONE:-}" = true ]
echo "updated process resumed"
UPDATED
    chmod 0755 "$scratch/download"
    printf '%064d  other.sh\n%064d  %s\n' 0 0 "$name" > "$scratch/download.sha256"

    if PATH="$scratch/bin:$PATH" DOWNLOAD_SOURCE="$scratch/download" \
        CHECKSUM_SOURCE="$scratch/download.sha256" \
        SELF_UPDATE_URL="https://example.test/$name" bash "$target" --upgrade >/dev/null 2>&1; then
        echo "$name accepted checksum mismatch" >&2
        exit 1
    fi

    sha256sum "$scratch/download" | awk -v name="$name" '{ print "0000000000000000000000000000000000000000000000000000000000000000  other.sh"; print $1 "  " name }' > "$scratch/download.sha256"
    output=$(PATH="$scratch/bin:$PATH" DOWNLOAD_SOURCE="$scratch/download" \
        CHECKSUM_SOURCE="$scratch/download.sha256" \
        SELF_UPDATE_URL="https://example.test/$name" bash "$target" --upgrade)
    grep -Fq 'updated process resumed' <<< "$output"
    cmp -s "$scratch/download" "$target"
}

for installer in setup-edge.sh setup-master.sh; do
    assert_preserved_on_bad_update "$installer"
    assert_checksum_and_reexec "$installer"
done

echo "setup self-update tests PASS"
