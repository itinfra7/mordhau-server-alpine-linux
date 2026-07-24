package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func modViewWithFile(modID, modfileID int, enabled bool) ConfiguredMod {
	return ConfiguredMod{
		ID:      modID,
		Enabled: enabled,
		Metadata: &ModIOItem{
			ID: modID,
			Modfile: &ModIOFile{
				ID:      modfileID,
				Version: "file-" + time.Unix(int64(modfileID), 0).UTC().Format("150405"),
			},
		},
	}
}

func TestModRefreshSettingsDefaultAndVersionOneMigration(t *testing.T) {
	t.Run("new default", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mod-refresh.json")
		manager := &Manager{modRefreshSettingsFile: path}
		if err := manager.loadOrCreateModRefreshSettings(); err != nil {
			t.Fatal(err)
		}
		if manager.modRefreshSettings.Version != modRefreshSettingsVersion ||
			manager.modRefreshSettings.IntervalMinutes != 5 ||
			manager.modRefreshSettings.RestartOnUpdate {
			t.Fatalf("unexpected default settings: %+v", manager.modRefreshSettings)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("settings mode = %o, want 0600", info.Mode().Perm())
		}
	})

	t.Run("version one", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mod-refresh.json")
		legacy := modRefreshSettingsFile{
			Version:         1,
			IntervalMinutes: 10,
		}
		if err := writeJSONAtomic(path, legacy, 0644); err != nil {
			t.Fatal(err)
		}
		manager := &Manager{modRefreshSettingsFile: path}
		if err := manager.loadOrCreateModRefreshSettings(); err != nil {
			t.Fatal(err)
		}
		if manager.modRefreshSettings.Version != modRefreshSettingsVersion ||
			manager.modRefreshSettings.IntervalMinutes != 10 ||
			manager.modRefreshSettings.RestartOnUpdate {
			t.Fatalf("migration changed the saved interval: %+v", manager.modRefreshSettings)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("migrated settings mode = %o, want 0600", info.Mode().Perm())
		}
	})
}

