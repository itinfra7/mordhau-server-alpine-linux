This release contains the following changes relative to v2.2.2.

## Changelog

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
  bounded requests, pinned DNS results, and rejection of non-public
  destinations.
- Never return the saved webhook URL or mod.io API key to the browser, and keep
  moderation reason text and webhook destinations out of audit details.

### Validation

- Verify recovery state, metric retention, log management, webhook address
  policy, timed moderation, live ping and platform transport, MapRotation
  preservation, dependency-removal plans, and all three mod restart policies.
- Pass `go test ./...`, `go vet ./...`, `go test -race ./...`, JavaScript
  syntax validation, POSIX shell syntax validation, deterministic Runtime
  bridge builds, Unicode Bridge installation tests, and whitespace checks.

## Documentation

See `README.md` for installation, update, configuration, security, testing,
and rollback instructions. See `CHANGELOG.md` for the complete version
history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.2.2](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.2.2)

Full comparison: [v2.2.2...v2.3.0](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.2.2...v2.3.0)
