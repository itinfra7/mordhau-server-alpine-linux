package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeBridgeStatusIsCollectedOnceForSnapshots(t *testing.T) {
	directory := t.TempDir()
	statusPath := filepath.Join(directory, "status.json")
	requestPath := filepath.Join(directory, "request.txt")
	responsePath := filepath.Join(directory, "response.json")
	ping := 42
	status := runtimeBridgeStatusFile{
		Version:               1,
		Ready:                 true,
		PlayerControllerCount: 2,
		GameModeClass:         "BP_TestGameMode_C",
		TargetCount:           3,
		Targets: []RuntimeTarget{
			{
				ID:         "game_mode:10:20",
				Kind:       "game_mode",
				Class:      "BP_TestGameMode_C",
				PlayerSlot: -1,
			},
			{
				ID:                "player_controller:11:21",
				Kind:              "player_controller",
				Class:             "BP_TestController_C",
				PlayerSlot:        0,
				PlayerName:        "테스트 사용자",
				PlayFabID:         "ABCDEF0123456789",
				Platform:          "Epic",
				PlatformAccountID: "test-account",
				PingMS:            &ping,
			},
			{
				ID:         "player_controller:12:22",
				Kind:       "player_controller",
				Class:      "BP_TestController_C",
				PlayerSlot: 1,
			},
		},
	}
	if err := writeJSONAtomic(statusPath, status, 0600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		runtimeStatusPath:    statusPath,
		runtimeRequestPath:   requestPath,
		runtimeResponsePath:  responsePath,
		runtimeTargetCache:   make(map[string]runtimeTargetCacheEntry),
		runtimeServerProcess: func() (int, bool) { return 123, true },
	}
	manager.sampleRuntimeBridgeStatus()

	summary := manager.runtimeSummaryView()
	if !summary.Ready || summary.Status != "ready" ||
		summary.PlayerControllerCount != 2 ||
		summary.GameModeClass != "BP_TestGameMode_C" {
		t.Fatalf("unexpected runtime summary: %+v", summary)
	}
	view := manager.runtimeStatusView()
	if !view.Ready || len(view.Targets) != 3 ||
		view.Targets[1].Kind != "player_controller" ||
		view.Targets[1].PlayerName != "테스트 사용자" ||
		view.Targets[1].PlayFabID != "ABCDEF0123456789" ||
		view.Targets[1].Platform != "Epic" ||
		view.Targets[1].PingMS == nil ||
		*view.Targets[1].PingMS != ping {
		t.Fatalf("unexpected runtime target view: %+v", view)
	}
}

func TestRuntimeBridgeStatusRejectsStaleAndStoppedState(t *testing.T) {
	directory := t.TempDir()
	statusPath := filepath.Join(directory, "status.json")
	status := runtimeBridgeStatusFile{
		Version:     1,
		Ready:       true,
		TargetCount: 0,
		Targets:     []RuntimeTarget{},
	}
	if err := writeJSONAtomic(statusPath, status, 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-runtimeBridgeStaleAfter - time.Second)
	if err := os.Chtimes(statusPath, old, old); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		runtimeStatusPath:    statusPath,
		runtimeServerProcess: func() (int, bool) { return 123, true },
	}
	manager.sampleRuntimeBridgeStatus()
	if summary := manager.runtimeSummaryView(); summary.Ready || summary.Status != "stale" {
		t.Fatalf("stale status accepted: %+v", summary)
	}

	manager.runtimeServerProcess = func() (int, bool) { return 0, false }
	manager.sampleRuntimeBridgeStatus()
	if summary := manager.runtimeSummaryView(); summary.Ready || summary.Status != "server_stopped" {
		t.Fatalf("stopped server status accepted: %+v", summary)
	}
}

