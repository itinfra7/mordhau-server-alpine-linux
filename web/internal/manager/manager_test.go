package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

func TestRandomPasswordShape(t *testing.T) {
	for i := 0; i < 100; i++ {
		password, err := randomPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(password) != 8 {
			t.Fatalf("password length = %d", len(password))
		}
		if !strings.ContainsAny(password, "abcdefghijklmnopqrstuvwxyz") ||
			!strings.ContainsAny(password, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") ||
			!strings.ContainsAny(password, "0123456789") {
			t.Fatalf("password does not contain every required character class")
		}
	}
}

func TestIniRoundTripAndTargetedChange(t *testing.T) {
	original := []byte("; retained comment\r\n[One]\r\nMap=A\r\nMap=B\r\n\r\n[Two]\r\nKey=Old\r\n")
	document := parseIni(original)
	if !bytes.Equal(document.bytes(), original) {
		t.Fatal("unmodified INI did not round-trip exactly")
	}
	setIniValue(&document, "Two", "Key", "New")
	result := string(document.bytes())
	for _, retained := range []string{"; retained comment", "Map=A", "Map=B", "[One]", "[Two]"} {
		if !strings.Contains(result, retained) {
			t.Fatalf("missing retained INI content %q", retained)
		}
	}
	if strings.Count(result, "Map=") != 2 || !strings.Contains(result, "Key=New") {
		t.Fatal("targeted INI change damaged duplicates or failed")
	}
}

func TestDisabledINIEntriesRemainVisibleAndRoundTrip(t *testing.T) {
	original := []byte("[One]\r\nEnabled=A\r\n" +
		disabledEntryPrefix + "Disabled=B=C\r\n; Ordinary=Comment\r\n")
	document := parseIni(original)
	if !bytes.Equal(document.bytes(), original) {
		t.Fatal("INI containing a disabled entry did not round-trip exactly")
	}

	view := makeConfigView("Game.ini", original, false)
	if len(view.Sections) != 1 {
		t.Fatalf("visible section count = %d", len(view.Sections))
	}
	if len(view.Sections[0].Entries) != 2 {
		t.Fatalf("visible entry count = %d", len(view.Sections[0].Entries))
	}
	if !view.Sections[0].Entries[0].Enabled {
		t.Fatal("active entry was shown as disabled")
	}
	disabled := view.Sections[0].Entries[1]
	if disabled.Enabled || disabled.Key != "Disabled" || disabled.Value != "B=C" {
		t.Fatalf("disabled entry parsed incorrectly: %+v", disabled)
	}
}

func TestDisabledINIEntryCanBeEditedAndReenabled(t *testing.T) {
	line := formatConfigEntry("Option", "Original", false)
	key, value, enabled, ok := configEntryParts(line)
	if !ok || enabled || key != "Option" || value != "Original" {
		t.Fatal("disabled entry was not parsed")
	}
	if _, _, active := entryParts(line); active {
		t.Fatal("disabled entry was treated as active")
	}

	edited := formatConfigEntry("Option", "Edited", enabled)
	if !strings.HasPrefix(edited, disabledEntryPrefix) || !strings.HasSuffix(edited, "Option=Edited") {
		t.Fatalf("editing did not preserve disabled state: %q", edited)
	}

	reenabled := formatConfigEntry(key, value, true)
	if reenabled != "Option=Original" {
		t.Fatalf("unexpected re-enabled line: %q", reenabled)
	}
	if activeKey, activeValue, active := entryParts(reenabled); !active ||
		activeKey != key || activeValue != value {
		t.Fatal("re-enabled entry was not active")
	}

	lines := []string{"[One]", "Option=Original"}
	if err := setConfigEntryEnabled(lines, 1, false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lines[1], disabledEntryPrefix) {
		t.Fatalf("backend toggle did not disable entry: %q", lines[1])
	}
	if err := setConfigEntryEnabled(lines, 1, true); err != nil {
		t.Fatal(err)
	}
	if lines[1] != "Option=Original" {
		t.Fatalf("backend toggle did not re-enable entry: %q", lines[1])
	}
}

func TestDisabledRCONEntryRemainsIntentionallyDisabled(t *testing.T) {
	data := []byte("[/Script/Mordhau.MordhauGameSession]\n" +
		disabledEntryPrefix + "RconPassword" + "=Example9A\n" +
		"RconPort=7778\n")
	value, enabled, exists := iniEntryState(
		data,
		"/Script/Mordhau.MordhauGameSession",
		"RconPassword",
	)
	if !exists || enabled || value != "Example9A" {
		t.Fatal("disabled RCON password state was not retained")
	}
	if _, active := iniValue(data, "/Script/Mordhau.MordhauGameSession", "RconPassword"); active {
		t.Fatal("disabled RCON password was exposed as active")
	}
}

func TestAccessRulePrecedenceAndEmergency(t *testing.T) {
	now := time.Now()
	expires := now.Add(time.Minute)
	config := AccessConfig{
		BasePolicy: "all_deny",
		Rules: []AccessRule{
			{Action: "allow", Network: "10.0.0.0/8"},
			{Action: "deny", Network: "10.1.0.0/16"},
			{Action: "allow", Network: "10.1.2.0/24"},
		},
	}
	tests := []struct {
		ip      string
		allowed bool
	}{
		{"10.2.0.1", true},
		{"10.1.3.1", false},
		{"10.1.2.9", true},
		{"192.0.2.1", false},
	}
	for _, test := range tests {
		if got := accessAllowed(netip.MustParseAddr(test.ip), config, now); got != test.allowed {
			t.Fatalf("accessAllowed(%s) = %v", test.ip, got)
		}
	}

	config.Rules = append(config.Rules,
		AccessRule{Action: "deny", Network: "10.1.2.9/32"},
		AccessRule{Action: "allow", Network: "10.1.2.9/32", Temporary: true, ExpiresAt: &expires},
	)
	if !accessAllowed(netip.MustParseAddr("10.1.2.9"), config, now) {
		t.Fatal("active emergency rule did not keep the current IP allowed")
	}
	if accessAllowed(netip.MustParseAddr("10.1.2.9"), config, expires.Add(time.Second)) {
		t.Fatal("expired emergency rule was still effective")
	}
}

func TestRCONPacketFramingPreservesUTF8(t *testing.T) {
	text := "한국어 채팅 — Русский — 简体中文 — Français"
	var wire bytes.Buffer
	if err := writeRCONPacket(&wire, rconPacket{ID: 7, Type: rconResponseValue, Body: []byte(text)}); err != nil {
		t.Fatal(err)
	}
	packet, err := readRCONPacket(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if packet.ID != 7 || packet.Type != rconResponseValue || string(packet.Body) != text {
		t.Fatal("RCON packet framing changed multilingual UTF-8 data")
	}
}

func TestUnicodeRCONCommandUsesASCIIFileToken(t *testing.T) {
	token := "012345678901234567890123"
	command, err := unicodeRCONCommand(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range command {
		if value > 0x7f {
			t.Fatalf("RCON command contains non-ASCII byte 0x%02x", value)
		}
	}
	if !bytes.HasPrefix(command, []byte(unicodeBridgeCommandPrefix)) {
		t.Fatalf("unexpected Unicode bridge command: %q", command)
	}
	if !bytes.HasPrefix(command, []byte("string unicode.say ")) {
		t.Fatalf("command does not use MORDHAU's string extension point: %q", command)
	}
	payload := command[len(unicodeBridgeCommandPrefix):]
	if string(payload) != token {
		t.Fatalf("Unicode bridge token = %q", payload)
	}
}

func TestUnicodeMessageStagingPreservesUTF8AndPermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "PlayerFiles")
	message := "한국어 — Русский — 简体中文 — Français — 😀"
	token, path, err := stageUnicodeMessageAt(directory, message)
	if err != nil {
		t.Fatal(err)
	}
	if !validUnicodeBridgeToken(token) {
		t.Fatalf("invalid staged token %q", token)
	}
	expectedPath := filepath.Join(
		directory,
		unicodeBridgeFilePrefix+token+unicodeBridgeFileExtension,
	)
	if path != expectedPath {
		t.Fatalf("staged path = %q, want %q", path, expectedPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != message || !utf8.Valid(data) {
		t.Fatalf("staged UTF-8 message = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("staged file mode = %04o", info.Mode().Perm())
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0700 {
		t.Fatalf("spool directory mode = %04o", directoryInfo.Mode().Perm())
	}
}

func TestUnicodeBridgeSpoolCleanupIsStrict(t *testing.T) {
	directory := t.TempDir()
	staleName := unicodeBridgeFilePrefix +
		"012345678901234567890123" +
		unicodeBridgeFileExtension
	preservedNames := []string{
		"unrelated-player-file.txt",
		unicodeBridgeFilePrefix + "123" + unicodeBridgeFileExtension,
		unicodeBridgeFilePrefix + "01234567890123456789012x" + unicodeBridgeFileExtension,
		"." + staleName + ".tmp",
	}
	if err := os.WriteFile(filepath.Join(directory, staleName), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range preservedNames {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	managedDirectory := filepath.Join(
		directory,
		unicodeBridgeFilePrefix+"987654321098765432109876"+unicodeBridgeFileExtension,
	)
	if err := os.Mkdir(managedDirectory, 0700); err != nil {
		t.Fatal(err)
	}

	if err := cleanupUnicodeBridgeSpoolAt(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, staleName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale managed file was not removed: %v", err)
	}
	for _, name := range preservedNames {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("preserved file %q: %v", name, err)
		}
	}
	if info, err := os.Stat(managedDirectory); err != nil || !info.IsDir() {
		t.Fatalf("managed-looking directory was removed: %v", err)
	}
}

func TestUnicodeMessageValidationRejectsInvalidInput(t *testing.T) {
	for _, message := range []string{
		"",
		"   ",
		"line one\nline two",
		string([]byte{0xff}),
		strings.Repeat("a", unicodeMessageMaxRunes+1),
		strings.Repeat("한", unicodeMessageMaxBytes/3+1),
	} {
		if err := validateUnicodeMessage(message); !errors.Is(err, errInvalidUnicodeMessage) {
			t.Fatalf("validateUnicodeMessage(%q) error = %v", message, err)
		}
	}
}

func TestExecuteRCONCommandAuthenticatesAndSendsASCII(t *testing.T) {
	command, err := unicodeRCONCommand("012345678901234567890123")
	if err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	defer client.Close()

	serverResult := make(chan string, 1)
	go func() {
		defer server.Close()
		auth, err := readRCONPacket(server)
		if err != nil {
			serverResult <- err.Error()
			return
		}
		if auth.ID != 101 || auth.Type != rconAuth || string(auth.Body) != "SecretRcon9" {
			serverResult <- "unexpected authentication packet"
			return
		}
		if err := writeRCONPacket(server, rconPacket{
			ID:   auth.ID,
			Type: rconAuthResponse,
		}); err != nil {
			serverResult <- err.Error()
			return
		}
		listen, err := readRCONPacket(server)
		if err != nil {
			serverResult <- err.Error()
			return
		}
		if listen.ID != 102 || listen.Type != rconExecCommand ||
			string(listen.Body) != rconListenCustomCommand {
			serverResult <- "unexpected custom-broadcast subscription packet"
			return
		}
		if err := writeRCONPacket(server, rconPacket{
			ID:   listen.ID,
			Type: rconResponseValue,
			Body: []byte(rconListenCustomSuccess),
		}); err != nil {
			serverResult <- err.Error()
			return
		}
		exec, err := readRCONPacket(server)
		if err != nil {
			serverResult <- err.Error()
			return
		}
		if exec.ID != 103 || exec.Type != rconExecCommand || !bytes.Equal(exec.Body, command) {
			serverResult <- "unexpected Unicode bridge execution packet"
			return
		}
		if err := writeRCONPacket(server, rconPacket{
			ID:   exec.ID,
			Type: rconResponseValue,
			Body: []byte("Custom: " + unicodeBridgeAcknowledged),
		}); err != nil {
			serverResult <- err.Error()
			return
		}
		serverResult <- ""
	}()

	if err := executeRCONCommand(client, "SecretRcon9", command); err != nil {
		t.Fatal(err)
	}
	if result := <-serverResult; result != "" {
		t.Fatal(result)
	}
}

func TestRCONKoreanFallbackProducesUTF8(t *testing.T) {
	source := "한국어 알림"
	encoded, _, err := transform.String(korean.EUCKR.NewEncoder(), source)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeRCON([]byte(encoded), "ko")
	if decoded != source || !utf8.ValidString(decoded) {
		t.Fatalf("Korean fallback decode = %q", decoded)
	}
}

func TestMordhauChatLogLinePreservesUnicode(t *testing.T) {
	line := `[2026.07.23-20.23.14:871][328]LogGameMode: Display: (ALL) Name, WithComma, 0123456789ABCDEF: "한국어 — Русский — 简体中文"`
	chat, ok := parseMordhauChatLogLine(line)
	if !ok {
		t.Fatal("valid MORDHAU chat log line was rejected")
	}
	expected := "Chat: 0123456789ABCDEF, Name, WithComma, (ALL) 한국어 — Русский — 简体中文"
	if chat != expected {
		t.Fatalf("chat log conversion = %q", chat)
	}
}

func TestChatLogFollowerReadsFileCreatedAfterStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Mordhau.log")
	follower := &chatLogFollower{path: path}
	chats, err := follower.readNewChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 0 {
		t.Fatalf("missing log returned chats: %#v", chats)
	}

	line := `[2026.07.23-20.00.00:000][1]LogGameMode: Display: (ALL) Player, 1: "첫 메시지"` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	chats, err = follower.readNewChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0] != "Chat: 1, Player, (ALL) 첫 메시지" {
		t.Fatalf("newly created log chats = %#v", chats)
	}
}

func TestChatLogFollowerStartsAtEndAndFollowsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Mordhau.log")
	oldLine := `[2026.07.23-20.00.00:000][1]LogGameMode: Display: (ALL) Old, 1: "old"` + "\n"
	if err := os.WriteFile(path, []byte(oldLine), 0600); err != nil {
		t.Fatal(err)
	}

	follower := &chatLogFollower{path: path}
	chats, err := follower.readNewChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 0 {
		t.Fatalf("initial historical chats were replayed: %#v", chats)
	}

	newLine := `[2026.07.23-20.00.01:000][2]LogGameMode: Display: (TEAM) Player, 2: "새 메시지"`
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(newLine); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	chats, err = follower.readNewChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 0 {
		t.Fatalf("partial log line was emitted: %#v", chats)
	}

	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	chats, err = follower.readNewChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0] != "Chat: 2, Player, (TEAM) 새 메시지" {
		t.Fatalf("new chat lines = %#v", chats)
	}

	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	rotatedLine := `[2026.07.23-20.00.02:000][3]LogGameMode: Display: (ALL) Player, 2: "회전 후 메시지"` + "\n"
	if err := os.WriteFile(path, []byte(rotatedLine), 0600); err != nil {
		t.Fatal(err)
	}
	chats, err = follower.readNewChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0] != "Chat: 2, Player, (ALL) 회전 후 메시지" {
		t.Fatalf("rotated-log chats = %#v", chats)
	}
}

func TestRCONChatUsesUnicodeLogSource(t *testing.T) {
	manager := &Manager{}
	manager.addRCONText("Chat: 2, Player, (ALL) ??\r\nKillfeed: retained\r\n")
	if len(manager.rconEvents) != 1 || manager.rconEvents[0].Text != "Killfeed: retained" {
		t.Fatalf("unexpected direct RCON events: %#v", manager.rconEvents)
	}
}

func TestRCONAllBroadcastSubscriptionUsesCurrentSyntax(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	serverResult := make(chan string, 1)
	serverRelease := make(chan struct{})
	go func() {
		defer server.Close()
		packet, err := readRCONPacket(server)
		if err != nil {
			serverResult <- err.Error()
			return
		}
		if packet.ID != 102 || packet.Type != rconExecCommand ||
			string(packet.Body) != rconListenAllCommand {
			serverResult <- "unexpected all-broadcast subscription packet"
			return
		}
		if err := writeRCONPacket(server, rconPacket{
			ID:   packet.ID,
			Type: rconResponseValue,
			Body: []byte(rconListenAllSuccess),
		}); err != nil {
			serverResult <- err.Error()
			return
		}
		serverResult <- ""
		<-serverRelease
	}()

	manager := &Manager{}
	err := manager.enableAllRCONBroadcasts(client)
	close(serverRelease)
	if err != nil {
		t.Fatal(err)
	}
	if result := <-serverResult; result != "" {
		t.Fatal(result)
	}
}

func TestRCONBroadcastOptionsHelpIsHidden(t *testing.T) {
	lines := filteredRCONLines(
		"Chat: retained\r\n" +
			rconBroadcastOptionsHelp + "\r\n" +
			rconInvalidBroadcast + "\r\n",
	)
	if len(lines) != 2 || lines[0] != "Chat: retained" || lines[1] != rconInvalidBroadcast {
		t.Fatalf("unexpected visible RCON lines: %#v", lines)
	}
}

func TestRCONCandidatesPreferCurrentAndDeduplicate(t *testing.T) {
	current := rconSettings{Password: "NewPass1", Port: 7779}
	cached := rconSettings{Password: "OldPass1", Port: 7778}

	candidates := rconCandidates(&current, &cached)
	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d", len(candidates))
	}
	if !sameRCONSettings(candidates[0], current) {
		t.Fatal("current Game.ini settings were not attempted first")
	}
	if !sameRCONSettings(candidates[1], cached) {
		t.Fatal("last successful settings were not retained as fallback")
	}

	candidates = rconCandidates(&current, &current)
	if len(candidates) != 1 || !sameRCONSettings(candidates[0], current) {
		t.Fatal("identical current and cached settings were not deduplicated")
	}

	candidates = rconCandidates(nil, &cached)
	if len(candidates) != 1 || !sameRCONSettings(candidates[0], cached) {
		t.Fatal("cached settings were not usable when Game.ini was unavailable")
	}
}

