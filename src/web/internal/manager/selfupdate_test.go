package manager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSemanticVersionValidationAndComparison(t *testing.T) {
	for _, value := range []string{"0.0.0", "2.4.0", "999999999.1.7"} {
		if _, err := parseSemanticVersion(value); err != nil {
			t.Fatalf("valid version %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"",
		"v2.4.0",
		"2.4",
		"2.4.0.1",
		"02.4.0",
		"2.-1.0",
		"1000000000.0.0",
	} {
		if _, err := parseSemanticVersion(value); err == nil {
			t.Fatalf("invalid version %q accepted", value)
		}
	}
	for _, test := range []struct {
		left  string
		right string
		want  int
	}{
		{left: "2.3.3", right: "2.4.0", want: -1},
		{left: "2.4.0", right: "2.4.0", want: 0},
		{left: "3.0.0", right: "2.99.99", want: 1},
	} {
		got, err := compareSemanticVersions(test.left, test.right)
		if err != nil || got != test.want {
			t.Fatalf(
				"compare %s to %s = %d, %v; want %d",
				test.left,
				test.right,
				got,
				err,
				test.want,
			)
		}
	}
}

func TestManagerUpdateCheckDueUsesPersistedHourlyInterval(t *testing.T) {
	checked := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	state := initialManagerUpdateState()
	state.CheckedAt = checked
	if managerUpdateCheckDue(state, checked.Add(59*time.Minute)) {
		t.Fatal("manager update check became due before one hour")
	}
	if !managerUpdateCheckDue(state, checked.Add(time.Hour)) {
		t.Fatal("manager update check did not become due after one hour")
	}
	state.Status = "running"
	if managerUpdateCheckDue(state, checked.Add(2*time.Hour)) {
		t.Fatal("running manager update allowed a background release check")
	}
}

func TestFetchLatestManagerReleaseRequiresStableAssets(t *testing.T) {
	archive, _ := managerReleaseArchiveName("2.4.0")
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(
				response,
				`{"tag_name":"v2.4.0","html_url":"https://example.invalid/",`+
					`"body":"Security and updater changes.","draft":false,`+
					`"prerelease":false,"unexpected":"allowed","assets":[`+
					`{"name":%q},{"name":"SHA256SUMS"}]}`,
				archive,
			)
		},
	))
	defer server.Close()

	release, err := fetchLatestManagerRelease(
		context.Background(),
		server.Client(),
		server.URL,
	)
	if err != nil {
		t.Fatalf("fetch latest release: %v", err)
	}
	if release.Version != "2.4.0" ||
		release.ReleaseURL !=
			"https://github.com/itinfra7/mordhau-server-alpine-linux/releases/tag/v2.4.0" ||
		release.ReleaseNotes != "Security and updater changes." {
		t.Fatalf("unexpected release: %+v", release)
	}
}

func TestFetchLatestManagerReleaseRejectsMissingAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			fmt.Fprint(
				response,
				`{"tag_name":"v2.4.0","draft":false,"prerelease":false,`+
					`"assets":[{"name":"SHA256SUMS"}]}`,
			)
		},
	))
	defer server.Close()
	if _, err := fetchLatestManagerRelease(
		context.Background(),
		server.Client(),
		server.URL,
	); err == nil || !strings.Contains(err.Error(), "missing required asset") {
		t.Fatalf("missing archive accepted: %v", err)
	}
}

func TestFetchLatestManagerReleaseLimitsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			_, _ = response.Write(bytes.Repeat(
				[]byte("x"),
				managerUpdateReleaseLimit+1,
			))
		},
	))
	defer server.Close()
	if _, err := fetchLatestManagerRelease(
		context.Background(),
		server.Client(),
		server.URL,
	); err == nil || !strings.Contains(err.Error(), "permitted size") {
		t.Fatalf("oversized response accepted: %v", err)
	}
}

func TestChecksumForReleaseArchive(t *testing.T) {
	name := "mordhau-server-alpine-linux-v2.4.0.tar.gz"
	checksum := strings.Repeat("a", 64)
	data := []byte(checksum + "  " + name + "\n" +
		strings.Repeat("b", 64) + "  CHANGELOG-v2.4.0.md\n")
	got, err := checksumForReleaseArchive(data, name)
	if err != nil || got != checksum {
		t.Fatalf("checksum = %q, %v", got, err)
	}
	duplicate := append(data, []byte(checksum+"  "+name+"\n")...)
	if _, err := checksumForReleaseArchive(duplicate, name); err == nil {
		t.Fatal("duplicate archive checksum accepted")
	}
	if _, err := checksumForReleaseArchive(
		[]byte("invalid  "+name+"\n"),
		name,
	); err == nil {
		t.Fatal("malformed archive checksum accepted")
	}
}

