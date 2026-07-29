This release contains the following changes relative to v2.3.0.

## Changelog

### Added

- Add lossless, single-threaded XZ `-9e` compression for finalized
  `Mordhau_<timestamp>.log` archives.
- Add automatic background compression after managed log rotation and an
  explicit `server.sh compress-logs` maintenance command.
- Add authenticated raw game-log search and JSON Lines export across the
  active log, uncompressed archives, and `.log.xz` archives without extracting
  compressed files to disk.

### Changed

- Verify each XZ stream and its restored SHA-256 before removing the
  uncompressed source.
- Preserve archive modification times, use idle CPU and I/O priority, and keep
  game-server startup independent of compression completion.
- Read `.log.xz` files directly for player-history reconstruction and apply
  configured game-log retention to compressed and uncompressed archives.
- Treat `.log` and `.log.xz` forms as the same immutable player-history source
  so archive conversion cannot duplicate sessions.
- Install the Alpine `xz` package as a required runtime dependency.

### Security

- Keep compressed archives and compression state at mode `0600`.
- Reject symbolic links, nested paths, out-of-scope names, changed sources,
  corrupt output, and mismatched archive collisions without removing the
  original log.
- Serialize authenticated game-log searches and resolve archive paths only
  from the server-owned game-log directory.

### Validation

- Verify lossless compression, XZ integrity, restored checksums, permissions,
  interrupted-finalization recovery, collision preservation, and idempotent
  maintenance runs.
- Verify streamed XZ reads, compressed player-history import,
  duplicate-import prevention, raw/XZ search, retention, and a live
  authenticated search against an installed compressed archive.
- Pass `go test ./...`, `go vet ./...`, `go test -race ./...`, JavaScript
  syntax validation, POSIX shell syntax validation, Runtime Bridge builds,
  Unicode Bridge installation tests, and whitespace checks.

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

Previous release: [v2.3.0](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.3.0)

Full comparison: [v2.3.0...v2.3.1](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.3.0...v2.3.1)
