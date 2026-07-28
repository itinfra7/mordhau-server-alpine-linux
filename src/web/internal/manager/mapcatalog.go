package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultRepakPath          = rootDir + "/bin/repak.exe"
	mapCatalogCommandTimeout  = 45 * time.Second
	mapCatalogBuildTimeout    = 2 * time.Minute
	mapCatalogListOutputLimit = 32 << 20
	mapCatalogAssetReadLimit  = 64 << 20
	mapCatalogMaximumMaps     = 4096
)

var (
	errMapCatalogUnavailable = errors.New("map catalog is unavailable")
	errMapSelectionInvalid   = errors.New("invalid map selection")
	errMapAssetReadLimit     = errors.New("repak map asset exceeded the read limit")
)

type MapCatalogMap struct {
	Name    string `json:"name"`
	Package string `json:"package"`
	Source  string `json:"source"`
}

type MapCatalogGameMode struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Class   string          `json:"class"`
	Sources []string        `json:"sources"`
	Maps    []MapCatalogMap `json:"maps"`
}

type MapCatalogView struct {
	GameModes   []MapCatalogGameMode `json:"game_modes"`
	MapCount    int                  `json:"map_count"`
	Warnings    []string             `json:"warnings"`
	GeneratedAt time.Time            `json:"generated_at"`
}

type mapCatalogCache struct {
	Fingerprint string
	View        MapCatalogView
}

type mapCatalogPak struct {
	Path         string
	Source       string
	Official     bool
	DeclaredMaps map[string]struct{}
}

type mapCatalogMapOwner struct {
	Package       string
	GameModeClass string
}

type officialMapMode struct {
	Prefix string
	Name   string
	Class  string
}

var officialMapModes = []officialMapMode{
	{Prefix: "FFA_", Name: "Deathmatch", Class: "BP_DeathmatchGameMode_C"},
	{Prefix: "TDM_", Name: "Team Deathmatch", Class: "BP_TeamDeathmatchGameMode_C"},
	{Prefix: "SKM_", Name: "Skirmish", Class: "BP_SkirmishGameMode_C"},
	{Prefix: "FL_", Name: "Frontline", Class: "BP_FrontlineGameMode_C"},
	{Prefix: "INV_", Name: "Invasion", Class: "BP_InvasionGameMode_C"},
	{Prefix: "HRD_", Name: "Horde", Class: "BP_HordeGameMode_C"},
	{Prefix: "DIH_", Name: "Demon Horde", Class: "BP_DemonHordeGameMode_C"},
	{Prefix: "SG_", Name: "Swordgame", Class: "BP_SwordGameGameMode_C"},
	{Prefix: "BR_", Name: "Battle Royale", Class: "BP_BattleRoyaleGameMode_C"},
}

type limitedCommandOutput struct {
	buffer bytes.Buffer
	limit  int
	full   bool
}

func (output *limitedCommandOutput) Write(data []byte) (int, error) {
	if output.limit < 1 {
		return len(data), nil
	}
	remaining := output.limit - output.buffer.Len()
	if remaining <= 0 {
		output.full = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = output.buffer.Write(data[:remaining])
		output.full = true
		return len(data), nil
	}
	_, _ = output.buffer.Write(data)
	return len(data), nil
}

func mapCatalogRepakEnvironment() []string {
	return append(
		os.Environ(),
		"WINEPREFIX="+rootDir+"/.wine",
		"WINEDEBUG=-all",
	)
}

func runRepakText(
	ctx context.Context,
	repakPath string,
	arguments ...string,
) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, mapCatalogCommandTimeout)
	defer cancel()
	command := exec.CommandContext(
		commandContext,
		"wine",
		append([]string{repakPath}, arguments...)...,
	)
	command.Env = mapCatalogRepakEnvironment()
	var stdout limitedCommandOutput
	var stderr limitedCommandOutput
	stdout.limit = mapCatalogListOutputLimit
	stderr.limit = 64 << 10
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return "", errors.New("repak command timed out")
	}
	if stdout.full {
		return "", errors.New("repak listing exceeded the output limit")
	}
	if err != nil {
		message := strings.TrimSpace(stderr.buffer.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("repak: %s", message)
	}
	return stdout.buffer.String(), nil
}

