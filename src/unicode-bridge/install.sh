#!/bin/sh

set -eu

BRIDGE_VERSION="1.0.3"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DIST_DIR="$SCRIPT_DIR/dist"
BRIDGE_BUNDLE="$DIST_DIR/MordhauUnicodeBridge"
BRIDGE_SECTION="[/Script/Mordhau.MordhauGameMode]"
BRIDGE_ACTOR="SpawnServerActorsOnMapLoad=/MordhauUnicodeBridge/BP_MordhauUnicodeBridge.BP_MordhauUnicodeBridge_C"
BRIDGE_DISABLED="; MORDHAU_MANAGER_DISABLED: $BRIDGE_ACTOR"
MORDHAU_ROOT=""
CONFIG_TEMP=""
STATE_TEMP=""

usage() {
    cat <<'EOF'
Usage: ./src/unicode-bridge/install.sh --mordhau-root PATH

Installs or updates the cooked MORDHAU Unicode Bridge and registers its
nonreplicated server actor in active and staged Game.ini files.

The dedicated server must be stopped while this command runs.
EOF
}

die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

cleanup() {
    if [ -n "$CONFIG_TEMP" ] && [ -f "$CONFIG_TEMP" ]; then
        rm -f "$CONFIG_TEMP"
    fi
    if [ -n "$STATE_TEMP" ] && [ -f "$STATE_TEMP" ]; then
        rm -f "$STATE_TEMP"
    fi
}
trap cleanup EXIT INT TERM

while [ "$#" -gt 0 ]; do
    case "$1" in
        --mordhau-root)
            [ "$#" -ge 2 ] || die "--mordhau-root requires a value"
            MORDHAU_ROOT=$2
            shift
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

