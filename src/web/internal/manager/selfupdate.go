package manager

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	managerRepositoryOwner          = "itinfra7"
	managerRepositoryName           = "mordhau-server-alpine-linux"
	managerUpdateLatestReleaseURL   = "https://api.github.com/repos/itinfra7/mordhau-server-alpine-linux/releases/latest"
	managerUpdateReleaseDownloadURL = "https://github.com/itinfra7/mordhau-server-alpine-linux/releases/download"
	managerUpdateStatePath          = stateDir + "/manager-update.json"
	managerVersionPath              = stateDir + "/manager-version"
	managerBinaryPath               = rootDir + "/bin/mordhau-web"
	managerUpdateLogPath            = runtimeDir + "/manager-update.log"
	managerUpdateLockPath           = "/run/mordhau-manager-update.lock"
	managerUpdateStateVersion       = 1
	managerUpdateCheckInterval      = time.Hour
	managerUpdateHTTPTimeout        = 20 * time.Second
	managerUpdateReleaseLimit       = 2 << 20
	managerUpdateChecksumLimit      = 1 << 20
	managerUpdateArchiveLimit       = 64 << 20
	managerUpdateExpandedLimit      = 256 << 20
	managerUpdateFileLimit          = 5000
	managerUpdateNotesLimit         = 4096
	managerUpdateErrorLimit         = 1024
)

var (
	errManagerUpdateBusy           = errors.New("a manager update is already running")
	errManagerUpdateLifecycleBusy  = errors.New("a dedicated-server lifecycle operation is in progress")
	errManagerUpdateUnavailable    = errors.New("no newer manager release is available")
	errManagerUpdateStale          = errors.New("the selected manager release is no longer current")
	errManagerUpdateWorkerNotOwner = errors.New(
		"manager update worker did not acquire the update request",
	)
)

type semanticVersion struct {
	Major int64
	Minor int64
	Patch int64
}

type managerUpdateRelease struct {
	Version      string
	ReleaseURL   string
	ReleaseNotes string
}

type managerUpdateStateFile struct {
	Version       int       `json:"version"`
	LatestVersion string    `json:"latest_version,omitempty"`
	ReleaseURL    string    `json:"release_url,omitempty"`
	ReleaseNotes  string    `json:"release_notes,omitempty"`
	CheckedAt     time.Time `json:"checked_at,omitempty"`
	CheckError    string    `json:"check_error,omitempty"`
	Status        string    `json:"status"`
	TargetVersion string    `json:"target_version,omitempty"`
	RequestedBy   string    `json:"requested_by,omitempty"`
	RequestedIP   string    `json:"requested_ip,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	Progress      string    `json:"progress,omitempty"`
	Error         string    `json:"error,omitempty"`
}

type ManagerUpdateView struct {
	InstalledVersion string    `json:"installed_version"`
	LatestVersion    string    `json:"latest_version,omitempty"`
	Available        bool      `json:"available"`
	ReleaseURL       string    `json:"release_url,omitempty"`
	ReleaseNotes     string    `json:"release_notes,omitempty"`
	CheckedAt        time.Time `json:"checked_at,omitempty"`
	CheckError       string    `json:"check_error,omitempty"`
	Status           string    `json:"status"`
	TargetVersion    string    `json:"target_version,omitempty"`
	RequestedBy      string    `json:"requested_by,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
	Progress         string    `json:"progress,omitempty"`
	Error            string    `json:"error,omitempty"`
}

type githubLatestRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

type managerUpdateWorkerConfig struct {
	StatePath       string
	VersionPath     string
	LockPath        string
	ReleaseBaseURL  string
	HTTPClient      *http.Client
	Now             func() time.Time
	InitialDelay    time.Duration
	TemporaryParent string
	RunInstaller    func(context.Context, string) error
	ServiceRunning  func(string) bool
	StartService    func(string) error
}

func parseSemanticVersion(value string) (semanticVersion, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return semanticVersion{}, errors.New("version must contain three components")
	}
	values := make([]int64, 3)
	for index, component := range parts {
		if component == "" || len(component) > 9 {
			return semanticVersion{}, errors.New("invalid version component")
		}
		if len(component) > 1 && component[0] == '0' {
			return semanticVersion{}, errors.New("version components must be canonical")
		}
		for _, character := range component {
			if character < '0' || character > '9' {
				return semanticVersion{}, errors.New("version components must be numeric")
			}
		}
		parsed, err := strconv.ParseInt(component, 10, 64)
		if err != nil {
			return semanticVersion{}, errors.New("invalid version component")
		}
		values[index] = parsed
	}
	return semanticVersion{Major: values[0], Minor: values[1], Patch: values[2]}, nil
}

