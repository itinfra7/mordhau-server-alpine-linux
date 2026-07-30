# MORDHAU Server Alpine Linux

This repository installs and manages the Windows build of MORDHAU Dedicated
Server on Alpine Linux through Wine. It includes SteamCMD update automation,
OpenRC services, and an authenticated Go web manager.

## Repository Scope

The repository provides:

- Windows SteamCMD installation under `/root/steamcmd`
- Windows MORDHAU Dedicated Server installation under `/root/mordhau`
- A dedicated Wine prefix at `/root/mordhau/.wine`
- SteamCMD validation before every server start or restart
- POSIX shell lifecycle control
- OpenRC service definitions for the game server and web manager
- An animated responsive Go web manager with shared one-minute system metrics
  and persistent light/dark themes
- Optional multi-server operation with `Standalone`, `Fleet Controller`, and
  `Managed Server` roles, an explicit top-bar server selector, and remote
  access to each registered server's management panels
- Default-off cross-server routing for All Chat, Team Chat, Web SAY, web RCON
  SAY, and player login/logout events with mandatory source-server labels
- A native, game-thread Unreal Reflection bridge for authenticated inspection
  and controlled editing of current server actor properties
- Type-aware Runtime property controls with exact Boolean and enum choices,
  width-correct integer bounds, finite floating-point validation, and
  structured Unreal text checks
- Connected-player Runtime navigation labeled with the current nickname and
  PlayFab ID
- A shared live PlayerController count collected once by the server and
  distributed to every authenticated administrator
- A dashboard connected-player directory with PlayFab ID, nickname, country,
  account level, platform, and live ping
- Persistent player connection history with PlayFab ID and nickname search,
  known nickname and IP history, last observed general account level,
  verified Steam profile links, platform identity, accumulated play time,
  session timelines, timed moderation, kick and warning controls, and
  attributed administrator comments
- Bidirectional player ordering by last connection or accumulated server time,
  plus local country, region, and city enrichment from DB-IP City Lite
- Structured Game.ini and Engine.ini editing with persistent item and section
  enable/disable state
- Optional mod.io metadata, recursive dependency status, and dependency
  management for Game.ini
- Manual CustomPaks inventory, streamed PAK upload, and next-launch
  activation, deactivation, and deletion
- Optional active-mod update detection with countdown, empty-server, or
  scheduled managed restart policies
- Hourly server-side checks for stable management releases and public
  MORDHAU Dedicated Server Steam builds, with shared update banners
- Optional server-wide automatic management and dedicated-server updates with
  independent countdown, empty-server, or scheduled restart policies
- Optional weekday-aware Dedicated Server restart scheduling with a persistent
  ten-minute in-game countdown
- Dependency-aware mod removal with shared-dependency protection
- Persistent initial-map and dedicated-server port selection
- Current map and game-mode display plus catalog-validated live map travel
  across shipped, enabled mod.io, and active CustomPak content
- A catalog-backed visual MapRotation editor with reversible entry state and
  staged configuration support
- Desired-state-aware game-process crash detection, bounded automatic recovery,
  retained diagnostics, and manual retry
- Server-side one-minute metric history, managed log search/export/rotation,
  verified XZ game-log archiving and retention, and optional HTTPS webhook
  alerts
- UTF-8 server-event following from `Mordhau.log`, including player lifecycle,
  chat, match-state, killfeed, scorefeed, and punishment records, with repeated
  idle empty-server map states suppressed
- A unified RCON/SAY prompt with retained administrative command responses and
  acknowledged multilingual server messages
- Persistent latest lifecycle results and append-only server-event history
- A server-only Unicode Bridge for acknowledged outbound multilingual messages
- Web account and IPv4/IPv6 access-policy management with inclusive IPv4
  ranges and per-rule comments
- Optional trusted reverse-proxy client-IP resolution with direct-access
  fallback
- Per-account JSON Lines web access and change auditing

MORDHAU, SteamCMD, Wine, `repak`, and their assets are downloaded from their
respective upstream distribution channels and are not included in this
repository. The DB-IP City Lite database is also downloaded at runtime and is
not redistributed in the source archive.

## Supported Environment

- Alpine Linux 3.24
- x86_64 architecture
- OpenRC
- Root privileges
- Direct IP connectivity from a Fleet Controller to each Managed Server when
  the optional Server Fleet feature is enabled
- Internet access to Alpine package mirrors, SteamCMD, Steam content servers,
  Go module sources, this project's GitHub API and Releases, GitHub Releases
  for the checksum-pinned `repak` helper, and the DB-IP City Lite download host
- A mod.io API key when URL lookup, metadata, and recursive dependency
  inspection are required
- A modern desktop or mobile browser
- The supported MORDHAU shipping executable digest listed under
  `src/runtime-bridge/README.md` when the Runtime panel is required

The installer requires enough free storage for Wine, SteamCMD, Go build data,
MORDHAU Dedicated Server, and Steam update staging.

## Repository Layout

- `src/mordhau-server-alpine-linux.sh`
  Idempotent installer and management-code updater.
- `src/templates/server.sh`
  MORDHAU update, start, stop, restart, status, and game-log compression
  controller.
- `src/templates/webserver.sh`
  Foreground web-manager launcher with persistent port and trusted-proxy
  selection.
- `src/templates/openrc/`
  OpenRC service definitions.
- `src/steamcmd/mordhau-update.txt`
  SteamCMD runscript for Windows App ID `629800`.
- `src/web/`
  Go source, embedded frontend assets, and tests.
- `src/runtime-bridge/`
  Native Windows runtime-reflection bridge source, build script, supported
  executable guard, and technical documentation.
- `src/unicode-bridge/`
  Editable Blueprint source, cooked WindowsServer PAK, build tooling, integrity
  manifest, and standalone installer for the server-only Unicode Bridge.
- `src/tests/`
  Shell integration tests for repository-managed installation behavior.
- `CHANGELOG.md` and `RELEASE_NOTES.md`
  Version history and release-specific technical summary.
- `LICENSE`
  MIT terms for repository-authored source.

## Installation

### Release archive

Each GitHub Release publishes the versioned source archive, release notes,
full changelog, and archive checksum as separate assets. The source archive
also contains `CHANGELOG.md`.

The Release page body contains only that version's changelog section, a link
to the preceding Release, and a direct tag comparison. Cumulative feature
history remains in the versioned changelog asset instead of being repeated in
every Release body.

```sh
wget https://github.com/itinfra7/mordhau-server-alpine-linux/releases/download/v2.6.3/mordhau-server-alpine-linux-v2.6.3.tar.gz
wget https://github.com/itinfra7/mordhau-server-alpine-linux/releases/download/v2.6.3/CHANGELOG-v2.6.3.md
wget https://github.com/itinfra7/mordhau-server-alpine-linux/releases/download/v2.6.3/SHA256SUMS
sha256sum -c SHA256SUMS
tar -xzf mordhau-server-alpine-linux-v2.6.3.tar.gz
cd mordhau-server-alpine-linux-v2.6.3
chmod +x src/mordhau-server-alpine-linux.sh
./src/mordhau-server-alpine-linux.sh
```

### Repository checkout

```sh
git clone https://github.com/itinfra7/mordhau-server-alpine-linux.git
cd mordhau-server-alpine-linux
chmod +x src/mordhau-server-alpine-linux.sh
./src/mordhau-server-alpine-linux.sh
```

Installer options:

```text
--web-port PORT   Persist the web service port
--start-web       Start the web manager after installation
--start-server    Start MORDHAU Dedicated Server after installation
--enable-web      Enable web-manager boot startup and start it
--enable-server   Enable game-server boot startup and start it
--allow-downgrade Permit an explicit management-code downgrade
```

By default, both OpenRC services are installed in manual mode and remain
stopped. Existing boot-start settings and running states are preserved during
management-code updates. The installer reports fresh installation, upgrade,
same-version validation, or explicit downgrade according to the mode-`0600`
version state in `/root/mordhau/.manager/manager-version`. Downgrades are
rejected unless `--allow-downgrade` is supplied. The new version is recorded
only after managed files, validation, and requested service starts complete.

## First Installation

The installer performs these operations:

1. Installs Alpine packages required by Wine, SteamCMD, Go, OpenRC, and XZ
   game-log archiving.
2. Downloads and self-updates Windows SteamCMD.
3. Selects the Windows 64-bit depot, resolves App ID `629800` metadata, and
   installs or validates MORDHAU Dedicated Server. Only a transient
   SteamCMD `Missing configuration` result is retried.
4. Runs the server without game options for five seconds when generated
   WindowsServer configuration files do not exist.
