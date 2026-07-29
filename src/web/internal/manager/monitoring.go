package manager

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	monitoringSettingsVersion = 1
	metricsRetention          = 7 * 24 * time.Hour
	metricsMaximumPoints      = 7*24*60 + 120
	metricsCompactEvery       = 60
	minimumLogSizeMiB         = 1
	maximumLogSizeMiB         = 1024
	maximumLogBackups         = 30
	maximumArchiveDays        = 3650
	monitoringAlertCooldown   = 6 * time.Hour
)

type monitoringSettingsFile struct {
	Version                int    `json:"version"`
	LogSizeMiB             int    `json:"log_size_mib"`
	LogBackups             int    `json:"log_backups"`
	GameLogRetentionDays   int    `json:"game_log_retention_days"`
	WebhookURL             string `json:"webhook_url,omitempty"`
	AlertCrash             bool   `json:"alert_crash"`
	AlertRecoveryExhausted bool   `json:"alert_recovery_exhausted"`
	AlertDisk              bool   `json:"alert_disk"`
	AlertModRefresh        bool   `json:"alert_mod_refresh"`
	DiskThresholdPercent   int    `json:"disk_threshold_percent"`
}

type MonitoringSettingsView struct {
	LogSizeMiB             int  `json:"log_size_mib"`
	LogBackups             int  `json:"log_backups"`
	GameLogRetentionDays   int  `json:"game_log_retention_days"`
	WebhookConfigured      bool `json:"webhook_configured"`
	AlertCrash             bool `json:"alert_crash"`
	AlertRecoveryExhausted bool `json:"alert_recovery_exhausted"`
	AlertDisk              bool `json:"alert_disk"`
	AlertModRefresh        bool `json:"alert_mod_refresh"`
	DiskThresholdPercent   int  `json:"disk_threshold_percent"`
}

type MonitoringStatus struct {
	LastWebhookAttempt *time.Time `json:"last_webhook_attempt,omitempty"`
	LastWebhookSuccess *time.Time `json:"last_webhook_success,omitempty"`
	LastWebhookError   string     `json:"last_webhook_error,omitempty"`
}

type MonitoringView struct {
	Settings MonitoringSettingsView `json:"settings"`
	Status   MonitoringStatus       `json:"status"`
}

type monitoringSettingsRequest struct {
	LogSizeMiB             *int    `json:"log_size_mib"`
	LogBackups             *int    `json:"log_backups"`
	GameLogRetentionDays   *int    `json:"game_log_retention_days"`
	WebhookURL             *string `json:"webhook_url"`
	ClearWebhook           bool    `json:"clear_webhook"`
	AlertCrash             *bool   `json:"alert_crash"`
	AlertRecoveryExhausted *bool   `json:"alert_recovery_exhausted"`
	AlertDisk              *bool   `json:"alert_disk"`
	AlertModRefresh        *bool   `json:"alert_mod_refresh"`
	DiskThresholdPercent   *int    `json:"disk_threshold_percent"`
}

type monitoringAlert struct {
	Event   string            `json:"event"`
	Time    time.Time         `json:"time"`
	Details map[string]string `json:"details,omitempty"`
}

type monitoringWebhookPayload struct {
	Event   string            `json:"event"`
	Time    time.Time         `json:"time"`
	Details map[string]string `json:"details,omitempty"`
	Text    string            `json:"text"`
	Content string            `json:"content"`
}

type MetricsHistoryView struct {
	Range  string               `json:"range"`
	Points []MetricHistoryPoint `json:"points"`
}

func defaultMonitoringSettings() monitoringSettingsFile {
	return monitoringSettingsFile{
		Version:                monitoringSettingsVersion,
		LogSizeMiB:             10,
		LogBackups:             5,
		GameLogRetentionDays:   30,
		AlertCrash:             false,
		AlertRecoveryExhausted: false,
		AlertDisk:              false,
		AlertModRefresh:        false,
		DiskThresholdPercent:   90,
	}
}

func validMonitoringSettings(settings monitoringSettingsFile) bool {
	return settings.Version == monitoringSettingsVersion &&
		settings.LogSizeMiB >= minimumLogSizeMiB &&
		settings.LogSizeMiB <= maximumLogSizeMiB &&
		settings.LogBackups >= 0 &&
		settings.LogBackups <= maximumLogBackups &&
		settings.GameLogRetentionDays >= 0 &&
		settings.GameLogRetentionDays <= maximumArchiveDays &&
		settings.DiskThresholdPercent >= 50 &&
		settings.DiskThresholdPercent <= 100 &&
		(settings.WebhookURL == "" || validWebhookURLSyntax(settings.WebhookURL))
}

