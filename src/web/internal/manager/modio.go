package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	modIOSettingsPath       = stateDir + "/modio.json"
	modRefreshSettingsPath  = stateDir + "/mod-refresh.json"
	modIOGameNameID         = "mordhau"
	modIOGameSessionSection = "/Script/Mordhau.MordhauGameSession"
	defaultModIOAPIBase     = "https://api.mod.io/v1"
	maxModIOResponseBytes   = 8 << 20
	maxModIODependencies    = 500
	maxDependencyWorkers    = 4
)

var modIOHTTPClient = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type modIOSettingsFile struct {
	Version  int       `json:"version"`
	APIKey   string    `json:"api_key"`
	APIBase  string    `json:"api_base"`
	GameID   int       `json:"game_id"`
	GameName string    `json:"game_name"`
	SavedAt  time.Time `json:"saved_at"`
}

type ModIOSettingsView struct {
	APIKeyConfigured bool   `json:"api_key_configured"`
	APIBase          string `json:"api_base"`
	GameID           int    `json:"game_id,omitempty"`
	GameName         string `json:"game_name,omitempty"`
}

type modIOGame struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	NameID string `json:"name_id"`
	Status int    `json:"status"`
}

type ModIOFile struct {
	ID          int    `json:"id"`
	Version     string `json:"version"`
	DateUpdated int64  `json:"date_updated"`
	Filesize    int64  `json:"filesize"`
}

type ModIOItem struct {
	ID                  int        `json:"id"`
	GameID              int        `json:"game_id,omitempty"`
	Name                string     `json:"name"`
	NameID              string     `json:"name_id,omitempty"`
	Summary             string     `json:"summary,omitempty"`
	Status              int        `json:"status,omitempty"`
	Visible             int        `json:"visible,omitempty"`
	DateUpdated         int64      `json:"date_updated,omitempty"`
	Dependencies        bool       `json:"dependencies"`
	DependencyDepth     int        `json:"dependency_depth,omitempty"`
	Modfile             *ModIOFile `json:"modfile,omitempty"`
	MetadataAvailable   bool       `json:"metadata_available"`
	GeneratedProfileURL string     `json:"profile_url,omitempty"`
}

type modIOCollection[T any] struct {
	Data         []T `json:"data"`
	ResultCount  int `json:"result_count"`
	ResultOffset int `json:"result_offset"`
	ResultLimit  int `json:"result_limit"`
	ResultTotal  int `json:"result_total"`
}

type ModInstallPlan struct {
	Target              ModIOItem   `json:"target"`
	Dependencies        []ModIOItem `json:"dependencies"`
	DependenciesChecked bool        `json:"dependencies_checked"`
}

type ConfiguredMod struct {
	ID                     int         `json:"id"`
	Enabled                bool        `json:"enabled"`
	Occurrences            int         `json:"occurrences"`
	Metadata               *ModIOItem  `json:"metadata,omitempty"`
	Dependencies           []ModIOItem `json:"dependencies"`
	DependenciesChecked    bool        `json:"dependencies_checked"`
	DependencyError        string      `json:"dependency_error,omitempty"`
	UnresolvedDependencies []int       `json:"unresolved_dependencies"`
}

type ModManagementView struct {
	Settings       ModIOSettingsView `json:"settings"`
	Mods           []ConfiguredMod   `json:"mods"`
	InvalidEntries int               `json:"invalid_entries"`
	ConfigStaged   bool              `json:"config_staged"`
	ServerRunning  bool              `json:"server_running"`
	APIError       string            `json:"api_error,omitempty"`
	Refresh        ModRefreshView    `json:"refresh"`
	Revision       uint64            `json:"revision"`
}

type ModConfigChange struct {
	Changed   bool  `json:"changed"`
	Staged    bool  `json:"staged"`
	Added     []int `json:"added"`
	Reenabled []int `json:"reenabled"`
}

func normalizeModIOAPIBase(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultModIOAPIBase
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", errors.New("mod.io API path must be an HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return "", errors.New("mod.io API path must not contain credentials, a port, query, or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "api.mod.io" && !strings.HasSuffix(host, ".modapi.io") {
		return "", errors.New("mod.io API path must use api.mod.io or a modapi.io host")
	}
	if strings.TrimSuffix(parsed.EscapedPath(), "/") != "/v1" {
		return "", errors.New("mod.io API path must end with /v1")
	}
	return "https://" + host + "/v1", nil
}

