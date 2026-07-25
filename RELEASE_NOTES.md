# MORDHAU Server Alpine Linux v1.8.3

This release contains the following changes relative to v1.8.2.

## Changelog

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
