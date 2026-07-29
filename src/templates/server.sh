#!/bin/sh

set -eu

ROOT=/root/mordhau
STEAMCMD=/root/steamcmd/steamcmd.exe
STEAM_SCRIPT=/root/steamcmd/mordhau-update.txt
STATE_DIR="$ROOT/.manager"
RUNTIME_DIR="$STATE_DIR/runtime"
PENDING_DIR="$STATE_DIR/pending"
BACKUP_DIR="$STATE_DIR/backups"
DISABLED_INI_STATE="$STATE_DIR/disabled-ini-entries.json"
PENDING_DISABLED_INI_STATE="$PENDING_DIR/disabled-ini-entries.json"
CONFIG_DIR="$ROOT/Mordhau/Saved/Config/WindowsServer"
GAME_LOG="$ROOT/Mordhau/Saved/Logs/Mordhau.log"
ARCHIVE_DIR="$ROOT/log"
PID_FILE="$RUNTIME_DIR/mordhau.pid"
DESIRED_STATE_FILE="$STATE_DIR/server-desired-state"
LAUNCH_STATE_FILE="$RUNTIME_DIR/server-launch.json"
LOCK_FILE="$STATE_DIR/lifecycle.lock"
LOG_COMPRESSION_LOCK="$STATE_DIR/log-compression.lock"
LOG_COMPRESSION_LOG="$RUNTIME_DIR/log-compression.log"
LANGUAGE_FILE="$STATE_DIR/language"
START_MAP_FILE="$STATE_DIR/start-map"
SERVER_PORTS_FILE="$STATE_DIR/server-ports"
CONSOLE_LOG="$RUNTIME_DIR/server-console.log"
STEAM_RUN_LOG="$RUNTIME_DIR/steamcmd-update.log"
STEAM_CONSOLE_LOG=/root/steamcmd/logs/console_log.txt
EXE="$ROOT/MordhauServer.exe"
SHIPPING_EXE="$ROOT/Mordhau/Binaries/Win64/MordhauServer-Win64-Shipping.exe"
RUNTIME_BRIDGE_DLL="$ROOT/Mordhau/Binaries/Win64/dxgi.dll"
RUNTIME_BRIDGE_EXE_SHA256="a11348d6bfdb386d7f8a976a59e7d28d38b0d1ba2b9a2a7e0035ac28d53f885e"
WEB_MANAGER="$ROOT/bin/mordhau-web"

export WINEPREFIX="$ROOT/.wine"
export WINEDEBUG=-all
export XDG_RUNTIME_DIR="$ROOT/.runtime"

usage() {
    cat <<'EOF'
Usage: /root/mordhau/server.sh <command>

Commands:
  start      Update the server, apply staged INI/CustomPaks changes, and start it
  stop       Stop MORDHAU Dedicated Server
  restart    Stop, update, apply staged INI/CustomPaks changes, and start it
  update     Update only; the server must be stopped
  recover    Restart the last validated installation after an unexpected exit
  compress-logs
             Losslessly compress finalized game-log archives
  status     Show whether the server is running
  help       Show this help
EOF
}

ensure_layout() {
    mkdir -p \
        "$RUNTIME_DIR" \
        "$PENDING_DIR" \
        "$BACKUP_DIR" \
        "$STATE_DIR/custompaks-inactive" \
        "$STATE_DIR/custompaks-upload" \
        "$ARCHIVE_DIR" \
        "$XDG_RUNTIME_DIR"
    chmod 700 \
        "$STATE_DIR" \
        "$RUNTIME_DIR" \
        "$PENDING_DIR" \
        "$BACKUP_DIR" \
        "$STATE_DIR/custompaks-inactive" \
        "$STATE_DIR/custompaks-upload" \
        "$ARCHIVE_DIR" \
        "$XDG_RUNTIME_DIR"
    if [ ! -e "$LOG_COMPRESSION_LOG" ]; then
        : > "$LOG_COMPRESSION_LOG"
    fi
    if [ ! -e "$LOG_COMPRESSION_LOCK" ]; then
        : > "$LOG_COMPRESSION_LOCK"
    fi
    chmod 600 "$LOG_COMPRESSION_LOG" "$LOG_COMPRESSION_LOCK"
}

