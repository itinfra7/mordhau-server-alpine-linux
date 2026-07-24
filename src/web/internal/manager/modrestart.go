package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	modUpdateStateVersion = 1
	modRestartDelay       = 10 * time.Minute
	modRestartRetryDelay  = 15 * time.Second
)

var modRestartNotices = []struct {
	After   time.Duration
	Minutes int
}{
	{After: 0, Minutes: 10},
	{After: 5 * time.Minute, Minutes: 5},
	{After: 6 * time.Minute, Minutes: 4},
	{After: 7 * time.Minute, Minutes: 3},
	{After: 8 * time.Minute, Minutes: 2},
	{After: 9 * time.Minute, Minutes: 1},
}

type trackedModfile struct {
	ModID     int    `json:"mod_id"`
	ModfileID int    `json:"modfile_id"`
	Version   string `json:"version,omitempty"`
}

type modfileUpdate struct {
	ModID          int    `json:"mod_id"`
	PreviousFile   int    `json:"previous_modfile_id"`
	CurrentFile    int    `json:"current_modfile_id"`
	CurrentVersion string `json:"current_version,omitempty"`
}

type modRestartSchedule struct {
	DetectedAt     time.Time       `json:"detected_at"`
	RestartAt      time.Time       `json:"restart_at"`
	ServerPID      int             `json:"server_pid"`
	Updates        []modfileUpdate `json:"updates"`
	NextNotice     int             `json:"next_notice"`
	FinalAnnounced bool            `json:"final_announced"`
}

type modUpdateStateFile struct {
	Version  int                 `json:"version"`
	Baseline []trackedModfile    `json:"baseline"`
	Schedule *modRestartSchedule `json:"schedule,omitempty"`
}

type modUpdateDetection struct {
	Updates          []modfileUpdate
	RestartScheduled bool
	RestartAt        time.Time
	ServerRunning    bool
}

func (m *Manager) modUpdateStateFilePath() string {
	if m.modUpdateStateFile != "" {
		return m.modUpdateStateFile
	}
	return modUpdateStatePath
}

func cloneModRestartSchedule(schedule *modRestartSchedule) *modRestartSchedule {
	if schedule == nil {
		return nil
	}
	copy := *schedule
	copy.Updates = append([]modfileUpdate(nil), schedule.Updates...)
	return &copy
}

func cloneModUpdateState(state modUpdateStateFile) modUpdateStateFile {
	return modUpdateStateFile{
		Version:  state.Version,
		Baseline: append([]trackedModfile(nil), state.Baseline...),
		Schedule: cloneModRestartSchedule(state.Schedule),
	}
}

func validTrackedModfiles(files []trackedModfile) bool {
	previous := 0
	for _, file := range files {
		if file.ModID < 1 || file.ModfileID < 1 || file.ModID <= previous {
			return false
		}
		previous = file.ModID
	}
	return true
}

func validModfileUpdates(updates []modfileUpdate) bool {
	if len(updates) == 0 {
		return false
	}
	previous := 0
	for _, update := range updates {
		if update.ModID < 1 ||
			update.PreviousFile < 1 ||
			update.CurrentFile < 1 ||
			update.PreviousFile == update.CurrentFile ||
			update.ModID <= previous {
			return false
		}
		previous = update.ModID
	}
	return true
}

func validModUpdateState(state modUpdateStateFile) bool {
	if state.Version != modUpdateStateVersion ||
		!validTrackedModfiles(state.Baseline) {
		return false
	}
	if state.Schedule == nil {
		return true
	}
	schedule := state.Schedule
	return !schedule.DetectedAt.IsZero() &&
		!schedule.RestartAt.IsZero() &&
		schedule.RestartAt.After(schedule.DetectedAt) &&
		schedule.ServerPID > 1 &&
		validModfileUpdates(schedule.Updates) &&
		schedule.NextNotice >= 0 &&
		schedule.NextNotice <= len(modRestartNotices)
}

