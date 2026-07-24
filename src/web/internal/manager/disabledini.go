package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const disabledINIVersion = 1

func newDisabledINIFile() disabledINIFile {
	return disabledINIFile{
		Version:  disabledINIVersion,
		Sections: []disabledINISection{},
		Entries:  []disabledINIEntry{},
	}
}

func pendingDisabledINIPath() string {
	return filepath.Join(pendingDir, filepath.Base(disabledINIPath))
}

func disabledINIStorePath(staged bool) string {
	if staged {
		return pendingDisabledINIPath()
	}
	return disabledINIPath
}

func disabledINIStateStaged(configStaged bool) (bool, error) {
	if configStaged {
		return true, nil
	}
	if _, err := os.Stat(pendingDisabledINIPath()); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return false, nil
}

func cloneDisabledINIFile(source disabledINIFile) disabledINIFile {
	clone := source
	clone.Sections = append([]disabledINISection(nil), source.Sections...)
	clone.Entries = append([]disabledINIEntry(nil), source.Entries...)
	if clone.Sections == nil {
		clone.Sections = []disabledINISection{}
	}
	if clone.Entries == nil {
		clone.Entries = []disabledINIEntry{}
	}
	return clone
}

func validateDisabledINIFile(store *disabledINIFile) error {
	if store.Version != disabledINIVersion {
		return fmt.Errorf("unsupported disabled INI state version %d", store.Version)
	}
	if store.Entries == nil {
		store.Entries = []disabledINIEntry{}
	}
	if store.Sections == nil {
		store.Sections = []disabledINISection{}
	}
	ids := make(map[string]bool, len(store.Sections)+len(store.Entries))
	sectionKeys := make(map[string]bool, len(store.Sections))
	for index := range store.Sections {
		section := &store.Sections[index]
		if section.ID == "" || ids[section.ID] {
			return errors.New("disabled INI state contains an invalid section ID")
		}
		ids[section.ID] = true
		if !allowedConfigFile(section.File) ||
			!validIniText(section.Name, false) ||
			section.Position < 0 {
			return errors.New("disabled INI state contains an invalid section")
		}
		key := section.File + "\x00" + section.Name
		if sectionKeys[key] {
			return errors.New("disabled INI state contains a duplicate section")
		}
		sectionKeys[key] = true
	}
	for index := range store.Entries {
		entry := &store.Entries[index]
		if entry.ID == "" || ids[entry.ID] {
			return errors.New("disabled INI state contains an invalid entry ID")
		}
		ids[entry.ID] = true
		if !allowedConfigFile(entry.File) {
			return errors.New("disabled INI state contains an unsupported file")
		}
		if !validIniText(entry.Section, true) ||
			!validIniText(entry.Key, false) ||
			!validIniText(entry.Value, true) ||
			entry.Position < 0 {
			return errors.New("disabled INI state contains an invalid entry")
		}
	}
	return nil
}