func TestStateChangeOriginPolicySupportsProxyHostRewriting(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/login", nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Origin", "https://mordhau.example")
	request.Header.Set("Sec-Fetch-Site", "same-origin")

	if !stateChangeOriginAllowed(request) {
		t.Fatal("same-origin browser request was rejected after proxy host rewriting")
	}
}

func TestStateChangeOriginPolicySupportsClientsWithoutFetchMetadata(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/login", nil)
	request.Header.Set("Origin", "https://mordhau.example")

	if !stateChangeOriginAllowed(request) {
		t.Fatal("client without Fetch Metadata was rejected")
	}
}

func TestStateChangeOriginPolicyRejectsBrowserCrossSiteRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/login", nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")

	if stateChangeOriginAllowed(request) {
		t.Fatal("browser-reported cross-site request was accepted")
	}
}

func TestValidCSRFSupportsProxyHostRewriting(t *testing.T) {
	session := Session{CSRF: "test-csrf-token"}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/language", nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Origin", "https://mordhau.example")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("X-CSRF-Token", session.CSRF)

	if !validCSRF(request, session) {
		t.Fatal("valid CSRF token was rejected after proxy host rewriting")
	}

	request.Header.Set("Sec-Fetch-Site", "cross-site")
	if validCSRF(request, session) {
		t.Fatal("cross-site request was accepted with a CSRF token")
	}
}