func compareSemanticVersions(left, right string) (int, error) {
	leftVersion, err := parseSemanticVersion(left)
	if err != nil {
		return 0, err
	}
	rightVersion, err := parseSemanticVersion(right)
	if err != nil {
		return 0, err
	}
	leftParts := [...]int64{leftVersion.Major, leftVersion.Minor, leftVersion.Patch}
	rightParts := [...]int64{rightVersion.Major, rightVersion.Minor, rightVersion.Patch}
	for index := range leftParts {
		if leftParts[index] < rightParts[index] {
			return -1, nil
		}
		if leftParts[index] > rightParts[index] {
			return 1, nil
		}
	}
	return 0, nil
}

func managerReleaseTag(version string) (string, error) {
	if _, err := parseSemanticVersion(version); err != nil {
		return "", err
	}
	return "v" + version, nil
}

func managerReleaseArchiveName(version string) (string, error) {
	tag, err := managerReleaseTag(version)
	if err != nil {
		return "", err
	}
	return managerRepositoryName + "-" + tag + ".tar.gz", nil
}

func managerReleaseURL(version string) (string, error) {
	tag, err := managerReleaseTag(version)
	if err != nil {
		return "", err
	}
	return "https://github.com/" + managerRepositoryOwner + "/" +
		managerRepositoryName + "/releases/tag/" + tag, nil
}

func boundedManagerUpdateText(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value) + "…"
}

func defaultManagerUpdateHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.ExpectContinueTimeout = time.Second
	return &http.Client{
		Transport: transport,
		Timeout:   managerUpdateHTTPTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if request.URL.Scheme != "https" {
				return errors.New("manager update redirect is not HTTPS")
			}
			host := strings.ToLower(request.URL.Hostname())
			if host != "github.com" &&
				host != "api.github.com" &&
				!strings.HasSuffix(host, ".githubusercontent.com") {
				return errors.New("manager update redirect has an unexpected host")
			}
			return nil
		},
	}
}

func readLimitedResponse(response *http.Response, limit int64) ([]byte, error) {
	reader := io.LimitReader(response.Body, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response exceeds the permitted size")
	}
	return data, nil
}

func fetchLatestManagerRelease(
	ctx context.Context,
	client *http.Client,
	endpoint string,
) (managerUpdateRelease, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return managerUpdateRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", managerRepositoryName+"-manager")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := client.Do(request)
	if err != nil {
		return managerUpdateRelease{}, fmt.Errorf("query GitHub latest release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return managerUpdateRelease{}, fmt.Errorf(
			"query GitHub latest release: unexpected HTTP status %d",
			response.StatusCode,
		)
	}
	data, err := readLimitedResponse(response, managerUpdateReleaseLimit)
	if err != nil {
		return managerUpdateRelease{}, fmt.Errorf("read GitHub latest release: %w", err)
	}
	var release githubLatestRelease
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&release); err != nil {
		// GitHub can add fields at any time, so retry without unknown-field rejection.
		if err := json.Unmarshal(data, &release); err != nil {
			return managerUpdateRelease{}, errors.New("GitHub latest release response is invalid")
		}
	}
	if release.Draft || release.Prerelease {
		return managerUpdateRelease{}, errors.New("GitHub latest release is not a stable release")
	}
	if !strings.HasPrefix(release.TagName, "v") {
		return managerUpdateRelease{}, errors.New("GitHub latest release tag is invalid")
	}
	version := strings.TrimPrefix(release.TagName, "v")
	if _, err := parseSemanticVersion(version); err != nil {
		return managerUpdateRelease{}, errors.New("GitHub latest release version is invalid")
	}
	archiveName, _ := managerReleaseArchiveName(version)
	requiredAssets := map[string]bool{
		archiveName:  false,
		"SHA256SUMS": false,
	}
	for _, asset := range release.Assets {
		if _, required := requiredAssets[asset.Name]; required {
			requiredAssets[asset.Name] = true
		}
	}
	for name, present := range requiredAssets {
		if !present {
			return managerUpdateRelease{}, fmt.Errorf(
				"GitHub latest release is missing required asset %s",
				name,
			)
		}
	}
	releaseURL, _ := managerReleaseURL(version)
	return managerUpdateRelease{
		Version:      version,
		ReleaseURL:   releaseURL,
		ReleaseNotes: boundedManagerUpdateText(release.Body, managerUpdateNotesLimit),
	}, nil
}

