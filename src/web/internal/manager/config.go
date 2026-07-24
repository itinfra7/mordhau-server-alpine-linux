package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	errRevisionConflict = errors.New("configuration changed; reload and try again")
	errLifecycleBusy    = errors.New("a server lifecycle operation is in progress")
)

const disabledEntryPrefix = "; MORDHAU_MANAGER_DISABLED: "

type iniDocument struct {
	lines    []string
	newline  string
	trailing bool
	raw      []byte
}

type ConfigView struct {
	File     string       `json:"file"`
	Revision string       `json:"revision"`
	Staged   bool         `json:"staged"`
	Sections []IniSection `json:"sections"`
}

type IniSection struct {
	Line    int        `json:"line"`
	Name    string     `json:"name"`
	Entries []IniEntry `json:"entries"`
}

type IniEntry struct {
	Line    int    `json:"line"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

type ConfigMutation struct {
	File        string `json:"file"`
	Revision    string `json:"revision"`
	Action      string `json:"action"`
	Line        int    `json:"line"`
	SectionLine int    `json:"section_line"`
	Section     string `json:"section"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	Enabled     bool   `json:"enabled"`
}

func allowedConfigFile(name string) bool {
	return name == "Game.ini" || name == "Engine.ini"
}

func parseIni(data []byte) iniDocument {
	newline := "\n"
	if strings.Contains(string(data), "\r\n") {
		newline = "\r\n"
	}
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	trailing := strings.HasSuffix(normalized, "\n")
	lines := strings.Split(normalized, "\n")
	if trailing && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return iniDocument{lines: lines, newline: newline, trailing: trailing, raw: data}
}

func (document *iniDocument) bytes() []byte {
	value := strings.Join(document.lines, document.newline)
	if document.trailing || len(document.lines) > 0 {
		value += document.newline
	}
	return []byte(value)
}

func sectionName(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) >= 2 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']' {
		return strings.TrimSpace(trimmed[1 : len(trimmed)-1]), true
	}
	return "", false
}

func configEntryParts(line string) (key, value string, enabled, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", "", false, false
	}

	content := line
	enabled = true
	leftTrimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(leftTrimmed, disabledEntryPrefix) {
		content = strings.TrimPrefix(leftTrimmed, disabledEntryPrefix)
		enabled = false
	} else if strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
		return "", "", false, false
	}
	if _, section := sectionName(content); section {
		return "", "", false, false
	}
	index := strings.IndexByte(content, '=')
	if index < 1 {
		return "", "", false, false
	}
	key = strings.TrimSpace(content[:index])
	if key == "" {
		return "", "", false, false
	}
	return key, content[index+1:], enabled, true
}

func entryParts(line string) (string, string, bool) {
	key, value, enabled, ok := configEntryParts(line)
	if !ok || !enabled {
		return "", "", false
	}
	return key, value, true
}

func formatConfigEntry(key, value string, enabled bool) string {
	line := strings.TrimSpace(key) + "=" + value
	if !enabled {
		return disabledEntryPrefix + line
	}
	return line
}

func setConfigEntryEnabled(lines []string, line int, enabled bool) error {
	if line < 0 || line >= len(lines) {
		return errors.New("invalid entry line")
	}
	key, value, _, ok := configEntryParts(lines[line])
	if !ok {
		return errRevisionConflict
	}
	lines[line] = formatConfigEntry(key, value, enabled)
	return nil
}

func revision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func configPath(name string, staged bool) string {
	if staged {
		return filepath.Join(pendingDir, name)
	}
	return filepath.Join(configDir, name)
}