func TestAutomaticModRestartRequiresSavedAPIKey(t *testing.T) {
	temp := t.TempDir()
	enabled := true
	manager := &Manager{
		modIOSettingsFile:      filepath.Join(temp, "modio.json"),
		modRefreshSettingsFile: filepath.Join(temp, "mod-refresh.json"),
		modRefreshSettings: modRefreshSettingsFile{
			Version:         modRefreshSettingsVersion,
			IntervalMinutes: defaultModRefreshMinutes,
		},
		modCacheReady: true,
	}
	if _, err := manager.setModRefreshSettings(nil, &enabled); err == nil {
		t.Fatal("automatic restart was enabled without a saved API key")
	}

	modio := modIOSettingsFile{
		Version:  1,
		APIKey:   strings.Repeat("a", 32),
		APIBase:  defaultModIOAPIBase,
		GameID:   11,
		GameName: "MORDHAU",
		SavedAt:  time.Now(),
	}
	if err := writeJSONAtomic(manager.modIOSettingsFile, modio, 0600); err != nil {
		t.Fatal(err)
	}
	view, err := manager.setModRefreshSettings(nil, &enabled)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Refresh.RestartOnUpdate || !manager.modRefreshSettings.RestartOnUpdate {
		t.Fatal("saved API key did not allow automatic restart")
	}

	var stored modRefreshSettingsFile
	if err := readJSON(manager.modRefreshSettingsFile, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.RestartOnUpdate {
		t.Fatal("automatic restart setting was not persisted")
	}
}

func TestActiveModUpdateDetectionUsesNewModfileIDsOnly(t *testing.T) {
	previous := []trackedModfile{
		{ModID: 10, ModfileID: 100},
		{ModID: 20, ModfileID: 200},
	}
	current := []trackedModfile{
		{ModID: 10, ModfileID: 101, Version: "new"},
		{ModID: 20, ModfileID: 200, Version: "metadata-only-edit"},
		{ModID: 30, ModfileID: 300, Version: "newly-enabled"},
	}
	updates := changedActiveModfiles(previous, current)
	if len(updates) != 1 ||
		updates[0].ModID != 10 ||
		updates[0].PreviousFile != 100 ||
		updates[0].CurrentFile != 101 {
		t.Fatalf("unexpected detected updates: %+v", updates)
	}

	view := ModManagementView{Mods: []ConfiguredMod{
		modViewWithFile(30, 300, true),
		modViewWithFile(20, 200, false),
		modViewWithFile(10, 101, true),
	}}
	baseline := activeModfileBaseline(view)
	if len(baseline) != 2 ||
		baseline[0].ModID != 10 ||
		baseline[1].ModID != 30 {
		t.Fatalf("active baseline includes disabled or unsorted mods: %+v", baseline)
	}
}

func TestSuccessfulRefreshBuildsBaselineBeforeScheduling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mod-update-state.json")
	manager := &Manager{
		modRefreshSettings: modRefreshSettingsFile{
			Version:         modRefreshSettingsVersion,
			IntervalMinutes: defaultModRefreshMinutes,
			RestartOnUpdate: true,
		},
		modUpdateState: modUpdateStateFile{
			Version:  modUpdateStateVersion,
			Baseline: []trackedModfile{},
		},
		modUpdateStateFile:   path,
		modUpdateStateLoaded: true,
		modServerProcess: func() (int, bool) {
			return 4321, true
		},
	}
	at := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	view := ModManagementView{
		Settings: ModIOSettingsView{APIKeyConfigured: true},
		Mods:     []ConfiguredMod{modViewWithFile(10, 100, true)},
	}
	first, err := manager.recordSuccessfulModRefreshLocked(view, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Updates) != 0 || manager.modUpdateState.Schedule != nil {
		t.Fatalf("initial baseline scheduled a restart: %+v", first)
	}

	view.Mods[0] = modViewWithFile(10, 101, true)
	second, err := manager.recordSuccessfulModRefreshLocked(view, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !second.RestartScheduled ||
		len(second.Updates) != 1 ||
		manager.modUpdateState.Schedule == nil {
		t.Fatalf("new modfile did not schedule a restart: %+v", second)
	}
	if got := manager.modUpdateState.Schedule.RestartAt; !got.Equal(at.Add(11 * time.Minute)) {
		t.Fatalf("restart time = %s, want %s", got, at.Add(11*time.Minute))
	}
}

func TestModRestartCountdownAndManagedRestart(t *testing.T) {
	temp := t.TempDir()
	started := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)
	schedule := &modRestartSchedule{
		DetectedAt: started,
		RestartAt:  started.Add(modRestartDelay),
		ServerPID:  9876,
		Updates: []modfileUpdate{
			{ModID: 42, PreviousFile: 100, CurrentFile: 101},
		},
	}
	state := modUpdateStateFile{
		Version:  modUpdateStateVersion,
		Baseline: []trackedModfile{{ModID: 42, ModfileID: 101}},
		Schedule: schedule,
	}
	statePath := filepath.Join(temp, "mod-update-state.json")
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}

	var messages []string
	var operations []string
	manager := &Manager{
		modRefreshSettings: modRefreshSettingsFile{
			Version:         modRefreshSettingsVersion,
			IntervalMinutes: defaultModRefreshMinutes,
			RestartOnUpdate: true,
		},
		modUpdateState:       state,
		modUpdateStateFile:   statePath,
		modUpdateStateLoaded: true,
		auditPath:            filepath.Join(temp, "audit.log"),
		modServerProcess: func() (int, bool) {
			return 9876, true
		},
		modRestartMessageSend: func(message string) error {
			messages = append(messages, message)
			return nil
		},
		operationStart: func(action, username, clientIP, peerIP string) error {
			operations = append(operations, strings.Join(
				[]string{action, username, clientIP, peerIP},
				":",
			))
			return nil
		},
	}

	offsets := []time.Duration{
		0,
		5 * time.Minute,
		6 * time.Minute,
		7 * time.Minute,
		8 * time.Minute,
		9 * time.Minute,
	}
	for index, offset := range offsets {
		manager.processModRestartSchedule(started.Add(offset))
		if len(messages) != index+1 {
			t.Fatalf("notice %d was not sent at %s: %v", index, offset, messages)
		}
	}
	manager.processModRestartSchedule(started.Add(10 * time.Minute))

	wantMessages := []string{
		modRestartNoticeMessage(10),
		modRestartNoticeMessage(5),
		modRestartNoticeMessage(4),
		modRestartNoticeMessage(3),
		modRestartNoticeMessage(2),
		modRestartNoticeMessage(1),
		finalModRestartMessage(),
	}
	if len(messages) != len(wantMessages) {
		t.Fatalf("message count = %d, want %d: %v", len(messages), len(wantMessages), messages)
	}
	for index := range wantMessages {
		if messages[index] != wantMessages[index] {
			t.Fatalf("message %d = %q, want %q", index, messages[index], wantMessages[index])
		}
	}
	if len(operations) != 1 || operations[0] != "restart:system:local:local" {
		t.Fatalf("managed restart calls = %v", operations)
	}
	if manager.modUpdateState.Schedule != nil {
		t.Fatal("accepted restart left a pending countdown")
	}
}

