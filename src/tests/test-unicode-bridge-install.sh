#!/bin/sh

set -eu

REPOSITORY_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TEST_ROOT=$(mktemp -d /tmp/mordhau-unicode-bridge-test.XXXXXX)
MORDHAU_ROOT="$TEST_ROOT/mordhau"
CONFIG_DIR="$MORDHAU_ROOT/Mordhau/Saved/Config/WindowsServer"
PENDING_DIR="$MORDHAU_ROOT/.manager/pending"
BACKUP_DIR="$MORDHAU_ROOT/.manager/backups"
ACTOR="SpawnServerActorsOnMapLoad=/MordhauUnicodeBridge/BP_MordhauUnicodeBridge.BP_MordhauUnicodeBridge_C"

cleanup() {
    case "$TEST_ROOT" in
        /tmp/mordhau-unicode-bridge-test.*)
            rm -rf "$TEST_ROOT"
            ;;
    esac
}
trap cleanup EXIT INT TERM

mkdir -p "$CONFIG_DIR" "$PENDING_DIR"
: > "$MORDHAU_ROOT/MordhauServer.exe"
printf '%s\r\n' \
    '[/Script/Mordhau.MordhauGameMode]' \
    'ServerName=BridgeTest' \
    'SpawnServerActorsOnMapLoad=/OtherServerPlugin/BP_Other.BP_Other_C' \
    "; MORDHAU_MANAGER_DISABLED: $ACTOR" \
    '[/Script/Mordhau.MordhauGameSession]' \
    'RconPassword=Example123' \
    > "$CONFIG_DIR/Game.ini"
printf '%s\n' \
    '[/Script/Mordhau.MordhauGameSession]' \
    'RconPassword=Pending123' \
    > "$PENDING_DIR/Game.ini"

"$REPOSITORY_ROOT/unicode-bridge/install.sh" \
    --mordhau-root "$MORDHAU_ROOT" >/dev/null

for file in "$CONFIG_DIR/Game.ini" "$PENDING_DIR/Game.ini"; do
    count=$(tr -d '\r' < "$file" | grep -Fxc "$ACTOR")
    [ "$count" -eq 1 ] || {
        printf 'Expected one bridge actor in %s, found %s.\n' "$file" "$count" >&2
        exit 1
    }
    if grep -Fq "; MORDHAU_MANAGER_DISABLED: $ACTOR" "$file"; then
        printf 'Disabled bridge residue remains in %s.\n' "$file" >&2
        exit 1
    fi
done
grep -Fq 'SpawnServerActorsOnMapLoad=/OtherServerPlugin/BP_Other.BP_Other_C' \
    "$CONFIG_DIR/Game.ini"
cr_count=$(tr -cd '\r' < "$CONFIG_DIR/Game.ini" | wc -c | tr -d ' ')
line_count=$(wc -l < "$CONFIG_DIR/Game.ini" | tr -d ' ')
[ "$cr_count" -eq "$line_count" ] || {
    printf '%s\n' 'Active Game.ini did not retain CRLF line endings.' >&2
    exit 1
}
cmp \
    "$REPOSITORY_ROOT/unicode-bridge/dist/MordhauUnicodeBridge/Content/Paks/MordhauUnicodeBridge-WindowsServer.pak" \
    "$MORDHAU_ROOT/Mordhau/Content/CustomPaks/MordhauUnicodeBridge-WindowsServer.pak"
[ ! -e "$MORDHAU_ROOT/Mordhau/Mods/MordhauUnicodeBridge" ]

backup_count=$(find "$BACKUP_DIR" -type f | wc -l | tr -d ' ')
[ "$backup_count" -eq 2 ]
"$REPOSITORY_ROOT/unicode-bridge/install.sh" \
    --mordhau-root "$MORDHAU_ROOT" >/dev/null
second_backup_count=$(find "$BACKUP_DIR" -type f | wc -l | tr -d ' ')
[ "$second_backup_count" -eq "$backup_count" ]

printf '%s\n' 'Unicode Bridge installation tests passed.'