func validManagerUpdateState(state managerUpdateStateFile) bool {
	if state.Version != managerUpdateStateVersion {
		return false
	}
	switch state.Status {
	case "idle":
	case "running":
		if state.TargetVersion == "" || state.StartedAt.IsZero() ||
			!state.FinishedAt.IsZero() {
			return false
		}
	case "succeeded", "failed":
		if state.TargetVersion == "" || state.StartedAt.IsZero() ||
			state.FinishedAt.IsZero() {
			return false
		}
	default:
		return false
	}
	if state.LatestVersion != "" {
		if _, err := parseSemanticVersion(state.LatestVersion); err != nil {
			return false
		}
		expectedURL, _ := managerReleaseURL(state.LatestVersion)
		if state.ReleaseURL != expectedURL {
			return false
		}
	} else if state.ReleaseURL != "" || state.ReleaseNotes != "" {
		return false
	}
	if state.TargetVersion != "" {
		if _, err := parseSemanticVersion(state.TargetVersion); err != nil {
			return false
		}
	}
	return len(state.ReleaseNotes) <= managerUpdateNotesLimit+len("…") &&
		len(state.CheckError) <= managerUpdateErrorLimit+len("…") &&
		len(state.Error) <= managerUpdateErrorLimit+len("…") &&
		len(state.Progress) <= 256+len("…") &&
		len(state.RequestedBy) <= 128 &&
		len(state.RequestedIP) <= 64
}

func readManagerUpdateState(path string) (managerUpdateStateFile, error) {
	var state managerUpdateStateFile
	if err := readJSON(path, &state); err != nil {
		return state, err
	}
	if !validManagerUpdateState(state) {
		return state, errors.New("stored manager update state is invalid")
	}
	return state, nil
}

func initialManagerUpdateState() managerUpdateStateFile {
	return managerUpdateStateFile{
		Version: managerUpdateStateVersion,
		Status:  "idle",
	}
}

func (m *Manager) managerUpdateStatePath() string {
	if m.managerUpdateStateFile != "" {
		return m.managerUpdateStateFile
	}
	return managerUpdateStatePath
}

func (m *Manager) managerVersionPath() string {
	if m.managerUpdateVersionFile != "" {
		return m.managerUpdateVersionFile
	}
	return managerVersionPath
}

func (m *Manager) managerUpdateNowValue() time.Time {
	if m.managerUpdateNow != nil {
		return m.managerUpdateNow()
	}
	return time.Now()
}

func (m *Manager) managerUpdateLockPath() string {
	if m.managerUpdateLockFile != "" {
		return m.managerUpdateLockFile
	}
	return managerUpdateLockPath
}

func managerUpdateWorkerActive(lockPath string) (bool, error) {
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	defer lockFile.Close()
	if err := lockFile.Chmod(0600); err != nil {
		return false, err
	}
	if err := unix.Flock(
		int(lockFile.Fd()),
		unix.LOCK_EX|unix.LOCK_NB,
	); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) ||
			errors.Is(err, unix.EAGAIN) {
			return true, nil
		}
		return false, err
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_UN); err != nil {
		return false, err
	}
	return false, nil
}

func (m *Manager) reconcileManagerUpdateState() error {
	m.managerUpdateMu.Lock()
	defer m.managerUpdateMu.Unlock()

	state, err := readManagerUpdateState(m.managerUpdateStatePath())
	if err != nil || state.Status != "running" {
		return err
	}
	active, err := managerUpdateWorkerActive(m.managerUpdateLockPath())
	if err != nil {
		return fmt.Errorf("inspect manager update worker: %w", err)
	}
	if active {
		return nil
	}
	installed, versionErr := readInstalledManagerVersion(
		m.managerVersionPath(),
	)
	state.FinishedAt = m.managerUpdateNowValue()
	state.Progress = ""
	if versionErr == nil {
		comparison, compareErr := compareSemanticVersions(
			installed,
			state.TargetVersion,
		)
		if compareErr == nil && comparison >= 0 {
			state.Status = "succeeded"
			state.Error = ""
			return writeJSONAtomic(m.managerUpdateStatePath(), state, 0600)
		}
	}
	state.Status = "failed"
	state.Error = "The manager update was interrupted before completion."
	return writeJSONAtomic(m.managerUpdateStatePath(), state, 0600)
}

