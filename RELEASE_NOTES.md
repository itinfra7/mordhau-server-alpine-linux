This release contains the following changes relative to v2.6.6.

## Changelog

### Changed

- Identify automatic-update player notices as a server management-tool update,
  game-server update, or combined update without exposing product names,
  versions, build identifiers, or installation details.
- Normalize previously retained notices in the browser view while preserving
  the append-only administrative event record.

### Fixed

- Recover a missing player logout from fresh Runtime bridge PlayerController
  identities after a bounded continuous-absence grace period.
- Persist the recovered Server Events lifecycle record and close the matching
  player-history session while preventing late-log and manager-restart
  duplicates.
- Prevent unauthenticated chat observations from creating ghost active
  sessions.
- Suppress and compact repeated empty-server map-state tail cycles when a fresh
  Runtime snapshot confirms that no PlayerControllers are connected.

### Validation

- Verify per-player identity reconciliation, missing and incomplete Runtime
  data, initial discovery grace, late native close records, persistent session
  closure, restart deduplication, and chat-only activity.
- Verify browser-view compaction preserves the append-only event history and
  unrelated local and Fleet events.
- Verify automatic-update player notices never expose internal update targets.
- Verify legacy-notice normalization does not rewrite raw event history.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.6](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.6)

Full comparison: [v2.6.6...v2.6.7](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.6...v2.6.7)
