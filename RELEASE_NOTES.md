This release contains the following changes relative to v2.3.2.

## Changelog

### Fixed

- Synchronize the dashboard RCON port with `RconPort` in `Game.ini` when
  dedicated-server ports are saved and when an existing installation starts
  the updated web manager.
- Stage the synchronized INI value while the game server is running and update
  the active configuration while it is stopped.
- Stop an OpenRC-managed game server through OpenRC during an in-place upgrade
  so the previously running server is reliably started after validation.

### Security

- Preserve intentionally disabled `RconPort` entries and disabled game-session
  sections while updating their stored value.
- Serialize dashboard port changes with configuration and lifecycle operations,
  and restore the prior server-port state if INI synchronization cannot be
  persisted.

### Validation

- Verify duplicate active values, disabled entries, disabled sections, missing
  values, CRLF input, unrelated INI content, and invalid port rejection.
- Verify OpenRC-managed and manually launched game servers use their respective
  shutdown paths during an in-place upgrade.
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

Full comparison: [v2.3.2...v2.3.3](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.3.2...v2.3.3)