func (m *Manager) loadOrCreateManagerUpdateState() error {
	path := m.managerUpdateStatePath()
	_, err := readManagerUpdateState(path)
	if err == nil {
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
		return m.reconcileManagerUpdateState()
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load manager update state: %w", err)
	}
	if err := writeJSONAtomic(path, initialManagerUpdateState(), 0600); err != nil {
		return fmt.Errorf("create manager update state: %w", err)
	}
	return nil
}

func readInstalledManagerVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return "", errors.New("installed manager version state is invalid")
	}
	if _, err := parseSemanticVersion(value); err != nil {
		return "", errors.New("installed manager version state is invalid")
	}
	return value, nil
}

func managerUpdateView(
	state managerUpdateStateFile,
	versionPath string,
) (ManagerUpdateView, error) {
	installed, err := readInstalledManagerVersion(versionPath)
	view := ManagerUpdateView{
		InstalledVersion: installed,
		LatestVersion:    state.LatestVersion,
		ReleaseURL:       state.ReleaseURL,
		ReleaseNotes:     state.ReleaseNotes,
		CheckedAt:        state.CheckedAt,
		CheckError:       state.CheckError,
		Status:           state.Status,
		TargetVersion:    state.TargetVersion,
		RequestedBy:      state.RequestedBy,
		StartedAt:        state.StartedAt,
		FinishedAt:       state.FinishedAt,
		Progress:         state.Progress,
		Error:            state.Error,
	}
	if err != nil {
		view.InstalledVersion = "unknown"
		return view, err
	}
	if state.LatestVersion != "" {
		comparison, compareErr := compareSemanticVersions(installed, state.LatestVersion)
		if compareErr != nil {
			return view, compareErr
		}
		view.Available = comparison < 0
	}
	return view, nil
}

func (m *Manager) currentManagerUpdateView() (ManagerUpdateView, error) {
	state, err := readManagerUpdateState(m.managerUpdateStatePath())
	if err != nil {
		return ManagerUpdateView{}, err
	}
	return managerUpdateView(state, m.managerVersionPath())
}

func (m *Manager) managerUpdateRunning() bool {
	// Production managers always receive an explicit state path from New.
	// An unconfigured Manager has no update state and must not consult the
	// installed server's global state.
	if m.managerUpdateStateFile == "" {
		return false
	}
	state, err := readManagerUpdateState(m.managerUpdateStateFile)
	return err == nil && state.Status == "running"
}

func (m *Manager) checkManagerUpdate(
	ctx context.Context,
) (ManagerUpdateView, error) {
	m.managerUpdateMu.Lock()
	defer m.managerUpdateMu.Unlock()

	state, err := readManagerUpdateState(m.managerUpdateStatePath())
	if err != nil {
		return ManagerUpdateView{}, err
	}
	if state.Status == "running" {
		return managerUpdateView(state, m.managerVersionPath())
	}
	client := m.managerUpdateHTTPClient
	if client == nil {
		client = defaultManagerUpdateHTTPClient()
	}
	endpoint := m.managerUpdateLatestURL
	if endpoint == "" {
		endpoint = managerUpdateLatestReleaseURL
	}
	release, checkErr := fetchLatestManagerRelease(ctx, client, endpoint)
	state.CheckedAt = m.managerUpdateNowValue()
	if checkErr != nil {
		state.CheckError = boundedManagerUpdateText(checkErr.Error(), managerUpdateErrorLimit)
	} else {
		state.LatestVersion = release.Version
		state.ReleaseURL = release.ReleaseURL
		state.ReleaseNotes = release.ReleaseNotes
		state.CheckError = ""
	}
	if err := writeJSONAtomic(m.managerUpdateStatePath(), state, 0600); err != nil {
		return ManagerUpdateView{}, err
	}
	view, viewErr := managerUpdateView(state, m.managerVersionPath())
	m.signalAutomaticUpdateLoop()
	if checkErr != nil {
		return view, checkErr
	}
	return view, viewErr
}