func validWebhookURLSyntax(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil &&
		parsed.Scheme == "https" &&
		parsed.Host != "" &&
		parsed.Hostname() != "" &&
		parsed.User == nil &&
		parsed.Fragment == "" &&
		len(value) <= 2048
}

func (m *Manager) monitoringSettingsPath() string {
	if m.monitoringSettingsFile != "" {
		return m.monitoringSettingsFile
	}
	return monitoringSettingsPath
}

func (m *Manager) metricHistoryPath() string {
	if m.metricsHistoryFile != "" {
		return m.metricsHistoryFile
	}
	return metricsHistoryPath
}

func (m *Manager) monitoringTime() time.Time {
	if m.monitoringNow != nil {
		return m.monitoringNow()
	}
	return time.Now()
}

func validMetricHistoryPoint(point MetricHistoryPoint) bool {
	return !point.Time.IsZero() &&
		point.CPUPercent >= 0 && point.CPUPercent <= 100 &&
		point.MemoryPct >= 0 && point.MemoryPct <= 100 &&
		point.SwapPct >= 0 && point.SwapPct <= 100 &&
		point.DiskPct >= 0 && point.DiskPct <= 100 &&
		point.PlayerCount >= 0 && point.PlayerCount <= 1024
}

func readMetricHistory(path string, now time.Time) ([]MetricHistoryPoint, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []MetricHistoryPoint{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	cutoff := now.Add(-metricsRetention)
	points := make([]MetricHistoryPoint, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	for scanner.Scan() {
		var point MetricHistoryPoint
		if json.Unmarshal(scanner.Bytes(), &point) != nil ||
			!validMetricHistoryPoint(point) ||
			point.Time.Before(cutoff) ||
			point.Time.After(now.Add(5*time.Minute)) {
			continue
		}
		points = append(points, point)
		if len(points) > metricsMaximumPoints {
			points = append([]MetricHistoryPoint(nil),
				points[len(points)-metricsMaximumPoints:]...)
		}
	}
	return points, scanner.Err()
}

func writeMetricHistory(path string, points []MetricHistoryPoint) error {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	for _, point := range points {
		if err := encoder.Encode(point); err != nil {
			return err
		}
	}
	return writeFileAtomic(path, output.Bytes(), 0600)
}

func (m *Manager) loadOrCreateMonitoring() error {
	settings := defaultMonitoringSettings()
	if err := readJSON(m.monitoringSettingsPath(), &settings); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load monitoring settings: %w", err)
		}
		if err := writeJSONAtomic(m.monitoringSettingsPath(), settings, 0600); err != nil {
			return fmt.Errorf("create monitoring settings: %w", err)
		}
	} else if !validMonitoringSettings(settings) {
		return errors.New("stored monitoring settings are invalid")
	} else if err := os.Chmod(m.monitoringSettingsPath(), 0600); err != nil {
		return err
	}
	now := m.monitoringTime()
	points, err := readMetricHistory(m.metricHistoryPath(), now)
	if err != nil {
		return fmt.Errorf("load metrics history: %w", err)
	}
	if err := writeMetricHistory(m.metricHistoryPath(), points); err != nil {
		return fmt.Errorf("initialize metrics history: %w", err)
	}
	m.monitoringSettings = settings
	m.metricHistory = points
	return nil
}

func monitoringSettingsView(
	settings monitoringSettingsFile,
) MonitoringSettingsView {
	return MonitoringSettingsView{
		LogSizeMiB:             settings.LogSizeMiB,
		LogBackups:             settings.LogBackups,
		GameLogRetentionDays:   settings.GameLogRetentionDays,
		WebhookConfigured:      settings.WebhookURL != "",
		AlertCrash:             settings.AlertCrash,
		AlertRecoveryExhausted: settings.AlertRecoveryExhausted,
		AlertDisk:              settings.AlertDisk,
		AlertModRefresh:        settings.AlertModRefresh,
		DiskThresholdPercent:   settings.DiskThresholdPercent,
	}
}

func (m *Manager) monitoringView() MonitoringView {
	m.monitoringMu.RLock()
	defer m.monitoringMu.RUnlock()
	return MonitoringView{
		Settings: monitoringSettingsView(m.monitoringSettings),
		Status:   m.monitoringStatus,
	}
}

