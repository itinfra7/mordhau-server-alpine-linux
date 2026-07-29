#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE_SCRIPT="$SCRIPT_DIR/../templates/server.sh"
STEAM_RUNSCRIPT="$SCRIPT_DIR/../steamcmd/mordhau-update.txt"
TEST_ROOT=$(mktemp -d /tmp/mordhau-steamcmd-update.XXXXXX)

cleanup() {
    case "$TEST_ROOT" in
        /tmp/mordhau-steamcmd-update.*)
            if [ -e "$TEST_ROOT" ]; then
                find "$TEST_ROOT" -xdev -depth -delete
            fi
            ;;
    esac
}
trap cleanup EXIT HUP INT TERM

expected_runscript='@ShutdownOnFailedCommand 1
@NoPromptForPassword 1
@sSteamCmdForcePlatformType windows
@sSteamCmdForcePlatformBitness 64
force_install_dir Z:\root\mordhau
login anonymous
app_info_update 1
app_info_print 629800
app_update 629800 validate
quit'
[ "$(cat "$STEAM_RUNSCRIPT")" = "$expected_runscript" ] || {
    printf '%s\n' 'SteamCMD runscript does not contain the validated command order.' >&2
    exit 1
}

MORDHAU_ROOT="$TEST_ROOT/mordhau"
STEAMCMD_ROOT="$TEST_ROOT/steamcmd"
FAKE_BIN="$TEST_ROOT/bin"
TEST_SCRIPT="$TEST_ROOT/server.sh"
TEST_ATTEMPT_FILE="$TEST_ROOT/attempt"
TEST_SLEEP_FILE="$TEST_ROOT/sleep"
TEST_CONSOLE_LOG="$STEAMCMD_ROOT/logs/console_log.txt"
export TEST_ATTEMPT_FILE TEST_SLEEP_FILE TEST_CONSOLE_LOG

mkdir -p "$MORDHAU_ROOT" "$STEAMCMD_ROOT/logs" "$FAKE_BIN"
: > "$STEAMCMD_ROOT/steamcmd.exe"
: > "$STEAMCMD_ROOT/mordhau-update.txt"
: > "$TEST_CONSOLE_LOG"

sed \
    -e "s|^ROOT=/root/mordhau$|ROOT=$MORDHAU_ROOT|" \
    -e "s|^STEAMCMD=/root/steamcmd/steamcmd.exe$|STEAMCMD=$STEAMCMD_ROOT/steamcmd.exe|" \
    -e "s|^STEAM_SCRIPT=/root/steamcmd/mordhau-update.txt$|STEAM_SCRIPT=$STEAMCMD_ROOT/mordhau-update.txt|" \
    -e "s|^STEAM_CONSOLE_LOG=/root/steamcmd/logs/console_log.txt$|STEAM_CONSOLE_LOG=$TEST_CONSOLE_LOG|" \
    -e "s|cd /root/steamcmd|cd $STEAMCMD_ROOT|" \
    "$SOURCE_SCRIPT" |
    awk '
        /^pid_is_mordhau\(\) \{$/ {
            print "pid_is_mordhau() {"
            print "    return 1"
            print "}"
            replacing = 1
            next
        }
        replacing && /^}$/ {
            replacing = 0
            next
        }
        !replacing {
            print
        }
    ' > "$TEST_SCRIPT"
chmod 700 "$TEST_SCRIPT"

cat > "$FAKE_BIN/wine" <<'EOF'
#!/bin/sh
set -eu
attempt=0
if [ -r "$TEST_ATTEMPT_FILE" ]; then
    attempt=$(sed -n '1p' "$TEST_ATTEMPT_FILE")
fi
attempt=$((attempt + 1))
printf '%s\n' "$attempt" > "$TEST_ATTEMPT_FILE"
case "${TEST_WINE_MODE:-retry}" in
    retry)
        if [ "$attempt" -eq 1 ]; then
            printf "%s\n" "ERROR! Failed to install app '629800' (Missing configuration)" \
                >> "$TEST_CONSOLE_LOG"
            exit 1
        fi
        printf "%s\n" "Success! App '629800' fully installed." \
            >> "$TEST_CONSOLE_LOG"
        ;;
    fatal)
        printf '%s\n' 'ERROR! Failed to install app due to a disk write failure.' \
            >> "$TEST_CONSOLE_LOG"
        exit 1
        ;;
    *)
        exit 2
        ;;
esac
EOF
cat > "$FAKE_BIN/wineserver" <<'EOF'
#!/bin/sh
exit 0
EOF
cat > "$FAKE_BIN/sleep" <<'EOF'
#!/bin/sh
printf '%s\n' "$1" >> "$TEST_SLEEP_FILE"
EOF
chmod 700 "$FAKE_BIN/wine" "$FAKE_BIN/wineserver" "$FAKE_BIN/sleep"
PATH="$FAKE_BIN:$PATH"
export PATH

retry_output=$("$TEST_SCRIPT" update 2>&1)
[ "$(cat "$TEST_ATTEMPT_FILE")" -eq 2 ]
[ "$(cat "$TEST_SLEEP_FILE")" = 5 ]
printf '%s\n' "$retry_output" | grep -Fq 'SteamCMD attempt 1/6.'
printf '%s\n' "$retry_output" |
    grep -Fq 'SteamCMD is still loading App ID 629800 metadata; retrying in 5 seconds.'
printf '%s\n' "$retry_output" | grep -Fq 'SteamCMD attempt 2/6.'
printf '%s\n' "$retry_output" |
    grep -Fq 'MORDHAU Dedicated Server is up to date.'
[ ! -e "$MORDHAU_ROOT/.manager/runtime/steamcmd-console-attempt.log" ]
grep -Fq "Missing configuration" \
    "$MORDHAU_ROOT/.manager/runtime/steamcmd-console-current.log"
grep -Fq "Success! App '629800' fully installed." \
    "$MORDHAU_ROOT/.manager/runtime/steamcmd-console-current.log"

: > "$TEST_ATTEMPT_FILE"
: > "$TEST_SLEEP_FILE"
TEST_WINE_MODE=fatal
export TEST_WINE_MODE
if "$TEST_SCRIPT" update > "$TEST_ROOT/fatal-output.log" 2>&1; then
    printf '%s\n' 'A non-metadata SteamCMD failure was accepted.' >&2
    exit 1
fi
[ "$(cat "$TEST_ATTEMPT_FILE")" -eq 1 ]
[ ! -s "$TEST_SLEEP_FILE" ]
grep -Fq 'disk write failure' "$TEST_ROOT/fatal-output.log"

printf '%s\n' 'SteamCMD update tests passed.'