func readConfig(name string) (data []byte, staged bool, err error) {
	if !allowedConfigFile(name) {
		return nil, false, errors.New("unsupported configuration file")
	}
	pending := configPath(name, true)
	data, err = os.ReadFile(pending)
	if err == nil {
		return data, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	data, err = os.ReadFile(configPath(name, false))
	return data, false, err
}

func makeConfigView(name string, data []byte, staged bool) ConfigView {
	document := parseIni(data)
	view := ConfigView{
		File:     name,
		Revision: revision(data),
		Staged:   staged,
		Sections: []IniSection{},
	}
	current := IniSection{Line: -1, Name: "(entries before first section)"}
	haveGlobal := false
	for lineNumber, line := range document.lines {
		if name, ok := sectionName(line); ok {
			if current.Line >= 0 || haveGlobal {
				view.Sections = append(view.Sections, current)
			}
			current = IniSection{Line: lineNumber, Name: name, Entries: []IniEntry{}}
			haveGlobal = false
			continue
		}
		key, value, enabled, ok := configEntryParts(line)
		if !ok {
			continue
		}
		current.Entries = append(current.Entries, IniEntry{
			Line:    lineNumber,
			Key:     key,
			Value:   value,
			Enabled: enabled,
		})
		if current.Line < 0 {
			haveGlobal = true
		}
	}
	if current.Line >= 0 || haveGlobal {
		view.Sections = append(view.Sections, current)
	}
	return view
}

func (m *Manager) configView(name string) (ConfigView, error) {
	data, staged, err := readConfig(name)
	if err != nil {
		return ConfigView{}, err
	}
	return makeConfigView(name, data, staged), nil
}

func validIniText(value string, allowEmpty bool) bool {
	if strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	return allowEmpty || strings.TrimSpace(value) != ""
}

func acquireLifecycleLock() (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(stateDir, "lifecycle.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errLifecycleBusy
	}
	return file, nil
}

func releaseLifecycleLock(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func (m *Manager) mutateConfig(request ConfigMutation) (ConfigView, error) {
	if !allowedConfigFile(request.File) {
		return ConfigView{}, errors.New("unsupported configuration file")
	}
	m.configMu.Lock()
	defer m.configMu.Unlock()

	lock, err := acquireLifecycleLock()
	if err != nil {
		return ConfigView{}, err
	}
	defer releaseLifecycleLock(lock)

	data, staged, err := readConfig(request.File)
	if err != nil {
		return ConfigView{}, err
	}
	if request.Revision == "" || request.Revision != revision(data) {
		return ConfigView{}, errRevisionConflict
	}

	document := parseIni(data)
	lines := document.lines
	lineValid := request.Line >= 0 && request.Line < len(lines)
	sectionLineValid := request.SectionLine >= -1 && request.SectionLine < len(lines)

	switch request.Action {
	case "set_entry":
		if !lineValid || !validIniText(request.Key, false) || !validIniText(request.Value, true) {
			return ConfigView{}, errors.New("invalid entry")
		}
		_, _, enabled, ok := configEntryParts(lines[request.Line])
		if !ok {
			return ConfigView{}, errRevisionConflict
		}
		lines[request.Line] = formatConfigEntry(request.Key, request.Value, enabled)
	case "set_entry_enabled":
		if err := setConfigEntryEnabled(lines, request.Line, request.Enabled); err != nil {
			return ConfigView{}, err
		}
	case "remove_entry":
		if !lineValid {
			return ConfigView{}, errors.New("invalid entry line")
		}
		if _, _, _, ok := configEntryParts(lines[request.Line]); !ok {
			return ConfigView{}, errRevisionConflict
		}
		lines = append(lines[:request.Line], lines[request.Line+1:]...)
	case "add_entry":
		if !sectionLineValid || !validIniText(request.Key, false) || !validIniText(request.Value, true) {
			return ConfigView{}, errors.New("invalid new entry")
		}
		insertAt := 0
		if request.SectionLine >= 0 {
			if _, ok := sectionName(lines[request.SectionLine]); !ok {
				return ConfigView{}, errRevisionConflict
			}
			insertAt = len(lines)
			for i := request.SectionLine + 1; i < len(lines); i++ {
				if _, ok := sectionName(lines[i]); ok {
					insertAt = i
					break
				}
			}
		} else {
			insertAt = len(lines)
			for i := range lines {
				if _, ok := sectionName(lines[i]); ok {
					insertAt = i
					break
				}
			}
		}
		newLine := strings.TrimSpace(request.Key) + "=" + request.Value
		lines = append(lines, "")
		copy(lines[insertAt+1:], lines[insertAt:])
		lines[insertAt] = newLine
	case "add_section":
		if !validIniText(request.Section, false) ||
			strings.ContainsAny(request.Section, "[]") {
			return ConfigView{}, errors.New("invalid section name")
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+strings.TrimSpace(request.Section)+"]")
	case "rename_section":
		if !lineValid || !validIniText(request.Section, false) ||
			strings.ContainsAny(request.Section, "[]") {
			return ConfigView{}, errors.New("invalid section")
		}
		if _, ok := sectionName(lines[request.Line]); !ok {
			return ConfigView{}, errRevisionConflict
		}
		lines[request.Line] = "[" + strings.TrimSpace(request.Section) + "]"
	case "remove_section":
		if !lineValid {
			return ConfigView{}, errors.New("invalid section line")
		}
		if _, ok := sectionName(lines[request.Line]); !ok {
			return ConfigView{}, errRevisionConflict
		}
		end := len(lines)
		for i := request.Line + 1; i < len(lines); i++ {
			if _, ok := sectionName(lines[i]); ok {
				end = i
				break
			}
		}
		lines = append(lines[:request.Line], lines[end:]...)
	default:
		return ConfigView{}, errors.New("unsupported mutation")
	}
	document.lines = lines
	newData := document.bytes()

	targetStaged := staged || serverRunning()
	target := configPath(request.File, targetStaged)
	if !targetStaged {
		if err := backupConfig(request.File, data); err != nil {
			return ConfigView{}, err
		}
	}
	if err := writeFileAtomic(target, newData, 0600); err != nil {
		return ConfigView{}, err
	}
	return makeConfigView(request.File, newData, targetStaged), nil
}

func backupConfig(name string, data []byte) error {
	stamp := time.Now().Format("2006-01-02_15-04-05.000000000")
	path := filepath.Join(backupDir, name+"."+stamp+".bak")
	return writeFileAtomic(path, data, 0600)
}

func (m *Manager) discardPending() error {
	m.configMu.Lock()
	defer m.configMu.Unlock()
	lock, err := acquireLifecycleLock()
	if err != nil {
		return err
	}
	defer releaseLifecycleLock(lock)
	for _, name := range []string{"Game.ini", "Engine.ini"} {
		err := os.Remove(configPath(name, true))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func findSectionBounds(lines []string, wanted string) (start, end int, found bool) {
	for i, line := range lines {
		name, ok := sectionName(line)
		if !ok || name != wanted {
			continue
		}
		end = len(lines)
		for j := i + 1; j < len(lines); j++ {
			if _, ok := sectionName(lines[j]); ok {
				end = j
				break
			}
		}
		return i, end, true
	}
	return 0, 0, false
}

func setIniValue(document *iniDocument, section, key, value string) {
	start, end, found := findSectionBounds(document.lines, section)
	if !found {
		if len(document.lines) > 0 && strings.TrimSpace(document.lines[len(document.lines)-1]) != "" {
			document.lines = append(document.lines, "")
		}
		document.lines = append(document.lines, "["+section+"]", key+"="+value)
		return
	}
	for i := start + 1; i < end; i++ {
		existingKey, _, ok := entryParts(document.lines[i])
		if ok && strings.EqualFold(existingKey, key) {
			document.lines[i] = key + "=" + value
			return
		}
	}
	document.lines = append(document.lines, "")
	copy(document.lines[end+1:], document.lines[end:])
	document.lines[end] = key + "=" + value
}

func iniValue(data []byte, section, key string) (string, bool) {
	value, enabled, exists := iniEntryState(data, section, key)
	return value, exists && enabled
}

func iniEntryState(data []byte, section, key string) (value string, enabled, exists bool) {
	document := parseIni(data)
	start, end, found := findSectionBounds(document.lines, section)
	if !found {
		return "", false, false
	}
	var disabledValue string
	disabledFound := false
	for i := start + 1; i < end; i++ {
		existingKey, entryValue, entryEnabled, ok := configEntryParts(document.lines[i])
		if ok && strings.EqualFold(existingKey, key) {
			if entryEnabled {
				return strings.TrimSpace(entryValue), true, true
			}
			if !disabledFound {
				disabledValue = strings.TrimSpace(entryValue)
				disabledFound = true
			}
		}
	}
	if disabledFound {
		return disabledValue, false, true
	}
	return "", false, false
}

func (m *Manager) ensureRCONConfig() error {
	path := configPath("Game.ini", false)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated Game.ini: %w", err)
	}
	document := parseIni(data)
	const section = "/Script/Mordhau.MordhauGameSession"
	password, passwordEnabled, passwordExists := iniEntryState(data, section, "RconPassword")
	changed := false
	if !passwordExists || (passwordEnabled && password == "") {
		password, err = randomPassword()
		if err != nil {
			return err
		}
		setIniValue(&document, section, "RconPassword", password)
		changed = true
	}
	port, portEnabled, portExists := iniEntryState(data, section, "RconPort")
	portNumber, parseErr := strconv.Atoi(port)
	if !portExists || (portEnabled && (parseErr != nil || portNumber < 1 || portNumber > 65535)) {
		setIniValue(&document, section, "RconPort", strconv.Itoa(defaultRCONPort))
		changed = true
	}
	if changed {
		if err := backupConfig("Game.ini", data); err != nil {
			return err
		}
		return writeFileAtomic(path, document.bytes(), 0600)
	}
	return os.Chmod(path, 0600)
}

func pendingConfigExists() bool {
	for _, name := range []string{"Game.ini", "Engine.ini"} {
		if _, err := os.Stat(configPath(name, true)); err == nil {
			return true
		}
	}
	return false
}
