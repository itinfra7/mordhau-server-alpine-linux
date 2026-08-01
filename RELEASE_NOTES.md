This release contains the following changes relative to v2.6.5.

## Changelog

### Fixed

- Exclude IPv4 and IPv6 link-local tunnel endpoints from persistent player IP
  history.
- Remove previously stored link-local values from player address lists and
  connection IP fields while retaining session timing and all non-address
  profile data.

### Validation

- Verify canonical public, private, native IPv6, and IPv4-mapped addresses are
  preserved while link-local addresses are rejected.
- Verify migration is idempotent and current-log imports cannot restore a
  discarded link-local tunnel endpoint.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.5](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.5)

Full comparison: [v2.6.5...v2.6.6](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.5...v2.6.6)