func (m *Manager) setMonitoringSettings(
	change monitoringSettingsRequest,
) (MonitoringView, error) {
	m.monitoringMu.Lock()
	settings := m.monitoringSettings
	if change.LogSizeMiB != nil {
		settings.LogSizeMiB = *change.LogSizeMiB
	}
	if change.LogBackups != nil {
		settings.LogBackups = *change.LogBackups
	}
	if change.GameLogRetentionDays != nil {
		settings.GameLogRetentionDays = *change.GameLogRetentionDays
	}
	if change.ClearWebhook {
		settings.WebhookURL = ""
	} else if change.WebhookURL != nil && strings.TrimSpace(*change.WebhookURL) != "" {
		settings.WebhookURL = strings.TrimSpace(*change.WebhookURL)
	}
	if change.AlertCrash != nil {
		settings.AlertCrash = *change.AlertCrash
	}
	if change.AlertRecoveryExhausted != nil {
		settings.AlertRecoveryExhausted = *change.AlertRecoveryExhausted
	}
	if change.AlertDisk != nil {
		settings.AlertDisk = *change.AlertDisk
	}
	if change.AlertModRefresh != nil {
		settings.AlertModRefresh = *change.AlertModRefresh
	}
	if change.DiskThresholdPercent != nil {
		settings.DiskThresholdPercent = *change.DiskThresholdPercent
	}
	settings.Version = monitoringSettingsVersion
	if !validMonitoringSettings(settings) {
		m.monitoringMu.Unlock()
		return MonitoringView{}, errors.New("invalid monitoring settings")
	}
	if err := writeJSONAtomic(m.monitoringSettingsPath(), settings, 0600); err != nil {
		m.monitoringMu.Unlock()
		return MonitoringView{}, err
	}
	m.monitoringSettings = settings
	view := MonitoringView{
		Settings: monitoringSettingsView(settings),
		Status:   m.monitoringStatus,
	}
	m.monitoringMu.Unlock()
	m.pruneManagedLogs(m.monitoringTime())
	return view, nil
}

func (m *Manager) appendMetricHistory(metrics Metrics) {
	runtime := m.runtimeSummaryView()
	players := 0
	if runtime.Ready {
		players = runtime.PlayerControllerCount
	}
	point := MetricHistoryPoint{
		Time:        metrics.SampledAt,
		CPUPercent:  metrics.CPUPercent,
		MemoryPct:   metrics.Memory.Pct,
		SwapPct:     metrics.Swap.Pct,
		DiskPct:     metrics.Disk.Pct,
		PlayerCount: players,
	}
	if !validMetricHistoryPoint(point) {
		return
	}
	m.monitoringMu.Lock()
	cutoff := point.Time.Add(-metricsRetention)
	start := 0
	for start < len(m.metricHistory) &&
		m.metricHistory[start].Time.Before(cutoff) {
		start++
	}
	if start > 0 {
		m.metricHistory = append(
			[]MetricHistoryPoint(nil),
			m.metricHistory[start:]...,
		)
	}
	m.metricHistory = append(m.metricHistory, point)
	m.metricWritesSinceCompact++
	compact := m.metricWritesSinceCompact >= metricsCompactEvery
	if compact {
		m.metricWritesSinceCompact = 0
	}
	points := append([]MetricHistoryPoint(nil), m.metricHistory...)
	path := m.metricHistoryPath()
	m.monitoringMu.Unlock()

	var err error
	if compact {
		err = writeMetricHistory(path, points)
	} else {
		var data bytes.Buffer
		encoder := json.NewEncoder(&data)
		encoder.SetEscapeHTML(false)
		if encodeErr := encoder.Encode(point); encodeErr != nil {
			return
		}
		file, openErr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if openErr != nil {
			return
		}
		_, err = file.Write(data.Bytes())
		if err == nil {
			err = file.Chmod(0600)
		}
		_ = file.Close()
	}
	if err != nil {
		m.auditActorEvent("system", "local", "metrics_history_write_failed",
			map[string]string{"error": safeAuditText(err.Error(), 160)})
	}
	m.evaluateDiskAlert(point)
}

