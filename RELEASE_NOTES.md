This release contains the following changes relative to v2.3.1.

## Changelog

### Fixed

- Resolve MORDHAU App ID `629800` metadata before the first SteamCMD update,
  explicitly select the Windows 64-bit depot, and retry only a transient
  `Missing configuration` result with bounded delays.
- Require an exact MORDHAU command and Wine process identity for game-server
  status so unrelated shell or diagnostic command lines cannot produce a
  false running state.
- Identify an unmanaged web-manager process by its exact `/proc` executable
  target instead of a command-line substring.

### Validation

- Verify SteamCMD command ordering, bounded metadata-only retries, and
  immediate failure for unrelated update errors.
- Verify exact launcher and shipping-process matching plus rejection of shell,
  unrelated Wine, and alternate-path false positives.
- Verify an empty SteamCMD cache and isolated Wine prefix reach the App ID
  `629800` Windows depot download.
- Complete an Alpine installation through SteamCMD validation, native bridge
  installation, initial configuration, Unicode Bridge installation, Go tests,
  web-manager build, and OpenRC setup.

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

Previous release: [v2.3.1](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.3.1)

Full comparison: [v2.3.1...v2.3.2](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.3.1...v2.3.2)
