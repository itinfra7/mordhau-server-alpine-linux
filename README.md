# MORDHAU Server Alpine Linux

This repository installs and manages the Windows build of MORDHAU Dedicated
Server on Alpine Linux through Wine. It includes SteamCMD update automation,
OpenRC services, and an authenticated Go web manager.

## Repository Scope

The repository provides:

- Windows SteamCMD installation under `/root/steamcmd`
- Windows MORDHAU Dedicated Server installation under `/root/mordhau`
- A dedicated Wine prefix at `/root/mordhau/.wine`
- SteamCMD validation before every server start or restart
- POSIX shell lifecycle control
- OpenRC service definitions for the game server and web manager
- An animated responsive Go web manager with shared one-minute system metrics
  and persistent light/dark themes
- Structured Game.ini and Engine.ini editing
- Optional mod.io metadata, recursive dependency status, and dependency
  management for Game.ini
- Persistent initial-map and dedicated-server port selection
- Authenticated RCON event streaming with UTF-8 player-chat integration
- Persistent latest lifecycle results and append-only RCON event history
- A server-only Unicode Bridge for acknowledged outbound multilingual messages
- Web account and IPv4/IPv6 access-policy management with inclusive IPv4
  ranges and per-rule comments
- Optional trusted reverse-proxy client-IP resolution with direct-access
  fallback
- Per-account JSON Lines web access and change auditing

MORDHAU, SteamCMD, Wine, and their assets are downloaded from their respective
upstream distribution channels and are not included in this repository.

## Supported Environment

- Alpine Linux 3.24
- x86_64 architecture
- OpenRC
- Root privileges
- Internet access to Alpine package mirrors, SteamCMD, Steam content servers,
  and Go module sources
- A mod.io API key when URL lookup, metadata, and recursive dependency
  inspection are required
- A modern desktop or mobile browser

The installer requires enough free storage for Wine, SteamCMD, Go build data,
MORDHAU Dedicated Server, and Steam update staging.

## Repository Layout

- `src/mordhau-server-alpine-linux.sh`
  Idempotent installer and management-code updater.
- `src/templates/server.sh`
  MORDHAU update, start, stop, restart, and status controller.
- `src/templates/webserver.sh`
  Foreground web-manager launcher with persistent port and trusted-proxy
  selection.
- `src/templates/openrc/`
  OpenRC service definitions.
- `src/steamcmd/mordhau-update.txt`
  SteamCMD runscript for Windows App ID `629800`.
- `src/web/`
  Go source, embedded frontend assets, and tests.
- `src/unicode-bridge/`
  Editable Blueprint source, cooked WindowsServer PAK, build tooling, integrity
  manifest, and standalone installer for the server-only Unicode Bridge.
- `src/tests/`
  Shell integration tests for repository-managed installation behavior.
- `CHANGELOG.md` and `RELEASE_NOTES.md`
  Version history and release-specific technical summary.
- `LICENSE`
  MIT terms for repository-authored source.

## Installation

### Release archive

```sh
wget https://github.com/itinfra7/mordhau-server-alpine-linux/releases/download/v1.8.2/mordhau-server-alpine-linux-v1.8.2.tar.gz
wget https://github.com/itinfra7/mordhau-server-alpine-linux/releases/download/v1.8.2/SHA256SUMS
sha256sum -c SHA256SUMS
tar -xzf mordhau-server-alpine-linux-v1.8.2.tar.gz
cd mordhau-server-alpine-linux-v1.8.2
chmod +x src/mordhau-server-alpine-linux.sh
./src/mordhau-server-alpine-linux.sh
```

### Repository checkout

```sh
git clone https://github.com/itinfra7/mordhau-server-alpine-linux.git
cd mordhau-server-alpine-linux
chmod +x src/mordhau-server-alpine-linux.sh
./src/mordhau-server-alpine-linux.sh
```

Installer options:

```text
--web-port PORT   Persist the web service port
--start-web       Start the web manager after installation
--start-server    Start MORDHAU Dedicated Server after installation
--enable-web      Enable web-manager boot startup and start it
--enable-server   Enable game-server boot startup and start it
```

By default, both OpenRC services are installed in manual mode and remain
stopped. Existing boot-start settings and running states are preserved during
management-code updates.

## First Installation

The installer performs these operations:

1. Installs Alpine packages required by Wine, SteamCMD, Go, and OpenRC.
2. Downloads and self-updates Windows SteamCMD.
3. Installs or validates MORDHAU Dedicated Server App ID `629800`.
4. Runs the server without game options for five seconds when generated
   WindowsServer configuration files do not exist.
