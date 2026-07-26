#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
TEST_DIR=$(mktemp -d /tmp/mordhau-runtime-bridge-test.XXXXXX)

cleanup() {
    case "$TEST_DIR" in
        /tmp/mordhau-runtime-bridge-test.*)
            rm -rf "$TEST_DIR"
            ;;
    esac
}
trap cleanup EXIT INT TERM

command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1
command -v x86_64-w64-mingw32-objdump >/dev/null 2>&1

"$PROJECT_ROOT/src/runtime-bridge/build.sh" "$TEST_DIR/dxgi.dll" >/dev/null
"$PROJECT_ROOT/src/runtime-bridge/build.sh" "$TEST_DIR/dxgi-second.dll" >/dev/null
[ -s "$TEST_DIR/dxgi.dll" ]
[ -s "$TEST_DIR/dxgi-second.dll" ]
cmp "$TEST_DIR/dxgi.dll" "$TEST_DIR/dxgi-second.dll"
x86_64-w64-mingw32-objdump -p "$TEST_DIR/dxgi.dll" |
    grep -Fq 'CreateDXGIFactory'
grep -Fq \
    'typedef void (__fastcall *FPropertyExportTextItemFn)(' \
    "$PROJECT_ROOT/src/runtime-bridge/runtime_bridge.c"
grep -Fq \
    'RVA_AACTOR_FLUSH_NET_DORMANCY = 0x02A24C30' \
    "$PROJECT_ROOT/src/runtime-bridge/runtime_bridge.c"

printf '%s\n' 'Runtime bridge build test passed.'