type mapAssetStringCollector struct {
	run                []byte
	hasDefaultGameMode bool
	gameModeClasses    map[string]struct{}
	bytesRead          int
	full               bool
}

func mapGameModeClassCandidate(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "Default__") {
		return "", false
	}
	if separator := strings.LastIndexAny(value, "/."); separator >= 0 {
		value = value[separator+1:]
	}
	if !strings.HasSuffix(value, "_C") ||
		!strings.Contains(strings.ToLower(value), "gamemode") ||
		!validMordhauObjectName(value) {
		return "", false
	}
	return value, true
}

func (collector *mapAssetStringCollector) flush() {
	if len(collector.run) == 0 {
		return
	}
	value := string(collector.run)
	collector.run = collector.run[:0]
	if value == "DefaultGameMode" {
		collector.hasDefaultGameMode = true
		return
	}
	if candidate, ok := mapGameModeClassCandidate(value); ok {
		if collector.gameModeClasses == nil {
			collector.gameModeClasses = make(map[string]struct{})
		}
		collector.gameModeClasses[candidate] = struct{}{}
	}
}

func (collector *mapAssetStringCollector) Write(data []byte) (int, error) {
	remaining := mapCatalogAssetReadLimit - collector.bytesRead
	if remaining <= 0 {
		collector.full = true
		return 0, errMapAssetReadLimit
	}
	accepted := len(data)
	if accepted > remaining {
		accepted = remaining
		collector.full = true
	}
	for _, value := range data[:accepted] {
		if value >= 0x20 && value <= 0x7e {
			if len(collector.run) < 511 {
				collector.run = append(collector.run, value)
			}
			continue
		}
		collector.flush()
	}
	collector.bytesRead += accepted
	if collector.full {
		return accepted, errMapAssetReadLimit
	}
	return accepted, nil
}

func (collector *mapAssetStringCollector) defaultGameMode() string {
	collector.flush()
	// Unreal name tables are not a property/value stream, so proximity to the
	// "DefaultGameMode" name is not reliable. Accept only an unambiguous class
	// reference; otherwise omit the map instead of offering an invalid pairing.
	if collector.hasDefaultGameMode && len(collector.gameModeClasses) == 1 {
		for value := range collector.gameModeClasses {
			return value
		}
	}
	return ""
}

func runRepakAssetStrings(
	ctx context.Context,
	repakPath string,
	pakPath string,
	assetPaths []string,
) (string, error) {
	var collector mapAssetStringCollector
	for _, assetPath := range assetPaths {
		commandContext, cancel := context.WithTimeout(ctx, mapCatalogCommandTimeout)
		command := exec.CommandContext(
			commandContext,
			"wine",
			repakPath,
			"get",
			pakPath,
			assetPath,
		)
		command.Env = mapCatalogRepakEnvironment()
		var stderr limitedCommandOutput
		stderr.limit = 64 << 10
		command.Stdout = &collector
		command.Stderr = &stderr
		err := command.Run()
		timedOut := errors.Is(commandContext.Err(), context.DeadlineExceeded)
		cancel()
		if timedOut {
			return "", errors.New("repak map inspection timed out")
		}
		if collector.full {
			return "", errMapAssetReadLimit
		}
		if err != nil {
			message := strings.TrimSpace(stderr.buffer.String())
			if message == "" {
				message = err.Error()
			}
			return "", fmt.Errorf("repak map inspection: %s", message)
		}
	}
	return collector.defaultGameMode(), nil
}

func declaredModMaps(description string) map[string]struct{} {
	lines := strings.Split(strings.ReplaceAll(description, "\r\n", "\n"), "\n")
	result := make(map[string]struct{})
	reading := false
	emptyLines := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if !reading {
			if strings.Contains(lower, "maps list") ||
				strings.Contains(lower, "map list") {
				reading = true
			}
			continue
		}
		if line == "" {
			emptyLines++
			if len(result) > 0 && emptyLines >= 2 {
				break
			}
			continue
		}
		emptyLines = 0
		line = strings.Trim(line, "`*•- \t")
		if !validMordhauObjectName(line) {
			if len(result) > 0 {
				break
			}
			continue
		}
		result[strings.ToLower(line)] = struct{}{}
	}
	return result
}

