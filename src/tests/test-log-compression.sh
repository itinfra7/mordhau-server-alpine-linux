#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE_SCRIPT="$SCRIPT_DIR/../templates/server.sh"
TEST_ROOT=$(mktemp -d /tmp/mordhau-log-compression.XXXXXX)

cleanup() {
    case "$TEST_ROOT" in
        /tmp/mordhau-log-compression.*)
            if [ -e "$TEST_ROOT" ]; then
                find "$TEST_ROOT" -xdev -depth -delete
            fi
            ;;
    esac
}
trap cleanup EXIT HUP INT TERM

command -v xz >/dev/null 2>&1 || {
    printf '%s\n' 'xz is required for the log-compression test.' >&2
    exit 1
}

MORDHAU_ROOT="$TEST_ROOT/mordhau"
ARCHIVE_DIR="$MORDHAU_ROOT/log"
EXPECTED="$TEST_ROOT/expected.log"
TEST_SCRIPT="$TEST_ROOT/server.sh"
ARCHIVE="$ARCHIVE_DIR/Mordhau_2026-07-28_22-00-19.log"
COMPRESSED="${ARCHIVE}.xz"

mkdir -p "$ARCHIVE_DIR"
awk 'BEGIN { for (i = 0; i < 10000; i++) print "[2026.07.28-22.00.00:000][100]LogDread: repeated record" }' \
    > "$EXPECTED"
cp "$EXPECTED" "$ARCHIVE"
sed "s|^ROOT=/root/mordhau$|ROOT=$MORDHAU_ROOT|" \
    "$SOURCE_SCRIPT" > "$TEST_SCRIPT"
chmod 700 "$TEST_SCRIPT"

"$TEST_SCRIPT" compress-logs

[ ! -e "$ARCHIVE" ] || {
    printf '%s\n' 'uncompressed archive remains after verified compression' >&2
    exit 1
}
[ -f "$COMPRESSED" ] || {
    printf '%s\n' 'compressed archive was not created' >&2
    exit 1
}
xz -t -- "$COMPRESSED"
xz -dc -- "$COMPRESSED" | cmp "$EXPECTED" -
[ "$(stat -c '%a' "$COMPRESSED")" = "600" ] || {
    printf '%s\n' 'compressed archive permissions are not 0600' >&2
    exit 1
}

cp "$EXPECTED" "$ARCHIVE"
"$TEST_SCRIPT" compress-logs
[ ! -e "$ARCHIVE" ] || {
    printf '%s\n' 'verified duplicate source was not reconciled' >&2
    exit 1
}

printf '%s\n' 'different content' > "$ARCHIVE"
if "$TEST_SCRIPT" compress-logs; then
    printf '%s\n' 'mismatched archive collision was accepted' >&2
    exit 1
fi
[ -f "$ARCHIVE" ] && [ -f "$COMPRESSED" ] || {
    printf '%s\n' 'mismatched collision did not preserve both files' >&2
    exit 1
}
unlink "$ARCHIVE"

"$TEST_SCRIPT" compress-logs
printf '%s\n' 'Log compression test passed.'
