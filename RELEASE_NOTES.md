This release contains the following changes relative to v2.5.0.

## Changelog

### Added

- Add independent `10-minute countdown`, `when the server is empty`, and
  `scheduled server time` policies for automatic MORDHAU Control and
  Dedicated Server updates.
- Add a default-off recurring Dedicated Server restart schedule with
  server-local time and weekday selection.
- Add persistent 10-, 5-, 4-, 3-, 2-, and 1-minute Unicode Bridge notices for
  recurring scheduled restarts.

### Changed

- Migrate existing automatic-update state to the countdown policy without
  changing either update enablement setting.
- Require a continuously empty 30-second Runtime observation before an
  empty-server automatic update begins.
- Use the MORDHAU Control policy for a combined maintenance window when both a
  Control release and Dedicated Server build are available, because the
  verified Control installer also updates the Dedicated Server.
- Skip a recurring restart occurrence when a mod update, product update,
  detached manager update, or manual lifecycle operation already owns the
  maintenance window.
- Keep future scheduled-policy windows from blocking an earlier compatible
  update or restart; only the active ten-minute window claims restart
  coordination.

### Security

- Store automatic-update and recurring-restart policy, countdown progress,
  and next-occurrence state in mode-`0600` server files.
- Keep recurring restart and automatic update mutations behind authenticated,
  CSRF-protected APIs, including Fleet Controller remote routing and requester
  auditing.

### Validation

- Verify v1 automatic-update migration, independent policy persistence,
  empty-server grace handling, server-local scheduled windows, weekday
  selection, complete countdown sequences, next-occurrence persistence, CSRF
  enforcement, and lifecycle conflict exclusion.
- Verify frontend syntax, responsive policy and weekday controls, the complete
  Go suite, race detection, shell syntax, installer version transitions, and
  the existing integration-test suite.

## Documentation

See `README.md` for installation, update policies, recurring restart
scheduling, testing, security, and rollback instructions. See `CHANGELOG.md`
for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.5.0](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.5.0)

Full comparison: [v2.5.0...v2.6.0](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.5.0...v2.6.0)