type testTarEntry struct {
	name     string
	body     []byte
	mode     int64
	typeflag byte
	linkname string
}

func managerTestArchive(
	t *testing.T,
	version string,
	entries []testTarEntry,
) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	top := managerRepositoryName + "-v" + version
	for _, entry := range entries {
		name := entry.name
		if !strings.HasPrefix(name, "/") &&
			!strings.HasPrefix(name, "../") &&
			!strings.HasPrefix(name, top) {
			name = top + "/" + name
		}
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := entry.mode
		if mode == 0 {
			mode = 0600
		}
		header := &tar.Header{
			Name:     name,
			Mode:     mode,
			Size:     int64(len(entry.body)),
			Typeflag: typeflag,
			Linkname: entry.linkname,
		}
		if typeflag != tar.TypeReg && typeflag != tar.TypeRegA {
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if header.Size > 0 {
			if _, err := tarWriter.Write(entry.body); err != nil {
				t.Fatalf("write tar body: %v", err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

func validManagerTestArchive(t *testing.T, version string) []byte {
	t.Helper()
	return managerTestArchive(t, version, []testTarEntry{
		{
			name: "src/mordhau-server-alpine-linux.sh",
			body: []byte(
				"#!/bin/sh\nPROJECT_VERSION=\"" + version + "\"\n",
			),
			mode: 0700,
		},
		{name: "README.md", body: []byte("release\n")},
	})
}

func TestExtractManagerReleaseArchiveValidatesPathsAndVersion(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "release.tar.gz")
	if err := os.WriteFile(
		archivePath,
		validManagerTestArchive(t, "2.4.0"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "extract")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	extracted, err := extractManagerReleaseArchive(
		archivePath,
		destination,
		"2.4.0",
	)
	if err != nil {
		t.Fatalf("extract valid archive: %v", err)
	}
	installer := filepath.Join(
		extracted,
		"src",
		"mordhau-server-alpine-linux.sh",
	)
	if info, err := os.Stat(installer); err != nil || info.Mode()&0100 == 0 {
		t.Fatalf("installer mode = %v, %v", info, err)
	}

	for name, entries := range map[string][]testTarEntry{
		"traversal": {
			{name: "../outside", body: []byte("bad")},
		},
		"symlink": {
			{
				name:     "src/link",
				typeflag: tar.TypeSymlink,
				linkname: "/etc/passwd",
			},
		},
		"wrong-version": {
			{
				name: "src/mordhau-server-alpine-linux.sh",
				body: []byte("#!/bin/sh\nPROJECT_VERSION=\"9.9.9\"\n"),
				mode: 0700,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "release.tar.gz")
			if err := os.WriteFile(
				path,
				managerTestArchive(t, "2.4.0", entries),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "extract")
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := extractManagerReleaseArchive(
				path,
				target,
				"2.4.0",
			); err == nil {
				t.Fatalf("%s archive accepted", name)
			}
		})
	}
}

func TestPackagedManagerReleaseArchive(t *testing.T) {
	archivePath := strings.TrimSpace(os.Getenv("MORDHAU_RELEASE_ARCHIVE"))
	version := strings.TrimSpace(os.Getenv("MORDHAU_RELEASE_VERSION"))
	if archivePath == "" && version == "" {
		t.Skip("set MORDHAU_RELEASE_ARCHIVE and MORDHAU_RELEASE_VERSION")
	}
	if archivePath == "" || version == "" {
		t.Fatal("both release archive environment variables are required")
	}
	destination := filepath.Join(t.TempDir(), "extract")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	extracted, err := extractManagerReleaseArchive(
		archivePath,
		destination,
		version,
	)
	if err != nil {
		t.Fatalf("validate packaged release archive: %v", err)
	}
	for _, required := range []string{
		"README.md",
		"CHANGELOG.md",
		"LICENSE",
		"src/mordhau-server-alpine-linux.sh",
		"src/web/go.mod",
	} {
		info, err := os.Stat(filepath.Join(extracted, required))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("packaged release is missing %s: %v", required, err)
		}
	}
}

func TestBeginManagerUpdatePersistsBeforeWorkerStart(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "manager-update.json")
	versionPath := filepath.Join(root, "manager-version")
	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	state := initialManagerUpdateState()
	state.LatestVersion = "2.4.0"
	state.ReleaseURL, _ = managerReleaseURL("2.4.0")
	state.CheckedAt = now.Add(-time.Minute)
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versionPath, []byte("2.3.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var started string
	manager := &Manager{
		managerUpdateStateFile:   statePath,
		managerUpdateVersionFile: versionPath,
		managerUpdateNow:         func() time.Time { return now },
		managerUpdateWorkerStart: func(target string) error {
			persisted, err := readManagerUpdateState(statePath)
			if err != nil {
				return err
			}
			if persisted.Status != "running" ||
				persisted.TargetVersion != target {
				return errors.New("running state was not persisted first")
			}
			started = target
			return nil
		},
	}
	view, err := manager.beginManagerUpdate(
		"2.4.0",
		"operator",
		"192.0.2.1",
	)
	if err != nil {
		t.Fatalf("begin update: %v", err)
	}
	if started != "2.4.0" || view.Status != "running" ||
		view.RequestedBy != "operator" {
		t.Fatalf("unexpected started update: %q, %+v", started, view)
	}
	if _, err := manager.beginManagerUpdate(
		"2.4.0",
		"operator",
		"192.0.2.1",
	); !errors.Is(err, errManagerUpdateBusy) {
		t.Fatalf("concurrent update error = %v", err)
	}
}

func TestBeginManagerUpdateRejectsLifecycleOperation(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "manager-update.json")
	versionPath := filepath.Join(root, "manager-version")
	state := initialManagerUpdateState()
	state.LatestVersion = "2.4.0"
	state.ReleaseURL, _ = managerReleaseURL("2.4.0")
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versionPath, []byte("2.3.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		managerUpdateStateFile:   statePath,
		managerUpdateVersionFile: versionPath,
		op:                       Operation{Running: true},
	}
	if _, err := manager.beginManagerUpdate(
		"2.4.0",
		"operator",
		"192.0.2.1",
	); !errors.Is(err, errManagerUpdateLifecycleBusy) {
		t.Fatalf("lifecycle conflict error = %v", err)
	}
	persisted, err := readManagerUpdateState(statePath)
	if err != nil || persisted.Status != "idle" {
		t.Fatalf("lifecycle conflict changed update state: %+v, %v", persisted, err)
	}
}

func TestServerActionHandlerRejectsRunningManagerUpdate(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "manager-update.json")
	state := initialManagerUpdateState()
	state.Status = "running"
	state.TargetVersion = "2.4.0"
	state.StartedAt = time.Now()
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	manager := &Manager{
		managerUpdateStateFile: statePath,
		operationStart: func(
			string,
			string,
			string,
			string,
		) error {
			called = true
			return nil
		},
	}
	session := Session{Username: "operator", CSRF: "csrf-token"}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://manager.example/api/server/action",
		strings.NewReader(`{"action":"restart"}`),
	)
	request.Header.Set("X-CSRF-Token", session.CSRF)
	response := httptest.NewRecorder()
	manager.serverActionHandler(response, request, session)
	if response.Code != http.StatusConflict || called {
		t.Fatalf(
			"server action during manager update: status=%d called=%t body=%s",
			response.Code,
			called,
			response.Body.String(),
		)
	}
}

