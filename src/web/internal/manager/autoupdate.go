package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	automaticUpdateStatePath    = stateDir + "/automatic-updates.json"
	automaticUpdateStateVersion = 1
	automaticUpdateRetryDelay   = 15 * time.Second
)

type automaticUpdateSchedule struct {
	DetectedAt     time.Time `json:"detected_at"`
	RestartAt      time.Time `json:"restart_at"`
	ServerPID      int       `json:"server_pid"`
	ManagerVersion string    `json:"manager_version,omitempty"`
	SteamBuildID   string    `json:"steam_build_id,omitempty"`
	NextNotice     int       `json:"next_notice"`
	FinalAnnounced bool      `json:"final_announced"`
}

type automaticUpdateStateFile struct {
	Version                     int                      `json:"version"`
	ManagerEnabled              bool                     `json:"manager_enabled"`
	SteamEnabled                bool                     `json:"steam_enabled"`
	LastAttemptedManagerVersion string                   `json:"last_attempted_manager_version,omitempty"`
	LastAttemptedSteamBuildID   string                   `json:"last_attempted_steam_build_id,omitempty"`
	Schedule                    *automaticUpdateSchedule `json:"schedule,omitempty"`
}

type AutomaticUpdateView struct {
	ManagerEnabled bool       `json:"manager_enabled"`
	SteamEnabled   bool       `json:"steam_enabled"`
	Scheduled      bool       `json:"scheduled"`
	RestartAt      *time.Time `json:"restart_at,omitempty"`
	ManagerVersion string     `json:"manager_version,omitempty"`
	SteamBuildID   string     `json:"steam_build_id,omitempty"`
}

type automaticUpdateTargets struct {
	ManagerVersion string
	SteamBuildID   string
}

func cloneAutomaticUpdateSchedule(
	schedule *automaticUpdateSchedule,
) *automaticUpdateSchedule {
	if schedule == nil {
		return nil
	}
	copy := *schedule
	return &copy
}

func cloneAutomaticUpdateState(
	state automaticUpdateStateFile,
) automaticUpdateStateFile {
	state.Schedule = cloneAutomaticUpdateSchedule(state.Schedule)
	return state
}

func validAutomaticUpdateSchedule(schedule *automaticUpdateSchedule) bool {
	if schedule == nil {
		return true
	}
	if schedule.DetectedAt.IsZero() ||
		schedule.RestartAt.IsZero() ||
		!schedule.RestartAt.After(schedule.DetectedAt) ||
		schedule.ServerPID <= 1 ||
		schedule.NextNotice < 0 ||
		schedule.NextNotice > len(modRestartNotices) {
		return false
	}
	if schedule.ManagerVersion == "" && schedule.SteamBuildID == "" {
		return false
	}
	if schedule.ManagerVersion != "" {
		if _, err := parseSemanticVersion(schedule.ManagerVersion); err != nil {
			return false
		}
	}
	return schedule.SteamBuildID == "" ||
		validSteamBuildID(schedule.SteamBuildID)
}

func validAutomaticUpdateState(state automaticUpdateStateFile) bool {
	if state.Version != automaticUpdateStateVersion ||
		!validAutomaticUpdateSchedule(state.Schedule) {
		return false
	}
	if state.LastAttemptedManagerVersion != "" {
		if _, err := parseSemanticVersion(
			state.LastAttemptedManagerVersion,
		); err != nil {
			return false
		}
	}
	return state.LastAttemptedSteamBuildID == "" ||
		validSteamBuildID(state.LastAttemptedSteamBuildID)
}

func (m *Manager) automaticUpdateStatePath() string {
	if m.automaticUpdateStateFile != "" {
		return m.automaticUpdateStateFile
	}
	return automaticUpdateStatePath
}

func (m *Manager) automaticUpdateNowValue() time.Time {
	if m.automaticUpdateNow != nil {
		return m.automaticUpdateNow()
	}
	return time.Now()
}

func (m *Manager) saveAutomaticUpdateStateLocked(
	state automaticUpdateStateFile,
) error {
	return writeJSONAtomic(m.automaticUpdateStatePath(), state, 0600)
}

func (m *Manager) loadOrCreateAutomaticUpdateState() error {
	path := m.automaticUpdateStatePath()
	state := automaticUpdateStateFile{Version: automaticUpdateStateVersion}
	if err := readJSON(path, &state); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load automatic update settings: %w", err)
		}
		if err := writeJSONAtomic(path, state, 0600); err != nil {
			return fmt.Errorf("create automatic update settings: %w", err)
		}
	} else {
		if !validAutomaticUpdateState(state) {
			return errors.New("stored automatic update settings are invalid")
		}
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
	}
	m.automaticUpdateState = state
	return nil
}

