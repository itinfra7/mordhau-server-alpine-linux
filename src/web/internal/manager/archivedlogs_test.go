package manager

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeXZTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz is unavailable")
	}
	input := filepath.Join(t.TempDir(), "input.log")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("xz", "-9e", "-T1", "-c", "--", input)
	compressed, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compressed, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestArchivedGameLogNames(t *testing.T) {
	for _, name := range []string{
		"Mordhau_2026-07-28_22-00-19.log",
		"Mordhau_2026-07-28_22-00-19.log.xz",
	} {
		canonical, valid := canonicalArchivedGameLogName(name)
		if !valid || canonical != "Mordhau_2026-07-28_22-00-19.log" {
			t.Fatalf("archive name %q = %q, %t", name, canonical, valid)
		}
	}
	for _, name := range []string{
		"Mordhau.log",
		"Other_2026-07-28.log.xz",
		"Mordhau_2026-07-28.log.gz",
		"../Mordhau_2026-07-28.log.xz",
	} {
		if archivedGameLogName(name) {
			t.Fatalf("invalid archive name %q was accepted", name)
		}
	}
}

func TestOpenGameLogReaderStreamsXZWithoutExtraction(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "Mordhau_2026-07-28_22-00-19.log.xz")
	expected := []byte(strings.Repeat("repeated game-log record\n", 128))
	writeXZTestFile(t, path, expected)

	reader, err := openGameLogReader(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	actual, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if string(actual) != string(expected) {
		t.Fatal("streamed XZ content differs from the source")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("unexpected extracted files: %#v", entries)
	}
}

func TestGameLogSearchReadsCurrentAndCompressedArchives(t *testing.T) {
	directory := t.TempDir()
	archiveDirectory := filepath.Join(directory, "archive")
	currentDirectory := filepath.Join(directory, "current")
	if err := os.MkdirAll(archiveDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(currentDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	archiveName := "Mordhau_2026-07-28_22-00-19.log.xz"
	archiveLine := "[2026.07.28-21.45.00:123][100]LogDread: compressed marker\n"
	writeXZTestFile(
		t,
		filepath.Join(archiveDirectory, archiveName),
		[]byte(archiveLine),
	)
	currentPath := filepath.Join(currentDirectory, "Mordhau.log")
	if err := os.WriteFile(
		currentPath,
		[]byte("[2026.07.29-12.00.00:000][100]LogGame: current marker\n"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		playerArchiveDirectory: archiveDirectory,
		playerCurrentLogFile:   currentPath,
		monitoringSettings:     defaultMonitoringSettings(),
	}
	view, err := manager.searchManagedLogs(context.Background(), logSearchQuery{
		Source: "game",
		Query:  "compressed marker",
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Truncated || len(view.Records) != 1 {
		t.Fatalf("game-log search = %+v", view)
	}
	record := view.Records[0]
	if record.Kind != "LogDread" ||
		record.Text != strings.TrimSuffix(archiveLine, "\n") ||
		record.Details["file"] != archiveName {
		t.Fatalf("compressed game-log record = %+v", record)
	}
}

func TestPlayerHistoryDoesNotReimportArchiveAfterXZConversion(t *testing.T) {
	directory := t.TempDir()
	archiveDirectory := filepath.Join(directory, "archive")
	if err := os.MkdirAll(archiveDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 28, 20, 0, 0, 0, time.Local)
	data := []byte(strings.Join(
		testPlayerLogLines(
			testPlayerID,
			"Compressed",
			"198.51.100.20",
			start,
			time.Minute,
		),
		"\n",
	) + "\n")
	rawName := "Mordhau_2026-07-28_20-01-00.log"
	rawPath := filepath.Join(archiveDirectory, rawName)
	if err := os.WriteFile(rawPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	historyPath := filepath.Join(directory, "players.json")
	newManager := func() *Manager {
		return &Manager{
			playerHistoryFile:      historyPath,
			playerArchiveDirectory: archiveDirectory,
			playerCurrentLogFile:   filepath.Join(directory, "missing.log"),
			playerServerProcess:    func() (int, bool) { return 0, false },
		}
	}
	first := newManager()
	if err := first.loadOrCreatePlayerHistory(); err != nil {
		t.Fatal(err)
	}
	detail, err := first.playerDetail(testPlayerID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.TotalSeconds != 60 {
		t.Fatalf("initial imported duration = %d", detail.TotalSeconds)
	}

	compressedPath := rawPath + ".xz"
	writeXZTestFile(t, compressedPath, data)
	if err := os.Remove(rawPath); err != nil {
		t.Fatal(err)
	}
	second := newManager()
	if err := second.loadOrCreatePlayerHistory(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := second.playerDetail(testPlayerID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TotalSeconds != 60 ||
		len(second.playerHistory.ImportedLogs) != 1 ||
		second.playerHistory.ImportedLogs[0].Name != rawName {
		t.Fatalf("history after XZ conversion = %+v", second.playerHistory)
	}
}
