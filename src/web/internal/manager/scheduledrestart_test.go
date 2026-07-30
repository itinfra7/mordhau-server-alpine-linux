package manager

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestScheduledServerRestartDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduled-restart.json")
	manager := &Manager{
		scheduledRestartStateFile: path,
		scheduledRestartWake:      make(chan struct{}, 1),
		scheduledRestartServerProcess: func() (int, bool) {
			return 0, false
		},
	}
	if err := manager.loadOrCreateScheduledServerRestartState(); err != nil {
		t.Fatal(err)
	}
	view := manager.scheduledServerRestartView()
	if view.Enabled || view.Scheduled ||
		view.ScheduledTime != defaultModRestartTime ||
		len(view.Weekdays) != 7 {
		t.Fatalf("unexpected scheduled restart defaults: %+v", view)
	}
	view, err := manager.setScheduledServerRestartSettings(
		true,
		"05:30",
		[]string{"fri", "mon", "wed"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Enabled ||
		view.ScheduledTime != "05:30" ||
		strings.Join(view.Weekdays, ",") != "mon,wed,fri" {
		t.Fatalf("scheduled restart settings were not normalized: %+v", view)
	}
	var stored scheduledServerRestartStateFile
	if err := readJSON(path, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled ||
		stored.ScheduledTime != "05:30" ||
		strings.Join(stored.Weekdays, ",") != "mon,wed,fri" {
		t.Fatalf("scheduled restart settings were not persisted: %+v", stored)
	}
}

func TestNextScheduledServerRestartHonorsWeekdaysAndFullCountdown(
	t *testing.T,
) {
	location := time.FixedZone("server", 9*60*60)
	monday := time.Date(2026, 8, 3, 3, 30, 0, 0, location)
	got := nextScheduledServerRestart(
		monday,
		"04:00",
		[]string{"mon", "wed"},
	)
	want := time.Date(2026, 8, 3, 4, 0, 0, 0, location)
	if !got.Equal(want) {
		t.Fatalf("same-day restart = %s, want %s", got, want)
	}
	got = nextScheduledServerRestart(
		time.Date(2026, 8, 3, 3, 55, 0, 0, location),
		"04:00",
		[]string{"mon", "wed"},
	)
	want = time.Date(2026, 8, 5, 4, 0, 0, 0, location)
	if !got.Equal(want) {
		t.Fatalf("next weekday restart = %s, want %s", got, want)
	}
}

func TestScheduledServerRestartHandlerRequiresCSRF(t *testing.T) {
	root := t.TempDir()
	manager := &Manager{
		scheduledRestartStateFile: filepath.Join(
			root,
			"scheduled-restart.json",
		),
		scheduledRestartWake: make(chan struct{}, 1),
		scheduledRestartServerProcess: func() (int, bool) {
			return 0, false
		},
		auditPath: filepath.Join(root, "audit.log"),
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	if err := manager.loadOrCreateScheduledServerRestartState(); err != nil {
		t.Fatal(err)
	}
	session := Session{Username: "operator", CSRF: "csrf-token"}
	body := `{"enabled":true,"scheduled_time":"04:00","weekdays":["mon"]}`

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"http://manager.example/api/server/restart-schedule",
		strings.NewReader(body),
	)
	manager.scheduledServerRestartSettingsHandler(response, request, session)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing-CSRF status = %d", response.Code)
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodPost,
		"http://manager.example/api/server/restart-schedule",
		strings.NewReader(body),
	)
	request.Header.Set("X-CSRF-Token", session.CSRF)
	manager.scheduledServerRestartSettingsHandler(response, request, session)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"settings status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	if !manager.scheduledServerRestartView().Enabled {
		t.Fatal("handler did not enable scheduled restarts")
	}
}

