package manager

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/oschwald/maxminddb-golang/v2"
)

const (
	geoIPDatabasePath             = geoIPDir + "/dbip-city-lite.mmdb"
	geoIPStatePath                = geoIPDir + "/state.json"
	geoIPIgnorePath               = geoIPDir + "/ignore-networks"
	defaultGeoIPDownloadBaseURL   = "https://download.db-ip.com/free"
	geoIPProviderName             = "DB-IP City Lite"
	geoIPAttributionURL           = "https://db-ip.com"
	geoIPUpdateInterval           = 24 * time.Hour
	geoIPDownloadTimeout          = 10 * time.Minute
	geoIPMaximumCompressedBytes   = int64(128 << 20)
	geoIPMaximumDatabaseBytes     = int64(512 << 20)
	geoIPStorageReserveBytes      = int64(1 << 30)
	geoIPMaximumLocationNameRunes = 160
	geoIPMaximumIgnoredPrefixes   = 256
)

var (
	errGeoIPEditionUnavailable = errors.New("GeoIP database edition is unavailable")
	defaultGeoIPHTTPClient     = &http.Client{
		Timeout: geoIPDownloadTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

type PlayerLocation struct {
	CountryCode string `json:"country_code"`
	CountryName string `json:"country_name"`
	Region      string `json:"region,omitempty"`
	City        string `json:"city,omitempty"`
}

type GeoIPStatus struct {
	Available         bool       `json:"available"`
	Provider          string     `json:"provider"`
	AttributionURL    string     `json:"attribution_url"`
	Edition           string     `json:"edition,omitempty"`
	DatabaseUpdatedAt *time.Time `json:"database_updated_at,omitempty"`
	DownloadedAt      *time.Time `json:"downloaded_at,omitempty"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	Error             string     `json:"error,omitempty"`
}

type geoIPStateFile struct {
	Version      int       `json:"version"`
	Edition      string    `json:"edition"`
	DownloadedAt time.Time `json:"downloaded_at"`
	LastChecked  time.Time `json:"last_checked_at"`
}

type geoIPCityRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Subdivisions []struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"subdivisions"`
}

func newGeoIPStatus() GeoIPStatus {
	return GeoIPStatus{
		Provider:       geoIPProviderName,
		AttributionURL: geoIPAttributionURL,
	}
}

func (m *Manager) geoIPDatabasePath() string {
	if m.geoIPDatabaseFile != "" {
		return m.geoIPDatabaseFile
	}
	return geoIPDatabasePath
}

func (m *Manager) geoIPStatePath() string {
	if m.geoIPStateFile != "" {
		return m.geoIPStateFile
	}
	return geoIPStatePath
}

func (m *Manager) geoIPIgnorePath() string {
	if m.geoIPIgnoreFile != "" {
		return m.geoIPIgnoreFile
	}
	return geoIPIgnorePath
}

func (m *Manager) geoIPDownloadBase() string {
	if m.geoIPDownloadBaseURL != "" {
		return strings.TrimRight(m.geoIPDownloadBaseURL, "/")
	}
	return defaultGeoIPDownloadBaseURL
}

func (m *Manager) geoIPClient() *http.Client {
	if m.geoIPHTTPClient != nil {
		return m.geoIPHTTPClient
	}
	return defaultGeoIPHTTPClient
}

func (m *Manager) geoIPCurrentTime() time.Time {
	if m.geoIPNow != nil {
		return m.geoIPNow()
	}
	return time.Now()
}

func validGeoIPEdition(value string) bool {
	if len(value) != len("2006-01") || value[4] != '-' {
		return false
	}
	parsed, err := time.Parse("2006-01", value)
	return err == nil && parsed.Format("2006-01") == value
}

func loadGeoIPState(path string) (geoIPStateFile, error) {
	var state geoIPStateFile
	if err := readJSON(path, &state); err != nil {
		return geoIPStateFile{}, err
	}
	if state.Version != 1 ||
		!validGeoIPEdition(state.Edition) ||
		state.DownloadedAt.IsZero() ||
		state.LastChecked.IsZero() {
		return geoIPStateFile{}, errors.New("stored GeoIP state is invalid")
	}
	if err := os.Chmod(path, 0600); err != nil {
		return geoIPStateFile{}, err
	}
	return state, nil
}

func parseGeoIPIgnoredPrefix(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Prefix{}, errors.New("GeoIP ignored network must not be empty")
	}
	if address, err := netip.ParseAddr(value); err == nil {
		if address.Zone() != "" {
			return netip.Prefix{}, errors.New("GeoIP ignored network must not use an IPv6 zone")
		}
		address = address.Unmap()
		return netip.PrefixFrom(address, address.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.IsValid() {
		return netip.Prefix{}, errors.New(
			"GeoIP ignored network must be an IP address or CIDR prefix",
		)
	}
	address := prefix.Addr()
	if address.Zone() != "" {
		return netip.Prefix{}, errors.New("GeoIP ignored network must not use an IPv6 zone")
	}
	bits := prefix.Bits()
	if address.Is4In6() {
		if bits < 96 {
			return netip.Prefix{}, errors.New(
				"IPv4-mapped GeoIP ignored networks must be /96 or narrower",
			)
		}
		address = address.Unmap()
		bits -= 96
	}
	address = address.Unmap()
	return netip.PrefixFrom(address, bits).Masked(), nil
}

func loadGeoIPIgnoredPrefixes(path string) ([]netip.Prefix, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeFileAtomic(path, nil, 0600); err != nil {
			return nil, err
		}
		return []netip.Prefix{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	prefixes := make([]netip.Prefix, 0, len(lines))
	seen := make(map[string]struct{})
	for lineNumber, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, err := parseGeoIPIgnoredPrefix(line)
		if err != nil {
			return nil, fmt.Errorf("GeoIP ignored network line %d: %w", lineNumber+1, err)
		}
		key := prefix.String()
		if _, exists := seen[key]; exists {
			continue
		}
		if len(prefixes) >= geoIPMaximumIgnoredPrefixes {
			return nil, fmt.Errorf(
				"GeoIP ignored networks exceed %d entries",
				geoIPMaximumIgnoredPrefixes,
			)
		}
		seen[key] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func validateGeoIPFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("GeoIP database is not a regular file")
	}
	if info.Size() < 1 || info.Size() > geoIPMaximumDatabaseBytes {
		return nil, fmt.Errorf("GeoIP database has an invalid size: %d bytes", info.Size())
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	return info, nil
}

func validGeoIPMetadata(metadata maxminddb.Metadata) bool {
	return metadata.BinaryFormatMajorVersion == 2 &&
		metadata.IPVersion == 6 &&
		strings.Contains(strings.ToLower(metadata.DatabaseType), "city")
}

func (m *Manager) initializeGeoIP() {
	status := newGeoIPStatus()
	ignored, ignoreErr := loadGeoIPIgnoredPrefixes(m.geoIPIgnorePath())
	m.geoIPMu.Lock()
	m.geoIPIgnoredPrefixes = ignored
	if ignoreErr != nil {
		m.geoIPIgnoreError = ignoreErr.Error()
	}
	m.geoIPMu.Unlock()
	if ignoreErr != nil {
		status.Error = ignoreErr.Error()
	}
	path := m.geoIPDatabasePath()
	if _, err := validateGeoIPFile(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) && status.Error == "" {
			status.Error = err.Error()
		}
		m.geoIPMu.Lock()
		m.geoIPStatus = status
		m.geoIPMu.Unlock()
		return
	}

	reader, err := maxminddb.Open(path)
	if err != nil {
		if status.Error == "" {
			status.Error = err.Error()
		}
		m.geoIPMu.Lock()
		m.geoIPStatus = status
		m.geoIPMu.Unlock()
		return
	}
	if !validGeoIPMetadata(reader.Metadata) {
		_ = reader.Close()
		if status.Error == "" {
			status.Error = "GeoIP database metadata is incompatible"
		}
		m.geoIPMu.Lock()
		m.geoIPStatus = status
		m.geoIPMu.Unlock()
		return
	}

	buildTime := reader.Metadata.BuildTime()
	buildEdition := buildTime.UTC().Format("2006-01")
	state, stateErr := loadGeoIPState(m.geoIPStatePath())
	status.Available = true
	status.DatabaseUpdatedAt = &buildTime
	if stateErr == nil && state.Edition == buildEdition {
		status.Edition = state.Edition
		downloadedAt := state.DownloadedAt
		lastChecked := state.LastChecked
		status.DownloadedAt = &downloadedAt
		status.LastCheckedAt = &lastChecked
	} else {
		status.Edition = buildEdition
	}
	if ignoreErr != nil {
		status.Error = ignoreErr.Error()
	}

	m.geoIPMu.Lock()
	m.geoIPReader = reader
	m.geoIPStatus = status
	m.geoIPMu.Unlock()
}

func (m *Manager) geoIPStatusView() GeoIPStatus {
	m.geoIPMu.RLock()
	defer m.geoIPMu.RUnlock()
	return m.geoIPStatus
}

func normalizeGeoLocationName(value string) string {
	if !utf8.ValidString(value) {
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > geoIPMaximumLocationNameRunes {
		return ""
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return ""
		}
	}
	return value
}

func localizedGeoLocationName(names map[string]string) string {
	if value := normalizeGeoLocationName(names["en"]); value != "" {
		return value
	}
	languages := make([]string, 0, len(names))
	for language := range names {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	for _, language := range languages {
		if value := normalizeGeoLocationName(names[language]); value != "" {
			return value
		}
	}
	return ""
}

func validCountryCode(value string) bool {
	if len(value) != 2 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func geoIPLocationFromRecord(record geoIPCityRecord) *PlayerLocation {
	countryCode := strings.ToUpper(strings.TrimSpace(record.Country.ISOCode))
	if !validCountryCode(countryCode) {
		return nil
	}
	countryName := localizedGeoLocationName(record.Country.Names)
	if countryName == "" {
		countryName = countryCode
	}
	regions := make([]string, 0, len(record.Subdivisions))
	seenRegions := make(map[string]struct{}, len(record.Subdivisions))
	for _, subdivision := range record.Subdivisions {
		region := localizedGeoLocationName(subdivision.Names)
		if region == "" {
			continue
		}
		key := strings.ToLower(region)
		if _, exists := seenRegions[key]; exists {
			continue
		}
		seenRegions[key] = struct{}{}
		regions = append(regions, region)
	}
	return &PlayerLocation{
		CountryCode: countryCode,
		CountryName: countryName,
		Region:      strings.Join(regions, " / "),
		City:        localizedGeoLocationName(record.City.Names),
	}
}

func (m *Manager) playerLocationForAddress(value string) *PlayerLocation {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return nil
	}
	address = address.Unmap()
	if !address.IsValid() ||
		!address.IsGlobalUnicast() ||
		address.IsPrivate() {
		return nil
	}

	m.geoIPMu.RLock()
	defer m.geoIPMu.RUnlock()
	for _, prefix := range m.geoIPIgnoredPrefixes {
		if prefix.Addr().Is4() == address.Is4() && prefix.Contains(address) {
			return nil
		}
	}
	if m.geoIPReader == nil {
		return nil
	}
	result := m.geoIPReader.Lookup(address)
	if result.Err() != nil || !result.Found() {
		return nil
	}
	var record geoIPCityRecord
	if err := result.Decode(&record); err != nil {
		return nil
	}
	return geoIPLocationFromRecord(record)
}

func geoIPEditions(now time.Time, count int) []string {
	if count < 1 {
		return nil
	}
	now = now.UTC()
	editions := make([]string, 0, count)
	for offset := 0; offset < count; offset++ {
		month := time.Date(now.Year(), now.Month()-time.Month(offset), 1, 0, 0, 0, 0, time.UTC)
		editions = append(editions, month.Format("2006-01"))
	}
	return editions
}

func geoIPStorageAvailable(path string, required int64) error {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return err
	}
	available := int64(stats.Bavail) * int64(stats.Bsize)
	if available < required {
		return fmt.Errorf(
			"insufficient storage for GeoIP update: %d bytes available, %d required",
			available,
			required,
		)
	}
	return nil
}

func (m *Manager) markGeoIPChecked(at time.Time, err error) {
	m.geoIPMu.Lock()
	checkedAt := at
	m.geoIPStatus.LastCheckedAt = &checkedAt
	if err != nil {
		m.geoIPStatus.Error = err.Error()
	} else if m.geoIPIgnoreError != "" {
		m.geoIPStatus.Error = m.geoIPIgnoreError
	} else {
		m.geoIPStatus.Error = ""
	}
	m.geoIPMu.Unlock()
}

func (m *Manager) installGeoIPReader(
	reader *maxminddb.Reader,
	edition string,
	downloadedAt time.Time,
) {
	buildTime := reader.Metadata.BuildTime()
	status := newGeoIPStatus()
	status.Available = true
	status.Edition = edition
	status.DatabaseUpdatedAt = &buildTime
	status.DownloadedAt = &downloadedAt
	status.LastCheckedAt = &downloadedAt

	m.geoIPMu.Lock()
	if m.geoIPIgnoreError != "" {
		status.Error = m.geoIPIgnoreError
	}
	oldReader := m.geoIPReader
	m.geoIPReader = reader
	m.geoIPStatus = status
	if oldReader != nil {
		_ = oldReader.Close()
	}
	m.geoIPMu.Unlock()
}

func (m *Manager) downloadGeoIPEdition(
	ctx context.Context,
	edition string,
) error {
	if !validGeoIPEdition(edition) {
		return errors.New("invalid GeoIP database edition")
	}
	url := m.geoIPDownloadBase() + "/dbip-city-lite-" + edition + ".mmdb.gz"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "mordhau-server-alpine-linux")
	response, err := m.geoIPClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", errGeoIPEditionUnavailable, edition)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GeoIP download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > geoIPMaximumCompressedBytes {
		return fmt.Errorf(
			"compressed GeoIP database exceeds %d bytes",
			geoIPMaximumCompressedBytes,
		)
	}

	directory := filepath.Dir(m.geoIPDatabasePath())
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	if err := geoIPStorageAvailable(
		directory,
		geoIPStorageReserveBytes+geoIPMaximumDatabaseBytes,
	); err != nil {
		return err
	}

	id, err := randomID()
	if err != nil {
		return err
	}
	temporaryPath := filepath.Join(directory, ".dbip-city-lite.tmp."+id)
	file, err := os.OpenFile(
		temporaryPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0600,
	)
	if err != nil {
		return err
	}
	keepFile := false
	defer func() {
		_ = file.Close()
		if !keepFile {
			_ = os.Remove(temporaryPath)
		}
	}()

	compressed := &io.LimitedReader{
		R: response.Body,
		N: geoIPMaximumCompressedBytes + 1,
	}
	decompressor, err := gzip.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("open compressed GeoIP database: %w", err)
	}
	written, copyErr := io.Copy(
		file,
		io.LimitReader(decompressor, geoIPMaximumDatabaseBytes+1),
	)
	closeErr := decompressor.Close()
	if copyErr != nil {
		return fmt.Errorf("decompress GeoIP database: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close compressed GeoIP database: %w", closeErr)
	}
	if compressed.N <= 0 {
		return fmt.Errorf(
			"compressed GeoIP database exceeds %d bytes",
			geoIPMaximumCompressedBytes,
		)
	}
	if written < 1 || written > geoIPMaximumDatabaseBytes {
		return fmt.Errorf("GeoIP database has an invalid size: %d bytes", written)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, 0600); err != nil {
		return err
	}

	reader, err := maxminddb.Open(temporaryPath)
	if err != nil {
		return fmt.Errorf("open downloaded GeoIP database: %w", err)
	}
	keepReader := false
	defer func() {
		if !keepReader {
			_ = reader.Close()
		}
	}()
	if !validGeoIPMetadata(reader.Metadata) {
		return errors.New("downloaded GeoIP database metadata is incompatible")
	}
	if err := reader.Verify(); err != nil {
		return fmt.Errorf("verify downloaded GeoIP database: %w", err)
	}

	if err := os.Rename(temporaryPath, m.geoIPDatabasePath()); err != nil {
		return err
	}
	keepFile = true
	downloadedAt := m.geoIPCurrentTime()
	state := geoIPStateFile{
		Version:      1,
		Edition:      edition,
		DownloadedAt: downloadedAt,
		LastChecked:  downloadedAt,
	}
	m.installGeoIPReader(reader, edition, downloadedAt)
	keepReader = true
	if err := writeJSONAtomic(m.geoIPStatePath(), state, 0600); err != nil {
		return fmt.Errorf("save GeoIP update state: %w", err)
	}
	return nil
}

