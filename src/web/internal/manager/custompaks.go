package manager

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const (
	customPakStateVersion        = 1
	customPakMaximumUploadBytes  = int64(8 << 30)
	customPakStorageReserveBytes = int64(1 << 30)
	customPakStateActive         = "active"
	customPakStateInactive       = "inactive"
	customPakStateDelete         = "delete"
	customPakLocationActive      = "active"
	customPakLocationInactive    = "inactive"
	customPakLocationUpload      = "upload"
	unicodeBridgeCustomPak       = "MordhauUnicodeBridge-WindowsServer.pak"
)

var (
	errCustomPakInvalid      = errors.New("invalid CustomPak")
	errCustomPakNotFound     = errors.New("CustomPak not found")
	errCustomPakConflict     = errors.New("CustomPak conflict")
	errCustomPakProtected    = errors.New("manager-owned CustomPak is protected")
	errCustomPakTooLarge     = errors.New("CustomPak exceeds the upload limit")
	errCustomPakStorage      = errors.New("insufficient storage for CustomPak upload")
	errCustomPakEmpty        = errors.New("CustomPak file is empty")
	errCustomPakDeleteAbsent = errors.New("CustomPak is not pending deletion")
	errCustomPakServerActive = errors.New("CustomPaks changes require the game server to be stopped")
)

type CustomPakItem struct {
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	ModifiedAt    string `json:"modified_at"`
	CurrentState  string `json:"current_state"`
	DesiredState  string `json:"desired_state"`
	PendingAction string `json:"pending_action,omitempty"`
	Enabled       bool   `json:"enabled"`
}

type CustomPaksView struct {
	Items                   []CustomPakItem `json:"items"`
	PendingChanges          bool            `json:"pending_changes"`
	PendingCount            int             `json:"pending_count"`
	ServerRunning           bool            `json:"server_running"`
	MaxUploadBytes          int64           `json:"max_upload_bytes"`
	ManagedPackagesExcluded int             `json:"managed_packages_excluded"`
}

type customPakAction struct {
	Name          string `json:"name"`
	DesiredState  string `json:"desired_state"`
	PreviousState string `json:"previous_state,omitempty"`
}

type customPakStateFile struct {
	Version int               `json:"version"`
	Actions []customPakAction `json:"actions"`
}

type customPakPaths struct {
	activeDir      string
	inactiveDir    string
	uploadDir      string
	statePath      string
	lifecycleLock  string
	maxUploadBytes int64
	reserveBytes   int64
}

type customPakDiskFile struct {
	name       string
	path       string
	location   string
	size       int64
	modifiedAt string
}

func defaultCustomPakPaths() customPakPaths {
	return customPakPaths{
		activeDir:      rootDir + "/Mordhau/Content/CustomPaks",
		inactiveDir:    stateDir + "/custompaks-inactive",
		uploadDir:      stateDir + "/custompaks-upload",
		statePath:      stateDir + "/custompaks-state.json",
		lifecycleLock:  stateDir + "/lifecycle.lock",
		maxUploadBytes: customPakMaximumUploadBytes,
		reserveBytes:   customPakStorageReserveBytes,
	}
}

func ensureCustomPakDirectories(paths customPakPaths) error {
	for _, directory := range []string{paths.activeDir, paths.inactiveDir, paths.uploadDir} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		if err := os.Chmod(directory, 0700); err != nil {
			return err
		}
	}
	return nil
}

func protectedCustomPakName(name string) bool {
	return strings.EqualFold(name, unicodeBridgeCustomPak)
}

func validateCustomPakName(name string) error {
	if name == "" || !utf8.ValidString(name) || len(name) > 240 {
		return fmt.Errorf("%w: filename must be valid UTF-8 and at most 240 bytes", errCustomPakInvalid)
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: filename must not contain a path", errCustomPakInvalid)
	}
	if strings.ContainsRune(name, ':') || strings.HasSuffix(name, " ") ||
		strings.HasSuffix(name, ".") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("%w: filename is not portable to the Windows server", errCustomPakInvalid)
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: filename contains a control character", errCustomPakInvalid)
		}
	}
	if !strings.EqualFold(filepath.Ext(name), ".pak") ||
		strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name))) == "" {
		return fmt.Errorf("%w: filename must end in .pak", errCustomPakInvalid)
	}
	if protectedCustomPakName(name) {
		return errCustomPakProtected
	}
	return nil
}

