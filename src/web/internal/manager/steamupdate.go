package manager

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	steamAppID                  = "629800"
	steamUpdateStatePath        = stateDir + "/steam-update.json"
	steamAppManifestPath        = rootDir + "/steamapps/appmanifest_629800.acf"
	steamConsoleLogPath         = "/root/steamcmd/logs/console_log.txt"
	steamCMDPath                = "/root/steamcmd/steamcmd.exe"
	lifecycleLockPath           = stateDir + "/lifecycle.lock"
	steamUpdateStateVersion     = 1
	steamUpdateCheckInterval    = time.Hour
	steamUpdateCommandTimeout   = 2 * time.Minute
	steamUpdateConsoleReadLimit = 2 << 20
)

var errSteamUpdateLifecycleBusy = errors.New(
	"a dedicated-server lifecycle operation is in progress",
)

type steamUpdateStateFile struct {
	Version       int       `json:"version"`
	LatestBuildID string    `json:"latest_build_id,omitempty"`
	CheckedAt     time.Time `json:"checked_at,omitempty"`
	CheckError    string    `json:"check_error,omitempty"`
}

type SteamUpdateView struct {
	InstalledBuildID string    `json:"installed_build_id"`
	LatestBuildID    string    `json:"latest_build_id,omitempty"`
	Available        bool      `json:"available"`
	CheckedAt        time.Time `json:"checked_at,omitempty"`
	CheckError       string    `json:"check_error,omitempty"`
}

