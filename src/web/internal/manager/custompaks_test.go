package manager

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCustomPakPaths(t *testing.T) customPakPaths {
	t.Helper()
	root := t.TempDir()
	paths := customPakPaths{
		activeDir:      filepath.Join(root, "active"),
		inactiveDir:    filepath.Join(root, "inactive"),
		uploadDir:      filepath.Join(root, "upload"),
		statePath:      filepath.Join(root, "state", "custompaks-state.json"),
		lifecycleLock:  filepath.Join(root, "state", "lifecycle.lock"),
		maxUploadBytes: 1 << 20,
		reserveBytes:   0,
	}
	if err := os.MkdirAll(filepath.Dir(paths.statePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := ensureCustomPakDirectories(paths); err != nil {
		t.Fatal(err)
	}
	return paths
}

func writeTestCustomPak(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func customPakItemByName(t *testing.T, view CustomPaksView, name string) CustomPakItem {
	t.Helper()
	for _, item := range view.Items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("CustomPak %q not found in %#v", name, view.Items)
	return CustomPakItem{}
}

func TestValidateCustomPakNameAndManagerPackageProtection(t *testing.T) {
	for _, name := range []string{"Manual.pak", "한국어 모드.PAK", "Dread_2-v1.2.3.pak"} {
		if err := validateCustomPakName(name); err != nil {
			t.Errorf("validateCustomPakName(%q): %v", name, err)
		}
	}
	for _, name := range []string{
		"",
		".pak",
		".hidden.pak",
		"not-a-pak.zip",
		"folder/mod.pak",
		`folder\mod.pak`,
		"bad:name.pak",
		"trailing.pak.",
		"control\nname.pak",
	} {
		if err := validateCustomPakName(name); !errors.Is(err, errCustomPakInvalid) {
			t.Errorf("validateCustomPakName(%q) = %v, want invalid", name, err)
		}
	}
	if err := validateCustomPakName(unicodeBridgeCustomPak); !errors.Is(err, errCustomPakProtected) {
		t.Fatalf("manager package validation = %v, want protected", err)
	}
}

func TestCustomPaksViewExcludesManagedPackagesAndShowsPendingState(t *testing.T) {
	paths := testCustomPakPaths(t)
	writeTestCustomPak(t, paths.activeDir, unicodeBridgeCustomPak, "managed")
	writeTestCustomPak(t, paths.activeDir, "Active.pak", "active")
	writeTestCustomPak(t, paths.inactiveDir, "Inactive.pak", "inactive")
	writeTestCustomPak(t, paths.uploadDir, "Upload.pak", "upload")
	actions := map[string]customPakAction{
		customPakKey("Active.pak"): {
			Name:         "Active.pak",
			DesiredState: customPakStateInactive,
		},
		customPakKey("Upload.pak"): {
			Name:         "Upload.pak",
			DesiredState: customPakStateActive,
		},
	}
	if err := saveCustomPakActionsAt(paths, actions); err != nil {
		t.Fatal(err)
	}

	view, err := customPaksViewAt(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	if !view.ServerRunning || view.ManagedPackagesExcluded != 1 {
		t.Fatalf("unexpected server/managed state: %+v", view)
	}
	if len(view.Items) != 3 || !view.PendingChanges || view.PendingCount != 2 {
		t.Fatalf("unexpected CustomPaks view: %+v", view)
	}
	active := customPakItemByName(t, view, "Active.pak")
	if active.CurrentState != customPakLocationActive ||
		active.DesiredState != customPakStateInactive ||
		active.PendingAction != "deactivate" ||
		active.Enabled {
		t.Fatalf("unexpected active item: %+v", active)
	}
	inactive := customPakItemByName(t, view, "Inactive.pak")
	if inactive.CurrentState != customPakLocationInactive ||
		inactive.PendingAction != "" ||
		inactive.Enabled {
		t.Fatalf("unexpected inactive item: %+v", inactive)
	}
	upload := customPakItemByName(t, view, "Upload.pak")
	if upload.CurrentState != "uploaded" ||
		upload.PendingAction != "install" ||
		!upload.Enabled {
		t.Fatalf("unexpected uploaded item: %+v", upload)
	}
}

func TestApplyPendingCustomPaksMovesAndDeletesOnNextLaunch(t *testing.T) {
	paths := testCustomPakPaths(t)
	writeTestCustomPak(t, paths.activeDir, unicodeBridgeCustomPak, "managed")
	writeTestCustomPak(t, paths.activeDir, "Disable.pak", "disable")
	writeTestCustomPak(t, paths.inactiveDir, "Enable.pak", "enable")
	writeTestCustomPak(t, paths.activeDir, "Delete.pak", "delete")

	if err := setCustomPakEnabledAt(paths, "Disable.pak", false); err != nil {
		t.Fatal(err)
	}
	if err := setCustomPakEnabledAt(paths, "Enable.pak", true); err != nil {
		t.Fatal(err)
	}
	if err := stageCustomPakDeletionAt(paths, "Delete.pak"); err != nil {
		t.Fatal(err)
	}
	if written, err := stageCustomPakUploadAt(
		paths,
		"Install.pak",
		bytes.NewBufferString("install"),
	); err != nil || written != int64(len("install")) {
		t.Fatalf("stage upload = %d, %v", written, err)
	}

	applied, err := applyPendingCustomPaksAt(paths)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 4 {
		t.Fatalf("applied changes = %d, want 4", applied)
	}
	for _, path := range []string{
		filepath.Join(paths.activeDir, unicodeBridgeCustomPak),
		filepath.Join(paths.activeDir, "Enable.pak"),
		filepath.Join(paths.activeDir, "Install.pak"),
		filepath.Join(paths.inactiveDir, "Disable.pak"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s: %v", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(paths.activeDir, "Disable.pak"),
		filepath.Join(paths.inactiveDir, "Enable.pak"),
		filepath.Join(paths.activeDir, "Delete.pak"),
		filepath.Join(paths.uploadDir, "Install.pak"),
		paths.statePath,
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("unexpected remaining path %s: %v", path, err)
		}
	}
	if applied, err := applyPendingCustomPaksAt(paths); err != nil || applied != 0 {
		t.Fatalf("second apply = %d, %v; want no changes", applied, err)
	}
}

func TestCustomPakDeletionCanBeCanceled(t *testing.T) {
	paths := testCustomPakPaths(t)
	writeTestCustomPak(t, paths.inactiveDir, "Keep.pak", "keep")
	writeTestCustomPak(t, paths.activeDir, "KeepPendingState.pak", "keep-pending")
	if err := stageCustomPakDeletionAt(paths, "Keep.pak"); err != nil {
		t.Fatal(err)
	}
	if err := cancelCustomPakDeletionAt(paths, "Keep.pak"); err != nil {
		t.Fatal(err)
	}
	view, err := customPaksViewAt(paths, false)
	if err != nil {
		t.Fatal(err)
	}
	item := customPakItemByName(t, view, "Keep.pak")
	if item.PendingAction != "" || item.Enabled || view.PendingChanges {
		t.Fatalf("deletion was not canceled: item=%+v view=%+v", item, view)
	}
	if err := cancelCustomPakDeletionAt(paths, "Keep.pak"); !errors.Is(
		err,
		errCustomPakDeleteAbsent,
	) {
		t.Fatalf("second deletion cancel = %v", err)
	}

	if err := setCustomPakEnabledAt(paths, "KeepPendingState.pak", false); err != nil {
		t.Fatal(err)
	}
	if err := stageCustomPakDeletionAt(paths, "KeepPendingState.pak"); err != nil {
		t.Fatal(err)
	}
	if err := cancelCustomPakDeletionAt(paths, "KeepPendingState.pak"); err != nil {
		t.Fatal(err)
	}
	view, err = customPaksViewAt(paths, false)
	if err != nil {
		t.Fatal(err)
	}
	restored := customPakItemByName(t, view, "KeepPendingState.pak")
	if restored.PendingAction != "deactivate" || restored.Enabled {
		t.Fatalf("pre-deletion desired state was not restored: %+v", restored)
	}
}

func TestCustomPakUploadRejectsEmptyOversizeAndDuplicateFiles(t *testing.T) {
	paths := testCustomPakPaths(t)
	paths.maxUploadBytes = 3
	if _, err := stageCustomPakUploadAt(
		paths,
		"Empty.pak",
		bytes.NewReader(nil),
	); !errors.Is(err, errCustomPakEmpty) {
		t.Fatalf("empty upload = %v", err)
	}
	if _, err := stageCustomPakUploadAt(
		paths,
		"Large.pak",
		bytes.NewBufferString("four"),
	); !errors.Is(err, errCustomPakTooLarge) {
		t.Fatalf("oversize upload = %v", err)
	}
	if _, err := stageCustomPakUploadAt(
		paths,
		"Unique.pak",
		bytes.NewBufferString("one"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := stageCustomPakUploadAt(
		paths,
		"unique.PAK",
		bytes.NewBufferString("two"),
	); !errors.Is(err, errCustomPakConflict) {
		t.Fatalf("case-insensitive duplicate = %v", err)
	}
}

func TestCustomPakMutationRefusesBusyLifecycle(t *testing.T) {
	paths := testCustomPakPaths(t)
	writeTestCustomPak(t, paths.activeDir, "Busy.pak", "busy")
	lock, err := acquireLifecycleLockAt(paths.lifecycleLock)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLifecycleLock(lock)

	manager := &Manager{customPakPaths: paths}
	if _, err := manager.setCustomPakEnabled("Busy.pak", false); !errors.Is(
		err,
		errLifecycleBusy,
	) {
		t.Fatalf("mutation during lifecycle operation = %v", err)
	}
}

func TestServerLauncherAppliesCustomPaksImmediatelyBeforeLaunch(t *testing.T) {
	data, err := os.ReadFile("../../../templates/server.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	sequence := "        update_server\n" +
		"        apply_pending_config\n" +
		"        apply_pending_custompaks\n" +
		"        launch_server"
	if count := strings.Count(script, sequence); count != 2 {
		t.Fatalf("next-launch CustomPaks sequence count = %d, want start and restart", count)
	}
	for _, expected := range []string{
		`"$WEB_MANAGER" --apply-custompaks`,
		`"$STATE_DIR/custompaks-inactive"`,
		`"$STATE_DIR/custompaks-upload"`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("server launcher is missing %q", expected)
		}
	}
}