read_pid() {
    [ -f "$PID_FILE" ] || return 1
    pid=$(sed -n '1p' "$PID_FILE" 2>/dev/null || true)
    case "$pid" in
        ''|*[!0-9]*) return 1 ;;
    esac
    printf '%s\n' "$pid"
}

pid_is_mordhau() {
    check_pid=$1
    [ -r "/proc/$check_pid/cmdline" ] || return 1
    tr '\000' ' ' < "/proc/$check_pid/cmdline" 2>/dev/null |
        grep -Eq 'MordhauServer(\.exe|-Win64-Shipping\.exe)'
}

find_mordhau_pid() {
    if saved_pid=$(read_pid 2>/dev/null) &&
       kill -0 "$saved_pid" 2>/dev/null &&
       pid_is_mordhau "$saved_pid"; then
        printf '%s\n' "$saved_pid"
        return 0
    fi

    for proc_dir in /proc/[0-9]*; do
        candidate=${proc_dir#/proc/}
        if pid_is_mordhau "$candidate"; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

is_running() {
    find_mordhau_pid >/dev/null 2>&1
}

remove_pid_file() {
    if [ -f "$PID_FILE" ]; then
        unlink "$PID_FILE"
    fi
}

write_desired_state() {
    desired_state=$1
    case "$desired_state" in
        running|stopped) ;;
        *) return 1 ;;
    esac
    desired_temp="$STATE_DIR/.server-desired-state.$$"
    printf '%s\n' "$desired_state" > "$desired_temp"
    chmod 600 "$desired_temp"
    mv "$desired_temp" "$DESIRED_STATE_FILE"
}

desired_state() {
    [ -r "$DESIRED_STATE_FILE" ] || return 1
    stored_state=$(sed -n '1p' "$DESIRED_STATE_FILE" | tr -d '\r\n')
    case "$stored_state" in
        running|stopped)
            printf '%s\n' "$stored_state"
            ;;
        *)
            return 1
            ;;
    esac
}

write_launch_state() {
    launch_pid=$1
    case "$launch_pid" in
        ''|*[!0-9]*) return 1 ;;
    esac
    launch_temp="$RUNTIME_DIR/.server-launch.json.$$"
    printf '{"version":1,"pid":%s,"started_at_unix":%s}\n' \
        "$launch_pid" "$(date '+%s')" > "$launch_temp"
    chmod 600 "$launch_temp"
    mv "$launch_temp" "$LAUNCH_STATE_FILE"
}

validate_language() {
    case "$1" in
        en|de|es|zh-Hans|fr|it|pt|ru|ko|zh-Hant) return 0 ;;
        *) return 1 ;;
    esac
}

selected_language() {
    language=en
    if [ -f "$LANGUAGE_FILE" ]; then
        language=$(sed -n '1p' "$LANGUAGE_FILE" | tr -d '\r\n')
    fi
    if ! validate_language "$language"; then
        printf '%s\n' "Invalid language '$language' in $LANGUAGE_FILE; using en." >&2
        language=en
    fi
    printf '%s\n' "$language"
}

selected_start_map() {
    start_map=
    if [ -f "$START_MAP_FILE" ]; then
        start_map=$(sed -n '1p' "$START_MAP_FILE" | tr -d '\r\n')
    fi
    case "$start_map" in
        '') ;;
        -*|*[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./?=:+-]*)
            printf '%s\n' "Invalid start map in $START_MAP_FILE." >&2
            return 1
            ;;
    esac
    if [ "${#start_map}" -gt 160 ]; then
        printf '%s\n' "Invalid start map in $START_MAP_FILE." >&2
        return 1
    fi
    printf '%s\n' "$start_map"
}

