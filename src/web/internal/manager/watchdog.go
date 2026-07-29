package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	recoverySettingsVersion      = 1
	recoveryStateVersion         = 1
	defaultRecoveryMaxAttempts   = 3
	defaultRecoveryWindowMinutes = 30
	minimumRecoveryMaxAttempts   = 1
	maximumRecoveryMaxAttempts   = 10
	minimumRecoveryWindowMinutes = 5
	maximumRecoveryWindowMinutes = 1440
	recoverySampleInterval       = 2 * time.Second
	recoveryInitialDelay         = 15 * time.Second
	recoveryMaximumDelay         = 5 * time.Minute
	recoveryConsoleTailMaximum   = 16 << 10
	serverDesiredRunning         = "running"
	serverDesiredStopped         = "stopped"
)

type recoverySettingsFile struct {
	Version       int  `json:"version"`
	Enabled       bool `json:"enabled"`
	MaxAttempts   int  `json:"max_attempts"`
	WindowMinutes int  `json:"window_minutes"`
}

type serverLaunchStateFile struct {
	Version       int   `json:"version"`
	PID           int   `json:"pid"`
	StartedAtUnix int64 `json:"started_at_unix"`
}

type CrashDiagnostic struct {
	DetectedAt    time.Time `json:"detected_at"`
	PID           int       `json:"pid,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	UptimeSeconds int64     `json:"uptime_seconds,omitempty"`
	Cause         string    `json:"cause"`
	ConsoleTail   string    `json:"console_tail,omitempty"`
}

type recoveryStateFile struct {
	Version          int              `json:"version"`
	Attempts         []time.Time      `json:"attempts"`
	IncidentActive   bool             `json:"incident_active"`
	NextAttemptAt    time.Time        `json:"next_attempt_at,omitempty"`
	Exhausted        bool             `json:"exhausted"`
	LastCrash        *CrashDiagnostic `json:"last_crash,omitempty"`
	LastAttemptAt    time.Time        `json:"last_attempt_at,omitempty"`
	LastAttemptError string           `json:"last_attempt_error,omitempty"`
	LastRecoveredAt  time.Time        `json:"last_recovered_at,omitempty"`
}

type RecoverySettingsView struct {
	Enabled       bool `json:"enabled"`
	MaxAttempts   int  `json:"max_attempts"`
	WindowMinutes int  `json:"window_minutes"`
}

type RecoveryView struct {
	Settings             RecoverySettingsView `json:"settings"`
	DesiredState         string               `json:"desired_state"`
	IncidentActive       bool                 `json:"incident_active"`
	RestartScheduled     bool                 `json:"restart_scheduled"`
	NextAttemptAt        *time.Time           `json:"next_attempt_at,omitempty"`
	AttemptsInWindow     int                  `json:"attempts_in_window"`
	Exhausted            bool                 `json:"exhausted"`
	LastCrash            *CrashDiagnostic     `json:"last_crash,omitempty"`
	LastAttemptAt        *time.Time           `json:"last_attempt_at,omitempty"`
	LastAttemptError     string               `json:"last_attempt_error,omitempty"`
	LastRecoveredAt      *time.Time           `json:"last_recovered_at,omitempty"`
	CurrentUptimeSeconds int64                `json:"current_uptime_seconds,omitempty"`
}

func validRecoverySettings(settings recoverySettingsFile) bool {
	return settings.Version == recoverySettingsVersion &&
		settings.MaxAttempts >= minimumRecoveryMaxAttempts &&
		settings.MaxAttempts <= maximumRecoveryMaxAttempts &&
		settings.WindowMinutes >= minimumRecoveryWindowMinutes &&
		settings.WindowMinutes <= maximumRecoveryWindowMinutes
}

func validCrashDiagnostic(diagnostic *CrashDiagnostic) bool {
	if diagnostic == nil {
		return true
	}
	return !diagnostic.DetectedAt.IsZero() &&
		diagnostic.PID >= 0 &&
		diagnostic.UptimeSeconds >= 0 &&
		(diagnostic.Cause == "process_exited" ||
			diagnostic.Cause == "process_missing") &&
		utf8.ValidString(diagnostic.ConsoleTail) &&
		len(diagnostic.ConsoleTail) <= recoveryConsoleTailMaximum
}

func validRecoveryState(state recoveryStateFile) bool {
	if state.Version != recoveryStateVersion ||
		len(state.Attempts) > maximumRecoveryMaxAttempts*8 ||
		!validCrashDiagnostic(state.LastCrash) ||
		(!state.IncidentActive &&
			(!state.NextAttemptAt.IsZero() || state.Exhausted)) ||
		!utf8.ValidString(state.LastAttemptError) ||
		len(state.LastAttemptError) > 512 {
		return false
	}
	previous := time.Time{}
	for _, attempt := range state.Attempts {
		if attempt.IsZero() || (!previous.IsZero() && attempt.Before(previous)) {
			return false
		}
		previous = attempt
	}
	return true
}

func (m *Manager) recoverySettingsPath() string {
	if m.recoverySettingsFile != "" {
		return m.recoverySettingsFile
	}
	return recoverySettingsPath
}

func (m *Manager) recoveryStatePath() string {
	if m.recoveryStateFile != "" {
		return m.recoveryStateFile
	}
	return recoveryStatePath
}

func (m *Manager) recoveryDesiredPath() string {
	if m.recoveryDesiredStateFile != "" {
		return m.recoveryDesiredStateFile
	}
	return serverDesiredStatePath
}

func (m *Manager) recoveryLaunchPath() string {
	if m.recoveryLaunchStateFile != "" {
		return m.recoveryLaunchStateFile
	}
	return serverLaunchStatePath
}

func (m *Manager) recoveryConsolePath() string {
	if m.recoveryConsoleLogFile != "" {
		return m.recoveryConsoleLogFile
	}
	return serverConsoleLogPath
}

func (m *Manager) recoveryTime() time.Time {
	if m.recoveryNow != nil {
		return m.recoveryNow()
	}
	return time.Now()
}

func (m *Manager) recoveryProcess() (int, bool) {
	if m.recoveryServerProcess != nil {
		return m.recoveryServerProcess()
	}
	return serverProcess()
}

func readServerDesiredState(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	state := strings.TrimSpace(string(data))
	if state != serverDesiredRunning && state != serverDesiredStopped {
		return "", errors.New("invalid stored server desired state")
	}
	return state, nil
}

func writeServerDesiredState(path, state string) error {
	if state != serverDesiredRunning && state != serverDesiredStopped {
		return errors.New("invalid server desired state")
	}
	return writeFileAtomic(path, []byte(state+"\n"), 0600)
}

func pruneRecoveryAttempts(
	attempts []time.Time,
	now time.Time,
	window time.Duration,
) []time.Time {
	cutoff := now.Add(-window)
	start := 0
	for start < len(attempts) && attempts[start].Before(cutoff) {
		start++
	}
	return append([]time.Time(nil), attempts[start:]...)
}

func recoveryDelay(attempts int) time.Duration {
	delay := recoveryInitialDelay
	for index := 0; index < attempts; index++ {
		if delay >= recoveryMaximumDelay/2 {
			return recoveryMaximumDelay
		}
		delay *= 2
	}
	if delay > recoveryMaximumDelay {
		return recoveryMaximumDelay
	}
	return delay
}

func cloneCrashDiagnostic(value *CrashDiagnostic) *CrashDiagnostic {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func tailTextFile(path string, maximum int) string {
	if maximum < 1 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	start := int64(0)
	if info.Size() > int64(maximum) {
		start = info.Size() - int64(maximum)
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maximum)))
	if err != nil {
		return ""
	}
	if start > 0 {
		if newline := strings.IndexByte(string(data), '\n'); newline >= 0 {
			data = data[newline+1:]
		}
	}
	return strings.TrimSpace(strings.ToValidUTF8(string(data), "\uFFFD"))
}

func readServerLaunchState(path string) (serverLaunchStateFile, bool) {
	var state serverLaunchStateFile
	if err := readJSON(path, &state); err != nil ||
		state.Version != 1 ||
		state.PID < 2 ||
		state.StartedAtUnix < 1 {
		return serverLaunchStateFile{}, false
	}
	return state, true
}

func (m *Manager) buildCrashDiagnostic(
	now time.Time,
	pid int,
	cause string,
) CrashDiagnostic {
	diagnostic := CrashDiagnostic{
		DetectedAt:  now,
		PID:         pid,
		Cause:       cause,
		ConsoleTail: tailTextFile(m.recoveryConsolePath(), recoveryConsoleTailMaximum),
	}
	if launch, ok := readServerLaunchState(m.recoveryLaunchPath()); ok &&
		(pid == 0 || launch.PID == pid) {
		diagnostic.PID = launch.PID
		diagnostic.StartedAt = time.Unix(launch.StartedAtUnix, 0)
		if now.After(diagnostic.StartedAt) {
			diagnostic.UptimeSeconds = int64(now.Sub(diagnostic.StartedAt) / time.Second)
		}
	}
	return diagnostic
}

func (m *Manager) saveRecoveryStateLocked() error {
	m.recoveryState.Version = recoveryStateVersion
	if m.recoveryState.Attempts == nil {
		m.recoveryState.Attempts = []time.Time{}
	}
	return writeJSONAtomic(m.recoveryStatePath(), m.recoveryState, 0600)
}

func (m *Manager) loadOrCreateRecoveryState() error {
	settings := recoverySettingsFile{
		Version:       recoverySettingsVersion,
		Enabled:       true,
		MaxAttempts:   defaultRecoveryMaxAttempts,
		WindowMinutes: defaultRecoveryWindowMinutes,
	}
	if err := readJSON(m.recoverySettingsPath(), &settings); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load recovery settings: %w", err)
		}
		if err := writeJSONAtomic(m.recoverySettingsPath(), settings, 0600); err != nil {
			return fmt.Errorf("create recovery settings: %w", err)
		}
	} else if !validRecoverySettings(settings) {
		return errors.New("stored recovery settings are invalid")
	} else if err := os.Chmod(m.recoverySettingsPath(), 0600); err != nil {
		return err
	}

	state := recoveryStateFile{
		Version:  recoveryStateVersion,
		Attempts: []time.Time{},
	}
	created := false
	if err := readJSON(m.recoveryStatePath(), &state); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load recovery state: %w", err)
		}
		created = true
	} else if !validRecoveryState(state) {
		return errors.New("stored recovery state is invalid")
	} else if err := os.Chmod(m.recoveryStatePath(), 0600); err != nil {
		return err
	}

	pid, running := m.recoveryProcess()
	if _, err := readServerDesiredState(m.recoveryDesiredPath()); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		desired := serverDesiredStopped
		if running {
			desired = serverDesiredRunning
		}
		if err := writeServerDesiredState(m.recoveryDesiredPath(), desired); err != nil {
			return fmt.Errorf("initialize server desired state: %w", err)
		}
	}

	now := m.recoveryTime()
	state.Attempts = pruneRecoveryAttempts(
		state.Attempts,
		now,
		time.Duration(settings.WindowMinutes)*time.Minute,
	)
	if running {
		if state.IncidentActive {
			state.LastRecoveredAt = now
		}
		state.IncidentActive = false
		state.NextAttemptAt = time.Time{}
		state.Exhausted = false
		state.LastAttemptError = ""
		m.recoveryObservedRunning = true
		m.recoveryObservedPID = pid
	}

	m.recoverySettings = settings
	m.recoveryState = state
	if created || running {
		if err := m.saveRecoveryStateLocked(); err != nil {
			return fmt.Errorf("initialize recovery state: %w", err)
		}
	}
	return nil
}

func recoverySettingsView(settings recoverySettingsFile) RecoverySettingsView {
	return RecoverySettingsView{
		Enabled:       settings.Enabled,
		MaxAttempts:   settings.MaxAttempts,
		WindowMinutes: settings.WindowMinutes,
	}
}

func (m *Manager) recoveryView() RecoveryView {
	now := m.recoveryTime()
	desired, err := readServerDesiredState(m.recoveryDesiredPath())
	if err != nil {
		desired = "unknown"
	}
	m.recoveryMu.RLock()
	settings := m.recoverySettings
	state := m.recoveryState
	m.recoveryMu.RUnlock()
	state.Attempts = pruneRecoveryAttempts(
		state.Attempts,
		now,
		time.Duration(settings.WindowMinutes)*time.Minute,
	)
	view := RecoveryView{
		Settings:         recoverySettingsView(settings),
		DesiredState:     desired,
		IncidentActive:   state.IncidentActive,
		AttemptsInWindow: len(state.Attempts),
		Exhausted:        state.Exhausted,
		LastCrash:        cloneCrashDiagnostic(state.LastCrash),
		LastAttemptError: state.LastAttemptError,
	}
	if state.IncidentActive && !state.NextAttemptAt.IsZero() {
		view.RestartScheduled = true
		view.NextAttemptAt = timeView(state.NextAttemptAt)
	}
	view.LastAttemptAt = timeView(state.LastAttemptAt)
	view.LastRecoveredAt = timeView(state.LastRecoveredAt)
	if _, running := m.recoveryProcess(); running {
		if launch, ok := readServerLaunchState(m.recoveryLaunchPath()); ok {
			started := time.Unix(launch.StartedAtUnix, 0)
			if now.After(started) {
				view.CurrentUptimeSeconds = int64(now.Sub(started) / time.Second)
			}
		}
	}
	return view
}

func lifecycleOperationBusy() bool {
	lock, err := acquireLifecycleLock()
	if err != nil {
		return true
	}
	releaseLifecycleLock(lock)
	return false
}

func (m *Manager) recoveryLifecycleOperationBusy() bool {
	if m.recoveryLifecycleBusy != nil {
		return m.recoveryLifecycleBusy()
	}
	return lifecycleOperationBusy()
}

func (m *Manager) startRecoveryOperation() error {
	if m.recoveryOperationStart != nil {
		return m.recoveryOperationStart("recover", "system", "local", "local")
	}
	return m.startOperation("recover", "system", "local", "local")
}

func (m *Manager) observeRecovery(now time.Time) {
	pid, running := m.recoveryProcess()
	desired, desiredErr := readServerDesiredState(m.recoveryDesiredPath())

	m.recoveryMu.Lock()
	if running {
		changed := !m.recoveryObservedRunning ||
			m.recoveryObservedPID != pid ||
			m.recoveryState.IncidentActive ||
			m.recoveryState.Exhausted ||
			!m.recoveryState.NextAttemptAt.IsZero()
		recovered := m.recoveryState.IncidentActive
		m.recoveryObservedRunning = true
		m.recoveryObservedPID = pid
		if recovered {
			m.recoveryState.LastRecoveredAt = now
		}
		m.recoveryState.IncidentActive = false
		m.recoveryState.NextAttemptAt = time.Time{}
		m.recoveryState.Exhausted = false
		m.recoveryState.LastAttemptError = ""
		m.recoveryState.Attempts = pruneRecoveryAttempts(
			m.recoveryState.Attempts,
			now,
			time.Duration(m.recoverySettings.WindowMinutes)*time.Minute,
		)
		if changed {
			_ = m.saveRecoveryStateLocked()
		}
		m.recoveryMu.Unlock()
		if recovered {
			m.addRCONEvent("system", "Automatic crash recovery succeeded.")
			m.auditActorEvent("system", "local", "server_crash_recovery_succeeded",
				map[string]string{"pid": strconv.Itoa(pid)})
		}
		return
	}

	if desiredErr != nil || desired != serverDesiredRunning {
		changed := m.recoveryState.IncidentActive ||
			m.recoveryState.Exhausted ||
			!m.recoveryState.NextAttemptAt.IsZero() ||
			len(m.recoveryState.Attempts) > 0
		m.recoveryObservedRunning = false
		m.recoveryObservedPID = 0
		m.recoveryState.IncidentActive = false
		m.recoveryState.NextAttemptAt = time.Time{}
		m.recoveryState.Exhausted = false
		m.recoveryState.Attempts = []time.Time{}
		m.recoveryState.LastAttemptError = ""
		if changed {
			_ = m.saveRecoveryStateLocked()
		}
		m.recoveryMu.Unlock()
		return
	}

	if m.operationRunning() || m.recoveryLifecycleOperationBusy() {
		m.recoveryMu.Unlock()
		return
	}

	if !m.recoveryState.IncidentActive {
		cause := "process_missing"
		if m.recoveryObservedRunning || m.recoveryObservedPID > 0 {
			cause = "process_exited"
		}
		diagnostic := m.buildCrashDiagnostic(
			now,
			m.recoveryObservedPID,
			cause,
		)
		m.recoveryState.LastCrash = &diagnostic
		m.recoveryState.IncidentActive = true
		m.recoveryState.NextAttemptAt = time.Time{}
		m.recoveryState.Exhausted = false
		m.recoveryState.LastAttemptError = ""
		m.recoveryObservedRunning = false
		m.recoveryObservedPID = 0
		_ = m.saveRecoveryStateLocked()
		m.recoveryMu.Unlock()
		m.addRCONEvent("system", "MORDHAU server exited unexpectedly; automatic recovery is evaluating the incident.")
		m.auditActorEvent("system", "local", "server_crash_detected",
			map[string]string{
				"pid":            strconv.Itoa(diagnostic.PID),
				"uptime_seconds": strconv.FormatInt(diagnostic.UptimeSeconds, 10),
				"cause":          diagnostic.Cause,
			})
		m.queueMonitoringAlert("server_crash", map[string]string{
			"pid":            strconv.Itoa(diagnostic.PID),
			"uptime_seconds": strconv.FormatInt(diagnostic.UptimeSeconds, 10),
			"cause":          diagnostic.Cause,
		})
		m.recoveryMu.Lock()
	}

	m.recoveryState.Attempts = pruneRecoveryAttempts(
		m.recoveryState.Attempts,
		now,
		time.Duration(m.recoverySettings.WindowMinutes)*time.Minute,
	)
	if !m.recoverySettings.Enabled {
		m.recoveryState.NextAttemptAt = time.Time{}
		_ = m.saveRecoveryStateLocked()
		m.recoveryMu.Unlock()
		return
	}
	if len(m.recoveryState.Attempts) >= m.recoverySettings.MaxAttempts {
		newlyExhausted := !m.recoveryState.Exhausted
		attempts := len(m.recoveryState.Attempts)
		m.recoveryState.Exhausted = true
		m.recoveryState.NextAttemptAt = time.Time{}
		_ = m.saveRecoveryStateLocked()
		m.recoveryMu.Unlock()
		if newlyExhausted {
			m.addRCONEvent("system", "Automatic crash recovery stopped after reaching its retry limit.")
			m.auditActorEvent("system", "local", "server_crash_recovery_exhausted",
				map[string]string{
					"attempts": strconv.Itoa(attempts),
				})
			m.queueMonitoringAlert("recovery_exhausted", map[string]string{
				"attempts": strconv.Itoa(attempts),
			})
		}
		return
	}
	if m.recoveryState.NextAttemptAt.IsZero() {
		m.recoveryState.NextAttemptAt = now.Add(
			recoveryDelay(len(m.recoveryState.Attempts)),
		)
		_ = m.saveRecoveryStateLocked()
		m.recoveryMu.Unlock()
		return
	}
	if now.Before(m.recoveryState.NextAttemptAt) {
		m.recoveryMu.Unlock()
		return
	}

	m.recoveryState.Attempts = append(m.recoveryState.Attempts, now)
	m.recoveryState.LastAttemptAt = now
	m.recoveryState.LastAttemptError = ""
	m.recoveryState.NextAttemptAt = time.Time{}
	attempt := len(m.recoveryState.Attempts)
	_ = m.saveRecoveryStateLocked()
	m.recoveryMu.Unlock()

	m.auditActorEvent("system", "local", "server_crash_recovery_requested",
		map[string]string{"attempt": strconv.Itoa(attempt)})
	if err := m.startRecoveryOperation(); err != nil {
		m.recoveryMu.Lock()
		m.recoveryState.LastAttemptError = safeAuditText(err.Error(), 512)
		_ = m.saveRecoveryStateLocked()
		m.recoveryMu.Unlock()
		m.auditActorEvent("system", "local", "server_crash_recovery_request_failed",
			map[string]string{
				"attempt": strconv.Itoa(attempt),
				"reason":  safeAuditText(err.Error(), 160),
			})
	}
}

func (m *Manager) recoveryLoop(ctx context.Context) {
	m.observeRecovery(m.recoveryTime())
	ticker := time.NewTicker(recoverySampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.observeRecovery(m.recoveryTime())
		}
	}
}

func (m *Manager) setRecoverySettings(
	enabled *bool,
	maxAttempts *int,
	windowMinutes *int,
) (RecoveryView, error) {
	if enabled == nil && maxAttempts == nil && windowMinutes == nil {
		return RecoveryView{}, errors.New("no recovery setting was supplied")
	}
	m.recoveryMu.Lock()
	settings := m.recoverySettings
	if enabled != nil {
		settings.Enabled = *enabled
	}
	if maxAttempts != nil {
		settings.MaxAttempts = *maxAttempts
	}
	if windowMinutes != nil {
		settings.WindowMinutes = *windowMinutes
	}
	settings.Version = recoverySettingsVersion
	if !validRecoverySettings(settings) {
		m.recoveryMu.Unlock()
		return RecoveryView{}, fmt.Errorf(
			"recovery attempts must be %d-%d and the window must be %d-%d minutes",
			minimumRecoveryMaxAttempts,
			maximumRecoveryMaxAttempts,
			minimumRecoveryWindowMinutes,
			maximumRecoveryWindowMinutes,
		)
	}
	if err := writeJSONAtomic(m.recoverySettingsPath(), settings, 0600); err != nil {
		m.recoveryMu.Unlock()
		return RecoveryView{}, err
	}
	m.recoverySettings = settings
	now := m.recoveryTime()
	m.recoveryState.Attempts = pruneRecoveryAttempts(
		m.recoveryState.Attempts,
		now,
		time.Duration(settings.WindowMinutes)*time.Minute,
	)
	if !settings.Enabled {
		m.recoveryState.NextAttemptAt = time.Time{}
		m.recoveryState.Exhausted = false
	} else if m.recoveryState.IncidentActive {
		m.recoveryState.Exhausted = false
		m.recoveryState.NextAttemptAt = now
	}
	if err := m.saveRecoveryStateLocked(); err != nil {
		m.recoveryMu.Unlock()
		return RecoveryView{}, err
	}
	m.recoveryMu.Unlock()
	return m.recoveryView(), nil
}

func (m *Manager) retryRecoveryNow() (RecoveryView, error) {
	if _, running := m.recoveryProcess(); running {
		return RecoveryView{}, errors.New("the dedicated server is already running")
	}
	desired, err := readServerDesiredState(m.recoveryDesiredPath())
	if err != nil || desired != serverDesiredRunning {
		return RecoveryView{}, errors.New(
			"automatic recovery is not armed; use Start for an intentionally stopped server",
		)
	}
	m.recoveryMu.Lock()
	if !m.recoverySettings.Enabled {
		m.recoveryMu.Unlock()
		return RecoveryView{}, errors.New("automatic recovery is disabled")
	}
	now := m.recoveryTime()
	if !m.recoveryState.IncidentActive {
		diagnostic := m.buildCrashDiagnostic(now, 0, "process_missing")
		m.recoveryState.LastCrash = &diagnostic
		m.recoveryState.IncidentActive = true
	}
	m.recoveryState.Attempts = []time.Time{}
	m.recoveryState.Exhausted = false
	m.recoveryState.LastAttemptError = ""
	m.recoveryState.NextAttemptAt = now
	if err := m.saveRecoveryStateLocked(); err != nil {
		m.recoveryMu.Unlock()
		return RecoveryView{}, err
	}
	m.recoveryMu.Unlock()
	return m.recoveryView(), nil
}
