# Changelog

All notable changes to this repository are documented in this file.

## [Unreleased]

## [2.4.0] - 2026-07-29

### Added

- Add server-side hourly detection of stable project GitHub Releases and
  public MORDHAU Dedicated Server Steam builds, with shared top-of-page
  notifications and authenticated manual checks.
- Add a checksum-verified detached management updater that validates the
  selected release, safely extracts its archive, runs the bundled installer,
  retains progress across web-service replacement, and restores previously
  running services after installer failure.
- Add server-wide, default-off automatic update settings for management
  releases and dedicated-server builds. A detected update uses persistent
  10-, 5-, 4-, 3-, 2-, and 1-minute English in-game notices before a managed
  restart, or updates immediately when the game server is stopped.
- Add semantic management-version detection for fresh installs, upgrades,
  same-version validation, and explicitly authorized `--allow-downgrade`
  transitions.

### Fixed

- Prevent management updates, Steam build checks, automatic updates, and web
  lifecycle actions from overlapping incompatible operations.
- Reconcile an interrupted detached update on the next web-manager start and
  record the worker result before releasing its process lock.
- Base hourly external checks on the persisted server-side check time and
  avoid repeated availability or failure audit records for an unchanged
  result.
- Accept a newly written, structurally valid public-build response when
  SteamCMD returns a nonzero Wine process status, while still rejecting
  incomplete or stale console output.
- Preserve a complete existing SteamCMD installation during manager upgrades
  instead of replacing it with the older bootstrap archive before every
  self-update.
- Synchronize the dashboard RCON port with `RconPort` in `Game.ini` when
  dedicated-server ports are saved and when an existing installation starts
  the updated web manager.
- Stage the synchronized INI value while the game server is running and update
  the active configuration while it is stopped, preventing configuration
  display and managed launch settings from diverging.
- Stop an OpenRC-managed game server through OpenRC during an in-place upgrade
  so its service state is cleared and the previously running server is
  reliably started after validation.

### Security

- Restrict management updates to the fixed project GitHub repository,
  canonical stable semantic-version tags, and required release assets.
  Enforce response, download, archive-entry, file-count, and expanded-size
  limits; reject traversal, links, devices, duplicate paths, and unexpected
  top-level paths; and verify the release archive against `SHA256SUMS`.
- Require authentication and CSRF validation for manual checks, automatic
  settings, and update requests. Audit the requesting account and canonical
  client address without exposing credentials.
- Exclude the detached updater from installer process shutdown, serialize
  update workers with a root-only lock, atomically replace the running web
  binary, and reject lifecycle changes while a management update is active.
- Reject implicit management-code downgrades and malformed installed-version
  state, and atomically record a new version only after validation and
  requested service starts succeed.
- Preserve intentionally disabled `RconPort` entries and disabled game-session
  sections while updating their stored value, and serialize dashboard port
  changes with configuration and lifecycle operations.
- Restore the prior server-port state if the corresponding INI synchronization
  cannot be persisted.

### Validation

- Verify stable-release and required-asset validation, response limits,
  checksum parsing, traversal and link rejection, installer-version matching,
  detached state transitions, update-worker locking, interrupted-state
  reconciliation, installer failure recovery, CSRF enforcement, and
  lifecycle conflict rejection.
- Verify Steam manifest and public-branch build parsing, persistent shared
  status, complete metadata returned with a nonzero SteamCMD status,
  lifecycle-busy handling, default-off automatic settings, persisted countdown
  state, final restart requests, and detached manager-update dispatch.
- Verify synchronization of duplicate active values, disabled entries,
  disabled sections, missing values, CRLF input, unrelated INI content, and
  invalid port rejection.
- Verify OpenRC-managed and manually launched game servers use their respective
  shutdown paths during an in-place upgrade.
- Verify complete and incomplete SteamCMD bootstrap installations are
  distinguished before an installer refresh.
