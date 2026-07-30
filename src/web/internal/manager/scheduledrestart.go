package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	scheduledServerRestartStatePath    = stateDir + "/scheduled-restart.json"
	scheduledServerRestartStateVersion = 1
	scheduledServerRestartRetryDelay   = 15 * time.Second
	scheduledServerRestartPollDelay    = time.Minute
	scheduledServerRestartLateGrace    = 5 * time.Minute
)

var scheduledServerRestartWeekdays = []string{
	"mon",
	"tue",
	"wed",
	"thu",
	"fri",
	"sat",
	"sun",
}

type scheduledServerRestartSchedule struct {
	RestartAt      time.Time `json:"restart_at"`
	ServerPID      int       `json:"server_pid"`
	NextNotice     int       `json:"next_notice"`
	FinalAnnounced bool      `json:"final_announced"`
}

type scheduledServerRestartStateFile struct {
	Version       int                             `json:"version"`
	Enabled       bool                            `json:"enabled"`
	ScheduledTime string                          `json:"scheduled_time"`
	Weekdays      []string                        `json:"weekdays"`
	Schedule      *scheduledServerRestartSchedule `json:"schedule,omitempty"`
}

type ScheduledServerRestartView struct {
	Enabled       bool       `json:"enabled"`
	ScheduledTime string     `json:"scheduled_time"`
	Weekdays      []string   `json:"weekdays"`
	Scheduled     bool       `json:"scheduled"`
	RestartAt     *time.Time `json:"restart_at,omitempty"`
}

func defaultScheduledServerRestartState() scheduledServerRestartStateFile {
	return scheduledServerRestartStateFile{
		Version:       scheduledServerRestartStateVersion,
		ScheduledTime: defaultModRestartTime,
		Weekdays:      append([]string(nil), scheduledServerRestartWeekdays...),
	}
}

func cloneScheduledServerRestartSchedule(
	schedule *scheduledServerRestartSchedule,
) *scheduledServerRestartSchedule {
	if schedule == nil {
		return nil
	}
	copy := *schedule
	return &copy
}

func cloneScheduledServerRestartState(
	state scheduledServerRestartStateFile,
) scheduledServerRestartStateFile {
	state.Weekdays = append([]string(nil), state.Weekdays...)
	state.Schedule = cloneScheduledServerRestartSchedule(state.Schedule)
	return state
}

func scheduledServerRestartWeekday(value string) (time.Weekday, bool) {
	switch value {
	case "sun":
		return time.Sunday, true
	case "mon":
		return time.Monday, true
	case "tue":
		return time.Tuesday, true
	case "wed":
		return time.Wednesday, true
	case "thu":
		return time.Thursday, true
	case "fri":
		return time.Friday, true
	case "sat":
		return time.Saturday, true
	default:
		return 0, false
	}
}

func normalizeScheduledServerRestartWeekdays(
	values []string,
) ([]string, error) {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if _, valid := scheduledServerRestartWeekday(value); !valid {
			return nil, errors.New("scheduled restart contains an invalid weekday")
		}
		if seen[value] {
			return nil, errors.New("scheduled restart contains a duplicate weekday")
		}
		seen[value] = true
	}
	if len(seen) == 0 {
		return nil, errors.New("select at least one scheduled restart weekday")
	}
	normalized := make([]string, 0, len(seen))
	for _, value := range scheduledServerRestartWeekdays {
		if seen[value] {
			normalized = append(normalized, value)
		}
	}
	return normalized, nil
}

func validScheduledServerRestartSchedule(
	schedule *scheduledServerRestartSchedule,
) bool {
	return schedule == nil ||
		(!schedule.RestartAt.IsZero() &&
			schedule.ServerPID > 1 &&
			schedule.NextNotice >= 0 &&
			schedule.NextNotice <= len(modRestartNotices))
}

func validScheduledServerRestartState(
	state scheduledServerRestartStateFile,
) bool {
	if state.Version != scheduledServerRestartStateVersion ||
		!validModRestartTime(state.ScheduledTime) ||
		!validScheduledServerRestartSchedule(state.Schedule) {
		return false
	}
	if !state.Enabled && state.Schedule != nil {
		return false
	}
	normalized, err := normalizeScheduledServerRestartWeekdays(state.Weekdays)
	if err != nil || len(normalized) != len(state.Weekdays) {
		return false
	}
	for index := range normalized {
		if normalized[index] != state.Weekdays[index] {
			return false
		}
	}
	return true
}

