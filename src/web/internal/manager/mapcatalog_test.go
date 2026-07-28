package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testMapCatalogView() MapCatalogView {
	mode := MapCatalogGameMode{
		ID:      mapModeID("BP_AeternisDreadV2GameMode_C"),
		Name:    "Aeternis Dread V2",
		Class:   "BP_AeternisDreadV2GameMode_C",
		Sources: []string{"Aeternis Dread 2 · mod.io 1935867"},
		Maps: []MapCatalogMap{{
			Name:    "DR_Catacombs",
			Package: "AeternisDR2/Content/Maps/DR_Catacombs.umap",
			Source:  "Aeternis Dread 2 · mod.io 1935867",
		}},
	}
	return MapCatalogView{
		GameModes: []MapCatalogGameMode{mode},
		MapCount:  1,
	}
}

func TestMapAssetStringCollectorFindsPackagedDefaultGameMode(t *testing.T) {
	var collector mapAssetStringCollector
	_, _ = collector.Write([]byte(
		"DefaultGameMode\x00" +
			"/AeternisDR2/GameModes/BP_AeternisDreadV2GameMode_C\x00",
	))
	if got := collector.defaultGameMode(); got != "BP_AeternisDreadV2GameMode_C" {
		t.Fatalf("default game mode = %q", got)
	}
}

func TestMapAssetStringCollectorRejectsAmbiguousGameModes(t *testing.T) {
	var collector mapAssetStringCollector
	_, _ = collector.Write([]byte(
		"DefaultGameMode\x00BP_FirstGameMode_C\x00BP_SecondGameMode_C\x00",
	))
	if got := collector.defaultGameMode(); got != "" {
		t.Fatalf("ambiguous default game mode = %q", got)
	}
}

func TestOfficialUnprefixedMapCanUsePackagedGameMode(t *testing.T) {
	if _, prefixed := officialModeForMap("LiteMordhauTestLevel"); prefixed {
		t.Fatal("unprefixed map was incorrectly classified by its name")
	}
	mode, known := officialModeForClass("BP_DeathmatchGameMode_C")
	if !known ||
		mode.Name != "Deathmatch" ||
		mode.Class != "BP_DeathmatchGameMode_C" {
		t.Fatalf("packaged Deathmatch mode = %+v, known %t", mode, known)
	}
}

func TestMapCatalogRejectsInternalTravelGameModes(t *testing.T) {
	for _, className := range []string{
		"BP_ModInitializationGameMode_C",
		"BP_MordhauMainMenuGameMode_C",
		"BP_MordhauGameMode_C",
	} {
		if mapCatalogGameModeIsPlayable(className) {
			t.Fatalf("internal game mode is playable: %s", className)
		}
	}
	if !mapCatalogGameModeIsPlayable("BP_DeathmatchGameMode_C") {
		t.Fatal("Deathmatch game mode was rejected")
	}
}

func TestMapAssetStringCollectorEnforcesReadLimit(t *testing.T) {
	collector := mapAssetStringCollector{
		bytesRead: mapCatalogAssetReadLimit - 2,
	}
	written, err := collector.Write([]byte("abc"))
	if written != 2 || !errors.Is(err, errMapAssetReadLimit) || !collector.full {
		t.Fatalf(
			"limited asset write = %d, err %v, full %t",
			written,
			err,
			collector.full,
		)
	}
}

func TestDeclaredModMapsOnlyReadsTheMapsList(t *testing.T) {
	description := "Overview\n\nMaps List:\n- DR_Catacombs\n- DR_Crypt\n\n\nOther settings\n"
	maps := declaredModMaps(description)
	if len(maps) != 2 {
		t.Fatalf("declared maps = %#v", maps)
	}
	for _, name := range []string{"dr_catacombs", "dr_crypt"} {
		if _, exists := maps[name]; !exists {
			t.Fatalf("declared maps are missing %q", name)
		}
	}
	if _, exists := maps["other_settings"]; exists {
		t.Fatal("text after the maps list was parsed as a map")
	}
}

func TestMapCatalogSelectionRequiresAModeAndMapPair(t *testing.T) {
	view := testMapCatalogView()
	mode, entry, ok := findMapSelection(
		view,
		view.GameModes[0].ID,
		"DR_Catacombs",
	)
	if !ok || mode.Class != "BP_AeternisDreadV2GameMode_C" ||
		entry.Source == "" {
		t.Fatalf("valid map selection = mode %+v, map %+v, ok %t", mode, entry, ok)
	}
	if _, _, ok := findMapSelection(
		view,
		view.GameModes[0].ID,
		"DR_NotPackaged",
	); ok {
		t.Fatal("a map outside the selected mode was accepted")
	}
}