func automaticUpdateViewFromState(
	state automaticUpdateStateFile,
) AutomaticUpdateView {
	view := AutomaticUpdateView{
		ManagerEnabled: state.ManagerEnabled,
		SteamEnabled:   state.SteamEnabled,
	}
	if state.Schedule != nil {
		view.Scheduled = true
		view.RestartAt = timeView(state.Schedule.RestartAt)
		view.ManagerVersion = state.Schedule.ManagerVersion
		view.SteamBuildID = state.Schedule.SteamBuildID
	}
	return view
}

func (m *Manager) automaticUpdateView() AutomaticUpdateView {
	m.automaticUpdateMu.RLock()
	view := automaticUpdateViewFromState(m.automaticUpdateState)
	m.automaticUpdateMu.RUnlock()
	return view
}

func (m *Manager) automaticUpdateScheduled() bool {
	m.automaticUpdateMu.RLock()
	scheduled := m.automaticUpdateState.Schedule != nil
	m.automaticUpdateMu.RUnlock()
	return scheduled
}

func (m *Manager) modUpdateRestartScheduled() bool {
	m.modsMu.RLock()
	scheduled := m.modUpdateState.Schedule != nil
	m.modsMu.RUnlock()
	return scheduled
}

func (m *Manager) setAutomaticUpdateSettings(
	managerEnabled bool,
	steamEnabled bool,
) (AutomaticUpdateView, error) {
	m.automaticUpdateMu.Lock()
	state := cloneAutomaticUpdateState(m.automaticUpdateState)
	if managerEnabled && !state.ManagerEnabled {
		state.LastAttemptedManagerVersion = ""
	}
	if steamEnabled && !state.SteamEnabled {
		state.LastAttemptedSteamBuildID = ""
	}
	state.ManagerEnabled = managerEnabled
	state.SteamEnabled = steamEnabled
	if state.Schedule != nil {
		if !managerEnabled {
			state.Schedule.ManagerVersion = ""
		}
		if !steamEnabled {
			state.Schedule.SteamBuildID = ""
		}
		if state.Schedule.ManagerVersion == "" &&
			state.Schedule.SteamBuildID == "" {
			state.Schedule = nil
		}
	}
	if err := m.saveAutomaticUpdateStateLocked(state); err != nil {
		m.automaticUpdateMu.Unlock()
		return AutomaticUpdateView{}, err
	}
	m.automaticUpdateState = state
	view := automaticUpdateViewFromState(state)
	m.automaticUpdateMu.Unlock()
	m.signalAutomaticUpdateLoop()
	return view, nil
}

func (m *Manager) automaticUpdateServerProcess() (int, bool) {
	if m.automaticUpdateProcess != nil {
		return m.automaticUpdateProcess()
	}
	return serverProcess()
}

func (m *Manager) automaticUpdateTargets() automaticUpdateTargets {
	m.automaticUpdateMu.RLock()
	state := cloneAutomaticUpdateState(m.automaticUpdateState)
	m.automaticUpdateMu.RUnlock()
	var targets automaticUpdateTargets
	if state.ManagerEnabled {
		if view, err := m.currentManagerUpdateView(); err == nil &&
			view.Available &&
			view.Status != "running" &&
			!(view.Status == "failed" &&
				view.TargetVersion == view.LatestVersion) &&
			state.LastAttemptedManagerVersion != view.LatestVersion {
			targets.ManagerVersion = view.LatestVersion
		}
	}
	if state.SteamEnabled {
		if view, err := m.currentSteamUpdateView(); err == nil &&
			view.Available &&
			state.LastAttemptedSteamBuildID != view.LatestBuildID {
			targets.SteamBuildID = view.LatestBuildID
		}
	}
	return targets
}

func automaticUpdateTargetsEmpty(targets automaticUpdateTargets) bool {
	return targets.ManagerVersion == "" && targets.SteamBuildID == ""
}

func sameAutomaticUpdateSchedule(
	left *automaticUpdateSchedule,
	right *automaticUpdateSchedule,
) bool {
	return left != nil &&
		right != nil &&
		left.DetectedAt.Equal(right.DetectedAt) &&
		left.RestartAt.Equal(right.RestartAt) &&
		left.ServerPID == right.ServerPID &&
		left.ManagerVersion == right.ManagerVersion &&
		left.SteamBuildID == right.SteamBuildID
}