func nextScheduledServerRestart(
	now time.Time,
	value string,
	weekdays []string,
) time.Time {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		parsed, _ = time.Parse("15:04", defaultModRestartTime)
	}
	allowed := make(map[time.Weekday]bool, len(weekdays))
	for _, value := range weekdays {
		if weekday, valid := scheduledServerRestartWeekday(value); valid {
			allowed[weekday] = true
		}
	}
	for offset := 0; offset < 14; offset++ {
		day := now.AddDate(0, 0, offset)
		if !allowed[day.Weekday()] {
			continue
		}
		candidate := time.Date(
			day.Year(),
			day.Month(),
			day.Day(),
			parsed.Hour(),
			parsed.Minute(),
			0,
			0,
			now.Location(),
		)
		if !candidate.Before(now.Add(modRestartDelay)) {
			return candidate
		}
	}
	return time.Time{}
}

func (m *Manager) scheduledServerRestartStatePath() string {
	if m.scheduledRestartStateFile != "" {
		return m.scheduledRestartStateFile
	}
	return scheduledServerRestartStatePath
}

func (m *Manager) scheduledServerRestartNowValue() time.Time {
	if m.scheduledRestartNow != nil {
		return m.scheduledRestartNow()
	}
	return time.Now()
}

func (m *Manager) saveScheduledServerRestartStateLocked(
	state scheduledServerRestartStateFile,
) error {
	return writeJSONAtomic(m.scheduledServerRestartStatePath(), state, 0600)
}

func (m *Manager) loadOrCreateScheduledServerRestartState() error {
	path := m.scheduledServerRestartStatePath()
	state := defaultScheduledServerRestartState()
	if err := readJSON(path, &state); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load scheduled server restart settings: %w", err)
		}
		if err := writeJSONAtomic(path, state, 0600); err != nil {
			return fmt.Errorf("create scheduled server restart settings: %w", err)
		}
	} else {
		if !validScheduledServerRestartState(state) {
			return errors.New("stored scheduled server restart settings are invalid")
		}
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
	}
	m.scheduledRestartState = state
	return nil
}

func scheduledServerRestartViewFromState(
	state scheduledServerRestartStateFile,
) ScheduledServerRestartView {
	view := ScheduledServerRestartView{
		Enabled:       state.Enabled,
		ScheduledTime: state.ScheduledTime,
		Weekdays:      append([]string(nil), state.Weekdays...),
	}
	if state.Schedule != nil {
		view.Scheduled = true
		view.RestartAt = timeView(state.Schedule.RestartAt)
	}
	return view
}

func (m *Manager) scheduledServerRestartView() ScheduledServerRestartView {
	m.scheduledRestartMu.RLock()
	view := scheduledServerRestartViewFromState(m.scheduledRestartState)
	m.scheduledRestartMu.RUnlock()
	return view
}

func (m *Manager) setScheduledServerRestartSettings(
	enabled bool,
	scheduledTime string,
	weekdays []string,
) (ScheduledServerRestartView, error) {
	if !validModRestartTime(scheduledTime) {
		return ScheduledServerRestartView{},
			errors.New("scheduled restart time must use HH:MM")
	}
	normalized, err := normalizeScheduledServerRestartWeekdays(weekdays)
	if err != nil {
		return ScheduledServerRestartView{}, err
	}
	m.scheduledRestartMu.Lock()
	state := cloneScheduledServerRestartState(m.scheduledRestartState)
	state.Enabled = enabled
	state.ScheduledTime = scheduledTime
	state.Weekdays = normalized
	state.Schedule = nil
	if err := m.saveScheduledServerRestartStateLocked(state); err != nil {
		m.scheduledRestartMu.Unlock()
		return ScheduledServerRestartView{}, err
	}
	m.scheduledRestartState = state
	m.scheduledRestartMu.Unlock()
	m.signalScheduledServerRestartLoop()
	m.signalAutomaticUpdateLoop()
	m.signalModRestartLoop()
	if enabled {
		_, _ = m.ensureScheduledServerRestartSchedule(
			m.scheduledServerRestartNowValue(),
		)
	}
	return m.scheduledServerRestartView(), nil
}

