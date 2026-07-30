This release contains the following changes relative to v2.4.0.

## Changelog

### Added

- Add `Standalone`, `Fleet Controller`, and `Managed Server` roles with
  persistent Ed25519 identities, Controller-assigned aliases, pairings, and
  connectivity state.
- Add a node-ID-scoped top-bar server selector. A Fleet Controller can operate
  the existing management panels against its local server or an explicitly
  selected Managed Server while direct Managed Server web access remains
  available.
- Add ten-second Managed Server heartbeats carrying game-process state,
  connected-player count, management version, and server-collected resource
  metrics to the Fleet Controller cache.
- Add independent, per-server controls for All Chat, Team Chat, Web SAY, web
  RCON SAY, and player login/logout routing. Every category defaults to OFF,
  requires both source and destination opt-in, and includes a mandatory
  Controller-assigned source label.

### Changed

- Parse chat channel and message fields from `Mordhau.log` for All Chat and
  Team Chat routing while retaining the existing UTF-8 Server Events output.
- Route still-tracked player logout events when the game process stops or the
  active log is replaced, and preserve event order per destination server.
- Use one explicit authenticated API registry for direct and remotely selected
  manager operations.
- Retain each browser tab's selected node in its URL and reload safely when a
  role changes or the active Managed Server is removed.

### Security

- Keep new and upgraded installations in `Standalone` mode with no fleet
  listener and no event routing until an administrator explicitly enables a
  fleet role.
- Require TLS 1.3 with mutually pinned Ed25519 public identities. Managed
  Servers additionally require the direct TCP peer to equal the configured
  Controller source IP; Controllers validate the Managed Server node ID and
  reject redirects.
- Store identity and fleet state in root-only files. Private keys never leave
  an installation, and the Controller does not forward browser cookies,
  authorization headers, forwarding headers, or public-proxy trust.
- Bind IPv4 and IPv6 Fleet listener addresses to their explicit socket
  families so an IPv4 wildcard cannot become an unintended dual-stack socket.
- Require browser authentication and the original CSRF token before any
  remote state change. Restrict the internal gateway to the explicit manager
  API registry and audit the Controller account, canonical browser IP, fleet
  peer, request ID, and destination node.
- Recheck destination policy at delivery, deduplicate event IDs, bound queues
  and fields, enforce Unicode Bridge message limits, and omit web credentials,
  administrator identities, player IPs, and PlayFab IDs from relay events.

### Validation

- Verify identity permissions and connection-key parsing, mutual TLS pinning,
  TLS 1.3 restriction, IPv4/IPv6 endpoint validation, expected Controller
  source-IP enforcement, canonical browser-IP propagation, forwarding-header
  removal, and remote browser CSRF rejection.
- Verify node-scoped API routing, structured All/Team chat parsing,
  multilingual source labels, all five rendered event types, forced
  live-session logout, RCON SAY parsing, per-destination ordering, and
  symmetric source and destination event opt-in.
- Verify frontend syntax and responsive Fleet controls, the complete Go suite,
  shell syntax, installer version transitions, and existing integration
  tests.

## Documentation

See `README.md` for installation, pairing, event routing, security, testing,
update, and rollback instructions. See `CHANGELOG.md` for the complete version
history.

## Integrity

Verify the release archive and checksum from the same directory:

```sh
sha256sum -c SHA256SUMS
```

Repository-authored source is available under the MIT License.

Previous release: [v2.4.0](https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.4.0)

Full comparison: [v2.4.0...v2.5.0](https://github.com/itinfra7/mordhau-server-alpine-linux/compare/v2.4.0...v2.5.0)
