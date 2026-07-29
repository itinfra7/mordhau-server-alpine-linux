package manager

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
		query.Source != "events" {
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

func (m *Manager) searchManagedLogs(
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
	sort.SliceStable(records, func(left, right int) bool {
		return records[left].Time.After(records[right].Time)
	})
	truncated := len(records) > query.Limit
	if truncated {
		records = records[:query.Limit]
	}
	return LogSearchView{Records: records, Truncated: truncated}, nil
}
