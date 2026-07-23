# Changelog

All notable changes to this repository are documented in this file.

## [1.1.0] - 2026-07-23

### Added

- Optional mod.io API-key and API-path validation with root-only local
  persistence.
- MORDHAU mod lookup by URL, name ID, or Resource ID.
- Current modfile metadata and bounded recursive dependency inspection.
- Dependency-first, deduplicated Game.ini `Mods=<Resource ID>` management
  scoped to the MORDHAU game-session section.
- Configured-mod enable, disable, and remove controls with running-server
  staging and configuration backups.
- Persistent initial-map selection passed immediately after
  `MordhauServer.exe`.
- Managed game, RCON, beacon, and query launch ports with range, uniqueness,
  and web-port collision checks.
- Automatic web RCON use of the saved RCON launch port.
- Audit events for mod.io settings, mod configuration, initial-map changes,
  and dedicated-server port changes.
- Unit tests for mod.io URL and API-path validation, mod-entry scope and
  ordering, start-map validation, and server-port persistence.

### Changed

- Managed server starts now pass explicit game, RCON, beacon, and query port
  arguments.
- The installer initializes default server-port state while preserving
  existing launch and mod.io settings during updates.
- Web asset versioning and release documentation now identify version 1.1.0.

## [1.0.0] - 2026-07-23

### Added

- Alpine Linux package and environment installation.
- Windows SteamCMD bootstrap and Windows MORDHAU Dedicated Server App ID
  `629800` installation.
- Five-second option-free initial server run for generated configuration.
- Generated RCON credential initialization using the current
  `MordhauGameSession` section.
- Steam validation before managed starts and restarts.
- POSIX shell start, stop, restart, update, and status control.
- Modification-time-based MORDHAU log archiving.
- OpenRC services for the game server and management web server.
- Automatic/manual boot-mode controls.
- Go web manager with authenticated sessions, account management, CSRF
  protection, and login throttling.
- Fetch Metadata request validation compatible with NAT and reverse-proxy Host
  rewriting.
- Root-only JSON Lines web audit logging for HTTP access, authentication,
  administrative changes, direct client IP addresses, and responsible
  accounts.
- Audit-log exclusion of passwords, session and CSRF tokens, RCON
  credentials, request bodies, configuration values, and revisions.
- IPv4/IPv6 address and CIDR access policies with temporary emergency access.
- Live CPU, memory, swap, and server-filesystem metrics.
- Revision-checked Game.ini and Engine.ini structured editing and staging.
- Reversible per-entry INI enable/disable controls using an explicit
  server-ignored comment marker.
- Launch-language selection.
- Source RCON framing, authentication, automatic `listen all`, reconnection,
  live-credential-change fallback, and multilingual output decoding.
- Unit tests for passwords, INI preservation, network-rule precedence, and
  multilingual RCON handling.

### Fixed

- Empty access-rule lists now remain JSON arrays, preventing a refresh-time
  management-page error when no explicit network rules are configured.
