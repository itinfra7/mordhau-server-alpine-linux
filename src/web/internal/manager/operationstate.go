package manager

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const operationStateVersion = 1

func validStoredOperation(operation Operation) bool {
	if operation.Action == "" {
		return !operation.Running &&
			operation.StartedAt.IsZero() &&
			operation.FinishedAt.IsZero()
	}
	switch operation.Action {
	case "start", "stop", "restart", "update":
	default:
		return false
	}
	if operation.StartedAt.IsZero() {
		return false
	}
	if operation.Running {
		return operation.FinishedAt.IsZero()
	}
	return !operation.FinishedAt.IsZero()
}

func (m *Manager) operationStateFilePath() string {
	if m.operationPath != "" {
		return m.operationPath
	}
	return operationStatePath
}

func (m *Manager) saveOperationLocked() error {
	state := operationStateFile{
		Version:   operationStateVersion,
		Operation: m.op,
	}
	return writeJSONAtomic(m.operationStateFilePath(), state, 0600)
}

func (m *Manager) loadOrCreateOperationState() error {
	path := m.operationStateFilePath()
	var state operationStateFile
	if err := readJSON(path, &state); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load lifecycle operation state: %w", err)
		}
		state.Version = operationStateVersion
		if err := writeJSONAtomic(path, state, 0600); err != nil {
			return fmt.Errorf("create lifecycle operation state: %w", err)
		}
		m.op = state.Operation
		return nil
	}
	if state.Version != operationStateVersion ||
		!validStoredOperation(state.Operation) {
		return errors.New("stored lifecycle operation state is invalid")
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("protect lifecycle operation state: %w", err)
	}

	m.op = state.Operation
	if !m.op.Running {
		return nil
	}
	m.op.Running = false
	m.op.Successful = false
	m.op.FinishedAt = time.Now()
	m.op.Output = strings.TrimSpace(m.op.Output)
	if m.op.Output != "" {
		m.op.Output += "\n"
	}
	m.op.Output += "The web manager stopped before this operation recorded a result."
	if err := m.saveOperationLocked(); err != nil {
		return fmt.Errorf("record interrupted lifecycle operation: %w", err)
	}
	return nil
}
