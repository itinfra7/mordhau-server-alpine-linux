package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	defaultModRefreshMinutes  = 60
	minimumModRefreshMinutes  = 1
	maximumModRefreshMinutes  = 10080
	modRefreshSettingsVersion = 1
	maximumModRefreshRetry    = 5 * time.Minute
)

type modRefreshSettingsFile struct {
	Version         int `json:"version"`
	IntervalMinutes int `json:"interval_minutes"`
}

type ModRefreshView struct {
	IntervalMinutes int        `json:"interval_minutes"`
	Refreshing      bool       `json:"refreshing"`
	LastAttemptAt   *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty"`
	NextRefreshAt   *time.Time `json:"next_refresh_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
}

func validModRefreshMinutes(minutes int) bool {
	return minutes >= minimumModRefreshMinutes && minutes <= maximumModRefreshMinutes
}

func (m *Manager) loadOrCreateModRefreshSettings() error {
	settings := modRefreshSettingsFile{
		Version:         modRefreshSettingsVersion,
		IntervalMinutes: defaultModRefreshMinutes,
	}
	if err := readJSON(modRefreshSettingsPath, &settings); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load mod refresh settings: %w", err)
		}
		if err := writeJSONAtomic(modRefreshSettingsPath, settings, 0600); err != nil {
			return fmt.Errorf("create mod refresh settings: %w", err)
		}
	} else {
		if settings.Version != modRefreshSettingsVersion ||
			!validModRefreshMinutes(settings.IntervalMinutes) {
			return errors.New("stored mod refresh settings are invalid")
		}
		if err := os.Chmod(modRefreshSettingsPath, 0600); err != nil {
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
	return ModRefreshView{
		IntervalMinutes: m.modRefreshSettings.IntervalMinutes,
		Refreshing:      m.modRefreshing,
		LastAttemptAt:   timeView(m.modLastAttempt),
		LastSuccessAt:   timeView(m.modLastSuccess),
		NextRefreshAt:   timeView(m.modNextRefresh),
		LastError:       m.modLastError,
	}
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

func (m *Manager) setModRefreshInterval(minutes int) (ModManagementView, error) {
	if !validModRefreshMinutes(minutes) {
		return ModManagementView{}, fmt.Errorf(
			"refresh interval must be between %d and %d whole minutes",
			minimumModRefreshMinutes,
			maximumModRefreshMinutes,
		)
	}

	m.modRefreshSettingsMu.Lock()
	defer m.modRefreshSettingsMu.Unlock()
	settings := modRefreshSettingsFile{
		Version:         modRefreshSettingsVersion,
		IntervalMinutes: minutes,
	}
	if err := writeJSONAtomic(modRefreshSettingsPath, settings, 0600); err != nil {
		return ModManagementView{}, err
	}

	now := time.Now()
	m.modsMu.Lock()
	m.modRefreshSettings = settings
	m.modNextRefresh = nextRefreshForInterval(m.modLastSuccess, now, minutes)
	m.modRevision++
	view := m.cachedModViewLocked()
	ready := m.modCacheReady
	m.modsMu.Unlock()
	m.signalModRefreshLoop()

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
