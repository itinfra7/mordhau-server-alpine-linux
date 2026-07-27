#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE_DIR="$SCRIPT_DIR/source/MordhauUnicodeBridge"
DIST_DIR="$SCRIPT_DIR/dist"
EDITOR_ROOT=""
WINE_PREFIX=""
BUILD_TEMP=""

usage() {
    cat <<'EOF'
Usage: ./src/unicode-bridge/build-windows-server.sh --editor-root PATH --wine-prefix PATH

Cooks the Blueprint source with the official MORDHAU Editor for WindowsServer
and rebuilds the distributable PAK. The command runs the Windows editor tools
through Wine and uses xvfb-run automatically when it is available.
EOF
}

die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

cleanup() {
    if [ -n "$BUILD_TEMP" ]; then
        case "$BUILD_TEMP" in
            /tmp/mordhau-unicode-bridge.*)
                if [ -e "$BUILD_TEMP" ]; then
                    find "$BUILD_TEMP" -xdev -depth -delete
                fi
                ;;
        esac
    fi
}
trap cleanup EXIT INT TERM

while [ "$#" -gt 0 ]; do
    case "$1" in
        --editor-root)
            [ "$#" -ge 2 ] || die "--editor-root requires a value"
            EDITOR_ROOT=$2
            shift
            ;;
        --wine-prefix)
            [ "$#" -ge 2 ] || die "--wine-prefix requires a value"
            WINE_PREFIX=$2
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

[ -n "$EDITOR_ROOT" ] || {
    usage
    die "--editor-root is required"
}
[ -n "$WINE_PREFIX" ] || {
    usage
    die "--wine-prefix is required"
}
[ -d "$EDITOR_ROOT" ] || die "editor root does not exist: $EDITOR_ROOT"
[ -d "$WINE_PREFIX" ] || die "Wine prefix does not exist: $WINE_PREFIX"
EDITOR_ROOT=$(CDPATH= cd -- "$EDITOR_ROOT" && pwd)
WINE_PREFIX=$(CDPATH= cd -- "$WINE_PREFIX" && pwd)
export WINEPREFIX="$WINE_PREFIX"
export WINEDEBUG=-all

PROJECT_ROOT="$EDITOR_ROOT/Mordhau"
UPROJECT="$PROJECT_ROOT/MordhauSDK.uproject"
EDITOR_CMD="$EDITOR_ROOT/InstalledBuild/Windows/Engine/Binaries/Win64/UE4Editor-Cmd.exe"
UNREALPAK="$EDITOR_ROOT/InstalledBuild/Windows/Engine/Binaries/Win64/UnrealPak.exe"
EDITOR_PLUGIN="$PROJECT_ROOT/Mods/MordhauUnicodeBridge"
COOK_ROOT="$PROJECT_ROOT/Saved/Cooked/WindowsServer/MordhauSDK"
COOKED_PLUGIN="$COOK_ROOT/Mods/MordhauUnicodeBridge"
ASSET_REGISTRY="$COOK_ROOT/AssetRegistry.bin"
COOKED_ASSET="$COOKED_PLUGIN/Content/BP_MordhauUnicodeBridge.uasset"
COOKED_EXPORT="$COOKED_PLUGIN/Content/BP_MordhauUnicodeBridge.uexp"
DIST_PLUGIN="$DIST_DIR/MordhauUnicodeBridge"
PAK="$DIST_PLUGIN/Content/Paks/MordhauUnicodeBridge-WindowsServer.pak"

for command_name in grep install mktemp sha256sum unlink wine winepath; do
    command -v "$command_name" >/dev/null 2>&1 ||
        die "required command is unavailable: $command_name"
done
for required in \
    "$UPROJECT" \
    "$EDITOR_CMD" \
    "$UNREALPAK" \
    "$SOURCE_DIR/MordhauUnicodeBridge.uplugin" \
    "$SOURCE_DIR/Content/BP_MordhauUnicodeBridge.uasset"; do
    [ -f "$required" ] || die "required build input is missing: $required"
done

BUILD_TEMP=$(mktemp -d /tmp/mordhau-unicode-bridge.XXXXXX)
chmod 0700 "$BUILD_TEMP"
if [ -z "${XDG_RUNTIME_DIR:-}" ]; then
    XDG_RUNTIME_DIR="$BUILD_TEMP/xdg-runtime"
    install -d -m 0700 "$XDG_RUNTIME_DIR"
    export XDG_RUNTIME_DIR
