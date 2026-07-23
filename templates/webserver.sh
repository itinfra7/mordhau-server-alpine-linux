#!/bin/sh

set -eu

ROOT=/root/mordhau
BINARY="$ROOT/bin/mordhau-web"
PORT_FILE="$ROOT/.manager/web-port"

usage() {
    cat <<'EOF'
Usage: /root/mordhau/webserver.sh --port <1-65535>

Starts the MORDHAU management web server on 0.0.0.0:<port>.

Examples:
  /root/mordhau/webserver.sh --port 8080
  /root/mordhau/webserver.sh --port 8443
EOF
}

case "${1:-}" in
    help|-h|--help)
        usage
        exit 0
        ;;
    --port)
        [ "$#" -eq 2 ] || {
            usage
            exit 2
        }
        ;;
    *)
        usage
        exit 2
        ;;
esac

port=$2
case "$port" in
    ''|*[!0-9]*)
        usage
        exit 2
        ;;
esac
if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    usage
    exit 2
fi

if [ ! -x "$BINARY" ]; then
    printf 'Missing manager binary: %s\n' "$BINARY" >&2
    exit 1
fi

mkdir -p "$ROOT/.manager"
chmod 700 "$ROOT/.manager"
port_temp="$PORT_FILE.$$"
printf '%s\n' "$port" > "$port_temp"
chmod 600 "$port_temp"
mv "$port_temp" "$PORT_FILE"

exec "$BINARY" --listen "0.0.0.0:$port"