5. Generates an eight-character mixed-case alphanumeric RCON password and
   enables RCON on port `7778` through the generated
   `/Script/Mordhau.MordhauGameSession` section.
6. Verifies the MORDHAU shipping executable, builds the native runtime
   reflection bridge for a supported build, and installs its guarded DXGI
   proxy.
7. Verifies and installs the cooked server-only Unicode Bridge, then registers
   its nonreplicated server actor in active and staged Game.ini files.
8. Builds and installs the Go web manager.
9. Creates the initial web account and persistent security state.
10. Installs both OpenRC service definitions.

The initial web credentials are written with mode `0600` to:

```text
/root/mordhau/default_web_account.txt
```

The generated username contains four lowercase letters or digits. The
generated password contains eight characters with lowercase, uppercase, and
numeric characters.

## Server Control

```sh
/root/mordhau/server.sh start
/root/mordhau/server.sh stop
/root/mordhau/server.sh restart
/root/mordhau/server.sh update
/root/mordhau/server.sh compress-logs
/root/mordhau/server.sh status
```

Behavior:

- `start` validates the Steam installation, applies staged INI and CustomPaks
  changes, archives the previous log, and starts the server.
- `stop` performs graceful termination and then stops the dedicated Wine
  prefix if required.
- `restart` stops, validates, applies staged changes, and starts.
- `update` only runs while the game server is stopped.
- `compress-logs` losslessly compresses every finalized, uncompressed game-log
  archive without stopping the game server.
- Every managed launch uses the selected initial map, game/RCON/beacon/query
  ports, `-language=<code>`, `-LocalLogTimes`, and `-log`.

Before each managed launch, an existing `Mordhau.log` is moved to:

```text
/root/mordhau/log/Mordhau_<yyyy-mm-dd_hh-mm-ss>.log
```

The timestamp is derived from the log file's final modification time. Existing
archives are never overwritten. A background task then uses single-threaded
XZ `-9e` compression at idle CPU and I/O priority and produces:

```text
/root/mordhau/log/Mordhau_<yyyy-mm-dd_hh-mm-ss>.log.xz
```

The task tests the XZ stream and compares the restored SHA-256 with the
uncompressed source before removing that source. A failed compression retains
the `.log` file. Compressed files preserve the source modification time and
mode `0600`; progress is recorded in:

```text
/root/mordhau/.manager/runtime/log-compression.log
```

Compression never touches the active
`/root/mordhau/Mordhau/Saved/Logs/Mordhau.log` and does not delay the managed
game-server launch.

## Web Manager

Start in the foreground:

```sh
/root/mordhau/webserver.sh --port 8080
```

Start through OpenRC using the saved port:

```sh
rc-service mordhau-web start
```

The listener is IPv4 `0.0.0.0:<port>`.

The web manager provides:

- Login with optional 30-day persistent authentication
- Argon2id password hashing and CSRF-protected state changes
- Account creation, editing, and deletion
- Last-account deletion prevention
- IPv4 and IPv6 address/CIDR allow and deny rules
- Inclusive IPv4 allow and deny ranges using `start-end` or `start~end`
- Optional UTF-8 comments on individual network rules
- Selectable `all allow` or `all deny` base policy
- A 30-minute exact-address emergency allow when switching to `all deny`
- CPU, memory, swap, and MORDHAU-filesystem utilization sampled once per
  minute by one server-side collector and shared across administrator sessions
- Server-retained 24-hour and seven-day metric history, including connected
  PlayerController counts
- Current PlayerController count sampled once per second in the game process
  and shared through the authenticated event stream
- A clickable dashboard Players card when at least one PlayerController is
  present, showing PlayFab ID, nickname, country flag, account level, platform,
  and ping with direct Player Profile navigation
- Runtime GameMode and GameState inspection followed by single-open
  connected-player groups labeled with nickname and PlayFab ID; each player
  group contains its PlayerController, PlayerState, and possessed Pawn
- Runtime property filtering by name, type, declaring class, or current
  exported value
- Game-thread runtime property changes with expected-value conflict detection,
  type-aware controls and server-side validation, read-only type enforcement,
  replication metadata, net-dormancy flushing, and ForceNetUpdate
- A Players directory ordered by most recent connection with PlayFab ID and
  historical-nickname search
- Ascending or descending player ordering by last connection or accumulated
  server time
- Per-player last connection, accumulated server time, current-session state,
  last observed general account level, verified Steam profile link, known
  nicknames, and known IP addresses
- Country flags and approximate country, region, and city labels resolved
  locally from the latest available DB-IP City Lite database
- Permanent or timed server mute/unmute and ban/unban controls, reasoned
  kicks, Unicode administrator warnings, retained session timelines, and
  attributed administrator comments
- A default light theme with a persistent light/dark toggle
- Responsive phone, tablet, and desktop layouts with notched-display safe
  areas and touch-sized controls
- Optional Fleet Controller and Managed Server roles with a top-bar,
  node-ID-scoped server selector; Dashboard, Monitoring, Runtime, Players,
  Configuration, Mods, CustomPaks, Accounts, and Network access remain
  available for the selected server
- Controller-cached Managed Server connectivity, game state, player count,
  manager version, and server-collected resource metrics refreshed through a
  ten-second authenticated heartbeat
- Per-server, default-off event-routing controls for All Chat, Team Chat, Web
  SAY, web RCON SAY, and player login/logout
- Start, stop, restart, and stopped-only update controls
- Shared top-of-page notifications for newer stable management releases and
  public MORDHAU Dedicated Server Steam builds
- Authenticated manual update checks, checksum-verified management release
  installation, and restart/update controls for a detected Steam build
- Server-wide, default-off automatic update settings for management releases
  and dedicated-server builds, with persistent 10-, 5-, 4-, 3-, 2-, and
  1-minute English in-game notices before a managed restart
- Persistent latest lifecycle action, requester account and canonical client
  IP, result, timestamps, and output
- Boot startup mode controls for both OpenRC services
- Persistent web-service port selection
- Persistent initial-map selection
- Current map and game-mode display plus validated live map selection grouped
  by compatible installed game mode
- A visual MapRotation editor using the same dynamic installed-content
  catalog, with ordering and reversible active/inactive entries
- Game, RCON, beacon, and query port selection with range and collision checks
- Launch-language selection
- Optional mod.io API-key validation, mod lookup, per-mod recursive dependency
  status, unresolved-dependency warnings, and scoped `Mods=<Resource ID>`
  management
- Server-wide cached mod metadata auto-refresh from 1 to 10,080 minutes,
  defaulting to 5 minutes for new state
- Optional automatic restart when an enabled mod publishes a new modfile,
  using a ten-minute countdown, a continuously empty server, or a selected
  server-local schedule
- Dependency-aware mod removal with an explicit preview and protection for
  dependencies shared by other configured mods
- Manual CustomPaks listing with drag-and-drop or file-picker PAK upload,
  progress reporting, and staged Active/Inactive/Delete controls
- Game.ini and Engine.ini section/item creation, editing, and removal
- Reversible per-item and whole-section enable and disable controls
- Revision checks, active-file backups, and staged edits while the game is
  running
- UTF-8 `Mordhau.log` following for player lifecycle, chat, match state,
  killfeed, scorefeed, and punishment events, including partial-write and log
  replacement handling and repeated idle empty-server match-state suppression
- A terminal-style RCON/SAY prompt below the shared server-event window
- Administrative RCON command execution with bounded response collection and
  immediate output in retained server-event history
- SAY messages using a root-only UTF-8 spool, ASCII-token RCON transport, and
  acknowledgement before success is reported
- Root-only server-event persistence with recent-history loading for later
  administrator sessions
- Root-only web access and administrative change audit logging
- Desired-state-aware crash recovery with retained diagnostics, bounded
  retries, and manual retry
- Audit and Server Events search/export, configurable log rotation and game-log
  retention, plus optional HTTPS webhook alerts

The CustomPaks panel lists regular `.pak` files from active, inactive, and
upload-staging storage. An uploaded file is staged as active by default.
Active/Inactive changes move a manually installed package between active and
root-only inactive storage, while Delete removes it. All three actions are
applied only after Steam validation and immediately before the next managed
server start, so they cannot change the currently running game process.

Active packages use:

```text
/root/mordhau/Mordhau/Content/CustomPaks
```

Inactive and newly uploaded packages remain under mode-`0700` manager storage:

```text
/root/mordhau/.manager/custompaks-inactive
/root/mordhau/.manager/custompaks-upload
```

The server permits one PAK per upload, limits a file to 8 GiB, preserves at
least 1 GiB of filesystem space, validates the UTF-8 filename, and never
overwrites an existing case-insensitive name. Project-managed packages,
including the Unicode Bridge, remain visible with their owning component.
They cannot be deactivated, deleted, or replaced through the manual upload
API. If such a package was moved into inactive storage outside the manager,
the panel permits only its restoration to Active.