func validModIOAPIKey(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func loadModIOSettingsFile() (modIOSettingsFile, error) {
	var settings modIOSettingsFile
	if err := readJSON(modIOSettingsPath, &settings); err != nil {
		return modIOSettingsFile{}, err
	}
	if settings.Version != 1 || !validModIOAPIKey(settings.APIKey) || settings.GameID < 1 {
		return modIOSettingsFile{}, errors.New("stored mod.io settings are invalid")
	}
	apiBase, err := normalizeModIOAPIBase(settings.APIBase)
	if err != nil {
		return modIOSettingsFile{}, errors.New("stored mod.io API path is invalid")
	}
	settings.APIBase = apiBase
	if err := os.Chmod(modIOSettingsPath, 0600); err != nil {
		return modIOSettingsFile{}, err
	}
	return settings, nil
}

func publicModIOSettings(settings *modIOSettingsFile) ModIOSettingsView {
	if settings == nil {
		return ModIOSettingsView{APIBase: defaultModIOAPIBase}
	}
	return ModIOSettingsView{
		APIKeyConfigured: true,
		APIBase:          settings.APIBase,
		GameID:           settings.GameID,
		GameName:         settings.GameName,
	}
}

func (m *Manager) modIOSettings() (*modIOSettingsFile, error) {
	m.modioMu.Lock()
	defer m.modioMu.Unlock()
	settings, err := loadModIOSettingsFile()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

func (m *Manager) saveModIOSettings(apiKey, apiBase string) (ModIOSettingsView, error) {
	m.modioMu.Lock()
	defer m.modioMu.Unlock()

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		existing, err := loadModIOSettingsFile()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ModIOSettingsView{}, errors.New("enter a mod.io API key")
			}
			return ModIOSettingsView{}, err
		}
		apiKey = existing.APIKey
	}
	if !validModIOAPIKey(apiKey) {
		return ModIOSettingsView{}, errors.New("mod.io API key must contain exactly 32 letters or digits")
	}
	normalizedBase, err := normalizeModIOAPIBase(apiBase)
	if err != nil {
		return ModIOSettingsView{}, err
	}
	candidate := modIOSettingsFile{
		Version: 1,
		APIKey:  apiKey,
		APIBase: normalizedBase,
	}
	game, err := modIOFindGame(candidate)
	if err != nil {
		return ModIOSettingsView{}, err
	}
	if game.Status != 1 {
		return ModIOSettingsView{}, errors.New("the MORDHAU entry returned by mod.io is not live")
	}
	candidate.GameID = game.ID
	candidate.GameName = game.Name
	candidate.SavedAt = time.Now()
	if err := writeJSONAtomic(modIOSettingsPath, candidate, 0600); err != nil {
		return ModIOSettingsView{}, err
	}
	return publicModIOSettings(&candidate), nil
}