func (m *Manager) clearAutomaticUpdateSchedule(
	expected *automaticUpdateSchedule,
) bool {
	m.automaticUpdateMu.Lock()
	if expected != nil &&
		!sameAutomaticUpdateSchedule(m.automaticUpdateState.Schedule, expected) {
		m.automaticUpdateMu.Unlock()
		return false
	}
	state := cloneAutomaticUpdateState(m.automaticUpdateState)
	state.Schedule = nil
	if err := m.saveAutomaticUpdateStateLocked(state); err != nil {
		m.automaticUpdateMu.Unlock()
		m.auditActorEvent(
			"system",
			"local",
			"automatic_update_state_save_failed",
			map[string]string{"phase": "clear_schedule"},
		)
		return false
	}
	m.automaticUpdateState = state
	m.automaticUpdateMu.Unlock()
	return true
}

func (m *Manager) ensureAutomaticUpdateSchedule(
	targets automaticUpdateTargets,
	now time.Time,
) (*automaticUpdateSchedule, bool) {
	pid, running := m.automaticUpdateServerProcess()
	m.automaticUpdateMu.Lock()
	state := cloneAutomaticUpdateState(m.automaticUpdateState)
	if automaticUpdateTargetsEmpty(targets) || !running {
		state.Schedule = nil
	} else if state.Schedule == nil || state.Schedule.ServerPID != pid {
		state.Schedule = &automaticUpdateSchedule{
			DetectedAt:     now,
			RestartAt:      now.Add(modRestartDelay),
			ServerPID:      pid,
			ManagerVersion: targets.ManagerVersion,
			SteamBuildID:   targets.SteamBuildID,
		}
	} else {
		state.Schedule.ManagerVersion = targets.ManagerVersion
		state.Schedule.SteamBuildID = targets.SteamBuildID
	}
	changed := false
	switch {
	case m.automaticUpdateState.Schedule == nil && state.Schedule != nil:
		changed = true
	case m.automaticUpdateState.Schedule != nil && state.Schedule == nil:
		changed = true
	case m.automaticUpdateState.Schedule != nil && state.Schedule != nil &&
		!sameAutomaticUpdateSchedule(
			m.automaticUpdateState.Schedule,
			state.Schedule,
		):
		changed = true
	}
	if changed {
		if err := m.saveAutomaticUpdateStateLocked(state); err != nil {
			m.automaticUpdateMu.Unlock()
			m.auditActorEvent(
				"system",
				"local",
				"automatic_update_state_save_failed",
				map[string]string{"phase": "schedule"},
			)
			return nil, running
		}
		m.automaticUpdateState = state
	}
	schedule := cloneAutomaticUpdateSchedule(state.Schedule)
	m.automaticUpdateMu.Unlock()
	if changed && schedule != nil {
		m.auditActorEvent(
			"system",
			"local",
			"automatic_update_scheduled",
			automaticUpdateAuditDetails(schedule),
		)
	}
	return schedule, running
}

func automaticUpdateAuditDetails(
	schedule *automaticUpdateSchedule,
) map[string]string {
	details := make(map[string]string)
	if schedule == nil {
		return details
	}
	if schedule.ManagerVersion != "" {
		details["manager_version"] = schedule.ManagerVersion
	}
	if schedule.SteamBuildID != "" {
		details["steam_build_id"] = schedule.SteamBuildID
	}
	if !schedule.RestartAt.IsZero() {
		details["restart_at"] = schedule.RestartAt.Format(time.RFC3339)
	}
	return details
}

func automaticUpdateDescription(schedule *automaticUpdateSchedule) string {
	var items []string
	if schedule.ManagerVersion != "" {
		items = append(items, "MORDHAU Control v"+schedule.ManagerVersion)
	}
	if schedule.SteamBuildID != "" {
		items = append(
			items,
			"MORDHAU Dedicated Server build "+schedule.SteamBuildID,
		)
	}
	return strings.Join(items, " and ")
}

func automaticUpdateNoticeMessage(
	schedule *automaticUpdateSchedule,
	minutes int,
) string {
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	return fmt.Sprintf(
		"[SYSTEM UPDATE] The server will restart in %d %s to install %s.",
		minutes,
		unit,
		automaticUpdateDescription(schedule),
	)
}

func automaticUpdateFinalMessage(
	schedule *automaticUpdateSchedule,
) string {
	return "[SYSTEM UPDATE] The server is restarting now to install " +
		automaticUpdateDescription(schedule) + "."
}