- Verify semantic version validation and ordering, upgrade/reinstall/downgrade
  classification, downgrade refusal, malformed-state refusal, and atomic
  mode-`0600` version recording.

## [2.3.2] - 2026-07-29

### Fixed

- Resolve MORDHAU App ID `629800` metadata before the first SteamCMD
  `app_update`, explicitly select the Windows 64-bit depot, and retry only the
  transient `Missing configuration` result with bounded delays.
- Require an exact MORDHAU executable command and Wine process identity when
  detecting the game server, preventing unrelated shell or diagnostic command
  lines from being reported as a running server.
- Identify an unmanaged web-manager process by its exact `/proc` executable
  target instead of a command-line substring.

### Validation

- Verify the SteamCMD command order and bounded metadata-only retry behavior,
  including immediate failure for unrelated update errors.
- Verify exact game-process matching for the launcher and shipping executable,
  plus rejection of shell, unrelated Wine, and alternate-path false
  positives.
- Verify an empty SteamCMD cache and isolated Wine prefix reach the App ID
  `629800` Windows depot download, and complete an Alpine installation through
  SteamCMD validation, native bridge installation, initial configuration,
  Unicode Bridge installation, Go tests, web-manager build, and OpenRC setup.

## [2.3.1] - 2026-07-29

### Added

- Add lossless, single-threaded XZ `-9e` compression for finalized
  `Mordhau_<timestamp>.log` archives, including an explicit `compress-logs`
  maintenance command and automatic background compression after each managed
  log rotation.
- Add authenticated raw game-log search and JSON Lines export across the
  active log, uncompressed archives, and `.log.xz` archives without extracting
  compressed files to disk.

### Changed

- Preserve each archive's final-use modification time and verify both the XZ
  stream and restored SHA-256 before replacing an uncompressed archive.
- Read `.log.xz` archives directly when rebuilding player history, avoid
  duplicate history import during `.log` to `.log.xz` conversion, and apply
  configured game-log retention to both formats.
- Run archive compression with one XZ thread and idle CPU and I/O priority so
  game-server startup does not wait for compression.

### Security

- Keep compressed archives at mode `0600`, reject symbolic links and
  out-of-scope archive paths, reject mismatched archive collisions, safely
  reconcile byte-identical duplicate sources, and retain the uncompressed
  source whenever compression or verification fails.
- Serialize authenticated game-log searches and resolve XZ input paths only
  from the server-owned archive directory.

### Validation

- Verify lossless shell compression, restored content, archive permissions,
  interrupted-finalization reconciliation, mismatched-collision preservation,
  idempotent maintenance runs, streamed XZ reads without extracted files,
  compressed player-history import, duplicate-import prevention, raw/XZ
  game-log search, and XZ archive retention.

## [2.3.0] - 2026-07-29

### Added

- Add desired-state-aware crash detection and automatic game-server recovery
  with a configurable retry budget, bounded exponential backoff, retained
  process diagnostics, and an authenticated manual retry control.
- Add a Monitoring panel with server-side one-minute CPU, memory, swap, disk,
  and connected-player history for 24-hour and seven-day charts.
- Add bounded audit and Server Events search, JSON Lines export, configurable
  log rotation, archived game-log retention, disk-threshold monitoring, and
  optional HTTPS webhook alerts for crashes, exhausted recovery, disk usage,
  and mod-refresh failures.
- Make the dashboard Players card open a live connected-player directory with
  PlayFab ID, nickname, country flag, account level, platform, and ping, with
  direct navigation to the corresponding Player Profile.
- Add Steam, Epic, and Unknown platform badges, exact live ping sampling,
  retained per-player connection timelines, reasoned kick controls, and
  Unicode administrator warnings.
- Add permanent or timed mute and ban controls with retained reason,
  responsible administrator, expiry time, and automatic server-side reversal.
- Add a visual MapRotation editor backed by the installed-content catalog,
  with add, reorder, enable, disable, remove, revision-check, and staged-save
  behavior.
