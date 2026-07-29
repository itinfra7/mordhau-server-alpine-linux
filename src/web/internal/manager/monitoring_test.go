package manager

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookAddressPolicyRejectsNonPublicAndReservedRanges(t *testing.T) {
	for _, value := range []string{
		"127.0.0.1",
		"10.0.0.1",
		"100.64.0.1",
		"169.254.169.254",
		"192.0.2.1",
		"198.18.0.1",
		"203.0.113.1",
		"::1",
		"2001:db8::1",
	} {
		if safeWebhookAddress(netip.MustParseAddr(value)) {
			t.Fatalf("reserved webhook address %s was accepted", value)
		}
	}
	for _, value := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !safeWebhookAddress(netip.MustParseAddr(value)) {
			t.Fatalf("public webhook address %s was rejected", value)
		}
	}
}

func TestMonitoringAlertCooldownIsAppliedBeforeDelivery(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	settings := defaultMonitoringSettings()
	settings.WebhookURL = "https://example.com/hook"
	settings.AlertCrash = true
	manager := &Manager{
		monitoringSettings:   settings,
		monitoringAlertQueue: make(chan monitoringAlert, 4),
		monitoringAlertLast:  make(map[string]time.Time),
		monitoringNow:        func() time.Time { return now },
	}
	manager.queueMonitoringAlert("server_crash", map[string]string{"pid": "10"})
	manager.queueMonitoringAlert("server_crash", map[string]string{"pid": "11"})
	if got := len(manager.monitoringAlertQueue); got != 1 {
		t.Fatalf("queued alerts inside cooldown = %d, want 1", got)
	}
	now = now.Add(monitoringAlertCooldown)
	manager.queueMonitoringAlert("server_crash", map[string]string{"pid": "12"})
	if got := len(manager.monitoringAlertQueue); got != 2 {
		t.Fatalf("queued alerts after cooldown = %d, want 2", got)
	}
}

func TestMetricHistoryRetentionAndManagedLogRotation(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(root, "metrics.jsonl")
	points := []MetricHistoryPoint{
		{Time: now.Add(-8 * 24 * time.Hour), CPUPercent: 1},
		{Time: now.Add(-time.Hour), CPUPercent: 2},
	}
	if err := writeMetricHistory(path, points); err != nil {
		t.Fatal(err)
	}
	retained, err := readMetricHistory(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) != 1 || retained[0].CPUPercent != 2 {
		t.Fatalf("retained metric points = %+v", retained)
	}

	logPath := filepath.Join(root, "events.log")
	if err := writeFileAtomic(logPath, []byte("current\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(logPath+".1", []byte("previous\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rotateLogFile(logPath, 2); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(logPath + ".1"); err != nil ||
		string(data) != "current\n" {
		t.Fatalf("first rotated log = %q, %v", data, err)
	}
	if data, err := os.ReadFile(logPath + ".2"); err != nil ||
		string(data) != "previous\n" {
		t.Fatalf("second rotated log = %q, %v", data, err)
	}
}
