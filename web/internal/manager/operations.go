package manager

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"time"
)

func (m *Manager) startOperation(action, username, clientIP string) error {
	switch action {
	case "start", "stop", "restart", "update":
	default:
		return errors.New("unsupported server action")
	}
	if action == "update" && serverRunning() {
		return errors.New("the server must be stopped before an update")
	}
	if action == "start" && serverRunning() {
		return errors.New("the server is already running")
	}

	m.mu.Lock()
	if m.op.Running {
		m.mu.Unlock()
		return errors.New("another server operation is already running")
	}
	m.op = Operation{
		Action:    action,
		Running:   true,
		StartedAt: time.Now(),
		Requested: username,
	}
	m.mu.Unlock()
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
		m.mu.Unlock()
		m.addRCONEvent("system", "Server operation "+result+": "+action)
		m.auditActorEvent(username, clientIP, "server_action_completed", map[string]string{
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