func customPakKey(name string) string {
	return strings.ToLower(name)
}

func scanCustomPakFilesAt(
	paths customPakPaths,
) (map[string]customPakDiskFile, int, error) {
	if err := ensureCustomPakDirectories(paths); err != nil {
		return nil, 0, err
	}
	files := make(map[string]customPakDiskFile)
	managedPackages := 0
	directories := []struct {
		path     string
		location string
	}{
		{paths.activeDir, customPakLocationActive},
		{paths.inactiveDir, customPakLocationInactive},
		{paths.uploadDir, customPakLocationUpload},
	}
	for _, directory := range directories {
		entries, err := os.ReadDir(directory.path)
		if err != nil {
			return nil, 0, err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".pak") {
				continue
			}
			if protectedCustomPakName(entry.Name()) {
				managedPackages++
				continue
			}
			if err := validateCustomPakName(entry.Name()); err != nil {
				return nil, 0, err
			}
			info, err := entry.Info()
			if err != nil {
				return nil, 0, err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			key := customPakKey(entry.Name())
			if existing, found := files[key]; found {
				return nil, 0, fmt.Errorf(
					"%w: %q exists in both %s and %s storage",
					errCustomPakConflict,
					entry.Name(),
					existing.location,
					directory.location,
				)
			}
			files[key] = customPakDiskFile{
				name:       entry.Name(),
				path:       filepath.Join(directory.path, entry.Name()),
				location:   directory.location,
				size:       info.Size(),
				modifiedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z"),
			}
		}
	}
	return files, managedPackages, nil
}

func loadCustomPakActionsAt(paths customPakPaths) (map[string]customPakAction, error) {
	var state customPakStateFile
	if err := readJSON(paths.statePath, &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]customPakAction), nil
		}
		return nil, err
	}
	if state.Version != customPakStateVersion {
		return nil, errors.New("unsupported CustomPaks state version")
	}
	if len(state.Actions) > 1000 {
		return nil, errors.New("CustomPaks state contains too many actions")
	}
	actions := make(map[string]customPakAction, len(state.Actions))
	for _, action := range state.Actions {
		if err := validateCustomPakName(action.Name); err != nil {
			return nil, err
		}
		switch action.DesiredState {
		case customPakStateActive, customPakStateInactive, customPakStateDelete:
		default:
			return nil, errors.New("CustomPaks state contains an invalid desired state")
		}
		if action.PreviousState != "" &&
			(action.DesiredState != customPakStateDelete ||
				(action.PreviousState != customPakStateActive &&
					action.PreviousState != customPakStateInactive)) {
			return nil, errors.New("CustomPaks state contains an invalid previous state")
		}
		key := customPakKey(action.Name)
		if _, duplicate := actions[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate action for %q", errCustomPakConflict, action.Name)
		}
		actions[key] = action
	}
	return actions, nil
}