- Add modfile publication metadata and a dependency-aware removal planner that
  can remove exclusively required dependencies while retaining shared or
  unresolved dependencies.
- Add active-mod update restart policies for a ten-minute countdown, a
  continuously empty server, or a selected server-local scheduled time.

### Changed

- Distinguish intentional stops from unexpected game-process exits through a
  root-only desired-state file. Automatic recovery reuses the last validated
  installation without running SteamCMD or applying staged configuration.
- Require the Runtime bridge to report zero PlayerControllers continuously for
  30 seconds before the empty-server mod restart policy proceeds.
- Preserve configured MapRotation entries that are temporarily absent from the
  installed-content catalog and retain duplicate map names that validly belong
  to multiple game modes.
- Sample system metrics once per minute in the web process and distribute the
  same snapshot and retained history to every authenticated browser.

### Security

- Keep recovery state, metrics history, monitoring policy, moderation leases,
  and player session history in root-only files.
- Require authentication and CSRF validation for recovery, monitoring,
  MapRotation, mod-removal, moderation, kick, and warning changes.
- Restrict webhook delivery to HTTPS with TLS 1.2 or newer, no redirects,
  bounded requests, pinned DNS results, and rejection of private, loopback,
  link-local, multicast, unspecified, documentation, benchmark, carrier-grade
  NAT, and reserved destinations.
- Never return the saved webhook URL or mod.io API key to the browser, and keep
  moderation reason text and webhook destinations out of audit details.

### Validation

- Verify crash detection, intentional-stop exclusion, retry-window pruning,
  exponential backoff, retry exhaustion, manual recovery, and persisted
  desired/launch state.
- Verify one-minute metric retention and compaction, 24-hour and seven-day
  views, log rotation and retention, bounded search/export, webhook address
  policy, alert cooldowns, and secret-safe status reporting.
- Verify timed moderation expiry, rollback on failed RCON confirmation,
  session timelines, live player merging, Steam/Epic normalization, and exact
  ping transport through the native Runtime bridge.
- Verify MapRotation preservation, ordering, duplicate identity rejection,
  disabled state, dynamic map catalog integration, and multi-mode map
  retention.
- Verify recursive dependency-removal plans, shared dependency retention,
  revision conflict handling, modfile metadata, all three restart policies,
  the empty-server grace interval, and persisted schedule migration.
- Pass `go test ./...`, `go vet ./...`, `go test -race ./...`, JavaScript
  syntax validation, POSIX shell syntax validation, deterministic Runtime
  bridge builds, Unicode Bridge installation tests, and whitespace checks.

## [2.2.2] - 2026-07-28

### Added

- Show the currently loaded map and game-mode class on the dashboard from the
  authoritative game log and live Runtime bridge.
- Add a live map-change dialog grouped by installed game mode. The
  server-cached catalog includes shipped maps, enabled mod.io content, and
  active CustomPaks only when a safe map/game-mode pairing can be established.
- Validate every live map selection against that catalog before sending the
  fixed `changelevel <map>` RCON command, and retain the requester, selection,
  result, and response in the existing event and audit records.
- Install the checksum-pinned `repak` 0.2.3 helper and its upstream licenses
  for bounded, read-only Unreal PAK index and map metadata inspection.
- Persist each player's last observed general MORDHAU account level by reading
  server-side inventory XP and converting it through the supported MORDHAU
  build's native XP-to-level function, independently of replicated and ranked
  fields.
- Show that level in Player Profile and as a distinct badge between the
  country flag and nickname in Known Players.
- Retain a verified SteamID64 from the live `PlayFabPlayer` identity and show
  a Steam icon and profile link for Steam-backed player records.

### Changed

- Discover unprefixed official maps from their packaged default GameMode
  instead of requiring a mode-name prefix. This includes
  `LiteMordhauTestLevel` under Deathmatch while omitting internal
  initialization, main-menu, and base GameMode destinations.