func (m *Manager) saveModUpdateStateValue(state modUpdateStateFile) error {
	return writeJSONAtomic(m.modUpdateStateFilePath(), state, 0600)
}

func (m *Manager) loadOrCreateModUpdateState() error {
	path := m.modUpdateStateFilePath()
	state := modUpdateStateFile{
		Version:  modUpdateStateVersion,
		Baseline: []trackedModfile{},
	}
	if err := readJSON(path, &state); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load mod update state: %w", err)
		}
		if err := m.saveModUpdateStateValue(state); err != nil {
			return fmt.Errorf("create mod update state: %w", err)
		}
	} else {
		if !validModUpdateState(state) {
			return errors.New("stored mod update state is invalid")
		}
		if !m.modRefreshSettings.RestartOnUpdate && state.Schedule != nil {
			state.Schedule = nil
			if err := m.saveModUpdateStateValue(state); err != nil {
				return fmt.Errorf("clear disabled mod restart schedule: %w", err)
			}
		} else if err := os.Chmod(path, 0600); err != nil {
			return err
		}
	}
	m.modUpdateState = state
	m.modUpdateStateLoaded = true
	return nil
}

func activeModfileBaseline(view ModManagementView) []trackedModfile {
	files := make([]trackedModfile, 0, len(view.Mods))
	for _, mod := range view.Mods {
		if !mod.Enabled ||
			mod.Metadata == nil ||
			mod.Metadata.Modfile == nil ||
			mod.ID < 1 ||
			mod.Metadata.Modfile.ID < 1 {
			continue
		}
		files = append(files, trackedModfile{
			ModID:     mod.ID,
			ModfileID: mod.Metadata.Modfile.ID,
			Version:   strings.TrimSpace(mod.Metadata.Modfile.Version),
		})
	}
	sort.Slice(files, func(left, right int) bool {
		return files[left].ModID < files[right].ModID
	})
	return files
}

func changedActiveModfiles(
	previous []trackedModfile,
	current []trackedModfile,
) []modfileUpdate {
	previousByID := make(map[int]trackedModfile, len(previous))
	for _, file := range previous {
		previousByID[file.ModID] = file
	}
	updates := make([]modfileUpdate, 0)
	for _, file := range current {
		old, found := previousByID[file.ModID]
		if !found || old.ModfileID == file.ModfileID {
			continue
		}
		updates = append(updates, modfileUpdate{
			ModID:          file.ModID,
			PreviousFile:   old.ModfileID,
			CurrentFile:    file.ModfileID,
			CurrentVersion: file.Version,
		})
	}
	return updates
}

func mergeModfileUpdates(
	existing []modfileUpdate,
	incoming []modfileUpdate,
) []modfileUpdate {
	merged := make(map[int]modfileUpdate, len(existing)+len(incoming))
	for _, update := range existing {
		merged[update.ModID] = update
	}
	for _, update := range incoming {
		if old, found := merged[update.ModID]; found {
			if update.CurrentFile == old.PreviousFile {
				delete(merged, update.ModID)
				continue
			}
			update.PreviousFile = old.PreviousFile
		}
		merged[update.ModID] = update
	}
	result := make([]modfileUpdate, 0, len(merged))
	for _, update := range merged {
		result = append(result, update)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].ModID < result[right].ModID
	})
	return result
}

func (m *Manager) serverProcessForMods() (int, bool) {
	if m.modServerProcess != nil {
		return m.modServerProcess()
	}
	return serverProcess()
}