[ -n "$MORDHAU_ROOT" ] || {
    usage
    die "--mordhau-root is required"
}
case "$MORDHAU_ROOT" in
    /*)
        ;;
    *)
        die "--mordhau-root must be an absolute path"
        ;;
esac
[ -d "$MORDHAU_ROOT" ] || die "MORDHAU root does not exist: $MORDHAU_ROOT"
MORDHAU_ROOT=$(CDPATH= cd -- "$MORDHAU_ROOT" && pwd)
[ -f "$MORDHAU_ROOT/MordhauServer.exe" ] ||
    die "MordhauServer.exe is missing under $MORDHAU_ROOT"

STATE_DIR="$MORDHAU_ROOT/.manager"
BACKUP_DIR="$STATE_DIR/backups"
PENDING_DIR="$STATE_DIR/pending"
CONFIG_DIR="$MORDHAU_ROOT/Mordhau/Saved/Config/WindowsServer"
CUSTOM_PAKS_DIR="$MORDHAU_ROOT/Mordhau/Content/CustomPaks"
BRIDGE_PAK_TARGET="$CUSTOM_PAKS_DIR/MordhauUnicodeBridge-WindowsServer.pak"
GAME_INI="$CONFIG_DIR/Game.ini"

for command_name in awk cmp cp install mktemp mv sha256sum; do
    command -v "$command_name" >/dev/null 2>&1 ||
        die "required command is unavailable: $command_name"
done
for required in \
    "$DIST_DIR/SHA256SUMS" \
    "$BRIDGE_BUNDLE/MordhauUnicodeBridge.uplugin" \
    "$BRIDGE_BUNDLE/Content/Paks/MordhauUnicodeBridge-WindowsServer.pak"; do
    [ -f "$required" ] || die "Unicode Bridge bundle is incomplete: missing $required"
done
[ -f "$GAME_INI" ] || die "Game.ini is missing: $GAME_INI"

(
    cd "$DIST_DIR"
    sha256sum -c SHA256SUMS
) >/dev/null || die "Unicode Bridge bundle integrity verification failed"

install -d -m 0700 \
    "$BACKUP_DIR" \
    "$PENDING_DIR" \
    "$CUSTOM_PAKS_DIR"
install -m 0644 \
    "$BRIDGE_BUNDLE/Content/Paks/MordhauUnicodeBridge-WindowsServer.pak" \
    "$BRIDGE_PAK_TARGET"
cmp \
    "$BRIDGE_BUNDLE/Content/Paks/MordhauUnicodeBridge-WindowsServer.pak" \
    "$BRIDGE_PAK_TARGET" >/dev/null ||
    die "installed Unicode Bridge PAK verification failed"

bridge_config_ready() {
    awk \
        -v section="$BRIDGE_SECTION" \
        -v actor="$BRIDGE_ACTOR" \
        -v disabled="$BRIDGE_DISABLED" '
        {
            line = $0
            sub(/\r$/, "", line)
            if (line == section) {
                inside = 1
                next
            }
            if (line ~ /^\[/) {
                inside = 0
            }
            if (inside && line == actor) {
                active_count++
            }
            if (inside && line == disabled) {
                disabled_count++
            }
        }
        END {
            exit(active_count == 1 && disabled_count == 0 ? 0 : 1)
        }
    ' "$1"
}

ensure_bridge_actor() {
    config_file=$1
    backup_name=$2
    bridge_config_ready "$config_file" && return 0

    stamp=$(date '+%Y-%m-%d_%H-%M-%S')
    backup_file="$BACKUP_DIR/${backup_name}.${stamp}.unicode-bridge.bak"
    backup_suffix=0
    while [ -e "$backup_file" ]; do
        backup_suffix=$((backup_suffix + 1))
        backup_file="$BACKUP_DIR/${backup_name}.${stamp}.${backup_suffix}.unicode-bridge.bak"
    done
    cp -p "$config_file" "$backup_file"

    crlf=0
    if awk '
        NR == 1 {
            exit(substr($0, length($0), 1) == "\r" ? 0 : 1)
        }
        END {
            if (NR == 0) {
                exit 1
            }
        }
    ' "$config_file"; then
        crlf=1
    fi

    config_parent=${config_file%/*}
    CONFIG_TEMP=$(mktemp "$config_parent/.unicode-bridge.XXXXXX")
    if ! awk \
        -v section="$BRIDGE_SECTION" \
        -v actor="$BRIDGE_ACTOR" \
        -v disabled="$BRIDGE_DISABLED" \
        -v crlf="$crlf" '
        function emit(value) {
            printf "%s%s", value, crlf ? "\r\n" : "\n"
        }
        {
            line = $0
            sub(/\r$/, "", line)
            if (line ~ /^\[/) {
                if (inside && !inserted) {
                    emit(actor)
                    inserted = 1
                }
                inside = 0
            }
            if (line == section) {
                inside = 1
                section_found = 1
            }
            if (inside && (line == actor || line == disabled)) {
                if (!inserted) {
                    emit(actor)
                    inserted = 1
                }
                next
            }
            emit(line)
        }
        END {
            if (!section_found) {
                if (NR > 0) {
                    emit("")
                }
                emit(section)
            }
            if (!inserted) {
                emit(actor)
            }
        }
    ' "$config_file" > "$CONFIG_TEMP"; then
        rm -f "$CONFIG_TEMP"
        CONFIG_TEMP=""
        die "failed to update $config_file"
    fi
    chmod 0600 "$CONFIG_TEMP"
    mv "$CONFIG_TEMP" "$config_file"
    CONFIG_TEMP=""
}

ensure_bridge_actor "$GAME_INI" "Game.ini"
if [ -f "$PENDING_DIR/Game.ini" ]; then
    ensure_bridge_actor "$PENDING_DIR/Game.ini" "pending-Game.ini"
fi

STATE_TEMP=$(mktemp "$STATE_DIR/.unicode-bridge-version.XXXXXX")
printf '%s\n' "$BRIDGE_VERSION" > "$STATE_TEMP"
chmod 0600 "$STATE_TEMP"
mv "$STATE_TEMP" "$STATE_DIR/unicode-bridge-version"
STATE_TEMP=""

printf 'Installed MORDHAU Unicode Bridge %s.\n' "$BRIDGE_VERSION"
