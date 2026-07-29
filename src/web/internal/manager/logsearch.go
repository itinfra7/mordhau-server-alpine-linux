package manager

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	logSearchDefaultLimit = 200
	logSearchMaximumLimit = 500
	logExportMaximumLimit = 10000
)

var gameLogSearchSlot = make(chan struct{}, 1)

type LogSearchRecord struct {
	Source   string            `json:"source"`
	Time     time.Time         `json:"time"`
	Event    string            `json:"event,omitempty"`
	Kind     string            `json:"kind,omitempty"`
	Account  string            `json:"account,omitempty"`
	ClientIP string            `json:"client_ip,omitempty"`
	PeerIP   string            `json:"peer_ip,omitempty"`
	Text     string            `json:"text,omitempty"`
	Details  map[string]string `json:"details,omitempty"`
}

type LogSearchView struct {
	Records   []LogSearchRecord `json:"records"`
	Truncated bool              `json:"truncated"`
}

type logSearchQuery struct {
	Source  string
	Query   string
	Account string
	Kind    string
	From    time.Time
	To      time.Time
	Limit   int
}

func parseLogSearchQuery(request *http.Request, maximum int) (logSearchQuery, error) {
	query := logSearchQuery{
		Source: strings.ToLower(strings.TrimSpace(
			request.URL.Query().Get("source"),
		)),
		Query: strings.ToLower(strings.TrimSpace(
			request.URL.Query().Get("q"),
		)),
		Account: strings.ToLower(strings.TrimSpace(
			request.URL.Query().Get("account"),
		)),
		Kind: strings.ToLower(strings.TrimSpace(
			request.URL.Query().Get("kind"),
		)),
		Limit: logSearchDefaultLimit,
	}
	if query.Source == "" {
		query.Source = "all"
	}
	if query.Source != "all" && query.Source != "audit" &&
		query.Source != "events" && query.Source != "game" {
		return logSearchQuery{}, errors.New("invalid log source")
	}
	if len(query.Query) > 256 || len(query.Account) > 64 ||
		len(query.Kind) > 64 {
		return logSearchQuery{}, errors.New("log filter is too long")
	}
	if value := request.URL.Query().Get("from"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return logSearchQuery{}, errors.New("invalid log start time")
		}
		query.From = parsed
	}
	if value := request.URL.Query().Get("to"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return logSearchQuery{}, errors.New("invalid log end time")
		}
		query.To = parsed
	}
	if !query.From.IsZero() && !query.To.IsZero() &&
		query.To.Before(query.From) {
		return logSearchQuery{}, errors.New("log end time precedes start time")
	}
	if value := request.URL.Query().Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > maximum {
			return logSearchQuery{}, fmt.Errorf(
				"log limit must be 1-%d",
				maximum,
			)
		}
		query.Limit = limit
	}
	if query.Limit > maximum {
		query.Limit = maximum
	}
	return query, nil
}

func logSearchMatches(record LogSearchRecord, query logSearchQuery) bool {
	if !query.From.IsZero() && record.Time.Before(query.From) {
		return false
	}
	if !query.To.IsZero() && record.Time.After(query.To) {
		return false
	}
	if query.Account != "" &&
		!strings.Contains(strings.ToLower(record.Account), query.Account) {
		return false
	}
	if query.Kind != "" {
		value := record.Kind
		if value == "" {
			value = record.Event
		}
		if !strings.Contains(strings.ToLower(value), query.Kind) {
			return false
		}
	}
	if query.Query == "" {
		return true
	}
	data, err := json.Marshal(record)
	return err == nil &&
		strings.Contains(strings.ToLower(string(data)), query.Query)
}

func managedLogFiles(path string, backups int) []string {
	files := make([]string, 0, backups+1)
	for index := backups; index >= 1; index-- {
		files = append(files, path+"."+strconv.Itoa(index))
	}
	files = append(files, path)
	return files
}

func scanAuditLog(
	path string,
	query logSearchQuery,
	records *[]LogSearchRecord,
	maximum int,
) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var stored auditRecord
		if json.Unmarshal(scanner.Bytes(), &stored) != nil {
			continue
		}
		eventTime, err := time.Parse(auditTimeLayout, stored.Timestamp)
		if err != nil {
			continue
		}
		record := LogSearchRecord{
			Source:   "audit",
			Time:     eventTime,
			Event:    stored.Event,
			Account:  stored.Account,
			ClientIP: stored.ClientIP,
			PeerIP:   stored.PeerIP,
			Details:  stored.Details,
		}
		if logSearchMatches(record, query) {
			*records = append(*records, record)
			if len(*records) > maximum*4 {
				sort.SliceStable(*records, func(left, right int) bool {
					return (*records)[left].Time.After((*records)[right].Time)
				})
				*records = (*records)[:maximum*2]
			}
		}
	}
	return scanner.Err()
}

