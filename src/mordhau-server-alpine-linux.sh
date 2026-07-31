#!/bin/sh

set -eu

PROJECT_VERSION="2.6.4"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
MORDHAU_ROOT="/root/mordhau"
STEAMCMD_ROOT="/root/steamcmd"
STATE_DIR="$MORDHAU_ROOT/.manager"
RUNTIME_DIR="$STATE_DIR/runtime"
CONFIG_DIR="$MORDHAU_ROOT/Mordhau/Saved/Config/WindowsServer"
INSTALL_LOCK="/run/mordhau-server-alpine-linux.lock"
STEAMCMD_URL="https://steamcdn-a.akamaihd.net/client/installer/steamcmd.zip"
REPAK_URL="https://github.com/trumank/repak/releases/download/v0.2.3/repak_cli-x86_64-pc-windows-msvc.zip"
REPAK_SHA256="6720d602144d75df477a99d5bedb6ea780997546afc335901d4937cafeaa73fa"
TMP_DIR=""
WEB_BINARY_TEMP=""
WEB_PORT=""
START_WEB=0
START_SERVER=0
ENABLE_WEB=0
ENABLE_SERVER=0
SERVER_WAS_RUNNING=0
WEB_WAS_RUNNING=0
ALLOW_DOWNGRADE=0
INSTALLED_VERSION=""

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
Usage: ./src/mordhau-server-alpine-linux.sh [options]

Installs or updates the Windows MORDHAU Dedicated Server, SteamCMD, the Go
management web application, the server-only Unicode Bridge, and OpenRC service
definitions on Alpine Linux. The supported dedicated-server build also receives
the native runtime-reflection bridge used by the authenticated Runtime panel
and a checksum-pinned PAK inspection helper for live map selection.

Options:
  --web-port PORT   Persist the web service port (default: existing value or 8080)
  --start-web       Start the web manager after installation
  --start-server    Start MORDHAU Dedicated Server after installation
  --enable-web      Add mordhau-web to the default runlevel and start it
  --enable-server   Add mordhau-server to the default runlevel and start it
  --allow-downgrade Permit an explicit management-code downgrade
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
        --allow-downgrade)
            ALLOW_DOWNGRADE=1
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
    if [ -n "$WEB_BINARY_TEMP" ]; then
        case "$WEB_BINARY_TEMP" in
            "$MORDHAU_ROOT"/bin/.mordhau-web.*)
                if [ -f "$WEB_BINARY_TEMP" ]; then
                    find "$WEB_BINARY_TEMP" -xdev -depth -delete
                fi
                ;;
        esac
    fi
    if [ -n "$TMP_DIR" ]; then
        case "$TMP_DIR" in
            /tmp/mordhau-server-alpine-linux.*)
                if [ -e "$TMP_DIR" ]; then
                    find "$TMP_DIR" -xdev -depth -delete
                fi
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
        "$SCRIPT_DIR/runtime-bridge/build.sh" \
        "$SCRIPT_DIR/runtime-bridge/dxgi.def" \
        "$SCRIPT_DIR/runtime-bridge/runtime_bridge.c" \
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

version_valid() {
    value=$1
    case "$value" in
        ''|*[!0-9.]*|.*|*.|*..*)
            return 1
            ;;
    esac
    major=${value%%.*}
    remainder=${value#*.}
    minor=${remainder%%.*}
    patch=${remainder#*.}
    [ "$patch" != "$remainder" ] || return 1
    case "$patch" in
        *.*)
            return 1
            ;;
    esac
    for component in "$major" "$minor" "$patch"; do
        [ -n "$component" ] &&
            [ "${#component}" -le 9 ] ||
            return 1
        case "$component" in
            0|[1-9]*)
                ;;
            *)
                return 1
                ;;
        esac
    done
}