func TestScheduledServerRestartCountsDownAndRequestsRestart(t *testing.T) {
	root := t.TempDir()
	location := time.FixedZone("server", 9*60*60)
	now := time.Date(2026, 8, 3, 3, 50, 0, 0, location)
	state := scheduledServerRestartStateFile{
		Version:       scheduledServerRestartStateVersion,
		Enabled:       true,
		ScheduledTime: "04:00",
		Weekdays:      append([]string(nil), scheduledServerRestartWeekdays...),
		Schedule: &scheduledServerRestartSchedule{
			RestartAt: now.Add(modRestartDelay),
			ServerPID: 4321,
		},
	}
	statePath := filepath.Join(root, "scheduled-restart.json")
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
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
	var messages []string
	requestedAction := ""
	manager := &Manager{
		scheduledRestartState:     state,
		scheduledRestartStateFile: statePath,
		scheduledRestartWake:      make(chan struct{}, 1),
		scheduledRestartServerProcess: func() (int, bool) {
			return 4321, true
		},
		scheduledRestartMessageSend: func(message string) error {
			messages = append(messages, message)
			return nil
		},
		managerUpdateStateFile: managerUpdatePath,
		auditPath:              filepath.Join(root, "audit.log"),
		operationStart: func(
			action string,
			_ string,
			_ string,
			_ string,
		) error {
			requestedAction = action
			return nil
		},
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	manager.processScheduledServerRestart(now)
	if len(messages) != 1 ||
		!strings.Contains(messages[0], "restart in 10 minutes") {
		t.Fatalf("unexpected first scheduled restart notice: %q", messages)
	}
	for index, offset := range []time.Duration{
		5 * time.Minute,
		6 * time.Minute,
		7 * time.Minute,
		8 * time.Minute,
		9 * time.Minute,
	} {
		manager.processScheduledServerRestart(now.Add(offset))
		wantMinutes := 5 - index
		if len(messages) != index+2 ||
			!strings.Contains(
				messages[index+1],
				strconv.Itoa(wantMinutes)+" minute",
			) {
			t.Fatalf(
				"scheduled restart notice %d = %q",
				wantMinutes,
				messages,
			)
		}
	}
	manager.processScheduledServerRestart(now.Add(modRestartDelay))
	if requestedAction != "restart" ||
		len(messages) != 7 ||
		!strings.Contains(messages[6], "restarting now") {
		t.Fatalf(
			"scheduled restart was not requested: action=%q messages=%q",
			requestedAction,
			messages,
		)
	}
	view := manager.scheduledServerRestartView()
	if !view.Scheduled ||
		view.RestartAt == nil ||
		!view.RestartAt.After(now.Add(modRestartDelay)) {
		t.Fatalf("next scheduled restart was not retained: %+v", view)
	}
}

func TestScheduledServerRestartSkipsMissedNoticeWindow(t *testing.T) {
	root := t.TempDir()
	location := time.FixedZone("server", 9*60*60)
	restartAt := time.Date(2026, 8, 3, 4, 0, 0, 0, location)
	state := scheduledServerRestartStateFile{
		Version:       scheduledServerRestartStateVersion,
		Enabled:       true,
		ScheduledTime: "04:00",
		Weekdays:      append([]string(nil), scheduledServerRestartWeekdays...),
		Schedule: &scheduledServerRestartSchedule{
			RestartAt: restartAt,
			ServerPID: 4321,
		},
	}
	statePath := filepath.Join(root, "scheduled-restart.json")
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
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
	requestedAction := ""
	manager := &Manager{
		scheduledRestartState:     state,
		scheduledRestartStateFile: statePath,
		scheduledRestartServerProcess: func() (int, bool) {
			return 4321, true
		},
		managerUpdateStateFile: managerUpdatePath,
		auditPath:              filepath.Join(root, "audit.log"),
		operationStart: func(
			action string,
			_ string,
			_ string,
			_ string,
		) error {
			requestedAction = action
			return nil
		},
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	manager.processScheduledServerRestart(restartAt)
	view := manager.scheduledServerRestartView()
	want := restartAt.AddDate(0, 0, 1)
	if requestedAction != "" ||
		view.RestartAt == nil ||
		!view.RestartAt.Equal(want) {
		t.Fatalf(
			"missed restart action=%q view=%+v, want %s",
			requestedAction,
			view,
			want,
		)
	}
}

func TestScheduledServerRestartSkipsAutomaticUpdateWindow(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 3, 3, 50, 0, 0, time.UTC)
	state := scheduledServerRestartStateFile{
		Version:       scheduledServerRestartStateVersion,
		Enabled:       true,
		ScheduledTime: "04:00",
		Weekdays:      append([]string(nil), scheduledServerRestartWeekdays...),
		Schedule: &scheduledServerRestartSchedule{
			RestartAt: now.Add(modRestartDelay),
			ServerPID: 4321,
		},
	}
	statePath := filepath.Join(root, "scheduled-restart.json")
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
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
		scheduledRestartState:     state,
		scheduledRestartStateFile: statePath,
		scheduledRestartServerProcess: func() (int, bool) {
			return 4321, true
		},
		automaticUpdateState: automaticUpdateStateFile{
			Version:              automaticUpdateStateVersion,
			ManagerRestartPolicy: modRestartPolicyCountdown,
			ManagerScheduledTime: defaultModRestartTime,
			SteamRestartPolicy:   modRestartPolicyCountdown,
			SteamScheduledTime:   defaultModRestartTime,
			Schedule: &automaticUpdateSchedule{
				DetectedAt:   now,
				RestartAt:    now.Add(modRestartDelay),
				ServerPID:    4321,
				SteamBuildID: "17123456",
				Policy:       modRestartPolicyCountdown,
			},
		},
		managerUpdateStateFile: managerUpdatePath,
		auditPath:              filepath.Join(root, "audit.log"),
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	manager.processScheduledServerRestart(now)
	view := manager.scheduledServerRestartView()
	want := now.Add(24*time.Hour + modRestartDelay)
	if view.RestartAt == nil || !view.RestartAt.Equal(want) {
		t.Fatalf(
			"recurring restart did not yield to automatic update: %+v, want %s",
			view,
			want,
		)
	}
}