func (m *Manager) managerUpdateCheckLoop(ctx context.Context) {
	checkIfDue := func() {
		state, err := readManagerUpdateState(m.managerUpdateStatePath())
		if err != nil ||
			!managerUpdateCheckDue(state, m.managerUpdateNowValue()) {
			return
		}
		before, _ := managerUpdateView(state, m.managerVersionPath())
		view, checkErr := m.checkManagerUpdate(ctx)
		if checkErr != nil {
			if view.CheckError != "" &&
				view.CheckError != before.CheckError {
				m.auditActorEvent(
					"system",
					"local",
					"manager_update_check_failed",
					map[string]string{
						"error": boundedManagerUpdateText(
							checkErr.Error(),
							256,
						),
					},
				)
			}
			return
		}
		if view.Available &&
			(!before.Available ||
				before.LatestVersion != view.LatestVersion) {
			m.auditActorEvent("system", "local", "manager_update_available",
				map[string]string{
					"installed_version": view.InstalledVersion,
					"latest_version":    view.LatestVersion,
				})
		}
	}
	checkIfDue()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkIfDue()
		}
	}
}

func managerUpdateCheckDue(
	state managerUpdateStateFile,
	now time.Time,
) bool {
	return state.Status != "running" &&
		(state.CheckedAt.IsZero() ||
			!now.Before(state.CheckedAt.Add(managerUpdateCheckInterval)))
}

func (m *Manager) startManagerUpdateWorker(targetVersion string) error {
	binary := m.managerUpdateBinary
	if binary == "" {
		binary = managerBinaryPath
	}
	logPath := m.managerUpdateLogFile
	if logPath == "" {
		logPath = managerUpdateLogPath
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := logFile.Chmod(0600); err != nil {
		_ = logFile.Close()
		return err
	}
	command := exec.Command(binary, "--manager-update-worker", targetVersion)
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	if err := command.Process.Release(); err != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = logFile.Close()
		return err
	}
	return logFile.Close()
}

func (m *Manager) beginManagerUpdate(
	targetVersion string,
	requestedBy string,
	requestedIP string,
) (ManagerUpdateView, error) {
	m.managerUpdateMu.Lock()
	defer m.managerUpdateMu.Unlock()

	state, err := readManagerUpdateState(m.managerUpdateStatePath())
	if err != nil {
		return ManagerUpdateView{}, err
	}
	if state.Status == "running" {
		view, _ := managerUpdateView(state, m.managerVersionPath())
		return view, errManagerUpdateBusy
	}
	if m.operationRunning() {
		view, _ := managerUpdateView(state, m.managerVersionPath())
		return view, errManagerUpdateLifecycleBusy
	}
	view, err := managerUpdateView(state, m.managerVersionPath())
	if err != nil {
		return view, err
	}
	if !view.Available {
		return view, errManagerUpdateUnavailable
	}
	if targetVersion == "" || targetVersion != state.LatestVersion {
		return view, errManagerUpdateStale
	}
	now := m.managerUpdateNowValue()
	state.Status = "running"
	state.TargetVersion = targetVersion
	state.RequestedBy = boundedManagerUpdateText(requestedBy, 128)
	state.RequestedIP = boundedManagerUpdateText(requestedIP, 64)
	state.StartedAt = now
	state.FinishedAt = time.Time{}
	state.Progress = "Starting the detached update worker"
	state.Error = ""
	if err := writeJSONAtomic(m.managerUpdateStatePath(), state, 0600); err != nil {
		return ManagerUpdateView{}, err
	}
	start := m.managerUpdateWorkerStart
	if start == nil {
		start = m.startManagerUpdateWorker
	}
	if err := start(targetVersion); err != nil {
		state.Status = "failed"
		state.FinishedAt = m.managerUpdateNowValue()
		state.Progress = ""
		state.Error = boundedManagerUpdateText(
			"start detached update worker: "+err.Error(),
			managerUpdateErrorLimit,
		)
		_ = writeJSONAtomic(m.managerUpdateStatePath(), state, 0600)
		view, _ := managerUpdateView(state, m.managerVersionPath())
		return view, errors.New(state.Error)
	}
	return managerUpdateView(state, m.managerVersionPath())
}

