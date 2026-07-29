package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultModRefreshMinutes  = 5
	minimumModRefreshMinutes  = 1
	maximumModRefreshMinutes  = 10080
	modRefreshSettingsVersion = 3
	maximumModRefreshRetry    = 5 * time.Minute
	modRestartPolicyCountdown = "countdown"
	modRestartPolicyWhenEmpty = "when_empty"
	modRestartPolicyScheduled = "scheduled"
	defaultModRestartTime     = "04:00"
)

type modRefreshSettingsFile struct {
	Version         int    `json:"version"`
	IntervalMinutes int    `json:"interval_minutes"`
	RestartOnUpdate bool   `json:"restart_on_update"`
	RestartPolicy   string `json:"restart_policy"`
	ScheduledTime   string `json:"scheduled_time"`
}

type ModRefreshView struct {
	IntervalMinutes  int        `json:"interval_minutes"`
	RestartOnUpdate  bool       `json:"restart_on_update"`
	RestartPolicy    string     `json:"restart_policy"`
	ScheduledTime    string     `json:"scheduled_time"`
	RestartScheduled bool       `json:"restart_scheduled"`
	WaitingForEmpty  bool       `json:"waiting_for_empty"`
	RestartAt        *time.Time `json:"restart_at,omitempty"`
	RestartModIDs    []int      `json:"restart_mod_ids"`
	Refreshing       bool       `json:"refreshing"`
	LastAttemptAt    *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
	NextRefreshAt    *time.Time `json:"next_refresh_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
}

func validModRefreshMinutes(minutes int) bool {
	return minutes >= minimumModRefreshMinutes && minutes <= maximumModRefreshMinutes
}

func validModRestartPolicy(policy string) bool {
	switch policy {
	case modRestartPolicyCountdown, modRestartPolicyWhenEmpty, modRestartPolicyScheduled:
		return true
	default:
		return false
	}
}

func validModRestartTime(value string) bool {
	_, err := time.Parse("15:04", value)
	return err == nil
}

func (m *Manager) modRefreshSettingsFilePath() string {
	if m.modRefreshSettingsFile != "" {
		return m.modRefreshSettingsFile
	}
	return modRefreshSettingsPath
}

func (m *Manager) loadOrCreateModRefreshSettings() error {
	path := m.modRefreshSettingsFilePath()
	settings := modRefreshSettingsFile{
		Version:         modRefreshSettingsVersion,
		IntervalMinutes: defaultModRefreshMinutes,
		RestartPolicy:   modRestartPolicyCountdown,
		ScheduledTime:   defaultModRestartTime,
	}
	changed := false
	if err := readJSON(path, &settings); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load mod refresh settings: %w", err)
		}
		if err := writeJSONAtomic(path, settings, 0600); err != nil {
			return fmt.Errorf("create mod refresh settings: %w", err)
		}
	} else {
		if !validModRefreshMinutes(settings.IntervalMinutes) {
			return errors.New("stored mod refresh settings are invalid")
		}
		switch settings.Version {
		case 1:
			settings.RestartOnUpdate = false
			fallthrough
		case 2:
			settings.Version = modRefreshSettingsVersion
			settings.RestartPolicy = modRestartPolicyCountdown
			settings.ScheduledTime = defaultModRestartTime
			changed = true
		case modRefreshSettingsVersion:
		default:
			return errors.New("stored mod refresh settings are invalid")
		}
		if !validModRestartPolicy(settings.RestartPolicy) ||
			!validModRestartTime(settings.ScheduledTime) {
			return errors.New("stored mod refresh settings are invalid")
		}
		if settings.RestartOnUpdate {
			if _, err := loadModIOSettingsFile(m.modIOSettingsFilePath()); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("validate mod.io settings for automatic restart: %w", err)
				}
				settings.RestartOnUpdate = false
				changed = true
			}
		}
		if changed {
			if err := writeJSONAtomic(path, settings, 0600); err != nil {
				return fmt.Errorf("migrate mod refresh settings: %w", err)
			}
		} else if err := os.Chmod(path, 0600); err != nil {
			return err
		}
	}
	m.modRefreshSettings = settings
	return nil
}

func timeView(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func (m *Manager) modRefreshViewLocked() ModRefreshView {
	restartPolicy := m.modRefreshSettings.RestartPolicy
	if !validModRestartPolicy(restartPolicy) {
		restartPolicy = modRestartPolicyCountdown
	}
	scheduledTime := m.modRefreshSettings.ScheduledTime
	if !validModRestartTime(scheduledTime) {
		scheduledTime = defaultModRestartTime
	}
	view := ModRefreshView{
		IntervalMinutes: m.modRefreshSettings.IntervalMinutes,
		RestartOnUpdate: m.modRefreshSettings.RestartOnUpdate,
		RestartPolicy:   restartPolicy,
		ScheduledTime:   scheduledTime,
		RestartModIDs:   []int{},
		Refreshing:      m.modRefreshing,
		LastAttemptAt:   timeView(m.modLastAttempt),
		LastSuccessAt:   timeView(m.modLastSuccess),
		NextRefreshAt:   timeView(m.modNextRefresh),
		LastError:       m.modLastError,
	}
	if schedule := m.modUpdateState.Schedule; schedule != nil {
		view.RestartScheduled = true
		view.WaitingForEmpty = schedule.Policy == modRestartPolicyWhenEmpty
		view.RestartAt = timeView(schedule.RestartAt)
		for _, update := range schedule.Updates {
			view.RestartModIDs = append(view.RestartModIDs, update.ModID)
		}
	}
	return view
}

func (m *Manager) cachedModViewLocked() ModManagementView {
	view := m.modCache
	view.Refresh = m.modRefreshViewLocked()
	view.Revision = m.modRevision
	return view
}

func (m *Manager) cachedModManagementView() (ModManagementView, error) {
	m.modsMu.RLock()
	if m.modCacheReady {
		view := m.cachedModViewLocked()
		m.modsMu.RUnlock()
		view.ServerRunning = serverRunning()
		return view, nil
	}
	m.modsMu.RUnlock()
	return m.ensureModCache()
}

func modRefreshResultError(view ModManagementView) string {
	if view.APIError != "" {
		return view.APIError
	}
	for _, mod := range view.Mods {
		if mod.DependencyError != "" {
			return "one or more mod dependency lookups failed"
		}
	}
	return ""
}

func modRefreshRetryDelay(intervalMinutes int) time.Duration {
	delay := time.Duration(intervalMinutes) * time.Minute
	if delay > maximumModRefreshRetry {
		delay = maximumModRefreshRetry
	}
	if delay < time.Minute {
		return time.Minute
	}
	return delay
}

func (m *Manager) buildModManagementView() (ModManagementView, error) {
	if m.modManagementViewBuild != nil {
		return m.modManagementViewBuild()
	}
	return m.modManagementView()
}

func (m *Manager) ensureModCache() (ModManagementView, error) {
	return m.refreshModCacheInternal(false)
}

func (m *Manager) refreshModCache() (ModManagementView, error) {
	return m.refreshModCacheInternal(true)
}

func (m *Manager) refreshModCacheAfterConfigurationChange() (ModManagementView, error) {
	m.modsMu.RLock()
	refreshing := m.modRefreshing
	done := m.modRefreshDone
	m.modsMu.RUnlock()
	if refreshing {
		<-done
	}
	return m.refreshModCache()
}

func (m *Manager) refreshModCacheInternal(force bool) (ModManagementView, error) {
	m.modsMu.Lock()
	if !force && m.modCacheReady {
		view := m.cachedModViewLocked()
		m.modsMu.Unlock()
		view.ServerRunning = serverRunning()
		return view, nil
	}
	if m.modRefreshing {
		done := m.modRefreshDone
		m.modsMu.Unlock()
		<-done
		m.modsMu.RLock()
		view := m.cachedModViewLocked()
		ready := m.modCacheReady
		m.modsMu.RUnlock()
		if !ready {
			return ModManagementView{}, errors.New("mod metadata cache is unavailable")
		}
		view.ServerRunning = serverRunning()
		return view, nil
	}

	m.modRefreshing = true
	m.modLastAttempt = time.Now()
	m.modRefreshDone = make(chan struct{})
	done := m.modRefreshDone
	m.modsMu.Unlock()

	view, err := m.buildModManagementView()
	finished := time.Now()
	refreshError := ""
	if err != nil {
		refreshError = err.Error()
	} else {
		refreshError = modRefreshResultError(view)
	}

	m.modsMu.Lock()
	var detection modUpdateDetection
	if err == nil && refreshError == "" {
		var stateErr error
		detection, stateErr = m.recordSuccessfulModRefreshLocked(view, finished)
		if stateErr != nil {
			refreshError = stateErr.Error()
		}
	}
	if err == nil {
		m.modCache = view
		m.modCacheReady = true
	}
	if refreshError == "" {
		m.modLastSuccess = finished
		m.modLastError = ""
		m.modNextRefresh = finished.Add(
			time.Duration(m.modRefreshSettings.IntervalMinutes) * time.Minute,
		)
	} else {
		m.modLastError = refreshError
		m.modNextRefresh = finished.Add(
			modRefreshRetryDelay(m.modRefreshSettings.IntervalMinutes),
		)
	}
	m.modRefreshing = false
	m.modRevision++
	result := m.cachedModViewLocked()
	ready := m.modCacheReady
	close(done)
	m.modsMu.Unlock()

	m.signalModRefreshLoop()
	m.handleModUpdateDetection(detection)
	if refreshError != "" {
		m.queueMonitoringAlert("mod_refresh_failed", map[string]string{
			"error": safeAuditText(refreshError, 200),
		})
	}
	if err != nil {
		return result, err
	}
	if !ready {
		return ModManagementView{}, errors.New("mod metadata cache is unavailable")
	}
	result.ServerRunning = serverRunning()
	return result, nil
}

func nextRefreshForInterval(
	lastSuccess time.Time,
	now time.Time,
	intervalMinutes int,
) time.Time {
	if lastSuccess.IsZero() {
		return now
	}
	next := lastSuccess.Add(time.Duration(intervalMinutes) * time.Minute)
	if next.Before(now) {
		return now
	}
	return next
}

func (m *Manager) setModRefreshSettings(
	minutes *int,
	restartOnUpdate *bool,
) (ModManagementView, error) {
	return m.setModRefreshSettingsWithPolicy(minutes, restartOnUpdate, nil, nil)
}

func (m *Manager) setModRefreshSettingsWithPolicy(
	minutes *int,
	restartOnUpdate *bool,
	restartPolicy *string,
	scheduledTime *string,
) (ModManagementView, error) {
	if minutes == nil && restartOnUpdate == nil &&
		restartPolicy == nil && scheduledTime == nil {
		return ModManagementView{}, errors.New("no mod refresh setting was supplied")
	}
	if minutes != nil && !validModRefreshMinutes(*minutes) {
		return ModManagementView{}, fmt.Errorf(
			"refresh interval must be between %d and %d whole minutes",
			minimumModRefreshMinutes,
			maximumModRefreshMinutes,
		)
	}
	if restartPolicy != nil {
		normalized := strings.TrimSpace(*restartPolicy)
		if !validModRestartPolicy(normalized) {
			return ModManagementView{}, errors.New("invalid mod update restart policy")
		}
		restartPolicy = &normalized
	}
	if scheduledTime != nil {
		normalized := strings.TrimSpace(*scheduledTime)
		if !validModRestartTime(normalized) {
			return ModManagementView{}, errors.New("scheduled restart time must use HH:MM")
		}
		scheduledTime = &normalized
	}
	if restartOnUpdate != nil && *restartOnUpdate {
		settings, err := m.modIOSettings()
		if err != nil {
			return ModManagementView{}, err
		}
		if settings == nil {
			return ModManagementView{}, errors.New(
				"save a valid mod.io API key before enabling automatic restart",
			)
		}
	}

	m.modRefreshSettingsMu.Lock()
	defer m.modRefreshSettingsMu.Unlock()
	m.modsMu.Lock()
	settings := m.modRefreshSettings
	previousRestartPolicy := settings.RestartPolicy
	previousScheduledTime := settings.ScheduledTime
	if settings.IntervalMinutes == 0 {
		settings.IntervalMinutes = defaultModRefreshMinutes
	}
	if !validModRestartPolicy(settings.RestartPolicy) {
		settings.RestartPolicy = modRestartPolicyCountdown
	}
	if !validModRestartTime(settings.ScheduledTime) {
		settings.ScheduledTime = defaultModRestartTime
	}
	settings.Version = modRefreshSettingsVersion
	if minutes != nil {
		settings.IntervalMinutes = *minutes
	}
	if restartOnUpdate != nil {
		settings.RestartOnUpdate = *restartOnUpdate
	}
	if restartPolicy != nil {
		settings.RestartPolicy = *restartPolicy
	}
	if scheduledTime != nil {
		settings.ScheduledTime = *scheduledTime
	}
	if err := writeJSONAtomic(m.modRefreshSettingsFilePath(), settings, 0600); err != nil {
		m.modsMu.Unlock()
		return ModManagementView{}, err
	}

	now := time.Now()
	m.modRefreshSettings = settings
	m.modNextRefresh = nextRefreshForInterval(
		m.modLastSuccess,
		now,
		settings.IntervalMinutes,
	)
	cancelled := false
	cancelSaveFailed := false
	stateSavePhase := "setting_disabled"
	if !settings.RestartOnUpdate && m.modUpdateState.Schedule != nil {
		state := cloneModUpdateState(m.modUpdateState)
		state.Schedule = nil
		if err := m.saveModUpdateStateValue(state); err != nil {
			cancelSaveFailed = true
		} else {
			m.modUpdateState = state
			cancelled = true
		}
	} else if settings.RestartOnUpdate &&
		m.modUpdateState.Schedule != nil &&
		(settings.RestartPolicy != previousRestartPolicy ||
			settings.ScheduledTime != previousScheduledTime) {
		stateSavePhase = "restart_policy_changed"
		state := cloneModUpdateState(m.modUpdateState)
		configureModRestartSchedule(
			state.Schedule,
			settings,
			now,
		)
		if err := m.saveModUpdateStateValue(state); err != nil {
			cancelSaveFailed = true
		} else {
			m.modUpdateState = state
		}
	}
	m.modRevision++
	view := m.cachedModViewLocked()
	ready := m.modCacheReady
	m.modsMu.Unlock()
	m.signalModRefreshLoop()
	m.signalModRestartLoop()
	if cancelled {
		m.auditActorEvent("system", "local", "mod_update_restart_cancelled",
			map[string]string{"reason": "setting_disabled"})
	}
	if cancelSaveFailed {
		m.auditActorEvent("system", "local", "mod_update_state_save_failed",
			map[string]string{"phase": stateSavePhase})
	}

	if !ready {
		return m.refreshModCache()
	}
	view.ServerRunning = serverRunning()
	return view, nil
}

func (m *Manager) currentModRevision() uint64 {
	m.modsMu.RLock()
	defer m.modsMu.RUnlock()
	return m.modRevision
}

func (m *Manager) signalModRefreshLoop() {
	if m.modRefreshWake == nil {
		return
	}
	select {
	case m.modRefreshWake <- struct{}{}:
	default:
	}
}

func (m *Manager) nextModRefresh() time.Time {
	m.modsMu.RLock()
	defer m.modsMu.RUnlock()
	return m.modNextRefresh
}

func (m *Manager) modRefreshLoop(ctx context.Context) {
	_, _ = m.refreshModCache()
	for {
		next := m.nextModRefresh()
		delay := time.Until(next)
		if next.IsZero() || delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-m.modRefreshWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			_, _ = m.refreshModCache()
		}
	}
}