func TestWebAuditLogRecordsAccountIPAndSecondPrecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mordhau-web.log")
	manager := &Manager{auditPath: path}
	request := httptest.NewRequest(http.MethodPost, "http://manager.example/api/config/mutate", nil)
	request.RemoteAddr = "203.0.113.40:54321"

	manager.auditRequestEvent(request, "operator", "configuration_changed", map[string]string{
		"file": "Game.ini",
		"key":  "RconPassword",
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record auditRecord
	if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(auditTimeLayout, record.Timestamp); err != nil {
		t.Fatalf("audit timestamp is not second-precision RFC3339: %q", record.Timestamp)
	}
	if record.Account != "operator" || record.ClientIP != "203.0.113.40" {
		t.Fatalf("unexpected audit actor: account=%q ip=%q", record.Account, record.ClientIP)
	}
	if record.Method != http.MethodPost || record.Path != "/api/config/mutate" {
		t.Fatalf("unexpected request audit fields: method=%q path=%q", record.Method, record.Path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("audit log mode = %04o", info.Mode().Perm())
	}
}

func TestLifecycleOperationStateSurvivesManagerRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	started := time.Date(2026, 7, 24, 11, 12, 13, 0, time.UTC)
	finished := started.Add(9 * time.Second)
	manager := &Manager{
		operationPath: path,
		op: Operation{
			Action:     "restart",
			Successful: true,
			StartedAt:  started,
			FinishedAt: finished,
			Requested:  "operator",
			Output:     "update complete\nserver started",
		},
	}
	manager.mu.Lock()
	err := manager.saveOperationLocked()
	manager.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	reloaded := &Manager{operationPath: path}
	if err := reloaded.loadOrCreateOperationState(); err != nil {
		t.Fatal(err)
	}
	if reloaded.op.Action != "restart" ||
		!reloaded.op.Successful ||
		reloaded.op.Requested != "operator" ||
		reloaded.op.Output != "update complete\nserver started" ||
		!reloaded.op.StartedAt.Equal(started) ||
		!reloaded.op.FinishedAt.Equal(finished) {
		t.Fatalf("unexpected reloaded operation: %+v", reloaded.op)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("operation state mode = %04o", info.Mode().Perm())
	}
}

func TestRunningLifecycleOperationIsMarkedInterruptedOnReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.json")
	started := time.Now().Add(-time.Minute)
	state := operationStateFile{
		Version: operationStateVersion,
		Operation: Operation{
			Action:    "update",
			Running:   true,
			StartedAt: started,
			Requested: "operator",
		},
	}
	if err := writeJSONAtomic(path, state, 0600); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{operationPath: path}
	if err := manager.loadOrCreateOperationState(); err != nil {
		t.Fatal(err)
	}
	if manager.op.Running || manager.op.Successful ||
		manager.op.FinishedAt.IsZero() ||
		!strings.Contains(manager.op.Output, "stopped before this operation recorded a result") {
		t.Fatalf("interrupted operation was not finalized: %+v", manager.op)
	}

	reloaded := &Manager{operationPath: path}
	if err := reloaded.loadOrCreateOperationState(); err != nil {
		t.Fatal(err)
	}
	if reloaded.op.Running || reloaded.op.FinishedAt.IsZero() {
		t.Fatalf("interrupted result was not persisted: %+v", reloaded.op)
	}
}