func (m *Manager) sendAutomaticUpdateMessage(message string) error {
	if m.automaticUpdateMessageSend != nil {
		return m.automaticUpdateMessageSend(message)
	}
	return m.sendUnicodeRCONMessage(message)
}

func (m *Manager) advanceAutomaticUpdateNotice(
	expected *automaticUpdateSchedule,
	nextNotice int,
) bool {
	m.automaticUpdateMu.Lock()
	if !sameAutomaticUpdateSchedule(
		m.automaticUpdateState.Schedule,
		expected,
	) {
		m.automaticUpdateMu.Unlock()
		return false
	}
	state := cloneAutomaticUpdateState(m.automaticUpdateState)
	if nextNotice > state.Schedule.NextNotice {
		state.Schedule.NextNotice = nextNotice
	}
	if err := m.saveAutomaticUpdateStateLocked(state); err != nil {
		m.automaticUpdateMu.Unlock()
		return false
	}
	m.automaticUpdateState = state
	m.automaticUpdateMu.Unlock()
	return true
}

func (m *Manager) markAutomaticUpdateFinalNotice(
	expected *automaticUpdateSchedule,
) {
	m.automaticUpdateMu.Lock()
	if !sameAutomaticUpdateSchedule(
		m.automaticUpdateState.Schedule,
		expected,
	) || m.automaticUpdateState.Schedule.FinalAnnounced {
		m.automaticUpdateMu.Unlock()
		return
	}
	state := cloneAutomaticUpdateState(m.automaticUpdateState)
	state.Schedule.FinalAnnounced = true
	if err := m.saveAutomaticUpdateStateLocked(state); err == nil {
		m.automaticUpdateState = state
	}
	m.automaticUpdateMu.Unlock()
}

func (m *Manager) recordAutomaticUpdateAccepted(
	schedule *automaticUpdateSchedule,
) {
	m.automaticUpdateMu.Lock()
	state := cloneAutomaticUpdateState(m.automaticUpdateState)
	if schedule.ManagerVersion != "" {
		state.LastAttemptedManagerVersion = schedule.ManagerVersion
	}
	if schedule.SteamBuildID != "" {
		state.LastAttemptedSteamBuildID = schedule.SteamBuildID
	}
	state.Schedule = nil
	if err := m.saveAutomaticUpdateStateLocked(state); err != nil {
		m.automaticUpdateMu.Unlock()
		m.auditActorEvent(
			"system",
			"local",
			"automatic_update_state_save_failed",
			map[string]string{"phase": "record_attempt"},
		)
		return
	}
	m.automaticUpdateState = state
	m.automaticUpdateMu.Unlock()
}

func (m *Manager) executeAutomaticUpdate(
	schedule *automaticUpdateSchedule,
	serverRunning bool,
) error {
	if m.operationRunning() {
		return errors.New("another server operation is running")
	}
	if schedule.ManagerVersion != "" {
		if _, err := m.beginManagerUpdate(
			schedule.ManagerVersion,
			"system",
			"local",
		); err != nil {
			return err
		}
		m.recordAutomaticUpdateAccepted(schedule)
		m.auditActorEvent(
			"system",
			"local",
			"automatic_manager_update_requested",
			automaticUpdateAuditDetails(schedule),
		)
		return nil
	}
	action := "update"
	if serverRunning {
		action = "restart"
	}
	if err := m.requestOperation(
		action,
		"system",
		"local",
		"local",
	); err != nil {
		return err
	}
	m.recordAutomaticUpdateAccepted(schedule)
	details := automaticUpdateAuditDetails(schedule)
	details["action"] = action
	m.auditActorEvent(
		"system",
		"local",
		"automatic_steam_update_requested",
		details,
	)
	return nil
}

func nextAutomaticUpdateActionAt(
	schedule *automaticUpdateSchedule,
	now time.Time,
) time.Time {
	started := schedule.RestartAt.Add(-modRestartDelay)
	if schedule.NextNotice < len(modRestartNotices) {
		next := started.Add(modRestartNotices[schedule.NextNotice].After)
		if !next.After(now) {
			return now.Add(automaticUpdateRetryDelay)
		}
		return next
	}
	if !schedule.RestartAt.After(now) {
		return now.Add(automaticUpdateRetryDelay)
	}
	return schedule.RestartAt
}

