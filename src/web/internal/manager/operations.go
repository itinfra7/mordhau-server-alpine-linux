package manager

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

func (m *Manager) requestOperation(action, username, clientIP, peerIP string) error {
	if m.operationStart != nil {
		return m.operationStart(action, username, clientIP, peerIP)
	}
	return m.startOperation(action, username, clientIP, peerIP)
}

func (m *Manager) startOperation(action, username, clientIP, peerIP string) error {
	switch action {
	case "start", "stop", "restart", "update", "recover":
	default:
		return errors.New("unsupported server action")
	}
	if action == "update" && serverRunning() {
		return errors.New("the server must be stopped before an update")
	}
	if (action == "start" || action == "recover") && serverRunning() {
		return errors.New("the server is already running")
	}

	m.mu.Lock()
	if m.op.Running {
		m.mu.Unlock()
		return errors.New("another server operation is already running")
	}
	previous := m.op
	m.op = Operation{
		Action:      action,
		Running:     true,
		StartedAt:   time.Now(),
		Requested:   username,
		RequestedIP: clientIP,
	}
	if err := m.saveOperationLocked(); err != nil {
		m.op = previous
		m.mu.Unlock()
		return fmt.Errorf("save lifecycle operation state: %w", err)
	}
	m.mu.Unlock()
	m.signalModRestartLoop()
	m.signalScheduledServerRestartLoop()
	m.addRCONEvent("system", "Server operation started: "+action)

	go func() {
		command := exec.Command(serverScript, action)
		var output cappedBuffer
		command.Stdout = &output
		command.Stderr = &output
		err := command.Run()

		m.mu.Lock()
		m.op.Running = false
		m.op.Successful = err == nil
		m.op.FinishedAt = time.Now()
		m.op.Output = strings.TrimSpace(output.String())
		if err != nil {
			if m.op.Output != "" {
				m.op.Output += "\n"
			}
			m.op.Output += "Error: " + err.Error()
		}
		result := "completed"
		if err != nil {
			result = "failed"
		}
		persistErr := m.saveOperationLocked()
		m.mu.Unlock()
		if persistErr != nil {
			log.Printf("save completed lifecycle operation state: %v", persistErr)
		}
		m.addRCONEvent("system", "Server operation "+result+": "+action)
		if action == "start" || action == "restart" || action == "update" {
			m.signalSteamUpdateCheck()
		}
		m.signalAutomaticUpdateLoop()
		m.signalScheduledServerRestartLoop()
		m.auditNetworkActorEvent(username, clientIP, peerIP, "server_action_completed", map[string]string{
			"action": action,
			"result": result,
		})
	}()
	return nil
}

type cappedBuffer struct {
	data bytes.Buffer
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	const maximum = 64 << 10
	original := len(data)
	if buffer.data.Len() < maximum {
		remaining := maximum - buffer.data.Len()
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.data.Write(data)
	}
	return original, nil
}

func (buffer *cappedBuffer) String() string {
	return buffer.data.String()
}