func TestRCONHistorySurvivesManagerRestartWithUnicode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mordhau-rcon.log")
	manager := &Manager{rconLogPath: path}
	if err := manager.loadRCONEventLog(); err != nil {
		t.Fatal(err)
	}
	manager.addRCONEvent("rcon", "한국어 Русский 简体中文 Français")
	manager.addRCONEvent("system", "RCON connected")

	reloaded := &Manager{rconLogPath: path}
	if err := reloaded.loadRCONEventLog(); err != nil {
		t.Fatal(err)
	}
	events := reloaded.rconHistory(rconBrowserHistoryLimit)
	if len(events) != 2 {
		t.Fatalf("reloaded event count = %d", len(events))
	}
	if events[0].Sequence != 1 ||
		events[0].Text != "한국어 Русский 简体中文 Français" ||
		events[1].Sequence != 2 ||
		reloaded.rconSequence != 2 {
		t.Fatalf("unexpected reloaded RCON history: %#v", events)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("RCON event log mode = %04o", info.Mode().Perm())
	}
}

func TestRCONHistoryRecoversAfterTruncatedFinalRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mordhau-rcon.log")
	manager := &Manager{rconLogPath: path}
	if err := manager.loadRCONEventLog(); err != nil {
		t.Fatal(err)
	}
	manager.addRCONEvent("rcon", "first")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"sequence":2`); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reloaded := &Manager{rconLogPath: path}
	if err := reloaded.loadRCONEventLog(); err != nil {
		t.Fatal(err)
	}
	reloaded.addRCONEvent("system", "second")

	final := &Manager{rconLogPath: path}
	if err := final.loadRCONEventLog(); err != nil {
		t.Fatal(err)
	}
	events := final.rconHistory(rconBrowserHistoryLimit)
	if len(events) != 2 ||
		events[0].Text != "first" ||
		events[1].Text != "second" ||
		events[1].Sequence != 2 {
		t.Fatalf("unexpected recovered RCON history: %#v", events)
	}
}

func TestRCONHistoryReturnsLatestBrowserWindow(t *testing.T) {
	manager := &Manager{}
	for index := 1; index <= 450; index++ {
		manager.addRCONEvent("rcon", fmt.Sprintf("event %d", index))
	}
	events := manager.rconHistory(rconBrowserHistoryLimit)
	if len(events) != rconBrowserHistoryLimit {
		t.Fatalf("browser history count = %d", len(events))
	}
	if events[0].Sequence != 51 || events[len(events)-1].Sequence != 450 {
		t.Fatalf(
			"browser history sequence range = %d..%d",
			events[0].Sequence,
			events[len(events)-1].Sequence,
		)
	}
}

func TestHTTPAccessAuditIncludesUnauthenticatedRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mordhau-web.log")
	manager := &Manager{auditPath: path}
	handler := manager.auditMiddleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://manager.example/login", nil)
	request.RemoteAddr = "2001:db8::10"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record auditRecord
	if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
		t.Fatal(err)
	}
	if record.Event != "http_access" || record.Account != "unauthenticated" {
		t.Fatalf("unexpected access audit record: event=%q account=%q", record.Event, record.Account)
	}
	if record.Status != http.StatusNoContent || record.ClientIP != "2001:db8::10" {
		t.Fatalf("unexpected access result: status=%d ip=%q", record.Status, record.ClientIP)
	}
}

func TestManagementAssetsAreNotStoredByBrowsers(t *testing.T) {
	manager := &Manager{}
	for _, path := range []string{"/static/app.js", "/static/app.css"} {
		request := httptest.NewRequest(http.MethodGet, "http://manager.example"+path, nil)
		response := httptest.NewRecorder()
		manager.staticHandler(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
			t.Fatalf("%s Cache-Control = %q", path, cacheControl)
		}
	}
}

func TestAccessConfigSerializesEmptyRulesAsArray(t *testing.T) {
	manager := &Manager{
		access: AccessConfig{
			Version:    1,
			BasePolicy: "all_allow",
		},
	}
	encoded, err := json.Marshal(manager.accessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"rules":[]`) {
		t.Fatalf("empty access rules were not serialized as an array: %s", encoded)
	}
}

