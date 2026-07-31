This release contains the following changes relative to v2.6.3.

## Changelog

### Fixed

- Normalize local Fleet-mode player login and logout records to the same
  `(Server) <Player> joined/left the server.` presentation used by relayed
  lifecycle events.
- Preserve source-server in-game echo suppression and append-only raw event
  history while applying lifecycle normalization only to web responses.

### Validation

- Verify Controller and Managed Server lifecycle formatting, Unicode player
  names, relayed-source preservation, standalone compatibility, legacy event
  compatibility, and immutable stored history.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.3](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.3)

Full comparison: [v2.6.3...v2.6.4](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.3...v2.6.4)
