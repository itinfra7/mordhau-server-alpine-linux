This release contains the following changes relative to v2.6.7.

## Changelog

### Changed

- Include each player's validated canonical PlayFabID in relayed All and Team
  Chat and use the same structured chat presentation for local and remote
  servers.
- Mark every event collected by the currently selected Fleet server and render
  it in bold while retaining regular weight for relayed events.
- Keep identifier-free events compatible during rolling upgrades while
  validating every supplied player identifier.

### Fixed

- Apply selected-server emphasis to chat, match-state, command, response, and
  system events instead of only local login and logout records.
- Apply the selected-server label and emphasis metadata to immediate web RCON
  command results as well as snapshot and history responses.

### Validation

- Verify canonical PlayFabID publication and display for both All and Team
  Chat, invalid-ID rejection, and compatibility with legacy events.
- Verify Controller and Managed local records are marked current, relayed
  records remain unmarked, and persistent event history is not mutated.
- Verify frontend source emphasis depends on explicit API metadata and does not
  introduce a server-specific text color.

## Documentation

See `README.md` for installation, operation, testing, security, and rollback
instructions. See `CHANGELOG.md` for the complete version history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.6.7](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.6.7)

Full comparison: [v2.6.7...v2.6.8](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.6.7...v2.6.8)