func managerReleaseAssetURL(baseURL, version, name string) (string, error) {
	tag, err := managerReleaseTag(version)
	if err != nil {
		return "", err
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return baseURL + "/" + url.PathEscape(tag) + "/" + url.PathEscape(name), nil
}

func downloadManagerUpdateAsset(
	ctx context.Context,
	client *http.Client,
	assetURL string,
	destination string,
	limit int64,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", managerRepositoryName+"-updater")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	written, err := io.Copy(file, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if written > limit {
		return errors.New("download exceeds the permitted size")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func checksumForReleaseArchive(data []byte, archiveName string) (string, error) {
	var checksum string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return "", errors.New("SHA256SUMS contains an invalid line")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != archiveName {
			continue
		}
		if checksum != "" {
			return "", errors.New("SHA256SUMS contains a duplicate archive entry")
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", errors.New("SHA256SUMS archive checksum is invalid")
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return "", errors.New("SHA256SUMS archive checksum is invalid")
		}
		checksum = strings.ToLower(fields[0])
	}
	if checksum == "" {
		return "", errors.New("SHA256SUMS does not contain the release archive")
	}
	return checksum, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractManagerReleaseArchive(
	archivePath string,
	destination string,
	version string,
) (string, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return "", errors.New("release archive is not valid gzip data")
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	tag, _ := managerReleaseTag(version)
	topDirectory := managerRepositoryName + "-" + tag
	seen := make(map[string]struct{})
	var expanded int64
	count := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.New("release archive contains invalid tar data")
		}
		count++
		if count > managerUpdateFileLimit {
			return "", errors.New("release archive contains too many entries")
		}
		if header.Size < 0 ||
			header.Size > managerUpdateExpandedLimit-expanded {
			return "", errors.New("release archive expands beyond the permitted size")
		}
		expanded += header.Size
		if header.Typeflag == tar.TypeXGlobalHeader &&
			header.Name == "pax_global_header" {
			continue
		}
		if strings.ContainsAny(header.Name, "\\\x00") ||
			strings.HasPrefix(header.Name, "/") {
			return "", errors.New("release archive contains an unsafe path")
		}
		cleanName := path.Clean(header.Name)
		if cleanName == "." || cleanName == ".." ||
			strings.HasPrefix(cleanName, "../") {
			return "", errors.New("release archive contains an unsafe path")
		}
		if cleanName != topDirectory &&
			!strings.HasPrefix(cleanName, topDirectory+"/") {
			return "", fmt.Errorf(
				"release archive has an unexpected top-level path %q",
				cleanName,
			)
		}
		if _, exists := seen[cleanName]; exists {
			return "", errors.New("release archive contains a duplicate path")
		}
		seen[cleanName] = struct{}{}
		target := filepath.Join(destination, filepath.FromSlash(cleanName))
		cleanDestination := filepath.Clean(destination)
		if target != cleanDestination &&
			!strings.HasPrefix(target, cleanDestination+string(os.PathSeparator)) {
			return "", errors.New("release archive path escapes the extraction directory")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return "", err
			}
			if err := os.Chmod(target, 0700); err != nil {
				return "", err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return "", err
			}
			mode := os.FileMode(0600)
			if header.Mode&0111 != 0 {
				mode = 0700
			}
			file, err := os.OpenFile(
				target,
				os.O_WRONLY|os.O_CREATE|os.O_EXCL,
				mode,
			)
			if err != nil {
				return "", err
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			syncErr := file.Sync()
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if syncErr != nil {
				return "", syncErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		default:
			return "", errors.New("release archive contains an unsupported entry type")
		}
	}
	extractedRoot := filepath.Join(destination, topDirectory)
	installer := filepath.Join(extractedRoot, "src", "mordhau-server-alpine-linux.sh")
	info, err := os.Stat(installer)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0100 == 0 {
		return "", errors.New("release archive does not contain an executable installer")
	}
	data, err := os.ReadFile(installer)
	if err != nil {
		return "", err
	}
	expected := "PROJECT_VERSION=\"" + version + "\""
	foundVersion := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == expected {
			foundVersion = true
			break
		}
	}
	if !foundVersion {
		return "", errors.New("release installer version does not match the selected release")
	}
	return extractedRoot, nil
}