func (m *Manager) serverProcessForScheduledRestart() (int, bool) {
	if m.scheduledRestartServerProcess != nil {
		return m.scheduledRestartServerProcess()
	}
	return serverProcess()
}

func sameScheduledServerRestartSchedule(
	left *scheduledServerRestartSchedule,
	right *scheduledServerRestartSchedule,
) bool {
	return left != nil &&
		right != nil &&
		left.ServerPID == right.ServerPID &&
		left.RestartAt.Equal(right.RestartAt)
}

func (m *Manager) scheduledServerRestartSnapshot() (
	scheduledServerRestartStateFile,
	*scheduledServerRestartSchedule,
) {
	m.scheduledRestartMu.RLock()
	state := cloneScheduledServerRestartState(m.scheduledRestartState)
	m.scheduledRestartMu.RUnlock()
	return state, state.Schedule
}

func (m *Manager) scheduledServerRestartActive(now time.Time) bool {
	state, schedule := m.scheduledServerRestartSnapshot()
	return state.Enabled &&
		schedule != nil &&
		!now.Before(schedule.RestartAt.Add(-modRestartDelay))
}

func (m *Manager) replaceScheduledServerRestartSchedule(
	expected *scheduledServerRestartSchedule,
	restartAt time.Time,
	pid int,
) (*scheduledServerRestartSchedule, bool) {
	m.scheduledRestartMu.Lock()
	if (expected == nil && m.scheduledRestartState.Schedule != nil) ||
		(expected != nil &&
			!sameScheduledServerRestartSchedule(
				m.scheduledRestartState.Schedule,
				expected,
			)) ||
		(!restartAt.IsZero() && !m.scheduledRestartState.Enabled) {
		m.scheduledRestartMu.Unlock()
		return nil, false
	}
	state := cloneScheduledServerRestartState(m.scheduledRestartState)
	if restartAt.IsZero() || pid <= 1 {
		state.Schedule = nil
	} else {
		state.Schedule = &scheduledServerRestartSchedule{
			RestartAt: restartAt,
			ServerPID: pid,
		}
	}
	if err := m.saveScheduledServerRestartStateLocked(state); err != nil {
		m.scheduledRestartMu.Unlock()
		return nil, false
	}
	m.scheduledRestartState = state
	schedule := cloneScheduledServerRestartSchedule(state.Schedule)
	m.scheduledRestartMu.Unlock()
	return schedule, true
}

func (m *Manager) ensureScheduledServerRestartSchedule(
	now time.Time,
) (*scheduledServerRestartSchedule, bool) {
	pid, running := m.serverProcessForScheduledRestart()
	state, schedule := m.scheduledServerRestartSnapshot()
	if !state.Enabled || !running {
		if schedule != nil {
			_, _ = m.replaceScheduledServerRestartSchedule(
				schedule,
				time.Time{},
				0,
			)
		}
		return nil, running
	}
	if schedule != nil && schedule.ServerPID == pid {
		return schedule, true
	}
	restartAt := nextScheduledServerRestart(
		now,
		state.ScheduledTime,
		state.Weekdays,
	)
	return m.replaceScheduledServerRestartSchedule(schedule, restartAt, pid)
}

func scheduledServerRestartNoticeMessage(minutes int) string {
	if minutes == 1 {
		return "[SCHEDULED RESTART] The server will restart in 1 minute."
	}
	return fmt.Sprintf(
		"[SCHEDULED RESTART] The server will restart in %d minutes.",
		minutes,
	)
}

func finalScheduledServerRestartMessage() string {
	return "[SCHEDULED RESTART] The server is restarting now."
}

func (m *Manager) sendScheduledServerRestartMessage(message string) error {
	if m.scheduledRestartMessageSend != nil {
		return m.scheduledRestartMessageSend(message)
	}
	return m.sendUnicodeRCONMessage(message)
}

func (m *Manager) advanceScheduledServerRestartNotice(
	expected *scheduledServerRestartSchedule,
	nextNotice int,
) bool {
	m.scheduledRestartMu.Lock()
	if !sameScheduledServerRestartSchedule(
		m.scheduledRestartState.Schedule,
		expected,
	) {
		m.scheduledRestartMu.Unlock()
		return false
	}
	state := cloneScheduledServerRestartState(m.scheduledRestartState)
	if nextNotice > state.Schedule.NextNotice {
		state.Schedule.NextNotice = nextNotice
	}
	if err := m.saveScheduledServerRestartStateLocked(state); err != nil {
		m.scheduledRestartMu.Unlock()
		return false
	}
	m.scheduledRestartState = state
	m.scheduledRestartMu.Unlock()
	return true
}

