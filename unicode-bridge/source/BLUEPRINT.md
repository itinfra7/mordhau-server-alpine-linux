# Blueprint specification

`MordhauUnicodeBridge/Content/BP_MordhauUnicodeBridge.uasset` is the editable
MORDHAU Editor source asset. It uses an Unreal Actor parent class with these
class defaults:

- Replicates: disabled
- Replicate Movement: disabled
- Net Load on Client: disabled

Its Event Graph performs the following operations:

```text
Event BeginPlay
  -> Get Game Mode
  -> Cast to MordhauGameMode
  -> Bind Event to OnRconStringCommand

HandleUnicodeRconCommand(Payload, ClientId)
  -> Starts With(Payload, "unicode.say ", Case Sensitive)
  -> Branch
  -> Right Chop(Payload, 12)
  -> Is Numeric
  -> Branch
  -> Append("mordhau-unicode-bridge-", numeric token)
  -> Load String from File(File Extension=".utf8")
  -> Branch on load success
  -> Get All Actors Of Class(MordhauPlayerController)
  -> For Each Loop
       -> Append("ClientReceiveMessage ", loaded text)
       -> Call Function By Name With Args(
            Str=appended command,
            Executor=Array Element
          )
  -> Loop Completed
  -> Send Message To Rcon Clients(
       Message="unicode.say ok",
       ClientId=ClientId,
       ToAll=false,
       TypeOfBroadcast=Custom
     )
```

The RCON client invokes MORDHAU's built-in
`string unicode.say <numeric-token>` command. `OnRconStringCommand` receives
only the `unicode.say <numeric-token>` payload. The payload prefix contains one
trailing space, so the `Right Chop` count is 12 characters.

The graph accepts only a numeric suffix and constructs the filename from the
fixed `mordhau-unicode-bridge-` prefix and `.utf8` extension. MORDHAU's
`LoadStringFromFile` reads that filename from `Saved/PlayerFiles`. The web
manager creates 24-digit random tokens and writes the corresponding files
before sending the command.

For each connected `MordhauPlayerController`, the graph constructs
`ClientReceiveMessage <loaded text>` and passes it to
`MordhauUtilityLibrary.CallFunctionByNameWithArgs` with that controller as the
executor. `ClientReceiveMessage` is the controller's reliable client RPC. The
acknowledgement is emitted only after the controller loop completes.
