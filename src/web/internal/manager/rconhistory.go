package manager

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	rconMemoryMaximum       = 1000
	rconMemoryRetain        = 800
	rconBrowserHistoryLimit = 400
)

type rconHistoryVisibility struct {
	activePlayers        map[string]struct{}
	emptyLeavingMapShown bool
}

func newRCONHistoryVisibility() *rconHistoryVisibility {
	return &rconHistoryVisibility{
		activePlayers: make(map[string]struct{}),
	}
}

func storedRCONPlayerLifecycle(event RCONEvent) (string, string, bool) {
	if event.Kind != "login" {
		return "", "", false
	}
	action := ""
	body := event.Text
	switch {
	case strings.HasSuffix(body, " logged in"):
		action = "login"
		body = strings.TrimSuffix(body, " logged in")
	case strings.HasSuffix(body, " logged out"):
		action = "logout"
		body = strings.TrimSuffix(body, " logged out")
	default:
		return "", "", false
	}
	if !strings.HasSuffix(body, ")") {
		return "", "", false
	}
	body = strings.TrimSuffix(body, ")")
	open := strings.LastIndex(body, " (")
	if open < 0 {
		return "", "", false
	}
	playerID := body[open+2:]
	if !validMordhauPlayerID(playerID) {
		return "", "", false
	}
	return playerID, action, true
}

func (visibility *rconHistoryVisibility) retain(event RCONEvent) bool {
	if playerID, action, ok := storedRCONPlayerLifecycle(event); ok {
		switch action {
		case "login":
			if len(visibility.activePlayers) == 0 {
				visibility.emptyLeavingMapShown = false
			}
			visibility.activePlayers[playerID] = struct{}{}
		case "logout":
			delete(visibility.activePlayers, playerID)
			if len(visibility.activePlayers) == 0 {
				visibility.emptyLeavingMapShown = false
			}
		}
		return true
	}
	if event.Kind != "matchstate" || len(visibility.activePlayers) > 0 {
		return true
	}
	switch event.Text {
	case matchStateWaitingToStartText:
		return false
	case matchStateLeavingMapText:
		if visibility.emptyLeavingMapShown {
			return false
		}
		visibility.emptyLeavingMapShown = true
	}
	return true
}

func (m *Manager) rconEventLogFilePath() string {
	if m.rconLogPath != "" {
		return m.rconLogPath
	}
	return rconEventLogPath
}

func validStoredRCONEvent(event RCONEvent, previousSequence uint64) bool {
	return event.Sequence > previousSequence &&
		!event.Time.IsZero() &&
		strings.TrimSpace(event.Kind) != "" &&
		strings.TrimSpace(event.Text) != ""
}

func retainRCONEvent(events []RCONEvent, event RCONEvent) []RCONEvent {
	events = append(events, event)
	if len(events) > rconMemoryMaximum {
		events = append([]RCONEvent(nil), events[len(events)-rconMemoryRetain:]...)
	}
	return events
}

func compactRCONLoginTimestamp(text string) string {
	const prefix = "Login: "
	if !strings.HasPrefix(text, prefix) {
		return text
	}
	body := strings.TrimPrefix(text, prefix)
	separator := strings.Index(body, ": ")
	if separator < 0 {
		return text
	}
	if _, err := time.Parse("2006.01.02-15.04.05", body[:separator]); err != nil {
		return text
	}
	return prefix + body[separator+2:]
}

func (m *Manager) fleetRCONServerLabel() (string, bool) {
	settings := m.currentFleetSettings()
	if settings.Role != FleetRoleController &&
		settings.Role != FleetRoleManaged {
		return "", false
	}
	if _, err := normalizeFleetAlias(settings.Alias); err != nil {
		return "", false
	}
	return fleetServerLabel(settings.Alias), true
}

func rconEventForFleetView(event RCONEvent, localLabel string) RCONEvent {
	if event.Kind == "login" {
		event.Text = compactRCONLoginTimestamp(event.Text)
	}
	if localLabel == "" || event.Kind == "fleet" {
		return event
	}
	event.Text = "(" + localLabel + ") " + event.Text
	return event
}

func (m *Manager) rconEventsForView(events []RCONEvent) []RCONEvent {
	localLabel, fleetEnabled := m.fleetRCONServerLabel()
	if !fleetEnabled {
		return events
	}
	for index := range events {
		events[index] = rconEventForFleetView(events[index], localLabel)
	}
	return events
}

func (m *Manager) loadRCONEventLog() error {
	path := m.rconEventLogFilePath()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open RCON event log: %w", err)
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("protect RCON event log: %w", err)
	}

	var events []RCONEvent
	var sequence uint64
	visibility := newRCONHistoryVisibility()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event RCONEvent
		if err := json.Unmarshal(line, &event); err != nil ||
			!validStoredRCONEvent(event, sequence) {
			continue
		}
		sequence = event.Sequence
		if isRCONTransportStatusEvent(event.Kind, event.Text) {
			continue
		}
		if !visibility.retain(event) {
			continue
		}
		events = retainRCONEvent(events, event)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read RCON event log: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect RCON event log: %w", err)
	}
	if info.Size() > 0 {
		var final [1]byte
		if _, err := file.ReadAt(final[:], info.Size()-1); err != nil {
			return fmt.Errorf("inspect final RCON event log byte: %w", err)
		}
		if final[0] != '\n' {
			if _, err := file.WriteAt([]byte{'\n'}, info.Size()); err != nil {
				return fmt.Errorf("repair final RCON event log record: %w", err)
			}
		}
	}

	m.rconMu.Lock()
	m.rconEvents = events
	m.rconSequence = sequence
	m.emptyLeavingMapShown = visibility.emptyLeavingMapShown
	m.rconMu.Unlock()
	return nil
}

func (m *Manager) persistedEmptyLeavingMapShown() bool {
	m.rconMu.RLock()
	defer m.rconMu.RUnlock()
	return m.emptyLeavingMapShown
}

func (m *Manager) appendRCONEventLog(event RCONEvent) error {
	if m.rconLogPath == "" {
		return nil
	}
	m.rconLogMu.Lock()
	defer m.rconLogMu.Unlock()

	if err := m.rotateManagedLog(m.rconEventLogFilePath()); err != nil {
		return err
	}
	file, err := os.OpenFile(
		m.rconEventLogFilePath(),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0600,
	)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(event)
}

func (m *Manager) rconHistory(limit int) []RCONEvent {
	if limit < 1 || limit > rconBrowserHistoryLimit {
		limit = rconBrowserHistoryLimit
	}
	m.rconMu.RLock()
	start := len(m.rconEvents) - limit
	if start < 0 {
		start = 0
	}
	events := append([]RCONEvent(nil), m.rconEvents[start:]...)
	m.rconMu.RUnlock()
	return m.rconEventsForView(events)
}