compare_versions() {
    left=$1
    right=$2
    version_valid "$left" && version_valid "$right" || return 1

    left_major=${left%%.*}
    left_remainder=${left#*.}
    left_minor=${left_remainder%%.*}
    left_patch=${left_remainder#*.}
    right_major=${right%%.*}
    right_remainder=${right#*.}
    right_minor=${right_remainder%%.*}
    right_patch=${right_remainder#*.}

    for pair in \
        "$left_major:$right_major" \
        "$left_minor:$right_minor" \
        "$left_patch:$right_patch"; do
        left_component=${pair%%:*}
        right_component=${pair#*:}
        if [ "$left_component" -lt "$right_component" ]; then
            printf '%s\n' -1
            return 0
        fi
        if [ "$left_component" -gt "$right_component" ]; then
            printf '%s\n' 1
            return 0
        fi
    done
    printf '%s\n' 0
}

prepare_version_transition() {
    version_path="$STATE_DIR/manager-version"
    version_valid "$PROJECT_VERSION" ||
        die "installer version is invalid: $PROJECT_VERSION"
    if [ ! -e "$version_path" ]; then
        log "Installing mordhau-server-alpine-linux $PROJECT_VERSION."
        return
    fi
    [ -f "$version_path" ] && [ -r "$version_path" ] ||
        die "installed version state is not a readable regular file"

    INSTALLED_VERSION=$(sed -n '1p' "$version_path" | tr -d '\r')
    version_contents=$(cat "$version_path")
    [ "$version_contents" = "$INSTALLED_VERSION" ] &&
        version_valid "$INSTALLED_VERSION" ||
        die "installed manager version state is invalid"

    comparison=$(compare_versions "$INSTALLED_VERSION" "$PROJECT_VERSION") ||
        die "unable to compare management-code versions"
    case "$comparison" in
        -1)
            log "Upgrading mordhau-server-alpine-linux $INSTALLED_VERSION -> $PROJECT_VERSION."
            ;;
        0)
            log "Reinstalling mordhau-server-alpine-linux $PROJECT_VERSION for validation."
            ;;
        1)
            [ "$ALLOW_DOWNGRADE" -eq 1 ] ||
                die "refusing downgrade $INSTALLED_VERSION -> $PROJECT_VERSION without --allow-downgrade"
            log "Downgrading mordhau-server-alpine-linux $INSTALLED_VERSION -> $PROJECT_VERSION."
            ;;
        *)
            die "unexpected version comparison result"
            ;;
    esac
}

record_installed_version() {
    version_temp=$(mktemp "$STATE_DIR/.manager-version.XXXXXX")
    printf '%s\n' "$PROJECT_VERSION" > "$version_temp"
    chmod 0600 "$version_temp"
    mv "$version_temp" "$STATE_DIR/manager-version"
}

ensure_packages() {
    log "Installing Alpine packages..."
    apk add --no-cache \
        ca-certificates \
        gnutls \
        go \
        mingw-w64-gcc \
        openrc \
        p11-kit \
        p11-kit-trust \
        unzip \
        wget \
        wine \
        xz
    update-ca-certificates >/dev/null 2>&1 || true

    for command_name in awk flock go rc-service rc-update setsid sha256sum timeout unzip wget wine wineserver x86_64-w64-mingw32-gcc xz; do
        command -v "$command_name" >/dev/null 2>&1 ||
            die "required command is unavailable after package installation: $command_name"
    done
}

ensure_layout() {
    install -d -m 0700 \
        "$MORDHAU_ROOT" \
        "$MORDHAU_ROOT/bin" \
        "$STEAMCMD_ROOT" \
        "$STATE_DIR" \
        "$STATE_DIR/backups" \
        "$STATE_DIR/custompaks-inactive" \
        "$STATE_DIR/custompaks-upload" \
        "$STATE_DIR/pending" \
        "$RUNTIME_DIR" \
        "$MORDHAU_ROOT/licenses/repak" \
        "$MORDHAU_ROOT/log" \
        "$XDG_RUNTIME_DIR"

    if [ -z "$WEB_PORT" ] && [ -r "$STATE_DIR/web-port" ]; then
        WEB_PORT=$(sed -n '1p' "$STATE_DIR/web-port" | tr -d '\r\n')
    fi
    [ -n "$WEB_PORT" ] || WEB_PORT=8080
    validate_port "$WEB_PORT" || die "web port must be between 1 and 65535"
}

process_is_executable() {
    process_id=$1
    expected=$2
    actual=$(readlink "/proc/$process_id/exe" 2>/dev/null) || return 1
    [ "$actual" = "$expected" ]
}

find_web_pid() {
    for proc_dir in /proc/[0-9]*; do
        process_id=${proc_dir#/proc/}
        if [ -n "${MORDHAU_MANAGER_UPDATE_WORKER_PID:-}" ] &&
           [ "$process_id" = "$MORDHAU_MANAGER_UPDATE_WORKER_PID" ]; then
            continue
        fi
        if process_is_executable "$process_id" "$MORDHAU_ROOT/bin/mordhau-web"; then
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
        if [ -x /etc/init.d/mordhau-server ] &&
           rc-service mordhau-server status >/dev/null 2>&1; then
            log "Stopping the OpenRC-managed MORDHAU server before validation..."
            rc-service mordhau-server stop
        else
            log "Stopping the running MORDHAU server before validation..."
            "$MORDHAU_ROOT/server.sh" stop
        fi
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

    if [ ! -e "$STATE_DIR/trusted-proxies" ]; then
        trusted_proxy_temp="$STATE_DIR/.trusted-proxies.$$"
        : > "$trusted_proxy_temp"
        chmod 0600 "$trusted_proxy_temp"
        mv "$trusted_proxy_temp" "$STATE_DIR/trusted-proxies"
    fi

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

install_repak() {
    archive="$TMP_DIR/repak.zip"
    extract_dir="$TMP_DIR/repak"
    log "Installing the repak PAK-index helper..."
    wget -qO "$archive" "$REPAK_URL"
    actual_sha256=$(sha256sum "$archive" | awk '{print $1}')
    [ "$actual_sha256" = "$REPAK_SHA256" ] ||
        die "repak archive checksum verification failed"
    install -d -m 0700 "$extract_dir"
    unzip -q "$archive" -d "$extract_dir"
    [ -s "$extract_dir/repak.exe" ] ||
        die "repak extraction did not create repak.exe"
    install -m 0700 "$extract_dir/repak.exe" "$MORDHAU_ROOT/bin/repak.exe"
    install -m 0644 \
        "$extract_dir/LICENSE-APACHE" \
        "$extract_dir/LICENSE-MIT" \
        "$MORDHAU_ROOT/licenses/repak/"
}

steamcmd_installation_complete() {
    for required in \
        steamcmd.exe \
        steam.dll \
        steamclient.dll \
        steamconsole.dll \
        tier0_s.dll \
        vstdlib_s.dll \
        package/steam_cmd_win32.installed
    do
        [ -s "$STEAMCMD_ROOT/$required" ] || return 1
    done
}

install_steamcmd() {
    if steamcmd_installation_complete; then
        log "Using the existing Windows SteamCMD installation..."
    else
        archive="$TMP_DIR/steamcmd.zip"
        log "Downloading Windows SteamCMD..."
        wget -qO "$archive" "$STEAMCMD_URL"
        unzip -oq "$archive" -d "$STEAMCMD_ROOT"
        [ -s "$STEAMCMD_ROOT/steamcmd.exe" ] ||
            die "SteamCMD extraction did not create steamcmd.exe"
    fi
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

install_runtime_bridge() {
    shipping_exe="$MORDHAU_ROOT/Mordhau/Binaries/Win64/MordhauServer-Win64-Shipping.exe"
    bridge_destination="$MORDHAU_ROOT/Mordhau/Binaries/Win64/dxgi.dll"
    supported_sha256="a11348d6bfdb386d7f8a976a59e7d28d38b0d1ba2b9a2a7e0035ac28d53f885e"

    [ -s "$shipping_exe" ] ||
        die "MORDHAU shipping executable is missing: $shipping_exe"
    actual_sha256=$(sha256sum "$shipping_exe" | awk '{print $1}')
    if [ "$actual_sha256" != "$supported_sha256" ]; then
        log "Runtime bridge skipped: the installed MORDHAU executable is not a supported build."
        return 0
    fi

    log "Building and installing the native runtime-reflection bridge..."
    "$SCRIPT_DIR/runtime-bridge/build.sh" "$TMP_DIR/dxgi.dll"
    [ -s "$TMP_DIR/dxgi.dll" ] ||
        die "runtime bridge build did not produce dxgi.dll"
    install -m 0644 "$TMP_DIR/dxgi.dll" "$bridge_destination"
    printf '%s\n' "$supported_sha256" > "$STATE_DIR/runtime-bridge-exe-sha256"
    chmod 0600 "$STATE_DIR/runtime-bridge-exe-sha256"
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

install_web_binary() {
    source_binary=$1
    web_binary_destination="$MORDHAU_ROOT/bin/mordhau-web"
    [ -s "$source_binary" ] || die "web manager build output is missing"
    WEB_BINARY_TEMP=$(mktemp "$MORDHAU_ROOT/bin/.mordhau-web.XXXXXX")
    install -m 0700 "$source_binary" "$WEB_BINARY_TEMP"
    mv "$WEB_BINARY_TEMP" "$web_binary_destination"
    WEB_BINARY_TEMP=""
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
    install_web_binary "$TMP_DIR/mordhau-web"
    cp -R "$SCRIPT_DIR/web/." "$MORDHAU_ROOT/web/"
    chmod -R u=rwX,go= "$MORDHAU_ROOT/web"

    "$MORDHAU_ROOT/bin/mordhau-web" --init >/dev/null
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
        rc-service mordhau-server status >/dev/null 2>&1 ||
            die "MORDHAU OpenRC service did not remain started"
    fi
    if [ "$WEB_WAS_RUNNING" -eq 1 ] || [ "$START_WEB" -eq 1 ]; then
        rc-service mordhau-web start
        rc-service mordhau-web status >/dev/null 2>&1 ||
            die "web OpenRC service did not remain started"
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
    prepare_version_transition
    remember_and_stop_services
    install_runtime_files
    install_steamcmd
    install_repak
    install_mordhau
    install_runtime_bridge
    run_initial_generation
    install_unicode_bridge
    build_web_manager
    configure_services
    record_installed_version

    log "Installed mordhau-server-alpine-linux $PROJECT_VERSION."
    log "Web account file: $MORDHAU_ROOT/default_web_account.txt"
    log "Server control: $MORDHAU_ROOT/server.sh"
    log "Web control: $MORDHAU_ROOT/webserver.sh --port $WEB_PORT"
}

main
