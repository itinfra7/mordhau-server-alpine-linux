# MORDHAU Server Alpine Linux v1.4.0

This release moves mod metadata scheduling and caching to the server so every
administrator shares one interval and one set of mod.io API requests.

## Included

- Windows SteamCMD installation and App ID `629800` validation
- Dedicated Wine prefix and generated WindowsServer configuration
- Editable MORDHAU Unicode Bridge Blueprint source and cooked WindowsServer PAK
- Verified, idempotent bridge installation and active/staged Game.ini
  server-actor registration
- POSIX shell start, stop, restart, update, and status control
- OpenRC services with manual or automatic boot modes
- Log archival based on the source `Mordhau.log` modification time
- Authenticated Go web manager bound to IPv4 `0.0.0.0`
- Default-light responsive interface with a persistent light/dark toggle
- Live CPU, memory, swap, and server-filesystem metrics
- Persistent latest lifecycle action, requester, result, timestamps, and
  command output
- Game.ini and Engine.ini structured editing with running-server staging
- Reversible per-entry enable/disable controls that preserve keys, values,
  ordering, and ordinary comments
- Persistent launch-language and initial-map selection
- Korean CP949, Russian CP1251, Simplified Chinese CP936, and Traditional
  Chinese CP950 RCON decoding paths
- UTF-8 player-chat following across partial log writes and managed log
  rotation
- Root-only transient UTF-8 message files with exact-pattern startup cleanup
- Random 24-digit ASCII token transport for outbound messages
- Server-side UTF-8 file loading through a nonreplicated actor and MORDHAU's
  reflected per-player `ClientReceiveMessage` reliable client RPC
- Bridge acknowledgement required before the web manager reports a Unicode
  message as sent
- No bridge entry in Game.ini `Mods=` and no client plugin download
- Lossy direct RCON chat suppression while all other subscribed RCON event
  channels remain active
- Managed game, RCON, beacon, and query launch ports
- Optional mod.io API-key validation, MORDHAU mod lookup, metadata, and
  recursive dependency inspection
- Per-mod dependency lists with enabled, disabled, and not-configured status
- Unresolved-dependency warnings limited to enabled target mods
- Server-wide mod metadata auto-refresh from 1 to 10,080 minutes with a
  60-minute default and root-only interval persistence
- One metadata/dependency cache and one in-progress refresh shared by all
  authenticated administrator sessions
- Authenticated event-stream revision updates that refresh every connected
  Mods page from the shared cache without additional mod.io lookups
- Browser-locale and browser-time-zone display of the last successful refresh
  and next refresh or retry
- Full interval reset only after successful refreshes, with failed attempts
  retaining the previous success time and using a capped retry delay
- Scoped Game.ini `Mods=<Resource ID>` add, enable, disable, and remove actions
- IPv4 and IPv6 address/CIDR access policies
- Null-safe rendering for empty account, access-rule, mod, and dependency data
- Account management, Argon2id password hashing, CSRF protection, and login
  throttling
- Login and authenticated request validation compatible with NAT and
  reverse-proxy Host rewriting
- Root-only per-account JSON Lines logging for web access, authentication,
  server actions, port and map changes, mod configuration, and administrative
  changes
- RCON `listen allon` event streaming with multilingual decoding
- Append-only root-only JSON Lines persistence for Live RCON events
- Initial loading of the latest 400 RCON events for administrators who connect
  after the events were received or after a web-service restart
- RCON reconnection across direct Game.ini password changes while the game is
  running
- Automatic web RCON use of the saved RCON launch port
- Server acknowledgement required before the web manager reports the
  all-broadcast subscription as active
- Broadcast-option help responses omitted from the live RCON event view
- Per-account Unicode-message audit events that exclude message text
- Live RCON `Send Message` control without input-language placeholder text
- Bridge rebuild tooling, cooked-artifact checksums, and installation
  integration tests

## Installation

Extract the release archive and run:

```sh
chmod +x mordhau-server-alpine-linux.sh
./mordhau-server-alpine-linux.sh
```

Both services remain in manual mode and stopped by default. See `README.md`
for installer options, service controls, mod.io setup, launch settings,
security guidance, testing, and rollback instructions.

## Integrity

Verify the release archive from its directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.
