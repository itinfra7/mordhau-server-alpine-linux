This release contains the following changes relative to v2.6.8.

## Changelog

### Fixed

- Separate Fleet in-game relay rendering from web Server Events rendering.
- Show the canonical PlayFabID to administrators in relayed All and Team Chat
  web records without adding it to messages delivered to game clients.

### Validation

- Verify one Fleet chat event keeps the established nickname-only in-game
  format while its web history record includes PlayFabID, nickname, channel,
  and message.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.8](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.8)

Full comparison: [v2.6.8...v2.6.9](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.8...v2.6.9)
