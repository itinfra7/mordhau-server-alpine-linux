package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testSteamManifest = `"AppState"
{
	"appid"		"629800"
	"name"		"MORDHAU Dedicated Server"
	"buildid"		"16147292"
	"TargetBuildID"		"16147292"
}`

const testSteamAppInfo = `[2026-07-30 00:06:43] AppID : 629800
"629800"
{
	"common"
	{
		"name"		"MORDHAU Dedicated Server"
	}
	"depots"
	{
		"branches"
		{
			"public"
			{
				"buildid"		"17123456"
				"timeupdated"		"1780000000"
			}
			"beta"
			{
				"buildid"		"99999999"
			}
		}
	}
}`

func TestSteamManifestBuildID(t *testing.T) {
	buildID, err := steamManifestBuildID([]byte(testSteamManifest))
	if err != nil || buildID != "16147292" {
		t.Fatalf("manifest build = %q, %v", buildID, err)
	}
	for _, data := range []string{
		`"AppState" { "appid" "1" "buildid" "16147292" }`,
		`"AppState" { "appid" "629800" }`,
		`"appid" "629800"` + "\n" + `"buildid" "../bad"`,
	} {
		if _, err := steamManifestBuildID([]byte(data)); err == nil {
			t.Fatalf("invalid manifest accepted: %q", data)
		}
	}
}

func TestSteamUpdateCheckDueUsesPersistedHourlyInterval(t *testing.T) {
	checked := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	state := steamUpdateStateFile{
		Version:   steamUpdateStateVersion,
		CheckedAt: checked,
	}
	if steamUpdateCheckDue(state, checked.Add(59*time.Minute)) {
		t.Fatal("Steam update check became due before one hour")
	}
	if !steamUpdateCheckDue(state, checked.Add(time.Hour)) {
		t.Fatal("Steam update check did not become due after one hour")
	}
}

func TestSteamPublicBuildIDUsesPublicBranch(t *testing.T) {
	buildID, err := steamPublicBuildID([]byte(testSteamAppInfo))
	if err != nil || buildID != "17123456" {
		t.Fatalf("public build = %q, %v", buildID, err)
	}
	if _, err := steamPublicBuildID(
		[]byte(`"branches" { "beta" { "buildid" "1" } }`),
	); err == nil {
		t.Fatal("missing public build accepted")
	}
}

func TestSteamBuildFromCommandResultAcceptsMetadataAfterNonZeroExit(
	t *testing.T,
) {
	buildID, err := steamBuildFromCommandResult(
		[]byte(testSteamAppInfo),
		"Loading Steam API...",
		errors.New("exit status 1"),
	)
	if err != nil || buildID != "17123456" {
		t.Fatalf("Steam command result = %q, %v", buildID, err)
	}
}

func TestSteamBuildFromCommandResultRequiresCurrentMetadata(t *testing.T) {
	if _, err := steamBuildFromCommandResult(
		[]byte("Loading Steam API..."),
		"Loading Steam API...",
		errors.New("exit status 1"),
	); err == nil ||
		err.Error() != "SteamCMD update check failed: Loading Steam API..." {
		t.Fatalf("nonzero Steam command error = %v", err)
	}
	if _, err := steamBuildFromCommandResult(
		[]byte("Loading Steam API..."),
		"",
		nil,
	); err == nil ||
		err.Error() != "SteamCMD did not return the public MORDHAU build ID" {
		t.Fatalf("missing Steam metadata error = %v", err)
	}
}

func TestCheckSteamUpdatePersistsServerWideResult(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "steam-update.json")
	manifestPath := filepath.Join(root, "appmanifest_629800.acf")
	now := time.Date(2026, 7, 30, 2, 3, 4, 0, time.UTC)
	if err := writeJSONAtomic(
		statePath,
		steamUpdateStateFile{Version: steamUpdateStateVersion},
		0600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		manifestPath,
		[]byte(testSteamManifest),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	calls := 0
	manager := &Manager{
		steamUpdateStateFile:    statePath,
		steamUpdateManifestFile: manifestPath,
		steamUpdateNow:          func() time.Time { return now },
		steamUpdateRemoteBuild: func(context.Context) (string, error) {
			calls++
			return "17123456", nil
		},
	}
	view, err := manager.checkSteamUpdate(context.Background())
	if err != nil {
		t.Fatalf("check Steam update: %v", err)
	}
	if calls != 1 || !view.Available ||
		view.InstalledBuildID != "16147292" ||
		view.LatestBuildID != "17123456" ||
		!view.CheckedAt.Equal(now) {
		t.Fatalf("unexpected Steam update view: calls=%d, %+v", calls, view)
	}
	persisted, err := readSteamUpdateState(statePath)
	if err != nil || persisted.LatestBuildID != "17123456" ||
		!persisted.CheckedAt.Equal(now) {
		t.Fatalf("persisted Steam update = %+v, %v", persisted, err)
	}
}

func TestCheckSteamUpdateDoesNotPersistLifecycleBusyAsFailure(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "steam-update.json")
	manifestPath := filepath.Join(root, "appmanifest_629800.acf")
	previous := steamUpdateStateFile{
		Version:       steamUpdateStateVersion,
		LatestBuildID: "16147292",
		CheckedAt:     time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC),
	}
	if err := writeJSONAtomic(statePath, previous, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		manifestPath,
		[]byte(testSteamManifest),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		steamUpdateStateFile:    statePath,
		steamUpdateManifestFile: manifestPath,
		steamUpdateRemoteBuild: func(context.Context) (string, error) {
			return "", errSteamUpdateLifecycleBusy
		},
	}
	if _, err := manager.checkSteamUpdate(
		context.Background(),
	); !errors.Is(err, errSteamUpdateLifecycleBusy) {
		t.Fatalf("busy check error = %v", err)
	}
	persisted, err := readSteamUpdateState(statePath)
	if err != nil || persisted.CheckError != "" ||
		!persisted.CheckedAt.Equal(previous.CheckedAt) {
		t.Fatalf("busy state was persisted as failure: %+v, %v", persisted, err)
	}
}