func TestRuntimeTargetUsesSerializedServerCache(t *testing.T) {
	directory := t.TempDir()
	requestPath := filepath.Join(directory, "request.txt")
	responsePath := filepath.Join(directory, "response.json")
	targetID := "game_mode:10:20"
	manager := &Manager{
		runtimeSummary:      RuntimeBridgeSummary{Ready: true, Status: "ready"},
		runtimeRequestPath:  requestPath,
		runtimeResponsePath: responsePath,
		runtimeTargetCache:  make(map[string]runtimeTargetCacheEntry),
	}
	var requests atomic.Int32
	workerDone := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(requestPath)
			if errors.Is(err, os.ErrNotExist) {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if err != nil {
				workerDone <- err
				return
			}
			_ = os.Remove(requestPath)
			fields := strings.Split(strings.TrimSpace(string(data)), "\t")
			if len(fields) != 4 || fields[0] != "V1" ||
				fields[2] != "GET" || fields[3] != targetID {
				workerDone <- errors.New("unexpected runtime request")
				return
			}
			requests.Add(1)
			response := RuntimeTargetView{
				Version:   1,
				RequestID: fields[1],
				OK:        true,
				Target: RuntimeTarget{
					ID:         targetID,
					Kind:       "game_mode",
					Class:      "BP_TestGameMode_C",
					PlayerSlot: -1,
				},
				ClassChain:    []string{"BP_TestGameMode_C", "GameMode"},
				Properties:    []RuntimeProperty{},
				PropertyCount: 0,
			}
			workerDone <- writeJSONAtomic(responsePath, response, 0600)
			return
		}
		workerDone <- errors.New("runtime request was not observed")
	}()

	first, err := manager.runtimeTarget(context.Background(), targetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-workerDone; err != nil {
		t.Fatal(err)
	}
	second, err := manager.runtimeTarget(context.Background(), targetID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Target.ID != targetID || second.Target.ID != targetID {
		t.Fatal("runtime target response was not retained")
	}
	if requests.Load() != 1 {
		t.Fatalf("runtime requests = %d, want one server-side request", requests.Load())
	}
}

func TestRuntimeInputValidationPreservesUnicode(t *testing.T) {
	if !validRuntimeTargetID("player_state:123:456") {
		t.Fatal("valid target ID rejected")
	}
	for _, invalid := range []string{"", "player state:1:2", "../target", "대상:1:2"} {
		if validRuntimeTargetID(invalid) {
			t.Fatalf("invalid target ID accepted: %q", invalid)
		}
	}
	for _, value := range []string{"한국어", "Русский", "简体中文", "Français"} {
		if !validRuntimeValue(value) {
			t.Fatalf("valid Unicode runtime value rejected: %q", value)
		}
		encoded := runtimeHex(value)
		if encoded == "" || strings.Contains(encoded, value) {
			t.Fatalf("runtime value was not encoded: %q", value)
		}
	}
}

func TestRuntimeBridgeStatusRejectsIdentityOnNonControllerTarget(t *testing.T) {
	directory := t.TempDir()
	statusPath := filepath.Join(directory, "status.json")
	status := runtimeBridgeStatusFile{
		Version:               1,
		Ready:                 true,
		PlayerControllerCount: 1,
		TargetCount:           2,
		Targets: []RuntimeTarget{
			{
				ID:         "player_controller:11:21",
				Kind:       "player_controller",
				Class:      "BP_TestController_C",
				PlayerSlot: 0,
			},
			{
				ID:         "player_state:12:22",
				Kind:       "player_state",
				Class:      "BP_TestPlayerState_C",
				PlayerSlot: 0,
				PlayerName: "must-not-be-here",
			},
		},
	}
	if err := writeJSONAtomic(statusPath, status, 0600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		runtimeStatusPath:    statusPath,
		runtimeServerProcess: func() (int, bool) { return 123, true },
	}
	manager.sampleRuntimeBridgeStatus()
	if summary := manager.runtimeSummaryView(); summary.Ready ||
		summary.Status != "invalid_status" {
		t.Fatalf("invalid identity placement accepted: %+v", summary)
	}
}

func TestRuntimeBridgeStatusPersistsVerifiedSteamIdentity(t *testing.T) {
	directory := t.TempDir()
	statusPath := filepath.Join(directory, "status.json")
	historyPath := filepath.Join(directory, "players.json")
	status := runtimeBridgeStatusFile{
		Version:               1,
		Ready:                 true,
		PlayerControllerCount: 1,
		TargetCount:           1,
		Targets: []RuntimeTarget{{
			ID:                "player_controller:11:21",
			Kind:              "player_controller",
			Class:             "BP_TestController_C",
			PlayerSlot:        0,
			PlayerName:        "Test Player",
			PlayFabID:         testPlayerID,
			Platform:          "Steam",
			PlatformAccountID: testSteamID64,
		}},
	}
	if err := writeJSONAtomic(statusPath, status, 0600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		runtimeStatusPath:    statusPath,
		runtimeServerProcess: func() (int, bool) { return 123, true },
		playerHistoryFile:    historyPath,
		playerHistory: playerHistoryFile{
			Version: playerHistoryVersion,
		},
	}
	manager.sampleRuntimeBridgeStatus()
	if summary := manager.runtimeSummaryView(); !summary.Ready {
		t.Fatalf("valid Steam identity rejected: %+v", summary)
	}
	if len(manager.playerHistory.Players) != 1 ||
		manager.playerHistory.Players[0].Platform != "Steam" ||
		manager.playerHistory.Players[0].PlatformAccountID !=
			testSteamID64 {
		t.Fatalf(
			"Steam identity was not persisted: %+v",
			manager.playerHistory.Players,
		)
	}
}

func TestRuntimeBridgeStatusRejectsMalformedSteamID64(t *testing.T) {
	directory := t.TempDir()
	statusPath := filepath.Join(directory, "status.json")
	status := runtimeBridgeStatusFile{
		Version:               1,
		Ready:                 true,
		PlayerControllerCount: 1,
		TargetCount:           1,
		Targets: []RuntimeTarget{{
			ID:                "player_controller:11:21",
			Kind:              "player_controller",
			Class:             "BP_TestController_C",
			PlayerSlot:        0,
			PlayFabID:         testPlayerID,
			Platform:          "Steam",
			PlatformAccountID: "not-a-steam-id",
		}},
	}
	if err := writeJSONAtomic(statusPath, status, 0600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		runtimeStatusPath:    statusPath,
		runtimeServerProcess: func() (int, bool) { return 123, true },
	}
	manager.sampleRuntimeBridgeStatus()
	if summary := manager.runtimeSummaryView(); summary.Ready ||
		summary.Status != "invalid_status" {
		t.Fatalf("malformed SteamID64 accepted: %+v", summary)
	}
}

func TestRuntimePropertyEditorsAndValidation(t *testing.T) {
	value := func(text string) *string { return &text }
	testCases := []struct {
		name     string
		property RuntimeProperty
		valid    []string
		invalid  []string
		kind     string
		editable bool
	}{
		{
			name: "boolean",
			property: RuntimeProperty{
				Type: "BoolProperty", Editable: true, Value: value("False"),
			},
			valid:    []string{"True", "False"},
			invalid:  []string{"true", "1", ""},
			kind:     "boolean",
			editable: true,
		},
		{
			name: "signed integer",
			property: RuntimeProperty{
				Type: "Int8Property", Editable: true, Value: value("1"),
			},
			valid:    []string{"-128", "0", "+127"},
			invalid:  []string{"-129", "128", "1.0", " 1"},
			kind:     "integer",
			editable: true,
		},
		{
			name: "uint64",
			property: RuntimeProperty{
				Type: "UInt64Property", Editable: true, Value: value("0"),
			},
			valid:    []string{"0", "18446744073709551615"},
			invalid:  []string{"-1", "18446744073709551616"},
			kind:     "integer",
			editable: true,
		},
		{
			name: "float",
			property: RuntimeProperty{
				Type: "FloatProperty", Editable: true, Value: value("1.000000"),
			},
			valid:    []string{"0", "-1.25", "3.4e+38"},
			invalid:  []string{"NaN", "Inf", "0x1p2", "3.5e+38", "1e-46"},
			kind:     "number",
			editable: true,
		},
		{
			name: "enum",
			property: RuntimeProperty{
				Type:       "EnumProperty",
				Editable:   true,
				Value:      value("Disabled"),
				EnumValues: []string{"Disabled", "Enabled"},
			},
			valid:    []string{"Disabled", "Enabled"},
			invalid:  []string{"2", "enabled", ""},
			kind:     "select",
			editable: true,
		},
		{
			name: "structured text",
			property: RuntimeProperty{
				Type: "StructProperty", Editable: true, Value: value("(X=1,Y=2)"),
			},
			valid:    []string{"", "(X=1,Y=(A=\"한국어\"))"},
			invalid:  []string{"(X=1", "(X='unterminated)"},
			kind:     "unreal_text",
			editable: true,
		},
		{
			name: "empty string",
			property: RuntimeProperty{
				Type: "StrProperty", Editable: true, Value: value("text"),
			},
			valid:    []string{"", "Русский"},
			kind:     "string",
			editable: true,
		},
		{
			name: "enum metadata missing",
			property: RuntimeProperty{
				Type: "EnumProperty", Editable: true, Value: value("Disabled"),
			},
			kind:     "read_only",
			editable: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			property := testCase.property
			normalizeRuntimeProperty(&property)
			if property.Editor.Kind != testCase.kind ||
				property.Editable != testCase.editable {
				t.Fatalf("unexpected editor: %+v", property)
			}
			for _, candidate := range testCase.valid {
				if err := validateRuntimePropertyValue(property, candidate); err != nil {
					t.Fatalf("valid value %q rejected: %v", candidate, err)
				}
			}
			for _, candidate := range testCase.invalid {
				if err := validateRuntimePropertyValue(property, candidate); err == nil {
					t.Fatalf("invalid value %q accepted", candidate)
				}
			}
		})
	}
}