func TestStaleModRestartScheduleIsCancelled(t *testing.T) {
	temp := t.TempDir()
	started := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	state := modUpdateStateFile{
		Version:  modUpdateStateVersion,
		Baseline: []trackedModfile{{ModID: 42, ModfileID: 101}},
		Schedule: &modRestartSchedule{
			DetectedAt: started,
			RestartAt:  started.Add(modRestartDelay),
			ServerPID:  100,
			Updates: []modfileUpdate{
				{ModID: 42, PreviousFile: 100, CurrentFile: 101},
			},
		},
	}
	manager := &Manager{
		modRefreshSettings: modRefreshSettingsFile{
			Version:         modRefreshSettingsVersion,
			IntervalMinutes: defaultModRefreshMinutes,
			RestartOnUpdate: true,
		},
		modUpdateState:       state,
		modUpdateStateFile:   filepath.Join(temp, "mod-update-state.json"),
		modUpdateStateLoaded: true,
		auditPath:            filepath.Join(temp, "audit.log"),
		modServerProcess: func() (int, bool) {
			return 200, true
		},
	}
	if err := writeJSONAtomic(manager.modUpdateStateFile, state, 0600); err != nil {
		t.Fatal(err)
	}
	manager.processModRestartSchedule(started)
	if manager.modUpdateState.Schedule != nil {
		t.Fatal("schedule for a different server process was retained")
	}
}

func TestFinalRestartRechecksSettingAfterAnnouncement(t *testing.T) {
	temp := t.TempDir()
	started := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	state := modUpdateStateFile{
		Version:  modUpdateStateVersion,
		Baseline: []trackedModfile{{ModID: 42, ModfileID: 101}},
		Schedule: &modRestartSchedule{
			DetectedAt: started,
			RestartAt:  started.Add(modRestartDelay),
			ServerPID:  300,
			Updates: []modfileUpdate{
				{ModID: 42, PreviousFile: 100, CurrentFile: 101},
			},
			NextNotice: len(modRestartNotices),
		},
	}
	manager := &Manager{
		modRefreshSettings: modRefreshSettingsFile{
			Version:         modRefreshSettingsVersion,
			IntervalMinutes: defaultModRefreshMinutes,
			RestartOnUpdate: true,
		},
		modUpdateState:       state,
		modUpdateStateFile:   filepath.Join(temp, "mod-update-state.json"),
		modUpdateStateLoaded: true,
		auditPath:            filepath.Join(temp, "audit.log"),
		modServerProcess: func() (int, bool) {
			return 300, true
		},
	}
	if err := writeJSONAtomic(manager.modUpdateStateFile, state, 0600); err != nil {
		t.Fatal(err)
	}
	manager.modRestartMessageSend = func(string) error {
		manager.modsMu.Lock()
		manager.modRefreshSettings.RestartOnUpdate = false
		manager.modsMu.Unlock()
		return nil
	}
	called := false
	manager.operationStart = func(_, _, _, _ string) error {
		called = true
		return nil
	}

	next := manager.processModRestartSchedule(started.Add(modRestartDelay))
	if called {
		t.Fatal("restart was requested after the setting was disabled during the final notice")
	}
	if next.IsZero() {
		t.Fatal("disabled schedule was not left eligible for prompt cancellation")
	}
}