IPv4 ranges include both endpoints and are stored in canonical `start-end`
form. The manager decomposes each range into the smallest exact set of CIDR
blocks, so addresses outside the submitted boundaries never match. Those
blocks participate in the same most-specific-prefix and equal-prefix deny
precedence as ordinary CIDR rules.

Each explicit network rule can store an optional single-line comment of up to
160 Unicode characters. Comments are metadata only and do not affect address
matching or rule precedence. Existing rules without a comment remain valid.

## Server Fleet

Every installation starts in `Standalone` mode. Standalone mode opens no fleet
listener, creates no outbound fleet connection, and keeps every event-sync
category disabled.

The optional roles are:

- `Fleet Controller`: stores the Managed Server directory, maintains
  Controller-initiated connections, routes selected server APIs, and provides
  the top-bar server selector.
- `Managed Server`: retains its ordinary direct web interface and additionally
  accepts a configured Fleet Controller on a separate listener. The default
  fleet listener is IPv4-only `0.0.0.0:8091`; use an explicit IPv6 address or
  `[::]:8091` when an IPv6 listener is required.

The browser connects only to the Fleet Controller when using the top-bar
selector. Each browser tab carries an explicit node ID in its URL; changing
the selected server in one tab does not change another tab or another
administrator's target. The Fleet Controller forwards only registered manager
API routes and does not forward browser cookies or authorization headers.

Pair two installations:

1. Open **Server Fleet** on the Controller installation, choose
   `Fleet Controller`, assign its display name, save, and copy its connection
   key.
2. Open **Server Fleet** on the Managed installation, choose
   `Managed Server`, assign its display name and listener address, and save.
   Copy the Managed Server connection key.
3. On the Managed Server, enter the exact source IP from which the Controller
   reaches it and paste the Controller connection key.
4. On the Fleet Controller, add the Managed Server with a unique display name,
   its reachable `IP:port`, and its connection key.
5. Permit the configured fleet TCP port from the Controller IP to the Managed
   Server. The fleet port does not need general Internet exposure.

A connection key contains only an Ed25519 public identity and its derived node
ID. Private identities and fleet state are retained with root-only permissions:

```text
/root/mordhau/.manager/fleet.json
/root/mordhau/.manager/fleet-identity-key.pem
/root/mordhau/.manager/fleet-identity-cert.pem
```

Fleet links require TLS 1.3, mutual possession of the configured Ed25519 keys,
the Managed Server's exact configured Controller source IP, and a matching
Managed Server node ID. Public certificate authorities, DNS names, redirects,
and forwarded client-address headers are not trusted for this channel.

Cross-server events are opt-in per server and category. A server with a
category disabled neither publishes nor receives that category. New nodes and
new installations start with all five categories disabled:

- All Chat
- Team Chat
- Web SAY
- RCON SAY issued successfully through the web RCON prompt
- Player login and logout

Relayed lines always include the Controller-assigned source display name, for
example:

```text
(Dread Server) <Player> : hello
(Dread Server · TEAM) <Player> : defend
(Dread Server · WEB SAY) maintenance soon
(Dread Server · RCON SAY) match restarting
(Dread Server) <Player> joined the server.
```

In Fleet mode, Server Events also prefix every locally collected event with
the selected server's display name. Relayed events retain their origin label,
so login, logout, chat, match-state, command, and response lines remain
attributable when local and cross-server records share one console. A source
server never receives its own relayed in-game message; its local event record
provides the corresponding web-visible entry without creating an echo.

MORDHAU does not expose equivalent team membership across independent
servers. A relayed Team Chat line is therefore labeled `TEAM` but displayed
server-wide on each destination. External RCON clients' SAY commands are not
identifiable in `Mordhau.log`; RCON SAY synchronization covers commands issued
through this web manager. Web SAY and every relayed message use the server-only
Unicode Bridge. Login/logout routing also closes any still-tracked live
sessions when the game process stops or the active log is replaced. Delivery
order is retained per destination server.

Changing a server back to `Standalone` closes its fleet transport while
preserving its pairing configuration for a later authorized role change.

## Player History and Moderation

The Players panel reconstructs connection history from archived
`Mordhau_<timestamp>.log` and `Mordhau_<timestamp>.log.xz` files plus the
current `Mordhau.log`, then follows new records while the web manager is
running. XZ archives are decompressed as a stream and are never extracted to
disk. Login requests are correlated with successful authentication by PlayFab
ID and are the only source allowed to introduce or reprioritize a persistent
nickname. UTF-8 chat identity and the authoritative game-connection close
record remain available for server-event and session correlation without
changing nickname history. Archived logs are fingerprinted after a successful
import, and a `.log` to `.log.xz` conversion retains the same archive identity
so it cannot duplicate sessions.

Persistent history is stored with mode `0600` at:

```text
/root/mordhau/.manager/players.json
```

The player list is ordered by the most recent successful connection. Search
matches PlayFab ID, the latest nickname, and every retained historical
nickname. Administrators can order results in either direction by last
connection or accumulated server time. A selected record shows its last
connection, accumulated completed and current-session time, nickname history,
last observed general MORDHAU account level, and canonical IPv4 or IPv6
addresses. It also shows up to 200 recent connection sessions with join, leave,
duration, address, and locally resolved location. The same level appears as a
visually distinct badge between the country flag and nickname in the player
list. Browser-local date and time formatting is used for displayed timestamps.

When at least one PlayerController is present, the dashboard Players card
opens a live directory containing each player's PlayFab ID, nickname, country
flag, last observed account level, normalized Steam/Epic/Unknown platform, and
current ping. Selecting a row opens that player's persistent profile. The
directory is disabled when the Runtime bridge is unavailable or no
PlayerController is present.

The native Runtime bridge reads XP through the supported server build's
`UMordhauInventory::GetPlayerXP` implementation and converts it with
`UMordhauUtilityLibrary::GetRankFromXP`. The web manager samples that result
when a player is connected, retries transient inventory unavailability, and
refreshes a still-connected player every five minutes. The last valid account
level is retained in player history; an unavailable value is shown as not
observed rather than inferred from `ReplicatedRank`, Duel rank, or Teamfight
rank.

When the live `PlayFabPlayer` identity identifies a Steam account, the
validated 17-digit SteamID64 is retained with the player record. Player
Profile shows an inline Steam account link that opens the corresponding
`steamcommunity.com/profiles/<SteamID64>` page in a new tab. This identity
comes from the running server and does not require a Steam Web API key or an
external profile lookup. Epic identity is normalized and retained as an Epic
badge without constructing a public profile URL. Missing or unsupported
identity providers are shown as Unknown.

