package manager

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	rconMemoryMaximum       = 1000
	rconMemoryRetain        = 800
	rconBrowserHistoryLimit = 400
)

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
	m.rconMu.Unlock()
	return nil
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
	return events
}