load_server_ports() {
    game_port=7777
    rcon_port=7778
    beacon_port=15000
    query_port=27015
    [ -f "$SERVER_PORTS_FILE" ] || return 0

    game_seen=0
    rcon_seen=0
    beacon_seen=0
    query_seen=0
    while IFS= read -r port_line || [ -n "$port_line" ]; do
        [ -n "$port_line" ] || continue
        case "$port_line" in
            *=*)
                port_key=${port_line%%=*}
                port_value=${port_line#*=}
                ;;
            *)
                printf 'Invalid server port setting in %s.\n' "$SERVER_PORTS_FILE" >&2
                return 1
                ;;
        esac
        case "$port_value" in
            ''|0|0*|*[!0-9]*)
                printf 'Invalid %s port in %s.\n' "$port_key" "$SERVER_PORTS_FILE" >&2
                return 1
                ;;
        esac
        if [ "${#port_value}" -gt 5 ] || [ "$port_value" -gt 65535 ]; then
            printf 'Invalid %s port in %s.\n' "$port_key" "$SERVER_PORTS_FILE" >&2
            return 1
        fi
        case "$port_key" in
            game)
                [ "$game_seen" -eq 0 ] || return 1
                game_port=$port_value
                game_seen=1
                ;;
            rcon)
                [ "$rcon_seen" -eq 0 ] || return 1
                rcon_port=$port_value
                rcon_seen=1
                ;;
            beacon)
                [ "$beacon_seen" -eq 0 ] || return 1
                beacon_port=$port_value
                beacon_seen=1
                ;;
            query)
                [ "$query_seen" -eq 0 ] || return 1
                query_port=$port_value
                query_seen=1
                ;;
            *)
                printf 'Unknown server port setting %s in %s.\n' "$port_key" "$SERVER_PORTS_FILE" >&2
                return 1
                ;;
        esac
    done < "$SERVER_PORTS_FILE"

    if [ "$game_seen" -ne 1 ] || [ "$rcon_seen" -ne 1 ] ||
       [ "$beacon_seen" -ne 1 ] || [ "$query_seen" -ne 1 ]; then
        printf 'Incomplete server port settings in %s.\n' "$SERVER_PORTS_FILE" >&2
        return 1
    fi
    if [ "$game_port" = "$rcon_port" ] || [ "$game_port" = "$beacon_port" ] ||
       [ "$game_port" = "$query_port" ] || [ "$rcon_port" = "$beacon_port" ] ||
       [ "$rcon_port" = "$query_port" ] || [ "$beacon_port" = "$query_port" ]; then
        printf 'Server ports in %s must be unique.\n' "$SERVER_PORTS_FILE" >&2
        return 1
    fi
}

update_server() {
    if is_running; then
        printf '%s\n' 'Cannot update while MORDHAU Dedicated Server is running.' >&2
        return 1
    fi
    if [ ! -f "$STEAMCMD" ] || [ ! -f "$STEAM_SCRIPT" ]; then
        printf '%s\n' 'SteamCMD is not installed correctly.' >&2
        return 1
    fi

    console_size=0
    if [ -f "$STEAM_CONSOLE_LOG" ]; then
        console_size=$(wc -c < "$STEAM_CONSOLE_LOG" | tr -d ' ')
    fi

    : > "$STEAM_RUN_LOG"
    printf '%s\n' 'Updating MORDHAU Dedicated Server through SteamCMD...'
    (
        cd /root/steamcmd
        wine "$STEAMCMD" +runscript 'Z:\root\steamcmd\mordhau-update.txt'
    ) >> "$STEAM_RUN_LOG" 2>&1 || true
    wineserver -w >> "$STEAM_RUN_LOG" 2>&1 || true

    new_console="$RUNTIME_DIR/steamcmd-console-current.log"
    if [ -f "$STEAM_CONSOLE_LOG" ]; then
        tail -c "+$((console_size + 1))" "$STEAM_CONSOLE_LOG" > "$new_console" 2>/dev/null ||
            cp "$STEAM_CONSOLE_LOG" "$new_console"
    else
        : > "$new_console"
    fi

    if grep -Fq "Success! App '629800' fully installed." "$new_console"; then
        printf '%s\n' 'MORDHAU Dedicated Server is up to date.'
        return 0
    fi

    printf '%s\n' "SteamCMD update failed. See $STEAM_RUN_LOG and $new_console." >&2
    tail -n 40 "$new_console" >&2 || true
    return 1
}

