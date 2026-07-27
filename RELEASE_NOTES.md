This release contains the following changes relative to v2.2.0.

## Changelog

### Fixed

- Accept new persistent player nicknames only when a login request has been
  correlated with successful player authentication.
- Prevent mutable chat and disconnect observations, including temporary
  server-side PlayerState edits, from adding or reprioritizing nickname
  history.

### Validation

- Verify that an observed runtime-only name is excluded from history while a
  Unicode nickname supplied by a later authenticated login is retained and
  becomes the latest nickname.

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

Previous release: [v2.2.0](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.2.0)

Full comparison: [v2.2.0...v2.2.1](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.2.0...v2.2.1)