func TestRuntimeEnumSentinelsAreNotSelectable(t *testing.T) {
	value := "Enabled"
	property := RuntimeProperty{
		Type:       "EnumProperty",
		Editable:   true,
		Value:      &value,
		EnumValues: []string{"Disabled", "Enabled", "E_MAX", "MAX"},
	}

	normalizeRuntimeProperty(&property)
	if property.Editor.Kind != "select" || !property.Editable {
		t.Fatalf("unexpected enum editor: %+v", property)
	}
	if got := strings.Join(property.EnumValues, ","); got != "Disabled,Enabled" {
		t.Fatalf("selectable enum values = %q", got)
	}
	for _, sentinel := range []string{"E_MAX", "MAX"} {
		if err := validateRuntimePropertyValue(property, sentinel); err == nil {
			t.Fatalf("enum sentinel %q was accepted", sentinel)
		}
	}

	value = "E_MAX"
	property = RuntimeProperty{
		Type:       "EnumProperty",
		Editable:   true,
		Value:      &value,
		EnumValues: []string{"Disabled", "Enabled", "E_MAX"},
	}
	normalizeRuntimeProperty(&property)
	if property.Editable || property.Editor.Kind != "read_only" {
		t.Fatalf("sentinel current value should be read-only: %+v", property)
	}
}