apply_pending_config() {
    applied=0
    stamp=$(date '+%Y-%m-%d_%H-%M-%S')
    for name in Game.ini Engine.ini; do
        pending="$PENDING_DIR/$name"
        target="$CONFIG_DIR/$name"
        if [ -f "$pending" ]; then
            if [ -f "$target" ]; then
                cp -p "$target" "$BACKUP_DIR/${name}.${stamp}.bak"
            fi
            chmod 600 "$pending"
            mv "$pending" "$target"
            applied=1
        fi
    done
    if [ -f "$PENDING_DISABLED_INI_STATE" ]; then
        if [ -f "$DISABLED_INI_STATE" ]; then
            cp -p "$DISABLED_INI_STATE" \
                "$BACKUP_DIR/disabled-ini-entries.json.${stamp}.bak"
        fi
        chmod 600 "$PENDING_DISABLED_INI_STATE"
        mv "$PENDING_DISABLED_INI_STATE" "$DISABLED_INI_STATE"
        applied=1
    fi
    if [ "$applied" -eq 1 ]; then
        printf '%s\n' 'Applied staged INI configuration and disabled-item state.'
    fi
}

apply_pending_custompaks() {
    [ -x "$WEB_MANAGER" ] || {
        printf '%s\n' "Missing manager binary: $WEB_MANAGER" >&2
        return 1
    }
    "$WEB_MANAGER" --apply-custompaks
}

compress_archived_log() (
    source_log=$1
    case "$source_log" in
        "$ARCHIVE_DIR"/*)
            ;;
        *)
            printf 'Refusing to compress an unexpected path: %s\n' \
                "$source_log" >&2
            return 1
            ;;
    esac
    source_name=${source_log#"$ARCHIVE_DIR"/}
    case "$source_name" in
        Mordhau_*.log)
            ;;
        *)
            printf 'Refusing to compress an unexpected archive name: %s\n' \
                "$source_name" >&2
            return 1
            ;;
    esac
    case "$source_name" in
        */*)
            printf 'Refusing to compress a nested archive path: %s\n' \
                "$source_log" >&2
            return 1
            ;;
    esac
    [ -f "$source_log" ] || return 0
    if [ -L "$source_log" ]; then
        printf 'Refusing to compress a symbolic link: %s\n' "$source_log" >&2
        return 1
    fi

    compressed_log="${source_log}.xz"
    temporary_log="${compressed_log}.tmp.$$"
    if [ -e "$compressed_log" ]; then
        if [ -f "$compressed_log" ] && [ ! -L "$compressed_log" ] &&
           xz -t -- "$compressed_log"; then
            source_checksum=$(sha256sum "$source_log" | awk '{print $1}')
            compressed_checksum=$(
                xz -dc -- "$compressed_log" |
                    sha256sum |
                    awk '{print $1}'
            )
            if [ "$source_checksum" = "$compressed_checksum" ]; then
                unlink "$source_log"
                printf 'Removed verified duplicate source %s; retained %s.\n' \
                    "$source_log" "$compressed_log"
                return 0
            fi
        fi
        printf 'Refusing to overwrite existing compressed log: %s\n' \
            "$compressed_log" >&2
        return 1
    fi
    cleanup_compression_temp() {
        if [ -f "$temporary_log" ]; then
            unlink "$temporary_log"
        fi
    }
    trap cleanup_compression_temp EXIT HUP INT TERM

    original_checksum=$(sha256sum "$source_log" | awk '{print $1}')
    if command -v ionice >/dev/null 2>&1; then
        nice -n 19 ionice -c 3 \
            xz -9e -T1 -c -- "$source_log" > "$temporary_log"
    else
        nice -n 19 xz -9e -T1 -c -- "$source_log" > "$temporary_log"
    fi
    xz -t -- "$temporary_log"
    restored_checksum=$(
        xz -dc -- "$temporary_log" |
            sha256sum |
            awk '{print $1}'
    )
    if [ "$restored_checksum" != "$original_checksum" ]; then
        printf 'Compressed log verification failed: %s\n' "$source_log" >&2
        return 1
    fi
    current_checksum=$(sha256sum "$source_log" | awk '{print $1}')
    if [ "$current_checksum" != "$original_checksum" ]; then
        printf 'Source log changed during compression: %s\n' "$source_log" >&2
        return 1
    fi

    touch -r "$source_log" "$temporary_log"
    chmod 600 "$temporary_log"
    mv "$temporary_log" "$compressed_log"
    unlink "$source_log"
    trap - EXIT HUP INT TERM
    printf 'Compressed %s as %s (SHA-256 %s).\n' \
        "$source_log" "$compressed_log" "$original_checksum"
)