- Retain at most one `Waiting to start` and one `Leaving map` server event
  during each interval with no connected players, then suppress both idle
  states after the first `Leaving map`.
- Reopen the idle match-state event window when a player authenticates and
  again when the last connected player leaves, while preserving every match
  state whenever at least one player is connected.

### Validation

- Verify the installed server catalog against shipped content and enabled
  mod.io PAKs, including the Dread 2 map/game-mode pairing.
- Verify strict catalog pair selection, fixed RCON map command construction,
  requester auditing, current map/game-mode parsing, ambiguous packaged
  game-mode rejection, unprefixed `LiteMordhauTestLevel` discovery, internal
  destination rejection, and map-catalog UI controls.
- Verify native account-progress validation with the known XP-to-level pair
  `90435` to level `38`, rejection of replicated, Duel, and Teamfight rank
  fields, latest-value persistence, and player list/profile rendering.
- Verify strict 17-digit SteamID64 validation, persistence, safe new-tab
  profile links, and rejection of malformed platform identities.
- Verify empty-server duplicate suppression, preservation across game-log
  rotation and managed server restarts, unrestricted active-player events, and
  a fresh empty-server window after the final player disconnects.

## [2.2.1] - 2026-07-27

### Fixed

- Accept new persistent player nicknames only when a login request has been
  correlated with successful player authentication.
- Prevent mutable chat and disconnect observations, including temporary
  server-side PlayerState edits, from adding or reprioritizing nickname
  history.

### Validation

- Verify that an observed runtime-only name is excluded from history while a
  Unicode nickname supplied by a later authenticated login is retained and
  becomes the latest nickname.

## [2.2.0] - 2026-07-27

### Added

- Add a Players tab between Runtime and Configuration with a most-recent-first
  PlayFab ID and nickname directory, historical nickname search, last
  connection, accumulated server time, current-session state, and retained
  nickname and canonical IP history.
- Add verified mute/unmute and ban/unban toggles plus persistent player
  comments attributed to the responsible web account.
- Add bidirectional Players ordering by last connection or accumulated server
  time, country flags for each player's latest address, and approximate
  country, region, and city labels for retained addresses.
- Add automatic local DB-IP City Lite download, full MMDB verification,
  atomic monthly updates, and configurable IP/CIDR exclusions for internal
  networks.
- Seed missing Engine.ini `IpNetDriver` values with
  `NetServerMaxTickRate=60` and `ConnectionTimeout=10.0` during installer
  initialization while preserving existing and intentionally disabled values.
- Add an authenticated CustomPaks panel immediately after Mods with manual PAK
  inventory, drag-and-drop and file-picker upload, upload progress, and
  responsive controls.
- Add next-start activation, deactivation, and deletion staging for manually
  installed PAK files.
- Make the dashboard brand link return the current page to its top.

### Changed

- Correlate accepted game connections, login requests, authentication, UTF-8
  identity observations, and disconnect records into persistent player
  sessions; import archived logs once and rescan the current log
  idempotently.
- Keep historical nicknames in each player-list search document and include
  accumulated server time and the latest locally resolved country in list
  records.
- Apply staged CustomPaks changes after Steam validation and immediately before
  each managed start or restart.
- Use validated BusyBox-compatible `find` cleanup for installer, bridge-build,
  and integration-test temporary directories.

### Security

- Store player history and comments in a mode-`0600` state file, require
  authenticated CSRF-protected mutations, exclude comment text from audit
  details, and confirm moderation changes against the running server's RCON
  lists before reporting success.
- Keep GeoIP data and configuration root-only; use a fixed HTTPS download
  origin with redirects disabled, compressed and expanded size limits,
  filesystem reserve enforcement, MMDB metadata and tree verification, and
  atomic replacement. Resolve player addresses locally without a per-address
  API request.
- Keep inactive and uploaded PAK files in root-only manager directories, apply
  all mutations under the lifecycle lock, and show project-managed packages as
  protected entries that cannot be deactivated, deleted, or manually replaced.
