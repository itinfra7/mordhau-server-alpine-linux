package manager

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

func TestGeoIPEditionAndMonthFallbacks(t *testing.T) {
	for value, expected := range map[string]bool{
		"2026-07": true,
		"2026-7":  false,
		"2026-13": false,
		"26-07":   false,
		"":        false,
	} {
		if actual := validGeoIPEdition(value); actual != expected {
			t.Fatalf("validGeoIPEdition(%q) = %t, want %t", value, actual, expected)
		}
	}
	editions := geoIPEditions(
		time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		3,
	)
	expected := []string{"2026-01", "2025-12", "2025-11"}
	for index := range expected {
		if editions[index] != expected[index] {
			t.Fatalf("editions = %#v, want %#v", editions, expected)
		}
	}
}

func TestGeoIPLocationUsesEnglishAndDeduplicatesRegions(t *testing.T) {
	var record geoIPCityRecord
	record.Country.ISOCode = "kr"
	record.Country.Names = map[string]string{
		"en": "South Korea",
		"ko": "대한민국",
	}
	record.City.Names = map[string]string{"en": "Seoul"}
	record.Subdivisions = append(record.Subdivisions,
		struct {
			Names map[string]string `maxminddb:"names"`
		}{Names: map[string]string{"en": "Seoul"}},
		struct {
			Names map[string]string `maxminddb:"names"`
		}{Names: map[string]string{"en": "seoul"}},
	)

	location := geoIPLocationFromRecord(record)
	if location == nil ||
		location.CountryCode != "KR" ||
		location.CountryName != "South Korea" ||
		location.Region != "Seoul" ||
		location.City != "Seoul" {
		t.Fatalf("location = %+v", location)
	}
}

func TestGeoIPRejectsInvalidNamesMetadataAndPrivateAddresses(t *testing.T) {
	if value := normalizeGeoLocationName("bad\nname"); value != "" {
		t.Fatalf("control-character name = %q", value)
	}
	if validGeoIPMetadata(maxminddb.Metadata{
		BinaryFormatMajorVersion: 2,
		IPVersion:                4,
		DatabaseType:             "DBIP-City-Lite",
	}) {
		t.Fatal("IPv4-only database was accepted")
	}
	manager := &Manager{}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "::1", "fd00::1"} {
		if location := manager.playerLocationForAddress(address); location != nil {
			t.Fatalf("private address %s returned %+v", address, location)
		}
	}
}

func TestGeoIPIgnoredNetworksAreCanonicalAndDeduplicated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ignore-networks")
	if err := os.WriteFile(
		path,
		[]byte("# Ignored benchmark range\n198.18.1.1/15\n198.18.0.0/15\n::ffff:192.0.2.1/128\n"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	prefixes, err := loadGeoIPIgnoredPrefixes(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"198.18.0.0/15", "192.0.2.1/32"}
	if len(prefixes) != len(expected) {
		t.Fatalf("prefixes = %#v", prefixes)
	}
	for index := range expected {
		if prefixes[index].String() != expected[index] {
			t.Fatalf("prefixes = %#v, want %#v", prefixes, expected)
		}
	}
}

func TestGeoIPDownloadReportsUnavailableEdition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/dbip-city-lite-2026-07.mmdb.gz" {
			t.Fatalf("download path = %q", request.URL.Path)
		}
		http.NotFound(response, request)
	}))
	defer server.Close()

	directory := t.TempDir()
	manager := &Manager{
		geoIPDatabaseFile:    filepath.Join(directory, "city.mmdb"),
		geoIPStateFile:       filepath.Join(directory, "state.json"),
		geoIPDownloadBaseURL: server.URL,
		geoIPHTTPClient:      server.Client(),
		geoIPNow: func() time.Time {
			return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
		},
	}
	err := manager.downloadGeoIPEdition(context.Background(), "2026-07")
	if !errors.Is(err, errGeoIPEditionUnavailable) {
		t.Fatalf("download error = %v", err)
	}
}