func defaultManagerUpdateWorkerConfig() managerUpdateWorkerConfig {
	return managerUpdateWorkerConfig{
		StatePath:       managerUpdateStatePath,
		VersionPath:     managerVersionPath,
		LockPath:        managerUpdateLockPath,
		ReleaseBaseURL:  managerUpdateReleaseDownloadURL,
		HTTPClient:      defaultManagerUpdateHTTPClient(),
		Now:             time.Now,
		InitialDelay:    2 * time.Second,
		TemporaryParent: "",
		RunInstaller: func(ctx context.Context, installer string) error {
			command := exec.CommandContext(ctx, installer)
			command.Dir = filepath.Dir(filepath.Dir(installer))
			command.Env = append(
				os.Environ(),
				"MORDHAU_MANAGER_UPDATE_WORKER_PID="+strconv.Itoa(os.Getpid()),
			)
			command.Stdin = nil
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			return command.Run()
		},
		ServiceRunning: func(service string) bool {
			return exec.Command("rc-service", service, "status").Run() == nil
		},
		StartService: func(service string) error {
			return exec.Command("rc-service", service, "start").Run()
		},
	}
}

func updateManagerWorkerState(
	config managerUpdateWorkerConfig,
	targetVersion string,
	update func(*managerUpdateStateFile),
) error {
	state, err := readManagerUpdateState(config.StatePath)
	if err != nil {
		return err
	}
	if state.TargetVersion != targetVersion || state.Status != "running" {
		return errors.New("manager update request state does not match the worker")
	}
	update(&state)
	return writeJSONAtomic(config.StatePath, state, 0600)
}

