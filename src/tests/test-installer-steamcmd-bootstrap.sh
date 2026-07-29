#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE_SCRIPT="$SCRIPT_DIR/../mordhau-server-alpine-linux.sh"
TEST_ROOT=$(mktemp -d /tmp/mordhau-installer-steamcmd.XXXXXX)
LIBRARY="$TEST_ROOT/steamcmd.sh"

cleanup() {
    case "$TEST_ROOT" in
        /tmp/mordhau-installer-steamcmd.*)
            if [ -e "$TEST_ROOT" ]; then
                find "$TEST_ROOT" -xdev -depth -delete
            fi
            ;;
    esac
}
trap cleanup EXIT HUP INT TERM

sed -n '/^steamcmd_installation_complete() {$/,/^}$/p' \
    "$SOURCE_SCRIPT" > "$LIBRARY"

# shellcheck disable=SC1090
. "$LIBRARY"

STEAMCMD_ROOT="$TEST_ROOT/steamcmd"
mkdir -p "$STEAMCMD_ROOT/package"

for required in \
    steamcmd.exe \
    steam.dll \
    steamclient.dll \
    steamconsole.dll \
    tier0_s.dll \
    vstdlib_s.dll \
    package/steam_cmd_win32.installed
do
    printf '%s\n' present > "$STEAMCMD_ROOT/$required"
done

steamcmd_installation_complete || {
    printf '%s\n' 'A complete SteamCMD installation was rejected.' >&2
    exit 1
}

: > "$STEAMCMD_ROOT/steamconsole.dll"
if steamcmd_installation_complete; then
    printf '%s\n' 'An incomplete SteamCMD installation was accepted.' >&2
    exit 1
fi

printf '%s\n' 'Installer SteamCMD bootstrap tests passed.'
