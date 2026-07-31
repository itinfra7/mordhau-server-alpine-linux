This release contains the following changes relative to v2.6.4.

## Changelog

### Fixed

- Use the normal Server Events text color for both local and relayed player
  login and logout records.
- Emphasize lifecycle records collected by the currently selected server with
  bold text while keeping relayed server lifecycle records at regular weight.

### Validation

- Verify local lifecycle emphasis without a lifecycle color override or a
  relayed Fleet-event style override.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.4](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.4)

Full comparison: [v2.6.4...v2.6.5](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.4...v2.6.5)
