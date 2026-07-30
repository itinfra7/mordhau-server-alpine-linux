This release contains the following changes relative to v2.6.0.

## Changelog

### Fixed

- Prevent initial RCON/SAY prompt setup from focusing the input and scrolling
  mobile browsers from the dashboard top to Server Events after login,
  refresh, or page restoration.
- Suppress `Waiting to start` while no players are connected and retain only
  the first `Leaving map` event until a player session opens a new idle
  visibility window.
- Reconstruct idle MatchState visibility from persistent Server Events when
  MORDHAU Control restarts, preventing the same empty-server map cycle from
  being shown again after a web-service restart.
- Compact legacy repeated idle MatchState records when persistent Server
  Events are loaded, without rewriting or deleting the original event log.

### Validation

- Verify empty-server MatchState suppression, active-player visibility,
  post-session visibility reset, persistent-history compaction, sequence
  continuity, and manager-restart state restoration.
- Verify manual initial scroll restoration, focus-free prompt initialization,
  explicit focus after a user changes prompt mode, frontend syntax, and the
  complete Go and integration-test suites.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.0](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.0)

Full comparison: [v2.6.0...v2.6.1](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.0...v2.6.1)