func TestMapCatalogRejectsDuplicateNamesWithDifferentDestinations(t *testing.T) {
	owners := make(map[string]mapCatalogMapOwner)
	ambiguous := make(map[string]string)
	first := MapCatalogMap{
		Name:    "FFA_Test",
		Package: "Mordhau/Content/Maps/First/FFA_Test.umap",
	}
	if !recordMapCatalogOwner(
		owners,
		ambiguous,
		"BP_DeathmatchGameMode_C",
		first,
	) {
		t.Fatal("first map owner was rejected")
	}
	if !recordMapCatalogOwner(
		owners,
		ambiguous,
		"BP_DeathmatchGameMode_C",
		first,
	) {
		t.Fatal("an identical map owner was treated as ambiguous")
	}
	second := first
	second.Package = "Mordhau/Mods/Other/Maps/FFA_Test.umap"
	if recordMapCatalogOwner(
		owners,
		ambiguous,
		"BP_CustomGameMode_C",
		second,
	) {
		t.Fatal("a duplicate map name with another destination was accepted")
	}
	if ambiguous["ffa_test"] != "FFA_Test" {
		t.Fatalf("ambiguous maps = %#v", ambiguous)
	}
}

func TestGameLogProcessorTracksCurrentMapAndGameMode(t *testing.T) {
	processor := newGameLogProcessor()
	for _, line := range []string{
		`[2026.07.28-16.00.00:001][100]LogLoad: LoadMap: /AeternisDR2/Maps/DR_Catacombs?game=BP_AeternisDreadV2GameMode_C`,
		`[2026.07.28-16.00.01:002][101]LogLoad: Game class is 'BP_AeternisDreadV2GameMode_C'`,
	} {
		if events := processor.processLine(line); len(events) != 0 {
			t.Fatalf("load context unexpectedly emitted server events: %#v", events)
		}
	}
	mapName, gameMode := processor.gameContext()
	if mapName != "DR_Catacombs" ||
		gameMode != "BP_AeternisDreadV2GameMode_C" {
		t.Fatalf("game context = map %q, mode %q", mapName, gameMode)
	}
	processor.reset()
	mapName, gameMode = processor.gameContext()
	if mapName != "" || gameMode != "" {
		t.Fatalf("reset game context = map %q, mode %q", mapName, gameMode)
	}
}

func TestMapChangeHandlerUsesOnlyTheCataloguedMapName(t *testing.T) {
	directory := t.TempDir()
	view := testMapCatalogView()
	var command string
	manager := &Manager{
		rconLogPath: filepath.Join(directory, "rcon.log"),
		auditPath:   filepath.Join(directory, "audit.log"),
		mapCatalogViewBuild: func(context.Context) (MapCatalogView, error) {
			return view, nil
		},
		mapServerProcess: func() (int, bool) {
			return 321, true
		},
		rconCommandExecute: func(value string) (rconCommandResult, error) {
			command = value
			return rconCommandResult{Lines: []string{"Travel accepted"}}, nil
		},
	}
	session := Session{Username: "operator", CSRF: "csrf-token"}
	body, err := json.Marshal(mapChangeRequest{
		ModeID: view.GameModes[0].ID,
		Map:    "DR_Catacombs",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://manager.example/api/maps/change",
		strings.NewReader(string(body)),
	)
	request.Header.Set("X-CSRF-Token", session.CSRF)
	response := httptest.NewRecorder()

	manager.mapChangeHandler(response, request, session)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if command != "changelevel DR_Catacombs" {
		t.Fatalf("RCON command = %q", command)
	}
	audit, err := os.ReadFile(manager.auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"event":"map_change_executed"`,
		`"account":"operator"`,
		`"map":"DR_Catacombs"`,
		`"game_mode":"BP_AeternisDreadV2GameMode_C"`,
	} {
		if !strings.Contains(string(audit), expected) {
			t.Fatalf("map-change audit is missing %q: %s", expected, audit)
		}
	}
}

func TestLiveInstalledMapCatalog(t *testing.T) {
	if os.Getenv("MORDHAU_LIVE_MAP_CATALOG") != "1" {
		t.Skip("set MORDHAU_LIVE_MAP_CATALOG=1 for an installed-content check")
	}
	view, _, err := buildMapCatalog(context.Background(), defaultRepakPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.GameModes) == 0 || view.MapCount == 0 {
		t.Fatalf("installed map catalog is empty: %+v", view)
	}
	foundOfficial := false
	foundLiteMordhauTestLevel := false
	for _, mode := range view.GameModes {
		if !mapCatalogGameModeIsPlayable(mode.Class) {
			t.Fatalf("catalog contains internal game mode %q", mode.Class)
		}
		for _, source := range mode.Sources {
			foundOfficial = foundOfficial || source == "MORDHAU"
		}
		if mode.Class == "BP_DeathmatchGameMode_C" {
			for _, entry := range mode.Maps {
				if entry.Name == "LiteMordhauTestLevel" &&
					entry.Source == "MORDHAU" {
					foundLiteMordhauTestLevel = true
				}
			}
		}
	}
	if !foundOfficial {
		t.Fatal("installed map catalog has no verified official maps")
	}
	if !foundLiteMordhauTestLevel {
		t.Fatal("Deathmatch catalog is missing LiteMordhauTestLevel")
	}
	data, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("installed map catalog:\n%s", data)
}