5. Generates an eight-character mixed-case alphanumeric RCON password and
   enables RCON on port `7778` through the generated
   `/Script/Mordhau.MordhauGameSession` section.
6. Verifies and installs the cooked server-only Unicode Bridge, then registers
   its nonreplicated server actor in active and staged Game.ini files.
7. Builds and installs the Go web manager.
8. Creates the initial web account and persistent security state.
9. Installs both OpenRC service definitions.

The initial web credentials are written with mode `0600` to:

```text
/root/mordhau/default_web_account.txt
```

The generated username contains four lowercase letters or digits. The
generated password contains eight characters with lowercase, uppercase, and
numeric characters.

## Server Control

```sh
/root/mordhau/server.sh start
/root/mordhau/server.sh stop
/root/mordhau/server.sh restart
/root/mordhau/server.sh update
/root/mordhau/server.sh status
```

Behavior:

- `start` validates the Steam installation, applies staged INI changes,
  archives the previous log, and starts the server.
- `stop` performs graceful termination and then stops the dedicated Wine
  prefix if required.
- `restart` stops, validates, applies staged changes, and starts.
- `update` only runs while the game server is stopped.
- Every managed launch uses the selected initial map, game/RCON/beacon/query
  ports, `-language=<code>`, `-LocalLogTimes`, and `-log`.

Before each managed launch, an existing `Mordhau.log` is moved to:

```text
/root/mordhau/log/Mordhau_<yyyy-mm-dd_hh-mm-ss>.log
```

The timestamp is derived from the log file's final modification time. Existing
archives are never overwritten.

## Web Manager

Start in the foreground:

```sh
/root/mordhau/webserver.sh --port 8080
```

Start through OpenRC using the saved port:

```sh
rc-service mordhau-web start
```

The listener is IPv4 `0.0.0.0:<port>`.

The web manager provides:

- Login with optional 30-day persistent authentication
- Argon2id password hashing and CSRF-protected state changes
- Account creation, editing, and deletion
- Last-account deletion prevention
- IPv4 and IPv6 address/CIDR allow and deny rules
- Inclusive IPv4 allow and deny ranges using `start-end` or `start~end`
- Optional UTF-8 comments on individual network rules
- Selectable `all allow` or `all deny` base policy
- A 30-minute exact-address emergency allow when switching to `all deny`
- CPU, memory, swap, and MORDHAU-filesystem utilization sampled once per
  minute by one server-side collector and shared across administrator sessions
- A default light theme with a persistent light/dark toggle
- Responsive phone, tablet, and desktop layouts with notched-display safe
  areas and touch-sized controls
- Start, stop, restart, and stopped-only update controls
- Persistent latest lifecycle action, requester account and canonical client
  IP, result, timestamps, and output
- Boot startup mode controls for both OpenRC services
- Persistent web-service port selection
- Persistent initial-map selection
- Game, RCON, beacon, and query port selection with range and collision checks
- Launch-language selection
- Optional mod.io API-key validation, mod lookup, per-mod recursive dependency
  status, unresolved-dependency warnings, and scoped `Mods=<Resource ID>`
  management
- Server-wide cached mod metadata auto-refresh from 1 to 10,080 minutes,
  defaulting to 60 minutes
- Game.ini and Engine.ini section/item creation, editing, and removal
- Reversible per-entry enable and disable controls
- Revision checks, active-file backups, and staged edits while the game is
  running
- RCON authentication, acknowledged `listen allon` subscription, reconnection
  across live Game.ini credential changes, and live events
- Root-only RCON event persistence with recent-history loading for later
  administrator sessions
- A Send Message form with a root-only UTF-8 spool, ASCII token RCON
  transport, and acknowledgement before success is reported
- Root-only web access and administrative change audit logging

IPv4 ranges include both endpoints and are stored in canonical `start-end`
form. The manager decomposes each range into the smallest exact set of CIDR
blocks, so addresses outside the submitted boundaries never match. Those
blocks participate in the same most-specific-prefix and equal-prefix deny
precedence as ordinary CIDR rules.

Each explicit network rule can store an optional single-line comment of up to
160 Unicode characters. Comments are metadata only and do not affect address
matching or rule precedence. Existing rules without a comment remain valid.

## Trusted Reverse Proxies

Direct access remains enabled and is the default. For a direct request, the
manager uses the canonical TCP peer address and ignores `X-Forwarded-For`,
`X-Real-IP`, and `Forwarded`, including malformed or forged values.

