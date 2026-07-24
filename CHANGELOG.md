# Changelog

All notable changes to this repository are documented in this file.

## [1.7.0] - 2026-07-24

### Added

- Optional single-line comments for individual network access rules.
- UTF-8 comment validation with a 160-character limit and control-character
  rejection.
- Desktop and mobile comment fields for creating and editing access rules.
- Regression tests for multilingual comments, limits, persistent JSON
  compatibility, frontend binding, and responsive layout.

### Changed

- Preserve an existing comment when an older API client edits a rule without
  sending the optional `comment` field.
- Record comment presence, change state, and character count in audit events
  without recording comment text.

## [1.6.1] - 2026-07-24

### Fixed

- Show single-address, CIDR, and inclusive IPv4-range examples together in
  the Network access rule placeholder.

## [1.6.0] - 2026-07-24

### Added

- Inclusive IPv4 access rules using either `start-end` or `start~end` input.
- Exact minimal-CIDR decomposition for arbitrary IPv4 ranges.
- Regression tests for both separators, canonical storage, equal endpoints,
  the entire IPv4 space, malformed and reversed ranges, exact boundaries, and
  range/CIDR precedence.

### Changed

- Normalize accepted IPv4 ranges to canonical `start-end` form in persistent
  policy state and audit records.
- Explain IPv4 range syntax and precedence directly in the Network access
  panel.

## [1.5.0] - 2026-07-24

### Added

- Responsive dashboard and login layouts for narrow mobile viewports.
- Safe-area handling for notched displays and standalone browser windows.
- Mobile regression tests for viewport metadata, touch targets, input sizing,
  status visibility, narrow-screen grids, text wrapping, and safe-area rules.

### Changed

- Keep server status and the authenticated account visible in a compact
  two-row mobile header.
- Use 44-pixel touch targets and 16-pixel mobile form controls to improve
  touch operation and prevent automatic input zoom on mobile Safari.
- Reflow lifecycle controls, service settings, INI entries, account rows,
  access rules, mod controls, dependencies, and Live RCON lines at phone
  widths.
- Wrap long operation output, RCON text, dependency identities, mod summaries,
  status text, and notifications without creating page-level horizontal
  overflow.
- Disable mobile autocapitalization and spell checking for usernames, CIDR
  rules, INI fields, map names, URLs, and mod references.

## [1.4.0] - 2026-07-24

### Added

- Root-only server-wide mod refresh interval persistence with a 60-minute
  default and a range of 1 to 10,080 whole minutes.
- A single server-side mod metadata/dependency cache shared by every
  authenticated administrator.
- Shared mod-cache revisions in the authenticated event stream so connected
  pages update after background, manual, and configuration-driven refreshes.
- Last successful refresh and next refresh/retry timestamps formatted in each
  browser's locale and time zone.
- Root-only persistence for the latest lifecycle action, requester, result,
  timestamps, and command output.
- An append-only root-only JSON Lines log for Live RCON events, plus an
  authenticated endpoint that loads the latest 400 events for later sessions.
- Tests for concurrent-client request collapsing, cache reuse, success-based
  interval resets, failure retries, lifecycle restart recovery, multilingual
  RCON history reload, truncated-log recovery, and timestamp UI integration.

### Changed

- Move automatic refresh scheduling from each browser to one server process,
  preventing administrator count from multiplying mod.io API requests.
- Recalculate the full automatic interval only after a successful refresh.
- Preserve the prior successful timestamp after a failed attempt and use a
  separate retry delay capped at five minutes.
- Store the selected interval in
  `/root/mordhau/.manager/mod-refresh.json` instead of browser local storage.
- Clear the obsolete browser-local interval preference when the updated page
  loads.
- Restore the latest lifecycle result and the retained Live RCON window when
  the web manager or an administrator session starts.
- Follow an already-running metadata lookup with one new refresh when mod
  configuration or mod.io settings change, so the shared cache reflects the
  committed configuration.

### Fixed

- Do not report an acknowledged Unicode Bridge RCON command as failed when the
  peer closes immediately before the final connection deadline is cleared.

## [1.3.1] - 2026-07-24

