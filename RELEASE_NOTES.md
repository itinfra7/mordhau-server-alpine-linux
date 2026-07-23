# MORDHAU Server Alpine Linux v1.0.0

This release provides an Alpine Linux installer and management stack for the
Windows MORDHAU Dedicated Server.

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
- Launch-language selection
- IPv4 and IPv6 address/CIDR access policies
- Null-safe rendering when an access policy has no explicit network rules
- Account management, Argon2id password hashing, CSRF protection, and login
  throttling
- Login and authenticated request validation compatible with NAT and
  reverse-proxy Host rewriting
- Root-only per-account JSON Lines logging for web access, authentication,
  server actions, and administrative changes
- RCON `listen all` event streaming with multilingual decoding
- RCON reconnection across direct Game.ini credential changes while the game
  is running

## Installation

Extract the release archive and run:

```sh
chmod +x mordhau-server-alpine-linux.sh
./mordhau-server-alpine-linux.sh
```

Both services remain in manual mode and stopped by default. See `README.md`
for installer options, service controls, security guidance, testing, and
rollback instructions.

## Integrity

Verify the release archive from its directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.
