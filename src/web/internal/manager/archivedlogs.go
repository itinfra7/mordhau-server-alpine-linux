package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func canonicalArchivedGameLogName(name string) (string, bool) {
	if name == "" || name != filepath.Base(name) ||
		!strings.HasPrefix(name, "Mordhau_") {
		return "", false
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".log"):
		return name, true
	case strings.HasSuffix(lower, ".log.xz"):
		return name[:len(name)-len(".xz")], true
	default:
		return "", false
	}
}

func archivedGameLogName(name string) bool {
	_, valid := canonicalArchivedGameLogName(name)
	return valid
}

type xzLogReader struct {
	stdout  io.ReadCloser
	command *exec.Cmd
	stderr  bytes.Buffer
	closed  bool
}

func (reader *xzLogReader) Read(data []byte) (int, error) {
	return reader.stdout.Read(data)
}

func (reader *xzLogReader) Close() error {
	if reader.closed {
		return nil
	}
	reader.closed = true
	closeErr := reader.stdout.Close()
	waitErr := reader.command.Wait()
	if waitErr != nil {
		message := strings.TrimSpace(reader.stderr.String())
		if message == "" {
			return fmt.Errorf("decompress XZ game log: %w", waitErr)
		}
		return fmt.Errorf("decompress XZ game log: %w: %s", waitErr, message)
	}
	return closeErr
}

func openGameLogReader(
	ctx context.Context,
	path string,
) (io.ReadCloser, error) {
	if !strings.HasSuffix(strings.ToLower(path), ".xz") {
		return os.Open(path)
	}
	command := exec.CommandContext(ctx, "xz", "-dc", "--", path)
	reader := &xzLogReader{command: command}
	command.Stderr = &reader.stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open XZ game log: %w", err)
	}
	reader.stdout = stdout
	if err := command.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("XZ support is unavailable")
		}
		return nil, fmt.Errorf("start XZ game-log reader: %w", err)
	}
	return reader, nil
}