func TestManagerUpdateWorkerLockDetection(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "manager-update.lock")
	active, err := managerUpdateWorkerActive(lockPath)
	if err != nil || active {
		t.Fatalf("unlocked worker state = %t, %v", active, err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := unix.Flock(
		int(lockFile.Fd()),
		unix.LOCK_EX|unix.LOCK_NB,
	); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
	active, err = managerUpdateWorkerActive(lockPath)
	if err != nil || !active {
		t.Fatalf("locked worker state = %t, %v", active, err)
	}
}

func TestReconcileInterruptedManagerUpdate(t *testing.T) {
	for _, test := range []struct {
		name             string
		installedVersion string
		wantStatus       string
	}{
		{
			name:             "interrupted",
			installedVersion: "2.3.3",
			wantStatus:       "failed",
		},
		{
			name:             "installer completed",
			installedVersion: "2.4.0",
			wantStatus:       "succeeded",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			statePath := filepath.Join(root, "manager-update.json")
			versionPath := filepath.Join(root, "manager-version")
			state := initialManagerUpdateState()
			state.Status = "running"
			state.TargetVersion = "2.4.0"
			state.StartedAt = time.Now().Add(-time.Minute)
			if err := writeJSONAtomic(statePath, state, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				versionPath,
				[]byte(test.installedVersion+"\n"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			manager := &Manager{
				managerUpdateStateFile:   statePath,
				managerUpdateVersionFile: versionPath,
				managerUpdateLockFile:    filepath.Join(root, "update.lock"),
				managerUpdateNow:         time.Now,
			}
			if err := manager.reconcileManagerUpdateState(); err != nil {
				t.Fatalf("reconcile manager update: %v", err)
			}
			persisted, err := readManagerUpdateState(statePath)
			if err != nil ||
				persisted.Status != test.wantStatus ||
				persisted.FinishedAt.IsZero() {
				t.Fatalf("reconciled state = %+v, %v", persisted, err)
			}
			if test.wantStatus == "failed" &&
				!strings.Contains(persisted.Error, "interrupted") {
				t.Fatalf("interrupted update error = %q", persisted.Error)
			}
		})
	}
}

func TestRunManagerUpdateWorkerDownloadsVerifiesAndInstalls(t *testing.T) {
	root := t.TempDir()
	version := "2.4.0"
	archiveName, _ := managerReleaseArchiveName(version)
	archive := validManagerTestArchive(t, version)
	sum := sha256.Sum256(archive)
	checksums := []byte(
		hex.EncodeToString(sum[:]) + "  " + archiveName + "\n",
	)
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			switch filepath.Base(request.URL.Path) {
			case archiveName:
				_, _ = response.Write(archive)
			case "SHA256SUMS":
				_, _ = response.Write(checksums)
			default:
				http.NotFound(response, request)
			}
		},
	))
	defer server.Close()

	statePath := filepath.Join(root, "manager-update.json")
	versionPath := filepath.Join(root, "manager-version")
	lockPath := filepath.Join(root, "manager-update.lock")
	state := initialManagerUpdateState()
	state.Status = "running"
	state.TargetVersion = version
	state.StartedAt = time.Now().Add(-time.Minute)
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versionPath, []byte("2.3.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	installerCalled := false
	config := managerUpdateWorkerConfig{
		StatePath:       statePath,
		VersionPath:     versionPath,
		LockPath:        lockPath,
		ReleaseBaseURL:  server.URL,
		HTTPClient:      server.Client(),
		Now:             time.Now,
		TemporaryParent: root,
		RunInstaller: func(ctx context.Context, installer string) error {
			installerCalled = true
			if !strings.HasSuffix(
				installer,
				"/src/mordhau-server-alpine-linux.sh",
			) {
				return fmt.Errorf("unexpected installer path %s", installer)
			}
			return os.WriteFile(versionPath, []byte(version+"\n"), 0600)
		},
		ServiceRunning: func(string) bool { return false },
		StartService:   func(string) error { return nil },
	}
	if err := runManagerUpdateWorker(
		context.Background(),
		version,
		config,
	); err != nil {
		t.Fatalf("run update worker: %v", err)
	}
	if !installerCalled {
		t.Fatal("verified installer was not executed")
	}
	installed, err := readInstalledManagerVersion(versionPath)
	if err != nil || installed != version {
		t.Fatalf("installed version = %q, %v", installed, err)
	}
	persisted, err := readManagerUpdateState(statePath)
	if err != nil ||
		persisted.Status != "succeeded" ||
		persisted.FinishedAt.IsZero() {
		t.Fatalf("worker result state = %+v, %v", persisted, err)
	}
}

