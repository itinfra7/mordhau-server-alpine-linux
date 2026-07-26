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

func TestMetricsSampleInterval(t *testing.T) {
	if metricsSampleInterval != time.Minute {
		t.Fatalf("metrics sample interval = %s, want %s", metricsSampleInterval, time.Minute)
	}
}

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

func TestServerEventLogValuesAreEnabledUnlessExplicitlyDisabled(t *testing.T) {
	const section = "/Script/Mordhau.MordhauGameMode"
	data := []byte("[" + section + "]\n" +
		"bLogChat=False\n")
	store := newDisabledINIFile()
	store.Entries = append(store.Entries, disabledINIEntry{
		ID:      "disabled-score",
		File:    "Game.ini",
		Section: section,
		Key:     "bLogScore",
		Value:   "False",
	})

	updated, changed := ensureServerEventLogValues(data, store)
	if !changed {
		t.Fatal("required game-log settings were not enabled")
	}
	for _, key := range []string{"bLogKillfeed", "bLogChat"} {
		value, enabled := iniValue(updated, section, key)
		if !enabled || !strings.EqualFold(value, "true") {
			t.Fatalf("%s = %q, enabled %t", key, value, enabled)
		}
	}
	if _, enabled := iniValue(updated, section, "bLogScore"); enabled {
		t.Fatal("explicitly disabled score logging was re-enabled")
	}

	second, changed := ensureServerEventLogValues(updated, store)
	if changed || !bytes.Equal(second, updated) {
		t.Fatal("game-log setting enforcement was not idempotent")
	}

	store.Sections = append(store.Sections, disabledINISection{
		ID:   "disabled-game-mode",
		File: "Game.ini",
		Name: section,
	})
	sectionDisabled, changed := ensureServerEventLogValues(data, store)
	if changed || !bytes.Equal(sectionDisabled, data) {
		t.Fatal("an explicitly disabled game-mode section was modified")
	}
}

func TestEngineNetworkDefaultsOnlyFillMissingValues(t *testing.T) {
	const section = "/Script/OnlineSubsystemUtils.IpNetDriver"
	data := []byte("[" + section + "]\r\n" +
		"NetServerMaxTickRate=30\r\n" +
		"\r\n[Other]\r\nRetained=Yes\r\n")
	store := newDisabledINIFile()

	updated, changed := ensureEngineNetworkDefaultValues(data, store)
	if !changed {
		t.Fatal("missing connection timeout was not added")
	}
	tickRate, tickRateEnabled := iniValue(updated, section, "NetServerMaxTickRate")
	if !tickRateEnabled || tickRate != "30" {
		t.Fatalf("existing tick rate changed to %q", tickRate)
	}
	timeout, timeoutEnabled := iniValue(updated, section, "ConnectionTimeout")
	if !timeoutEnabled || timeout != "10.0" {
		t.Fatalf("connection timeout = %q, enabled %t", timeout, timeoutEnabled)
	}
	if !bytes.Contains(updated, []byte("\r\n")) ||
		!bytes.Contains(updated, []byte("[Other]\r\nRetained=Yes")) {
		t.Fatal("Engine.ini formatting or unrelated content was not preserved")
	}

	second, changed := ensureEngineNetworkDefaultValues(updated, store)
	if changed || !bytes.Equal(second, updated) {
		t.Fatal("Engine.ini network defaults were not idempotent")
	}
}

func TestEngineNetworkDefaultsRespectExistingAndDisabledValues(t *testing.T) {
	const section = "/Script/OnlineSubsystemUtils.IpNetDriver"
	data := []byte("[" + section + "]\nConnectionTimeout=20.0\n")
	store := newDisabledINIFile()
	store.Entries = append(store.Entries, disabledINIEntry{
		ID:      "disabled-tick-rate",
		File:    "Engine.ini",
		Section: section,
		Key:     "NetServerMaxTickRate",
		Value:   "24",
	})

	updated, changed := ensureEngineNetworkDefaultValues(data, store)
	if changed || !bytes.Equal(updated, data) {
		t.Fatal("an existing or explicitly disabled network value was replaced")
	}

	store.Sections = append(store.Sections, disabledINISection{
		ID:   "disabled-ip-net-driver",
		File: "Engine.ini",
		Name: section,
	})
	sectionDisabled, changed := ensureEngineNetworkDefaultValues(
		[]byte("[Other]\nRetained=Yes\n"),
		store,
	)
	if changed || string(sectionDisabled) != "[Other]\nRetained=Yes\n" {
		t.Fatal("an explicitly disabled IpNetDriver section was recreated")
	}
}

func TestPersistentDisabledEntrySurvivesINIRewriteAndReenables(t *testing.T) {
	original := []byte("[One]\nFirst=A\nMap=One\nMap=Two\nLast=Z\n")
	document := parseIni(original)
	store := newDisabledINIFile()

	if err := disableConfigEntry("Game.ini", &document, &store, 2); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(document.bytes()), disabledEntryPrefix) ||
		strings.Contains(string(document.bytes()), "Map=One") {
		t.Fatalf("disabled entry remained in the game-owned INI:\n%s", document.bytes())
	}
	if len(store.Entries) != 1 || store.Entries[0].Position != 1 {
		t.Fatalf("unexpected persistent disabled state: %+v", store)
	}

	view := makeConfigViewWithDisabled("Game.ini", document.bytes(), false, store)
	entries := view.Sections[0].Entries
	if len(entries) != 4 ||
		entries[1].Enabled ||
		entries[1].Value != "One" ||
		entries[2].Value != "Two" {
		t.Fatalf("rewritten INI and persistent state merged incorrectly: %+v", entries)
	}

	id := store.Entries[0].ID
	if err := enableConfigEntry("Game.ini", &document, &store, id); err != nil {
		t.Fatal(err)
	}
	if len(store.Entries) != 0 {
		t.Fatalf("re-enabled entry remained persistent: %+v", store.Entries)
	}
	if got := string(document.bytes()); got != string(original) {
		t.Fatalf("re-enabled entry order changed:\n%s", got)
	}
}

