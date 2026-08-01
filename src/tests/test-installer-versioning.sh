#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE_SCRIPT="$SCRIPT_DIR/../mordhau-server-alpine-linux.sh"
TEST_ROOT=$(mktemp -d /tmp/mordhau-installer-versioning.XXXXXX)
LIBRARY="$TEST_ROOT/versioning.sh"

cleanup() {
    case "$TEST_ROOT" in
        /tmp/mordhau-installer-versioning.*)
            if [ -e "$TEST_ROOT" ]; then
                find "$TEST_ROOT" -xdev -depth -delete
            fi
            ;;
    esac
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$TEST_ROOT/state"
{
    sed -n '/^version_valid() {$/,/^}$/p' "$SOURCE_SCRIPT"
    sed -n '/^compare_versions() {$/,/^}$/p' "$SOURCE_SCRIPT"
    sed -n '/^prepare_version_transition() {$/,/^}$/p' "$SOURCE_SCRIPT"
    sed -n '/^record_installed_version() {$/,/^}$/p' "$SOURCE_SCRIPT"
} > "$LIBRARY"

# shellcheck disable=SC1090
. "$LIBRARY"

PROJECT_VERSION=2.6.6
STATE_DIR="$TEST_ROOT/state"
ALLOW_DOWNGRADE=0
INSTALLED_VERSION=""
log() {
    printf '%s\n' "$*"
}
die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

for version in 0.0.0 2.6.6 10.20.300; do
    version_valid "$version" || {
        printf 'Valid version rejected: %s\n' "$version" >&2
        exit 1
    }
done
for version in "" 1 1.2 1.2.3.4 v1.2.3 1..3 1.2.-3 01.2.3 1.02.3 1.2.03 1234567890.2.3; do
    if version_valid "$version"; then
        printf 'Invalid version accepted: %s\n' "$version" >&2
        exit 1
    fi
done

[ "$(compare_versions 2.6.5 2.6.6)" -eq -1 ]
[ "$(compare_versions 2.6.6 2.6.6)" -eq 0 ]
[ "$(compare_versions 2.7.0 2.6.6)" -eq 1 ]
[ "$(compare_versions 3.0.0 2.99.99)" -eq 1 ]

prepare_version_transition > "$TEST_ROOT/fresh.log"
grep -Fq 'Installing mordhau-server-alpine-linux 2.6.6.' "$TEST_ROOT/fresh.log"
[ -z "$INSTALLED_VERSION" ]

printf '%s\n' 2.6.5 > "$STATE_DIR/manager-version"
prepare_version_transition > "$TEST_ROOT/upgrade.log"
grep -Fq \
    'Upgrading mordhau-server-alpine-linux 2.6.5 -> 2.6.6.' \
    "$TEST_ROOT/upgrade.log"
[ "$INSTALLED_VERSION" = 2.6.5 ]

printf '%s\n' 2.6.6 > "$STATE_DIR/manager-version"
prepare_version_transition > "$TEST_ROOT/reinstall.log"
grep -Fq \
    'Reinstalling mordhau-server-alpine-linux 2.6.6 for validation.' \
    "$TEST_ROOT/reinstall.log"

printf '%s\n' 2.7.0 > "$STATE_DIR/manager-version"
if (
    ALLOW_DOWNGRADE=0
    prepare_version_transition
) > "$TEST_ROOT/downgrade-denied.log" 2>&1; then
    printf '%s\n' 'Unapproved management-code downgrade was accepted.' >&2
    exit 1
fi
grep -Fq 'without --allow-downgrade' "$TEST_ROOT/downgrade-denied.log"

ALLOW_DOWNGRADE=1
prepare_version_transition > "$TEST_ROOT/downgrade-allowed.log"
grep -Fq \
    'Downgrading mordhau-server-alpine-linux 2.7.0 -> 2.6.6.' \
    "$TEST_ROOT/downgrade-allowed.log"

printf '%s\n%s\n' 2.3.2 unexpected > "$STATE_DIR/manager-version"
if (
    prepare_version_transition
) > "$TEST_ROOT/invalid-state.log" 2>&1; then
    printf '%s\n' 'Invalid installed version state was accepted.' >&2
    exit 1
fi
grep -Fq 'installed manager version state is invalid' "$TEST_ROOT/invalid-state.log"

printf '%s\n' 2.6.0 > "$STATE_DIR/manager-version"
record_installed_version
[ "$(cat "$STATE_DIR/manager-version")" = 2.6.6 ]
[ "$(stat -c '%a' "$STATE_DIR/manager-version")" = 600 ]
[ -z "$(find "$STATE_DIR" -maxdepth 1 -name '.manager-version.*' -print)" ]

printf '%s\n' 'Installer versioning tests passed.'