func (m *Manager) processAutomaticUpdateSchedule(
	schedule *automaticUpdateSchedule,
	now time.Time,
) time.Time {
	pid, running := m.automaticUpdateServerProcess()
	if !running {
		if err := m.executeAutomaticUpdate(schedule, false); err != nil {
			return now.Add(automaticUpdateRetryDelay)
		}
		return time.Time{}
	}
	if pid != schedule.ServerPID {
		m.clearAutomaticUpdateSchedule(schedule)
		return now.Add(automaticUpdateRetryDelay)
	}
	if m.operationRunning() {
		return now.Add(automaticUpdateRetryDelay)
	}
	if !now.Before(schedule.RestartAt) {
		if !schedule.FinalAnnounced {
			message := automaticUpdateFinalMessage(schedule)
			result := "sent"
			if err := m.sendAutomaticUpdateMessage(message); err != nil {
				result = "failed"
			} else {
				m.addRCONEvent("outbound", "system: "+message)
			}
			details := automaticUpdateAuditDetails(schedule)
			details["result"] = result
			m.auditActorEvent(
				"system",
				"local",
				"automatic_update_final_notice",
				details,
			)
			m.markAutomaticUpdateFinalNotice(schedule)
		}
		if err := m.executeAutomaticUpdate(schedule, true); err != nil {
			m.auditActorEvent(
				"system",
				"local",
				"automatic_update_request_failed",
				map[string]string{
					"error": boundedManagerUpdateText(err.Error(), 256),
				},
			)
			return now.Add(automaticUpdateRetryDelay)
		}
		return time.Time{}
	}
	index := latestDueModRestartNotice(&modRestartSchedule{
		RestartAt: schedule.RestartAt,
	}, now)
	if index >= schedule.NextNotice {
		notice := modRestartNotices[index]
		message := automaticUpdateNoticeMessage(schedule, notice.Minutes)
		if err := m.sendAutomaticUpdateMessage(message); err != nil {
			m.auditActorEvent(
				"system",
				"local",
				"automatic_update_notice_failed",
				map[string]string{
					"minutes": strconv.Itoa(notice.Minutes),
				},
			)
			return now.Add(automaticUpdateRetryDelay)
		}
		m.addRCONEvent("outbound", "system: "+message)
		if !m.advanceAutomaticUpdateNotice(schedule, index+1) {
			return now.Add(automaticUpdateRetryDelay)
		}
		details := automaticUpdateAuditDetails(schedule)
		details["minutes"] = strconv.Itoa(notice.Minutes)
		m.auditActorEvent(
			"system",
			"local",
			"automatic_update_notice_sent",
			details,
		)
		m.automaticUpdateMu.RLock()
		updated := cloneAutomaticUpdateSchedule(
			m.automaticUpdateState.Schedule,
		)
		m.automaticUpdateMu.RUnlock()
		if updated == nil {
			return time.Time{}
		}
		return nextAutomaticUpdateActionAt(updated, now)
	}
	return nextAutomaticUpdateActionAt(schedule, now)
}

func (m *Manager) processAutomaticUpdates(now time.Time) time.Time {
	if m.managerUpdateRunning() {
		return now.Add(automaticUpdateRetryDelay)
	}
	if m.modUpdateRestartScheduled() {
		return now.Add(automaticUpdateRetryDelay)
	}
	targets := m.automaticUpdateTargets()
	schedule, running := m.ensureAutomaticUpdateSchedule(targets, now)
	if schedule == nil {
		if automaticUpdateTargetsEmpty(targets) {
			return time.Time{}
		}
		if running {
			return now.Add(automaticUpdateRetryDelay)
		}
		schedule = &automaticUpdateSchedule{
			DetectedAt:     now,
			RestartAt:      now.Add(modRestartDelay),
			ManagerVersion: targets.ManagerVersion,
			SteamBuildID:   targets.SteamBuildID,
		}
		if err := m.executeAutomaticUpdate(schedule, false); err != nil {
			return now.Add(automaticUpdateRetryDelay)
		}
		return time.Time{}
	}
	return m.processAutomaticUpdateSchedule(schedule, now)
}

func (m *Manager) signalAutomaticUpdateLoop() {
	if m.automaticUpdateWake == nil {
		return
	}
	select {
	case m.automaticUpdateWake <- struct{}{}:
	default:
	}
}

func (m *Manager) automaticUpdateLoop(ctx context.Context) {
	for {
		next := m.processAutomaticUpdates(m.automaticUpdateNowValue())
		if next.IsZero() {
			select {
			case <-ctx.Done():
				return
			case <-m.automaticUpdateWake:
				continue
			}
		}
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.automaticUpdateWake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}