### Added

- Configurable Mods-page metadata auto-refresh with a 60-minute default and a
  validated range of 1 to 10,080 whole minutes.
- Browser-local interval persistence and a visible summary of the active
  refresh setting.

### Changed

- Schedule each automatic refresh only after the preceding mod.io lookup has
  completed to prevent overlapping recursive dependency requests.
- Defer a refresh that becomes due while the page is hidden until it becomes
  visible again.
- Reset the next automatic refresh after manual refreshes and mod
  configuration changes.

## [1.3.0] - 2026-07-24

### Added

- Default-light web styling with a persistent light/dark theme toggle in the
  dashboard header.
- Recursive dependency lists for every configured mod with enabled, disabled,
  and not-configured status indicators.
- Unresolved-dependency warnings for enabled mods when a required Resource ID
  is disabled or absent from Game.ini.
- Tests for dependency-warning suppression on disabled mods, non-null
  dependency arrays, theme assets, and Live RCON message markup.

### Changed

- Rename the Live RCON outbound-message label to `Send Message`.
- Remove the multilingual placeholder from the outbound-message input.
- Suppress unresolved-dependency warnings for disabled target mods while
  retaining their dependency details.

## [1.2.0] - 2026-07-24

### Added

- Bundled MORDHAU Unicode Bridge with its editable Blueprint source, cooked
  WindowsServer PAK, integrity manifest, rebuild tooling, and standalone
  installer.
- Automatic Unicode Bridge installation and update from the main Alpine Linux
  installer.
- Idempotent active and staged Game.ini server-actor registration with
  configuration backups and preservation of unrelated server actors.
- Authenticated web endpoint and Live RCON form for outbound multilingual
  server messages.
- Root-only transient UTF-8 message staging, random 24-digit ASCII token
  transport over RCON, reflected per-player `ClientReceiveMessage` reliable
  RPC delivery, and bridge acknowledgement after the controller loop
  completes.
- Exact-pattern cleanup for stale Unicode Bridge spool files while preserving
  unrelated `Saved/PlayerFiles` content.
- Per-account Unicode-message audit events containing character and UTF-8 byte
  counts without storing message text.
- Unit tests for token commands, UTF-8 staging, spool permissions and cleanup,
  input validation, RCON authentication, and bridge acknowledgement.
- Shell integration tests for PAK installation, INI registration, backups, and
  idempotent updates.

### Security

- Keep the bridge server-only by using a nonreplicated actor with client
  network loading disabled and by excluding it from Game.ini `Mods=` entries.
- Keep message content out of the RCON parser, constrain bridge filenames to a
  fixed prefix and extension with numeric tokens, and use root-only spool
  permissions.
- Require an authenticated RCON connection for bridge token commands and
  retain the web manager's session, CSRF, and network-access controls.

## [1.1.2] - 2026-07-23

### Fixed

- Follow MORDHAU's UTF-8 game log for player-chat events so non-ASCII chat
  remains intact when the server's RCON broadcast has already replaced it
  with question marks.
- Suppress the corresponding lossy RCON chat record while retaining all other
  subscribed RCON event channels.
- Follow log truncation and launch-time rotation without replaying historical
  chat when the web manager starts.

### Changed

- Document the RCON and UTF-8 chat-log event sources.
- Add tests for Unicode chat parsing, partial writes, log rotation, and lossy
  RCON chat suppression.

## [1.1.1] - 2026-07-23

### Fixed

- Use the current `listen allon` RCON syntax for subscribing to every
  broadcast channel.
- Require the server's all-broadcast success response before reporting the
  RCON stream as connected.
- Suppress the broadcast-option help response from the live RCON event view.
- Add tests for the subscription packet, acknowledgement, and output filter.

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
- Source RCON framing, authentication, automatic broadcast subscription,
  reconnection, live-credential-change fallback, and multilingual output
  decoding.
- Unit tests for passwords, INI preservation, network-rule precedence, and
  multilingual RCON handling.

### Fixed

- Empty access-rule lists now remain JSON arrays, preventing a refresh-time
  management-page error when no explicit network rules are configured.