func (m *Manager) metricsHistoryView(rangeName string) MetricsHistoryView {
	duration := 24 * time.Hour
	if rangeName == "7d" {
		duration = metricsRetention
	} else {
		rangeName = "24h"
	}
	cutoff := m.monitoringTime().Add(-duration)
	m.monitoringMu.RLock()
	points := make([]MetricHistoryPoint, 0)
	for _, point := range m.metricHistory {
		if !point.Time.Before(cutoff) {
			points = append(points, point)
		}
	}
	m.monitoringMu.RUnlock()
	if rangeName == "7d" && len(points) > 2200 {
		step := (len(points) + 2199) / 2200
		reduced := make([]MetricHistoryPoint, 0, len(points)/step+1)
		for index := 0; index < len(points); index += step {
			reduced = append(reduced, points[index])
		}
		if len(reduced) == 0 ||
			!reduced[len(reduced)-1].Time.Equal(points[len(points)-1].Time) {
			reduced = append(reduced, points[len(points)-1])
		}
		points = reduced
	}
	return MetricsHistoryView{Range: rangeName, Points: points}
}

func (m *Manager) managedLogSettings() monitoringSettingsFile {
	m.monitoringMu.RLock()
	settings := m.monitoringSettings
	m.monitoringMu.RUnlock()
	if !validMonitoringSettings(settings) {
		settings = defaultMonitoringSettings()
	}
	return settings
}

func rotateLogFile(path string, backups int) error {
	if backups < 1 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	oldest := path + "." + strconv.Itoa(backups)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := backups - 1; index >= 1; index-- {
		oldPath := path + "." + strconv.Itoa(index)
		newPath := path + "." + strconv.Itoa(index+1)
		if err := os.Rename(oldPath, newPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(path, path+".1"); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *Manager) rotateManagedLog(path string) error {
	settings := m.managedLogSettings()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < int64(settings.LogSizeMiB)<<20 {
		return nil
	}
	return rotateLogFile(path, settings.LogBackups)
}

func (m *Manager) pruneManagedLogs(now time.Time) {
	settings := m.managedLogSettings()
	for _, path := range []string{
		m.auditPath,
		m.rconEventLogFilePath(),
	} {
		if path == "" {
			continue
		}
		for index := settings.LogBackups + 1; index <= maximumLogBackups+1; index++ {
			_ = os.Remove(path + "." + strconv.Itoa(index))
		}
	}
	if settings.GameLogRetentionDays == 0 {
		return
	}
	cutoff := now.Add(
		-time.Duration(settings.GameLogRetentionDays) * 24 * time.Hour,
	)
	archiveDir := m.playerArchiveDirectory
	if archiveDir == "" {
		archiveDir = logDir
	}
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() ||
			!archivedGameLogName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(archiveDir, entry.Name()))
		}
	}
}

func alertEnabled(settings monitoringSettingsFile, event string) bool {
	switch event {
	case "server_crash":
		return settings.AlertCrash
	case "recovery_exhausted":
		return settings.AlertRecoveryExhausted
	case "disk_threshold":
		return settings.AlertDisk
	case "mod_refresh_failed":
		return settings.AlertModRefresh
	default:
		return false
	}
}

func (m *Manager) queueMonitoringAlert(
	event string,
	details map[string]string,
) {
	m.monitoringMu.Lock()
	settings := m.monitoringSettings
	last := m.monitoringAlertLast[event]
	now := m.monitoringTime()
	if settings.WebhookURL == "" || !alertEnabled(settings, event) ||
		(!last.IsZero() && now.Sub(last) < monitoringAlertCooldown) {
		m.monitoringMu.Unlock()
		return
	}
	if m.monitoringAlertLast == nil {
		m.monitoringAlertLast = make(map[string]time.Time)
	}
	m.monitoringAlertLast[event] = now
	m.monitoringMu.Unlock()
	alert := monitoringAlert{
		Event:   event,
		Time:    now,
		Details: safeAuditDetails(details),
	}
	select {
	case m.monitoringAlertQueue <- alert:
	default:
		m.auditActorEvent("system", "local", "webhook_alert_dropped",
			map[string]string{"event": event})
	}
}

func (m *Manager) evaluateDiskAlert(point MetricHistoryPoint) {
	settings := m.managedLogSettings()
	if point.DiskPct >= float64(settings.DiskThresholdPercent) {
		m.queueMonitoringAlert("disk_threshold", map[string]string{
			"disk_percent": fmt.Sprintf("%.1f", point.DiskPct),
			"threshold":    strconv.Itoa(settings.DiskThresholdPercent),
		})
	}
}

