# MORDHAU Server Alpine Linux v1.1.1

This release extends the Alpine Linux management stack for the Windows
MORDHAU Dedicated Server with mod.io-assisted configuration and managed launch
settings.

## Included

- Windows SteamCMD installation and App ID `629800` validation
- Dedicated Wine prefix and generated WindowsServer configuration
- POSIX shell start, stop, restart, update, and status control
- OpenRC services with manual or automatic boot modes
- Log archival based on the source `Mordhau.log` modification time
- Authenticated Go web manager bound to IPv4 `0.0.0.0`
- Live CPU, memory, swap, and server-filesystem metrics
- Game.ini and Engine.ini structured editing with running-server staging
- Reversible per-entry enable/disable controls that preserve keys, values,
  ordering, and ordinary comments
- Persistent launch-language and initial-map selection
- Managed game, RCON, beacon, and query launch ports
- Optional mod.io API-key validation, MORDHAU mod lookup, metadata, and
  recursive dependency inspection
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
- RCON reconnection across direct Game.ini password changes while the game is
  running
- Automatic web RCON use of the saved RCON launch port
- Server acknowledgement required before the web manager reports the
  all-broadcast subscription as active
- Broadcast-option help responses omitted from the live RCON event view

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