func restoreManagerUpdateServices(
	config managerUpdateWorkerConfig,
	serverWasRunning bool,
	webWasRunning bool,
) error {
	var failures []string
	for _, service := range []struct {
		name    string
		running bool
	}{
		{name: "mordhau-server", running: serverWasRunning},
		{name: "mordhau-web", running: webWasRunning},
	} {
		if !service.running || config.ServiceRunning(service.name) {
			continue
		}
		if err := config.StartService(service.name); err != nil {
			failures = append(failures, service.name+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func runManagerUpdateWorker(
	ctx context.Context,
	targetVersion string,
	config managerUpdateWorkerConfig,
) (resultErr error) {
	if _, err := parseSemanticVersion(targetVersion); err != nil {
		return fmt.Errorf(
			"%w: selected manager update version is invalid",
			errManagerUpdateWorkerNotOwner,
		)
	}
	lockFile, err := os.OpenFile(config.LockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := lockFile.Chmod(0600); err != nil {
		return err
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf(
			"%w: another manager update worker is running",
			errManagerUpdateWorkerNotOwner,
		)
	}
	defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)

	state, err := readManagerUpdateState(config.StatePath)
	if err != nil {
		return fmt.Errorf("%w: %v", errManagerUpdateWorkerNotOwner, err)
	}
	if state.Status != "running" || state.TargetVersion != targetVersion {
		return fmt.Errorf(
			"%w: manager update request state does not match the worker",
			errManagerUpdateWorkerNotOwner,
		)
	}
	defer func() {
		now := config.Now()
		stateErr := updateManagerWorkerState(
			config,
			targetVersion,
			func(state *managerUpdateStateFile) {
				state.FinishedAt = now
				state.Progress = ""
				if resultErr != nil {
					state.Status = "failed"
					state.Error = boundedManagerUpdateText(
						resultErr.Error(),
						managerUpdateErrorLimit,
					)
					return
				}
				state.Status = "succeeded"
				state.Error = ""
			},
		)
		if stateErr == nil {
			return
		}
		if resultErr == nil {
			resultErr = fmt.Errorf(
				"record manager update result: %w",
				stateErr,
			)
			return
		}
		resultErr = fmt.Errorf(
			"%v; record manager update result: %w",
			resultErr,
			stateErr,
		)
	}()
	if config.InitialDelay > 0 {
		timer := time.NewTimer(config.InitialDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	installed, err := readInstalledManagerVersion(config.VersionPath)
	if err != nil {
		return err
	}
	comparison, err := compareSemanticVersions(installed, targetVersion)
	if err != nil {
		return err
	}
	if comparison >= 0 {
		return errors.New("selected release is not newer than the installed version")
	}

	serverWasRunning := config.ServiceRunning("mordhau-server")
	webWasRunning := config.ServiceRunning("mordhau-web")
	temporary, err := os.MkdirTemp(config.TemporaryParent, "mordhau-manager-update.")
	if err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0700); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	defer os.RemoveAll(temporary)

	archiveName, _ := managerReleaseArchiveName(targetVersion)
	archivePath := filepath.Join(temporary, archiveName)
	checksumPath := filepath.Join(temporary, "SHA256SUMS")
	archiveURL, _ := managerReleaseAssetURL(
		config.ReleaseBaseURL,
		targetVersion,
		archiveName,
	)
	checksumURL, _ := managerReleaseAssetURL(
		config.ReleaseBaseURL,
		targetVersion,
		"SHA256SUMS",
	)
	if err := updateManagerWorkerState(config, targetVersion, func(state *managerUpdateStateFile) {
		state.Progress = "Downloading the release archive and checksum"
	}); err != nil {
		return err
	}
	if err := downloadManagerUpdateAsset(
		ctx,
		config.HTTPClient,
		archiveURL,
		archivePath,
		managerUpdateArchiveLimit,
	); err != nil {
		return fmt.Errorf("download release archive: %w", err)
	}
	if err := downloadManagerUpdateAsset(
		ctx,
		config.HTTPClient,
		checksumURL,
		checksumPath,
		managerUpdateChecksumLimit,
	); err != nil {
		return fmt.Errorf("download release checksum: %w", err)
	}

	if err := updateManagerWorkerState(config, targetVersion, func(state *managerUpdateStateFile) {
		state.Progress = "Verifying the release checksum"
	}); err != nil {
		return err
	}
	checksumData, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}
	expectedChecksum, err := checksumForReleaseArchive(checksumData, archiveName)
	if err != nil {
		return err
	}
	actualChecksum, err := fileSHA256(archivePath)
	if err != nil {
		return err
	}
	if actualChecksum != expectedChecksum {
		return errors.New("release archive checksum verification failed")
	}

	if err := updateManagerWorkerState(config, targetVersion, func(state *managerUpdateStateFile) {
		state.Progress = "Validating and extracting the release"
	}); err != nil {
		return err
	}
	extractDirectory := filepath.Join(temporary, "release")
	if err := os.Mkdir(extractDirectory, 0700); err != nil {
		return err
	}
	extractedRoot, err := extractManagerReleaseArchive(
		archivePath,
		extractDirectory,
		targetVersion,
	)
	if err != nil {
		return err
	}
	installer := filepath.Join(
		extractedRoot,
		"src",
		"mordhau-server-alpine-linux.sh",
	)

	if err := updateManagerWorkerState(config, targetVersion, func(state *managerUpdateStateFile) {
		state.Progress = "Installing the verified release; services will restart"
	}); err != nil {
		return err
	}
	if err := config.RunInstaller(ctx, installer); err != nil {
		recoveryErr := restoreManagerUpdateServices(
			config,
			serverWasRunning,
			webWasRunning,
		)
		if recoveryErr != nil {
			return fmt.Errorf(
				"installer failed: %v; service recovery failed: %w",
				err,
				recoveryErr,
			)
		}
		return fmt.Errorf("installer failed: %w", err)
	}
	installed, err = readInstalledManagerVersion(config.VersionPath)
	if err != nil {
		return err
	}
	if installed != targetVersion {
		return fmt.Errorf(
			"installer recorded version %s instead of %s",
			installed,
			targetVersion,
		)
	}
	return nil
}

// RunManagerUpdateWorker executes a previously authenticated update request.
// It is invoked by the detached worker mode of the installed manager binary.
func RunManagerUpdateWorker(ctx context.Context, targetVersion string) error {
	config := defaultManagerUpdateWorkerConfig()
	log.Printf("manager update worker started for v%s", targetVersion)
	err := runManagerUpdateWorker(ctx, targetVersion, config)
	if errors.Is(err, errManagerUpdateWorkerNotOwner) {
		log.Printf("manager update worker did not start for v%s: %v", targetVersion, err)
		return err
	}
	if err != nil {
		log.Printf("manager update worker failed for v%s: %v", targetVersion, err)
		return err
	}
	log.Printf("manager update worker completed for v%s", targetVersion)
	return nil
}