func (m *Manager) markScheduledServerRestartFinalNotice(
	expected *scheduledServerRestartSchedule,
) {
	m.scheduledRestartMu.Lock()
	if !sameScheduledServerRestartSchedule(
		m.scheduledRestartState.Schedule,
		expected,
	) || m.scheduledRestartState.Schedule.FinalAnnounced {
		m.scheduledRestartMu.Unlock()
		return
	}
	state := cloneScheduledServerRestartState(m.scheduledRestartState)
	state.Schedule.FinalAnnounced = true
	if err := m.saveScheduledServerRestartStateLocked(state); err == nil {
		m.scheduledRestartState = state
	}
	m.scheduledRestartMu.Unlock()
}

func (m *Manager) skipScheduledServerRestart(
	schedule *scheduledServerRestartSchedule,
	now time.Time,
	reason string,
) time.Time {
	state, current := m.scheduledServerRestartSnapshot()
	if current == nil ||
		!sameScheduledServerRestartSchedule(current, schedule) {
		return now.Add(scheduledServerRestartRetryDelay)
	}
	next := nextScheduledServerRestart(
		schedule.RestartAt.Add(time.Second),
		state.ScheduledTime,
		state.Weekdays,
	)
	updated, saved := m.replaceScheduledServerRestartSchedule(
		schedule,
		next,
		schedule.ServerPID,
	)
	if !saved || updated == nil {
		return now.Add(scheduledServerRestartRetryDelay)
	}
	m.auditActorEvent(
		"system",
		"local",
		"scheduled_server_restart_skipped",
		map[string]string{
			"reason":          reason,
			"next_restart_at": updated.RestartAt.Format(time.RFC3339),
		},
	)
	m.signalAutomaticUpdateLoop()
	m.signalModRestartLoop()
	return updated.RestartAt.Add(-modRestartDelay)
}

func (m *Manager) completeScheduledServerRestart(
	schedule *scheduledServerRestartSchedule,
	now time.Time,
) time.Time {
	state, current := m.scheduledServerRestartSnapshot()
	if current == nil ||
		!sameScheduledServerRestartSchedule(current, schedule) {
		return now.Add(scheduledServerRestartRetryDelay)
	}
	next := nextScheduledServerRestart(
		schedule.RestartAt.Add(time.Second),
		state.ScheduledTime,
		state.Weekdays,
	)
	if err := m.requestOperation(
		"restart",
		"system",
		"local",
		"local",
	); err != nil {
		m.auditActorEvent(
			"system",
			"local",
			"scheduled_server_restart_request_failed",
			map[string]string{"error": safeAuditText(err.Error(), 160)},
		)
		return now.Add(scheduledServerRestartRetryDelay)
	}
	updated, saved := m.replaceScheduledServerRestartSchedule(
		schedule,
		next,
		schedule.ServerPID,
	)
	if !saved {
		return now.Add(scheduledServerRestartRetryDelay)
	}
	details := map[string]string{
		"restart_at": schedule.RestartAt.Format(time.RFC3339),
	}
	if updated != nil {
		details["next_restart_at"] = updated.RestartAt.Format(time.RFC3339)
	}
	m.auditActorEvent(
		"system",
		"local",
		"scheduled_server_restart_requested",
		details,
	)
	m.signalAutomaticUpdateLoop()
	m.signalModRestartLoop()
	return time.Time{}
}

func nextScheduledServerRestartActionAt(
	schedule *scheduledServerRestartSchedule,
	now time.Time,
) time.Time {
	started := schedule.RestartAt.Add(-modRestartDelay)
	if schedule.NextNotice < len(modRestartNotices) {
		next := started.Add(modRestartNotices[schedule.NextNotice].After)
		if !next.After(now) {
			return now.Add(scheduledServerRestartRetryDelay)
		}
		return next
	}
	if !schedule.RestartAt.After(now) {
		return now.Add(scheduledServerRestartRetryDelay)
	}
	return schedule.RestartAt
}

