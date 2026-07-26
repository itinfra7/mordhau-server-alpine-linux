package manager

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	ID      string     `json:"id,omitempty"`
	Line    int        `json:"line"`
	Name    string     `json:"name"`
	Enabled bool       `json:"enabled"`
	Entries []IniEntry `json:"entries"`
}

type IniEntry struct {
	ID      string `json:"id,omitempty"`
	Line    int    `json:"line"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

type ConfigMutation struct {
	File        string `json:"file"`
	Revision    string `json:"revision"`
	Action      string `json:"action"`
	EntryID     string `json:"entry_id"`
	SectionID   string `json:"section_id"`
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
	if len(data) == 0 {
		return iniDocument{lines: []string{}, newline: newline, raw: data}
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
	return makeConfigViewWithDisabled(name, data, staged, newDisabledINIFile())
}

func makeConfigViewWithDisabled(
	name string,
	data []byte,
	staged bool,
	store disabledINIFile,
) ConfigView {
	document := parseIni(data)
	view := ConfigView{
		File:     name,
		Revision: configRevision(name, data, store),
		Staged:   staged,
		Sections: []IniSection{},
	}
	current := IniSection{
		Line:    -1,
		Name:    "(entries before first section)",
		Enabled: true,
		Entries: []IniEntry{},
	}
	haveGlobal := false
	for lineNumber, line := range document.lines {
		if parsedName, ok := sectionName(line); ok {
			if current.Line >= 0 || haveGlobal {
				view.Sections = append(view.Sections, current)
			}
			current = IniSection{
				Line:    lineNumber,
				Name:    parsedName,
				Enabled: true,
				Entries: []IniEntry{},
			}
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

	globalOffset := 0
	if len(view.Sections) > 0 && view.Sections[0].Line < 0 &&
		view.Sections[0].Name == "(entries before first section)" {
		globalOffset = 1
	}
	for _, disabledSection := range disabledINISectionsFor(store, name) {
		found := -1
		for index := range view.Sections {
			if view.Sections[index].Name == disabledSection.Name {
				found = index
				break
			}
		}
		if found >= 0 {
			view.Sections[found].ID = disabledSection.ID
			view.Sections[found].Enabled = false
			continue
		}
		position := disabledSection.Position + globalOffset
		if position < globalOffset {
			position = globalOffset
		}
		if position > len(view.Sections) {
			position = len(view.Sections)
		}
		virtual := IniSection{
			ID:      disabledSection.ID,
			Line:    -1,
			Name:    disabledSection.Name,
			Enabled: false,
			Entries: []IniEntry{},
		}
		view.Sections = append(view.Sections, IniSection{})
		copy(view.Sections[position+1:], view.Sections[position:])
		view.Sections[position] = virtual
	}

	disabledEntries := make([]disabledINIEntry, 0)
	for _, disabledEntry := range store.Entries {
		if disabledEntry.File == name {
			disabledEntries = append(disabledEntries, disabledEntry)
		}
	}
	sort.SliceStable(disabledEntries, func(left, right int) bool {
		if disabledEntries[left].Section != disabledEntries[right].Section {
			return disabledEntries[left].Section < disabledEntries[right].Section
		}
		if disabledEntries[left].Position != disabledEntries[right].Position {
			return disabledEntries[left].Position < disabledEntries[right].Position
		}
		return disabledEntries[left].ID < disabledEntries[right].ID
	})
	for _, disabledEntry := range disabledEntries {
		displaySection := disabledEntry.Section
		if displaySection == "" {
			displaySection = "(entries before first section)"
		}
		sectionIndex := -1
		for index := range view.Sections {
			if view.Sections[index].Name == displaySection {
				sectionIndex = index
				break
			}
		}
		if sectionIndex < 0 {
			virtual := IniSection{
				Line:    -1,
				Name:    displaySection,
				Enabled: true,
				Entries: []IniEntry{},
			}
			if disabledEntry.Section == "" {
				view.Sections = append(view.Sections, IniSection{})
				copy(view.Sections[1:], view.Sections[:len(view.Sections)-1])
				view.Sections[0] = virtual
				sectionIndex = 0
			} else {
				view.Sections = append(view.Sections, virtual)
				sectionIndex = len(view.Sections) - 1
			}
		}
		entry := IniEntry{
			ID:      disabledEntry.ID,
			Line:    -1,
			Key:     disabledEntry.Key,
			Value:   disabledEntry.Value,
			Enabled: false,
		}
		position := disabledEntry.Position
		if position < 0 {
			position = 0
		}
		if position > len(view.Sections[sectionIndex].Entries) {
			position = len(view.Sections[sectionIndex].Entries)
		}
		entries := view.Sections[sectionIndex].Entries
		entries = append(entries, IniEntry{})
		copy(entries[position+1:], entries[position:])
		entries[position] = entry
		view.Sections[sectionIndex].Entries = entries
	}
	return view
}

func (m *Manager) configView(name string) (ConfigView, error) {
	m.configMu.Lock()
	defer m.configMu.Unlock()
	data, staged, err := readConfig(name)
	if err != nil {
		return ConfigView{}, err
	}
	storeStaged, err := disabledINIStateStaged(staged)
	if err != nil {
		return ConfigView{}, err
	}
	store, err := loadDisabledINIFile(storeStaged)
	if err != nil {
		return ConfigView{}, err
	}
	return makeConfigViewWithDisabled(name, data, staged, store), nil
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

func configSectionStorageName(section IniSection) string {
	if section.Line < 0 && section.Name == "(entries before first section)" && section.ID == "" {
		return ""
	}
	return section.Name
}

func configSectionPosition(view ConfigView, wanted int) int {
	position := 0
	for index := range view.Sections {
		if index == wanted {
			return position
		}
		if configSectionStorageName(view.Sections[index]) != "" {
			position++
		}
	}
	return position
}

func findConfigSection(
	view ConfigView,
	line int,
	id string,
	name string,
) (int, IniSection, bool) {
	for index, section := range view.Sections {
		if id != "" && section.ID == id {
			return index, section, true
		}
		if id == "" && line >= 0 && section.Line == line {
			return index, section, true
		}
	}
	if id == "" && line < 0 {
		for index, section := range view.Sections {
			if section.Name == name {
				return index, section, true
			}
		}
	}
	return -1, IniSection{}, false
}

func findConfigEntry(
	view ConfigView,
	line int,
	id string,
) (int, int, IniEntry, bool) {
	for sectionIndex, section := range view.Sections {
		for entryIndex, entry := range section.Entries {
			if id != "" && entry.ID == id {
				return sectionIndex, entryIndex, entry, true
			}
			if id == "" && line >= 0 && entry.Line == line {
				return sectionIndex, entryIndex, entry, true
			}
		}
	}
	return -1, -1, IniEntry{}, false
}

func insertConfigEntry(
	document *iniDocument,
	view ConfigView,
	sectionIndex int,
	position int,
	line string,
) error {
	if sectionIndex < 0 || sectionIndex >= len(view.Sections) {
		return errRevisionConflict
	}
	section := view.Sections[sectionIndex]
	for index := position + 1; index < len(section.Entries); index++ {
		entry := section.Entries[index]
		if entry.Enabled && entry.Line >= 0 {
			document.lines = insertConfigLine(document.lines, entry.Line, line)
			return nil
		}
	}

	storageName := configSectionStorageName(section)
	if storageName == "" {
		insertAt := len(document.lines)
		for index := range document.lines {
			if _, ok := sectionName(document.lines[index]); ok {
				insertAt = index
				break
			}
		}
		document.lines = insertConfigLine(document.lines, insertAt, line)
		return nil
	}
	_, end, found := findSectionBounds(document.lines, storageName)
	if found {
		document.lines = insertConfigLine(document.lines, end, line)
		return nil
	}
	if len(document.lines) > 0 && strings.TrimSpace(document.lines[len(document.lines)-1]) != "" {
		document.lines = append(document.lines, "")
	}
	document.lines = append(document.lines, "["+storageName+"]", line)
	return nil
}

func disableConfigEntry(
	name string,
	document *iniDocument,
	store *disabledINIFile,
	line int,
) error {
	view := makeConfigViewWithDisabled(name, document.bytes(), false, *store)
	sectionIndex, entryPosition, entry, found := findConfigEntry(view, line, "")
	if !found || !entry.Enabled || entry.Line < 0 {
		return errRevisionConflict
	}
	if entry.Line >= len(document.lines) {
		return errRevisionConflict
	}
	key, value, enabled, ok := configEntryParts(document.lines[entry.Line])
	if !ok || !enabled {
		return errRevisionConflict
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	store.Entries = append(store.Entries, disabledINIEntry{
		ID:       id,
		File:     name,
		Section:  configSectionStorageName(view.Sections[sectionIndex]),
		Position: entryPosition,
		Key:      key,
		Value:    value,
	})
	document.lines = append(
		document.lines[:entry.Line],
		document.lines[entry.Line+1:]...,
	)
	return nil
}

func enableConfigEntry(
	name string,
	document *iniDocument,
	store *disabledINIFile,
	id string,
) error {
	storeIndex := disabledINIEntryIndex(store, name, id)
	if storeIndex < 0 {
		return errRevisionConflict
	}
	view := makeConfigViewWithDisabled(name, document.bytes(), false, *store)
	sectionIndex, entryPosition, _, found := findConfigEntry(view, -1, id)
	if !found {
		return errRevisionConflict
	}
	entry := takeDisabledINIEntryAt(store, storeIndex)
	if sectionStateIndex, _ := disabledINISectionByName(store, name, entry.Section); sectionStateIndex >= 0 {
		takeDisabledINISectionAt(store, sectionStateIndex)
	}
	return insertConfigEntry(
		document,
		view,
		sectionIndex,
		entryPosition,
		formatConfigEntry(entry.Key, entry.Value, true),
	)
}

func removeConfigEntry(
	name string,
	document *iniDocument,
	store *disabledINIFile,
	line int,
	id string,
) error {
	view := makeConfigViewWithDisabled(name, document.bytes(), false, *store)
	sectionIndex, entryPosition, entry, found := findConfigEntry(view, line, id)
	if !found {
		return errRevisionConflict
	}
	if entry.ID != "" {
		storeIndex := disabledINIEntryIndex(store, name, entry.ID)
		if storeIndex < 0 {
			return errRevisionConflict
		}
		removeDisabledINIEntryAt(store, storeIndex)
		return nil
	}
	if entry.Line < 0 || entry.Line >= len(document.lines) {
		return errRevisionConflict
	}
	if _, _, enabled, ok := configEntryParts(document.lines[entry.Line]); !ok || !enabled {
		return errRevisionConflict
	}
	shiftDisabledINIPositions(
		store,
		name,
		configSectionStorageName(view.Sections[sectionIndex]),
		entryPosition+1,
		-1,
	)
	document.lines = append(
		document.lines[:entry.Line],
		document.lines[entry.Line+1:]...,
	)
	return nil
}

func setConfigSectionEnabled(
	name string,
	document *iniDocument,
	store *disabledINIFile,
	line int,
	id string,
	sectionNameValue string,
	enabled bool,
) error {
	view := makeConfigViewWithDisabled(name, document.bytes(), false, *store)
	sectionIndex, section, found := findConfigSection(view, line, id, sectionNameValue)
	if !found || configSectionStorageName(section) == "" {
		return errRevisionConflict
	}
	storageName := configSectionStorageName(section)
	if enabled {
		stateIndex, _ := disabledINISectionByName(store, name, storageName)
		if stateIndex < 0 {
			return errRevisionConflict
		}
		takeDisabledINISectionAt(store, stateIndex)
		entryIDs := make([]string, 0)
		for _, entry := range section.Entries {
			if !entry.Enabled && entry.ID != "" {
				entryIDs = append(entryIDs, entry.ID)
			}
		}
		for _, entryID := range entryIDs {
			if err := enableConfigEntry(name, document, store, entryID); err != nil {
				return err
			}
		}
		return nil
	}
	if _, existing := disabledINISectionByName(store, name, storageName); existing != nil {
		return errRevisionConflict
	}
	activeLines := make([]int, 0)
	for _, entry := range section.Entries {
		if entry.Enabled && entry.Line >= 0 {
			activeLines = append(activeLines, entry.Line)
		}
	}
	sort.Ints(activeLines)
	for index := len(activeLines) - 1; index >= 0; index-- {
		if err := disableConfigEntry(name, document, store, activeLines[index]); err != nil {
			return err
		}
	}
	sectionID, err := randomID()
	if err != nil {
		return err
	}
	store.Sections = append(store.Sections, disabledINISection{
		ID:       sectionID,
		File:     name,
		Name:     storageName,
		Position: configSectionPosition(view, sectionIndex),
	})
	return nil
}

type configFileSnapshot struct {
	exists bool
	data   []byte
}

func snapshotConfigFile(path string) (configFileSnapshot, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return configFileSnapshot{}, nil
	}
	if err != nil {
		return configFileSnapshot{}, err
	}
	return configFileSnapshot{exists: true, data: data}, nil
}

func restoreConfigFile(path string, snapshot configFileSnapshot) {
	if snapshot.exists {
		_ = writeFileAtomic(path, snapshot.data, 0600)
		return
	}
	_ = os.Remove(path)
}

func persistConfigState(
	name string,
	oldData []byte,
	newData []byte,
	oldStore disabledINIFile,
	newStore disabledINIFile,
	staged bool,
) error {
	targetConfig := configPath(name, staged)
	targetStore := disabledINIStorePath(staged)
	configSnapshot, err := snapshotConfigFile(targetConfig)
	if err != nil {
		return err
	}
	storeSnapshot, err := snapshotConfigFile(targetStore)
	if err != nil {
		return err
	}
	oldStoreData, err := marshalDisabledINIFile(oldStore)
	if err != nil {
		return err
	}
	newStoreData, err := marshalDisabledINIFile(newStore)
	if err != nil {
		return err
	}
	configChanged := !bytes.Equal(oldData, newData)
	storeChanged := !bytes.Equal(oldStoreData, newStoreData)

	if !staged {
		if configChanged {
			if err := backupConfig(name, oldData); err != nil {
				return err
			}
		}
		if storeChanged && storeSnapshot.exists {
			if err := backupDisabledINI(storeSnapshot.data); err != nil {
				return err
			}
		}
	}

	writeConfig := configChanged || !configSnapshot.exists
	writeStore := storeChanged || !storeSnapshot.exists
	if !writeConfig && !writeStore {
		return nil
	}

	configFirst := len(newStore.Entries) < len(oldStore.Entries) ||
		len(newStore.Sections) < len(oldStore.Sections)
	if configFirst {
		if writeConfig {
			if err := writeFileAtomic(targetConfig, newData, 0600); err != nil {
				return err
			}
		}
		if writeStore {
			if err := writeFileAtomic(targetStore, newStoreData, 0600); err != nil {
				restoreConfigFile(targetConfig, configSnapshot)
				return err
			}
		}
		return nil
	}
	if writeStore {
		if err := writeFileAtomic(targetStore, newStoreData, 0600); err != nil {
			return err
		}
	}
	if writeConfig {
		if err := writeFileAtomic(targetConfig, newData, 0600); err != nil {
			restoreConfigFile(targetStore, storeSnapshot)
			return err
		}
	}
	return nil
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
	storeStaged, err := disabledINIStateStaged(staged)
	if err != nil {
		return ConfigView{}, err
	}
	store, err := loadDisabledINIFile(storeStaged)
	if err != nil {
		return ConfigView{}, err
	}
	oldStore := cloneDisabledINIFile(store)
	if request.Revision == "" || request.Revision != configRevision(request.File, data, store) {
		return ConfigView{}, errRevisionConflict
	}

	document := parseIni(data)
	lines := document.lines
	lineValid := request.Line >= 0 && request.Line < len(lines)
	sectionLineValid := request.SectionLine >= -1 && request.SectionLine < len(lines)
	view := makeConfigViewWithDisabled(request.File, data, staged, store)

	switch request.Action {
	case "set_entry":
		if !validIniText(request.Key, false) || !validIniText(request.Value, true) {
			return ConfigView{}, errors.New("invalid entry")
		}
		if request.EntryID != "" {
			index := disabledINIEntryIndex(&store, request.File, request.EntryID)
			if index < 0 {
				return ConfigView{}, errRevisionConflict
			}
			store.Entries[index].Key = strings.TrimSpace(request.Key)
			store.Entries[index].Value = request.Value
		} else {
			if !lineValid {
				return ConfigView{}, errors.New("invalid entry")
			}
			_, _, enabled, ok := configEntryParts(lines[request.Line])
			if !ok || !enabled {
				return ConfigView{}, errRevisionConflict
			}
			lines[request.Line] = formatConfigEntry(request.Key, request.Value, true)
		}
	case "set_entry_enabled":
		if request.Enabled {
			if err := enableConfigEntry(request.File, &document, &store, request.EntryID); err != nil {
				return ConfigView{}, err
			}
		} else {
			if err := disableConfigEntry(request.File, &document, &store, request.Line); err != nil {
				return ConfigView{}, err
			}
		}
	case "remove_entry":
		if err := removeConfigEntry(
			request.File,
			&document,
			&store,
			request.Line,
			request.EntryID,
		); err != nil {
			return ConfigView{}, err
		}
	case "add_entry":
		if !sectionLineValid || !validIniText(request.Key, false) || !validIniText(request.Value, true) {
			return ConfigView{}, errors.New("invalid new entry")
		}
		sectionIndex, section, found := findConfigSection(
			view,
			request.SectionLine,
			request.SectionID,
			request.Section,
		)
		if !found {
			return ConfigView{}, errRevisionConflict
		}
		if !section.Enabled {
			id, err := randomID()
			if err != nil {
				return ConfigView{}, err
			}
			store.Entries = append(store.Entries, disabledINIEntry{
				ID:       id,
				File:     request.File,
				Section:  configSectionStorageName(section),
				Position: len(section.Entries),
				Key:      strings.TrimSpace(request.Key),
				Value:    request.Value,
			})
			break
		}
		newLine := strings.TrimSpace(request.Key) + "=" + request.Value
		if err := insertConfigEntry(
			&document,
			view,
			sectionIndex,
			len(section.Entries)-1,
			newLine,
		); err != nil {
			return ConfigView{}, err
		}
	case "add_section":
		if !validIniText(request.Section, false) ||
			strings.ContainsAny(request.Section, "[]") {
			return ConfigView{}, errors.New("invalid section name")
		}
		newName := strings.TrimSpace(request.Section)
		for _, existing := range view.Sections {
			if configSectionStorageName(existing) == newName {
				return ConfigView{}, errors.New("section already exists")
			}
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+newName+"]")
		document.lines = lines
	case "rename_section":
		if !validIniText(request.Section, false) ||
			strings.ContainsAny(request.Section, "[]") {
			return ConfigView{}, errors.New("invalid section")
		}
		_, existingSection, found := findConfigSection(
			view,
			request.Line,
			request.SectionID,
			"",
		)
		if !found || configSectionStorageName(existingSection) == "" {
			return ConfigView{}, errRevisionConflict
		}
		oldName := configSectionStorageName(existingSection)
		newName := strings.TrimSpace(request.Section)
		if newName != oldName {
			for _, candidate := range view.Sections {
				if configSectionStorageName(candidate) == newName {
					return ConfigView{}, errors.New("section already exists")
				}
			}
		}
		if existingSection.Line >= 0 {
			if _, ok := sectionName(lines[existingSection.Line]); !ok {
				return ConfigView{}, errRevisionConflict
			}
			lines[existingSection.Line] = "[" + newName + "]"
		}
		for index := range store.Sections {
			if store.Sections[index].File == request.File &&
				store.Sections[index].Name == oldName {
				store.Sections[index].Name = newName
			}
		}
		for index := range store.Entries {
			if store.Entries[index].File == request.File &&
				store.Entries[index].Section == oldName {
				store.Entries[index].Section = newName
			}
		}
	case "remove_section":
		sectionIndex, existingSection, found := findConfigSection(
			view,
			request.Line,
			request.SectionID,
			request.Section,
		)
		if !found || configSectionStorageName(existingSection) == "" {
			return ConfigView{}, errRevisionConflict
		}
		storageName := configSectionStorageName(existingSection)
		if existingSection.Line >= 0 {
			end := len(lines)
			for i := existingSection.Line + 1; i < len(lines); i++ {
				if _, ok := sectionName(lines[i]); ok {
					end = i
					break
				}
			}
			lines = append(lines[:existingSection.Line], lines[end:]...)
			document.lines = lines
		}
		filteredEntries := store.Entries[:0]
		for _, entry := range store.Entries {
			if entry.File != request.File || entry.Section != storageName {
				filteredEntries = append(filteredEntries, entry)
			}
		}
		store.Entries = filteredEntries
		if stateIndex, _ := disabledINISectionByName(&store, request.File, storageName); stateIndex >= 0 {
			removeDisabledINISectionAt(&store, stateIndex)
		} else {
			shiftDisabledINISectionPositions(
				&store,
				request.File,
				configSectionPosition(view, sectionIndex)+1,
				-1,
			)
		}
	case "set_section_enabled":
		if err := setConfigSectionEnabled(
			request.File,
			&document,
			&store,
			request.Line,
			request.SectionID,
			request.Section,
			request.Enabled,
		); err != nil {
			return ConfigView{}, err
		}
	default:
		return ConfigView{}, errors.New("unsupported mutation")
	}
	newData := document.bytes()

	targetStaged := staged || storeStaged || serverRunning()
	if err := persistConfigState(
		request.File,
		data,
		newData,
		oldStore,
		store,
		targetStaged,
	); err != nil {
		return ConfigView{}, err
	}
	return makeConfigViewWithDisabled(request.File, newData, targetStaged, store), nil
}

func backupConfig(name string, data []byte) error {
	stamp := time.Now().Format("2006-01-02_15-04-05.000000000")
	path := filepath.Join(backupDir, name+"."+stamp+".bak")
	return writeFileAtomic(path, data, 0600)
}

func backupDisabledINI(data []byte) error {
	stamp := time.Now().Format("2006-01-02_15-04-05.000000000")
	path := filepath.Join(backupDir, "disabled-ini-entries.json."+stamp+".bak")
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
	for _, path := range []string{
		configPath("Game.ini", true),
		configPath("Engine.ini", true),
		pendingDisabledINIPath(),
	} {
		err := os.Remove(path)
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

func iniEntryStateWithDisabled(
	data []byte,
	store disabledINIFile,
	file string,
	section string,
	key string,
) (value string, enabled, exists bool) {
	value, enabled, exists = iniEntryState(data, section, key)
	if exists {
		return value, enabled, true
	}
	for _, entry := range store.Entries {
		if entry.File == file &&
			entry.Section == section &&
			strings.EqualFold(entry.Key, key) {
			return strings.TrimSpace(entry.Value), false, true
		}
	}
	return "", false, false
}

func ensureEngineNetworkDefaultValues(
	data []byte,
	store disabledINIFile,
) ([]byte, bool) {
	const section = "/Script/OnlineSubsystemUtils.IpNetDriver"
	for _, disabledSection := range store.Sections {
		if disabledSection.File == "Engine.ini" &&
			disabledSection.Name == section {
			return data, false
		}
	}

	document := parseIni(data)
	changed := false
	defaults := []struct {
		key   string
		value string
	}{
		{key: "NetServerMaxTickRate", value: "60"},
		{key: "ConnectionTimeout", value: "10.0"},
	}
	for _, defaultValue := range defaults {
		_, _, exists := iniEntryStateWithDisabled(
			data,
			store,
			"Engine.ini",
			section,
			defaultValue.key,
		)
		if exists {
			continue
		}
		setIniValue(&document, section, defaultValue.key, defaultValue.value)
		changed = true
	}
	return document.bytes(), changed
}

func (m *Manager) EnsureEngineNetworkDefaults() error {
	data, staged, err := readConfig("Engine.ini")
	if err != nil {
		return fmt.Errorf("read generated Engine.ini: %w", err)
	}
	store, err := loadDisabledINIFile(staged)
	if err != nil {
		return err
	}
	updated, changed := ensureEngineNetworkDefaultValues(data, store)
	path := configPath("Engine.ini", staged)
	if changed {
		if !staged {
			if err := backupConfig("Engine.ini", data); err != nil {
				return err
			}
		}
		return writeFileAtomic(path, updated, 0600)
	}
	return os.Chmod(path, 0600)
}

func (m *Manager) ensureRCONConfig() error {
	path := configPath("Game.ini", false)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated Game.ini: %w", err)
	}
	document := parseIni(data)
	store, err := loadDisabledINIFile(false)
	if err != nil {
		return err
	}
	const section = "/Script/Mordhau.MordhauGameSession"
	password, passwordEnabled, passwordExists := iniEntryStateWithDisabled(
		data,
		store,
		"Game.ini",
		section,
		"RconPassword",
	)
	changed := false
	if !passwordExists || (passwordEnabled && password == "") {
		password, err = randomPassword()
		if err != nil {
			return err
		}
		setIniValue(&document, section, "RconPassword", password)
		changed = true
	}
	port, portEnabled, portExists := iniEntryStateWithDisabled(
		data,
		store,
		"Game.ini",
		section,
		"RconPort",
	)
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

func (m *Manager) ensureServerEventLogConfig() error {
	path := configPath("Game.ini", false)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated Game.ini: %w", err)
	}
	store, err := loadDisabledINIFile(false)
	if err != nil {
		return err
	}
	updated, changed := ensureServerEventLogValues(data, store)
	if changed {
		if err := backupConfig("Game.ini", data); err != nil {
			return err
		}
		return writeFileAtomic(path, updated, 0600)
	}
	return os.Chmod(path, 0600)
}

func ensureServerEventLogValues(
	data []byte,
	store disabledINIFile,
) ([]byte, bool) {
	const section = "/Script/Mordhau.MordhauGameMode"
	for _, disabledSection := range store.Sections {
		if disabledSection.File == "Game.ini" &&
			disabledSection.Name == section {
			return data, false
		}
	}

	document := parseIni(data)
	changed := false
	for _, key := range []string{"bLogKillfeed", "bLogChat", "bLogScore"} {
		value, enabled, exists := iniEntryStateWithDisabled(
			data,
			store,
			"Game.ini",
			section,
			key,
		)
		if exists && !enabled {
			continue
		}
		if !exists || !strings.EqualFold(strings.TrimSpace(value), "true") {
			setIniValue(&document, section, key, "True")
			changed = true
		}
	}
	return document.bytes(), changed
}

func pendingConfigExists() bool {
	for _, path := range []string{
		configPath("Game.ini", true),
		configPath("Engine.ini", true),
		pendingDisabledINIPath(),
	} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}