Country flags and approximate country, administrative region, and city names
come from the free monthly DB-IP City Lite MMDB database. The web manager
downloads and verifies the current edition on first use, retains a working
older edition if a newer download fails, and checks daily for a new monthly
edition. DB-IP City Lite is used under the
[Creative Commons Attribution 4.0](https://creativecommons.org/licenses/by/4.0/)
license, and the Players panel includes the required DB-IP attribution link.
Player addresses are looked up only in the local database; they are not
submitted to an external geolocation API.

The downloaded database and update state use mode `0600` under:

```text
/root/mordhau/.manager/geoip
```

Private addresses are never looked up. Public-looking addresses used by an
internal NAT, VPN, or test network can be excluded by adding one IP address or
CIDR prefix per line to:

```text
/root/mordhau/.manager/geoip/ignore-networks
```

Blank lines and lines beginning with `#` are ignored. Restart only
`mordhau-web` after editing the file. GeoIP results are approximate and may
identify a carrier, VPN endpoint, or upstream gateway instead of a player's
physical location.

Every authenticated web account can add a player comment. Each comment stores
the responsible account and creation time. Comment text is displayed as text,
not HTML. Mute and ban controls accept a whole-minute duration from `0` to
`525600`; zero means permanent until manually reversed. A timed restriction is
applied as a regular server restriction, then retained with its reason,
responsible administrator, and expiry time so the manager can automatically
reverse it. The requested state is queried from the server before success is
reported, and persisted lease state is restored if RCON application or
confirmation fails.

The action panel can kick a currently connected player with an optional ASCII
reason or broadcast a clearly addressed Unicode administrator warning through
the Unicode Bridge. These controls require a running game server, working
local RCON configuration, and, for warnings, a working Unicode Bridge.

Player IP history is sensitive administrative data and is available only
after the same network-policy and account authentication checks as the rest
of the manager.

## Runtime Reflection

The Runtime panel obtains its data from the native Windows bridge installed as:

```text
/root/mordhau/Mordhau/Binaries/Win64/dxgi.dll
```

The managed launcher enables that DLL only when
`MordhauServer-Win64-Shipping.exe` matches the supported SHA-256 digest. The
DLL performs additional PE-header and hook-prologue checks before attaching.
If a Steam update changes the executable, the dedicated server starts with
Wine's built-in DXGI implementation and the Runtime panel reports that the
bridge is unavailable. Updated bridge offsets must be released for the new
server build before runtime access is re-enabled.

The bridge discovers the active authority GameMode, GameState, every live
PlayerController, each controller's PlayerState, and its possessed Pawn from
the current `UWorld`. Properties are grouped by the actual runtime class and
each superclass. The web view includes Unreal property type and flags, static
array index, exported text value, RepIndex, RepNotify function, lifetime
condition, and effective replication scope. Connected controllers also expose
their current `PlayerNamePrivate` and PlayFab ID to the authenticated manager,
which groups each player's controller, state, and Pawn under one accordion.
Opening one player closes the previously open player.

Runtime search matches property names, Unreal types, declaring classes, and
the current exported value. A numeric query such as `100` therefore includes
properties whose displayed value contains `100`, subject to the selected
declaring-class and editable-only filters.

The editor derives its control from the reflected property type. Boolean
properties use an exact `True`/`False` selector. Enum-backed byte and enum
properties use choices read from the property-associated `UEnum`. Signed and
unsigned integer controls enforce the corresponding 8-, 16-, 32-, or 64-bit
range without converting 64-bit values through browser floating-point
numbers. Float and double controls accept only finite decimal or scientific
notation within their type range. Name, string, text, struct, array, set, and
map values use controls appropriate to their exported representation, with
balanced structured-text checks where applicable.

Changes are serialized by the Go server and executed after `UWorld::Tick` on
the game thread. Every change contains the value originally loaded by the
browser; a concurrent change causes a conflict instead of overwriting newer
state. Before forwarding a change, the Go server resolves the property
metadata again and enforces the same type, range, and enum constraints as the
browser. The bridge then imports the replacement through Unreal's property
system, verifies the resulting value, and restores the original value after
an import failure. For a replication-eligible Actor field it then calls
`AActor::FlushNetDormancy` followed by `AActor::ForceNetUpdate`.

Object and class references, interfaces, delegates, field paths, deprecated
and editor-only fields, function parameters, engine-internal Blueprint frame
storage, and unexportable values remain read-only. The UI also distinguishes
server-only fields from Net fields and displays the configured lifetime
condition.

Arbitrary runtime mutation cannot make a non-Net property replicate. GameMode
exists only on the server. Net fields remain subject to Unreal's replication
layout, ownership, actor relevancy, and lifetime condition; the bridge flushes
Actor net dormancy before requesting the immediate update.
`InitialOnly` values do not update clients that are already connected.

The bridge writes its one-second status sample and root-only request/response
IPC under `/root/mordhau/.manager/runtime`. The Go manager reads the shared
status sample once, includes the PlayerController count in its existing
authenticated event stream, serializes property requests, and caches identical
target views briefly so simultaneous administrators do not multiply
game-thread work.

## Trusted Reverse Proxies

Direct access remains enabled and is the default. For a direct request, the
manager uses the canonical TCP peer address and ignores `X-Forwarded-For`,
`X-Real-IP`, and `Forwarded`, including malformed or forged values.

Trusted reverse proxies are configured in:

```text
/root/mordhau/.manager/trusted-proxies
```

The installer creates this mode-`0600` file empty, so no proxy is trusted by
default. Add one IP address or CIDR prefix per line and restart only the web
service. For example:

```sh
printf '%s\n' '192.0.2.10/32' > /root/mordhau/.manager/trusted-proxies
chmod 0600 /root/mordhau/.manager/trusted-proxies
rc-service mordhau-web restart
```

The launcher passes each entry as a repeatable `--trusted-proxy` startup
option. IPv4, IPv6, and IPv4-mapped IPv6 values are parsed structurally and
used canonically.

A request from a trusted TCP peer must contain exactly one
`X-Forwarded-For` header holding one IP address. Missing or empty values,
duplicate header fields, comma-separated chains, malformed addresses, IPv6
zone identifiers, unspecified addresses, and multicast addresses receive
HTTP 400 before session authentication. The manager uses that validated
address for access rules, emergency access, login throttling, lifecycle
request attribution, and audit records. The original TCP peer is retained
separately as `peer_ip` in the root-only audit log.

A compatible reverse proxy must replace, rather than append to, the forwarded
client address:

```nginx
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header Forwarded "";
client_max_body_size 8194m;
proxy_request_buffering off;
proxy_send_timeout 2h;
proxy_read_timeout 2h;
```

TLS may terminate at the reverse proxy while the built-in listener continues
to serve HTTP on the trusted internal network. Unproxied HTTP access to the
configured `0.0.0.0:<port>` listener continues to work under the same network
access policy. The body-size and buffering directives allow the proxy to
stream the manager's 8 GiB maximum PAK upload plus multipart framing.

## Mobile Layout

The dashboard and login page use the same authenticated endpoints on desktop
and mobile browsers. At widths of 720 pixels and below, controls use
touch-sized targets, technical inputs use a 16-pixel font, and multi-column
forms reflow without page-level horizontal scrolling. The header retains
server and account status, while the section tabs remain horizontally
scrollable.

At phone widths, INI rows, account and network-rule actions, mod controls,
dependency details, port fields, and server-event records stack vertically.
Long technical values wrap within their cards. Viewport safe-area insets are
applied for notched displays and standalone browser windows.

## Web Audit Log

The web manager writes a dedicated JSON Lines audit log to:

```text
/root/mordhau/log/mordhau-web.log
```

The file is created with mode `0600`. Every record contains a local RFC 3339
timestamp with second precision, an event name, and the responsible account.
HTTP access records also contain the canonical client IP address, direct TCP
peer IP address, method, path, response status, response size, and request
duration.

Dedicated events identify login success, login failure, logout, server
actions and their completion, crash detection and recovery, recovery and
monitoring policy changes, webhook tests, language changes, initial-map and
port changes, mod configuration and dependency removal, mod.io connection
changes, manual metadata refreshes, server-wide refresh-setting changes,
active-mod update detection, restart-policy notices, automatic restart
requests, Game.ini and Engine.ini mutations, MapRotation changes,
pending-configuration removal, CustomPak upload and staged-state changes,
Unicode server-message sends, timed or permanent player mute and ban changes,
player kicks and warnings, attributed player comments, administrative RCON
command success or failure, account changes, network-policy changes, runtime
property changes and failures, OpenRC boot-mode changes, and saved web-port
changes.
Requests without a valid session use the account name `unauthenticated`.

Passwords, request bodies, session cookies, CSRF tokens, RCON credentials,
configuration values, and configuration revisions are not written to the
audit log. Configuration events identify the file, operation, section, and
key without recording its value. Unicode server-message audit events record
only UTF-8 byte and character counts, not message text. Network-rule events
record whether a comment is present and its character count without recording
the comment text. RCON command audit events record the command name, character
and byte counts, response-line count, truncation state, and outcome without
recording command arguments or response text. Runtime-property audit events
identify the target kind and class, declaring class, property, static-array
index, replication scope, and outcome without recording the old or new value.
Player-comment audit events record the PlayFab ID and outcome without
recording comment text. Moderation events identify the PlayFab ID, action,
duration, and outcome without recording the reason or warning text. Webhook
events never record the configured destination.

## Persistent Dashboard History

The latest lifecycle operation is stored in:

```text
/root/mordhau/.manager/operation.json
```

The mode-`0600` state includes the fixed action, requesting account, canonical
requesting client IP, start and finish times, result, and captured command
output. The dashboard reloads that state for later sessions and after
web-service restarts. If the web manager stops before a running operation
records its result, the next start preserves the operation and marks it as
interrupted instead of leaving it permanently running.

Every accepted server event is appended as UTF-8 JSON Lines to:

```text
/root/mordhau/log/mordhau-rcon.log
```

The filename remains compatible with installations that used RCON broadcasts
as the event source. Each mode-`0600` record contains a monotonic sequence,
source or receipt timestamp, event kind, and text. The log is retained across
web-service restarts. A newly connected administrator receives the latest 400
events once, then continues from the authenticated event stream without
repeatedly transferring the entire on-disk history. The browser retains the
same 400-event Server Events window. Administrative commands, requesting
account names, returned response lines, no-output results, failures,
output-truncation notices, and SAY messages use this same history.
Fleet responses add source-server labels at read time without rewriting stored
records. Legacy player-lifecycle text that contains a second server-local
timestamp is compacted for Fleet display; the event timestamp beside each
line remains formatted in the viewing browser's locale and time zone.

## Recovery and Monitoring

Managed server actions record the intended game-process state as `running` or
`stopped`. When the process exits while the desired state remains `running`,
the web manager records the last launch PID, start time, observed uptime, and
a bounded tail of the server console, then schedules recovery. Intentional
stops, stopped-only updates, and disabled recovery are never restarted.

Recovery is enabled by default with three attempts in a rolling 30-minute
window. Attempts use exponential delays beginning at 15 seconds and capped at
five minutes. Recovery launches the last Steam-validated installation directly
and does not run SteamCMD or apply staged INI/CustomPaks changes. Exhausted
recovery remains visible until an administrator uses the authenticated manual
retry control or starts the server normally. The retry budget and window are
configurable from the Monitoring panel.

Recovery state uses mode `0600` at:

```text
/root/mordhau/.manager/server-desired-state
/root/mordhau/.manager/recovery.json
/root/mordhau/.manager/recovery-state.json
/root/mordhau/.manager/runtime/server-launch.json
```

The same panel displays 24-hour and seven-day charts for CPU, memory, swap,
the filesystem containing `/root/mordhau`, and connected PlayerControllers.
One server-side collector samples each minute and appends a seven-day,
mode-`0600` history to:

```text
/root/mordhau/.manager/metrics-history.jsonl
```

Audit and Server Events records can be filtered by source, text, account,
event kind, and time range, then exported as bounded UTF-8 JSON Lines. A
separate Game logs source searches the active raw game log and finalized
`.log` or `.log.xz` archives, including each original line and archive name.
Compressed archives are streamed through XZ without extraction, and only one
game-log search runs at a time. The manager can rotate both control logs at a
configured size, retain a configured number of backups, and remove archived
game logs in either format when they exceed the configured retention period.
Rotation, compression, search, and retention do not alter the active MORDHAU
`Mordhau.log`.

An optional HTTPS webhook can receive crash, exhausted-recovery,
disk-threshold, and mod-refresh-failure alerts. Each alert type has a six-hour
cooldown. The destination must resolve entirely to public addresses; requests
use TLS 1.2 or newer, no proxy, no redirects, pinned resolution for that
delivery, bounded body handling, and bounded timeouts. The saved destination
is never returned to the browser. Monitoring policy is stored with mode `0600`
at `/root/mordhau/.manager/monitoring.json`.

## Languages

The launch selector supports:

- English (`en`)
- German (`de`)
- Spanish (`es`)
- Simplified Chinese (`zh-Hans`)
- French (`fr`)
- Italian (`it`)
- Portuguese (`pt`)
- Russian (`ru`)
- Korean (`ko`)
- Traditional Chinese (`zh-Hant`)

The Server Events collector follows UTF-8 records from:

```text
/root/mordhau/Mordhau/Saved/Logs/Mordhau.log
```

It emits authenticated player login and logout, chat, match-state, killfeed,
scorefeed, and punishment records. Login-request identities are correlated by
player ID with later authentication and disconnect records, preserving a
UTF-8 player name even if a secondary identity line is lossy. Source
timestamps are retained as the event time and displayed in each browser's
locale and time zone. New player-lifecycle text does not repeat that timestamp
inside the message. The follower handles partial writes, truncation, and
managed log replacement. When the web service starts while the game is
running, it scans existing records only to reconstruct connected-player state
and does not replay the historical file into the dashboard.

While no player is connected, `MatchState: Waiting to start` is omitted and
the collector retains only the first `MatchState: Leaving map`. Further idle
map-state records are omitted until a player authenticates. All match-state
events remain visible while at least one player is connected. When the final
player disconnects, a new empty-server window allows one more visible
`Leaving map`.

`bLogChat`, `bLogKillfeed`, and `bLogScore` under
`[/Script/Mordhau.MordhauGameMode]` are initialized to `True` when they are
missing or false. An item or whole section explicitly disabled through the
structured INI controls remains disabled; events governed by that setting are
then absent until it is enabled again.

The manager does not maintain an RCON broadcast subscription. RCON is opened
on demand for a command or SAY request and closed after its bounded response.
This avoids an idle `listen allon` connection while retaining full
administrative command access.

Administrative commands reject control characters, invalid UTF-8, empty
commands, commands longer than 512 characters or 2,048 bytes, and serialize
concurrent web requests. The manager collects matching response packets until
a short idle boundary, with an eight-second total deadline and limits of 128
KiB and 398 response lines. RCON packets are fully framed before text
decoding. Valid UTF-8 is preserved; selected-language legacy decoding is used
only for invalid UTF-8 payloads. Command text and returned output are retained
in root-only server-event history and are visible to every authenticated
administrator.

The manager combines the current Game.ini RCON password and port with the last
working settings stored in `/root/mordhau/.manager/rcon-last.json` using mode
`0600`. If Game.ini or the saved port is edited while the game is running, the
server continues using its in-memory settings until restart, so the last
working values remain available for on-demand commands and SAY messages. The
next successful request after restart replaces the saved state.

Outbound text from the Server Events prompt in SAY mode uses the bundled
MORDHAU Unicode Bridge. The manager validates UTF-8, rejects control
characters, limits each message to 512 Unicode characters and 2,048 UTF-8
bytes, and writes it to a mode-`0600` transient file under
`/root/mordhau/Mordhau/Saved/PlayerFiles`. The filename contains a random
24-digit token. Only the ASCII command `string unicode.say <token>` passes
through MORDHAU's RCON parser. The server actor validates the numeric token,
loads the corresponding UTF-8 file, and broadcasts the text to every connected
`MordhauPlayerController` by invoking the reflected `ClientReceiveMessage`
reliable client RPC through
`MordhauUtilityLibrary.CallFunctionByNameWithArgs`. The web request succeeds
only after the actor finishes the controller loop and returns `unicode.say ok`
on the `custom` RCON broadcast channel.

The spool directory uses mode `0700`. The manager removes each message file
after the send attempt and removes stale files matching the managed
24-digit-token filename format when the web service starts. Unrelated files
under `Saved/PlayerFiles` are not removed.

The bridge is installed under:

```text
/root/mordhau/Mordhau/Content/CustomPaks/MordhauUnicodeBridge-WindowsServer.pak
```

It is registered through `SpawnServerActorsOnMapLoad` and is not added to
Game.ini `Mods=` entries. Its actor is nonreplicated and disables client
network loading, so connected players do not download the bridge. Editable
Blueprint source, the cooked WindowsServer PAK, build instructions, and the
standalone installer are documented in `src/unicode-bridge/README.md`.

## Configuration Management

The web manager edits the server-generated files:

```text
/root/mordhau/Mordhau/Saved/Config/WindowsServer/Game.ini
/root/mordhau/Mordhau/Saved/Config/WindowsServer/Engine.ini
```

During installer initialization, missing
`[/Script/OnlineSubsystemUtils.IpNetDriver]` values are seeded with
`NetServerMaxTickRate=60` and `ConnectionTimeout=10.0`. Existing values,
disabled entries, and a disabled section are preserved.

The editor preserves duplicate keys, ordering, comments, and unrelated lines.
Disabled items and disabled-section state are stored independently in the
root-only file `/root/mordhau/.manager/disabled-ini-entries.json`. Disabled
items are omitted from the game-owned INI file, so a MORDHAU or Unreal Engine
configuration rewrite cannot discard the manager's reversible state.
Re-enabling an item restores its original `Key=Value` line at its logical
position. Disabling a section moves every active item in that section into the
persistent state; enabling the section restores all of them together. A
disabled section remains recoverable even if the game removes its empty
section header.

Edits made while the server is running are written to
`/root/mordhau/.manager/pending`, including a staged copy of the disabled-item
state, and applied together by the next managed start or restart. Discarding
staged configuration removes both parts. Direct edits made while stopped are
backed up under `/root/mordhau/.manager/backups`.

Upgrades automatically migrate legacy
`; MORDHAU_MANAGER_DISABLED: Key=Value` markers into persistent state while
leaving ordinary user-authored comments untouched. A backup containing legacy
markers can be selectively recovered while no staged configuration exists:

```sh
rc-service mordhau-web stop
/root/mordhau/bin/mordhau-web \
  --recover-disabled-from /root/mordhau/.manager/backups/Game.ini.example.bak \
  --recover-file Game.ini \
  --recover-section '/Script/Mordhau.MordhauGameMode' \
  --recover-key MapRotation
rc-service mordhau-web start
```

Disabling `RconPassword` intentionally makes RCON commands and SAY unavailable
after the next server start until the item is enabled again. `Mordhau.log`
server events remain available.

The generated files remain the source of truth for the installed MORDHAU
version. The repository does not install a static gameplay-configuration
template.

## Map Rotation

The Configuration panel includes a visual editor for every `MapRotation=`
entry under:

```text
[/Script/Mordhau.MordhauGameMode]
```

The add controls use the same server-generated installed-content catalog as
live map travel, including shipped maps, enabled mod.io content, and active
CustomPaks. Existing entries can be reordered by drag-and-drop or buttons,
enabled, disabled, or removed. A map that is no longer available remains
visible and editable instead of being discarded. Maps that validly appear
under more than one game mode are retained as installed multi-mode entries.

Saving uses the current Game.ini revision and fails on a concurrent edit.
Unrelated sections, entries, comments, ordering, duplicate MapRotation values,
and persistent disabled-item identities are preserved. When the whole
MordhauGameMode section is disabled, every MapRotation entry is treated as
disabled until the section is enabled again. Changes made while the game is
running remain staged and apply on the next managed start or restart.

## Mod Management

The Mods panel manages `Mods=<Resource ID>` entries only within:

```text
[/Script/Mordhau.MordhauGameSession]
```

A numeric Resource ID can be added without an API key. Configuring a mod.io
API key also enables MORDHAU mod-page URL and name-ID lookup, current modfile
metadata, and recursive dependency inspection. Dependencies are deduplicated,
validated as public live MORDHAU resources, and inserted before the selected
mod. Existing entries are not duplicated.

Each configured mod displays its recursive mod.io dependency list and whether
each dependency is enabled, disabled, or absent from Game.ini. An enabled mod
shows a warning when a required dependency is disabled or absent. Disabled
mods retain dependency information without producing unresolved-dependency
warnings. Available metadata includes the current modfile ID, version,
publication and update dates, and file size.

The server refreshes the configured-mod cache every 5 minutes by default for
newly created state. A previously selected interval is preserved during
upgrades. The interval can be set from 1 to 10,080 whole minutes and is shared
by every administrator. The server performs one metadata/dependency refresh
regardless of how many browsers are connected. Concurrent manual requests join
the same in-progress refresh instead of starting additional mod.io requests.

After a successful refresh, the full interval starts again from that success
time. A failed attempt retains the previous successful timestamp and uses a
separate retry delay capped at five minutes. The page displays the last
successful refresh and next refresh or retry as absolute date/time values
formatted in each browser's locale and time zone.

Completed refreshes and interval changes increment a shared revision sent over
the existing authenticated event stream. Connected administrator pages then
read the server cache and update without another mod.io lookup. The interval
is stored in `/root/mordhau/.manager/mod-refresh.json` with mode `0600`.

After a valid API key is saved, the Mods page can enable automatic server
restart on active-mod updates. The option is disabled by default. A successful
metadata refresh compares each enabled mod's current mod.io `modfile.id` with
the preceding successful baseline. The first successful lookup establishes
the baseline without scheduling a restart. Newly enabled and disabled mods do
not create a false update event.

Three restart policies are available:

- **10-minute countdown** immediately schedules a restart and sends English
  10-, 5-, 4-, 3-, 2-, and 1-minute notices through the Unicode Bridge.
- **When the server is empty** waits until the Runtime bridge reports zero
  PlayerControllers continuously for 30 seconds, then announces and restarts.
  Runtime unavailability never counts as an empty server.
- **Scheduled server time** selects the next occurrence of a server-local
  `HH:MM` time that allows the complete ten-minute countdown.

At the deadline the manager announces the restart and invokes the managed
`restart` action, which validates the Steam installation and lets MORDHAU
obtain active mod updates during startup. Additional active-mod updates join
the existing schedule without postponing it.

The baseline and pending countdown are stored with mode `0600` in:

```text
/root/mordhau/.manager/mod-update-state.json
```

Restarting only the web service resumes a valid countdown. Disabling the
option, clearing the API key, stopping or replacing the game process, or
starting another lifecycle operation cancels the pending automatic restart.
An update discovered while the game server is stopped updates the baseline
without scheduling an unnecessary restart.

The API key and API path are stored in
`/root/mordhau/.manager/modio.json` with mode `0600`. The key is not returned
to the browser or written to the audit log. Requests are restricted to HTTPS
mod.io API hosts, redirects are disabled, and response size and request time
are bounded.

Disabling a configured mod retains its Resource ID as an inactive INI entry.
Removing a mod first opens a revision-bound plan. The target is always removed;
recursively required dependencies can also be selected only when they are
configured and no configured mod outside the removal set requires them.
Shared dependencies are retained and identified by their remaining parents.
If dependency metadata is incomplete, the conservative plan removes only the
target. The selected IDs are revalidated against the current graph and Game.ini
revision before one atomic configuration mutation. The game server downloads
active mods during its normal startup process.

## Live Map and Game Mode

The dashboard reads `LoadMap` and game-class records from the authoritative
`Mordhau.log` stream. While the native Runtime bridge is ready, its current
GameMode class takes precedence over the last log-derived class. These values
are collected once by the web manager and distributed through the shared
snapshot; connected browsers do not independently inspect the game process.

The Change map dialog builds a server-side catalog from shipped
`*WindowsServer.pak` files, enabled mod.io resources, and active CustomPaks.
Known official mode prefixes provide a fast classification path. An official
map without a conventional prefix is still included when its packaged default
GameMode is unambiguous; map names such as `LiteMordhauTestLevel` therefore do
not need an `FFA_` prefix to appear under Deathmatch. Internal initialization,
main-menu, and base GameMode destinations are omitted. A mod map is offered
only when its name is declared by available mod.io metadata and its packaged
default GameMode is unambiguous. An active CustomPak without mod.io metadata
is offered only when its packaged default GameMode can be identified
unambiguously. A PAK that cannot be inspected is omitted and reported as a
catalog warning.

PAK indexes and selected map assets are read with checksum-pinned `repak`
0.2.3 under bounded command time and output limits. The helper is installed
at `/root/mordhau/bin/repak.exe`; its Apache-2.0 and MIT license texts are
stored under `/root/mordhau/licenses/repak`. The resulting catalog is cached
by installed-content fingerprint and rebuilt after relevant PAK or enabled
mod state changes.

The browser submits only a catalog mode ID and exact map name. The manager
revalidates that pair against its current catalog, requires a running game
server and authenticated CSRF-protected session, then issues only
`changelevel <validated-map-name>`. The selected map's packaged default
GameMode controls the destination mode. The requester and canonical client
address are audited, while the command result is retained in Server Events.

## Launch Map and Ports

The dashboard stores an optional initial map and passes it immediately after
`MordhauServer.exe`. An empty value leaves map selection to MORDHAU.

Managed launches use these default ports:

| Purpose | Default | Launch parameter | Protocol |
| --- | ---: | --- | --- |
| Game traffic | 7777 | `-Port=` | UDP |
| RCON | 7778 | `-RconPort=` | TCP |
| Beacon | 15000 | `-BeaconPort=` | UDP |
| Steam query | 27015 | `-QueryPort=` | UDP |

All values must be between 1 and 65535, must be unique, and must differ from
the saved web-service port. Changes apply at the next managed start or
restart. The Steam query listener is used when Steam server advertising is
enabled in MORDHAU configuration.

The selected map and ports are stored with mode `0600` under
`/root/mordhau/.manager`. On-demand web RCON and SAY requests automatically
follow the saved RCON port. The saved dashboard RCON port is also synchronized
to `RconPort` in `Game.ini`. A running server receives that INI change through
the pending configuration applied on its next managed start or restart;
existing disabled-entry or disabled-section state remains disabled.

## OpenRC

Service names:

```text
mordhau-server
mordhau-web
```

Manual and automatic boot modes can be selected in the web manager. Equivalent
commands are:

```sh
rc-update add mordhau-server default
rc-update del mordhau-server default
rc-update add mordhau-web default
rc-update del mordhau-web default
```

Changing a boot mode does not change the current process state.

## Management and Dedicated Server Updates

The web manager checks the latest stable GitHub Release for this repository
and the public Steam build for MORDHAU Dedicated Server once per hour. Check
timestamps and results are stored on the server, so connected browsers read
one shared result and do not independently contact GitHub or Steam. Manual
checks are authenticated, CSRF-protected server requests.

A newer release or Steam build appears in a shared banner at the top of the
web manager. A management update downloads the versioned release archive and
`SHA256SUMS` from the fixed project Release origin, verifies the archive
checksum, validates archive paths and the embedded installer version, and
runs the installer in a detached process. The update state survives the web
service replacement, and interrupted work is reported after the next start.
A dedicated-server update uses the existing managed `restart` action while
the server is running or the stopped-only `update` action while it is stopped.
Detached updater output is retained at:

```text
/root/mordhau/.manager/runtime/manager-update.log
```

Both automatic options are disabled by default and are stored server-wide in:

```text
/root/mordhau/.manager/automatic-updates.json
```

Each automatic update option has its own restart policy:

- **10-minute countdown** starts immediately and announces the target in
  English at 10, 5, 4, 3, 2, and 1 minute through the Unicode Bridge.
- **When the server is empty** announces that an update is ready, waits for
  the Runtime bridge to report zero PlayerControllers continuously for
  30 seconds, and then performs the update.
- **Scheduled server time** selects the next server-local `HH:MM` that permits
  the complete ten-minute announcement sequence. A missed announcement window
  is moved to the next valid server-local occurrence instead of restarting
  immediately without notice.

A stopped server is updated without an unnecessary countdown. If management
and Dedicated Server updates are available together, the checksum-verified
management installer also updates the Dedicated Server, so the management
update policy controls that combined maintenance window. Automatic updates do
not overlap another lifecycle operation, an active mod-update restart, or an
active scheduled-restart countdown.

The Dashboard can also enable recurring Dedicated Server restarts for selected
weekdays and one server-local time. This feature is disabled by default and is
stored in:

```text
/root/mordhau/.manager/scheduled-restart.json
```

Each occurrence uses the same 10-, 5-, 4-, 3-, 2-, and 1-minute in-game
announcement sequence. A missed countdown is moved to the next selected day.
A stopped server is not started by the schedule. If a
mod update, automatic product update, detached management update, or manual
lifecycle operation already owns that maintenance window, the recurring
restart occurrence is skipped rather than causing a second restart.

An official MORDHAU Dedicated Server release can change executable signatures,
files, or directory layout. Such changes can temporarily disable the native
Runtime bridge or require compatible project updates for bridge, PAK, or
management integration. The web setting displays this warning before
automatic dedicated-server updates are enabled.

## Security Model

- The web manager runs as root because it controls Wine processes, OpenRC, and
  root-owned configuration files.
- State files, disabled INI items and sections, sessions, generated
  credentials, the last working on-demand RCON credential, pending
  configuration, lifecycle results, player connection history, the
  server-event history, and the web audit log use root-only permissions.
- Recovery desired state, launch diagnostics, retry history, moderation
  leases, monitoring policy, and metrics history use root-only permissions.
- DB-IP City Lite data, update state, and ignored-network configuration are
  root-only. Updates use a fixed HTTPS origin, reject redirects, enforce
  compressed and expanded size limits, preserve a filesystem reserve, verify
  the complete MMDB search tree, and replace the active database atomically.
- Player IP geolocation uses only that local MMDB reader; player addresses are
  not sent to DB-IP or another lookup API.
- CustomPaks upload and inactive storage are root-only. Package mutations share
  the lifecycle lock, reject paths and overwrite conflicts, and are not
  applied while a managed server launch is in progress.
- Live map requests accept only an exact server-generated catalog pair. PAK
  inspection uses direct process arguments, read-only `list`/`get` operations,
  bounded output, and a timeout; ambiguous mod map/game-mode pairs are omitted.
- Session tokens are stored as SHA-256 digests.
- Login attempts are rate-limited.
- CSRF tokens and same-site HTTP-only cookies protect authenticated changes.
- Browser requests explicitly marked as cross-site by Fetch Metadata are
  rejected without comparing the public URL to an internally rewritten Host
  header.
- Direct requests use the TCP peer and ignore all forwarding headers.
- Forwarded client addresses are accepted only from explicitly configured
  trusted proxy prefixes. A trusted request requires one structurally valid,
  single-address `X-Forwarded-For` value; invalid forms are rejected before
  authentication.
- Canonical client and TCP peer addresses are retained separately in request
  context and root-only audit records.
- Server Fleet is disabled by default. A Managed Server listener starts only
  after the role, Controller public identity, and exact Controller source IP
  are configured.
- Fleet transport is restricted to TLS 1.3 with mutually pinned Ed25519
  subject-public-key identities. The Controller initiates every connection,
  validates the Managed Server node ID, rejects redirects, and does not trust
  DNS names or public certificate authorities for fleet identity.
- A Managed Server independently verifies the Controller certificate and
  direct TCP peer address before handling a request. Fleet-provided canonical
  browser and TCP peer addresses are stored separately in request context and
  audit records; forwarded-address and browser credential headers are removed.
- Remote state changes require the authenticated Controller browser's original
  CSRF token before the Controller creates a separate internal request.
  Internal requests are limited to an explicit manager API registry and retain
  the Controller account, canonical browser IP, destination node ID, and
  request ID in root-only audit records.
- Fleet identity keys and settings use root-only files. Event routing requires
  both source and destination category opt-in, deduplicates event IDs, bounds
  per-destination queues and Unicode message size, preserves destination
  ordering, and never accepts a source display name from a Managed Server.
  Relay events contain no web credential, administrator identity, player IP,
  or PlayFab ID.
- The most specific CIDR block wins; inclusive IPv4 ranges are evaluated as
  exact minimal CIDR blocks. Deny wins an equal-prefix tie except for the
  active emergency exact-address allow.
- RCON connects to `127.0.0.1` from the web manager.
- Player mute and ban changes require authentication and CSRF validation and
  are accepted only after the running server's restriction lists confirm the
  requested state. Timed restrictions persist before the RCON mutation and
  restore their preceding state if application or confirmation fails.
- Every authenticated web account can issue commands with full RCON
  administrator authority. Command arguments and responses are retained in
  the root-only server-event history but excluded from the separate web audit
  log.
- Unicode message files and their spool directory are root-only. RCON carries
  only a numeric token; the bridge constructs a fixed filename prefix and
  extension and does not accept a path from the command.
- The Unicode Bridge command still requires authenticated RCON. An RCON client
  without local access to a corresponding spool file cannot supply message
  text through the token command. The RCON port and credential must remain
  restricted.
- The mod.io API key is never returned through a management endpoint.
- mod.io requests accept only validated HTTPS API hosts and do not follow
  redirects.
- Monitoring webhook destinations are never returned through a management
  endpoint. Delivery requires HTTPS, TLS 1.2 or newer, direct DNS-pinned
  connections, no redirects, bounded responses and timeouts, and exclusively
  public destination addresses.
- Runtime bridge IPC remains inside a mode-`0700` manager directory and is not
  exposed as a network listener.
- Runtime target identifiers include Unreal object index and serial number,
  and every request is restricted to actors rediscovered from the active
  `UWorld`.
- Runtime writes require authentication, CSRF validation, an expected current
  value, a supported importable property type, and a supported executable
  build. The separate web audit log records the account and canonical client
  address without recording property values.
- Lifecycle operations accept fixed actions and do not execute user-provided
  shell arguments.
- Management updates accept only the fixed project repository and canonical
  stable semantic-version tags, require the expected release assets, enforce
  download and extraction limits, reject unsafe archive entries, and verify
  the selected archive against `SHA256SUMS` before executing its installer.
- The detached management updater holds a process lock, records its final
  state before releasing that lock, excludes itself from installer shutdown,
  restores previously running services after installer failure, and prevents
  concurrent web lifecycle operations.
- Automatic recovery proceeds only when the root-only desired state is
  `running`, no lifecycle action is active, the process identity is absent,
  recovery is enabled, and the configured rolling retry budget permits it.

The built-in listener serves HTTP. Use a trusted network or a TLS reverse
proxy, and configure network access rules before exposing the web port.
Restrict external access to the MORDHAU RCON port with the host firewall.

## Testing

Repository tests:

```sh
cd src/web
go test ./...
go vet ./...
go test -race ./...
```

Shell validation:

```sh
sh -n src/mordhau-server-alpine-linux.sh
sh -n src/templates/server.sh
sh -n src/templates/webserver.sh
sh -n src/templates/openrc/mordhau-server
sh -n src/templates/openrc/mordhau-web
sh -n src/unicode-bridge/install.sh
sh -n src/unicode-bridge/build-windows-server.sh
sh -n src/tests/test-unicode-bridge-install.sh
sh -n src/runtime-bridge/build.sh
sh -n src/tests/test-runtime-bridge-build.sh
sh -n src/tests/test-log-compression.sh
sh -n src/tests/test-steamcmd-update.sh
sh -n src/tests/test-installer-versioning.sh
sh -n src/tests/test-installer-service-lifecycle.sh
sh -n src/tests/test-installer-update-worker.sh
./src/tests/test-unicode-bridge-install.sh
./src/tests/test-runtime-bridge-build.sh
./src/tests/test-log-compression.sh
./src/tests/test-steamcmd-update.sh
./src/tests/test-installer-versioning.sh
./src/tests/test-installer-service-lifecycle.sh
./src/tests/test-installer-update-worker.sh
```

Installed-service checks:

```sh
rc-service mordhau-server status
rc-service mordhau-web status
```

The Go tests cover random-password constraints, INI preservation, CIDR
precedence, inclusive IPv4 range normalization, exact boundary matching,
range/CIDR precedence, resolved-client emergency access, default-empty trusted
proxy configuration, direct-header spoofing resistance, strict single-value
forwarded IPv4/IPv6 parsing, proxy-aware access control, audit and login-limit
attribution, network-rule comment normalization and backward-compatible JSON,
audit-log permissions and secret exclusion, persistent item and whole-section
INI disable/enable round trips, duplicate ordering, virtual-section recovery,
legacy-marker migration, required game-log setting initialization and
explicit-disable preservation, RCON credential fallback order, administrative
command validation, bounded response packet collection, actor attribution and
audit argument exclusion, transport-status migration filtering, packet
framing, Korean legacy decoding, UTF-8 chat parsing, player-ID-based Unicode
login/logout identity correlation, canonical connection-address correlation,
idempotent archived/current player-history import, session-duration
accounting, attributed persistent comments, verified mute/ban command
handling, GeoIP edition and ignored-prefix validation, local location-record
normalization, fixed-origin download failure handling,
match/kill/score/punishment parsing, current map and game-mode parsing, idle
match-state suppression, streamed XZ archive reads, compressed archive
identity and retention, raw/XZ game-log search,
missing-log creation, partial writes, truncation, and log replacement,
ASCII-only Unicode token commands, UTF-8 message staging, spool permissions and
stale-file cleanup, bridge acknowledgements, input validation, start-map
validation, server-port parsing and collision checks, mod.io URL and API-path
validation, dependency ordering, scoped mod-entry mutation, shared-cache
deduplication under concurrent clients, successful-refresh interval resets,
failure retry behavior, lifecycle-result persistence, interrupted-operation
recovery, multilingual server-event history persistence, truncated-history
recovery, desired-state crash detection, intentional-stop exclusion,
retry-window pruning, exponential recovery delays, retry exhaustion,
one-minute metric history and retention, log rotation and search, webhook
destination policy and cooldown, timed moderation expiry, rollback after
failed RCON confirmation, connection-session timelines, dependency-aware mod
removal, countdown, empty-server, and scheduled mod restart policies,
automatic-update policy migration and execution, weekday restart selection,
scheduled-restart countdown persistence, and maintenance-window exclusion.
CustomPaks tests cover visible project-managed package protection,
staged state, activation and deactivation moves, deletion and cancellation,
upload limits, case-insensitive duplicate rejection, lifecycle locking, and
idempotent next-launch application. Runtime tests cover server-wide status
sampling, stale and stopped bridge state, target-view request serialization
and cache reuse, target-ID validation, player-identity placement,
multilingual property values,
type-derived editor selection, exact enum choices, integer boundary handling,
finite floating-point parsing, and structured-text delimiter validation.
Map-catalog tests cover packaged default-mode ambiguity, unprefixed official
maps, internal-destination rejection, declared mod-map scope, exact mode/map
pair validation, fixed RCON command construction, MapRotation ordering and
disabled-state preservation, duplicate identity rejection, multi-mode map
retention, requester auditing, and an opt-in check against installed shipped
and mod content. Player-level tests
accept only native inventory XP conversion,
validate the known `90435` XP to level `38` pair, reject replicated and
competitive-rank substitutes, normalize the earlier zero-value sentinel, and
retain the latest valid observation. Platform-identity tests enforce strict
SteamID64 validation, safe profile-link construction, Epic identity
normalization, and exact live-ping transport.
Server Fleet tests cover mode defaults, connection-key round trips, root-only
identity permissions, TLS 1.3 mutual Ed25519 pinning, expected Controller
source-IP enforcement, canonical browser-IP propagation with forwarding-header
spoof removal, node-scoped API allowlisting, outer-browser CSRF enforcement,
Unicode source labels, all five rendered event types, All/Team game-log
mapping, forced live-session logout, RCON SAY parsing, per-destination order,
source/destination event opt-in, local Server Events attribution, legacy
player-lifecycle timestamp compaction, and relayed-source preservation.
The shell integration tests cover PAK installation, active and staged Game.ini
registration, existing server-actor preservation, backup creation,
idempotent reinstallation, verified lossless XZ game-log compression,
archive permissions, interrupted-finalization recovery, collision
preservation, and idempotent compression maintenance. Static asset tests
verify the default-light theme
initializer, persistent theme, server-managed mod-refresh controls,
browser-time-zone timestamps, initial server-event history loading, the
unified RCON/SAY prompt, mobile viewport metadata, touch targets, input sizing,
safe-area handling, player-grouped Runtime navigation, type-specific Runtime
edit controls, current-value search, manually indicated value refresh,
narrow-screen control reflow, Players search/profile/moderation/comment
controls, connected-player dashboard navigation, timed restrictions, session
timelines, visual MapRotation controls, dependency-removal planning,
restart-policy selection, Monitoring charts and log tools, recovery controls,
CustomPaks upload and staging controls, management and Steam update banners,
server-wide automatic-update policy controls, recurring restart scheduling,
and mobile visibility of server and account status. Management-update tests
cover stable-release validation,
semantic version ordering, required assets, bounded downloads, checksum
selection, safe archive extraction, detached execution, interrupted-state
reconciliation, lifecycle exclusion, and service restoration. Steam-update
tests cover installed/public build parsing, server-wide result persistence,
lifecycle contention, and automatic-update scheduling. The native build test
compiles the Windows DLL twice, verifies deterministic output and its DXGI
proxy export, and pins the PDB-derived property-export signature,
enum-property layout, player identity fields, and net-dormancy entry point.

Connected-client Runtime validation covers the PlayerController count changing
from zero to one, discovery of the associated PlayerController, PlayerState,
and possessed Pawn under the controller nickname and PlayFab ID, reflected
type-editor metadata and enum sentinel filtering, authoritative readback after
a property write, client delivery of a `Net`/`OnRep` PlayerState string
property, normalized platform identity, live ping, dashboard-to-profile
navigation, and restoration of the original value.

## Update and Rollback

Run the installer from a newer release to update repository-managed scripts,
services, the checksum-pinned PAK inspection helper, the Unicode Bridge, the
supported native runtime bridge, and the Go web manager. The bundled PAK
replaces the repository-managed Unicode Bridge
version, and the required server-actor entry is added without duplicating
existing entries. A native runtime bridge is replaced only after the current
shipping executable passes its supported-build digest check. Existing
accounts, access rules, generated credentials, INI files, backups, logs,
language selection, initial map, server ports, trusted proxy settings, mod.io
settings, mod refresh interval and restart policy, CustomPaks state and
inactive/uploaded packages, player connection history, moderation leases and
comments, GeoIP database and ignored networks, recovery policy and history,
desired server state, monitoring policy, metrics history, latest lifecycle
result, server-event history, release and Steam check state, automatic-update
settings, scheduled-restart policy, Server Fleet role, identities, pairings,
aliases and event-sync policies, and boot modes are preserved.

The same installer can be started from the management-release banner. The
manual archive procedure remains available when the web manager is unavailable
or a controlled rollback is required.

To roll back management code:

1. Download the required earlier repository release.
2. Stop `mordhau-web` and `mordhau-server`.
3. Run that release's installer with `--allow-downgrade`.
4. Restore an INI backup from `/root/mordhau/.manager/backups` when a
   configuration rollback is also required.

Removing the OpenRC boot registrations:

```sh
rc-update del mordhau-server default
rc-update del mordhau-web default
```

MORDHAU files and manager state are not removed by those commands.

## License

Repository-authored source is licensed under the MIT License. See `LICENSE`.
This license does not grant rights to MORDHAU, SteamCMD, Wine, Unreal Engine,
or other third-party software.

## Credits

- MORDHAU and Triternion: https://mordhau.com/
- MORDHAU Dedicated Server on Steam: https://store.steampowered.com/app/629800/MORDHAU_Dedicated_Server/
- Unreal Engine and Epic Games: https://www.unrealengine.com/
- SteamCMD and Valve: https://developer.valvesoftware.com/wiki/SteamCMD
- mod.io: https://mod.io/
- DB-IP City Lite: https://db-ip.com/db/lite.php
- MaxMind DB Reader for Go: https://github.com/oschwald/maxminddb-golang
- Wine: https://www.winehq.org/
- repak: https://github.com/trumank/repak
- Author: itinfra7 (GitHub: https://github.com/itinfra7)