func (m *Manager) clearModIOSettings() error {
	m.modioMu.Lock()
	defer m.modioMu.Unlock()
	if err := os.Remove(modIOSettingsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func modIOGet(settings modIOSettingsFile, endpoint string, query url.Values, result any) error {
	base, err := url.Parse(settings.APIBase)
	if err != nil {
		return errors.New("invalid stored mod.io API path")
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(endpoint, "/")
	base.RawPath = ""
	if query == nil {
		query = make(url.Values)
	}
	query.Set("api_key", settings.APIKey)
	base.RawQuery = query.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return errors.New("failed to prepare mod.io request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "MORDHAU-Manager/1")
	response, err := modIOHTTPClient.Do(request)
	if err != nil {
		return errors.New("mod.io request failed")
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxModIOResponseBytes+1))
	if err != nil {
		return errors.New("failed to read mod.io response")
	}
	if len(data) > maxModIOResponseBytes {
		return errors.New("mod.io response exceeded the size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case http.StatusUnauthorized:
			return errors.New("mod.io rejected the API key or API path")
		case http.StatusForbidden:
			return errors.New("mod.io denied access to the requested resource")
		case http.StatusNotFound:
			return errors.New("the requested mod.io resource was not found")
		case http.StatusTooManyRequests:
			return errors.New("mod.io rate limit reached; try again later")
		default:
			return fmt.Errorf("mod.io returned HTTP %d", response.StatusCode)
		}
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("mod.io returned an invalid JSON response")
	}
	return nil
}

func modIOFindGame(settings modIOSettingsFile) (modIOGame, error) {
	var response modIOCollection[modIOGame]
	query := url.Values{
		"name_id": {modIOGameNameID},
		"_limit":  {"2"},
	}
	if err := modIOGet(settings, "games", query, &response); err != nil {
		return modIOGame{}, err
	}
	for _, game := range response.Data {
		if strings.EqualFold(game.NameID, modIOGameNameID) && game.ID > 0 {
			return game, nil
		}
	}
	return modIOGame{}, errors.New("MORDHAU was not found through the supplied mod.io API path")
}

func validModSlug(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '-' && index > 0 {
			continue
		}
		return false
	}
	return true
}

func parseModReference(reference string) (id int, slug string, err error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return 0, "", errors.New("enter a mod.io URL, name ID, or numeric Resource ID")
	}
	if parsedID, parseErr := strconv.ParseInt(reference, 10, 32); parseErr == nil && parsedID > 0 {
		return int(parsedID), "", nil
	}
	if strings.Contains(reference, "://") {
		parsed, parseErr := url.Parse(reference)
		if parseErr != nil || parsed.Scheme != "https" {
			return 0, "", errors.New("mod.io URL must use HTTPS")
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "mod.io" && host != "www.mod.io" && host != "mordhau.mod.io" {
			return 0, "", errors.New("only mod.io MORDHAU URLs are accepted")
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) == 4 && parts[0] == "g" && parts[1] == modIOGameNameID && parts[2] == "m" {
			slug = parts[3]
		} else if host == "mordhau.mod.io" && len(parts) == 1 {
			slug = parts[0]
		} else {
			return 0, "", errors.New("URL is not a MORDHAU mod.io mod page")
		}
	} else {
		slug = reference
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if !validModSlug(slug) {
		return 0, "", errors.New("invalid mod.io mod name ID")
	}
	return 0, slug, nil
}

func prepareModIOItem(item ModIOItem) ModIOItem {
	item.MetadataAvailable = true
	if validModSlug(item.NameID) {
		item.GeneratedProfileURL = "https://mod.io/g/mordhau/m/" + item.NameID
	}
	return item
}

func modIOGetMod(settings modIOSettingsFile, reference string) (ModIOItem, error) {
	id, slug, err := parseModReference(reference)
	if err != nil {
		return ModIOItem{}, err
	}
	if id > 0 {
		var item ModIOItem
		if err := modIOGet(settings,
			fmt.Sprintf("games/%d/mods/%d", settings.GameID, id), nil, &item); err != nil {
			return ModIOItem{}, err
		}
		return prepareModIOItem(item), nil
	}

	var response modIOCollection[ModIOItem]
	query := url.Values{
		"name_id": {slug},
		"_limit":  {"2"},
	}
	if err := modIOGet(settings,
		fmt.Sprintf("games/%d/mods", settings.GameID), query, &response); err != nil {
		return ModIOItem{}, err
	}
	for _, item := range response.Data {
		if item.NameID == slug {
			return prepareModIOItem(item), nil
		}
	}
	return ModIOItem{}, errors.New("mod.io mod was not found")
}

func validateInstallableMod(item ModIOItem, gameID int) error {
	if item.ID < 1 || item.GameID != gameID {
		return errors.New("mod.io returned a mod for a different game")
	}
	if item.Status != 1 || item.Visible != 1 {
		return fmt.Errorf("mod %d is not public and live", item.ID)
	}
	if item.Modfile == nil || item.Modfile.ID < 1 {
		return fmt.Errorf("mod %d has no live mod file", item.ID)
	}
	return nil
}

func modIODependencies(settings modIOSettingsFile, modID int) ([]ModIOItem, error) {
	result := make([]ModIOItem, 0)
	offset := 0
	for {
		var response modIOCollection[ModIOItem]
		query := url.Values{
			"recursive": {"true"},
			"_limit":    {"100"},
			"_offset":   {strconv.Itoa(offset)},
		}
		if err := modIOGet(settings,
			fmt.Sprintf("games/%d/mods/%d/dependencies", settings.GameID, modID),
			query, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Data {
			result = append(result, prepareModIOItem(item))
			if len(result) > maxModIODependencies {
				return nil, errors.New("mod dependency tree exceeds the safety limit")
			}
		}
		offset += len(response.Data)
		if len(response.Data) == 0 || offset >= response.ResultTotal {
			break
		}
	}

	deduplicated := make(map[int]ModIOItem, len(result))
	for _, item := range result {
		if item.ID == modID {
			continue
		}
		if err := validateInstallableMod(item, settings.GameID); err != nil {
			return nil, fmt.Errorf("dependency validation failed: %w", err)
		}
		existing, found := deduplicated[item.ID]
		if !found || item.DependencyDepth > existing.DependencyDepth {
			deduplicated[item.ID] = item
		}
	}
	result = result[:0]
	for _, item := range deduplicated {
		result = append(result, item)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].DependencyDepth != result[right].DependencyDepth {
			return result[left].DependencyDepth > result[right].DependencyDepth
		}
		return result[left].ID < result[right].ID
	})
	return result, nil
}

func (m *Manager) modInstallPlan(reference string) (ModInstallPlan, error) {
	settings, err := m.modIOSettings()
	if err != nil {
		return ModInstallPlan{}, err
	}
	if settings == nil {
		id, slug, err := parseModReference(reference)
		if err != nil {
			return ModInstallPlan{}, err
		}
		if id < 1 || slug != "" {
			return ModInstallPlan{}, errors.New("configure a mod.io API key to resolve URLs and dependencies")
		}
		return ModInstallPlan{
			Target: ModIOItem{
				ID:   id,
				Name: fmt.Sprintf("Resource ID %d", id),
			},
			Dependencies:        []ModIOItem{},
			DependenciesChecked: false,
		}, nil
	}

	target, err := modIOGetMod(*settings, reference)
	if err != nil {
		return ModInstallPlan{}, err
	}
	if err := validateInstallableMod(target, settings.GameID); err != nil {
		return ModInstallPlan{}, err
	}
	dependencies, err := modIODependencies(*settings, target.ID)
	if err != nil {
		return ModInstallPlan{}, err
	}
	return ModInstallPlan{
		Target:              target,
		Dependencies:        dependencies,
		DependenciesChecked: true,
	}, nil
}

func planModIDs(plan ModInstallPlan) []int {
	result := make([]int, 0, len(plan.Dependencies)+1)
	seen := make(map[int]bool)
	for _, item := range plan.Dependencies {
		if item.ID > 0 && !seen[item.ID] {
			seen[item.ID] = true
			result = append(result, item.ID)
		}
	}
	if plan.Target.ID > 0 && !seen[plan.Target.ID] {
		result = append(result, plan.Target.ID)
	}
	return result
}

func findSectionBoundsFold(lines []string, wanted string) (start, end int, found bool) {
	for index, line := range lines {
		name, ok := sectionName(line)
		if !ok || !strings.EqualFold(name, wanted) {
			continue
		}
		end = len(lines)
		for next := index + 1; next < len(lines); next++ {
			if _, ok := sectionName(lines[next]); ok {
				end = next
				break
			}
		}
		return index, end, true
	}
	return 0, 0, false
}

func configuredModsFromData(data []byte) ([]ConfiguredMod, int) {
	return configuredModsFromState(data, newDisabledINIFile())
}

func configuredModsFromState(
	data []byte,
	store disabledINIFile,
) ([]ConfiguredMod, int) {
	view := makeConfigViewWithDisabled("Game.ini", data, false, store)
	result := make([]ConfiguredMod, 0)
	indexByID := make(map[int]int)
	invalid := 0
	for _, section := range view.Sections {
		if !strings.EqualFold(section.Name, modIOGameSessionSection) {
			continue
		}
		for _, entry := range section.Entries {
			if !strings.EqualFold(entry.Key, "Mods") {
				continue
			}
			parsed, err := strconv.ParseInt(strings.TrimSpace(entry.Value), 10, 32)
			if err != nil || parsed < 1 {
				invalid++
				continue
			}
			id := int(parsed)
			if index, exists := indexByID[id]; exists {
				result[index].Occurrences++
				result[index].Enabled = result[index].Enabled || entry.Enabled
				continue
			}
			indexByID[id] = len(result)
			result = append(result, ConfiguredMod{
				ID:                     id,
				Enabled:                entry.Enabled,
				Occurrences:            1,
				Dependencies:           []ModIOItem{},
				UnresolvedDependencies: []int{},
			})
		}
		break
	}
	return result, invalid
}

func modIOGetModsByIDs(settings modIOSettingsFile, ids []int) (map[int]ModIOItem, error) {
	result := make(map[int]ModIOItem, len(ids))
	for offset := 0; offset < len(ids); offset += 50 {
		end := offset + 50
		if end > len(ids) {
			end = len(ids)
		}
		values := make([]string, 0, end-offset)
		for _, id := range ids[offset:end] {
			values = append(values, strconv.Itoa(id))
		}
		var response modIOCollection[ModIOItem]
		query := url.Values{
			"id-in":  {strings.Join(values, ",")},
			"_limit": {strconv.Itoa(len(values))},
		}
		if err := modIOGet(settings,
			fmt.Sprintf("games/%d/mods", settings.GameID), query, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Data {
			item = prepareModIOItem(item)
			result[item.ID] = item
		}
	}
	return result, nil
}

type configuredModDependencyResult struct {
	Index        int
	Dependencies []ModIOItem
	Err          error
}

func loadConfiguredModDependencies(
	settings modIOSettingsFile,
	configured []ConfiguredMod,
) {
	indices := make([]int, 0, len(configured))
	for index := range configured {
		configured[index].Dependencies = []ModIOItem{}
		configured[index].UnresolvedDependencies = []int{}
		if configured[index].Metadata == nil {
			continue
		}
		if !configured[index].Metadata.Dependencies {
			configured[index].DependenciesChecked = true
			continue
		}
		indices = append(indices, index)
	}
	if len(indices) == 0 {
		markUnresolvedModDependencies(configured)
		return
	}

	workerCount := maxDependencyWorkers
	if len(indices) < workerCount {
		workerCount = len(indices)
	}
	jobs := make(chan int)
	results := make(chan configuredModDependencyResult, len(indices))
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			for index := range jobs {
				dependencies, err := modIODependencies(settings, configured[index].ID)
				results <- configuredModDependencyResult{
					Index:        index,
					Dependencies: dependencies,
					Err:          err,
				}
			}
		}()
	}
	go func() {
		for _, index := range indices {
			jobs <- index
		}
		close(jobs)
	}()

	for range indices {
		result := <-results
		if result.Err != nil {
			configured[result.Index].DependencyError = result.Err.Error()
			continue
		}
		configured[result.Index].Dependencies = result.Dependencies
		configured[result.Index].DependenciesChecked = true
	}
	markUnresolvedModDependencies(configured)
}

func markUnresolvedModDependencies(configured []ConfiguredMod) {
	enabled := make(map[int]bool, len(configured))
	for _, item := range configured {
		if item.Enabled {
			enabled[item.ID] = true
		}
	}
	for index := range configured {
		configured[index].UnresolvedDependencies = []int{}
		if !configured[index].Enabled || !configured[index].DependenciesChecked {
			continue
		}
		for _, dependency := range configured[index].Dependencies {
			if dependency.ID > 0 && !enabled[dependency.ID] {
				configured[index].UnresolvedDependencies = append(
					configured[index].UnresolvedDependencies,
					dependency.ID,
				)
			}
		}
	}
}

func (m *Manager) modManagementView() (ModManagementView, error) {
	m.configMu.Lock()
	data, staged, err := readConfig("Game.ini")
	if err != nil {
		m.configMu.Unlock()
		return ModManagementView{}, err
	}
	storeStaged, err := disabledINIStateStaged(staged)
	if err != nil {
		m.configMu.Unlock()
		return ModManagementView{}, err
	}
	store, err := loadDisabledINIFile(storeStaged)
	m.configMu.Unlock()
	if err != nil {
		return ModManagementView{}, err
	}
	configured, invalid := configuredModsFromState(data, store)
	settings, err := m.modIOSettings()
	if err != nil {
		return ModManagementView{}, err
	}
	view := ModManagementView{
		Settings:       publicModIOSettings(settings),
		Mods:           configured,
		InvalidEntries: invalid,
		ConfigStaged:   staged,
		ServerRunning:  serverRunning(),
	}
	if settings == nil || len(configured) == 0 {
		return view, nil
	}
	ids := make([]int, 0, len(configured))
	for _, item := range configured {
		ids = append(ids, item.ID)
	}
	metadata, err := modIOGetModsByIDs(*settings, ids)
	if err != nil {
		view.APIError = err.Error()
		return view, nil
	}
	for index := range view.Mods {
		if item, found := metadata[view.Mods[index].ID]; found {
			copy := item
			view.Mods[index].Metadata = &copy
		}
	}
	loadConfiguredModDependencies(*settings, view.Mods)
	return view, nil
}

func insertConfigLine(lines []string, index int, line string) []string {
	lines = append(lines, "")
	copy(lines[index+1:], lines[index:])
	lines[index] = line
	return lines
}

func modOccurrences(
	document *iniDocument,
	store disabledINIFile,
	id int,
) (sectionIndex int, activeLines []int, disabledIDs []string, found bool) {
	view := makeConfigViewWithDisabled("Game.ini", document.bytes(), false, store)
	sectionIndex = -1
	for index, section := range view.Sections {
		if !strings.EqualFold(section.Name, modIOGameSessionSection) {
			continue
		}
		sectionIndex = index
		for _, entry := range section.Entries {
			if !strings.EqualFold(entry.Key, "Mods") {
				continue
			}
			parsed, err := strconv.ParseInt(strings.TrimSpace(entry.Value), 10, 32)
			if err != nil || int(parsed) != id {
				continue
			}
			found = true
			if entry.Enabled && entry.Line >= 0 {
				activeLines = append(activeLines, entry.Line)
			} else if entry.ID != "" {
				disabledIDs = append(disabledIDs, entry.ID)
			}
		}
		break
	}
	return sectionIndex, activeLines, disabledIDs, found
}

func ensureModSection(document *iniDocument) {
	if _, _, found := findSectionBoundsFold(document.lines, modIOGameSessionSection); found {
		return
	}
	if len(document.lines) > 0 && strings.TrimSpace(document.lines[len(document.lines)-1]) != "" {
		document.lines = append(document.lines, "")
	}
	document.lines = append(document.lines, "["+modIOGameSessionSection+"]")
}

func addModsToConfigState(
	document *iniDocument,
	store *disabledINIFile,
	ids []int,
) (ModConfigChange, error) {
	change := ModConfigChange{Added: []int{}, Reenabled: []int{}}
	ensureModSection(document)
	for _, id := range ids {
		sectionIndex, activeLines, disabledIDs, _ := modOccurrences(document, *store, id)
		if len(activeLines) > 0 {
			continue
		}
		if len(disabledIDs) > 0 {
			if err := enableConfigEntry("Game.ini", document, store, disabledIDs[0]); err != nil {
				return ModConfigChange{}, err
			}
			change.Changed = true
			change.Reenabled = append(change.Reenabled, id)
			continue
		}
		view := makeConfigViewWithDisabled("Game.ini", document.bytes(), false, *store)
		if sectionIndex < 0 || sectionIndex >= len(view.Sections) ||
			!strings.EqualFold(view.Sections[sectionIndex].Name, modIOGameSessionSection) {
			for index, section := range view.Sections {
				if strings.EqualFold(section.Name, modIOGameSessionSection) {
					sectionIndex = index
					break
				}
			}
		}
		if sectionIndex < 0 || sectionIndex >= len(view.Sections) {
			return ModConfigChange{}, errors.New("MORDHAU game session section was not found")
		}
		section := view.Sections[sectionIndex]
		if !section.Enabled {
			stateIndex, _ := disabledINISectionByName(
				store,
				"Game.ini",
				configSectionStorageName(section),
			)
			if stateIndex < 0 {
				return ModConfigChange{}, errRevisionConflict
			}
			takeDisabledINISectionAt(store, stateIndex)
			view = makeConfigViewWithDisabled(
				"Game.ini",
				document.bytes(),
				false,
				*store,
			)
			for index, candidate := range view.Sections {
				if strings.EqualFold(candidate.Name, modIOGameSessionSection) {
					sectionIndex = index
					section = candidate
					break
				}
			}
		}
		if err := insertConfigEntry(
			document,
			view,
			sectionIndex,
			len(section.Entries)-1,
			"Mods="+strconv.Itoa(id),
		); err != nil {
			return ModConfigChange{}, err
		}
		change.Changed = true
		change.Added = append(change.Added, id)
	}
	return change, nil
}

func setModEnabledInConfigState(
	document *iniDocument,
	store *disabledINIFile,
	id int,
	enabled bool,
) (ModConfigChange, error) {
	_, activeLines, disabledIDs, found := modOccurrences(document, *store, id)
	if !found {
		return ModConfigChange{}, errors.New("mod Resource ID is not configured")
	}
	changed := false
	if enabled {
		for _, entryID := range disabledIDs {
			if err := enableConfigEntry("Game.ini", document, store, entryID); err != nil {
				return ModConfigChange{}, err
			}
			changed = true
		}
	} else {
		sort.Ints(activeLines)
		for index := len(activeLines) - 1; index >= 0; index-- {
			if err := disableConfigEntry("Game.ini", document, store, activeLines[index]); err != nil {
				return ModConfigChange{}, err
			}
			changed = true
		}
	}
	return ModConfigChange{Changed: changed}, nil
}

func removeModFromConfigState(
	document *iniDocument,
	store *disabledINIFile,
	id int,
) (ModConfigChange, error) {
	_, activeLines, disabledIDs, found := modOccurrences(document, *store, id)
	if !found {
		return ModConfigChange{}, errors.New("mod Resource ID is not configured")
	}
	sort.Ints(activeLines)
	for index := len(activeLines) - 1; index >= 0; index-- {
		if err := removeConfigEntry(
			"Game.ini",
			document,
			store,
			activeLines[index],
			"",
		); err != nil {
			return ModConfigChange{}, err
		}
	}
	for _, entryID := range disabledIDs {
		if err := removeConfigEntry("Game.ini", document, store, -1, entryID); err != nil {
			return ModConfigChange{}, err
		}
	}
	return ModConfigChange{Changed: true}, nil
}

func (m *Manager) mutateModConfig(
	mutation func(document *iniDocument, store *disabledINIFile) (ModConfigChange, error),
) (ModConfigChange, error) {
	m.configMu.Lock()
	defer m.configMu.Unlock()

	lock, err := acquireLifecycleLock()
	if err != nil {
		return ModConfigChange{}, err
	}
	defer releaseLifecycleLock(lock)

	data, staged, err := readConfig("Game.ini")
	if err != nil {
		return ModConfigChange{}, err
	}
	storeStaged, err := disabledINIStateStaged(staged)
	if err != nil {
		return ModConfigChange{}, err
	}
	store, err := loadDisabledINIFile(storeStaged)
	if err != nil {
		return ModConfigChange{}, err
	}
	oldStore := cloneDisabledINIFile(store)
	document := parseIni(data)
	change, err := mutation(&document, &store)
	if err != nil {
		return ModConfigChange{}, err
	}
	targetStaged := staged || storeStaged || serverRunning()
	change.Staged = targetStaged
	if !change.Changed {
		return change, nil
	}
	if err := persistConfigState(
		"Game.ini",
		data,
		document.bytes(),
		oldStore,
		store,
		targetStaged,
	); err != nil {
		return ModConfigChange{}, err
	}
	return change, nil
}

func (m *Manager) addConfiguredMods(ids []int) (ModConfigChange, error) {
	unique := make([]int, 0, len(ids))
	seen := make(map[int]bool)
	for _, id := range ids {
		if id < 1 || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return ModConfigChange{}, errors.New("no valid mod Resource IDs were supplied")
	}

	return m.mutateModConfig(func(
		document *iniDocument,
		store *disabledINIFile,
	) (ModConfigChange, error) {
		return addModsToConfigState(document, store, unique)
	})
}

func (m *Manager) setConfiguredModEnabled(id int, enabled bool) (ModConfigChange, error) {
	if id < 1 {
		return ModConfigChange{}, errors.New("invalid mod Resource ID")
	}
	return m.mutateModConfig(func(
		document *iniDocument,
		store *disabledINIFile,
	) (ModConfigChange, error) {
		return setModEnabledInConfigState(document, store, id, enabled)
	})
}

func (m *Manager) removeConfiguredMod(id int) (ModConfigChange, error) {
	if id < 1 {
		return ModConfigChange{}, errors.New("invalid mod Resource ID")
	}
	return m.mutateModConfig(func(
		document *iniDocument,
		store *disabledINIFile,
	) (ModConfigChange, error) {
		return removeModFromConfigState(document, store, id)
	})
}