- Require authenticated, CSRF-protected CustomPaks mutations; validate UTF-8
  `.pak` basenames, reject case-insensitive overwrite conflicts, cap each
  upload at 8 GiB, and retain a 1 GiB filesystem reserve.

### Validation

- Verify Unicode identity and canonical IPv4/IPv6 correlation, session
  deduplication, most-recent ordering, duration accounting, archived-log
  import persistence, empty-array API contracts, attributed comment
  persistence and audit exclusion, and RCON moderation command confirmation.
- Verify GeoIP edition rollover, localized record normalization, private and
  ignored-address exclusion, incompatible database rejection, unavailable
  edition handling, historical-nickname search wiring, both sorting keys and
  directions, attribution, and responsive location presentation.
- Verify manual and project-managed package discovery, protected-package
  deactivation/deletion/replacement rejection, inactive managed-package
  restoration, staged state rendering, activation/deactivation moves,
  deletion, upload limits, duplicate rejection, deletion cancellation,
  lifecycle locking, and idempotent next-launch application.

## [2.1.1] - 2026-07-26

### Added

- Extend Runtime property search to current exported values in addition to
  property names, types, and declaring classes.

### Validation

- Verify the dashboard ships the value-aware Runtime query path and updated
  search guidance.

## [2.1.0] - 2026-07-26

### Added

- Add type-derived Runtime editors for exact Boolean choices, reflected Unreal
  enum values, signed and unsigned integer widths, finite floating-point
  values, names, strings, and structured Unreal text.
- Add native `UEnum` value metadata for enum-backed byte and enum properties.
- Add authenticated controller identity metadata from
  `PlayerState.PlayerNamePrivate` and
  `MordhauPlayerState.PlayFabPlayer.PlayFabId`.
- Add single-open connected-player groups labeled with nickname and PlayFab ID
  and containing that player's PlayerController, PlayerState, and possessed
  Pawn.

### Changed

- Separate silent two-second Runtime value polling from the manual
  `Refresh values` button state so background refreshes do not repeatedly
  disable or animate the button.
- Keep 64-bit integer validation string-based in both browser and Go server to
  avoid precision loss through JavaScript floating-point conversion.

### Fixed

- Exclude Unreal terminal `MAX` and `*_MAX` enum sentinels from selectable
  Runtime values.
- Reject nonzero floating-point input that underflows to zero in either the
  browser or Go validation path.

### Security

- Resolve property metadata in the Go manager before every mutation and reject
  values outside the reflected editor type, enum choices, numeric range, or
  structured-text delimiter rules before forwarding them to the native
  bridge.
- Validate player identity placement, size, encoding, and PlayFab ID
  characters in both sampled status and target responses.
- Keep Unreal `ImportText` verification and rollback as the final authority
  for complex property values.

### Validation

- Verify browser and server metadata for Boolean, enum, integer, float,
  string, and structured property editors.
- Verify malformed Boolean values receive HTTP 400 before reaching the native
  bridge.
- Verify player identity grouping and enum choices against a connected
  dedicated-server client.

## [2.0.0] - 2026-07-26

### Added

- Add a native Windows runtime-reflection bridge for the supported MORDHAU
  Dedicated Server build, loaded through a guarded DXGI proxy under Wine.
- Add an authenticated Runtime panel for the active authority GameMode,
  GameState, PlayerControllers, PlayerStates, and possessed Pawns.
- Enumerate properties from each actual runtime class through its complete
  superclass chain with Unreal type, flags, offset, array index, exported
  value, RepIndex, RepNotify function, lifetime condition, and replication
  scope.
- Add immediate game-thread property changes with expected-value conflict
  detection, Unreal text import verification, failure rollback, and
  `AActor::FlushNetDormancy` plus `AActor::ForceNetUpdate` for
  replication-eligible Actor fields.
