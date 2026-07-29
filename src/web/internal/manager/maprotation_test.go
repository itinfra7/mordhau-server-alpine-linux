package manager

import (
	"strings"
	"testing"
)

func TestReplaceMapRotationPreservesUnrelatedLinesAndDisabledEntries(t *testing.T) {
	data := []byte(
		"[/Script/Mordhau.MordhauGameMode]\n" +
			"OtherSetting=keep\n" +
			"; operator comment\n" +
			"MapRotation=FFA_First\n" +
			"MapRotation=FFA_Second\n" +
			"AfterSetting=also-keep\n",
	)
	store := newDisabledINIFile()
	store.Entries = append(store.Entries, disabledINIEntry{
		ID:       "unrelated-disabled",
		File:     "Game.ini",
		Section:  mapRotationSection,
		Position: 2,
		Key:      "HiddenSetting",
		Value:    "retained",
	})
	result, resultStore, err := replaceMapRotation(
		data,
		store,
		[]MapRotationEntry{
			{Map: "FFA_Second", Enabled: false},
			{Map: "FFA_First", Enabled: true},
			{Map: "LiteMordhauTestLevel", Enabled: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(result)
	for _, wanted := range []string{
		"OtherSetting=keep",
		"; operator comment",
		"AfterSetting=also-keep",
		"MapRotation=FFA_First",
		"MapRotation=LiteMordhauTestLevel",
	} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("result is missing %q:\n%s", wanted, text)
		}
	}
	if strings.Contains(text, "MapRotation=FFA_Second") {
		t.Fatalf("disabled rotation leaked into active INI:\n%s", text)
	}
	view := makeConfigViewWithDisabled("Game.ini", result, true, resultStore)
	_, rotations, found := mapRotationConfigEntries(view)
	if !found || len(rotations) != 3 {
		t.Fatalf("rotation view = found %t, entries %+v", found, rotations)
	}
	for index, wanted := range []struct {
		name    string
		enabled bool
	}{
		{"FFA_Second", false},
		{"FFA_First", true},
		{"LiteMordhauTestLevel", true},
	} {
		if rotations[index].Value != wanted.name ||
			rotations[index].Enabled != wanted.enabled {
			t.Fatalf("rotation %d = %+v, want %+v", index, rotations[index], wanted)
		}
	}
	foundUnrelated := false
	for _, entry := range resultStore.Entries {
		if entry.ID == "unrelated-disabled" &&
			entry.Key == "HiddenSetting" &&
			entry.Value == "retained" {
			foundUnrelated = true
		}
	}
	if !foundUnrelated {
		t.Fatalf("unrelated disabled entry was lost: %+v", resultStore.Entries)
	}
}

func TestReplaceMapRotationKeepsWholeDisabledSectionDisabled(t *testing.T) {
	data := []byte("[/Script/Mordhau.MordhauGameMode]\n")
	store := newDisabledINIFile()
	store.Sections = append(store.Sections, disabledINISection{
		ID:       "disabled-section",
		File:     "Game.ini",
		Name:     mapRotationSection,
		Position: 0,
	})
	store.Entries = append(store.Entries,
		disabledINIEntry{
			ID:       "first",
			File:     "Game.ini",
			Section:  mapRotationSection,
			Position: 0,
			Key:      mapRotationKey,
			Value:    "FFA_First",
		},
		disabledINIEntry{
			ID:       "second",
			File:     "Game.ini",
			Section:  mapRotationSection,
			Position: 1,
			Key:      mapRotationKey,
			Value:    "FFA_Second",
		},
	)
	result, resultStore, err := replaceMapRotation(
		data,
		store,
		[]MapRotationEntry{
			{ID: "second", Map: "FFA_Second", Enabled: true},
			{ID: "first", Map: "FFA_First", Enabled: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "MapRotation=") {
		t.Fatalf("disabled section gained active entries:\n%s", result)
	}
	view := makeConfigViewWithDisabled("Game.ini", result, true, resultStore)
	section, rotations, found := mapRotationConfigEntries(view)
	if !found || section.Enabled || len(rotations) != 2 {
		t.Fatalf("disabled section view = section %+v, entries %+v", section, rotations)
	}
	if rotations[0].Value != "FFA_Second" ||
		rotations[1].Value != "FFA_First" ||
		rotations[0].Enabled ||
		rotations[1].Enabled {
		t.Fatalf("disabled rotation order/state was not preserved: %+v", rotations)
	}
}

func TestMapRotationCatalogKeepsMapsUsedByMultipleModes(t *testing.T) {
	catalog := MapCatalogView{
		GameModes: []MapCatalogGameMode{
			{
				ID:   "ffa",
				Name: "Free for all",
				Maps: []MapCatalogMap{{Name: "LiteMordhauTestLevel", Source: "base"}},
			},
			{
				ID:   "tdm",
				Name: "Team deathmatch",
				Maps: []MapCatalogMap{{Name: "LiteMordhauTestLevel", Source: "base"}},
			},
		},
	}
	index := mapRotationCatalogIndex(catalog)
	item, found := index["litemordhautestlevel"]
	if !found || item.ModeName != "Multiple game modes" {
		t.Fatalf("multi-mode map was lost from the rotation catalog: %+v", index)
	}
}
