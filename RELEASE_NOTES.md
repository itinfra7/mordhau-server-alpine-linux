# MORDHAU Server Alpine Linux v2.1.0

This release contains the following changes relative to v2.0.0.

## Changelog

### Added

- Add type-derived Runtime editors for exact Boolean choices, reflected Unreal
  enum values, signed and unsigned integer widths, finite floating-point
  values, names, strings, and structured Unreal text.
- Add native `UEnum` value metadata for enum-backed byte and enum properties.
- Add authenticated controller identity metadata from
  `PlayerState.PlayerNamePrivate` and
  `MordhauPlayerState.PlayFabPlayer.PlayFabId`.
- Add single-open connected-player groups labeled with nickname and PlayFab ID
  and containing that player's PlayerController, PlayerState, and possessed
  Pawn.

### Changed

- Separate silent two-second Runtime value polling from the manual
  `Refresh values` button state so background refreshes do not repeatedly
  disable or animate the button.
- Keep 64-bit integer validation string-based in both browser and Go server to
  avoid precision loss through JavaScript floating-point conversion.

### Fixed

- Exclude Unreal terminal `MAX` and `*_MAX` enum sentinels from selectable
  Runtime values.
- Reject nonzero floating-point input that underflows to zero in either the
  browser or Go validation path.

### Security

- Resolve property metadata in the Go manager before every mutation and reject
  values outside the reflected editor type, enum choices, numeric range, or
  structured-text delimiter rules before forwarding them to the native
  bridge.
- Validate player identity placement, size, encoding, and PlayFab ID
  characters in both sampled status and target responses.
- Keep Unreal `ImportText` verification and rollback as the final authority
  for complex property values.

### Validation

- Verify browser and server metadata for Boolean, enum, integer, float,
  string, and structured property editors.
- Verify malformed Boolean values receive HTTP 400 before reaching the native
  bridge.
- Verify player identity grouping and enum choices against a connected
  dedicated-server client.

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
