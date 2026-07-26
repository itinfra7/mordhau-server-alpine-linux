#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
OUTPUT=${1:-"$SCRIPT_DIR/dxgi.dll"}

command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1 || {
    printf '%s\n' "x86_64-w64-mingw32-gcc is required." >&2
    exit 1
}

x86_64-w64-mingw32-gcc \
    -std=c11 \
    -O2 \
    -s \
    -Wall \
    -Wextra \
    -Werror \
    -municode \
    -shared \
    -static-libgcc \
    -Wl,--image-base,0x180000000,--no-insert-timestamp \
    -o "$OUTPUT" \
    "$SCRIPT_DIR/runtime_bridge.c" \
    "$SCRIPT_DIR/dxgi.def"

printf '%s\n' "$OUTPUT"