func validSteamBuildID(value string) bool {
	if value == "" || len(value) > 20 {
		return false
	}
	if len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func quotedValveFields(line string) ([]string, bool) {
	var fields []string
	for index := 0; index < len(line); {
		for index < len(line) && (line[index] == ' ' || line[index] == '\t') {
			index++
		}
		if index == len(line) {
			break
		}
		if line[index] != '"' {
			return nil, false
		}
		index++
		var value strings.Builder
		closed := false
		for index < len(line) {
			character := line[index]
			index++
			if character == '"' {
				closed = true
				break
			}
			if character == '\\' && index < len(line) {
				next := line[index]
				if next == '\\' || next == '"' {
					value.WriteByte(next)
					index++
					continue
				}
			}
			value.WriteByte(character)
		}
		if !closed {
			return nil, false
		}
		fields = append(fields, value.String())
	}
	return fields, len(fields) > 0
}

func steamManifestBuildID(data []byte) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	appID := ""
	buildID := ""
	for scanner.Scan() {
		fields, ok := quotedValveFields(strings.TrimSpace(scanner.Text()))
		if !ok || len(fields) != 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "appid":
			appID = fields[1]
		case "buildid":
			if buildID == "" {
				buildID = fields[1]
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if appID != steamAppID || !validSteamBuildID(buildID) {
		return "", errors.New("MORDHAU Steam app manifest is invalid")
	}
	return buildID, nil
}

func steamPublicBuildID(data []byte) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var stack []string
	pendingSection := ""
	buildID := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "{":
			if pendingSection == "" {
				continue
			}
			stack = append(stack, pendingSection)
			pendingSection = ""
			continue
		case "}":
			pendingSection = ""
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		fields, ok := quotedValveFields(line)
		if !ok {
			continue
		}
		if len(fields) == 1 {
			pendingSection = fields[0]
			continue
		}
		pendingSection = ""
		if len(fields) != 2 || strings.ToLower(fields[0]) != "buildid" {
			continue
		}
		if len(stack) >= 2 &&
			stack[len(stack)-2] == "branches" &&
			stack[len(stack)-1] == "public" {
			buildID = fields[1]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if !validSteamBuildID(buildID) {
		return "", errors.New("SteamCMD did not return the public MORDHAU build ID")
	}
	return buildID, nil
}

func steamBuildFromCommandResult(
	consoleData []byte,
	commandOutput string,
	commandErr error,
) (string, error) {
	buildID, parseErr := steamPublicBuildID(consoleData)
	if parseErr == nil {
		return buildID, nil
	}
	if commandErr == nil {
		return "", parseErr
	}
	message := strings.TrimSpace(commandOutput)
	if message != "" {
		return "", fmt.Errorf(
			"SteamCMD update check failed: %s",
			boundedManagerUpdateText(message, 256),
		)
	}
	return "", fmt.Errorf("SteamCMD update check failed: %w", commandErr)
}

func readFileFromOffset(path string, offset int64, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("SteamCMD console output exceeds the permitted size")
	}
	return data, nil
}

func (m *Manager) steamUpdateStatePath() string {
	if m.steamUpdateStateFile != "" {
		return m.steamUpdateStateFile
	}
	return steamUpdateStatePath
}

func (m *Manager) steamManifestPath() string {
	if m.steamUpdateManifestFile != "" {
		return m.steamUpdateManifestFile
	}
	return steamAppManifestPath
}

func (m *Manager) steamUpdateNowValue() time.Time {
	if m.steamUpdateNow != nil {
		return m.steamUpdateNow()
	}
	return time.Now()
}

func validSteamUpdateState(state steamUpdateStateFile) bool {
	return state.Version == steamUpdateStateVersion &&
		(state.LatestBuildID == "" || validSteamBuildID(state.LatestBuildID)) &&
		len(state.CheckError) <= managerUpdateErrorLimit+len("…")
}

func readSteamUpdateState(path string) (steamUpdateStateFile, error) {
	var state steamUpdateStateFile
	if err := readJSON(path, &state); err != nil {
		return state, err
	}
	if !validSteamUpdateState(state) {
		return state, errors.New("stored Steam update state is invalid")
	}
	return state, nil
}

func (m *Manager) loadOrCreateSteamUpdateState() error {
	path := m.steamUpdateStatePath()
	if _, err := readSteamUpdateState(path); err == nil {
		return os.Chmod(path, 0600)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load Steam update state: %w", err)
	}
	state := steamUpdateStateFile{Version: steamUpdateStateVersion}
	if err := writeJSONAtomic(path, state, 0600); err != nil {
		return fmt.Errorf("create Steam update state: %w", err)
	}
	return nil
}

func (m *Manager) installedSteamBuildID() (string, error) {
	data, err := os.ReadFile(m.steamManifestPath())
	if err != nil {
		return "", err
	}
	return steamManifestBuildID(data)
}

func steamUpdateView(
	state steamUpdateStateFile,
	installedBuildID string,
) SteamUpdateView {
	return SteamUpdateView{
		InstalledBuildID: installedBuildID,
		LatestBuildID:    state.LatestBuildID,
		Available: state.LatestBuildID != "" &&
			state.LatestBuildID != installedBuildID,
		CheckedAt:  state.CheckedAt,
		CheckError: state.CheckError,
	}
}

func (m *Manager) currentSteamUpdateView() (SteamUpdateView, error) {
	state, err := readSteamUpdateState(m.steamUpdateStatePath())
	if err != nil {
		return SteamUpdateView{}, err
	}
	installed, err := m.installedSteamBuildID()
	if err != nil {
		return SteamUpdateView{}, err
	}
	return steamUpdateView(state, installed), nil
}

func (m *Manager) querySteamRemoteBuild(ctx context.Context) (string, error) {
	if m.managerUpdateRunning() {
		return "", errSteamUpdateLifecycleBusy
	}
	lockPath := m.steamUpdateLifecycleLock
	if lockPath == "" {
		lockPath = lifecycleLockPath
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer lockFile.Close()
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return "", errSteamUpdateLifecycleBusy
	}
	defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)

	consolePath := m.steamUpdateConsoleFile
	if consolePath == "" {
		consolePath = steamConsoleLogPath
	}
	var consoleOffset int64
	if info, err := os.Stat(consolePath); err == nil {
		consoleOffset = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	commandPath := m.steamUpdateCommand
	if commandPath == "" {
		commandPath = steamCMDPath
	}
	commandContext, cancel := context.WithTimeout(ctx, steamUpdateCommandTimeout)
	defer cancel()
	command := exec.CommandContext(
		commandContext,
		"wine",
		commandPath,
		"+@sSteamCmdForcePlatformType", "windows",
		"+@sSteamCmdForcePlatformBitness", "64",
		"+login", "anonymous",
		"+app_info_update", "1",
		"+app_info_print", steamAppID,
		"+quit",
	)
	command.Dir = "/root/steamcmd"
	command.Env = append(
		os.Environ(),
		"WINEPREFIX="+rootDir+"/.wine",
		"WINEDEBUG=-all",
		"XDG_RUNTIME_DIR="+rootDir+"/.runtime",
	)
	var output cappedBuffer
	command.Stdout = &output
	command.Stderr = &output
	commandErr := command.Run()
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return "", errors.New("SteamCMD update check timed out")
	}
	consoleData, err := readFileFromOffset(
		consolePath,
		consoleOffset,
		steamUpdateConsoleReadLimit,
	)
	if err != nil {
		return "", err
	}
	return steamBuildFromCommandResult(
		consoleData,
		output.String(),
		commandErr,
	)
}

