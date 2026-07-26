# MORDHAU Runtime Reflection Bridge

The runtime bridge is a Windows x86_64 `dxgi.dll` proxy loaded into the
MORDHAU Dedicated Server process by Wine. It exposes a root-only file IPC
interface used by the authenticated Go manager to inspect and change selected
live Unreal Engine actor properties on the game thread.

## Supported Server Build

The current bridge supports the MORDHAU Dedicated Server executable whose
SHA-256 digest is:

```text
a11348d6bfdb386d7f8a976a59e7d28d38b0d1ba2b9a2a7e0035ac28d53f885e
```

The launcher enables the native DLL override only when the installed
`MordhauServer-Win64-Shipping.exe` matches this digest. The DLL independently
checks the PE timestamp, image size, and hooked `UWorld::Tick` prologue before
installing the runtime hook. An unsupported game update therefore leaves the
Runtime panel unavailable instead of using stale engine offsets.

## Runtime Targets

The bridge permits only current instances discovered from the active
`UWorld`:

- Authority GameMode
- GameState
- PlayerControllers in `UWorld::PlayerControllerList`
- Each controller's PlayerState
- Each controller's possessed Pawn

Target identifiers combine the object kind, Unreal object index, and serial
number. Every request resolves the target again on the game thread, so an
identifier from a destroyed object cannot address a replacement object that
reuses the same index.

Properties are enumerated from the actual runtime class through its complete
superclass chain. Responses include the declaring class, property type,
static-array index, offset, flags, replication index, replication condition,
RepNotify function, exported value, and editability.

## Property Changes and Replication

Editable values use Unreal's native property text export and import formats.
A change request includes the value observed by the browser. The bridge
rejects the request if the current value has changed, imports the replacement
on the game thread, and verifies the resulting exported value. For a
replication-eligible Actor field, it then calls
`AActor::FlushNetDormancy` and `AActor::ForceNetUpdate`.

Object references, class references, interfaces, delegates, field paths,
deprecated fields, editor-only fields, function parameters, engine-internal
Blueprint frame storage, and values that cannot be exported are read-only.

These network-update calls do not make an arbitrary property replicable.
GameMode is server-only. A non-Net property changes only the authoritative
server instance. A Net property remains subject to its Unreal replication
condition, actor ownership, actor relevancy, and the active replication
layout. In particular, `InitialOnly` fields do not update clients that are
already connected.

## IPC

The bridge uses:

```text
/root/mordhau/.manager/runtime/runtime-bridge-request.txt
/root/mordhau/.manager/runtime/runtime-bridge-response.json
/root/mordhau/.manager/runtime/runtime-bridge-status.json
```

The containing directory uses mode `0700`. The Go manager serializes commands,
uses atomic request writes, validates response request IDs, limits response
sizes, and caches target views briefly so simultaneous administrators do not
multiply game-thread work. The status file is sampled once per second by the
bridge; the Go manager then shares the cached PlayerController count through
the existing authenticated event stream.

## Build

On Alpine Linux:

```sh
apk add mingw-w64-gcc
./src/runtime-bridge/build.sh /tmp/dxgi.dll
```

The installer builds and installs the DLL automatically when the dedicated
server executable matches the supported digest.