func mapCatalogModMetadata(directory string, id int) (string, map[string]struct{}) {
	source := "mod.io " + strconv.Itoa(id)
	var metadata struct {
		Name                 string `json:"name"`
		DescriptionPlaintext string `json:"description_plaintext"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "modio.json"))
	if err == nil && json.Unmarshal(data, &metadata) == nil {
		if name := strings.TrimSpace(metadata.Name); name != "" {
			source = name + " · mod.io " + strconv.Itoa(id)
		}
	}
	return source, declaredModMaps(metadata.DescriptionPlaintext)
}

func activeMapCatalogModIDs() ([]int, error) {
	data, err := os.ReadFile(configPath("Game.ini", false))
	if err != nil {
		return nil, err
	}
	store, err := loadDisabledINIFile(false)
	if err != nil {
		return nil, err
	}
	configured, _ := configuredModsFromState(data, store)
	ids := make([]int, 0, len(configured))
	for _, mod := range configured {
		if mod.Enabled {
			ids = append(ids, mod.ID)
		}
	}
	sort.Ints(ids)
	return ids, nil
}

func mapCatalogPaks() ([]mapCatalogPak, string, error) {
	var paks []mapCatalogPak
	official, err := filepath.Glob(
		rootDir + "/Mordhau/Content/Paks/*WindowsServer.pak",
	)
	if err != nil {
		return nil, "", err
	}
	sort.Strings(official)
	for _, path := range official {
		paks = append(paks, mapCatalogPak{
			Path:     path,
			Source:   "MORDHAU",
			Official: true,
		})
	}

	ids, err := activeMapCatalogModIDs()
	if err != nil {
		return nil, "", err
	}
	for _, id := range ids {
		directory := filepath.Join(
			rootDir,
			"Mordhau",
			"Content",
			".modio",
			"mods",
			strconv.Itoa(id),
		)
		paths, globErr := filepath.Glob(filepath.Join(directory, "*WindowsServer.pak"))
		if globErr != nil {
			return nil, "", globErr
		}
		sort.Strings(paths)
		source, declared := mapCatalogModMetadata(directory, id)
		for _, path := range paths {
			paks = append(paks, mapCatalogPak{
				Path:         path,
				Source:       source,
				DeclaredMaps: declared,
			})
		}
	}

	customPaths, err := filepath.Glob(rootDir + "/Mordhau/Content/CustomPaks/*.pak")
	if err != nil {
		return nil, "", err
	}
	sort.Strings(customPaths)
	for _, path := range customPaths {
		paks = append(paks, mapCatalogPak{
			Path:   path,
			Source: "CustomPak · " + filepath.Base(path),
		})
	}

	hash := sha256.New()
	_, _ = hash.Write([]byte("map-catalog-v2\n"))
	for _, pak := range paks {
		info, statErr := os.Stat(pak.Path)
		if statErr != nil {
			return nil, "", statErr
		}
		_, _ = fmt.Fprintf(
			hash,
			"%s\x00%d\x00%d\x00%s\n",
			pak.Path,
			info.Size(),
			info.ModTime().UnixNano(),
			pak.Source,
		)
		if len(pak.DeclaredMaps) > 0 {
			declared := make([]string, 0, len(pak.DeclaredMaps))
			for name := range pak.DeclaredMaps {
				declared = append(declared, name)
			}
			sort.Strings(declared)
			for _, name := range declared {
				_, _ = fmt.Fprintf(hash, "declared\x00%s\n", name)
			}
		}
	}
	return paks, hex.EncodeToString(hash.Sum(nil)), nil
}

func officialModeForMap(name string) (officialMapMode, bool) {
	upper := strings.ToUpper(name)
	for _, mode := range officialMapModes {
		if strings.HasPrefix(upper, mode.Prefix) {
			return mode, true
		}
	}
	return officialMapMode{}, false
}

func officialModeForClass(className string) (officialMapMode, bool) {
	for _, mode := range officialMapModes {
		if strings.EqualFold(mode.Class, className) {
			return mode, true
		}
	}
	return officialMapMode{}, false
}

func mapCatalogGameModeIsPlayable(className string) bool {
	switch strings.ToLower(className) {
	case "bp_modinitializationgamemode_c",
		"bp_mordhaumainmenugamemode_c",
		"bp_mordhaugamemode_c":
		return false
	default:
		return true
	}
}

func validMapCatalogAssetPath(value string) bool {
	if value == "" || len(value) > 1024 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("_-./", character) {
			continue
		}
		return false
	}
	return true
}

func validMapCatalogPackage(value string) bool {
	return strings.HasSuffix(value, ".umap") &&
		validMapCatalogAssetPath(value)
}

func humanizeGameModeClass(className string) string {
	value := strings.TrimSuffix(strings.TrimPrefix(className, "BP_"), "_C")
	value = strings.TrimSuffix(value, "GameMode")
	var output []rune
	var previous rune
	for index, character := range []rune(value) {
		if character == '_' || character == '-' {
			if len(output) > 0 && output[len(output)-1] != ' ' {
				output = append(output, ' ')
			}
			previous = character
			continue
		}
		if index > 0 && unicode.IsUpper(character) &&
			(unicode.IsLower(previous) || unicode.IsDigit(previous)) &&
			len(output) > 0 && output[len(output)-1] != ' ' {
			output = append(output, ' ')
		}
		output = append(output, character)
		previous = character
	}
	name := strings.Join(strings.Fields(string(output)), " ")
	if name == "" {
		return className
	}
	return name
}

func mapModeID(className string) string {
	sum := sha256.Sum256([]byte(className))
	return "mode-" + hex.EncodeToString(sum[:8])
}

func appendMapCatalogEntry(
	modes map[string]*MapCatalogGameMode,
	modeOrder *[]string,
	modeName string,
	modeClass string,
	entry MapCatalogMap,
) {
	if !validMordhauObjectName(entry.Name) ||
		!validMordhauObjectName(modeClass) ||
		!mapCatalogGameModeIsPlayable(modeClass) ||
		entry.Source == "" {
		return
	}
	key := strings.ToLower(modeClass)
	mode := modes[key]
	if mode == nil {
		if modeName == "" {
			modeName = humanizeGameModeClass(modeClass)
		}
		mode = &MapCatalogGameMode{
			ID:      mapModeID(modeClass),
			Name:    modeName,
			Class:   modeClass,
			Sources: []string{},
			Maps:    []MapCatalogMap{},
		}
		modes[key] = mode
		*modeOrder = append(*modeOrder, key)
	}
	for _, existing := range mode.Maps {
		if strings.EqualFold(existing.Name, entry.Name) {
			return
		}
	}
	sourceKnown := false
	for _, source := range mode.Sources {
		if source == entry.Source {
			sourceKnown = true
			break
		}
	}
	if !sourceKnown {
		mode.Sources = append(mode.Sources, entry.Source)
	}
	mode.Maps = append(mode.Maps, entry)
}

func recordMapCatalogOwner(
	owners map[string]mapCatalogMapOwner,
	ambiguous map[string]string,
	gameModeClass string,
	entry MapCatalogMap,
) bool {
	nameKey := strings.ToLower(entry.Name)
	owner := mapCatalogMapOwner{
		Package:       strings.ToLower(entry.Package),
		GameModeClass: strings.ToLower(gameModeClass),
	}
	if existing, exists := owners[nameKey]; exists {
		if existing != owner {
			ambiguous[nameKey] = entry.Name
			return false
		}
		return true
	}
	owners[nameKey] = owner
	return true
}

func buildMapCatalog(ctx context.Context, repakPath string) (MapCatalogView, string, error) {
	buildContext, cancel := context.WithTimeout(ctx, mapCatalogBuildTimeout)
	defer cancel()
	ctx = buildContext

	if repakPath == "" {
		repakPath = defaultRepakPath
	}
	info, err := os.Stat(repakPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
		return MapCatalogView{}, "", fmt.Errorf(
			"%w: repak helper is not installed",
			errMapCatalogUnavailable,
		)
	}
	paks, fingerprint, err := mapCatalogPaks()
	if err != nil {
		return MapCatalogView{}, "", fmt.Errorf("%w: %v", errMapCatalogUnavailable, err)
	}
	modes := make(map[string]*MapCatalogGameMode)
	modeOrder := make([]string, 0)
	warnings := make([]string, 0)
	mapOwners := make(map[string]mapCatalogMapOwner)
	ambiguousMaps := make(map[string]string)
	mapCount := 0

	for _, pak := range paks {
		listing, listErr := runRepakText(ctx, repakPath, "list", pak.Path)
		if listErr != nil {
			warnings = append(
				warnings,
				filepath.Base(pak.Path)+": "+listErr.Error(),
			)
			continue
		}
		entries := strings.Split(strings.ReplaceAll(listing, "\r\n", "\n"), "\n")
		entrySet := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			if validMapCatalogAssetPath(entry) {
				entrySet[entry] = struct{}{}
			}
		}
		for _, entry := range entries {
			if mapCount >= mapCatalogMaximumMaps {
				return MapCatalogView{}, "", fmt.Errorf(
					"%w: installed content exposes too many maps",
					errMapCatalogUnavailable,
				)
			}
			entry = strings.TrimSpace(entry)
			if !validMapCatalogPackage(entry) {
				continue
			}
			name := strings.TrimSuffix(filepath.Base(entry), ".umap")
			if pak.Official {
				if !strings.HasPrefix(
					entry,
					"Mordhau/Content/Mordhau/Maps/",
				) {
					continue
				}
				mode, namedByPrefix := officialModeForMap(name)
				if !namedByPrefix {
					assets := []string{entry}
					uexp := strings.TrimSuffix(entry, ".umap") + ".uexp"
					if _, exists := entrySet[uexp]; exists {
						assets = append(assets, uexp)
					}
					gameMode, inspectErr := runRepakAssetStrings(
						ctx,
						repakPath,
						pak.Path,
						assets,
					)
					if inspectErr != nil {
						warnings = append(
							warnings,
							filepath.Base(pak.Path)+" · "+name+": "+
								inspectErr.Error(),
						)
						continue
					}
					if gameMode == "" {
						continue
					}
					mode = officialMapMode{
						Name:  humanizeGameModeClass(gameMode),
						Class: gameMode,
					}
					if knownMode, known := officialModeForClass(gameMode); known {
						mode.Name = knownMode.Name
					}
				}
				mapEntry := MapCatalogMap{
					Name:    name,
					Package: entry,
					Source:  pak.Source,
				}
				if recordMapCatalogOwner(
					mapOwners,
					ambiguousMaps,
					mode.Class,
					mapEntry,
				) {
					appendMapCatalogEntry(
						modes,
						&modeOrder,
						mode.Name,
						mode.Class,
						mapEntry,
					)
				}
				mapCount++
				continue
			}
			if len(pak.DeclaredMaps) > 0 {
				if _, declared := pak.DeclaredMaps[strings.ToLower(name)]; !declared {
					continue
				}
			}
			assets := []string{entry}
			uexp := strings.TrimSuffix(entry, ".umap") + ".uexp"
			if _, exists := entrySet[uexp]; exists {
				assets = append(assets, uexp)
			}
			gameMode, inspectErr := runRepakAssetStrings(
				ctx,
				repakPath,
				pak.Path,
				assets,
			)
			if inspectErr != nil {
				warnings = append(
					warnings,
					filepath.Base(pak.Path)+" · "+name+": "+inspectErr.Error(),
				)
				continue
			}
			if gameMode == "" {
				if len(pak.DeclaredMaps) > 0 {
					warnings = append(
						warnings,
						filepath.Base(pak.Path)+" · "+name+
							": packaged default game mode is ambiguous or unavailable",
					)
				}
				continue
			}
			mapEntry := MapCatalogMap{
				Name:    name,
				Package: entry,
				Source:  pak.Source,
			}
			if recordMapCatalogOwner(
				mapOwners,
				ambiguousMaps,
				gameMode,
				mapEntry,
			) {
				appendMapCatalogEntry(
					modes,
					&modeOrder,
					"",
					gameMode,
					mapEntry,
				)
			}
			mapCount++
		}
	}

	ambiguousNames := make([]string, 0, len(ambiguousMaps))
	for _, name := range ambiguousMaps {
		ambiguousNames = append(ambiguousNames, name)
	}
	sort.Slice(ambiguousNames, func(left, right int) bool {
		return strings.ToLower(ambiguousNames[left]) <
			strings.ToLower(ambiguousNames[right])
	})
	for _, name := range ambiguousNames {
		warnings = append(
			warnings,
			name+": duplicate installed map name is ambiguous and was omitted",
		)
	}

	view := MapCatalogView{
		GameModes:   make([]MapCatalogGameMode, 0, len(modeOrder)),
		Warnings:    warnings,
		GeneratedAt: time.Now(),
	}
	for _, key := range modeOrder {
		mode := modes[key]
		filtered := mode.Maps[:0]
		for _, entry := range mode.Maps {
			if _, ambiguous := ambiguousMaps[strings.ToLower(entry.Name)]; !ambiguous {
				filtered = append(filtered, entry)
			}
		}
		mode.Maps = filtered
		if len(mode.Maps) == 0 {
			continue
		}
		sort.Strings(mode.Sources)
		sort.Slice(mode.Maps, func(left, right int) bool {
			return strings.ToLower(mode.Maps[left].Name) <
				strings.ToLower(mode.Maps[right].Name)
		})
		view.MapCount += len(mode.Maps)
		view.GameModes = append(view.GameModes, *mode)
	}
	sort.SliceStable(view.GameModes, func(left, right int) bool {
		leftOfficial := false
		rightOfficial := false
		for _, source := range view.GameModes[left].Sources {
			leftOfficial = leftOfficial || source == "MORDHAU"
		}
		for _, source := range view.GameModes[right].Sources {
			rightOfficial = rightOfficial || source == "MORDHAU"
		}
		if leftOfficial != rightOfficial {
			return leftOfficial
		}
		return strings.ToLower(view.GameModes[left].Name) <
			strings.ToLower(view.GameModes[right].Name)
	})
	return view, fingerprint, nil
}

func (m *Manager) mapCatalog(ctx context.Context) (MapCatalogView, error) {
	if m.mapCatalogViewBuild != nil {
		return m.mapCatalogViewBuild(ctx)
	}
	m.mapCatalogMu.Lock()
	defer m.mapCatalogMu.Unlock()

	_, fingerprint, err := mapCatalogPaks()
	if err == nil &&
		m.mapCatalogCache.Fingerprint == fingerprint &&
		len(m.mapCatalogCache.View.GameModes) > 0 {
		return m.mapCatalogCache.View, nil
	}
	repakPath := m.mapCatalogRepakPath
	if repakPath == "" {
		repakPath = defaultRepakPath
	}
	view, fingerprint, err := buildMapCatalog(ctx, repakPath)
	if err != nil {
		return MapCatalogView{}, err
	}
	m.mapCatalogCache = mapCatalogCache{
		Fingerprint: fingerprint,
		View:        view,
	}
	return view, nil
}

func (m *Manager) mapGameServerRunning() bool {
	if m.mapServerProcess != nil {
		_, running := m.mapServerProcess()
		return running
	}
	return serverRunning()
}

func findMapSelection(
	view MapCatalogView,
	modeID string,
	mapName string,
) (MapCatalogGameMode, MapCatalogMap, bool) {
	for _, mode := range view.GameModes {
		if mode.ID != modeID {
			continue
		}
		for _, entry := range mode.Maps {
			if entry.Name == mapName {
				return mode, entry, true
			}
		}
	}
	return MapCatalogGameMode{}, MapCatalogMap{}, false
}