func TestConfigAuditDetailsExcludeValuesAndRevisions(t *testing.T) {
	mutation := ConfigMutation{
		File:     "Game.ini",
		Revision: "secret-revision",
		Action:   "set_entry",
		Section:  "/Script/Mordhau.MordhauGameSession",
		Key:      "RconPassword",
		Value:    "SecretRcon9",
	}
	encoded, err := json.Marshal(configMutationAuditDetails(mutation, true))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, mutation.Value) || strings.Contains(text, mutation.Revision) {
		t.Fatal("configuration value or revision leaked into audit details")
	}
	if !strings.Contains(text, mutation.Key) {
		t.Fatal("configuration key was not identified in audit details")
	}

	toggle := mutation
	toggle.Action = "set_entry_enabled"
	toggle.Enabled = false
	encoded, err = json.Marshal(configMutationAuditDetails(toggle, true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"enabled":"false"`) {
		t.Fatal("entry enable/disable state was not included in audit details")
	}
}

func TestModIOAPIBaseValidation(t *testing.T) {
	accepted := map[string]string{
		"":                                defaultModIOAPIBase,
		"https://api.mod.io/v1/":          defaultModIOAPIBase,
		"https://u-123.modapi.io/v1":      "https://u-123.modapi.io/v1",
		"https://g-example.modapi.io/v1/": "https://g-example.modapi.io/v1",
	}
	for input, wanted := range accepted {
		got, err := normalizeModIOAPIBase(input)
		if err != nil {
			t.Fatalf("normalizeModIOAPIBase(%q): %v", input, err)
		}
		if got != wanted {
			t.Fatalf("normalizeModIOAPIBase(%q) = %q, want %q", input, got, wanted)
		}
	}

	rejected := []string{
		"http://api.mod.io/v1",
		"https://mod.io/v1",
		"https://example.com/v1",
		"https://api.mod.io:443/v1",
		"https://user@api.mod.io/v1",
		"https://api.mod.io/v1?x=1",
		"https://api.mod.io/v1#fragment",
		"https://api.mod.io/v1/games",
	}
	for _, input := range rejected {
		if _, err := normalizeModIOAPIBase(input); err == nil {
			t.Fatalf("normalizeModIOAPIBase(%q) unexpectedly succeeded", input)
		}
	}
}

func TestModIOAPIKeyValidation(t *testing.T) {
	if !validModIOAPIKey(strings.Repeat("aB3c", 8)) {
		t.Fatal("valid 32-character alphanumeric API key was rejected")
	}
	for _, value := range []string{
		strings.Repeat("a", 31),
		strings.Repeat("a", 33),
		strings.Repeat("a", 31) + "-",
		strings.Repeat("a", 31) + "\n",
	} {
		if validModIOAPIKey(value) {
			t.Fatalf("invalid API key shape was accepted: length=%d", len(value))
		}
	}
}

func TestParseModReference(t *testing.T) {
	tests := []struct {
		reference string
		id        int
		slug      string
	}{
		{reference: "1234567", id: 1234567},
		{reference: "example-mod", slug: "example-mod"},
		{reference: "https://mod.io/g/mordhau/m/example-mod", slug: "example-mod"},
		{reference: "https://www.mod.io/g/mordhau/m/example-mod", slug: "example-mod"},
		{reference: "https://mordhau.mod.io/example-mod", slug: "example-mod"},
	}
	for _, test := range tests {
		id, slug, err := parseModReference(test.reference)
		if err != nil {
			t.Fatalf("parseModReference(%q): %v", test.reference, err)
		}
		if id != test.id || slug != test.slug {
			t.Fatalf("parseModReference(%q) = (%d, %q), want (%d, %q)",
				test.reference, id, slug, test.id, test.slug)
		}
	}

	rejected := []string{
		"",
		"https://example.com/g/mordhau/m/example-mod",
		"http://mod.io/g/mordhau/m/example-mod",
		"https://mod.io/g/skaterxl/m/example-mod",
		"https://mod.io/g/mordhau/m/example-mod/extra",
		"Not A Slug",
	}
	for _, reference := range rejected {
		if _, _, err := parseModReference(reference); err == nil {
			t.Fatalf("parseModReference(%q) unexpectedly succeeded", reference)
		}
	}
}

func TestConfiguredModsParsingIsScopedAndNullSafe(t *testing.T) {
	data := []byte("[" + modIOGameSessionSection + "]\r\n" +
		"Mods=10\r\n" +
		disabledEntryPrefix + "Mods=20\r\n" +
		"Mods=10\r\n" +
		"Mods=invalid\r\n" +
		"[Other]\r\n" +
		"Mods=999\r\n")
	mods, invalid := configuredModsFromData(data)
	if invalid != 1 {
		t.Fatalf("invalid entry count = %d, want 1", invalid)
	}
	if len(mods) != 2 {
		t.Fatalf("configured mod count = %d, want 2", len(mods))
	}
	if mods[0].ID != 10 || !mods[0].Enabled || mods[0].Occurrences != 2 {
		t.Fatalf("first configured mod parsed incorrectly: %+v", mods[0])
	}
	if mods[1].ID != 20 || mods[1].Enabled || mods[1].Occurrences != 1 {
		t.Fatalf("second configured mod parsed incorrectly: %+v", mods[1])
	}
	for _, mod := range mods {
		if mod.Dependencies == nil || mod.UnresolvedDependencies == nil {
			t.Fatalf("configured mod dependency arrays must not be null: %+v", mod)
		}
	}

	empty, invalid := configuredModsFromData([]byte("[Other]\nMods=999\n"))
	if empty == nil || len(empty) != 0 || invalid != 0 {
		t.Fatalf("missing game-session section did not produce an empty array: %#v, %d", empty, invalid)
	}
}

func TestUnresolvedModDependenciesWarnOnlyForEnabledMods(t *testing.T) {
	mods := []ConfiguredMod{
		{
			ID:                  10,
			Enabled:             true,
			DependenciesChecked: true,
			Dependencies: []ModIOItem{
				{ID: 20},
				{ID: 30},
				{ID: 40},
			},
		},
		{
			ID:                  20,
			Enabled:             true,
			DependenciesChecked: true,
			Dependencies:        []ModIOItem{},
		},
		{
			ID:                  30,
			Enabled:             false,
			DependenciesChecked: true,
			Dependencies:        []ModIOItem{{ID: 50}},
		},
		{
			ID:                  60,
			Enabled:             false,
			DependenciesChecked: true,
			Dependencies:        []ModIOItem{{ID: 70}},
		},
	}

	markUnresolvedModDependencies(mods)
	if !slicesEqual(mods[0].UnresolvedDependencies, []int{30, 40}) {
		t.Fatalf(
			"enabled mod unresolved dependencies = %v, want [30 40]",
			mods[0].UnresolvedDependencies,
		)
	}
	if len(mods[1].UnresolvedDependencies) != 0 {
		t.Fatalf("resolved mod reported missing dependencies: %v", mods[1].UnresolvedDependencies)
	}
	if len(mods[2].UnresolvedDependencies) != 0 ||
		len(mods[3].UnresolvedDependencies) != 0 {
		t.Fatal("disabled mods must not report unresolved dependency warnings")
	}
}

func TestSharedModCacheCollapsesConcurrentClients(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	manager := &Manager{
		modRefreshSettings: modRefreshSettingsFile{
			Version:         modRefreshSettingsVersion,
			IntervalMinutes: defaultModRefreshMinutes,
		},
		modRefreshWake: make(chan struct{}, 1),
		modManagementViewBuild: func() (ModManagementView, error) {
			calls.Add(1)
			once.Do(func() { close(entered) })
			<-release
			return ModManagementView{
				Mods: []ConfiguredMod{},
			}, nil
		},
	}

	const clients = 12
	results := make([]ModManagementView, clients)
	errors := make([]error, clients)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < clients; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errors[index] = manager.cachedModManagementView()
		}(index)
	}
	close(start)
	<-entered
	close(release)
	wait.Wait()

	if calls.Load() != 1 {
		t.Fatalf("concurrent clients caused %d metadata builds, want 1", calls.Load())
	}
	for index, err := range errors {
		if err != nil {
			t.Fatalf("client %d received an error: %v", index, err)
		}
		if results[index].Revision != 1 {
			t.Fatalf("client %d received revision %d, want 1", index, results[index].Revision)
		}
	}

	if _, err := manager.cachedModManagementView(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("reading the shared cache performed another metadata lookup")
	}
}

func TestConfigurationChangeRefreshFollowsInProgressBuild(t *testing.T) {
	var calls atomic.Int32
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	manager := &Manager{
		modRefreshSettings: modRefreshSettingsFile{
			Version:         modRefreshSettingsVersion,
			IntervalMinutes: defaultModRefreshMinutes,
		},
		modRefreshWake: make(chan struct{}, 1),
		modManagementViewBuild: func() (ModManagementView, error) {
			if calls.Add(1) == 1 {
				close(firstEntered)
				<-releaseFirst
			}
			return ModManagementView{Mods: []ConfiguredMod{}}, nil
		},
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.refreshModCache()
		firstDone <- err
	}()
	<-firstEntered

	changeDone := make(chan error, 1)
	go func() {
		_, err := manager.refreshModCacheAfterConfigurationChange()
		changeDone <- err
	}()
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-changeDone; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("configuration change caused %d builds, want 2", calls.Load())
	}
}

func TestSuccessfulModRefreshResetsIntervalAndFailureDoesNot(t *testing.T) {
	var fail atomic.Bool
	manager := &Manager{
		modRefreshSettings: modRefreshSettingsFile{
			Version:         modRefreshSettingsVersion,
			IntervalMinutes: 60,
		},
		modRefreshWake: make(chan struct{}, 1),
		modManagementViewBuild: func() (ModManagementView, error) {
			view := ModManagementView{Mods: []ConfiguredMod{}}
			if fail.Load() {
				view.APIError = "temporary mod.io failure"
			}
			return view, nil
		},
	}

	success, err := manager.refreshModCache()
	if err != nil {
		t.Fatal(err)
	}
	if success.Refresh.LastSuccessAt == nil || success.Refresh.NextRefreshAt == nil {
		t.Fatal("successful refresh did not publish success and next-refresh timestamps")
	}
	if got := success.Refresh.NextRefreshAt.Sub(*success.Refresh.LastSuccessAt); got != time.Hour {
		t.Fatalf("next refresh delay = %s, want 1h", got)
	}
	lastSuccess := *success.Refresh.LastSuccessAt

	fail.Store(true)
	failed, err := manager.refreshModCache()
	if err != nil {
		t.Fatal(err)
	}
	if failed.Refresh.LastSuccessAt == nil ||
		!failed.Refresh.LastSuccessAt.Equal(lastSuccess) {
		t.Fatal("failed refresh changed the last successful refresh time")
	}
	if failed.Refresh.LastError == "" || failed.Refresh.NextRefreshAt == nil {
		t.Fatal("failed refresh did not publish its error and retry time")
	}
	retryDelay := time.Until(*failed.Refresh.NextRefreshAt)
	if retryDelay <= 0 || retryDelay > maximumModRefreshRetry+time.Second {
		t.Fatalf("failed refresh retry delay = %s", retryDelay)
	}
}

func TestNextRefreshUsesLastSuccessfulRefresh(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	lastSuccess := now.Add(-10 * time.Minute)
	if got := nextRefreshForInterval(lastSuccess, now, 60); !got.Equal(lastSuccess.Add(time.Hour)) {
		t.Fatalf("next refresh = %s", got)
	}
	if got := nextRefreshForInterval(lastSuccess, now, 5); !got.Equal(now) {
		t.Fatalf("overdue interval did not become immediately due: %s", got)
	}
}

func TestDashboardThemeAndMessageMarkup(t *testing.T) {
	indexData, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexData)
	for _, expected := range []string{
		`id="theme-toggle"`,
		`src="/static/theme.js?v=1.4.0"`,
		`<label for="rcon-message">Send Message</label>`,
		`id="mods-refresh-minutes"`,
		`min="1" max="10080"`,
		`value="60"`,
	} {
		if !strings.Contains(index, expected) {
			t.Fatalf("dashboard is missing %q", expected)
		}
	}
	for _, unwanted := range []string{
		"Unicode server message",
		"한국어 · Русский · 简体中文 · Français",
		"stored in this browser",
		"<script>\n",
	} {
		if strings.Contains(index, unwanted) {
			t.Fatalf("dashboard still contains %q", unwanted)
		}
	}

	loginData, err := staticFiles.ReadFile("static/login.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(loginData), `src="/static/theme.js?v=1.4.0"`) {
		t.Fatal("login page does not initialize the persisted theme")
	}

	themeData, err := staticFiles.ReadFile("static/theme.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(themeData), `let theme = "light"`) {
		t.Fatal("theme initializer does not default to light mode")
	}

	appData, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	appSource := string(appData)
	for _, expected := range []string{
		`/api/mods/refresh`,
		`/api/mods/refresh/settings`,
		`/api/rcon/history`,
		`Last successful refresh:`,
		`resolvedOptions().timeZone`,
	} {
		if !strings.Contains(appSource, expected) {
			t.Fatalf("frontend is missing %q", expected)
		}
	}
	for _, unwanted := range []string{
		`getItem("mordhau-mod-refresh-minutes")`,
		`setItem("mordhau-mod-refresh-minutes"`,
	} {
		if strings.Contains(appSource, unwanted) {
			t.Fatal("mod refresh interval is still read from or written to browser-local state")
		}
	}
}

func TestModDocumentMutationsPreserveScopeAndOrdering(t *testing.T) {
	original := []byte("[" + modIOGameSessionSection + "]\r\n" +
		"Mods=10\r\n" +
		disabledEntryPrefix + "Mods=20\r\n" +
		"[Other]\r\n" +
		"Mods=999\r\n")
	document := parseIni(original)

	change, err := addModsToDocument(&document, []int{10, 20, 30})
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed || !slicesEqual(change.Added, []int{30}) ||
		!slicesEqual(change.Reenabled, []int{20}) {
		t.Fatalf("unexpected add result: %+v", change)
	}
	result := string(document.bytes())
	wantedOrder := "Mods=10\r\nMods=20\r\nMods=30\r\n[Other]\r\nMods=999"
	if !strings.Contains(result, wantedOrder) {
		t.Fatalf("mod lines were not added in dependency-first order or scope:\n%s", result)
	}

	change, err = setModEnabledInDocument(&document, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed ||
		!strings.Contains(string(document.bytes()), disabledEntryPrefix+"Mods=10") {
		t.Fatal("configured mod was not disabled")
	}

	change, err = removeModFromDocument(&document, 20)
	if err != nil {
		t.Fatal(err)
	}
	result = string(document.bytes())
	if !change.Changed || strings.Contains(result, "Mods=20") {
		t.Fatal("configured mod was not removed")
	}
	if !strings.Contains(result, "[Other]\r\nMods=999") {
		t.Fatal("a Mods entry in another section was changed")
	}
}

func TestPlanModIDsAreDependenciesFirstAndDeduplicated(t *testing.T) {
	plan := ModInstallPlan{
		Target: ModIOItem{ID: 30},
		Dependencies: []ModIOItem{
			{ID: 10},
			{ID: 20},
			{ID: 10},
			{ID: 0},
			{ID: 30},
		},
	}
	if got := planModIDs(plan); !slicesEqual(got, []int{10, 20, 30}) {
		t.Fatalf("planModIDs() = %v, want [10 20 30]", got)
	}
}

func TestStartMapValidation(t *testing.T) {
	for _, value := range []string{
		"",
		"DREAD_Crypt",
		"/Game/Mordhau/Maps/Test?game=/Script/Mordhau.MordhauGameMode",
		"Map.Name-1+Variant:Two",
	} {
		if err := validateStartMap(value); err != nil {
			t.Fatalf("validateStartMap(%q): %v", value, err)
		}
	}
	for _, value := range []string{
		"-log",
		"Map Name",
		"Map;command",
		"Map\nName",
		strings.Repeat("a", 161),
	} {
		if err := validateStartMap(value); err == nil {
			t.Fatalf("validateStartMap(%q) unexpectedly succeeded", value)
		}
	}
}

func TestServerPortsRoundTripAndValidation(t *testing.T) {
	ports := ServerPorts{
		Game:   7777,
		RCON:   7778,
		Beacon: 15000,
		Query:  27015,
	}
	parsed, err := parseServerPorts(formatServerPorts(ports))
	if err != nil {
		t.Fatal(err)
	}
	if parsed != ports {
		t.Fatalf("parsed server ports = %+v, want %+v", parsed, ports)
	}
	if err := validateServerPortsForWeb(ports, 8080); err != nil {
		t.Fatalf("default server ports were rejected: %v", err)
	}

	duplicate := ports
	duplicate.Query = duplicate.Game
	if err := validateServerPorts(duplicate); err == nil {
		t.Fatal("duplicate server ports were accepted")
	}

	outOfRange := ports
	outOfRange.RCON = 65536
	if err := validateServerPorts(outOfRange); err == nil {
		t.Fatal("out-of-range server port was accepted")
	}

	if err := validateServerPortsForWeb(ports, ports.Beacon); err == nil {
		t.Fatal("server port matching the web service port was accepted")
	}
}

func TestServerPortsFileRejectsIncompleteOrUnknownSettings(t *testing.T) {
	for _, data := range []string{
		"game=7777\nrcon=7778\nbeacon=15000\n",
		"game=7777\nrcon=7778\nbeacon=15000\nquery=27015\nextra=1\n",
		"game=7777\ngame=7779\nrcon=7778\nbeacon=15000\nquery=27015\n",
		"game=invalid\nrcon=7778\nbeacon=15000\nquery=27015\n",
	} {
		if _, err := parseServerPorts([]byte(data)); err == nil {
			t.Fatalf("invalid server ports file was accepted: %q", data)
		}
	}
}

func TestPublicModIOSettingsNeverExposeAPIKey(t *testing.T) {
	secret := strings.Repeat("aB3c", 8)
	view := publicModIOSettings(&modIOSettingsFile{
		Version:  1,
		APIKey:   secret,
		APIBase:  defaultModIOAPIBase,
		GameID:   11,
		GameName: "MORDHAU",
	})
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), `"api_key":`) {
		t.Fatal("mod.io API key was exposed in public settings JSON")
	}
	if !strings.Contains(string(encoded), `"api_key_configured":true`) {
		t.Fatal("public settings did not report that an API key is configured")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestModIOErrorsDoNotLeakAPIKey(t *testing.T) {
	secret := strings.Repeat("aB3c", 8)
	originalClient := modIOHTTPClient
	defer func() {
		modIOHTTPClient = originalClient
	}()
	modIOHTTPClient = &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Query().Get("api_key") != secret {
				t.Fatal("API key was not sent to mod.io")
			}
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("rejected key " + secret)),
				Request:    request,
			}, nil
		}),
	}
	err := modIOGet(modIOSettingsFile{
		APIKey:  secret,
		APIBase: defaultModIOAPIBase,
	}, "games", nil, &modIOCollection[modIOGame]{})
	if err == nil {
		t.Fatal("unauthorized mod.io response unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("mod.io API key leaked through an error")
	}
}

func slicesEqual(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