func TestPersistentSectionDisableAndEnableCoversEveryEntry(t *testing.T) {
	original := []byte("[One]\nFirst=A\nDuplicate=One\nDuplicate=Two\n\n[Two]\nOther=Z\n")
	document := parseIni(original)
	store := newDisabledINIFile()

	if err := setConfigSectionEnabled(
		"Engine.ini",
		&document,
		&store,
		0,
		"",
		"One",
		false,
	); err != nil {
		t.Fatal(err)
	}
	if len(store.Sections) != 1 || len(store.Entries) != 3 {
		t.Fatalf("section disable did not persist all entries: %+v", store)
	}
	for _, entry := range store.Entries {
		if entry.Section != "One" {
			t.Fatalf("entry escaped the disabled section: %+v", entry)
		}
	}
	view := makeConfigViewWithDisabled("Engine.ini", document.bytes(), false, store)
	if view.Sections[0].Enabled || len(view.Sections[0].Entries) != 3 {
		t.Fatalf("disabled section view is incorrect: %+v", view.Sections[0])
	}
	for _, entry := range view.Sections[0].Entries {
		if entry.Enabled {
			t.Fatalf("section disable left an active entry: %+v", entry)
		}
	}

	sectionID := store.Sections[0].ID
	if err := setConfigSectionEnabled(
		"Engine.ini",
		&document,
		&store,
		-1,
		sectionID,
		"One",
		true,
	); err != nil {
		t.Fatal(err)
	}
	if len(store.Sections) != 0 || len(store.Entries) != 0 {
		t.Fatalf("section enable left disabled state behind: %+v", store)
	}
	enabledView := makeConfigViewWithDisabled("Engine.ini", document.bytes(), false, store)
	if !enabledView.Sections[0].Enabled || len(enabledView.Sections[0].Entries) != 3 {
		t.Fatalf("section was not restored: %+v", enabledView.Sections[0])
	}
	for _, entry := range enabledView.Sections[0].Entries {
		if !entry.Enabled {
			t.Fatalf("section enable left a disabled entry: %+v", entry)
		}
	}
	if enabledView.Sections[0].Entries[1].Value != "One" ||
		enabledView.Sections[0].Entries[2].Value != "Two" {
		t.Fatalf("duplicate entry order changed: %+v", enabledView.Sections[0].Entries)
	}
}

func TestDisabledSectionCanBeRestoredAfterGameRemovesHeader(t *testing.T) {
	document := parseIni([]byte("[Temporary]\nKey=Value\n"))
	store := newDisabledINIFile()
	if err := setConfigSectionEnabled(
		"Game.ini",
		&document,
		&store,
		0,
		"",
		"Temporary",
		false,
	); err != nil {
		t.Fatal(err)
	}

	document = parseIni([]byte{})
	view := makeConfigViewWithDisabled("Game.ini", document.bytes(), false, store)
	if len(view.Sections) != 1 ||
		view.Sections[0].Name != "Temporary" ||
		view.Sections[0].Enabled ||
		len(view.Sections[0].Entries) != 1 {
		t.Fatalf("disabled virtual section was not retained: %+v", view.Sections)
	}
	if err := setConfigSectionEnabled(
		"Game.ini",
		&document,
		&store,
		-1,
		store.Sections[0].ID,
		"Temporary",
		true,
	); err != nil {
		t.Fatal(err)
	}
	if got := string(document.bytes()); got != "[Temporary]\nKey=Value\n" {
		t.Fatalf("virtual section restoration = %q", got)
	}
}

func TestConfigRevisionIncludesPersistentDisabledState(t *testing.T) {
	data := []byte("[One]\nKey=Value\n")
	store := newDisabledINIFile()
	before := configRevision("Game.ini", data, store)
	store.Entries = append(store.Entries, disabledINIEntry{
		ID:       "entry-one",
		File:     "Game.ini",
		Section:  "One",
		Position: 1,
		Key:      "Other",
		Value:    "Disabled",
	})
	after := configRevision("Game.ini", data, store)
	if before == after {
		t.Fatal("sidecar-only change did not change the configuration revision")
	}
	if unrelated := configRevision("Engine.ini", data, store); unrelated !=
		configRevision("Engine.ini", data, newDisabledINIFile()) {
		t.Fatal("Game.ini disabled state changed the Engine.ini revision")
	}
}