compress_archived_logs() {
    compressed_count=0
    failed_count=0
    for source_log in "$ARCHIVE_DIR"/Mordhau_*.log; do
        [ -f "$source_log" ] || continue
        if compress_archived_log "$source_log"; then
            compressed_count=$((compressed_count + 1))
        else
            failed_count=$((failed_count + 1))
        fi
    done
    printf 'Game-log compression finished: %s compressed, %s failed.\n' \
        "$compressed_count" "$failed_count"
    [ "$failed_count" -eq 0 ]
}

run_log_compression() {
    exec 8> "$LOG_COMPRESSION_LOCK"
    chmod 600 "$LOG_COMPRESSION_LOCK"
    if ! flock -n 8; then
        printf '%s\n' 'Another game-log compression task is already running.'
        return 0
    fi
    compress_archived_logs
}

queue_log_compression() {
    (
        # The background compressor must not retain the lifecycle lock.
        exec 9>&-
        run_log_compression
    ) >> "$LOG_COMPRESSION_LOG" 2>&1 </dev/null &
}

archive_log() {
    [ -f "$GAME_LOG" ] || return 0
    stamp=$(date -r "$GAME_LOG" '+%Y-%m-%d_%H-%M-%S')
    destination="$ARCHIVE_DIR/Mordhau_${stamp}.log"
    if [ -e "$destination" ] || [ -e "${destination}.xz" ]; then
        printf 'Refusing to overwrite existing archived log: %s\n' "$destination" >&2
        return 1
    fi
    mv "$GAME_LOG" "$destination"
    chmod 600 "$destination"
    printf 'Archived previous log as %s.\n' "$destination"
    queue_log_compression
}

configure_runtime_bridge() {
    [ -s "$RUNTIME_BRIDGE_DLL" ] || return 0
    [ -s "$SHIPPING_EXE" ] || return 0

    runtime_exe_sha256=$(sha256sum "$SHIPPING_EXE" | awk '{print $1}')
    if [ "$runtime_exe_sha256" != "$RUNTIME_BRIDGE_EXE_SHA256" ]; then
        printf '%s\n' \
            'Runtime bridge disabled: the MORDHAU executable build is unsupported.' >&2
        return 0
    fi
    export WINEDLLOVERRIDES="dxgi=n,b${WINEDLLOVERRIDES:+;$WINEDLLOVERRIDES}"
}