func TestRunManagerUpdateWorkerRecordsInstallerFailureAndRestoresServices(
	t *testing.T,
) {
	root := t.TempDir()
	version := "2.4.0"
	archiveName, _ := managerReleaseArchiveName(version)
	archive := validManagerTestArchive(t, version)
	sum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			switch filepath.Base(request.URL.Path) {
			case archiveName:
				_, _ = response.Write(archive)
			case "SHA256SUMS":
				fmt.Fprintf(
					response,
					"%s  %s\n",
					hex.EncodeToString(sum[:]),
					archiveName,
				)
			default:
				http.NotFound(response, request)
			}
		},
	))
	defer server.Close()

	statePath := filepath.Join(root, "manager-update.json")
	versionPath := filepath.Join(root, "manager-version")
	state := initialManagerUpdateState()
	state.Status = "running"
	state.TargetVersion = version
	state.StartedAt = time.Now().Add(-time.Minute)
	if err := writeJSONAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versionPath, []byte("2.3.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	running := map[string]bool{
		"mordhau-server": true,
		"mordhau-web":    true,
	}
	var started []string
	config := managerUpdateWorkerConfig{
		StatePath:       statePath,
		VersionPath:     versionPath,
		LockPath:        filepath.Join(root, "manager-update.lock"),
		ReleaseBaseURL:  server.URL,
		HTTPClient:      server.Client(),
		Now:             time.Now,
		TemporaryParent: root,
		RunInstaller: func(context.Context, string) error {
			running["mordhau-server"] = false
			running["mordhau-web"] = false
			return errors.New("installation failed")
		},
		ServiceRunning: func(service string) bool {
			return running[service]
		},
		StartService: func(service string) error {
			started = append(started, service)
			running[service] = true
			return nil
		},
	}
	err := runManagerUpdateWorker(context.Background(), version, config)
	if err == nil || !strings.Contains(err.Error(), "installer failed") {
		t.Fatalf("installer failure result = %v", err)
	}
	if strings.Join(started, ",") != "mordhau-server,mordhau-web" {
		t.Fatalf("restored services = %q", started)
	}
	persisted, stateErr := readManagerUpdateState(statePath)
	if stateErr != nil ||
		persisted.Status != "failed" ||
		!strings.Contains(persisted.Error, "installer failed") {
		t.Fatalf("failed worker state = %+v, %v", persisted, stateErr)
	}
}
