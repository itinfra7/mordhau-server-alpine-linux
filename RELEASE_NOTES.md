# MORDHAU Server Alpine Linux v2.0.0

This release contains the following changes relative to v1.9.0.

## Changelog

### Added

- Add a native Windows runtime-reflection bridge for the supported MORDHAU
  Dedicated Server build, loaded through a guarded DXGI proxy under Wine.
- Add an authenticated Runtime panel for the active authority GameMode,
  GameState, PlayerControllers, PlayerStates, and possessed Pawns.
- Enumerate properties from each actual runtime class through its complete
  superclass chain with Unreal type, flags, offset, array index, exported
  value, RepIndex, RepNotify function, lifetime condition, and replication
  scope.
- Add immediate game-thread property changes with expected-value conflict
  detection, Unreal text import verification, failure rollback, and
  `AActor::FlushNetDormancy` plus `AActor::ForceNetUpdate` for
  replication-eligible Actor fields.
- Add a one-second shared PlayerController count collected in the game process
  and distributed through the existing authenticated event stream.
- Add responsive Runtime target, search, declaring-class filter, editable-only
  filter, value editor, and replication-status controls for desktop and mobile
  browsers.
- Add native bridge build validation and Go tests for shared status sampling,
  stale-state rejection, request serialization, cache reuse, target
  validation, and multilingual values.

### Changed

- Build and install the native bridge only when
  `MordhauServer-Win64-Shipping.exe` matches the supported SHA-256 digest.
- Enable the Wine native DXGI override only for that supported executable
  build; unsupported game updates continue with Wine's built-in DXGI and an
  unavailable Runtime panel.
- Cache Runtime status and identical short-lived target views in the Go
  manager so administrator count does not multiply game-process status
  collection.
- Extend the dashboard with the shared live PlayerController count and current
  runtime-bridge state.

### Security

- Restrict native runtime targets to actors rediscovered from the active
  `UWorld` and bind target identifiers to Unreal object indices and serial
  numbers.
- Keep object references, class references, interfaces, delegates, field
  paths, deprecated and editor-only fields, function parameters,
  engine-internal Blueprint frame storage, and unexportable values read-only.
- Keep bridge IPC inside the root-only manager runtime directory and expose no
  additional network listener.
- Require authenticated, CSRF-protected, expected-value-checked web mutations
  and audit the responsible account, canonical client address, target,
  property, and replication scope without recording property values.
- Refuse bridge hook installation when PE metadata or the `UWorld::Tick`
  prologue does not match the supported executable.

### Validation

- Confirm PlayerController count and associated PlayerController, PlayerState,
  and possessed Pawn discovery with a connected client.
- Confirm authoritative readback, client delivery through a `Net`/`OnRep`
  PlayerState string property, and restoration of the original value.

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
