This release contains the following changes relative to v2.1.1.

## Changelog

### Added

- Add a Players tab between Runtime and Configuration with a most-recent-first
  PlayFab ID and nickname directory, historical nickname search, last
  connection, accumulated server time, current-session state, and retained
  nickname and canonical IP history.
- Add verified mute/unmute and ban/unban toggles plus persistent player
  comments attributed to the responsible web account.
- Add bidirectional Players ordering by last connection or accumulated server
  time, country flags for each player's latest address, and approximate
  country, region, and city labels for retained addresses.
- Add automatic local DB-IP City Lite download, full MMDB verification,
  atomic monthly updates, and configurable IP/CIDR exclusions for internal
  networks.
- Seed missing Engine.ini `IpNetDriver` values with
  `NetServerMaxTickRate=60` and `ConnectionTimeout=10.0` during installer
  initialization while preserving existing and intentionally disabled values.
- Add an authenticated CustomPaks panel immediately after Mods with manual PAK
  inventory, drag-and-drop and file-picker upload, upload progress, and
  responsive controls.
- Add next-start activation, deactivation, and deletion staging for manually
  installed PAK files.
- Make the dashboard brand link return the current page to its top.

### Changed

- Correlate accepted game connections, login requests, authentication, UTF-8
  identity observations, and disconnect records into persistent player
  sessions; import archived logs once and rescan the current log
  idempotently.
- Keep historical nicknames in each player-list search document and include
  accumulated server time and the latest locally resolved country in list
  records.
- Apply staged CustomPaks changes after Steam validation and immediately before
  each managed start or restart.
- Use validated BusyBox-compatible `find` cleanup for installer, bridge-build,
  and integration-test temporary directories.

### Security

- Store player history and comments in a mode-`0600` state file, require
  authenticated CSRF-protected mutations, exclude comment text from audit
  details, and confirm moderation changes against the running server's RCON
  lists before reporting success.
- Keep GeoIP data and configuration root-only; use a fixed HTTPS download
  origin with redirects disabled, compressed and expanded size limits,
  filesystem reserve enforcement, MMDB metadata and tree verification, and
  atomic replacement. Resolve player addresses locally without a per-address
  API request.
- Keep inactive and uploaded PAK files in root-only manager directories, apply
  all mutations under the lifecycle lock, and show project-managed packages as
  protected entries that cannot be deactivated, deleted, or manually replaced.
- Require authenticated, CSRF-protected CustomPaks mutations; validate UTF-8
  `.pak` basenames, reject case-insensitive overwrite conflicts, cap each
  upload at 8 GiB, and retain a 1 GiB filesystem reserve.

### Validation

- Verify Unicode identity and canonical IPv4/IPv6 correlation, session
  deduplication, most-recent ordering, duration accounting, archived-log
  import persistence, empty-array API contracts, attributed comment
  persistence and audit exclusion, and RCON moderation command confirmation.
- Verify GeoIP edition rollover, localized record normalization, private and
  ignored-address exclusion, incompatible database rejection, unavailable
  edition handling, historical-nickname search wiring, both sorting keys and
  directions, attribution, and responsive location presentation.
- Verify manual and project-managed package discovery, protected-package
  deactivation/deletion/replacement rejection, inactive managed-package
  restoration, staged state rendering, activation/deactivation moves,
  deletion, upload limits, duplicate rejection, deletion cancellation,
  lifecycle locking, and idempotent next-launch application.

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

Previous release: [v2.1.1](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.1.1)

Full comparison: [v2.1.1...v2.2.0](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.1.1...v2.2.0)