fi
install -d -m 0755 "$EDITOR_PLUGIN/Content"
install -m 0644 \
    "$SOURCE_DIR/MordhauUnicodeBridge.uplugin" \
    "$EDITOR_PLUGIN/MordhauUnicodeBridge.uplugin"
install -m 0644 \
    "$SOURCE_DIR/Content/BP_MordhauUnicodeBridge.uasset" \
    "$EDITOR_PLUGIN/Content/BP_MordhauUnicodeBridge.uasset"

for stale_output in "$ASSET_REGISTRY" "$COOKED_ASSET" "$COOKED_EXPORT"; do
    [ ! -e "$stale_output" ] || unlink "$stale_output"
done

project_windows=$(winepath -w "$UPROJECT")
set +e
if command -v xvfb-run >/dev/null 2>&1; then
    xvfb-run -a wine "$EDITOR_CMD" "$project_windows" \
        -run=Cook \
        -TargetPlatform=WindowsServer \
        -CookDir=/MordhauUnicodeBridge \
        -unattended \
        -nop4 \
        -NoLogTimes \
        -UTF8Output > "$BUILD_TEMP/cook.log" 2>&1
else
    wine "$EDITOR_CMD" "$project_windows" \
        -run=Cook \
        -TargetPlatform=WindowsServer \
        -CookDir=/MordhauUnicodeBridge \
        -unattended \
        -nop4 \
        -NoLogTimes \
        -UTF8Output > "$BUILD_TEMP/cook.log" 2>&1
fi
cook_status=$?
set -e

for cooked_file in "$ASSET_REGISTRY" "$COOKED_ASSET" "$COOKED_EXPORT"; do
    if [ ! -s "$cooked_file" ]; then
        tail -n 80 "$BUILD_TEMP/cook.log" >&2 || true
        die "cook did not produce $cooked_file"
    fi
done
if [ "$cook_status" -ne 0 ]; then
    printf '%s\n' \
        "Warning: the editor commandlet returned $cook_status; validated bridge cook outputs will be packaged." >&2
fi

response_file="$BUILD_TEMP/MordhauUnicodeBridge-WindowsServer.txt"
registry_windows=$(winepath -w "$ASSET_REGISTRY")
asset_windows=$(winepath -w "$COOKED_ASSET")
export_windows=$(winepath -w "$COOKED_EXPORT")
{
    printf '"%s" "%s" -compress\n' \
        "$registry_windows" \
        "../../../Mordhau/Mods/MordhauUnicodeBridge/AssetRegistry.bin"
    printf '"%s" "%s" -compress\n' \
        "$asset_windows" \
        "../../../Mordhau/Mods/MordhauUnicodeBridge/Content/BP_MordhauUnicodeBridge.uasset"
    printf '"%s" "%s" -compress\n' \
        "$export_windows" \
        "../../../Mordhau/Mods/MordhauUnicodeBridge/Content/BP_MordhauUnicodeBridge.uexp"
} > "$response_file"

install -d -m 0755 "$DIST_PLUGIN/Content/Paks"
install -m 0644 \
    "$SOURCE_DIR/MordhauUnicodeBridge.uplugin" \
    "$DIST_PLUGIN/MordhauUnicodeBridge.uplugin"
[ ! -e "$PAK" ] || unlink "$PAK"
pak_windows=$(winepath -w "$PAK")
response_windows=$(winepath -w "$response_file")
wine "$UNREALPAK" "$pak_windows" "-Create=$response_windows"
wine "$UNREALPAK" "$pak_windows" -Test
wine "$UNREALPAK" "$pak_windows" -List > "$BUILD_TEMP/pak-list.log"
grep -Fq \
    "Mount point ../../../Mordhau/Mods/MordhauUnicodeBridge/" \
    "$BUILD_TEMP/pak-list.log"
grep -Fq \
    "\"AssetRegistry.bin\"" \
    "$BUILD_TEMP/pak-list.log"
grep -Fq \
    "\"Content/BP_MordhauUnicodeBridge.uasset\"" \
    "$BUILD_TEMP/pak-list.log"
grep -Fq \
    "\"Content/BP_MordhauUnicodeBridge.uexp\"" \
    "$BUILD_TEMP/pak-list.log"

(
    cd "$DIST_DIR"
    sha256sum \
        MordhauUnicodeBridge/MordhauUnicodeBridge.uplugin \
        MordhauUnicodeBridge/Content/Paks/MordhauUnicodeBridge-WindowsServer.pak \
        > SHA256SUMS
)

printf 'Built %s\n' "$PAK"
sha256sum "$PAK"
