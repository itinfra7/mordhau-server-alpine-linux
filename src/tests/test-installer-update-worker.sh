#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE_SCRIPT="$SCRIPT_DIR/../mordhau-server-alpine-linux.sh"
TEST_ROOT=$(mktemp -d /tmp/mordhau-installer-update-worker.XXXXXX)
MORDHAU_ROOT="$TEST_ROOT/mordhau"
WEB_BINARY="$MORDHAU_ROOT/bin/mordhau-web"
WORKER_PID=""
WEB_BINARY_TEMP=""

cleanup() {
    if [ -n "$WORKER_PID" ]; then
        kill "$WORKER_PID" >/dev/null 2>&1 || true
        wait "$WORKER_PID" >/dev/null 2>&1 || true
    fi
    case "$TEST_ROOT" in
        /tmp/mordhau-installer-update-worker.*)
            if [ -e "$TEST_ROOT" ]; then
                find "$TEST_ROOT" -xdev -depth -delete
            fi
            ;;
    esac
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$MORDHAU_ROOT/bin"
printf '%s\n' \
    'package main' \
    'import "time"' \
    'func main() { time.Sleep(30 * time.Second) }' \
    > "$TEST_ROOT/sleeper.go"
go build -o "$WEB_BINARY" "$TEST_ROOT/sleeper.go"
chmod 700 "$WEB_BINARY"

{
    sed -n '/^process_is_executable() {$/,/^}$/p' "$SOURCE_SCRIPT"
    sed -n '/^find_web_pid() {$/,/^}$/p' "$SOURCE_SCRIPT"
    sed -n '/^install_web_binary() {$/,/^}$/p' "$SOURCE_SCRIPT"
} > "$TEST_ROOT/functions.sh"

# shellcheck disable=SC1091
. "$TEST_ROOT/functions.sh"

"$WEB_BINARY" &
WORKER_PID=$!
sleep 1

[ "$(find_web_pid)" = "$WORKER_PID" ]
MORDHAU_MANAGER_UPDATE_WORKER_PID=$WORKER_PID
export MORDHAU_MANAGER_UPDATE_WORKER_PID
if find_web_pid > "$TEST_ROOT/unexpected-pid"; then
    printf '%s\n' 'The installer selected its detached update worker for shutdown.' >&2
    exit 1
fi

printf '%s\n' \
    'package main' \
    'func main() {}' \
    > "$TEST_ROOT/replacement.go"
go build -o "$TEST_ROOT/replacement" "$TEST_ROOT/replacement.go"
before=$(sha256sum "$WEB_BINARY" | awk '{print $1}')
replacement=$(sha256sum "$TEST_ROOT/replacement" | awk '{print $1}')
[ "$before" != "$replacement" ]
install_web_binary "$TEST_ROOT/replacement"
after=$(sha256sum "$WEB_BINARY" | awk '{print $1}')
[ "$after" = "$replacement" ]
kill -0 "$WORKER_PID"
[ -z "$(find "$MORDHAU_ROOT/bin" -maxdepth 1 -name '.mordhau-web.*' -print)" ]

printf '%s\n' 'Installer update-worker exclusion tests passed.'