func (m *Manager) recordSuccessfulModRefreshLocked(
	view ModManagementView,
	finished time.Time,
) (modUpdateDetection, error) {
	current := activeModfileBaseline(view)
	updates := changedActiveModfiles(m.modUpdateState.Baseline, current)
	state := cloneModUpdateState(m.modUpdateState)
	state.Version = modUpdateStateVersion
	state.Baseline = current

	detection := modUpdateDetection{
		Updates: append([]modfileUpdate(nil), updates...),
	}
	var pid int
	var running bool
	if len(updates) > 0 {
		pid, running = m.serverProcessForMods()
		detection.ServerRunning = running
	}
	if len(updates) > 0 &&
		m.modRefreshSettings.RestartOnUpdate &&
		view.Settings.APIKeyConfigured {
		if running {
			switch {
			case state.Schedule == nil:
				state.Schedule = &modRestartSchedule{
					DetectedAt: finished,
					RestartAt:  finished.Add(modRestartDelay),
					ServerPID:  pid,
					Updates:    append([]modfileUpdate(nil), updates...),
				}
				detection.RestartScheduled = true
			case state.Schedule.ServerPID == pid:
				state.Schedule.Updates = mergeModfileUpdates(
					state.Schedule.Updates,
					updates,
				)
				if len(state.Schedule.Updates) == 0 {
					state.Schedule = nil
				} else {
					detection.RestartScheduled = true
				}
			}
			if detection.RestartScheduled {
				detection.RestartAt = state.Schedule.RestartAt
			}
		}
	}

	if m.modUpdateStateLoaded {
		if err := m.saveModUpdateStateValue(state); err != nil {
			return modUpdateDetection{}, fmt.Errorf("save mod update state: %w", err)
		}
	}
	m.modUpdateState = state
	return detection, nil
}

func modUpdateIDs(updates []modfileUpdate) string {
	ids := make([]string, 0, len(updates))
	for _, update := range updates {
		ids = append(ids, strconv.Itoa(update.ModID))
	}
	return strings.Join(ids, ",")
}

func (m *Manager) handleModUpdateDetection(detection modUpdateDetection) {
	if len(detection.Updates) == 0 {
		return
	}
	details := map[string]string{
		"mod_ids":           modUpdateIDs(detection.Updates),
		"restart_scheduled": strconv.FormatBool(detection.RestartScheduled),
		"server_running":    strconv.FormatBool(detection.ServerRunning),
	}
	if detection.RestartScheduled {
		details["restart_at"] = detection.RestartAt.Format(time.RFC3339)
	}
	m.auditActorEvent("system", "local", "active_mod_update_detected", details)
	if detection.RestartScheduled {
		m.signalModRestartLoop()
	}
}

func (m *Manager) signalModRestartLoop() {
	if m.modRestartWake == nil {
		return
	}
	select {
	case m.modRestartWake <- struct{}{}:
	default:
	}
}

func sameModRestartSchedule(left, right *modRestartSchedule) bool {
	return left != nil &&
		right != nil &&
		left.ServerPID == right.ServerPID &&
		left.RestartAt.Equal(right.RestartAt)
}

func (m *Manager) scheduledRestartSnapshot() (
	modRefreshSettingsFile,
	*modRestartSchedule,
) {
	m.modsMu.RLock()
	settings := m.modRefreshSettings
	schedule := cloneModRestartSchedule(m.modUpdateState.Schedule)
	m.modsMu.RUnlock()
	return settings, schedule
}

func (m *Manager) clearModRestartSchedule(
	expected *modRestartSchedule,
	reason string,
) bool {
	m.modsMu.Lock()
	if !sameModRestartSchedule(m.modUpdateState.Schedule, expected) {
		m.modsMu.Unlock()
		return false
	}
	state := cloneModUpdateState(m.modUpdateState)
	state.Schedule = nil
	if err := m.saveModUpdateStateValue(state); err != nil {
		m.modsMu.Unlock()
		m.auditActorEvent("system", "local", "mod_update_state_save_failed",
			map[string]string{"phase": "cancel", "reason": reason})
		return false
	}
	m.modUpdateState = state
	m.modRevision++
	m.modsMu.Unlock()
	if reason != "restart_accepted" {
		m.auditActorEvent("system", "local", "mod_update_restart_cancelled",
			map[string]string{
				"mod_ids": modUpdateIDs(expected.Updates),
				"reason":  reason,
			})
	}
	return true
}

