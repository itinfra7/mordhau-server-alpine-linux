#!/bin/sh

set -eu

PROJECT_VERSION="1.7.0"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
MORDHAU_ROOT="/root/mordhau"
STEAMCMD_ROOT="/root/steamcmd"
STATE_DIR="$MORDHAU_ROOT/.manager"
RUNTIME_DIR="$STATE_DIR/runtime"
CONFIG_DIR="$MORDHAU_ROOT/Mordhau/Saved/Config/WindowsServer"
INSTALL_LOCK="/run/mordhau-server-alpine-linux.lock"
STEAMCMD_URL="https://steamcdn-a.akamaihd.net/client/installer/steamcmd.zip"
TMP_DIR=""
WEB_PORT=""
START_WEB=0
START_SERVER=0
ENABLE_WEB=0
ENABLE_SERVER=0
SERVER_WAS_RUNNING=0
WEB_WAS_RUNNING=0

export WINEPREFIX="$MORDHAU_ROOT/.wine"
export WINEDEBUG=-all
export XDG_RUNTIME_DIR="$MORDHAU_ROOT/.runtime"

log() {
    printf '%s\n' "$*"
}

die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<'EOF'
Usage: ./mordhau-server-alpine-linux.sh [options]

Installs or updates the Windows MORDHAU Dedicated Server, SteamCMD, the Go
management web application, the server-only Unicode Bridge, and OpenRC service
definitions on Alpine Linux.

Options:
  --web-port PORT   Persist the web service port (default: existing value or 8080)
  --start-web       Start the web manager after installation
  --start-server    Start MORDHAU Dedicated Server after installation
  --enable-web      Add mordhau-web to the default runlevel and start it
  --enable-server   Add mordhau-server to the default runlevel and start it
  -h, --help        Show this help
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --web-port)
            [ "$#" -ge 2 ] || die "--web-port requires a value"
            WEB_PORT=$2
            shift
            ;;
        --start-web)
            START_WEB=1
            ;;
        --start-server)
            START_SERVER=1
            ;;
        --enable-web)
            ENABLE_WEB=1
            START_WEB=1
            ;;
        --enable-server)
            ENABLE_SERVER=1
            START_SERVER=1
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            usage
            die "unknown option: $1"
            ;;
    esac
    shift
done

cleanup() {
    if [ -n "$TMP_DIR" ]; then
        case "$TMP_DIR" in
            /tmp/mordhau-server-alpine-linux.*)
                rm -rf "$TMP_DIR"
                ;;
        esac
    fi
}
trap cleanup EXIT INT TERM

require_environment() {
    [ "$(id -u)" -eq 0 ] || die "run this installer as root"
    [ -f /etc/alpine-release ] || die "Alpine Linux is required"
    command -v apk >/dev/null 2>&1 || die "apk is required"

    architecture=$(uname -m)
    case "$architecture" in
        x86_64|amd64)
            ;;
        *)
            die "unsupported architecture: $architecture (x86_64 is required)"
            ;;
    esac

    for required in \
        "$SCRIPT_DIR/templates/server.sh" \
        "$SCRIPT_DIR/templates/webserver.sh" \
        "$SCRIPT_DIR/templates/openrc/mordhau-server" \
        "$SCRIPT_DIR/templates/openrc/mordhau-web" \
        "$SCRIPT_DIR/steamcmd/mordhau-update.txt" \
        "$SCRIPT_DIR/unicode-bridge/install.sh" \
        "$SCRIPT_DIR/unicode-bridge/dist/SHA256SUMS" \
        "$SCRIPT_DIR/unicode-bridge/dist/MordhauUnicodeBridge/MordhauUnicodeBridge.uplugin" \
        "$SCRIPT_DIR/unicode-bridge/dist/MordhauUnicodeBridge/Content/Paks/MordhauUnicodeBridge-WindowsServer.pak" \
        "$SCRIPT_DIR/web/go.mod"; do
        [ -f "$required" ] || die "release bundle is incomplete: missing $required"
    done
}