func marshalDisabledINIFile(store disabledINIFile) ([]byte, error) {
	if err := validateDisabledINIFile(&store); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readDisabledINIFile(path string) (disabledINIFile, error) {
	var store disabledINIFile
	if err := readJSON(path, &store); err != nil {
		return disabledINIFile{}, err
	}
	if err := validateDisabledINIFile(&store); err != nil {
		return disabledINIFile{}, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return disabledINIFile{}, err
	}
	return store, nil
}

func loadDisabledINIFile(staged bool) (disabledINIFile, error) {
	if staged {
		store, err := readDisabledINIFile(disabledINIStorePath(true))
		if err == nil {
			return store, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return disabledINIFile{}, err
		}
	}
	store, err := readDisabledINIFile(disabledINIStorePath(false))
	if errors.Is(err, os.ErrNotExist) {
		return newDisabledINIFile(), nil
	}
	return store, err
}

func writeDisabledINIFile(store disabledINIFile, staged bool) error {
	data, err := marshalDisabledINIFile(store)
	if err != nil {
		return err
	}
	return writeFileAtomic(disabledINIStorePath(staged), data, 0600)
}

func configRevision(name string, data []byte, store disabledINIFile) string {
	sections := make([]disabledINISection, 0)
	for _, section := range store.Sections {
		if section.File == name {
			sections = append(sections, section)
		}
	}
	entries := make([]disabledINIEntry, 0)
	for _, entry := range store.Entries {
		if entry.File == name {
			entries = append(entries, entry)
		}
	}
	state, _ := json.Marshal(struct {
		Sections []disabledINISection `json:"sections"`
		Entries  []disabledINIEntry   `json:"entries"`
	}{
		Sections: sections,
		Entries:  entries,
	})
	hash := sha256.New()
	_, _ = hash.Write(data)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(state)
	return hex.EncodeToString(hash.Sum(nil))
}

func disabledINISectionByName(
	store *disabledINIFile,
	file string,
	name string,
) (int, *disabledINISection) {
	for index := range store.Sections {
		section := &store.Sections[index]
		if section.File == file && section.Name == name {
			return index, section
		}
	}
	return -1, nil
}

func disabledINISectionsFor(store disabledINIFile, file string) []disabledINISection {
	sections := make([]disabledINISection, 0)
	for _, section := range store.Sections {
		if section.File == file {
			sections = append(sections, section)
		}
	}
	sort.SliceStable(sections, func(left, right int) bool {
		if sections[left].Position != sections[right].Position {
			return sections[left].Position < sections[right].Position
		}
		return sections[left].ID < sections[right].ID
	})
	return sections
}

func shiftDisabledINISectionPositions(
	store *disabledINIFile,
	file string,
	from int,
	delta int,
) {
	for index := range store.Sections {
		section := &store.Sections[index]
		if section.File == file && section.Position >= from {
			section.Position += delta
			if section.Position < 0 {
				section.Position = 0
			}
		}
	}
}

func removeDisabledINISectionAt(store *disabledINIFile, index int) disabledINISection {
	section := store.Sections[index]
	store.Sections = append(store.Sections[:index], store.Sections[index+1:]...)
	shiftDisabledINISectionPositions(store, section.File, section.Position+1, -1)
	return section
}

func takeDisabledINISectionAt(store *disabledINIFile, index int) disabledINISection {
	section := store.Sections[index]
	store.Sections = append(store.Sections[:index], store.Sections[index+1:]...)
	return section
}

func disabledINIEntryIndex(store *disabledINIFile, file, id string) int {
	if id == "" {
		return -1
	}
	for index := range store.Entries {
		if store.Entries[index].File == file && store.Entries[index].ID == id {
			return index
		}
	}
	return -1
}

func shiftDisabledINIPositions(
	store *disabledINIFile,
	file string,
	section string,
	from int,
	delta int,
) {
	for index := range store.Entries {
		entry := &store.Entries[index]
		if entry.File == file && entry.Section == section && entry.Position >= from {
			entry.Position += delta
			if entry.Position < 0 {
				entry.Position = 0
			}
		}
	}
}

func removeDisabledINIEntryAt(store *disabledINIFile, index int) disabledINIEntry {
	entry := store.Entries[index]
	store.Entries = append(store.Entries[:index], store.Entries[index+1:]...)
	shiftDisabledINIPositions(store, entry.File, entry.Section, entry.Position+1, -1)
	return entry
}

func takeDisabledINIEntryAt(store *disabledINIFile, index int) disabledINIEntry {
	entry := store.Entries[index]
	store.Entries = append(store.Entries[:index], store.Entries[index+1:]...)
	return entry
}

func disabledINIEntrySignature(section, key, value string) string {
	return section + "\x00" + strings.ToLower(key) + "\x00" + value
}

func migrateLegacyDisabledEntries(
	name string,
	data []byte,
	store *disabledINIFile,
) ([]byte, int, error) {
	document := parseIni(data)
	kept := make([]string, 0, len(document.lines))
	positions := make(map[string]int)
	existing := make(map[string]int)
	consumed := make(map[string]int)
	for _, entry := range store.Entries {
		if entry.File == name {
			existing[disabledINIEntrySignature(entry.Section, entry.Key, entry.Value)]++
		}
	}
	section := ""
	migrated := 0

	for _, line := range document.lines {
		if parsedSection, ok := sectionName(line); ok {
			section = parsedSection
			kept = append(kept, line)
			continue
		}
		key, value, enabled, ok := configEntryParts(line)
		if !ok {
			kept = append(kept, line)
			continue
		}
		position := positions[section]
		positions[section]++
		if enabled {
			kept = append(kept, line)
			continue
		}
		signature := disabledINIEntrySignature(section, key, value)
		if consumed[signature] < existing[signature] {
			consumed[signature]++
			migrated++
			continue
		}
		id, err := randomID()
		if err != nil {
			return nil, 0, err
		}
		shiftDisabledINIPositions(store, name, section, position, 1)
		store.Entries = append(store.Entries, disabledINIEntry{
			ID:       id,
			File:     name,
			Section:  section,
			Position: position,
			Key:      key,
			Value:    value,
		})
		migrated++
	}
	if migrated == 0 {
		return data, 0, nil
	}
	document.lines = kept
	return document.bytes(), migrated, nil
}

func initializeDisabledINIState() error {
	store, err := readDisabledINIFile(disabledINIPath)
	storeExists := err == nil
	if errors.Is(err, os.ErrNotExist) {
		store = newDisabledINIFile()
	} else if err != nil {
		return fmt.Errorf("load disabled INI state: %w", err)
	}

	type migratedConfig struct {
		name string
		data []byte
		old  []byte
	}
	activeMigrations := make([]migratedConfig, 0)
	for _, name := range []string{"Game.ini", "Engine.ini"} {
		path := configPath(name, false)
		data, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		migrated, count, migrateErr := migrateLegacyDisabledEntries(name, data, &store)
		if migrateErr != nil {
			return migrateErr
		}
		if count > 0 {
			activeMigrations = append(activeMigrations, migratedConfig{
				name: name,
				data: migrated,
				old:  data,
			})
		}
	}
	if !storeExists || len(activeMigrations) > 0 {
		if err := writeDisabledINIFile(store, false); err != nil {
			return fmt.Errorf("persist disabled INI state: %w", err)
		}
	}
	for _, migration := range activeMigrations {
		if err := backupConfig(migration.name, migration.old); err != nil {
			return err
		}
		if err := writeFileAtomic(configPath(migration.name, false), migration.data, 0600); err != nil {
			return err
		}
	}

	pendingStore, pendingErr := readDisabledINIFile(pendingDisabledINIPath())
	pendingStoreExists := pendingErr == nil
	if errors.Is(pendingErr, os.ErrNotExist) {
		pendingStore = cloneDisabledINIFile(store)
	} else if pendingErr != nil {
		return fmt.Errorf("load staged disabled INI state: %w", pendingErr)
	}
	pendingChanged := false
	pendingMigrations := make([]migratedConfig, 0)
	for _, name := range []string{"Game.ini", "Engine.ini"} {
		path := configPath(name, true)
		data, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		if !pendingStoreExists {
			pendingChanged = true
			filteredSections := pendingStore.Sections[:0]
			for _, section := range pendingStore.Sections {
				if section.File != name {
					filteredSections = append(filteredSections, section)
				}
			}
			pendingStore.Sections = filteredSections
			filteredEntries := pendingStore.Entries[:0]
			for _, entry := range pendingStore.Entries {
				if entry.File != name {
					filteredEntries = append(filteredEntries, entry)
				}
			}
			pendingStore.Entries = filteredEntries
		}
		migrated, count, migrateErr := migrateLegacyDisabledEntries(name, data, &pendingStore)
		if migrateErr != nil {
			return migrateErr
		}
		if count == 0 {
			continue
		}
		pendingChanged = true
		pendingMigrations = append(pendingMigrations, migratedConfig{
			name: name,
			data: migrated,
			old:  data,
		})
	}
	if pendingChanged || pendingStoreExists {
		if err := writeDisabledINIFile(pendingStore, true); err != nil {
			return fmt.Errorf("persist staged disabled INI state: %w", err)
		}
	}
	for _, migration := range pendingMigrations {
		if err := writeFileAtomic(
			configPath(migration.name, true),
			migration.data,
			0600,
		); err != nil {
			return err
		}
	}
	return nil
}

func legacyDisabledValues(
	data []byte,
	section string,
	key string,
) []string {
	document := parseIni(data)
	currentSection := ""
	values := make([]string, 0)
	for _, line := range document.lines {
		if parsedSection, ok := sectionName(line); ok {
			currentSection = parsedSection
			continue
		}
		entryKey, value, enabled, ok := configEntryParts(line)
		if ok && !enabled &&
			currentSection == section &&
			strings.EqualFold(entryKey, key) {
			values = append(values, value)
		}
	}
	return values
}

func (m *Manager) RecoverDisabledEntriesFromBackup(
	backupPath string,
	file string,
	section string,
	key string,
) (int, error) {
	if !allowedConfigFile(file) ||
		!validIniText(section, false) ||
		!validIniText(key, false) {
		return 0, errors.New("invalid recovery selection")
	}
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return 0, err
	}
	values := legacyDisabledValues(backupData, section, key)
	if len(values) == 0 {
		return 0, errors.New("the backup contains no matching disabled entries")
	}

	m.configMu.Lock()
	defer m.configMu.Unlock()
	lock, err := acquireLifecycleLock()
	if err != nil {
		return 0, err
	}
	defer releaseLifecycleLock(lock)

	for _, path := range []string{
		configPath("Game.ini", true),
		configPath("Engine.ini", true),
		pendingDisabledINIPath(),
	} {
		if _, err := os.Stat(path); err == nil {
			return 0, errors.New("apply or discard staged configuration before recovery")
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}

	data, err := os.ReadFile(configPath(file, false))
	if err != nil {
		return 0, err
	}
	store, err := loadDisabledINIFile(false)
	if err != nil {
		return 0, err
	}
	view := makeConfigViewWithDisabled(file, data, false, store)
	insertion := -1
	existing := make(map[string]int)
	for _, candidateSection := range view.Sections {
		if candidateSection.Name != section {
			continue
		}
		insertion = len(candidateSection.Entries)
		for index, entry := range candidateSection.Entries {
			if strings.EqualFold(entry.Key, key) {
				existing[entry.Value]++
				insertion = index + 1
			}
		}
		break
	}
	if insertion < 0 {
		return 0, errors.New("the selected section is not present in the current configuration")
	}

	recovered := 0
	seen := make(map[string]int)
	for _, value := range values {
		seen[value]++
		if seen[value] <= existing[value] {
			continue
		}
		id, err := randomID()
		if err != nil {
			return 0, err
		}
		shiftDisabledINIPositions(&store, file, section, insertion, 1)
		store.Entries = append(store.Entries, disabledINIEntry{
			ID:       id,
			File:     file,
			Section:  section,
			Position: insertion,
			Key:      key,
			Value:    value,
		})
		insertion++
		recovered++
	}
	if recovered == 0 {
		return 0, nil
	}
	if current, err := os.ReadFile(disabledINIPath); err == nil {
		if err := backupDisabledINI(current); err != nil {
			return 0, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if err := writeDisabledINIFile(store, false); err != nil {
		return 0, err
	}
	return recovered, nil
}
