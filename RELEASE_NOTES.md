This release contains the following changes relative to v2.2.1.

## Changelog

### Added

- Show the currently loaded map and game-mode class on the dashboard from the
  authoritative game log and live Runtime bridge.
- Add a live map-change dialog grouped by installed game mode. The
  server-cached catalog includes shipped maps, enabled mod.io content, and
  active CustomPaks only when a safe map/game-mode pairing can be established.
- Validate every live map selection against that catalog before sending the
  fixed `changelevel <map>` RCON command, and retain the requester, selection,
  result, and response in the existing event and audit records.
- Install the checksum-pinned `repak` 0.2.3 helper and its upstream licenses
  for bounded, read-only Unreal PAK index and map metadata inspection.
- Persist each player's last observed general MORDHAU account level by reading
  server-side inventory XP and converting it through the supported MORDHAU
  build's native XP-to-level function, independently of replicated and ranked
  fields.
- Show that level in Player Profile and as a distinct badge between the
  country flag and nickname in Known Players.
- Retain a verified SteamID64 from the live `PlayFabPlayer` identity and show
  a Steam icon and profile link for Steam-backed player records.

### Changed

- Discover unprefixed official maps from their packaged default GameMode
  instead of requiring a mode-name prefix. This includes
  `LiteMordhauTestLevel` under Deathmatch while omitting internal
  initialization, main-menu, and base GameMode destinations.
- Retain at most one `Waiting to start` and one `Leaving map` server event
  during each interval with no connected players, then suppress both idle
  states after the first `Leaving map`.
- Reopen the idle match-state event window when a player authenticates and
  again when the last connected player leaves, while preserving every match
  state whenever at least one player is connected.

### Validation

- Verify the installed server catalog against shipped content and enabled
  mod.io PAKs, including the Dread 2 map/game-mode pairing.
- Verify strict catalog pair selection, fixed RCON map command construction,
  requester auditing, current map/game-mode parsing, ambiguous packaged
  game-mode rejection, unprefixed `LiteMordhauTestLevel` discovery, internal
  destination rejection, and map-catalog UI controls.
- Verify native account-progress validation with the known XP-to-level pair
  `90435` to level `38`, rejection of replicated, Duel, and Teamfight rank
  fields, latest-value persistence, and player list/profile rendering.
- Verify strict 17-digit SteamID64 validation, persistence, safe new-tab
  profile links, and rejection of malformed platform identities.
- Verify empty-server duplicate suppression, preservation across game-log
  rotation and managed server restarts, unrestricted active-player events, and
  a fresh empty-server window after the final player disconnects.

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

Previous release: [v2.2.1](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.2.1)

Full comparison: [v2.2.1...v2.2.2](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.2.1...v2.2.2)