- Add a one-second shared PlayerController count collected in the game process
  and distributed through the existing authenticated event stream.
- Add responsive Runtime target, search, declaring-class filter, editable-only
  filter, value editor, and replication-status controls for desktop and mobile
  browsers.
- Add native bridge build validation and Go tests for shared status sampling,
  stale-state rejection, request serialization, cache reuse, target
  validation, and multilingual values.

### Changed

- Build and install the native bridge only when
  `MordhauServer-Win64-Shipping.exe` matches the supported SHA-256 digest.
- Enable the Wine native DXGI override only for that supported executable
  build; unsupported game updates continue with Wine's built-in DXGI and an
  unavailable Runtime panel.
- Cache Runtime status and identical short-lived target views in the Go
  manager so administrator count does not multiply game-process status
  collection.
- Extend the dashboard with the shared live PlayerController count and current
  runtime-bridge state.

### Security

- Restrict native runtime targets to actors rediscovered from the active
  `UWorld` and bind target identifiers to Unreal object indices and serial
  numbers.
- Keep object references, class references, interfaces, delegates, field
  paths, deprecated and editor-only fields, function parameters,
  engine-internal Blueprint frame storage, and unexportable values read-only.
- Keep bridge IPC inside the root-only manager runtime directory and expose no
  additional network listener.
- Require authenticated, CSRF-protected, expected-value-checked web mutations
  and audit the responsible account, canonical client address, target,
  property, and replication scope without recording property values.
- Refuse bridge hook installation when PE metadata or the `UWorld::Tick`
  prologue does not match the supported executable.

### Validation

- Confirm PlayerController count and associated PlayerController, PlayerState,
  and possessed Pawn discovery with a connected client.
- Confirm authoritative readback, client delivery through a `Net`/`OnRep`
  PlayerState string property, and restoration of the original value.

## [1.9.0] - 2026-07-25

### Added

- Add authenticated administrative RCON command execution from the Server
  Events prompt with immediate, retained command and response events.
- Add bounded multi-packet response collection with selected-language legacy
  decoding, explicit no-output and truncation records, and mobile controls.
- Add UTF-8 `Mordhau.log` following for player lifecycle, chat, match-state,
  killfeed, scorefeed, and punishment events with source timestamps.
- Add a terminal-style RCON/SAY selector and shared prompt below the Server
  Events window.
- Publish the full tag-specific changelog as a versioned asset with every
  GitHub Release.
- Add an API-key-gated, default-off automatic restart setting for enabled
  mod.io modfile updates.
- Persist active-mod modfile baselines and pending restart countdowns across
  web-service restarts.
- Announce automatic restarts in-game at 10, 5, 4, 3, 2, and 1 minutes before
  invoking the managed restart action at the ten-minute deadline.

### Changed

- Replace cumulative feature inventories in GitHub Release bodies with the
  exact version section from `CHANGELOG.md`, preceding-Release navigation, and
  direct tag comparison links.
- Replace the permanent `listen allon` RCON subscription with a log-first
  server-event collector that handles missing files, partial writes,
  truncation, replacement, and restart-time player-state reconstruction.
- Open RCON only for bounded administrative commands and acknowledged Unicode
  SAY requests.
- Initialize `bLogChat`, `bLogKillfeed`, and `bLogScore` unless their item or
  containing section is explicitly disabled.
- Default newly created server-wide mod metadata refresh state to five minutes
  while preserving an existing saved interval during settings migration.
- Merge additional active-mod updates into an existing countdown without
  postponing its restart deadline.
- Cancel a pending automatic restart when its setting or API key is removed,
  the game process changes, or another lifecycle action begins.

### Fixed

- Correlate login-request, authentication, chat, and disconnect records by
  player ID so UTF-8 names remain intact in login and logout events even when
  a secondary identity line is lossy.

### Security

- Reject empty, invalid UTF-8, oversized, and control-character-containing
  RCON commands.
- Serialize web-issued RCON commands and bound each response by time, bytes,
  and line count.