func scanEventLog(
	path string,
	query logSearchQuery,
	records *[]LogSearchRecord,
	maximum int,
) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var stored RCONEvent
		if json.Unmarshal(scanner.Bytes(), &stored) != nil ||
			stored.Time.IsZero() {
			continue
		}
		record := LogSearchRecord{
			Source: "events",
			Time:   stored.Time,
			Kind:   stored.Kind,
			Text:   stored.Text,
		}
		if logSearchMatches(record, query) {
			*records = append(*records, record)
			if len(*records) > maximum*4 {
				sort.SliceStable(*records, func(left, right int) bool {
					return (*records)[left].Time.After((*records)[right].Time)
				})
				*records = (*records)[:maximum*2]
			}
		}
	}
	return scanner.Err()
}

func gameLogRecordKind(text string) string {
	separator := strings.IndexByte(text, ':')
	if separator <= 0 {
		return "game"
	}
	kind := strings.TrimSpace(text[:separator])
	if kind == "" || len(kind) > 128 {
		return "game"
	}
	return kind
}

func scanGameLog(
	ctx context.Context,
	path string,
	query logSearchQuery,
	records *[]LogSearchRecord,
	maximum int,
) error {
	reader, err := openGameLogReader(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 128<<10), playerLogScannerMaximumBytes)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		eventTime, text, valid := parseMordhauLogEnvelope(line)
		if !valid {
			continue
		}
		record := LogSearchRecord{
			Source: "game",
			Time:   eventTime,
			Kind:   gameLogRecordKind(text),
			Text:   line,
			Details: map[string]string{
				"file": filepath.Base(path),
			},
		}
		if logSearchMatches(record, query) {
			*records = append(*records, record)
			if len(*records) > maximum*4 {
				sort.SliceStable(*records, func(left, right int) bool {
					return (*records)[left].Time.After((*records)[right].Time)
				})
				*records = (*records)[:maximum*2]
			}
		}
	}
	scanErr := scanner.Err()
	closeErr := reader.Close()
	if scanErr != nil {
		return scanErr
	}
	return closeErr
}

func (m *Manager) archivedGameLogPaths() ([]string, error) {
	directory := m.playerArchivePath()
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string)
	for _, entry := range entries {
		canonical, valid := canonicalArchivedGameLogName(entry.Name())
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !valid {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		current, exists := paths[canonical]
		if !exists ||
			(strings.HasSuffix(strings.ToLower(current), ".xz") &&
				!strings.HasSuffix(strings.ToLower(path), ".xz")) {
			paths[canonical] = path
		}
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, paths[name])
	}
	return result, nil
}

func (m *Manager) searchManagedLogs(
	ctx context.Context,
	query logSearchQuery,
) (LogSearchView, error) {
	settings := m.managedLogSettings()
	records := make([]LogSearchRecord, 0, query.Limit)
	if query.Source == "all" || query.Source == "audit" {
		for _, path := range managedLogFiles(m.auditPath, settings.LogBackups) {
			if err := scanAuditLog(path, query, &records, query.Limit); err != nil {
				return LogSearchView{}, err
			}
		}
	}
	if query.Source == "all" || query.Source == "events" {
		for _, path := range managedLogFiles(
			m.rconEventLogFilePath(),
			settings.LogBackups,
		) {
			if err := scanEventLog(path, query, &records, query.Limit); err != nil {
				return LogSearchView{}, err
			}
		}
	}
	if query.Source == "game" {
		select {
		case gameLogSearchSlot <- struct{}{}:
			defer func() { <-gameLogSearchSlot }()
		case <-ctx.Done():
			return LogSearchView{}, ctx.Err()
		}
		paths := []string{m.playerCurrentLogPath()}
		archives, err := m.archivedGameLogPaths()
		if err != nil {
			return LogSearchView{}, err
		}
		paths = append(paths, archives...)
		for _, path := range paths {
			if err := scanGameLog(
				ctx,
				path,
				query,
				&records,
				query.Limit,
			); err != nil {
				return LogSearchView{}, err
			}
		}
	}
	sort.SliceStable(records, func(left, right int) bool {
		return records[left].Time.After(records[right].Time)
	})
	truncated := len(records) > query.Limit
	if truncated {
		records = records[:query.Limit]
	}
	return LogSearchView{Records: records, Truncated: truncated}, nil
}
