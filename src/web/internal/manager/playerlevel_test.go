package manager

import "testing"

func TestRuntimePlayerLevelUsesInventoryProgressOnly(t *testing.T) {
	view := RuntimeTargetView{
		Target: RuntimeTarget{Kind: "player_controller"},
		AccountProgress: &RuntimeAccountProgress{
			XP:    90435,
			Level: 38,
		},
	}
	level, ok := runtimePlayerLevel(view)
	if !ok || level != 38 {
		t.Fatalf("account level = %d, ok %t", level, ok)
	}

	replicatedRank := "123"
	for _, invalid := range []RuntimeTargetView{
		{
			Target: RuntimeTarget{Kind: "player_state"},
			Properties: []RuntimeProperty{{
				Name: "ReplicatedRank", Type: "Int16Property",
				Value: &replicatedRank,
			}},
		},
		{
			Target: RuntimeTarget{Kind: "player_controller"},
			Properties: []RuntimeProperty{{
				Name: "DuelRank", Type: "Int16Property",
				Value: &replicatedRank,
			}},
		},
		{
			Target: RuntimeTarget{Kind: "player_controller"},
			AccountProgress: &RuntimeAccountProgress{
				XP: 90435, Level: 0,
			},
		},
		{
			Target: RuntimeTarget{Kind: "player_controller"},
			AccountProgress: &RuntimeAccountProgress{
				XP: -1, Level: 38,
			},
		},
	} {
		if level, ok := runtimePlayerLevel(invalid); ok {
			t.Fatalf("invalid account progress was accepted as level %d: %+v", level, invalid)
		}
	}
}

func TestRuntimePlayerLevelTargetsUseIdentifiedController(t *testing.T) {
	manager := &Manager{
		runtimeSummary: RuntimeBridgeSummary{Ready: true},
		runtimeTargets: []RuntimeTarget{
			{
				ID:         "player_controller:10:20",
				Kind:       "player_controller",
				PlayerSlot: 3,
				PlayFabID:  testPlayerID,
			},
			{
				ID:         "player_state:11:21",
				Kind:       "player_state",
				PlayerSlot: 3,
			},
			{
				ID:         "player_controller:12:22",
				Kind:       "player_controller",
				PlayerSlot: 4,
			},
		},
	}
	targets := manager.runtimePlayerLevelTargets()
	if len(targets) != 1 ||
		targets[0].PlayFabID != testPlayerID ||
		targets[0].PlayerControllerID != "player_controller:10:20" ||
		targets[0].PlayerSlot != 3 {
		t.Fatalf("player level targets = %+v", targets)
	}
}

func TestPlayerLevelObservationPersistsLatestValidValue(t *testing.T) {
	history := playerHistoryFile{Version: playerHistoryVersion}
	if !applyPlayerLevelObservations(&history, []playerLevelObservation{{
		PlayFabID: testPlayerID,
		Level:     84,
	}}) {
		t.Fatal("first level observation did not change history")
	}
	if len(history.Players) != 1 ||
		history.Players[0].LastLevel == nil ||
		*history.Players[0].LastLevel != 84 {
		t.Fatalf("stored player level = %+v", history.Players)
	}
	if applyPlayerLevelObservations(&history, []playerLevelObservation{{
		PlayFabID: testPlayerID,
		Level:     84,
	}}) {
		t.Fatal("an unchanged level advanced history")
	}
	if !applyPlayerLevelObservations(&history, []playerLevelObservation{{
		PlayFabID: testPlayerID,
		Level:     85,
	}}) || *history.Players[0].LastLevel != 85 {
		t.Fatalf("latest player level was not retained: %+v", history.Players[0])
	}
	for _, invalid := range []int{0, -1} {
		if applyPlayerLevelObservations(&history, []playerLevelObservation{{
			PlayFabID: testPlayerID,
			Level:     invalid,
		}}) {
			t.Fatalf("invalid level %d changed history", invalid)
		}
	}
}

func TestLegacyZeroLevelBecomesUnobserved(t *testing.T) {
	zero := 0
	valid := 38
	history := playerHistoryFile{
		Version:  playerHistoryVersion,
		Revision: 7,
		Players: []playerRecord{
			{PlayFabID: testPlayerID, LastLevel: &zero},
			{PlayFabID: "ABCDEF0123456789", LastLevel: &valid},
		},
	}
	if !normalizeLegacyPlayerProgress(&history) {
		t.Fatal("legacy zero level was not normalized")
	}
	if history.Revision != 8 ||
		history.Players[0].LastLevel != nil ||
		history.Players[1].LastLevel == nil ||
		*history.Players[1].LastLevel != 38 {
		t.Fatalf("normalized history = %+v", history)
	}
	if err := validatePlayerHistory(&history); err != nil {
		t.Fatal(err)
	}
}

func TestSteamPlatformObservationPersistsVerifiedID64(t *testing.T) {
	history := playerHistoryFile{Version: playerHistoryVersion}
	observation := playerPlatformObservation{
		PlayFabID:         testPlayerID,
		Platform:          "Steam",
		PlatformAccountID: testSteamID64,
	}
	if !applyPlayerPlatformObservations(
		&history,
		[]playerPlatformObservation{observation},
	) {
		t.Fatal("Steam identity observation did not change history")
	}
	player := history.Players[0]
	if player.Platform != "Steam" ||
		player.PlatformAccountID != testSteamID64 {
		t.Fatalf("stored Steam identity = %+v", player)
	}
	if applyPlayerPlatformObservations(
		&history,
		[]playerPlatformObservation{observation},
	) {
		t.Fatal("unchanged Steam identity advanced history")
	}
	for _, invalid := range []playerPlatformObservation{
		{PlayFabID: testPlayerID, Platform: "Steam", PlatformAccountID: "123"},
		{PlayFabID: testPlayerID, Platform: "Other", PlatformAccountID: testSteamID64},
	} {
		if applyPlayerPlatformObservations(
			&history,
			[]playerPlatformObservation{invalid},
		) {
			t.Fatalf("invalid platform identity changed history: %+v", invalid)
		}
	}
}
