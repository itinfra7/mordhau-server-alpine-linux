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
		Version:      automaticUpdateStateVersion,
		SteamEnabled: true,
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
	manager := &Manager{
		automaticUpdateState:     state,
		automaticUpdateStateFile: statePath,
		automaticUpdateWake:      make(chan struct{}, 1),
		steamUpdateStateFile:     steamStatePath,
		steamUpdateManifestFile:  manifestPath,
		auditPath:                filepath.Join(root, "audit.log"),
		automaticUpdateProcess:   func() (int, bool) { return 4321, true },
		automaticUpdateNow:       time.Now,
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	return manager, statePath
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
	view, err := manager.setAutomaticUpdateSettings(true, true)
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
	body := `{"manager_enabled":true,"steam_enabled":true}`

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
	if !view.ManagerEnabled || !view.SteamEnabled {
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
		!strings.Contains(messages[0], "build 17123456") {
		t.Fatalf("unexpected first notice: %q", messages)
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

func TestAutomaticManagerUpdateUsesDetachedUpdater(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	autoPath := filepath.Join(root, "automatic-updates.json")
	autoState := automaticUpdateStateFile{
		Version:        automaticUpdateStateVersion,
		ManagerEnabled: true,
		Schedule: &automaticUpdateSchedule{
			DetectedAt:     now.Add(-modRestartDelay),
			RestartAt:      now,
			ServerPID:      777,
			ManagerVersion: "2.4.0",
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