validate_port() {
    case "$1" in
        ''|*[!0-9]*)
            return 1
            ;;
    esac
    [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

ensure_packages() {
    log "Installing Alpine packages..."
    apk add --no-cache \
        ca-certificates \
        gnutls \
        go \
        openrc \
        p11-kit \
        p11-kit-trust \
        unzip \
        wget \
        wine
    update-ca-certificates >/dev/null 2>&1 || true

    for command_name in awk flock go rc-service rc-update setsid sha256sum timeout unzip wget wine wineserver; do
        command -v "$command_name" >/dev/null 2>&1 ||
            die "required command is unavailable after package installation: $command_name"
    done
}

ensure_layout() {
    install -d -m 0700 \
        "$MORDHAU_ROOT" \
        "$STEAMCMD_ROOT" \
        "$STATE_DIR" \
        "$STATE_DIR/backups" \
        "$STATE_DIR/pending" \
        "$RUNTIME_DIR" \
        "$MORDHAU_ROOT/log" \
        "$XDG_RUNTIME_DIR"

    if [ -z "$WEB_PORT" ] && [ -r "$STATE_DIR/web-port" ]; then
        WEB_PORT=$(sed -n '1p' "$STATE_DIR/web-port" | tr -d '\r\n')
    fi
    [ -n "$WEB_PORT" ] || WEB_PORT=8080
    validate_port "$WEB_PORT" || die "web port must be between 1 and 65535"
}

process_matches() {
    process_id=$1
    expected=$2
    [ -r "/proc/$process_id/cmdline" ] || return 1
    tr '\000' ' ' < "/proc/$process_id/cmdline" 2>/dev/null | grep -Fq "$expected"
}

find_web_pid() {
    for proc_dir in /proc/[0-9]*; do
        process_id=${proc_dir#/proc/}
        if process_matches "$process_id" "$MORDHAU_ROOT/bin/mordhau-web"; then
            printf '%s\n' "$process_id"
            return 0
        fi
    done
    return 1
}

remember_and_stop_services() {
    if [ -x "$MORDHAU_ROOT/server.sh" ] &&
       "$MORDHAU_ROOT/server.sh" status >/dev/null 2>&1; then
        SERVER_WAS_RUNNING=1
        log "Stopping the running MORDHAU server before validation..."
        "$MORDHAU_ROOT/server.sh" stop
    fi

    if [ -x /etc/init.d/mordhau-web ] &&
       rc-service mordhau-web status >/dev/null 2>&1; then
        WEB_WAS_RUNNING=1
        log "Stopping the OpenRC-managed web server..."
        rc-service mordhau-web stop
    elif web_pid=$(find_web_pid 2>/dev/null); then
        WEB_WAS_RUNNING=1
        log "Stopping the running web manager..."
        kill -TERM "$web_pid" 2>/dev/null || true
        waited=0
        while kill -0 "$web_pid" 2>/dev/null && [ "$waited" -lt 10 ]; do
            sleep 1
            waited=$((waited + 1))
        done
        kill -0 "$web_pid" 2>/dev/null &&
            die "the existing web manager did not stop"
    fi
}

install_runtime_files() {
    install -m 0700 "$SCRIPT_DIR/templates/server.sh" "$MORDHAU_ROOT/server.sh"
    install -m 0700 "$SCRIPT_DIR/templates/webserver.sh" "$MORDHAU_ROOT/webserver.sh"
    install -m 0600 "$SCRIPT_DIR/steamcmd/mordhau-update.txt" \
        "$STEAMCMD_ROOT/mordhau-update.txt"
    install -m 0755 "$SCRIPT_DIR/templates/openrc/mordhau-server" \
        /etc/init.d/mordhau-server
    install -m 0755 "$SCRIPT_DIR/templates/openrc/mordhau-web" \
        /etc/init.d/mordhau-web

    port_temp="$STATE_DIR/.web-port.$$"
    printf '%s\n' "$WEB_PORT" > "$port_temp"
    chmod 0600 "$port_temp"
    mv "$port_temp" "$STATE_DIR/web-port"

    if [ ! -e "$STATE_DIR/server-ports" ]; then
        ports_temp="$STATE_DIR/.server-ports.$$"
        {
            printf '%s\n' \
                'game=7777' \
                'rcon=7778' \
                'beacon=15000' \
                'query=27015'
        } > "$ports_temp"
        chmod 0600 "$ports_temp"
        mv "$ports_temp" "$STATE_DIR/server-ports"
    fi
}

install_steamcmd() {
    archive="$TMP_DIR/steamcmd.zip"
    log "Downloading Windows SteamCMD..."
    wget -qO "$archive" "$STEAMCMD_URL"
    unzip -oq "$archive" -d "$STEAMCMD_ROOT"
    [ -s "$STEAMCMD_ROOT/steamcmd.exe" ] ||
        die "SteamCMD extraction did not create steamcmd.exe"
    chmod 0700 "$STEAMCMD_ROOT/steamcmd.exe"

    log "Initializing the dedicated Wine prefix..."
    wineboot -u >> "$RUNTIME_DIR/wineboot.log" 2>&1 || true
    wineserver -w >> "$RUNTIME_DIR/wineboot.log" 2>&1 || true

    log "Updating SteamCMD..."
    (
        cd "$STEAMCMD_ROOT"
        wine "$STEAMCMD_ROOT/steamcmd.exe" +quit
    ) >> "$RUNTIME_DIR/steamcmd-bootstrap.log" 2>&1 || true
    wineserver -w >> "$RUNTIME_DIR/steamcmd-bootstrap.log" 2>&1 || true
    [ -s "$STEAMCMD_ROOT/steamcmd.exe" ] ||
        die "SteamCMD self-update failed"
}

install_mordhau() {
    log "Installing or validating MORDHAU Dedicated Server..."
    "$MORDHAU_ROOT/server.sh" update
    [ -s "$MORDHAU_ROOT/MordhauServer.exe" ] ||
        die "MORDHAU Dedicated Server executable is missing after SteamCMD"
}

run_initial_generation() {
    game_ini="$CONFIG_DIR/Game.ini"
    engine_ini="$CONFIG_DIR/Engine.ini"
    if [ -f "$game_ini" ] && [ -f "$engine_ini" ]; then
        return
    fi

    log "Running the server without game options for five seconds to generate defaults..."
    (
        cd "$MORDHAU_ROOT"
        timeout -s TERM -k 3 5 wine "$MORDHAU_ROOT/MordhauServer.exe"
    ) > "$RUNTIME_DIR/initial-generation.log" 2>&1 || true
    wineserver -k >/dev/null 2>&1 || true
    sleep 1

    [ -f "$game_ini" ] || die "initial run did not generate Game.ini"
    [ -f "$engine_ini" ] || die "initial run did not generate Engine.ini"
}

install_unicode_bridge() {
    log "Installing the server-only Unicode Bridge..."
    "$SCRIPT_DIR/unicode-bridge/install.sh" --mordhau-root "$MORDHAU_ROOT"
}

build_web_manager() {
    log "Testing and building the Go web manager..."
    (
        cd "$SCRIPT_DIR/web"
        go test ./...
        CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
            -o "$TMP_DIR/mordhau-web" ./cmd/mordhau-web
    )
    [ -s "$TMP_DIR/mordhau-web" ] || die "Go build did not produce the manager binary"

    install -d -m 0700 "$MORDHAU_ROOT/bin" "$MORDHAU_ROOT/web"
    install -m 0700 "$TMP_DIR/mordhau-web" "$MORDHAU_ROOT/bin/mordhau-web"
    cp -R "$SCRIPT_DIR/web/." "$MORDHAU_ROOT/web/"
    chmod -R u=rwX,go= "$MORDHAU_ROOT/web"

    "$MORDHAU_ROOT/bin/mordhau-web" --init >/dev/null
    printf '%s\n' "$PROJECT_VERSION" > "$STATE_DIR/manager-version"
    chmod 0600 "$STATE_DIR/manager-version"
}

configure_services() {
    if [ "$ENABLE_WEB" -eq 1 ]; then
        rc-update add mordhau-web default
    fi
    if [ "$ENABLE_SERVER" -eq 1 ]; then
        rc-update add mordhau-server default
    fi

    if [ "$SERVER_WAS_RUNNING" -eq 1 ] || [ "$START_SERVER" -eq 1 ]; then
        rc-service mordhau-server start
    fi
    if [ "$WEB_WAS_RUNNING" -eq 1 ] || [ "$START_WEB" -eq 1 ]; then
        rc-service mordhau-web start
    fi
}

main() {
    require_environment
    ensure_packages

    exec 9> "$INSTALL_LOCK"
    flock -n 9 || die "another installer instance is running"
    TMP_DIR=$(mktemp -d /tmp/mordhau-server-alpine-linux.XXXXXX)
    chmod 0700 "$TMP_DIR"

    ensure_layout
    remember_and_stop_services
    install_runtime_files
    install_steamcmd
    install_mordhau
    run_initial_generation
    install_unicode_bridge
    build_web_manager
    configure_services

    log "Installed mordhau-server-alpine-linux $PROJECT_VERSION."
    log "Web account file: $MORDHAU_ROOT/default_web_account.txt"
    log "Server control: $MORDHAU_ROOT/server.sh"
    log "Web control: $MORDHAU_ROOT/webserver.sh --port $WEB_PORT"
}

main
