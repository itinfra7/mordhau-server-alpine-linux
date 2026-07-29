This release contains the following changes relative to v2.3.2.

## Changelog

### Added

- Add hourly server-side checks for stable project GitHub Releases and public
  MORDHAU Dedicated Server Steam builds, with shared update banners and
  authenticated manual checks.
- Add a checksum-verified detached management updater with bounded safe archive
  extraction, persistent progress, interrupted-state recovery, and service
  restoration after installer failure.
- Add server-wide, default-off automatic management and dedicated-server
  updates with persistent English 10-, 5-, 4-, 3-, 2-, and 1-minute in-game
  restart notices.
- Add semantic management-version detection for fresh installs, upgrades,
  same-version validation, and explicitly authorized `--allow-downgrade`
  transitions.

### Fixed

- Prevent management updates, Steam checks, automatic schedules, and web
  lifecycle actions from overlapping incompatible operations.
- Reconcile interrupted update state and avoid repeated external checks or
  duplicate availability audit records before the persisted hourly interval.
- Accept newly written, structurally valid public-build metadata despite a
  nonzero SteamCMD Wine process status, while rejecting incomplete or stale
  console output.
- Preserve a complete existing SteamCMD installation during manager upgrades
  instead of replacing it with an older bootstrap before each self-update.
- Synchronize the dashboard RCON port with `RconPort` in `Game.ini` when
  dedicated-server ports are saved and when an existing installation starts
  the updated web manager.
- Stage the synchronized INI value while the game server is running and update
  the active configuration while it is stopped.
- Stop an OpenRC-managed game server through OpenRC during an in-place upgrade
  so the previously running server is reliably started after validation.

### Security

- Restrict self-updates to canonical stable project releases, require the
  expected archive and checksum assets, enforce download and extraction
  limits, reject unsafe archive entries, and verify `SHA256SUMS` before
  executing the bundled installer.
- Require authentication and CSRF validation for update checks, automatic
  settings, and update requests; serialize detached workers and reject
  conflicting lifecycle changes while atomically replacing the web binary.
- Reject implicit management-code downgrades and malformed installed-version
  state, and atomically record a new version only after validation and
  requested service starts succeed.
- Preserve intentionally disabled `RconPort` entries and disabled game-session
  sections while updating their stored value.
- Serialize dashboard port changes with configuration and lifecycle operations,
  and restore the prior server-port state if INI synchronization cannot be
  persisted.

### Validation

- Verify release metadata, response limits, checksums, archive safety,
  installer-version matching, detached worker locking and result state,
  interruption recovery, installer failure service restoration, Steam build
  parsing including nonzero process status, server-wide settings, countdown
  persistence, and CSRF enforcement.
- Verify duplicate active values, disabled entries, disabled sections, missing
  values, CRLF input, unrelated INI content, and invalid port rejection.
- Verify OpenRC-managed and manually launched game servers use their respective
  shutdown paths during an in-place upgrade.
- Verify complete and incomplete SteamCMD bootstrap installations are
  distinguished before an installer refresh.
- Verify semantic version validation and ordering, transition classification,
  downgrade refusal, malformed-state refusal, and atomic mode-`0600` version
  recording.
- Verify the complete Go test suite and both upgraded Alpine installations.

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

Previous release: [v2.3.2](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.3.2)

Full comparison: [v2.3.2...v2.4.0](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.3.2...v2.4.0)