func (m *Manager) operationRunning() bool {
	m.mu.RLock()
	running := m.op.Running
	m.mu.RUnlock()
	return running
}

func modRestartNoticeMessage(minutes int) string {
	if minutes == 1 {
		return "[MOD UPDATE] The server will restart in 1 minute to apply mod updates."
	}
	return fmt.Sprintf(
		"[MOD UPDATE] The server will restart in %d minutes to apply mod updates.",
		minutes,
	)
}

func finalModRestartMessage() string {
	return "[MOD UPDATE] The server is restarting now to apply mod updates."
}

func latestDueModRestartNotice(schedule *modRestartSchedule, now time.Time) int {
	if schedule == nil || !now.Before(schedule.RestartAt) {
		return -1
	}
	started := schedule.RestartAt.Add(-modRestartDelay)
	latest := -1
	for index, notice := range modRestartNotices {
		if !now.Before(started.Add(notice.After)) {
			latest = index
		}
	}
	return latest
}

func nextModRestartActionAt(schedule *modRestartSchedule, now time.Time) time.Time {
	if schedule == nil {
		return time.Time{}
	}
	started := schedule.RestartAt.Add(-modRestartDelay)
	if schedule.NextNotice < len(modRestartNotices) {
		next := started.Add(modRestartNotices[schedule.NextNotice].After)
		if !next.After(now) {
			return now.Add(modRestartRetryDelay)
		}
		return next
	}
	if !schedule.RestartAt.After(now) {
		return now.Add(modRestartRetryDelay)
	}
	return schedule.RestartAt
}

func (m *Manager) sendModRestartNotice(message string) error {
	if m.modRestartMessageSend != nil {
		return m.modRestartMessageSend(message)
	}
	return m.sendUnicodeRCONMessage(message)
}

func (m *Manager) advanceModRestartNotice(
	expected *modRestartSchedule,
	nextNotice int,
) bool {
	m.modsMu.Lock()
	if !sameModRestartSchedule(m.modUpdateState.Schedule, expected) {
		m.modsMu.Unlock()
		return false
	}
	state := cloneModUpdateState(m.modUpdateState)
	if nextNotice <= state.Schedule.NextNotice {
		m.modsMu.Unlock()
		return true
	}
	state.Schedule.NextNotice = nextNotice
	if err := m.saveModUpdateStateValue(state); err != nil {
		m.modsMu.Unlock()
		m.auditActorEvent("system", "local", "mod_update_state_save_failed",
			map[string]string{"phase": "countdown"})
		return false
	}
	m.modUpdateState = state
	m.modsMu.Unlock()
	return true
}

func (m *Manager) markFinalModRestartNotice(expected *modRestartSchedule) {
	m.modsMu.Lock()
	if !sameModRestartSchedule(m.modUpdateState.Schedule, expected) ||
		m.modUpdateState.Schedule.FinalAnnounced {
		m.modsMu.Unlock()
		return
	}
	state := cloneModUpdateState(m.modUpdateState)
	state.Schedule.FinalAnnounced = true
	if err := m.saveModUpdateStateValue(state); err != nil {
		m.modsMu.Unlock()
		m.auditActorEvent("system", "local", "mod_update_state_save_failed",
			map[string]string{"phase": "final_notice"})
		return
	}
	m.modUpdateState = state
	m.modsMu.Unlock()
}

