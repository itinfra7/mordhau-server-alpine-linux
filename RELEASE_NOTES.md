This release contains the following changes relative to v2.6.2.

## Changelog

### Fixed

- Keep unconfigured manager instances isolated from the installed server's
  active self-update state, allowing the verified installer test suite to run
  inside the detached web-update worker.
- Give automatic-update and trusted-proxy test fixtures explicit private
  manager-update state files instead of permitting access to installed state.

### Validation

- Verify fixture state paths are isolated from the installed manager state.
- Verify the complete installer test suite while the detached update worker
  owns the production update state.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.2](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.2)

Full comparison: [v2.6.2...v2.6.3](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.2...v2.6.3)