Trusted reverse proxies are configured in:

```text
/root/mordhau/.manager/trusted-proxies
```

The installer creates this mode-`0600` file empty, so no proxy is trusted by
default. Add one IP address or CIDR prefix per line and restart only the web
service. For example:

```sh
printf '%s\n' '192.0.2.10/32' > /root/mordhau/.manager/trusted-proxies
chmod 0600 /root/mordhau/.manager/trusted-proxies
rc-service mordhau-web restart
```

The launcher passes each entry as a repeatable `--trusted-proxy` startup
option. IPv4, IPv6, and IPv4-mapped IPv6 values are parsed structurally and
used canonically.

A request from a trusted TCP peer must contain exactly one
`X-Forwarded-For` header holding one IP address. Missing or empty values,
duplicate header fields, comma-separated chains, malformed addresses, IPv6
zone identifiers, unspecified addresses, and multicast addresses receive
HTTP 400 before session authentication. The manager uses that validated
address for access rules, emergency access, login throttling, lifecycle
request attribution, and audit records. The original TCP peer is retained
separately as `peer_ip` in the root-only audit log.

A compatible reverse proxy must replace, rather than append to, the forwarded
client address:

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header Forwarded "";
```

TLS may terminate at the reverse proxy while the built-in listener continues
to serve HTTP on the trusted internal network. Unproxied HTTP access to the
configured `0.0.0.0:<port>` listener continues to work under the same network
access policy.

## Mobile Layout

The dashboard and login page use the same authenticated endpoints on desktop
and mobile browsers. At widths of 720 pixels and below, controls use
touch-sized targets, technical inputs use a 16-pixel font, and multi-column
forms reflow without page-level horizontal scrolling. The header retains
server and account status, while the section tabs remain horizontally
scrollable.

At phone widths, INI rows, account and network-rule actions, mod controls,
dependency details, port fields, and Live RCON records stack vertically.
Long technical values wrap within their cards. Viewport safe-area insets are
applied for notched displays and standalone browser windows.

## Web Audit Log

The web manager writes a dedicated JSON Lines audit log to:

```text
/root/mordhau/log/mordhau-web.log
```

The file is created with mode `0600`. Every record contains a local RFC 3339
timestamp with second precision, an event name, and the responsible account.
HTTP access records also contain the canonical client IP address, direct TCP
peer IP address, method, path, response status, response size, and request
duration.

Dedicated events identify login success, login failure, logout, server
actions and their completion, language changes, initial-map and port changes,
mod configuration, mod.io connection changes, manual metadata refreshes,
server-wide refresh-interval changes, Game.ini and Engine.ini mutations,
pending-configuration removal, Unicode server-message sends, account changes,
network-policy changes, OpenRC boot-mode changes, and saved web-port changes.
Requests without a valid session use the account name `unauthenticated`.

Passwords, request bodies, session cookies, CSRF tokens, RCON credentials,
configuration values, and configuration revisions are not written to the
audit log. Configuration events identify the file, operation, section, and
key without recording its value. Unicode server-message audit events record
only UTF-8 byte and character counts, not message text. Network-rule events
record whether a comment is present and its character count without recording
the comment text.

## Persistent Dashboard History

The latest lifecycle operation is stored in:

```text
/root/mordhau/.manager/operation.json
```

The mode-`0600` state includes the fixed action, requesting account, canonical
requesting client IP, start and finish times, result, and captured command
output. The dashboard reloads that state for later sessions and after
web-service restarts. If the web manager stops before a running operation
records its result, the next start preserves the operation and marks it as
interrupted instead of leaving it permanently running.

Every accepted Live RCON event is appended as UTF-8 JSON Lines to:

```text
/root/mordhau/log/mordhau-rcon.log
```

Each mode-`0600` record contains a monotonic sequence, timestamp, event kind,
and text. The log is retained across web-service restarts. A newly connected
administrator receives the latest 400 events once, then continues from the
authenticated event stream without repeatedly transferring the entire
on-disk history. The browser retains the same 400-event Live RCON window.

## Languages

The launch selector supports:

- English (`en`)
- German (`de`)
- Spanish (`es`)
- Simplified Chinese (`zh-Hans`)
- French (`fr`)
- Italian (`it`)
- Portuguese (`pt`)
- Russian (`ru`)
- Korean (`ko`)
- Traditional Chinese (`zh-Hant`)

RCON packets are fully framed before text decoding. Valid UTF-8 is preserved;
language-specific legacy decoding is used only for invalid UTF-8 payloads.

MORDHAU can replace non-ASCII player chat before emitting its RCON chat
broadcast, leaving no recoverable characters in that packet. The web manager
therefore follows new player-chat records from the UTF-8 `Mordhau.log` and
merges them into the live event stream. Direct RCON chat records are
suppressed to prevent duplicate or lossy output; login, match-state, killfeed,
scorefeed, custom, and punishment events continue to come from authenticated
RCON. The log follower handles partial writes and log rotation and starts at
the end of an existing log to avoid replaying historical chat.

The steady RCON reader uses a 90-second read deadline as an idle wake-up. If no
packet byte arrives before that deadline, the manager sends the idempotent
`listen allon` command to renew the subscription before MORDHAU's own idle
connection timeout. The acknowledgement is consumed without adding it to Live
RCON. A timeout after a partial packet remains a connection error because the
stream may be incomplete. Real connection loss updates the status and retries.

Transport connection and reconnection messages are omitted from Live RCON
because the current state is already shown above the console. Historical
transport-status records are also filtered when loading retained RCON history.
Connection transitions and non-idle failures remain available in the
root-only web audit log.

The manager combines the current Game.ini RCON password with the saved RCON
launch port. After each successful authentication, it stores the working
loopback endpoint and credential in `/root/mordhau/.manager/rcon-last.json`
with mode `0600`. If Game.ini or the saved port is edited while the game is
running, the server continues using its in-memory settings until restart; the
saved working settings allow the web manager to reconnect and keep
`listen allon` active during that interval. The next game restart applies the
edited values and replaces the saved reconnect state.

Outbound text from the Live RCON panel uses the bundled MORDHAU Unicode
Bridge. The manager validates UTF-8, rejects control characters, limits each
message to 512 Unicode characters and 2,048 UTF-8 bytes, and writes it to a
mode-`0600` transient file under
`/root/mordhau/Mordhau/Saved/PlayerFiles`. The filename contains a random
24-digit token. Only the ASCII command `string unicode.say <token>` passes
through MORDHAU's RCON parser. The server actor validates the numeric token,
loads the corresponding UTF-8 file, and broadcasts the text to every connected
`MordhauPlayerController` by invoking the reflected `ClientReceiveMessage`
reliable client RPC through
`MordhauUtilityLibrary.CallFunctionByNameWithArgs`. The web request succeeds
only after the actor finishes the controller loop and returns `unicode.say ok`
on the `custom` RCON broadcast channel.

The spool directory uses mode `0700`. The manager removes each message file
after the send attempt and removes stale files matching the managed
24-digit-token filename format when the web service starts. Unrelated files
under `Saved/PlayerFiles` are not removed.

The bridge is installed under:

```text
/root/mordhau/Mordhau/Content/CustomPaks/MordhauUnicodeBridge-WindowsServer.pak
```

It is registered through `SpawnServerActorsOnMapLoad` and is not added to
Game.ini `Mods=` entries. Its actor is nonreplicated and disables client
network loading, so connected players do not download the bridge. Editable
Blueprint source, the cooked WindowsServer PAK, build instructions, and the
standalone installer are documented in `src/unicode-bridge/README.md`.

## Configuration Management

The web manager edits the server-generated files:

```text
/root/mordhau/Mordhau/Saved/Config/WindowsServer/Game.ini
/root/mordhau/Mordhau/Saved/Config/WindowsServer/Engine.ini
```

The editor preserves duplicate keys, ordering, comments, and unrelated lines.
Edits made while the server is running are written to
`/root/mordhau/.manager/pending` and applied by the next managed start or
restart. Direct edits made while stopped are backed up under
`/root/mordhau/.manager/backups`.

Disabling an entry retains its key, value, order, and editability while
serializing it as:

```text
; MORDHAU_MANAGER_DISABLED: Key=Value
```

MORDHAU ignores that comment at the next start. Re-enabling the entry removes
the marker and restores the original `Key=Value` line. Ordinary user-authored
comments are not interpreted as disabled entries. Disabling `RconPassword`
intentionally makes the web RCON stream unavailable after the next server
start until the entry is enabled again.

The generated files remain the source of truth for the installed MORDHAU
version. The repository does not install a static gameplay-configuration
template.

## Mod Management

The Mods panel manages `Mods=<Resource ID>` entries only within:

```text
[/Script/Mordhau.MordhauGameSession]
```

A numeric Resource ID can be added without an API key. Configuring a mod.io
API key also enables MORDHAU mod-page URL and name-ID lookup, current modfile
metadata, and recursive dependency inspection. Dependencies are deduplicated,
validated as public live MORDHAU resources, and inserted before the selected
mod. Existing entries are not duplicated.

Each configured mod displays its recursive mod.io dependency list and whether
each dependency is enabled, disabled, or absent from Game.ini. An enabled mod
shows a warning when a required dependency is disabled or absent. Disabled
mods retain dependency information without producing unresolved-dependency
warnings.

The server refreshes the configured-mod cache every 60 minutes by default.
The interval can be set from 1 to 10,080 whole minutes and is shared by every
administrator. The server performs one metadata/dependency refresh regardless
of how many browsers are connected. Concurrent manual requests join the same
in-progress refresh instead of starting additional mod.io requests.

After a successful refresh, the full interval starts again from that success
time. A failed attempt retains the previous successful timestamp and uses a
separate retry delay capped at five minutes. The page displays the last
successful refresh and next refresh or retry as absolute date/time values
formatted in each browser's locale and time zone.

Completed refreshes and interval changes increment a shared revision sent over
the existing authenticated event stream. Connected administrator pages then
read the server cache and update without another mod.io lookup. The interval
is stored in `/root/mordhau/.manager/mod-refresh.json` with mode `0600`.

The API key and API path are stored in
`/root/mordhau/.manager/modio.json` with mode `0600`. The key is not returned
to the browser or written to the audit log. Requests are restricted to HTTPS
mod.io API hosts, redirects are disabled, and response size and request time
are bounded.

Disabling a configured mod retains its Resource ID as an inactive INI entry.
Removing a mod deletes only that ID from Game.ini; dependencies are retained
because another configured mod may use them. The game server downloads active
mods during its normal startup process.

## Launch Map and Ports

The dashboard stores an optional initial map and passes it immediately after
`MordhauServer.exe`. An empty value leaves map selection to MORDHAU.

Managed launches use these default ports:

| Purpose | Default | Launch parameter | Protocol |
| --- | ---: | --- | --- |
| Game traffic | 7777 | `-Port=` | UDP |
| RCON | 7778 | `-RconPort=` | TCP |
| Beacon | 15000 | `-BeaconPort=` | UDP |
| Steam query | 27015 | `-QueryPort=` | UDP |

All values must be between 1 and 65535, must be unique, and must differ from
the saved web-service port. Changes apply at the next managed start or
restart. The Steam query listener is used when Steam server advertising is
enabled in MORDHAU configuration.

The selected map and ports are stored with mode `0600` under
`/root/mordhau/.manager`. The web RCON client automatically follows the saved
RCON port.

## OpenRC

Service names:

```text
mordhau-server
mordhau-web
```

Manual and automatic boot modes can be selected in the web manager. Equivalent
commands are:

```sh
rc-update add mordhau-server default
rc-update del mordhau-server default
rc-update add mordhau-web default
rc-update del mordhau-web default
```

Changing a boot mode does not change the current process state.

## Security Model

- The web manager runs as root because it controls Wine processes, OpenRC, and
  root-owned configuration files.
- State files, sessions, generated credentials, the last working RCON
  reconnect credential, pending configuration, lifecycle results, the RCON
  event log, and the web audit log use root-only permissions.
- Session tokens are stored as SHA-256 digests.
- Login attempts are rate-limited.
- CSRF tokens and same-site HTTP-only cookies protect authenticated changes.
- Browser requests explicitly marked as cross-site by Fetch Metadata are
  rejected without comparing the public URL to an internally rewritten Host
  header.
- Direct requests use the TCP peer and ignore all forwarding headers.
- Forwarded client addresses are accepted only from explicitly configured
  trusted proxy prefixes. A trusted request requires one structurally valid,
  single-address `X-Forwarded-For` value; invalid forms are rejected before
  authentication.
- Canonical client and TCP peer addresses are retained separately in request
  context and root-only audit records.
- The most specific CIDR block wins; inclusive IPv4 ranges are evaluated as
  exact minimal CIDR blocks. Deny wins an equal-prefix tie except for the
  active emergency exact-address allow.
- RCON connects to `127.0.0.1` from the web manager.
- Unicode message files and their spool directory are root-only. RCON carries
  only a numeric token; the bridge constructs a fixed filename prefix and
  extension and does not accept a path from the command.
- The Unicode Bridge command still requires authenticated RCON. An RCON client
  without local access to a corresponding spool file cannot supply message
  text through the token command. The RCON port and credential must remain
  restricted.
- The mod.io API key is never returned through a management endpoint.
- mod.io requests accept only validated HTTPS API hosts and do not follow
  redirects.
- Lifecycle operations accept fixed actions and do not execute user-provided
  shell arguments.

The built-in listener serves HTTP. Use a trusted network or a TLS reverse
proxy, and configure network access rules before exposing the web port.
Restrict external access to the MORDHAU RCON port with the host firewall.

## Testing

Repository tests:

```sh
cd src/web
go test ./...
go vet ./...
```

Shell validation:

```sh
sh -n src/mordhau-server-alpine-linux.sh
sh -n src/templates/server.sh
sh -n src/templates/webserver.sh
sh -n src/templates/openrc/mordhau-server
sh -n src/templates/openrc/mordhau-web
sh -n src/unicode-bridge/install.sh
sh -n src/unicode-bridge/build-windows-server.sh
sh -n src/tests/test-unicode-bridge-install.sh
./src/tests/test-unicode-bridge-install.sh
```

Installed-service checks:

```sh
rc-service mordhau-server status
rc-service mordhau-web status
```

The Go tests cover random-password constraints, INI preservation, CIDR
precedence, inclusive IPv4 range normalization, exact boundary matching,
range/CIDR precedence, resolved-client emergency access, default-empty trusted
proxy configuration, direct-header spoofing resistance, strict single-value
forwarded IPv4/IPv6 parsing, proxy-aware access control, audit and login-limit
attribution, network-rule comment normalization and backward-compatible JSON,
audit-log permissions and secret exclusion, enabled/disabled INI entry round
trips, RCON credential fallback order, idle keepalive framing, zero-byte
versus partial-packet timeout handling, transport-status filtering, packet
framing, Korean legacy decoding, current all-broadcast subscription syntax
and response filtering, UTF-8 chat log parsing, partial writes, log rotation,
lossy RCON chat suppression,
ASCII-only Unicode token commands, UTF-8 message staging, spool permissions and
stale-file cleanup, bridge acknowledgements, input validation, start-map
validation, server-port parsing and collision checks, mod.io URL and API-path
validation, dependency ordering, scoped mod-entry mutation, shared-cache
deduplication under concurrent clients, successful-refresh interval resets,
failure retry behavior, lifecycle-result persistence, interrupted-operation
recovery, multilingual RCON history persistence, and truncated-log recovery.
The shell integration test covers PAK installation, active and staged Game.ini
registration, existing server-actor preservation, backup creation, and
idempotent reinstallation. Static asset tests verify the default-light theme
initializer, persistent theme, server-managed mod-refresh controls,
browser-time-zone timestamps, initial RCON history loading, the Live RCON
message form, mobile viewport metadata, touch targets, input sizing, safe-area
handling, narrow-screen control reflow, and mobile visibility of server and
account status.

## Update and Rollback

Run the installer from a newer release to update repository-managed scripts,
services, the Unicode Bridge, and the Go web manager. The bundled PAK replaces
the repository-managed bridge version, and the required server-actor entry is
added without duplicating existing entries. Existing accounts, access rules,
generated credentials, INI files, backups, logs, language selection, initial
map, server ports, trusted proxy settings, mod.io settings, mod refresh
interval, latest lifecycle result, RCON event history, and boot modes are
preserved.

To roll back management code:

1. Download the required earlier repository release.
2. Stop `mordhau-web` and `mordhau-server`.
3. Run that release's installer.
4. Restore an INI backup from `/root/mordhau/.manager/backups` when a
   configuration rollback is also required.

Removing the OpenRC boot registrations:

```sh
rc-update del mordhau-server default
rc-update del mordhau-web default
```

MORDHAU files and manager state are not removed by those commands.

## License

Repository-authored source is licensed under the MIT License. See `LICENSE`.
This license does not grant rights to MORDHAU, SteamCMD, Wine, Unreal Engine,
or other third-party software.

## Credits

- MORDHAU and Triternion: https://mordhau.com/
- MORDHAU Dedicated Server on Steam: https://store.steampowered.com/app/629800/MORDHAU_Dedicated_Server/
- Unreal Engine and Epic Games: https://www.unrealengine.com/
- SteamCMD and Valve: https://developer.valvesoftware.com/wiki/SteamCMD
- mod.io: https://mod.io/
- Wine: https://www.winehq.org/
- Author: itinfra7 (GitHub: https://github.com/itinfra7)
