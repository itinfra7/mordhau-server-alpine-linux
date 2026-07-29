package manager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	mapRotationSection = "/Script/Mordhau.MordhauGameMode"
	mapRotationKey     = "MapRotation"
	mapRotationMaximum = 1024
)

type MapRotationEntry struct {
	ID        string `json:"id"`
	Map       string `json:"map"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	ModeID    string `json:"mode_id,omitempty"`
	ModeName  string `json:"mode_name,omitempty"`
	Source    string `json:"source,omitempty"`
}

type MapRotationView struct {
	Revision       string               `json:"revision"`
	Staged         bool                 `json:"staged"`
	SectionEnabled bool                 `json:"section_enabled"`
	Entries        []MapRotationEntry   `json:"entries"`
	GameModes      []MapCatalogGameMode `json:"game_modes"`
	CatalogError   string               `json:"catalog_error,omitempty"`
	Warnings       []string             `json:"warnings"`
}

type mapRotationSaveRequest struct {
	Revision string             `json:"revision"`
	Entries  []MapRotationEntry `json:"entries"`
}

type mapRotationCatalogItem struct {
	ModeID   string
	ModeName string
	Source   string
}

func mapRotationCatalogIndex(
	catalog MapCatalogView,
) map[string]mapRotationCatalogItem {
	index := make(map[string]mapRotationCatalogItem)
	for _, mode := range catalog.GameModes {
		for _, entry := range mode.Maps {
			key := strings.ToLower(entry.Name)
			if existing, duplicate := index[key]; duplicate {
				existing.ModeID = ""
				existing.ModeName = "Multiple game modes"
				if existing.Source != entry.Source {
					existing.Source = "Multiple installed sources"
				}
				index[key] = existing
				continue
			}
			index[key] = mapRotationCatalogItem{
				ModeID:   mode.ID,
				ModeName: mode.Name,
				Source:   entry.Source,
			}
		}
	}
	return index
}

func mapRotationConfigEntries(view ConfigView) (IniSection, []IniEntry, bool) {
	for _, section := range view.Sections {
		if !strings.EqualFold(section.Name, mapRotationSection) {
			continue
		}
		entries := make([]IniEntry, 0)
		for _, entry := range section.Entries {
			if strings.EqualFold(entry.Key, mapRotationKey) {
				entries = append(entries, entry)
			}
		}
		return section, entries, true
	}
	return IniSection{Name: mapRotationSection, Enabled: true}, []IniEntry{}, false
}

func mapRotationViewFromConfig(
	config ConfigView,
	catalog MapCatalogView,
	catalogErr error,
) MapRotationView {
	section, entries, _ := mapRotationConfigEntries(config)
	index := mapRotationCatalogIndex(catalog)
	out := MapRotationView{
		Revision:       config.Revision,
		Staged:         config.Staged,
		SectionEnabled: section.Enabled,
		Entries:        make([]MapRotationEntry, 0, len(entries)),
		GameModes:      append([]MapCatalogGameMode(nil), catalog.GameModes...),
		Warnings:       append([]string(nil), catalog.Warnings...),
	}
	if catalogErr != nil {
		out.CatalogError = catalogErr.Error()
	}
	for _, entry := range entries {
		id := entry.ID
		if id == "" {
			id = fmt.Sprintf("line:%d", entry.Line)
		}
		item := MapRotationEntry{
			ID:      id,
			Map:     entry.Value,
			Enabled: entry.Enabled && section.Enabled,
		}
		if found, ok := index[strings.ToLower(entry.Value)]; ok {
			item.Available = true
			item.ModeID = found.ModeID
			item.ModeName = found.ModeName
			item.Source = found.Source
		}
		out.Entries = append(out.Entries, item)
	}
	return out
}

func (m *Manager) mapRotationView(ctx context.Context) (MapRotationView, error) {
	config, err := m.configView("Game.ini")
	if err != nil {
		return MapRotationView{}, err
	}
	catalog, catalogErr := m.mapCatalog(ctx)
	return mapRotationViewFromConfig(config, catalog, catalogErr), nil
}

func mapRotationRequestValid(
	request []MapRotationEntry,
	existing map[string]struct{},
	catalog map[string]mapRotationCatalogItem,
) error {
	if len(request) > mapRotationMaximum {
		return fmt.Errorf("map rotation cannot exceed %d entries", mapRotationMaximum)
	}
	seenIDs := make(map[string]bool, len(request))
	for _, entry := range request {
		if entry.ID != "" {
			if seenIDs[entry.ID] {
				return errors.New("map rotation contains a duplicate entry identity")
			}
			seenIDs[entry.ID] = true
		}
		if !validMordhauObjectName(entry.Map) {
			return fmt.Errorf("invalid map rotation entry %q", entry.Map)
		}
		key := strings.ToLower(entry.Map)
		if _, installed := catalog[key]; !installed {
			if _, preserved := existing[key]; !preserved {
				return fmt.Errorf(
					"map %q is not in the installed-content catalog",
					entry.Map,
				)
			}
		}
	}
	return nil
}

func replaceMapRotation(
	data []byte,
	store disabledINIFile,
	request []MapRotationEntry,
) ([]byte, disabledINIFile, error) {
	document := parseIni(data)
	view := makeConfigViewWithDisabled("Game.ini", data, false, store)
	sectionIndex := -1
	var section IniSection
	for index, candidate := range view.Sections {
		if strings.EqualFold(candidate.Name, mapRotationSection) {
			sectionIndex = index
			section = candidate
			break
		}
	}
	if sectionIndex < 0 {
		if len(document.lines) > 0 &&
			strings.TrimSpace(document.lines[len(document.lines)-1]) != "" {
			document.lines = append(document.lines, "")
		}
		document.lines = append(document.lines, "["+mapRotationSection+"]")
		view = makeConfigViewWithDisabled(
			"Game.ini",
			document.bytes(),
			false,
			store,
		)
		for index, candidate := range view.Sections {
			if strings.EqualFold(candidate.Name, mapRotationSection) {
				sectionIndex = index
				section = candidate
				break
			}
		}
	}
	if sectionIndex < 0 {
		return nil, disabledINIFile{}, errors.New("could not create map rotation section")
	}
	sectionStorageName := configSectionStorageName(section)

	rotationPositions := make(map[int]struct{})
	rotationIDs := make(map[string]struct{})
	activeLines := make([]int, 0)
	anchor := len(section.Entries)
	for position, entry := range section.Entries {
		if !strings.EqualFold(entry.Key, mapRotationKey) {
			continue
		}
		rotationPositions[position] = struct{}{}
		if position < anchor {
			anchor = position
		}
		if entry.ID != "" {
			rotationIDs[entry.ID] = struct{}{}
		}
		if entry.Enabled && entry.Line >= 0 {
			activeLines = append(activeLines, entry.Line)
		}
	}

	type combinedItem struct {
		existing *IniEntry
		request  *MapRotationEntry
	}
	combined := make([]combinedItem, 0, len(section.Entries)+len(request))
	inserted := false
	for position := range section.Entries {
		if position == anchor {
			for index := range request {
				item := request[index]
				combined = append(combined, combinedItem{request: &item})
			}
			inserted = true
		}
		if _, rotation := rotationPositions[position]; rotation {
			continue
		}
		item := section.Entries[position]
		combined = append(combined, combinedItem{existing: &item})
	}
	if !inserted {
		for index := range request {
			item := request[index]
			combined = append(combined, combinedItem{request: &item})
		}
	}

	disabledPositions := make(map[string]int)
	for position, item := range combined {
		if item.existing != nil && item.existing.ID != "" {
			disabledPositions[item.existing.ID] = position
		}
	}
	filtered := make([]disabledINIEntry, 0, len(store.Entries))
	for _, entry := range store.Entries {
		if entry.File == "Game.ini" &&
			strings.EqualFold(entry.Section, sectionStorageName) {
			if _, rotation := rotationIDs[entry.ID]; rotation ||
				strings.EqualFold(entry.Key, mapRotationKey) {
				continue
			}
			if position, exists := disabledPositions[entry.ID]; exists {
				entry.Position = position
			}
		}
		filtered = append(filtered, entry)
	}
	store.Entries = filtered

	reusableIDs := make(map[string]struct{}, len(rotationIDs))
	for id := range rotationIDs {
		reusableIDs[id] = struct{}{}
	}
	for position, item := range combined {
		if item.request == nil ||
			(item.request.Enabled && section.Enabled) {
			continue
		}
		id := item.request.ID
		if _, reusable := reusableIDs[id]; reusable {
			delete(reusableIDs, id)
		} else {
			var err error
			id, err = randomID()
			if err != nil {
				return nil, disabledINIFile{}, err
			}
		}
		store.Entries = append(store.Entries, disabledINIEntry{
			ID:       id,
			File:     "Game.ini",
			Section:  sectionStorageName,
			Position: position,
			Key:      mapRotationKey,
			Value:    item.request.Map,
		})
	}

	start, end, found := findSectionBoundsFold(document.lines, section.Name)
	if !found {
		return nil, disabledINIFile{}, errors.New("map rotation section disappeared")
	}
	insertAt := end
	if len(activeLines) > 0 {
		sort.Ints(activeLines)
		insertAt = activeLines[0]
	} else {
		for position := anchor; position < len(section.Entries); position++ {
			entry := section.Entries[position]
			if entry.Enabled && entry.Line >= 0 {
				insertAt = entry.Line
				break
			}
		}
		if insertAt < start+1 || insertAt > end {
			insertAt = end
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(activeLines)))
	for _, line := range activeLines {
		if line < 0 || line >= len(document.lines) {
			return nil, disabledINIFile{}, errRevisionConflict
		}
		document.lines = append(document.lines[:line], document.lines[line+1:]...)
		if line < insertAt {
			insertAt--
		}
	}
	for _, item := range request {
		if !item.Enabled || !section.Enabled {
			continue
		}
		document.lines = insertConfigLine(
			document.lines,
			insertAt,
			formatConfigEntry(mapRotationKey, item.Map, true),
		)
		insertAt++
	}
	return document.bytes(), store, nil
}

func (m *Manager) saveMapRotation(
	ctx context.Context,
	request mapRotationSaveRequest,
) (MapRotationView, error) {
	catalog, catalogErr := m.mapCatalog(ctx)
	m.configMu.Lock()
	defer m.configMu.Unlock()
	lock, err := acquireLifecycleLock()
	if err != nil {
		return MapRotationView{}, err
	}
	defer releaseLifecycleLock(lock)

	data, staged, err := readConfig("Game.ini")
	if err != nil {
		return MapRotationView{}, err
	}
	storeStaged, err := disabledINIStateStaged(staged)
	if err != nil {
		return MapRotationView{}, err
	}
	store, err := loadDisabledINIFile(storeStaged)
	if err != nil {
		return MapRotationView{}, err
	}
	if request.Revision == "" ||
		request.Revision != configRevision("Game.ini", data, store) {
		return MapRotationView{}, errRevisionConflict
	}
	config := makeConfigViewWithDisabled("Game.ini", data, staged, store)
	_, existingEntries, _ := mapRotationConfigEntries(config)
	existing := make(map[string]struct{}, len(existingEntries))
	for _, entry := range existingEntries {
		existing[strings.ToLower(entry.Value)] = struct{}{}
	}
	if err := mapRotationRequestValid(
		request.Entries,
		existing,
		mapRotationCatalogIndex(catalog),
	); err != nil {
		if catalogErr != nil {
			return MapRotationView{}, fmt.Errorf("%w: %v", err, catalogErr)
		}
		return MapRotationView{}, err
	}
	oldStore := cloneDisabledINIFile(store)
	newData, newStore, err := replaceMapRotation(data, store, request.Entries)
	if err != nil {
		return MapRotationView{}, err
	}
	targetStaged := staged || storeStaged || serverRunning()
	if err := persistConfigState(
		"Game.ini",
		data,
		newData,
		oldStore,
		newStore,
		targetStaged,
	); err != nil {
		return MapRotationView{}, err
	}
	resultConfig := makeConfigViewWithDisabled(
		"Game.ini",
		newData,
		targetStaged,
		newStore,
	)
	return mapRotationViewFromConfig(resultConfig, catalog, catalogErr), nil
}
