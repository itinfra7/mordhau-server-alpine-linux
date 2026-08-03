package manager

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testAutomaticUpdateManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	statePath := filepath.Join(root, "automatic-updates.json")
	state := automaticUpdateStateFile{
		Version:              automaticUpdateStateVersion,
		SteamEnabled:         true,
		ManagerRestartPolicy: modRestartPolicyCountdown,
		ManagerScheduledTime: defaultModRestartTime,
		SteamRestartPolicy:   modRestartPolicyCountdown,
		SteamScheduledTime:   defaultModRestartTime,
	}
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	steamStatePath := filepath.Join(root, "steam-update.json")
	if err := writeJSONAtomic(
		steamStatePath,
		steamUpdateStateFile{
			Version:       steamUpdateStateVersion,
			LatestBuildID: "17123456",
			CheckedAt:     time.Now(),
		},
		0600,
	); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "appmanifest_629800.acf")
	if err := os.WriteFile(
		manifestPath,
		[]byte(testSteamManifest),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	managerUpdatePath := filepath.Join(root, "manager-update.json")
	if err := writeJSONAtomic(
		managerUpdatePath,
		initialManagerUpdateState(),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		automaticUpdateState:     state,
		automaticUpdateStateFile: statePath,
		automaticUpdateWake:      make(chan struct{}, 1),
		steamUpdateStateFile:     steamStatePath,
		steamUpdateManifestFile:  manifestPath,
		auditPath:                filepath.Join(root, "audit.log"),
		automaticUpdateProcess:   func() (int, bool) { return 4321, true },
		automaticUpdateNow:       time.Now,
		managerUpdateStateFile:   managerUpdatePath,
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	return manager, statePath
}

func TestAutomaticUpdateFixtureUsesIsolatedManagerState(t *testing.T) {
	manager, _ := testAutomaticUpdateManager(t)
	if manager.managerUpdateStateFile == "" ||
		filepath.Clean(manager.managerUpdateStateFile) ==
			filepath.Clean(managerUpdateStatePath) {
		t.Fatalf(
			"automatic-update fixture uses installed manager state %q",
			manager.managerUpdateStateFile,
		)
	}
}

func TestAutomaticUpdateSettingsDefaultOffAndPersisted(t *testing.T) {
	root := t.TempDir()
	manager := &Manager{
		automaticUpdateStateFile: filepath.Join(
			root,
			"automatic-updates.json",
		),
		automaticUpdateWake: make(chan struct{}, 1),
	}
	if err := manager.loadOrCreateAutomaticUpdateState(); err != nil {
		t.Fatalf("create automatic update settings: %v", err)
	}
	view := manager.automaticUpdateView()
	if view.ManagerEnabled || view.SteamEnabled || view.Scheduled {
		t.Fatalf("automatic updates are not off by default: %+v", view)
	}
	view, err := manager.setAutomaticUpdateSettings(
		true,
		true,
		modRestartPolicyCountdown,
		defaultModRestartTime,
		modRestartPolicyCountdown,
		defaultModRestartTime,
	)
	if err != nil {
		t.Fatalf("save automatic update settings: %v", err)
	}
	if !view.ManagerEnabled || !view.SteamEnabled {
		t.Fatalf("saved settings not returned: %+v", view)
	}
	var persisted automaticUpdateStateFile
	if err := readJSON(manager.automaticUpdateStatePath(), &persisted); err != nil {
		t.Fatal(err)
	}
	if !persisted.ManagerEnabled || !persisted.SteamEnabled {
		t.Fatalf("settings not persisted: %+v", persisted)
	}
}

func TestAutomaticUpdateVersionOneMigrationPreservesEnabledSettings(
	t *testing.T,
) {
	root := t.TempDir()
	path := filepath.Join(root, "automatic-updates.json")
	detected := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	legacy := automaticUpdateStateFile{
		Version:        1,
		ManagerEnabled: true,
		SteamEnabled:   true,
		Schedule: &automaticUpdateSchedule{
			DetectedAt:     detected,
			RestartAt:      detected.Add(modRestartDelay),
			ServerPID:      1234,
			ManagerVersion: "2.5.0",
		},
	}
	if err := writeJSONAtomic(path, legacy, 0644); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{automaticUpdateStateFile: path}
	if err := manager.loadOrCreateAutomaticUpdateState(); err != nil {
		t.Fatalf("migrate automatic update state: %v", err)
	}
	state := manager.automaticUpdateState
	if state.Version != automaticUpdateStateVersion ||
		!state.ManagerEnabled ||
		!state.SteamEnabled ||
		state.ManagerRestartPolicy != modRestartPolicyCountdown ||
		state.SteamRestartPolicy != modRestartPolicyCountdown ||
		state.ManagerScheduledTime != defaultModRestartTime ||
		state.SteamScheduledTime != defaultModRestartTime ||
		state.Schedule == nil ||
		state.Schedule.Policy != modRestartPolicyCountdown {
		t.Fatalf("unexpected migrated automatic update state: %+v", state)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("migrated state mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestAutomaticUpdateSettingsHandlerRequiresCSRFAndPersists(
	t *testing.T,
) {
	root := t.TempDir()
	manager := &Manager{
		automaticUpdateStateFile: filepath.Join(
			root,
			"automatic-updates.json",
		),
		automaticUpdateWake: make(chan struct{}, 1),
		auditPath:           filepath.Join(root, "audit.log"),
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	if err := manager.loadOrCreateAutomaticUpdateState(); err != nil {
		t.Fatal(err)
	}
	session := Session{Username: "operator", CSRF: "csrf-token"}
	body := `{
		"manager_enabled":true,
		"steam_enabled":true,
		"manager_restart_policy":"scheduled",
		"manager_scheduled_time":"03:15",
		"steam_restart_policy":"when_empty",
		"steam_scheduled_time":"04:45"
	}`

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"http://manager.example/api/updates/automatic",
		strings.NewReader(body),
	)
	manager.automaticUpdateSettingsHandler(response, request, session)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing-CSRF status = %d", response.Code)
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodPost,
		"http://manager.example/api/updates/automatic",
		strings.NewReader(body),
	)
	request.Header.Set("X-CSRF-Token", session.CSRF)
	manager.automaticUpdateSettingsHandler(response, request, session)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"settings status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	view := manager.automaticUpdateView()
	if !view.ManagerEnabled ||
		!view.SteamEnabled ||
		view.ManagerRestartPolicy != modRestartPolicyScheduled ||
		view.ManagerScheduledTime != "03:15" ||
		view.SteamRestartPolicy != modRestartPolicyWhenEmpty ||
		view.SteamScheduledTime != "04:45" {
		t.Fatalf("handler did not persist settings: %+v", view)
	}
}

func TestAutomaticSteamUpdateCountsDownAndRequestsRestart(t *testing.T) {
	manager, statePath := testAutomaticUpdateManager(t)
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	var messages []string
	manager.automaticUpdateMessageSend = func(message string) error {
		messages = append(messages, message)
		return nil
	}
	requestedAction := ""
	manager.operationStart = func(
		action string,
		username string,
		clientIP string,
		peerIP string,
	) error {
		requestedAction = action
		return nil
	}

	next := manager.processAutomaticUpdates(now)
	if next.IsZero() {
		t.Fatal("automatic countdown did not schedule a next action")
	}
	if len(messages) != 1 ||
		!strings.Contains(messages[0], "restart in 10 minutes") ||
		!strings.Contains(messages[0], "to update the game server") {
		t.Fatalf("unexpected first notice: %q", messages)
	}
	for _, privateDetail := range []string{"17123456", "install"} {
		if strings.Contains(messages[0], privateDetail) {
			t.Fatalf("in-game notice exposed %q: %q", privateDetail, messages[0])
		}
	}
	var scheduled automaticUpdateStateFile
	if err := readJSON(statePath, &scheduled); err != nil {
		t.Fatal(err)
	}
	if scheduled.Schedule == nil || scheduled.Schedule.NextNotice != 1 {
		t.Fatalf("countdown state not persisted: %+v", scheduled)
	}

	for index, offset := range []time.Duration{
		5 * time.Minute,
		6 * time.Minute,
		7 * time.Minute,
		8 * time.Minute,
		9 * time.Minute,
	} {
		manager.processAutomaticUpdates(now.Add(offset))
		wantMinutes := 5 - index
		if len(messages) != index+2 ||
			!strings.Contains(
				messages[index+1],
				strconv.Itoa(wantMinutes)+" minute",
			) {
			t.Fatalf(
				"countdown notice %d = %q",
				wantMinutes,
				messages,
			)
		}
	}
	manager.processAutomaticUpdates(now.Add(modRestartDelay))
	if requestedAction != "restart" {
		t.Fatalf("automatic action = %q, want restart", requestedAction)
	}
	if len(messages) != 7 ||
		!strings.Contains(messages[6], "restarting now") {
		t.Fatalf("unexpected final notice: %q", messages)
	}
	var completed automaticUpdateStateFile
	if err := readJSON(statePath, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Schedule != nil ||
		completed.LastAttemptedSteamBuildID != "17123456" {
		t.Fatalf("accepted update not recorded: %+v", completed)
	}
	if targets := manager.automaticUpdateTargets(); targets.SteamBuildID != "" {
		t.Fatalf("same failed-or-pending build was rescheduled: %+v", targets)
	}
}

func TestAutomaticUpdatePlayerMessagesHideInternalTargets(t *testing.T) {
	tests := []struct {
		schedule automaticUpdateSchedule
		target   string
	}{
		{
			schedule: automaticUpdateSchedule{ManagerVersion: "9.8.7"},
			target:   "the server management tool",
		},
		{
			schedule: automaticUpdateSchedule{SteamBuildID: "17123456"},
			target:   "the game server",
		},
		{
			schedule: automaticUpdateSchedule{
				ManagerVersion: "9.8.7",
				SteamBuildID:   "17123456",
			},
			target: "the server management tool and game server",
		},
	}
	for _, test := range tests {
		messages := []string{
			automaticUpdateNoticeMessage(&test.schedule, 10),
			automaticUpdateFinalMessage(&test.schedule),
			automaticUpdateEmptyMessage(&test.schedule),
		}
		for _, message := range messages {
			for _, privateDetail := range []string{
				"MORDHAU Control",
				"MORDHAU Dedicated Server",
				"9.8.7",
				"17123456",
				"to install",
			} {
				if strings.Contains(message, privateDetail) {
					t.Fatalf(
						"player notice exposed %q: %q",
						privateDetail,
						message,
					)
				}
			}
			if !strings.Contains(message, test.target) {
				t.Fatalf("player notice target = %q, want %q", message, test.target)
			}
		}
	}
}

func TestLegacyAutomaticUpdatePlayerMessagesAreSanitizedForView(t *testing.T) {
	tests := map[string]string{
		"system: [SYSTEM UPDATE] The server will restart in 10 minutes to install MORDHAU Control v2.6.6.":                                                   "system: [SYSTEM UPDATE] The server will restart in 10 minutes to update the server management tool.",
		"[SYSTEM UPDATE] The server is restarting now to install MORDHAU Dedicated Server build 17123456.":                                                   "[SYSTEM UPDATE] The server is restarting now to update the game server.",
		"[SYSTEM UPDATE] MORDHAU Control v2.6.6 and MORDHAU Dedicated Server build 17123456 is ready. The server will restart as soon as no players remain.": "[SYSTEM UPDATE] An update for the server management tool and game server is ready. The server will restart as soon as no players remain.",
	}
	for input, want := range tests {
		if got := normalizeLegacyAutomaticUpdateNotice(input); got != want {
			t.Fatalf("normalized notice = %q, want %q", got, want)
		}
		if strings.Contains(want, "2.6.6") ||
			strings.Contains(want, "17123456") ||
			strings.Contains(want, "MORDHAU Control") {
			t.Fatalf("expected notice retained internal target: %q", want)
		}
	}

	unchanged := "system: [MOD UPDATE] The server will restart in 10 minutes."
	if got := normalizeLegacyAutomaticUpdateNotice(unchanged); got != unchanged {
		t.Fatalf("unrelated notice changed to %q", got)
	}
}

func TestRCONHistorySanitizesLegacyUpdateNoticeWithoutRewritingRawEvent(t *testing.T) {
	now := time.Now()
	raw := "system: [SYSTEM UPDATE] The server will restart in 10 minutes " +
		"to install MORDHAU Control v2.6.6."
	manager := &Manager{rconEvents: []RCONEvent{{
		Sequence: 1,
		Time:     now,
		Kind:     "outbound",
		Text:     raw,
	}}}
	events := manager.rconHistory(rconBrowserHistoryLimit)
	if len(events) != 1 ||
		events[0].Text != "system: [SYSTEM UPDATE] The server will restart in "+
			"10 minutes to update the server management tool." {
		t.Fatalf("sanitized history = %#v", events)
	}
	if manager.rconEvents[0].Text != raw {
		t.Fatal("view normalization rewrote append-only event history")
	}
}

func TestAutomaticSteamUpdateRunsImmediatelyWhenServerStopped(t *testing.T) {
	manager, _ := testAutomaticUpdateManager(t)
	manager.automaticUpdateProcess = func() (int, bool) { return 0, false }
	var messages []string
	manager.automaticUpdateMessageSend = func(message string) error {
		messages = append(messages, message)
		return nil
	}
	requestedAction := ""
	manager.operationStart = func(
		action string,
		username string,
		clientIP string,
		peerIP string,
	) error {
		requestedAction = action
		return nil
	}
	if next := manager.processAutomaticUpdates(time.Now()); !next.IsZero() {
		t.Fatalf("stopped-server update returned next action %v", next)
	}
	if requestedAction != "update" {
		t.Fatalf("stopped-server automatic action = %q", requestedAction)
	}
	if len(messages) != 0 {
		t.Fatalf("stopped-server update sent countdown notices: %q", messages)
	}
}

func TestAutomaticSteamUpdateWaitsForEmptyServer(t *testing.T) {
	manager, _ := testAutomaticUpdateManager(t)
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	if _, err := manager.setAutomaticUpdateSettings(
		false,
		true,
		modRestartPolicyCountdown,
		defaultModRestartTime,
		modRestartPolicyWhenEmpty,
		defaultModRestartTime,
	); err != nil {
		t.Fatal(err)
	}
	manager.runtimeSummary = RuntimeBridgeSummary{
		Ready:                 true,
		PlayerControllerCount: 1,
	}
	var messages []string
	manager.automaticUpdateMessageSend = func(message string) error {
		messages = append(messages, message)
		return nil
	}
	requestedAction := ""
	manager.operationStart = func(
		action string,
		_ string,
		_ string,
		_ string,
	) error {
		requestedAction = action
		return nil
	}

	manager.processAutomaticUpdates(now)
	if len(messages) != 1 ||
		!strings.Contains(messages[0], "as soon as no players remain") ||
		requestedAction != "" {
		t.Fatalf(
			"automatic update did not wait for players: messages=%q action=%q",
			messages,
			requestedAction,
		)
	}
	manager.runtimeMu.Lock()
	manager.runtimeSummary.PlayerControllerCount = 0
	manager.runtimeMu.Unlock()
	manager.processAutomaticUpdates(now.Add(automaticUpdateRetryDelay))
	if requestedAction != "" {
		t.Fatal("automatic update ignored the empty-server grace period")
	}
	manager.processAutomaticUpdates(
		now.Add(automaticUpdateRetryDelay + modRestartEmptyGrace),
	)
	if requestedAction != "restart" ||
		len(messages) != 2 ||
		!strings.Contains(messages[1], "restarting now") {
		t.Fatalf(
			"empty-server update was not executed: messages=%q action=%q",
			messages,
			requestedAction,
		)
	}
}

func TestAutomaticSteamUpdateUsesScheduledServerTime(t *testing.T) {
	manager, _ := testAutomaticUpdateManager(t)
	location := time.FixedZone("server", 9*60*60)
	now := time.Date(2026, 7, 30, 3, 30, 0, 0, location)
	if _, err := manager.setAutomaticUpdateSettings(
		false,
		true,
		modRestartPolicyCountdown,
		defaultModRestartTime,
		modRestartPolicyScheduled,
		"04:00",
	); err != nil {
		t.Fatal(err)
	}
	var messages []string
	manager.automaticUpdateMessageSend = func(message string) error {
		messages = append(messages, message)
		return nil
	}
	next := manager.processAutomaticUpdates(now)
	want := time.Date(2026, 7, 30, 3, 50, 0, 0, location)
	if !next.Equal(want) || len(messages) != 0 {
		t.Fatalf("scheduled update next=%s messages=%q, want %s", next, messages, want)
	}
	manager.processAutomaticUpdates(want)
	if len(messages) != 1 ||
		!strings.Contains(messages[0], "restart in 10 minutes") {
		t.Fatalf("scheduled update did not start its countdown: %q", messages)
	}
	view := manager.automaticUpdateView()
	if view.RestartPolicy != modRestartPolicyScheduled ||
		view.RestartAt == nil ||
		!view.RestartAt.Equal(time.Date(
			2026,
			7,
			30,
			4,
			0,
			0,
			0,
			location,
		)) {
		t.Fatalf("unexpected scheduled update view: %+v", view)
	}
}

func TestAutomaticScheduledUpdateSkipsMissedNoticeWindow(t *testing.T) {
	manager, _ := testAutomaticUpdateManager(t)
	location := time.FixedZone("server", 9*60*60)
	now := time.Date(2026, 7, 30, 3, 30, 0, 0, location)
	if _, err := manager.setAutomaticUpdateSettings(
		false,
		true,
		modRestartPolicyCountdown,
		defaultModRestartTime,
		modRestartPolicyScheduled,
		"04:00",
	); err != nil {
		t.Fatal(err)
	}
	requestedAction := ""
	manager.operationStart = func(
		action string,
		_ string,
		_ string,
		_ string,
	) error {
		requestedAction = action
		return nil
	}
	manager.processAutomaticUpdates(now)
	manager.processAutomaticUpdates(
		time.Date(2026, 7, 30, 4, 0, 0, 0, location),
	)
	view := manager.automaticUpdateView()
	want := time.Date(2026, 7, 31, 4, 0, 0, 0, location)
	if requestedAction != "" ||
		view.RestartAt == nil ||
		!view.RestartAt.Equal(want) {
		t.Fatalf(
			"missed update window action=%q view=%+v, want %s",
			requestedAction,
			view,
			want,
		)
	}
}

func TestAutomaticUpdateWaitsForActiveRecurringRestart(t *testing.T) {
	manager, _ := testAutomaticUpdateManager(t)
	now := time.Date(2026, 7, 30, 3, 50, 0, 0, time.UTC)
	manager.scheduledRestartState = scheduledServerRestartStateFile{
		Version:       scheduledServerRestartStateVersion,
		Enabled:       true,
		ScheduledTime: "04:00",
		Weekdays:      append([]string(nil), scheduledServerRestartWeekdays...),
		Schedule: &scheduledServerRestartSchedule{
			RestartAt: now.Add(modRestartDelay),
			ServerPID: 4321,
		},
	}
	next := manager.processAutomaticUpdates(now)
	if !next.Equal(now.Add(automaticUpdateRetryDelay)) {
		t.Fatalf("automatic update retry = %s", next)
	}
	if manager.automaticUpdateState.Schedule != nil {
		t.Fatal("automatic update claimed an active recurring restart window")
	}
}

func TestAutomaticManagerUpdateUsesDetachedUpdater(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	autoPath := filepath.Join(root, "automatic-updates.json")
	autoState := automaticUpdateStateFile{
		Version:              automaticUpdateStateVersion,
		ManagerEnabled:       true,
		ManagerRestartPolicy: modRestartPolicyCountdown,
		ManagerScheduledTime: defaultModRestartTime,
		SteamRestartPolicy:   modRestartPolicyCountdown,
		SteamScheduledTime:   defaultModRestartTime,
		Schedule: &automaticUpdateSchedule{
			DetectedAt:     now.Add(-modRestartDelay),
			RestartAt:      now,
			ServerPID:      777,
			ManagerVersion: "2.4.0",
			Policy:         modRestartPolicyCountdown,
			NextNotice:     len(modRestartNotices),
			FinalAnnounced: true,
		},
	}
	if err := writeJSONAtomic(autoPath, autoState, 0600); err != nil {
		t.Fatal(err)
	}
	managerStatePath := filepath.Join(root, "manager-update.json")
	managerState := initialManagerUpdateState()
	managerState.LatestVersion = "2.4.0"
	managerState.ReleaseURL, _ = managerReleaseURL("2.4.0")
	managerState.CheckedAt = now.Add(-time.Minute)
	if err := writeJSONAtomic(managerStatePath, managerState, 0600); err != nil {
		t.Fatal(err)
	}
	versionPath := filepath.Join(root, "manager-version")
	if err := os.WriteFile(versionPath, []byte("2.3.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	started := ""
	manager := &Manager{
		automaticUpdateState:     autoState,
		automaticUpdateStateFile: autoPath,
		automaticUpdateWake:      make(chan struct{}, 1),
		automaticUpdateProcess:   func() (int, bool) { return 777, true },
		managerUpdateStateFile:   managerStatePath,
		managerUpdateVersionFile: versionPath,
		managerUpdateNow:         func() time.Time { return now },
		managerUpdateWorkerStart: func(target string) error { started = target; return nil },
		auditPath:                filepath.Join(root, "audit.log"),
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	if next := manager.processAutomaticUpdates(now); !next.IsZero() {
		t.Fatalf("accepted manager update returned next action %v", next)
	}
	if started != "2.4.0" {
		t.Fatalf("detached manager target = %q", started)
	}
	persisted, err := readManagerUpdateState(managerStatePath)
	if err != nil || persisted.Status != "running" {
		t.Fatalf("manager update state = %+v, %v", persisted, err)
	}
	var automatic automaticUpdateStateFile
	if err := readJSON(autoPath, &automatic); err != nil {
		t.Fatal(err)
	}
	if automatic.Schedule != nil ||
		automatic.LastAttemptedManagerVersion != "2.4.0" {
		t.Fatalf("automatic manager attempt not recorded: %+v", automatic)
	}
}
