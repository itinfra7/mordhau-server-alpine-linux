#!/bin/sh

set -eu

ROOT=/root/mordhau
BINARY="$ROOT/bin/mordhau-web"
PORT_FILE="$ROOT/.manager/web-port"
TRUSTED_PROXY_FILE="$ROOT/.manager/trusted-proxies"

usage() {
    cat <<'EOF'
Usage: /root/mordhau/webserver.sh --port <1-65535>

Starts the MORDHAU management web server on 0.0.0.0:<port>.
Trusted reverse-proxy addresses are loaded from
/root/mordhau/.manager/trusted-proxies, one IP address or CIDR prefix per line.
An absent or empty file trusts no proxy.

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

set -- "$BINARY" --listen "0.0.0.0:$port"
if [ -r "$TRUSTED_PROXY_FILE" ]; then
    while IFS= read -r trusted_proxy || [ -n "$trusted_proxy" ]; do
        [ -n "$trusted_proxy" ] || continue
        set -- "$@" --trusted-proxy "$trusted_proxy"
    done < "$TRUSTED_PROXY_FILE"
fi

exec "$@"
