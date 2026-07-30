This release contains the following changes relative to v2.6.1.

## Changelog

### Fixed

- Prefix locally collected Server Events with the selected server's display
  name in Fleet Controller and Managed Server modes while preserving the
  source label already carried by relayed events.
- Remove redundant embedded timestamps from new player login and logout
  records. Browser-local event timestamps remain the single visible time
  source.
- Compact legacy embedded login/logout timestamps when Fleet event history is
  displayed without rewriting the append-only stored event log.
- Keep source-server in-game echo suppression intact while providing the
  source server's corresponding web-visible lifecycle record.

### Validation

- Verify Controller and Managed Server labels, local match-state and lifecycle
  attribution, relayed-source preservation, standalone compatibility,
  legacy timestamp compaction, and immutable persistent history.
- Verify both direct Managed Server event history and Controller-routed Fleet
  history retain locally collected login and logout records.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.1](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.1)

Full comparison: [v2.6.1...v2.6.2](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.1...v2.6.2)