- Attribute command success and failure to the authenticated account and
  canonical client address while excluding command arguments from the web
  audit log.

## [1.8.3] - 2026-07-24

### Added

- Add persistent, root-only disabled INI item and section state independent of
  game-owned Game.ini and Engine.ini files.
- Add whole-section disable and enable controls that move every item together,
  retain duplicate-key order, and restore sections removed by game rewrites.
- Add selective import of matching legacy disabled entries from an INI backup.
- Add active and staged state backups, revision coverage, and tests for item,
  section, virtual-section, duplicate-key, legacy-migration, and mod workflows.

### Changed

- Stage and apply disabled-item state together with running-server Game.ini and
  Engine.ini edits.
- Use the same persistent disabled-item model for structured INI editing and
  mod.io `Mods=` management.

### Fixed

- Prevent MORDHAU or Unreal Engine configuration serialization from erasing
  reversibly disabled entries that were previously represented only as INI
  comments.

## [1.8.2] - 2026-07-24

### Changed

- Consolidate installer, service templates, SteamCMD input, Go manager,
  Unicode Bridge, and integration tests under `src/`.
- Keep generated release archives and checksums outside the tracked source
  tree for publication as versioned GitHub Release assets.
- Credit Unreal Engine and Epic Games for the editor and packaging technology
  used to build the server-only Unicode Bridge.

## [1.8.1] - 2026-07-24

### Changed

- Sample CPU, memory, swap, and MORDHAU-filesystem utilization once per minute
  through the shared server-side collector.
- Keep the authenticated event stream responsive for lifecycle, process, RCON,
  and configuration state without repeating OS metric collection per client.

## [1.8.0] - 2026-07-24

### Added

- Add a default-empty, root-only trusted-proxy configuration loaded by the
  web launcher and passed as repeatable `--trusted-proxy` startup options.
- Resolve canonical client and TCP peer addresses into request context without
  rewriting `RemoteAddr`.
- Retain the validated client IP in persisted lifecycle requester state and
  record the direct TCP peer separately as `peer_ip` in web audit records.

### Security

- Ignore `X-Forwarded-For`, `X-Real-IP`, and `Forwarded` for every untrusted
  direct TCP peer.
- Require exactly one single-address `X-Forwarded-For` value from a trusted
  proxy and reject missing, duplicate, chained, malformed, zoned, unspecified,
  or multicast values with HTTP 400 before authentication.
- Apply the validated client address to network access policy, emergency
  access, login throttling, audit attribution, and lifecycle operations.
- Add regression coverage for trusted IPv4/IPv6 proxies, IPv4-mapped
  canonicalization, direct-header spoofing, strict header rejection,
  all-deny behavior, proxy/client audit separation, login throttling, and
  lifecycle requester attribution.

## [1.7.2] - 2026-07-24

### Fixed

- Renew an idle authenticated RCON subscription with the idempotent
  `listen allon` command after a zero-byte 90-second read deadline, preventing
  MORDHAU's later server-side idle close.
- Suppress the keepalive subscription acknowledgement from Live RCON.
- Continue treating a timeout after partial packet data as a real connection
  failure to avoid accepting a desynchronized stream.
- Audit EOF connection closures for retained transport diagnostics.
- Add regression coverage for keepalive framing, acknowledgement suppression,
  and zero-byte versus partial-packet timeout handling.

## [1.7.1] - 2026-07-24

### Fixed

- Treat a 90-second RCON read deadline with no incoming broadcasts as an idle
  wake-up instead of closing and reconnecting an otherwise healthy local
  connection.
- Keep the connected status active across idle read deadlines.
- Omit redundant RCON connection, reconnection, and connection-closed
  transport messages from Live RCON and retained history.
- Preserve non-idle connection diagnostics in the root-only web audit log.
- Add regression tests for idle-timeout continuation, future transport-event
  suppression, retained-history filtering, and sequence continuity.

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