func (m *Manager) processScheduledServerRestart(now time.Time) time.Time {
	state, _ := m.scheduledServerRestartSnapshot()
	if !state.Enabled {
		return time.Time{}
	}
	schedule, running := m.ensureScheduledServerRestartSchedule(now)
	if schedule == nil {
		if running {
			return now.Add(scheduledServerRestartRetryDelay)
		}
		return now.Add(scheduledServerRestartPollDelay)
	}
	if now.Before(schedule.RestartAt.Add(-modRestartDelay)) {
		return schedule.RestartAt.Add(-modRestartDelay)
	}
	if !now.Before(schedule.RestartAt) &&
		(schedule.NextNotice < len(modRestartNotices) ||
			now.After(schedule.RestartAt.Add(
				scheduledServerRestartLateGrace,
			))) {
		return m.skipScheduledServerRestart(
			schedule,
			now,
			"maintenance_window_missed",
		)
	}
	if m.managerUpdateRunning() {
		return m.skipScheduledServerRestart(
			schedule,
			now,
			"manager_update_running",
		)
	}
	if m.modUpdateRestartActive(now) {
		return m.skipScheduledServerRestart(
			schedule,
			now,
			"mod_update_restart_pending",
		)
	}
	if m.automaticUpdateRestartActive(now) {
		return m.skipScheduledServerRestart(
			schedule,
			now,
			"automatic_update_restart_pending",
		)
	}
	if m.operationRunning() {
		return m.skipScheduledServerRestart(
			schedule,
			now,
			"lifecycle_operation_running",
		)
	}
	pid, running := m.serverProcessForScheduledRestart()
	if !running || pid != schedule.ServerPID {
		_, _ = m.replaceScheduledServerRestartSchedule(
			schedule,
			time.Time{},
			0,
		)
		return now.Add(scheduledServerRestartPollDelay)
	}
	if !now.Before(schedule.RestartAt) {
		if !schedule.FinalAnnounced {
			message := finalScheduledServerRestartMessage()
			result := "sent"
			if err := m.sendScheduledServerRestartMessage(message); err != nil {
				result = "failed"
			} else {
				m.addRCONEvent("outbound", "system: "+message)
			}
			m.auditActorEvent(
				"system",
				"local",
				"scheduled_server_restart_final_notice",
				map[string]string{"result": result},
			)
			m.markScheduledServerRestartFinalNotice(schedule)
		}
		return m.completeScheduledServerRestart(schedule, now)
	}
	index := latestDueModRestartNotice(&modRestartSchedule{
		RestartAt: schedule.RestartAt,
	}, now)
	if index > schedule.NextNotice {
		return m.skipScheduledServerRestart(
			schedule,
			now,
			"countdown_notice_missed",
		)
	}
	if index >= schedule.NextNotice {
		notice := modRestartNotices[index]
		message := scheduledServerRestartNoticeMessage(notice.Minutes)
		if err := m.sendScheduledServerRestartMessage(message); err != nil {
			m.auditActorEvent(
				"system",
				"local",
				"scheduled_server_restart_notice_failed",
				map[string]string{
					"minutes": strconv.Itoa(notice.Minutes),
				},
			)
			return now.Add(scheduledServerRestartRetryDelay)
		}
		m.addRCONEvent("outbound", "system: "+message)
		if !m.advanceScheduledServerRestartNotice(
			schedule,
			index+1,
		) {
			return now.Add(scheduledServerRestartRetryDelay)
		}
		m.auditActorEvent(
			"system",
			"local",
			"scheduled_server_restart_notice_sent",
			map[string]string{
				"minutes": strconv.Itoa(notice.Minutes),
			},
		)
		_, schedule = m.scheduledServerRestartSnapshot()
	}
	return nextScheduledServerRestartActionAt(schedule, now)
}

func (m *Manager) signalScheduledServerRestartLoop() {
	if m.scheduledRestartWake == nil {
		return
	}
	select {
	case m.scheduledRestartWake <- struct{}{}:
	default:
	}
}

func (m *Manager) scheduledServerRestartLoop(ctx context.Context) {
	for {
		next := m.processScheduledServerRestart(
			m.scheduledServerRestartNowValue(),
		)
		if next.IsZero() {
			select {
			case <-ctx.Done():
				return
			case <-m.scheduledRestartWake:
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
		case <-m.scheduledRestartWake:
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
