package manager

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testRecoveryManager(t *testing.T) (*Manager, *time.Time, *bool, *int, *[]string) {
	t.Helper()
	root := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	running := false
	pid := 0
	actions := []string{}
	m := &Manager{
		operationPath:            filepath.Join(root, "operation.json"),
		auditPath:                filepath.Join(root, "audit.jsonl"),
		recoverySettingsFile:     filepath.Join(root, "recovery-settings.json"),
		recoveryStateFile:        filepath.Join(root, "recovery-state.json"),
		recoveryDesiredStateFile: filepath.Join(root, "desired-state"),
		recoveryLaunchStateFile:  filepath.Join(root, "launch.json"),
		recoveryConsoleLogFile:   filepath.Join(root, "console.log"),
		recoveryServerProcess: func() (int, bool) {
			return pid, running
		},
		recoveryLifecycleBusy: func() bool { return false },
		recoveryNow:           func() time.Time { return now },
		recoveryOperationStart: func(action, username, clientIP, peerIP string) error {
			actions = append(actions, action)
			return nil
		},
	}
	if err := m.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	if err := m.loadOrCreateOperationState(); err != nil {
		t.Fatal(err)
	}
	if err := m.loadOrCreateRecoveryState(); err != nil {
		t.Fatal(err)
	}
	return m, &now, &running, &pid, &actions
}

func TestRecoveryIgnoresIntentionalStop(t *testing.T) {
	m, now, _, _, actions := testRecoveryManager(t)
	if err := writeServerDesiredState(m.recoveryDesiredPath(), serverDesiredStopped); err != nil {
		t.Fatal(err)
	}
	m.observeRecovery(*now)
	view := m.recoveryView()
	if view.IncidentActive || view.RestartScheduled || len(*actions) != 0 {
		t.Fatalf("intentional stop created recovery activity: %+v, actions=%v", view, *actions)
	}
}

func TestRecoverySchedulesRetriesAndClearsAfterSuccess(t *testing.T) {
	m, now, running, pid, actions := testRecoveryManager(t)
	if err := writeServerDesiredState(m.recoveryDesiredPath(), serverDesiredRunning); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.recoveryConsolePath(), []byte("fatal test line\n"), 0600); err != nil {
		t.Fatal(err)
	}

	m.observeRecovery(*now)
	view := m.recoveryView()
	if !view.IncidentActive || !view.RestartScheduled || view.LastCrash == nil ||
		view.LastCrash.ConsoleTail != "fatal test line" {
		t.Fatalf("unexpected initial recovery view: %+v", view)
	}

	*now = (*now).Add(recoveryInitialDelay)
	m.observeRecovery(*now)
	if len(*actions) != 1 || (*actions)[0] != "recover" {
		t.Fatalf("recovery actions = %v, want one recover", *actions)
	}

	*running = true
	*pid = 4321
	m.observeRecovery(*now)
	view = m.recoveryView()
	if view.IncidentActive || view.Exhausted || view.RestartScheduled ||
		view.LastRecoveredAt == nil {
		t.Fatalf("recovery success did not clear incident: %+v", view)
	}
}

func TestRecoveryStopsAtConfiguredLimitAndManualRetryResetsWindow(t *testing.T) {
	m, now, _, _, actions := testRecoveryManager(t)
	if err := writeServerDesiredState(m.recoveryDesiredPath(), serverDesiredRunning); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("launcher unavailable")
	m.recoveryOperationStart = func(action, username, clientIP, peerIP string) error {
		*actions = append(*actions, action)
		return failure
	}
	maxAttempts := 2
	if _, err := m.setRecoverySettings(nil, &maxAttempts, nil); err != nil {
		t.Fatal(err)
	}

	m.observeRecovery(*now)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		*now = (*now).Add(recoveryDelay(attempt))
		m.observeRecovery(*now)
		if attempt+1 < maxAttempts {
			m.observeRecovery(*now)
		}
	}
	m.observeRecovery(*now)
	view := m.recoveryView()
	if !view.Exhausted || view.AttemptsInWindow != maxAttempts ||
		len(*actions) != maxAttempts {
		t.Fatalf("retry limit was not enforced: %+v, actions=%v", view, *actions)
	}

	if _, err := m.retryRecoveryNow(); err != nil {
		t.Fatal(err)
	}
	view = m.recoveryView()
	if view.Exhausted || view.AttemptsInWindow != 0 || !view.RestartScheduled {
		t.Fatalf("manual retry did not reset the window: %+v", view)
	}
}
