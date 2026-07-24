package manager

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const auditTimeLayout = "2006-01-02T15:04:05Z07:00"

type auditRecord struct {
	Timestamp  string            `json:"timestamp"`
	Event      string            `json:"event"`
	Account    string            `json:"account"`
	ClientIP   string            `json:"client_ip,omitempty"`
	PeerIP     string            `json:"peer_ip,omitempty"`
	Method     string            `json:"method,omitempty"`
	Path       string            `json:"path,omitempty"`
	Status     int               `json:"status,omitempty"`
	Bytes      int64             `json:"bytes,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`
	Details    map[string]string `json:"details,omitempty"`
}

type auditResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (writer *auditResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *auditResponseWriter) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	written, err := writer.ResponseWriter.Write(data)
	writer.bytes += int64(written)
	return written, err
}

func (writer *auditResponseWriter) Flush() {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *auditResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (m *Manager) initializeAuditLog() error {
	path := m.auditPath
	if path == "" {
		path = webAuditLogPath
		m.auditPath = path
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (m *Manager) writeAudit(record auditRecord) error {
	if record.Timestamp == "" {
		record.Timestamp = time.Now().Format(auditTimeLayout)
	}
	record.Event = safeAuditText(record.Event, 64)
	record.Account = safeAuditAccount(record.Account)
	record.ClientIP = safeAuditText(record.ClientIP, 64)
	record.PeerIP = safeAuditText(record.PeerIP, 64)
	record.Method = safeAuditText(record.Method, 16)
	record.Path = safeAuditText(record.Path, 256)
	record.Details = safeAuditDetails(record.Details)

	path := m.auditPath
	if path == "" {
		path = webAuditLogPath
	}

	m.auditMu.Lock()
	defer m.auditMu.Unlock()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(record)
}

func (m *Manager) audit(record auditRecord) {
	if err := m.writeAudit(record); err != nil {
		log.Printf("write web audit log: %v", err)
	}
}

func (m *Manager) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		account := "unauthenticated"
		if session, ok := m.sessionForRequest(request); ok {
			account = session.Username
		}

		writer := &auditResponseWriter{ResponseWriter: response}
		next.ServeHTTP(writer, request)
		if writer.status == 0 {
			writer.status = http.StatusOK
		}

		m.audit(auditRecord{
			Timestamp:  started.Format(auditTimeLayout),
			Event:      "http_access",
			Account:    account,
			ClientIP:   auditClientIP(request),
			PeerIP:     auditPeerIP(request),
			Method:     request.Method,
			Path:       request.URL.Path,
			Status:     writer.status,
			Bytes:      writer.bytes,
			DurationMS: time.Since(started).Milliseconds(),
		})
	})
}

func (m *Manager) auditRequestEvent(
	request *http.Request,
	account string,
	event string,
	details map[string]string,
) {
	m.audit(auditRecord{
		Event:    event,
		Account:  account,
		ClientIP: auditClientIP(request),
		PeerIP:   auditPeerIP(request),
		Method:   request.Method,
		Path:     request.URL.Path,
		Details:  details,
	})
}

func (m *Manager) auditActorEvent(
	account string,
	clientIP string,
	event string,
	details map[string]string,
) {
	m.audit(auditRecord{
		Event:    event,
		Account:  account,
		ClientIP: clientIP,
		Details:  details,
	})
}

func (m *Manager) auditNetworkActorEvent(
	account string,
	clientIP string,
	peerIP string,
	event string,
	details map[string]string,
) {
	m.audit(auditRecord{
		Event:    event,
		Account:  account,
		ClientIP: clientIP,
		PeerIP:   peerIP,
		Details:  details,
	})
}

func (m *Manager) rejectInvalidRequestAddress(
	response http.ResponseWriter,
	request *http.Request,
	address requestAddress,
) {
	writer := &auditResponseWriter{ResponseWriter: response}
	message := "invalid forwarded client address"
	if address.failureReason == requestAddressInvalidPeer {
		message = "invalid client address"
	}
	http.Error(writer, message, http.StatusBadRequest)

	peerIP := ""
	if address.peerIP.IsValid() {
		peerIP = address.peerIP.String()
	}
	m.audit(auditRecord{
		Event:   "http_access",
		Account: "unauthenticated",
		PeerIP:  peerIP,
		Method:  request.Method,
		Path:    request.URL.Path,
		Status:  writer.status,
		Bytes:   writer.bytes,
		Details: map[string]string{
			"address_error": address.failureReason,
		},
	})
}

func auditClientIP(request *http.Request) string {
	ip, err := requestIP(request)
	if err != nil {
		return "unknown"
	}
	return ip.String()
}

func auditPeerIP(request *http.Request) string {
	ip, err := requestPeerIP(request)
	if err != nil {
		return "unknown"
	}
	return ip.String()
}

func safeAuditAccount(account string) string {
	account = safeAuditText(account, 64)
	if account == "" {
		return "unauthenticated"
	}
	return account
}

func safeAuditText(value string, maximumRunes int) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	if utf8.RuneCountInString(value) <= maximumRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maximumRunes])
}

func safeAuditDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return nil
	}
	safe := make(map[string]string, len(details))
	for key, value := range details {
		key = safeAuditText(key, 64)
		if key == "" {
			continue
		}
		safe[key] = safeAuditText(value, 256)
	}
	if len(safe) == 0 {
		return nil
	}
	return safe
}

func configMutationAuditDetails(mutation ConfigMutation, staged bool) map[string]string {
	details := map[string]string{
		"file":   mutation.File,
		"action": mutation.Action,
		"staged": fmt.Sprintf("%t", staged),
	}
	switch mutation.Action {
	case "set_entry", "add_entry", "remove_entry", "set_entry_enabled":
		details["key"] = mutation.Key
		details["section"] = mutation.Section
		if mutation.Action == "set_entry_enabled" {
			details["enabled"] = fmt.Sprintf("%t", mutation.Enabled)
		}
	case "add_section", "rename_section", "remove_section", "set_section_enabled":
		details["section"] = mutation.Section
		if mutation.Action == "set_section_enabled" {
			details["enabled"] = fmt.Sprintf("%t", mutation.Enabled)
		}
	}
	return details
}
