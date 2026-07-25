# MORDHAU Server Alpine Linux v1.9.0

This release contains the following changes relative to v1.8.3.

## Changelog

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