func publicWebhookAddresses(
	ctx context.Context,
	host string,
) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		literal = literal.Unmap()
		if !safeWebhookAddress(literal) {
			return nil, errors.New("webhook host is not a public address")
		}
		return []netip.Addr{literal}, nil
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !safeWebhookAddress(address) {
			return nil, errors.New("webhook hostname resolves to a non-public address")
		}
		out = append(out, address)
	}
	if len(out) == 0 {
		return nil, errors.New("webhook hostname has no usable address")
	}
	return out, nil
}

func safeWebhookAddress(address netip.Addr) bool {
	if !address.IsValid() ||
		!address.IsGlobalUnicast() ||
		address.IsPrivate() ||
		address.IsLoopback() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified() {
		return false
	}
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001:2::/48"),
		netip.MustParsePrefix("2001:db8::/32"),
	} {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func monitoringAlertText(alert monitoringAlert) string {
	text := "MORDHAU Control: " + strings.ReplaceAll(alert.Event, "_", " ")
	if len(alert.Details) == 0 {
		return text
	}
	keys := make([]string, 0, len(alert.Details))
	for key := range alert.Details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+alert.Details[key])
	}
	return text + " (" + strings.Join(parts, ", ") + ")"
}

func sendMonitoringWebhook(
	ctx context.Context,
	rawURL string,
	alert monitoringAlert,
) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || !validWebhookURLSyntax(rawURL) {
		return errors.New("invalid HTTPS webhook URL")
	}
	addresses, err := publicWebhookAddresses(ctx, parsed.Hostname())
	if err != nil {
		return err
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	text := monitoringAlertText(alert)
	payload, err := json.Marshal(monitoringWebhookPayload{
		Event:   alert.Event,
		Time:    alert.Time,
		Details: alert.Details,
		Text:    text,
		Content: text,
	})
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: parsed.Hostname(),
		},
		DialContext: func(
			dialContext context.Context,
			network string,
			_ string,
		) (net.Conn, error) {
			var lastErr error
			for _, address := range addresses {
				target := net.JoinHostPort(address.String(), port)
				connection, dialErr := dialer.DialContext(
					dialContext,
					"tcp",
					target,
				)
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   12 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("webhook redirects are not allowed")
		},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		rawURL,
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "mordhau-control-webhook/1")
	response, err := client.Do(request)
	if err != nil {
		return errors.New("webhook delivery failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (m *Manager) deliverMonitoringAlert(alert monitoringAlert) {
	m.monitoringMu.RLock()
	rawURL := m.monitoringSettings.WebhookURL
	m.monitoringMu.RUnlock()
	if rawURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err := sendMonitoringWebhook(ctx, rawURL, alert)
	cancel()
	now := m.monitoringTime()
	m.monitoringMu.Lock()
	m.monitoringStatus.LastWebhookAttempt = timeView(now)
	if err != nil {
		m.monitoringStatus.LastWebhookError = safeAuditText(err.Error(), 300)
	} else {
		m.monitoringStatus.LastWebhookSuccess = timeView(now)
		m.monitoringStatus.LastWebhookError = ""
		m.monitoringAlertLast[alert.Event] = now
	}
	m.monitoringMu.Unlock()
	if err != nil {
		m.auditActorEvent("system", "local", "webhook_alert_failed",
			map[string]string{
				"event": alert.Event,
				"error": safeAuditText(err.Error(), 160),
			})
	} else {
		m.auditActorEvent("system", "local", "webhook_alert_sent",
			map[string]string{"event": alert.Event})
	}
}

func (m *Manager) monitoringAlertLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-m.monitoringAlertQueue:
			m.deliverMonitoringAlert(alert)
		}
	}
}

func (m *Manager) testMonitoringWebhook() error {
	m.monitoringMu.RLock()
	rawURL := m.monitoringSettings.WebhookURL
	m.monitoringMu.RUnlock()
	if rawURL == "" {
		return errors.New("no webhook URL is configured")
	}
	alert := monitoringAlert{
		Event: "test",
		Time:  m.monitoringTime(),
		Details: map[string]string{
			"message": "MORDHAU Control webhook test",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err := sendMonitoringWebhook(ctx, rawURL, alert)
	cancel()
	now := m.monitoringTime()
	m.monitoringMu.Lock()
	m.monitoringStatus.LastWebhookAttempt = timeView(now)
	if err != nil {
		m.monitoringStatus.LastWebhookError = safeAuditText(err.Error(), 300)
	} else {
		m.monitoringStatus.LastWebhookSuccess = timeView(now)
		m.monitoringStatus.LastWebhookError = ""
	}
	m.monitoringMu.Unlock()
	return err
}