func (m *Manager) processModRestartNotice(
	schedule *modRestartSchedule,
	index int,
	now time.Time,
) time.Time {
	notice := modRestartNotices[index]
	message := modRestartNoticeMessage(notice.Minutes)
	if err := m.sendModRestartNotice(message); err != nil {
		m.auditActorEvent("system", "local", "mod_update_restart_notice_failed",
			map[string]string{
				"minutes": strconv.Itoa(notice.Minutes),
				"mod_ids": modUpdateIDs(schedule.Updates),
			})
		return now.Add(modRestartRetryDelay)
	}
	m.addRCONEvent("outbound", "system: "+message)
	if !m.advanceModRestartNotice(schedule, index+1) {
		return now.Add(modRestartRetryDelay)
	}
	m.auditActorEvent("system", "local", "mod_update_restart_notice_sent",
		map[string]string{
			"minutes": strconv.Itoa(notice.Minutes),
			"mod_ids": modUpdateIDs(schedule.Updates),
		})
	_, updated := m.scheduledRestartSnapshot()
	return nextModRestartActionAt(updated, now)
}

func (m *Manager) processFinalModRestart(
	schedule *modRestartSchedule,
	now time.Time,
) time.Time {
	if !schedule.FinalAnnounced {
		message := finalModRestartMessage()
		result := "sent"
		if err := m.sendModRestartNotice(message); err != nil {
			result = "failed"
		} else {
			m.addRCONEvent("outbound", "system: "+message)
		}
		m.auditActorEvent("system", "local", "mod_update_restart_final_notice",
			map[string]string{
				"mod_ids": modUpdateIDs(schedule.Updates),
				"result":  result,
			})
		m.markFinalModRestartNotice(schedule)
	}

	settings, current := m.scheduledRestartSnapshot()
	if current == nil || !sameModRestartSchedule(current, schedule) {
		return time.Time{}
	}
	pid, running := m.serverProcessForMods()
	if !settings.RestartOnUpdate ||
		!running ||
		pid != schedule.ServerPID ||
		m.operationRunning() {
		return now.Add(modRestartRetryDelay)
	}

	if err := m.requestOperation("restart", "system", "local", "local"); err != nil {
		m.auditActorEvent("system", "local", "mod_update_restart_request_failed",
			map[string]string{
				"mod_ids": modUpdateIDs(schedule.Updates),
				"reason":  safeAuditText(err.Error(), 160),
			})
		return now.Add(modRestartRetryDelay)
	}
	m.auditActorEvent("system", "local", "mod_update_restart_requested",
		map[string]string{"mod_ids": modUpdateIDs(schedule.Updates)})
	if !m.clearModRestartSchedule(schedule, "restart_accepted") {
		return now.Add(modRestartRetryDelay)
	}
	return time.Time{}
}

func (m *Manager) processModRestartSchedule(now time.Time) time.Time {
	settings, schedule := m.scheduledRestartSnapshot()
	if schedule == nil {
		return time.Time{}
	}
	if !settings.RestartOnUpdate {
		if !m.clearModRestartSchedule(schedule, "setting_disabled") {
			return now.Add(modRestartRetryDelay)
		}
		return time.Time{}
	}
	pid, running := m.serverProcessForMods()
	if !running {
		if !m.clearModRestartSchedule(schedule, "server_stopped") {
			return now.Add(modRestartRetryDelay)
		}
		return time.Time{}
	}
	if pid != schedule.ServerPID {
		if !m.clearModRestartSchedule(schedule, "server_process_changed") {
			return now.Add(modRestartRetryDelay)
		}
		return time.Time{}
	}
	if m.operationRunning() {
		if !m.clearModRestartSchedule(schedule, "lifecycle_operation_started") {
			return now.Add(modRestartRetryDelay)
		}
		return time.Time{}
	}
	if !now.Before(schedule.RestartAt) {
		return m.processFinalModRestart(schedule, now)
	}
	index := latestDueModRestartNotice(schedule, now)
	if index >= schedule.NextNotice {
		return m.processModRestartNotice(schedule, index, now)
	}
	return nextModRestartActionAt(schedule, now)
}

func (m *Manager) modRestartLoop(ctx context.Context) {
	for {
		next := m.processModRestartSchedule(time.Now())
		if next.IsZero() {
			select {
			case <-ctx.Done():
				return
			case <-m.modRestartWake:
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
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-m.modRestartWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}
