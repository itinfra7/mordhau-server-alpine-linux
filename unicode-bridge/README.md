# MORDHAU Unicode Bridge

The MORDHAU Unicode Bridge carries outbound UTF-8 text through authenticated
RCON without sending non-ASCII bytes over MORDHAU's command parser. The web
manager writes each validated message to a root-only transient file under
`Mordhau/Saved/PlayerFiles`, then sends a random 24-digit file token through
MORDHAU's built-in string-command extension point:

```text
string unicode.say <24-digit-token>
```

The bridge receives `unicode.say <24-digit-token>` as the string-command
payload. The server actor validates the numeric suffix, loads
`mordhau-unicode-bridge-<24-digit-token>.utf8`, enumerates connected
`MordhauPlayerController` instances, and invokes each controller's reflected
`ClientReceiveMessage` function through
`MordhauUtilityLibrary.CallFunctionByNameWithArgs`. That function is the
controller's reliable client RPC. The bridge returns `unicode.say ok` on the
`custom` RCON broadcast channel to the requesting client after processing the
controller loop.

## Repository layout

- `source/MordhauUnicodeBridge` contains the editable Blueprint plugin source.
  `BP_MordhauUnicodeBridge.uasset` is the native MORDHAU Editor source asset.
- `dist/MordhauUnicodeBridge` contains the cooked WindowsServer PAK installed
  by the main repository installer.
- `build-windows-server.sh` cooks and packages the source with the official
  MORDHAU Editor through Wine.
- `install.sh` verifies, installs, updates, and registers the cooked bridge.

## Runtime model

The Blueprint is a nonreplicated Unreal Actor with client network loading
disabled. MORDHAU spawns it through the server-actor setting:

```ini
[/Script/Mordhau.MordhauGameMode]
SpawnServerActorsOnMapLoad=/MordhauUnicodeBridge/BP_MordhauUnicodeBridge.BP_MordhauUnicodeBridge_C
```

The installer does not add the bridge to `Mods=`. Connected players receive
text through the game's built-in player-controller message path and do not
download this server-only plugin.
The cooked PAK is installed in MORDHAU's automatically mounted server folder:

```text
Mordhau/Content/CustomPaks/MordhauUnicodeBridge-WindowsServer.pak
```

The web endpoint limits messages to 512 Unicode characters and 2,048 UTF-8
bytes and rejects control characters. The spool directory uses mode `0700`
and each message file uses mode `0600`. The manager removes the file after
each send attempt and removes stale files matching the exact managed filename
format at startup while preserving unrelated files.

RCON authentication and the web manager's account, CSRF, and network-access
controls remain mandatory. RCON carries only a numeric token, and the actor
constructs the fixed filename prefix and extension; the command cannot supply
a filesystem path or message text.

## Rebuild

Install the official MORDHAU Editor and provide the directory containing
`Mordhau/MordhauSDK.uproject` and `InstalledBuild/Windows`:

```sh
./unicode-bridge/build-windows-server.sh \
  --editor-root /path/to/MORDHAUEditor \
  --wine-prefix /path/to/editor-wineprefix
```

The script requires an initialized Wine prefix, Wine, and `winepath`; it uses
`xvfb-run` when available. It removes stale bridge cook outputs, validates the
new cooked assets, creates and tests the WindowsServer PAK, verifies its
expected mounted files, and refreshes `dist/SHA256SUMS`.

## Standalone installation

Stop the dedicated server, then run:

```sh
./unicode-bridge/install.sh --mordhau-root /root/mordhau
```

The main `mordhau-server-alpine-linux.sh` installer performs this operation
automatically. Existing configuration is preserved, changed INI files are
backed up under `.manager/backups`, and repeated installation does not add
duplicate server-actor entries.