func TestLegacyDisabledCommentsMigrateOutOfGameINI(t *testing.T) {
	original := []byte("[One]\r\nActive=A\r\n" +
		disabledEntryPrefix + "Disabled=B=C\r\n; ordinary comment\r\n")
	store := newDisabledINIFile()
	migrated, count, err := migrateLegacyDisabledEntries(
		"Game.ini",
		original,
		&store,
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(store.Entries) != 1 {
		t.Fatalf("migration count/state = %d, %+v", count, store)
	}
	if strings.Contains(string(migrated), disabledEntryPrefix) ||
		!strings.Contains(string(migrated), "; ordinary comment\r\n") {
		t.Fatalf("legacy migration damaged INI content:\n%s", migrated)
	}
	if store.Entries[0].Key != "Disabled" ||
		store.Entries[0].Value != "B=C" ||
		store.Entries[0].Position != 1 {
		t.Fatalf("legacy entry migrated incorrectly: %+v", store.Entries[0])
	}

	duplicates := []byte("[One]\n" +
		disabledEntryPrefix + "Map=Same\n" +
		disabledEntryPrefix + "Map=Same\n")
	duplicateStore := newDisabledINIFile()
	if _, count, err := migrateLegacyDisabledEntries(
		"Game.ini",
		duplicates,
		&duplicateStore,
	); err != nil || count != 2 || len(duplicateStore.Entries) != 2 {
		t.Fatalf(
			"duplicate legacy values were not preserved: count=%d state=%+v err=%v",
			count,
			duplicateStore,
			err,
		)
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

func TestNormalizeInclusiveIPv4Ranges(t *testing.T) {
	const canonical = "10.64.192.0-10.64.252.255"
	expectedPrefixes := []string{
		"10.64.192.0/19",
		"10.64.224.0/20",
		"10.64.240.0/21",
		"10.64.248.0/22",
		"10.64.252.0/24",
	}
	for _, input := range []string{
		"10.64.192.0~10.64.252.255",
		"10.64.192.0-10.64.252.255",
		" 10.64.192.0 - 10.64.252.255 ",
	} {
		network, err := normalizeAccessNetwork(input)
		if err != nil {
			t.Fatalf("normalizeAccessNetwork(%q): %v", input, err)
		}
		if network.canonical != canonical {
			t.Fatalf("normalizeAccessNetwork(%q) canonical = %q", input, network.canonical)
		}
		if len(network.prefixes) != len(expectedPrefixes) {
			t.Fatalf(
				"normalizeAccessNetwork(%q) returned %d prefixes",
				input,
				len(network.prefixes),
			)
		}
		for index, expected := range expectedPrefixes {
			if actual := network.prefixes[index].String(); actual != expected {
				t.Fatalf(
					"normalizeAccessNetwork(%q) prefix %d = %q, want %q",
					input,
					index,
					actual,
					expected,
				)
			}
		}
	}

	single, err := normalizeAccessNetwork("203.0.113.8~203.0.113.8")
	if err != nil {
		t.Fatal(err)
	}
	if single.canonical != "203.0.113.8/32" ||
		len(single.prefixes) != 1 ||
		single.prefixes[0].String() != "203.0.113.8/32" {
		t.Fatalf("equal-endpoint range was not reduced to one address: %#v", single)
	}

	full, err := normalizeAccessNetwork("0.0.0.0-255.255.255.255")
	if err != nil {
		t.Fatal(err)
	}
	if len(full.prefixes) != 1 || full.prefixes[0].String() != "0.0.0.0/0" {
		t.Fatalf("full IPv4 range = %#v", full.prefixes)
	}
}

func TestNormalizeExistingAddressAndCIDRRules(t *testing.T) {
	for input, expected := range map[string]string{
		"203.0.113.8":        "203.0.113.8/32",
		"203.0.113.9/24":     "203.0.113.0/24",
		"2001:db8::8":        "2001:db8::8/128",
		"2001:db8:1::8/48":   "2001:db8:1::/48",
		"2001:db8:abcd::/64": "2001:db8:abcd::/64",
	} {
		network, err := normalizeAccessNetwork(input)
		if err != nil {
			t.Fatalf("normalizeAccessNetwork(%q): %v", input, err)
		}
		if network.canonical != expected ||
			len(network.prefixes) != 1 ||
			network.prefixes[0].String() != expected {
			t.Fatalf("normalizeAccessNetwork(%q) = %#v, want %q", input, network, expected)
		}
	}
}

func TestNormalizeAccessRuleComment(t *testing.T) {
	comment, err := normalizeAccessRuleComment("  관리자 VPN · 한국어 · Русский  ")
	if err != nil {
		t.Fatal(err)
	}
	if comment != "관리자 VPN · 한국어 · Русский" {
		t.Fatalf("normalized comment = %q", comment)
	}

	maximum := strings.Repeat("가", maxAccessRuleCommentRunes)
	if comment, err := normalizeAccessRuleComment(maximum); err != nil || comment != maximum {
		t.Fatalf("maximum-length Unicode comment failed: %q, %v", comment, err)
	}

	for _, invalid := range []string{
		strings.Repeat("a", maxAccessRuleCommentRunes+1),
		"line one\nline two",
		"tab\tseparated",
		string([]byte{0xff}),
	} {
		if _, err := normalizeAccessRuleComment(invalid); err == nil {
			t.Fatalf("invalid comment %q unexpectedly succeeded", invalid)
		}
	}
}

func TestAccessRuleCommentJSONIsBackwardCompatible(t *testing.T) {
	var rule AccessRule
	if err := json.Unmarshal([]byte(
		`{"id":"example","action":"allow","network":"192.0.2.0/24",`+
			`"created_at":"2026-07-24T00:00:00Z"}`,
	), &rule); err != nil {
		t.Fatal(err)
	}
	if rule.Comment != "" {
		t.Fatalf("legacy rule acquired comment %q", rule.Comment)
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"comment"`) {
		t.Fatal("an empty comment was not omitted from persistent JSON")
	}

	rule.Comment = "Office VPN"
	encoded, err = json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"comment":"Office VPN"`) {
		t.Fatalf("comment was not persisted: %s", encoded)
	}
}

func TestInvalidIPv4RangesAreRejected(t *testing.T) {
	for _, input := range []string{
		"203.0.113.1~",
		"~203.0.113.2",
		"203.0.113.2-203.0.113.1",
		"203.0.113.1~203.0.113.2-203.0.113.3",
		"203.0.113.0/24-203.0.114.0/24",
		"2001:db8::1-2001:db8::2",
	} {
		if _, err := normalizeAccessNetwork(input); err == nil {
			t.Fatalf("normalizeAccessNetwork(%q) unexpectedly succeeded", input)
		}
	}
}

func TestIPv4RangeCIDRsCoverOnlyTheInclusiveRange(t *testing.T) {
	for _, test := range []struct {
		start string
		end   string
	}{
		{"0.0.0.0", "255.255.255.255"},
		{"0.0.0.1", "0.0.1.2"},
		{"192.0.2.3", "192.0.2.130"},
		{"10.64.192.0", "10.64.252.255"},
		{"255.255.254.253", "255.255.255.255"},
	} {
		start := ipv4Value(netip.MustParseAddr(test.start))
		end := ipv4Value(netip.MustParseAddr(test.end))
		prefixes := ipv4RangePrefixes(start, end)
		cursor := uint64(start)
		for _, prefix := range prefixes {
			prefixStart := uint64(ipv4Value(prefix.Addr()))
			blockSize := uint64(1) << (32 - prefix.Bits())
			if prefixStart != cursor {
				t.Fatalf(
					"%s-%s has a gap or overlap at %s",
					test.start,
					test.end,
					prefix,
				)
			}
			if prefixStart+blockSize-1 > uint64(end) {
				t.Fatalf("%s exceeds %s-%s", prefix, test.start, test.end)
			}
			cursor += blockSize
		}
		if cursor != uint64(end)+1 {
			t.Fatalf("%s-%s ended at IPv4 value %d", test.start, test.end, cursor)
		}
	}
}

func TestInclusiveIPv4RangeBoundariesAndPrecedence(t *testing.T) {
	now := time.Now()
	config := AccessConfig{
		BasePolicy: "all_deny",
		Rules: []AccessRule{
			{Action: "allow", Network: "10.64.192.0~10.64.252.255"},
		},
	}
	for _, test := range []struct {
		ip      string
		allowed bool
	}{
		{"10.64.191.255", false},
		{"10.64.192.0", true},
		{"10.64.224.1", true},
		{"10.64.252.255", true},
		{"10.64.253.0", false},
		{"2001:db8::1", false},
	} {
		if got := accessAllowed(netip.MustParseAddr(test.ip), config, now); got != test.allowed {
			t.Fatalf("accessAllowed(%s) = %v, want %v", test.ip, got, test.allowed)
		}
	}

	config.Rules = append(config.Rules,
		AccessRule{Action: "deny", Network: "10.64.250.0/24"},
	)
	if accessAllowed(netip.MustParseAddr("10.64.250.40"), config, now) {
		t.Fatal("a more-specific deny did not override the inclusive allow range")
	}
	if !accessAllowed(netip.MustParseAddr("10.64.251.40"), config, now) {
		t.Fatal("the more-specific deny affected an address outside its prefix")
	}

	config.Rules = append(config.Rules,
		AccessRule{Action: "allow", Network: "10.64.250.0-10.64.250.255"},
	)
	if accessAllowed(netip.MustParseAddr("10.64.250.40"), config, now) {
		t.Fatal("deny did not win an equal-prefix tie against an IPv4 range")
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

func TestRCONCommandValidation(t *testing.T) {
	command, err := normalizeRCONCommand(`  playerlist  `)
	if err != nil {
		t.Fatal(err)
	}
	if command != "playerlist" {
		t.Fatalf("normalized command = %q", command)
	}
	if _, err := normalizeRCONCommand(`kick "플레이어"`); err != nil {
		t.Fatalf("multilingual command argument was rejected: %v", err)
	}

	for _, invalid := range []string{
		"",
		"   ",
		"playerlist\nstatus",
		"playerlist\tstatus",
		string([]byte{0xff}),
		strings.Repeat("a", rconCommandMaxRunes+1),
		strings.Repeat("😀", rconCommandMaxBytes/4+1),
	} {
		if _, err := normalizeRCONCommand(invalid); !errors.Is(err, errInvalidRCONCommand) {
			t.Fatalf("normalizeRCONCommand(%q) error = %v", invalid, err)
		}
	}
}

func TestExecuteRCONAdminCommandCollectsResponsePackets(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	serverResult := make(chan string, 1)
	releaseServer := make(chan struct{})
	defer close(releaseServer)
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
		command, err := readRCONPacket(server)
		if err != nil {
			serverResult <- err.Error()
			return
		}
		if command.ID != rconAdminCommandPacketID ||
			command.Type != rconExecCommand ||
			string(command.Body) != "playerlist" {
			serverResult <- "unexpected administrative command packet"
			return
		}
		for _, response := range []string{
			"Players:\r\nAlpha",
			"Bravo\r\n한국어 응답",
		} {
			if err := writeRCONPacket(server, rconPacket{
				ID:   command.ID,
				Type: rconResponseValue,
				Body: []byte(response),
			}); err != nil {
				serverResult <- err.Error()
				return
			}
		}
		serverResult <- ""
		<-releaseServer
	}()

	result, commandSent, err := executeRCONAdminCommand(
		client,
		"SecretRcon9",
		"playerlist",
		"ko",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !commandSent {
		t.Fatal("successful command was not marked as sent")
	}
	if result.Truncated {
		t.Fatal("small command response was marked as truncated")
	}
	expected := []string{"Players:", "Alpha", "Bravo", "한국어 응답"}
	if len(result.Lines) != len(expected) {
		t.Fatalf("response lines = %#v", result.Lines)
	}
	for index := range expected {
		if result.Lines[index] != expected[index] {
			t.Fatalf("response line %d = %q, want %q",
				index,
				result.Lines[index],
				expected[index],
			)
		}
	}
	if serverError := <-serverResult; serverError != "" {
		t.Fatal(serverError)
	}
}

func TestExecuteRCONAdminCommandReportsPossibleExecutionWithoutResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	serverResult := make(chan error, 1)
	go func() {
		auth, err := readRCONPacket(server)
		if err == nil {
			err = writeRCONPacket(server, rconPacket{
				ID:   auth.ID,
				Type: rconAuthResponse,
			})
		}
		if err == nil {
			_, err = readRCONPacket(server)
		}
		_ = server.Close()
		serverResult <- err
	}()

	_, commandSent, err := executeRCONAdminCommand(
		client,
		"SecretRcon9",
		"restartlevel",
		"en",
	)
	if err == nil {
		t.Fatal("missing command response was accepted")
	}
	if !commandSent {
		t.Fatal("a written command was incorrectly safe to retry")
	}
	if serverError := <-serverResult; serverError != nil {
		t.Fatal(serverError)
	}
}

func TestRCONCommandHandlerPersistsOutputAndAuditsActor(t *testing.T) {
	directory := t.TempDir()
	manager := &Manager{
		rconLogPath: filepath.Join(directory, "rcon.log"),
		auditPath:   filepath.Join(directory, "audit.log"),
	}
	manager.rconCommandExecute = func(command string) (rconCommandResult, error) {
		if command != "adminlogin SuperSecret" {
			t.Fatalf("executor received %q", command)
		}
		return rconCommandResult{
			Lines: []string{"first response", "한국어 응답"},
		}, nil
	}
	session := Session{Username: "operator", CSRF: "csrf-token"}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://manager.example/api/rcon/command",
		strings.NewReader(`{"command":"adminlogin SuperSecret"}`),
	)
	request.Header.Set("X-CSRF-Token", session.CSRF)
	response := httptest.NewRecorder()

	manager.rconCommandHandler(response, request, session)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Status         string      `json:"status"`
		ResponseLines  int         `json:"response_lines"`
		ResponseEvents []RCONEvent `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "executed" || result.ResponseLines != 2 ||
		len(result.ResponseEvents) != 3 {
		t.Fatalf("unexpected handler result: %+v", result)
	}
	if result.ResponseEvents[0].Kind != "command" ||
		result.ResponseEvents[0].Text != "operator > adminlogin SuperSecret" ||
		result.ResponseEvents[1].Kind != "response" ||
		result.ResponseEvents[2].Text != "한국어 응답" {
		t.Fatalf("unexpected command events: %#v", result.ResponseEvents)
	}

	history := manager.rconHistory(rconBrowserHistoryLimit)
	if len(history) != 3 || history[2].Text != "한국어 응답" {
		t.Fatalf("command output was not retained: %#v", history)
	}
	auditData, err := os.ReadFile(manager.auditPath)
	if err != nil {
		t.Fatal(err)
	}
	auditText := string(auditData)
	for _, expected := range []string{
		`"event":"rcon_command_executed"`,
		`"account":"operator"`,
		`"command_name":"adminlogin"`,
		`"response_lines":"2"`,
	} {
		if !strings.Contains(auditText, expected) {
			t.Fatalf("audit log is missing %q: %s", expected, auditText)
		}
	}
	if strings.Contains(auditText, "SuperSecret") {
		t.Fatal("RCON command arguments leaked into the web audit log")
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
	line := `[2026.07.23-20.23.14:871][328]LogGameMode: Display: (ALL) Name, WithComma: "quoted", 0123456789ABCDEF: "한국어: "인용" — Русский — 简体中文"`
	chat, ok := parseMordhauChatLogLine(line)
	if !ok {
		t.Fatal("valid MORDHAU chat log line was rejected")
	}
	expected := `Chat: 0123456789ABCDEF, Name, WithComma: "quoted", (ALL) 한국어: "인용" — Русский — 简体中文`
	if chat != expected {
		t.Fatalf("chat log conversion = %q", chat)
	}
}

func TestGameLogFollowerReadsFileCreatedAfterStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Mordhau.log")
	follower := &gameLogFollower{path: path}
	available, err := follower.initialize(func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if available {
		t.Fatal("missing game log was reported as available")
	}

	line := `[2026.07.23-20.00.00:000][1]LogGameMode: Display: (ALL) Player, 0123456789ABCDEF: "첫 메시지"` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	lines, replaced, available, err := follower.readNewLines()
	if err != nil {
		t.Fatal(err)
	}
	if !available || !replaced || len(lines) != 1 || lines[0] != strings.TrimSuffix(line, "\n") {
		t.Fatalf(
			"newly created game log = available %t, replaced %t, lines %#v",
			available,
			replaced,
			lines,
		)
	}
}

func TestGameLogFollowerStartsAtEndAndFollowsPartialLinesAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Mordhau.log")
	oldLine := `[2026.07.23-20.00.00:000][1]LogGameMode: Display: (ALL) Old, 0123456789ABCDEF: "old"` + "\n"
	if err := os.WriteFile(path, []byte(oldLine), 0600); err != nil {
		t.Fatal(err)
	}

	follower := &gameLogFollower{path: path}
	historical := make([]string, 0, 1)
	available, err := follower.initialize(func(line string) {
		historical = append(historical, line)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !available || len(historical) != 1 || historical[0] != strings.TrimSuffix(oldLine, "\n") {
		t.Fatalf("historical state scan = available %t, lines %#v", available, historical)
	}

	newLine := `[2026.07.23-20.00.01:000][2]LogGameMode: Display: (TEAM) Player, 1234567890ABCDEF: "새 메시지"`
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
	lines, _, _, err := follower.readNewLines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("partial game-log line was emitted: %#v", lines)
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
	lines, replaced, available, err := follower.readNewLines()
	if err != nil {
		t.Fatal(err)
	}
	if !available || replaced || len(lines) != 1 || lines[0] != newLine {
		t.Fatalf(
			"completed game-log line = available %t, replaced %t, lines %#v",
			available,
			replaced,
			lines,
		)
	}

	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	rotatedLine := `[2026.07.23-20.00.02:000][3]LogGameMode: Display: (ALL) Player, 1234567890ABCDEF: "회전 후 메시지"` + "\n"
	if err := os.WriteFile(path, []byte(rotatedLine), 0600); err != nil {
		t.Fatal(err)
	}
	lines, replaced, available, err = follower.readNewLines()
	if err != nil {
		t.Fatal(err)
	}
	if !available || !replaced || len(lines) != 1 ||
		lines[0] != strings.TrimSuffix(rotatedLine, "\n") {
		t.Fatalf(
			"rotated game log = available %t, replaced %t, lines %#v",
			available,
			replaced,
			lines,
		)
	}
}

func TestGameLogProcessorPreservesUnicodePlayerLifecycle(t *testing.T) {
	const playerID = "D5E9DEF6A65BDE65"
	processor := newGameLogProcessor()
	lines := []string{
		`[2026.07.25-13.13.10:001][100]LogNet: Login request: ?Name=쿠아해병 userId: MordhauOnlineSubsystem:` + playerID + ` platform: Steam`,
		`[2026.07.25-13.13.11:002][101]LogMordhauGameSession: Player authentication for ???? (` + playerID + `) completed successfully`,
		`[2026.07.25-13.13.12:003][102]LogGameMode: Display: (ALL) 쿠아해병, ` + playerID + `: "한국어 — Русский — 简体中文"`,
		`[2026.07.25-13.13.14:004][103]LogNet: UChannel::CleanUp: ChIndex == 0. Closing connection. [UNetConnection] RemoteAddr: 127.0.0.1:1234, Name: IpConnection_0, Driver: GameNetDriver GameNetDriver, IsServer: YES, UniqueId: MordhauOnlineSubsystem:` + playerID,
	}

	var events []gameLogEvent
	for _, line := range lines {
		events = append(events, processor.processLine(line)...)
	}
	if len(events) != 3 {
		t.Fatalf("player lifecycle event count = %d: %#v", len(events), events)
	}
	if events[0].Kind != "login" ||
		events[0].Text != "Login: 2026.07.25-13.13.11: 쿠아해병 ("+playerID+") logged in" {
		t.Fatalf("login event = %#v", events[0])
	}
	if events[1].Kind != "chat" ||
		events[1].Text != "Chat: "+playerID+", 쿠아해병, (ALL) 한국어 — Русский — 简体中文" {
		t.Fatalf("chat event = %#v", events[1])
	}
	if events[2].Kind != "login" ||
		events[2].Text != "Login: 2026.07.25-13.13.14: 쿠아해병 ("+playerID+") logged out" {
		t.Fatalf("logout event = %#v", events[2])
	}
	for _, event := range events {
		if event.Time.Location() != time.Local {
			t.Fatalf("event time location = %s, want local", event.Time.Location())
		}
	}
}

func TestGameLogProcessorEmitsMatchKillScoreAndPunishmentEvents(t *testing.T) {
	processor := newGameLogProcessor()
	lines := []string{
		`[2026.07.25-13.20.00:001][200]LogGameMode: Display: Match State Changed from WaitingToStart to InProgress`,
		`[2026.07.25-13.20.01:002][201]LogGameMode: Display: 2026.07.25-13.20.01: Attacker (AAAAAAAAAAAAAAAA) killed Defender (BBBBBBBBBBBBBBBB)`,
		`[2026.07.25-13.20.02:003][202]LogGameMode: Display: 2026.07.25-13.20.02: Attacker (AAAAAAAAAAAAAAAA)'s score changed by 100 points and is now 200 points`,
		`[2026.07.25-13.20.03:004][203]LogMordhauGameSession: Display: Kicked player Attacker (AAAAAAAAAAAAAAAA)`,
	}
	wantKinds := []string{"matchstate", "killfeed", "scorefeed", "punishment"}
	wantText := []string{
		"MatchState: In progress",
		"Killfeed: 2026.07.25-13.20.01: Attacker (AAAAAAAAAAAAAAAA) killed Defender (BBBBBBBBBBBBBBBB)",
		"Scorefeed: 2026.07.25-13.20.02: Attacker (AAAAAAAAAAAAAAAA)'s score changed by 100 points and is now 200 points",
		"Punishment: Kicked player Attacker (AAAAAAAAAAAAAAAA)",
	}
	for index, line := range lines {
		events := processor.processLine(line)
		if len(events) != 1 || events[0].Kind != wantKinds[index] ||
			events[0].Text != wantText[index] {
			t.Fatalf("line %d events = %#v", index, events)
		}
	}
}

func TestRCONTransportStatusEventsAreHidden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mordhau-rcon.log")
	manager := &Manager{rconLogPath: path}
	if err := manager.loadRCONEventLog(); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{
		rconConnectedEvent,
		rconPreviousEvent,
		rconClosedEventPrefix + " read tcp: i/o timeout",
	} {
		manager.addRCONEvent("system", message)
	}
	manager.addRCONEvent("rcon", "Killfeed: retained")

	events := manager.rconHistory(rconBrowserHistoryLimit)
	if len(events) != 1 || events[0].Text != "Killfeed: retained" {
		t.Fatalf("transport status leaked into RCON history: %#v", events)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "RCON connected") ||
		strings.Contains(string(data), rconClosedEventPrefix) {
		t.Fatalf("transport status was persisted: %s", data)
	}
}

func TestStoredRCONTransportStatusEventsAreFiltered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mordhau-rcon.log")
	now := time.Now()
	stored := []RCONEvent{
		{Sequence: 1, Time: now, Text: rconConnectedEvent, Kind: "system"},
		{Sequence: 2, Time: now, Text: "Scorefeed: retained", Kind: "rcon"},
		{
			Sequence: 3,
			Time:     now,
			Text:     rconClosedEventPrefix + " read tcp: i/o timeout",
			Kind:     "system",
		},
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	for _, event := range stored {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{rconLogPath: path}
	if err := manager.loadRCONEventLog(); err != nil {
		t.Fatal(err)
	}
	events := manager.rconHistory(rconBrowserHistoryLimit)
	if len(events) != 1 ||
		events[0].Sequence != 2 ||
		events[0].Text != "Scorefeed: retained" ||
		manager.rconSequence != 3 {
		t.Fatalf("stored transport status was not filtered correctly: %#v", events)
	}
	manager.addRCONEvent("rcon", "Custom: next")
	events = manager.rconHistory(rconBrowserHistoryLimit)
	if len(events) != 2 || events[1].Sequence != 4 {
		t.Fatalf("sequence did not continue after filtered records: %#v", events)
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
	if record.Account != "operator" ||
		record.ClientIP != "203.0.113.40" ||
		record.PeerIP != "203.0.113.40" {
		t.Fatalf(
			"unexpected audit actor: account=%q client=%q peer=%q",
			record.Account,
			record.ClientIP,
			record.PeerIP,
		)
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
			Action:      "restart",
			Successful:  true,
			StartedAt:   started,
			FinishedAt:  finished,
			Requested:   "operator",
			RequestedIP: "198.51.100.60",
			Output:      "update complete\nserver started",
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
		reloaded.op.RequestedIP != "198.51.100.60" ||
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
	if record.Status != http.StatusNoContent ||
		record.ClientIP != "2001:db8::10" ||
		record.PeerIP != "2001:db8::10" {
		t.Fatalf(
			"unexpected access result: status=%d client=%q peer=%q",
			record.Status,
			record.ClientIP,
			record.PeerIP,
		)
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

func TestDashboardThemeAndServerPromptMarkup(t *testing.T) {
	indexData, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexData)
	for _, expected := range []string{
		`id="theme-toggle"`,
		`content="width=device-width, initial-scale=1, viewport-fit=cover"`,
		`src="/static/theme.js?v=2.1.1"`,
		`<p class="eyebrow">SERVER EVENTS</p>`,
		`id="server-event-console"`,
		`id="server-prompt-form"`,
		`id="server-prompt-mode"`,
		`<option value="rcon">RCON</option>`,
		`<option value="say">SAY</option>`,
		`id="server-prompt-input"`,
		`id="server-prompt-submit"`,
		`id="mods-refresh-minutes"`,
		`min="1" max="10080"`,
		`value="5"`,
		`id="mods-restart-on-update"`,
		`data-panel="runtime"`,
		`id="players-value"`,
		`id="runtime-targets"`,
		`id="runtime-properties"`,
		`id="runtime-edit-dialog"`,
		`id="runtime-edit-select"`,
		`id="runtime-edit-input"`,
		`placeholder="Name, type, class, or current value"`,
		`placeholder="10.0.0.4 | 10.0.0.0/24 | 10.0.0.4-10.0.0.9"`,
		`id="new-rule-comment"`,
		`maxlength="160"`,
		`start-end`,
		`start~end`,
	} {
		if !strings.Contains(index, expected) {
			t.Fatalf("dashboard is missing %q", expected)
		}
	}
	for _, unwanted := range []string{
		"LIVE RCON",
		"Execute RCON Command",
		`<label for="rcon-message">Send Message</label>`,
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
	loginSource := string(loginData)
	if !strings.Contains(loginSource, `src="/static/theme.js?v=2.1.1"`) {
		t.Fatal("login page does not initialize the persisted theme")
	}
	if !strings.Contains(loginSource, `viewport-fit=cover`) {
		t.Fatal("login page does not enable mobile safe-area layout")
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
		`/api/server/events/history`,
		`/api/rcon/command`,
		`/api/runtime/status`,
		`/api/runtime/target`,
		`/api/runtime/property`,
		`function runtimePlayerGroups(targets)`,
		`function validateRuntimeEditor()`,
		`function runtimePropertyMatchesQuery(property, query)`,
		`property.value.toLocaleLowerCase().includes(query)`,
		`loadRuntimeTarget({ manual: true })`,
		`Last successful refresh:`,
		`restart_on_update: enabled`,
		`resolvedOptions().timeZone`,
		`comment: comment.value`,
		`typeof rule.comment === "string"`,
		`event.text.startsWith("RCON connection closed:")`,
		`action: "set_section_enabled"`,
		`section_id: section.id || ""`,
		`entry_id: entry.id || ""`,
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

func TestMobileLayoutHasTouchAndNarrowViewportRules(t *testing.T) {
	cssData, err := staticFiles.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssData)
	for _, expected := range []string{
		`@media (max-width: 720px)`,
		`@media (max-width: 480px)`,
		`@media (max-width: 360px)`,
		`grid-template-areas: "server user theme logout"`,
		`min-height: 44px`,
		`font-size: 16px`,
		`env(safe-area-inset-bottom)`,
		`overflow-wrap: anywhere`,
		`touch-action: manipulation`,
		`.access-rule-row`,
		`.config-section.disabled`,
	} {
		if !strings.Contains(css, expected) {
			t.Fatalf("mobile stylesheet is missing %q", expected)
		}
	}
	for _, unwanted := range []string{
		`.server-pill { display: none; }`,
		`.user-chip { display: none; }`,
	} {
		if strings.Contains(css, unwanted) {
			t.Fatalf("mobile stylesheet hides important status with %q", unwanted)
		}
	}
}

func TestModDocumentMutationsPreserveScopeAndOrdering(t *testing.T) {
	original := []byte("[" + modIOGameSessionSection + "]\r\n" +
		"Mods=10\r\n" +
		disabledEntryPrefix + "Mods=20\r\n" +
		"[Other]\r\n" +
		"Mods=999\r\n")
	store := newDisabledINIFile()
	migrated, count, err := migrateLegacyDisabledEntries(
		"Game.ini",
		original,
		&store,
	)
	if err != nil || count != 1 {
		t.Fatalf("legacy setup migration: count=%d err=%v", count, err)
	}
	document := parseIni(migrated)

	change, err := addModsToConfigState(&document, &store, []int{10, 20, 30})
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

	change, err = setModEnabledInConfigState(&document, &store, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed ||
		strings.Contains(string(document.bytes()), disabledEntryPrefix) {
		t.Fatal("configured mod was not disabled")
	}

	change, err = removeModFromConfigState(&document, &store, 20)
	if err != nil {
		t.Fatal(err)
	}
	result = string(document.bytes())
	mods, _ := configuredModsFromState(document.bytes(), store)
	for _, mod := range mods {
		if mod.ID == 20 {
			t.Fatal("configured mod was not removed from active or disabled state")
		}
	}
	if !change.Changed || strings.Contains(result, "Mods=20") {
		t.Fatal("configured mod was not removed")
	}
	if !strings.Contains(result, "[Other]\r\nMods=999") {
		t.Fatal("a Mods entry in another section was changed")
	}
}

func TestModMutationsUsePersistentDisabledState(t *testing.T) {
	document := parseIni([]byte("[" + modIOGameSessionSection + "]\n" +
		"Mods=10\n" +
		"Mods=20\n"))
	store := newDisabledINIFile()

	change, err := setModEnabledInConfigState(&document, &store, 20, false)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed || len(store.Entries) != 1 ||
		strings.Contains(string(document.bytes()), disabledEntryPrefix) ||
		strings.Contains(string(document.bytes()), "Mods=20") {
		t.Fatalf("mod was not moved into persistent disabled state: %+v\n%s",
			store,
			document.bytes(),
		)
	}
	mods, invalid := configuredModsFromState(document.bytes(), store)
	if invalid != 0 || len(mods) != 2 || mods[1].ID != 20 || mods[1].Enabled {
		t.Fatalf("persistent disabled mod was not visible: %+v, invalid=%d", mods, invalid)
	}

	change, err = addModsToConfigState(&document, &store, []int{20, 30})
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed ||
		!slicesEqual(change.Reenabled, []int{20}) ||
		!slicesEqual(change.Added, []int{30}) ||
		len(store.Entries) != 0 {
		t.Fatalf("mod re-enable/add result = %+v, state=%+v", change, store)
	}
	if got := string(document.bytes()); got !=
		"["+modIOGameSessionSection+"]\nMods=10\nMods=20\nMods=30\n" {
		t.Fatalf("mod order after re-enable/add:\n%s", got)
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