func (m *Manager) refreshGeoIP(ctx context.Context, force bool) error {
	m.geoIPUpdateMu.Lock()
	defer m.geoIPUpdateMu.Unlock()

	now := m.geoIPCurrentTime()
	currentEdition := now.UTC().Format("2006-01")
	status := m.geoIPStatusView()
	if !force && status.Available && status.Edition == currentEdition {
		m.markGeoIPChecked(now, nil)
		return nil
	}

	editionCount := 1
	if !status.Available {
		editionCount = 3
	}
	var lastError error
	for _, edition := range geoIPEditions(now, editionCount) {
		if status.Available && edition == status.Edition {
			continue
		}
		err := m.downloadGeoIPEdition(ctx, edition)
		if err == nil {
			log.Printf("installed %s database edition %s", geoIPProviderName, edition)
			return nil
		}
		lastError = err
		if ctx.Err() != nil {
			break
		}
		if !errors.Is(err, errGeoIPEditionUnavailable) {
			break
		}
	}
	if lastError == nil {
		lastError = errors.New("no GeoIP database edition was available")
	}
	m.markGeoIPChecked(now, lastError)
	return lastError
}

func (m *Manager) geoIPUpdateLoop(ctx context.Context) {
	if err := m.refreshGeoIP(ctx, false); err != nil && ctx.Err() == nil {
		log.Printf("refresh GeoIP database: %v", err)
	}
	ticker := time.NewTicker(geoIPUpdateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.refreshGeoIP(ctx, false); err != nil && ctx.Err() == nil {
				log.Printf("refresh GeoIP database: %v", err)
			}
		}
	}
}