launch_server() {
    if is_running; then
        printf '%s\n' 'MORDHAU Dedicated Server is already running.' >&2
        return 1
    fi
    [ -x "$EXE" ] || {
        printf '%s\n' "Missing executable: $EXE" >&2
        return 1
    }

    language=$(selected_language)
    start_map=$(selected_start_map)
    load_server_ports
    configure_runtime_bridge
    archive_log
    if [ -n "$start_map" ]; then
        printf 'Starting MORDHAU Dedicated Server on %s with language %s (game %s, RCON %s, beacon %s, query %s)...\n' \
            "$start_map" "$language" "$game_port" "$rcon_port" "$beacon_port" "$query_port"
    else
        printf 'Starting MORDHAU Dedicated Server with language %s (game %s, RCON %s, beacon %s, query %s)...\n' \
            "$language" "$game_port" "$rcon_port" "$beacon_port" "$query_port"
    fi
    (
        # The long-running Wine process must not inherit the lifecycle lock.
        exec 9>&-
        cd "$ROOT"
        if [ -n "$start_map" ]; then
            exec setsid wine "$EXE" "$start_map" \
                "-Port=$game_port" "-RconPort=$rcon_port" \
                "-BeaconPort=$beacon_port" "-QueryPort=$query_port" \
                "-language=$language" -LocalLogTimes -log
        fi
        exec setsid wine "$EXE" \
            "-Port=$game_port" "-RconPort=$rcon_port" \
            "-BeaconPort=$beacon_port" "-QueryPort=$query_port" \
            "-language=$language" -LocalLogTimes -log
    ) >> "$CONSOLE_LOG" 2>&1 </dev/null &
    launched_pid=$!
    printf '%s\n' "$launched_pid" > "$PID_FILE"
    chmod 600 "$PID_FILE"

    waited=0
    while [ "$waited" -lt 15 ]; do
        if is_running; then
            actual_pid=$(find_mordhau_pid)
            if [ "$actual_pid" != "$launched_pid" ]; then
                printf '%s\n' "$actual_pid" > "$PID_FILE"
            fi
            write_launch_state "$actual_pid"
            printf 'MORDHAU Dedicated Server is running (PID %s).\n' "$actual_pid"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done

    remove_pid_file
    printf '%s\n' "MORDHAU Dedicated Server failed to start. See $CONSOLE_LOG." >&2
    tail -n 60 "$CONSOLE_LOG" >&2 || true
    return 1
}

stop_server() {
    if ! running_pid=$(find_mordhau_pid 2>/dev/null); then
        remove_pid_file
        printf '%s\n' 'MORDHAU Dedicated Server is already stopped.'
        return 0
    fi

    printf 'Stopping MORDHAU Dedicated Server (PID %s)...\n' "$running_pid"
    kill -TERM "$running_pid" 2>/dev/null || true
    waited=0
    while [ "$waited" -lt 20 ]; do
        if ! is_running; then
            remove_pid_file
            printf '%s\n' 'MORDHAU Dedicated Server stopped.'
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done

    printf '%s\n' 'Graceful shutdown timed out; stopping the dedicated Wine prefix.'
    wineserver -k >/dev/null 2>&1 || true
    waited=0
    while [ "$waited" -lt 5 ]; do
        if ! is_running; then
            remove_pid_file
            printf '%s\n' 'MORDHAU Dedicated Server stopped.'
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done

    if stubborn_pid=$(find_mordhau_pid 2>/dev/null); then
        kill -KILL "$stubborn_pid" 2>/dev/null || true
    fi
    remove_pid_file
    if is_running; then
        printf '%s\n' 'Failed to stop MORDHAU Dedicated Server.' >&2
        return 1
    fi
    printf '%s\n' 'MORDHAU Dedicated Server stopped.'
}

ensure_layout

command=${1:-}
case "$command" in
    help|-h|--help)
        usage
        exit 0
        ;;
    status)
        if is_running; then
            printf 'running (PID %s)\n' "$(find_mordhau_pid)"
            exit 0
        fi
        printf '%s\n' 'stopped'
        exit 3
        ;;
    compress-logs)
        run_log_compression
        exit $?
        ;;
    start|stop|restart|update|recover)
        ;;
    *)
        usage
        exit 2
        ;;
esac

exec 9> "$LOCK_FILE"
if ! flock -n 9; then
    printf '%s\n' 'Another MORDHAU lifecycle operation is already running.' >&2
    exit 1
fi

case "$command" in
    start)
        if is_running; then
            printf '%s\n' 'MORDHAU Dedicated Server is already running.' >&2
            exit 1
        fi
        update_server
        apply_pending_config
        apply_pending_custompaks
        write_desired_state running
        if ! launch_server; then
            write_desired_state stopped
            exit 1
        fi
        ;;
    stop)
        write_desired_state stopped
        stop_server
        ;;
    restart)
        write_desired_state running
        stop_server
        update_server
        apply_pending_config
        apply_pending_custompaks
        launch_server
        ;;
    update)
        write_desired_state stopped
        update_server
        ;;
    recover)
        [ "$(desired_state 2>/dev/null || true)" = "running" ] || {
            printf '%s\n' \
                'Automatic recovery is not armed because the desired state is stopped.' >&2
            exit 1
        }
        launch_server
        ;;
esac