func (m *Manager) checkSteamUpdate(ctx context.Context) (SteamUpdateView, error) {
	m.steamUpdateMu.Lock()
	defer m.steamUpdateMu.Unlock()

	state, err := readSteamUpdateState(m.steamUpdateStatePath())
	if err != nil {
		return SteamUpdateView{}, err
	}
	installed, err := m.installedSteamBuildID()
	if err != nil {
		return SteamUpdateView{}, err
	}
	query := m.steamUpdateRemoteBuild
	if query == nil {
		query = m.querySteamRemoteBuild
	}
	latest, checkErr := query(ctx)
	if errors.Is(checkErr, errSteamUpdateLifecycleBusy) {
		return steamUpdateView(state, installed), checkErr
	}
	state.CheckedAt = m.steamUpdateNowValue()
	if checkErr != nil {
		state.CheckError = boundedManagerUpdateText(
			checkErr.Error(),
			managerUpdateErrorLimit,
		)
	} else {
		state.LatestBuildID = latest
		state.CheckError = ""
	}
	if err := writeJSONAtomic(m.steamUpdateStatePath(), state, 0600); err != nil {
		return SteamUpdateView{}, err
	}
	view := steamUpdateView(state, installed)
	m.signalAutomaticUpdateLoop()
	if checkErr != nil {
		return view, checkErr
	}
	return view, nil
}

func (m *Manager) signalSteamUpdateCheck() {
	select {
	case m.steamUpdateWake <- struct{}{}:
	default:
	}
}

func (m *Manager) steamUpdateCheckLoop(ctx context.Context) {
	check := func(force bool) {
		state, err := readSteamUpdateState(m.steamUpdateStatePath())
		if err != nil ||
			(!force &&
				!steamUpdateCheckDue(state, m.steamUpdateNowValue())) {
			return
		}
		installed, installedErr := m.installedSteamBuildID()
		before := SteamUpdateView{}
		if installedErr == nil {
			before = steamUpdateView(state, installed)
		}
		view, err := m.checkSteamUpdate(ctx)
		if errors.Is(err, errSteamUpdateLifecycleBusy) {
			return
		}
		if err != nil {
			if view.CheckError != "" &&
				view.CheckError != before.CheckError {
				m.auditActorEvent(
					"system",
					"local",
					"steam_update_check_failed",
					map[string]string{
						"error": boundedManagerUpdateText(
							err.Error(),
							256,
						),
					},
				)
			}
			return
		}
		if view.Available &&
			(!before.Available ||
				before.LatestBuildID != view.LatestBuildID) {
			m.auditActorEvent("system", "local", "steam_update_available",
				map[string]string{
					"installed_build_id": view.InstalledBuildID,
					"latest_build_id":    view.LatestBuildID,
				})
		}
	}
	check(false)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check(false)
		case <-m.steamUpdateWake:
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				check(true)
			}
		}
	}
}

func steamUpdateCheckDue(
	state steamUpdateStateFile,
	now time.Time,
) bool {
	return state.CheckedAt.IsZero() ||
		!now.Before(state.CheckedAt.Add(steamUpdateCheckInterval))
}

func steamBuildDisplay(buildID string) string {
	if !validSteamBuildID(buildID) {
		return "unknown"
	}
	if value, err := strconv.ParseUint(buildID, 10, 64); err == nil {
		return strconv.FormatUint(value, 10)
	}
	return buildID
}
