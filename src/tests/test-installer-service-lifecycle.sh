#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE_SCRIPT="$SCRIPT_DIR/../mordhau-server-alpine-linux.sh"
TEST_ROOT=$(mktemp -d /tmp/mordhau-installer-lifecycle.XXXXXX)

cleanup() {
    case "$TEST_ROOT" in
        /tmp/mordhau-installer-lifecycle.*)
            if [ -e "$TEST_ROOT" ]; then
                find "$TEST_ROOT" -xdev -depth -delete
            fi
            ;;
    esac
}
trap cleanup EXIT HUP INT TERM

MORDHAU_ROOT="$TEST_ROOT/mordhau"
TEST_INITD="$TEST_ROOT/init.d"
FAKE_BIN="$TEST_ROOT/bin"
CALLS="$TEST_ROOT/calls"
HARNESS="$TEST_ROOT/harness.sh"
export TEST_INITD CALLS

mkdir -p "$MORDHAU_ROOT" "$TEST_INITD" "$FAKE_BIN"
: > "$TEST_INITD/mordhau-server"
chmod 700 "$TEST_INITD/mordhau-server"

cat > "$MORDHAU_ROOT/server.sh" <<'EOF'
#!/bin/sh
printf 'server.sh %s\n' "$1" >> "$CALLS"
case "$1" in
    status)
        [ "${TEST_SERVER_RUNNING:-0}" -eq 1 ]
        ;;
    stop)
        exit 0
        ;;
    *)
        exit 2
        ;;
esac
EOF

cat > "$FAKE_BIN/rc-service" <<'EOF'
#!/bin/sh
printf 'rc-service %s %s\n' "$1" "$2" >> "$CALLS"
case "$1:$2" in
    mordhau-server:status)
        [ "${TEST_SERVER_OPENRC:-0}" -eq 1 ]
        ;;
    mordhau-server:stop)
        exit 0
        ;;
    mordhau-web:status)
        exit 1
        ;;
    *)
        exit 2
        ;;
esac
EOF
chmod 700 "$MORDHAU_ROOT/server.sh" "$FAKE_BIN/rc-service"

{
    printf '%s\n' '#!/bin/sh' 'set -eu'
    printf 'MORDHAU_ROOT=%s\n' "$MORDHAU_ROOT"
    printf 'TEST_INITD=%s\n' "$TEST_INITD"
    printf '%s\n' \
        'SERVER_WAS_RUNNING=0' \
        'WEB_WAS_RUNNING=0' \
        'log() { :; }' \
        'find_web_pid() { return 1; }'
    sed -n '/^remember_and_stop_services() {$/,/^}$/p' "$SOURCE_SCRIPT" |
        sed \
            -e 's|\[ -x /etc/init.d/mordhau-server \]|[ -x "$TEST_INITD/mordhau-server" ]|' \
            -e 's|\[ -x /etc/init.d/mordhau-web \]|[ -x "$TEST_INITD/mordhau-web" ]|'
    printf '%s\n' \
        'remember_and_stop_services' \
        'printf "server_was_running=%s\n" "$SERVER_WAS_RUNNING" >> "$CALLS"'
} > "$HARNESS"
chmod 700 "$HARNESS"

PATH="$FAKE_BIN:$PATH"
export PATH

: > "$CALLS"
TEST_SERVER_RUNNING=1
TEST_SERVER_OPENRC=1
export TEST_SERVER_RUNNING TEST_SERVER_OPENRC
"$HARNESS"
grep -Fxq 'server.sh status' "$CALLS"
grep -Fxq 'rc-service mordhau-server status' "$CALLS"
grep -Fxq 'rc-service mordhau-server stop' "$CALLS"
grep -Fxq 'server_was_running=1' "$CALLS"
if grep -Fxq 'server.sh stop' "$CALLS"; then
    printf '%s\n' 'OpenRC-managed server was stopped outside OpenRC.' >&2
    exit 1
fi

: > "$CALLS"
TEST_SERVER_OPENRC=0
export TEST_SERVER_OPENRC
"$HARNESS"
grep -Fxq 'server.sh status' "$CALLS"
grep -Fxq 'rc-service mordhau-server status' "$CALLS"
grep -Fxq 'server.sh stop' "$CALLS"
grep -Fxq 'server_was_running=1' "$CALLS"
if grep -Fxq 'rc-service mordhau-server stop' "$CALLS"; then
    printf '%s\n' 'Manually launched server was treated as OpenRC-managed.' >&2
    exit 1
fi

printf '%s\n' 'Installer service lifecycle tests passed.'
