package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
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