func saveCustomPakActionsAt(
	paths customPakPaths,
	actions map[string]customPakAction,
) error {
	if len(actions) == 0 {
		if err := os.Remove(paths.statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	state := customPakStateFile{
		Version: customPakStateVersion,
		Actions: make([]customPakAction, 0, len(actions)),
	}
	for _, action := range actions {
		state.Actions = append(state.Actions, action)
	}
	sort.Slice(state.Actions, func(left, right int) bool {
		return strings.ToLower(state.Actions[left].Name) <
			strings.ToLower(state.Actions[right].Name)
	})
	return writeJSONAtomic(paths.statePath, state, 0600)
}

func customPakUploadLimitAt(paths customPakPaths) (int64, error) {
	var fileSystem syscall.Statfs_t
	if err := syscall.Statfs(paths.uploadDir, &fileSystem); err != nil {
		return 0, err
	}
	available := int64(fileSystem.Bavail) * int64(fileSystem.Bsize)
	if available <= paths.reserveBytes {
		return 0, nil
	}
	available -= paths.reserveBytes
	if available > paths.maxUploadBytes {
		return paths.maxUploadBytes, nil
	}
	return available, nil
}

func customPaksViewAt(paths customPakPaths, running bool) (CustomPaksView, error) {
	files, managedPackages, err := scanCustomPakFilesAt(paths)
	if err != nil {
		return CustomPaksView{}, err
	}
	actions, err := loadCustomPakActionsAt(paths)
	if err != nil {
		return CustomPaksView{}, err
	}
	limit, err := customPakUploadLimitAt(paths)
	if err != nil {
		return CustomPaksView{}, err
	}
	view := CustomPaksView{
		Items:                   make([]CustomPakItem, 0, len(files)),
		ServerRunning:           running,
		MaxUploadBytes:          limit,
		ManagedPackagesExcluded: managedPackages,
	}
	for key, file := range files {
		action, hasAction := actions[key]
		desiredState := customPakStateActive
		if file.location == customPakLocationInactive {
			desiredState = customPakStateInactive
		}
		if hasAction {
			desiredState = action.DesiredState
		} else if file.location == customPakLocationUpload {
			desiredState = customPakStateActive
		}

		pendingAction := ""
		switch {
		case desiredState == customPakStateDelete:
			pendingAction = customPakStateDelete
		case file.location == customPakLocationUpload:
			pendingAction = "install"
		case file.location == customPakLocationActive &&
			desiredState == customPakStateInactive:
			pendingAction = "deactivate"
		case file.location == customPakLocationInactive &&
			desiredState == customPakStateActive:
			pendingAction = "activate"
		}
		if pendingAction != "" {
			view.PendingChanges = true
			view.PendingCount++
		}
		currentState := file.location
		if currentState == customPakLocationUpload {
			currentState = "uploaded"
		}
		view.Items = append(view.Items, CustomPakItem{
			Name:          file.name,
			Size:          file.size,
			ModifiedAt:    file.modifiedAt,
			CurrentState:  currentState,
			DesiredState:  desiredState,
			PendingAction: pendingAction,
			Enabled:       desiredState == customPakStateActive,
		})
	}
	for key := range actions {
		if _, found := files[key]; !found {
			view.PendingChanges = true
			view.PendingCount++
		}
	}
	sort.Slice(view.Items, func(left, right int) bool {
		return strings.ToLower(view.Items[left].Name) <
			strings.ToLower(view.Items[right].Name)
	})
	return view, nil
}

func setCustomPakEnabledAt(paths customPakPaths, name string, enabled bool) error {
	if err := validateCustomPakName(name); err != nil {
		return err
	}
	files, _, err := scanCustomPakFilesAt(paths)
	if err != nil {
		return err
	}
	key := customPakKey(name)
	file, found := files[key]
	if !found {
		return errCustomPakNotFound
	}
	actions, err := loadCustomPakActionsAt(paths)
	if err != nil {
		return err
	}
	desiredState := customPakStateInactive
	if enabled {
		desiredState = customPakStateActive
	}
	switch file.location {
	case customPakLocationUpload:
		actions[key] = customPakAction{Name: file.name, DesiredState: desiredState}
	case customPakLocationActive:
		if enabled {
			delete(actions, key)
		} else {
			actions[key] = customPakAction{
				Name:         file.name,
				DesiredState: customPakStateInactive,
			}
		}
	case customPakLocationInactive:
		if enabled {
			actions[key] = customPakAction{
				Name:         file.name,
				DesiredState: customPakStateActive,
			}
		} else {
			delete(actions, key)
		}
	default:
		return errors.New("CustomPak is stored in an unsupported location")
	}
	return saveCustomPakActionsAt(paths, actions)
}

func stageCustomPakDeletionAt(paths customPakPaths, name string) error {
	if err := validateCustomPakName(name); err != nil {
		return err
	}
	files, _, err := scanCustomPakFilesAt(paths)
	if err != nil {
		return err
	}
	key := customPakKey(name)
	file, found := files[key]
	if !found {
		return errCustomPakNotFound
	}
	actions, err := loadCustomPakActionsAt(paths)
	if err != nil {
		return err
	}
	previousState := customPakStateActive
	if file.location == customPakLocationInactive {
		previousState = customPakStateInactive
	}
	if existing, pending := actions[key]; pending &&
		existing.DesiredState != customPakStateDelete {
		previousState = existing.DesiredState
	}
	actions[key] = customPakAction{
		Name:          file.name,
		DesiredState:  customPakStateDelete,
		PreviousState: previousState,
	}
	return saveCustomPakActionsAt(paths, actions)
}

func cancelCustomPakDeletionAt(paths customPakPaths, name string) error {
	if err := validateCustomPakName(name); err != nil {
		return err
	}
	files, _, err := scanCustomPakFilesAt(paths)
	if err != nil {
		return err
	}
	key := customPakKey(name)
	file, found := files[key]
	if !found {
		return errCustomPakNotFound
	}
	actions, err := loadCustomPakActionsAt(paths)
	if err != nil {
		return err
	}
	action, found := actions[key]
	if !found || action.DesiredState != customPakStateDelete {
		return errCustomPakDeleteAbsent
	}
	desiredState := action.PreviousState
	if desiredState == "" {
		desiredState = customPakStateActive
		if file.location == customPakLocationInactive {
			desiredState = customPakStateInactive
		}
	}
	currentState := customPakStateActive
	if file.location == customPakLocationInactive {
		currentState = customPakStateInactive
	}
	if file.location != customPakLocationUpload && desiredState == currentState {
		delete(actions, key)
	} else {
		actions[key] = customPakAction{
			Name:         file.name,
			DesiredState: desiredState,
		}
	}
	return saveCustomPakActionsAt(paths, actions)
}

func stageCustomPakUploadAt(
	paths customPakPaths,
	name string,
	source io.Reader,
) (int64, error) {
	if err := validateCustomPakName(name); err != nil {
		return 0, err
	}
	if err := ensureCustomPakDirectories(paths); err != nil {
		return 0, err
	}
	files, _, err := scanCustomPakFilesAt(paths)
	if err != nil {
		return 0, err
	}
	key := customPakKey(name)
	if existing, found := files[key]; found {
		return 0, fmt.Errorf(
			"%w: %q already exists in %s storage",
			errCustomPakConflict,
			existing.name,
			existing.location,
		)
	}
	actions, err := loadCustomPakActionsAt(paths)
	if err != nil {
		return 0, err
	}
	if _, found := actions[key]; found {
		return 0, fmt.Errorf("%w: an action for %q already exists", errCustomPakConflict, name)
	}
	limit, err := customPakUploadLimitAt(paths)
	if err != nil {
		return 0, err
	}
	if limit < 1 {
		return 0, errCustomPakStorage
	}

	id, err := randomID()
	if err != nil {
		return 0, err
	}
	temporaryPath := filepath.Join(paths.uploadDir, ".custompak-upload-"+id)
	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, err
	}
	keepTemporary := false
	defer func() {
		_ = file.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	written, copyErr := io.Copy(file, io.LimitReader(source, limit+1))
	if copyErr != nil {
		if errors.Is(copyErr, syscall.ENOSPC) || errors.Is(copyErr, syscall.EDQUOT) {
			return 0, fmt.Errorf("%w: %v", errCustomPakStorage, copyErr)
		}
		return 0, copyErr
	}
	if written > limit {
		if limit < paths.maxUploadBytes {
			return 0, errCustomPakStorage
		}
		return 0, errCustomPakTooLarge
	}
	if written == 0 {
		return 0, errCustomPakEmpty
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	if err := os.Chmod(temporaryPath, 0644); err != nil {
		return 0, err
	}
	files, _, err = scanCustomPakFilesAt(paths)
	if err != nil {
		return 0, err
	}
	if existing, found := files[key]; found {
		return 0, fmt.Errorf(
			"%w: %q already exists in %s storage",
			errCustomPakConflict,
			existing.name,
			existing.location,
		)
	}
	destination := filepath.Join(paths.uploadDir, name)
	if err := os.Link(temporaryPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, errCustomPakConflict
		}
		return 0, err
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(destination)
		return 0, err
	}
	keepTemporary = true

	actions[key] = customPakAction{Name: name, DesiredState: customPakStateActive}
	if err := saveCustomPakActionsAt(paths, actions); err != nil {
		_ = os.Remove(destination)
		return 0, err
	}
	return written, nil
}

func customPakDestinationAvailable(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: destination %q already exists", errCustomPakConflict, path)
}

func applyPendingCustomPaksAt(paths customPakPaths) (int, error) {
	if err := ensureCustomPakDirectories(paths); err != nil {
		return 0, err
	}
	files, _, err := scanCustomPakFilesAt(paths)
	if err != nil {
		return 0, err
	}
	actions, err := loadCustomPakActionsAt(paths)
	if err != nil {
		return 0, err
	}
	for key, file := range files {
		if file.location == customPakLocationUpload {
			if _, found := actions[key]; !found {
				actions[key] = customPakAction{
					Name:         file.name,
					DesiredState: customPakStateActive,
				}
			}
		}
	}
	if len(actions) == 0 {
		return 0, nil
	}

	ordered := make([]customPakAction, 0, len(actions))
	for _, action := range actions {
		ordered = append(ordered, action)
	}
	sort.Slice(ordered, func(left, right int) bool {
		return strings.ToLower(ordered[left].Name) <
			strings.ToLower(ordered[right].Name)
	})

	for _, action := range ordered {
		key := customPakKey(action.Name)
		file, found := files[key]
		if !found && action.DesiredState != customPakStateDelete {
			return 0, fmt.Errorf("%w: %q disappeared before apply", errCustomPakNotFound, action.Name)
		}
		if !found {
			continue
		}
		switch action.DesiredState {
		case customPakStateActive:
			if file.location != customPakLocationActive {
				if err := customPakDestinationAvailable(
					filepath.Join(paths.activeDir, file.name),
				); err != nil {
					return 0, err
				}
			}
		case customPakStateInactive:
			if file.location != customPakLocationInactive {
				if err := customPakDestinationAvailable(
					filepath.Join(paths.inactiveDir, file.name),
				); err != nil {
					return 0, err
				}
			}
		case customPakStateDelete:
		default:
			return 0, errors.New("invalid CustomPak desired state")
		}
	}

	for _, action := range ordered {
		key := customPakKey(action.Name)
		file, found := files[key]
		switch action.DesiredState {
		case customPakStateDelete:
			if found {
				if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return 0, err
				}
			}
		case customPakStateActive:
			if file.location != customPakLocationActive {
				destination := filepath.Join(paths.activeDir, file.name)
				if err := os.Rename(file.path, destination); err != nil {
					return 0, err
				}
				if err := os.Chmod(destination, 0644); err != nil {
					return 0, err
				}
			}
		case customPakStateInactive:
			if file.location != customPakLocationInactive {
				destination := filepath.Join(paths.inactiveDir, file.name)
				if err := os.Rename(file.path, destination); err != nil {
					return 0, err
				}
				if err := os.Chmod(destination, 0644); err != nil {
					return 0, err
				}
			}
		}
	}
	if err := os.Remove(paths.statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	return len(ordered), nil
}

func ApplyPendingCustomPaks() (int, error) {
	if serverRunning() {
		return 0, errCustomPakServerActive
	}
	return applyPendingCustomPaksAt(defaultCustomPakPaths())
}

func (m *Manager) customPaksView() (CustomPaksView, error) {
	m.customPaksMu.Lock()
	defer m.customPaksMu.Unlock()
	return customPaksViewAt(m.customPakPaths, serverRunning())
}

func (m *Manager) mutateCustomPaks(
	mutation func(customPakPaths) error,
) (CustomPaksView, error) {
	m.customPaksMu.Lock()
	defer m.customPaksMu.Unlock()
	lock, err := acquireLifecycleLockAt(m.customPakPaths.lifecycleLock)
	if err != nil {
		return CustomPaksView{}, err
	}
	defer releaseLifecycleLock(lock)
	if err := mutation(m.customPakPaths); err != nil {
		return CustomPaksView{}, err
	}
	return customPaksViewAt(m.customPakPaths, serverRunning())
}

func (m *Manager) setCustomPakEnabled(name string, enabled bool) (CustomPaksView, error) {
	return m.mutateCustomPaks(func(paths customPakPaths) error {
		return setCustomPakEnabledAt(paths, name, enabled)
	})
}

func (m *Manager) stageCustomPakDeletion(name string) (CustomPaksView, error) {
	return m.mutateCustomPaks(func(paths customPakPaths) error {
		return stageCustomPakDeletionAt(paths, name)
	})
}

func (m *Manager) cancelCustomPakDeletion(name string) (CustomPaksView, error) {
	return m.mutateCustomPaks(func(paths customPakPaths) error {
		return cancelCustomPakDeletionAt(paths, name)
	})
}

func (m *Manager) stageCustomPakUpload(
	name string,
	source io.Reader,
) (CustomPaksView, int64, error) {
	m.customPaksMu.Lock()
	defer m.customPaksMu.Unlock()
	lock, err := acquireLifecycleLockAt(m.customPakPaths.lifecycleLock)
	if err != nil {
		return CustomPaksView{}, 0, err
	}
	defer releaseLifecycleLock(lock)
	written, err := stageCustomPakUploadAt(m.customPakPaths, name, source)
	if err != nil {
		return CustomPaksView{}, 0, err
	}
	view, err := customPaksViewAt(m.customPakPaths, serverRunning())
	return view, written, err
}
