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
				ID:         "player_controller:11:21",
				Kind:       "player_controller",
				Class:      "BP_TestController_C",
				PlayerSlot: 0,
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
		view.Targets[1].Kind != "player_controller" {
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
