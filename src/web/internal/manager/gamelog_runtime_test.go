package manager

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func authenticatedGameLogSession(
	t *testing.T,
	processor *gameLogProcessor,
	playerID string,
	name string,
	address string,
	joinedAt time.Time,
) gameLogEvent {
	t.Helper()
	lines := testPlayerLogLines(
		playerID,
		name,
		address,
		joinedAt,
		time.Minute,
	)
	var events []gameLogEvent
	for _, line := range lines[:3] {
		events = append(events, processor.processLine(line)...)
	}
	if len(events) != 1 || events[0].PlayerAction != "login" {
		t.Fatalf("authenticated session events = %#v", events)
	}
	return events[0]
}

func TestGameLogProcessorReconcilesMissingRuntimePlayerLogout(t *testing.T) {
	start := time.Date(2026, 8, 3, 13, 49, 50, 0, time.Local)
	processor := newGameLogProcessor()
	login := authenticatedGameLogSession(
		t,
		processor,
		testPlayerID,
		"Unicode 플레이어",
		"203.0.113.45",
		start,
	)

	present := map[string]struct{}{testPlayerID: {}}
	if events := processor.reconcileRuntimePlayers(
		true,
		1,
		present,
		true,
		start.Add(2*time.Second),
	); len(events) != 0 {
		t.Fatalf("present runtime player was logged out: %#v", events)
	}

	missingAt := start.Add(10 * time.Second)
	if events := processor.reconcileRuntimePlayers(
		true,
		0,
		nil,
		true,
		missingAt,
	); len(events) != 0 {
		t.Fatalf("runtime grace did not start cleanly: %#v", events)
	}
	waiting := `[2026.08.03-13.50.00:000][10]LogGameMode: Display: ` +
		`Match State Changed from LeavingMap to WaitingToStart`
	if events := processor.processLine(waiting); len(events) != 0 {
		t.Fatalf("empty runtime state did not suppress waiting event: %#v", events)
	}
	if events := processor.reconcileRuntimePlayers(
		true,
		0,
		nil,
		true,
		missingAt.Add(runtimePlayerMissingGrace-time.Millisecond),
	); len(events) != 0 {
		t.Fatalf("player was logged out before grace elapsed: %#v", events)
	}

	events := processor.reconcileRuntimePlayers(
		true,
		0,
		nil,
		true,
		missingAt.Add(runtimePlayerMissingGrace),
	)
	if len(events) != 1 {
		t.Fatalf("inferred logout events = %#v", events)
	}
	logout := events[0]
	if logout.Kind != "login" ||
		logout.PlayerAction != "logout" ||
		logout.PlayerID != testPlayerID ||
		logout.PlayerName != "Unicode 플레이어" ||
		logout.PlayerIP != login.PlayerIP ||
		!logout.PlayerJoinedAt.Equal(start) ||
		!logout.Inferred {
		t.Fatalf("inferred logout = %+v", logout)
	}
	if len(processor.players) != 0 {
		t.Fatalf("runtime-absent player remains active: %#v", processor.players)
	}

	lateClose := testPlayerLogLines(
		testPlayerID,
		"Unicode 플레이어",
		"203.0.113.45",
		start,
		time.Minute,
	)[4]
	if duplicate := processor.processLine(lateClose); len(duplicate) != 0 {
		t.Fatalf("late native close duplicated logout: %#v", duplicate)
	}
}

func TestGameLogProcessorRuntimeReconciliationAvoidsFalseLogout(t *testing.T) {
	start := time.Date(2026, 8, 3, 14, 0, 0, 0, time.Local)
	processor := newGameLogProcessor()
	authenticatedGameLogSession(
		t,
		processor,
		testPlayerID,
		"Loading Player",
		"203.0.113.46",
		start,
	)

	if events := processor.reconcileRuntimePlayers(
		true,
		0,
		nil,
		true,
		start.Add(time.Second),
	); len(events) != 0 {
		t.Fatalf("initial runtime sample emitted logout: %#v", events)
	}
	if events := processor.reconcileRuntimePlayers(
		true,
		0,
		nil,
		true,
		start.Add(runtimePlayerInitialGrace-time.Millisecond),
	); len(events) != 0 {
		t.Fatalf("loading player was logged out during initial grace: %#v", events)
	}
	processor.reconcileRuntimePlayers(
		false,
		0,
		nil,
		false,
		start.Add(runtimePlayerInitialGrace),
	)
	if len(processor.runtimeMissingSince) != 0 {
		t.Fatal("unavailable Runtime Bridge retained an absence timer")
	}
	if events := processor.reconcileRuntimePlayers(
		true,
		1,
		nil,
		false,
		start.Add(time.Minute),
	); len(events) != 0 || len(processor.runtimeMissingSince) != 0 {
		t.Fatalf("incomplete positive identity set inferred absence: %#v", events)
	}
}

func TestGameLogProcessorRuntimeIdentitySetClosesOnlyAbsentPlayer(t *testing.T) {
	start := time.Date(2026, 8, 3, 15, 0, 0, 0, time.Local)
	processor := newGameLogProcessor()
	authenticatedGameLogSession(
		t,
		processor,
		testPlayerID,
		"First",
		"203.0.113.47",
		start,
	)
	authenticatedGameLogSession(
		t,
		processor,
		testOtherPlayerID,
		"Second",
		"203.0.113.48",
		start.Add(time.Second),
	)
	both := map[string]struct{}{
		testPlayerID:      {},
		testOtherPlayerID: {},
	}
	processor.reconcileRuntimePlayers(
		true,
		2,
		both,
		true,
		start.Add(2*time.Second),
	)
	onlySecond := map[string]struct{}{testOtherPlayerID: {}}
	missingAt := start.Add(3 * time.Second)
	processor.reconcileRuntimePlayers(
		true,
		1,
		onlySecond,
		true,
		missingAt,
	)
	events := processor.reconcileRuntimePlayers(
		true,
		1,
		onlySecond,
		true,
		missingAt.Add(runtimePlayerMissingGrace),
	)
	if len(events) != 1 || events[0].PlayerID != testPlayerID {
		t.Fatalf("identity reconciliation events = %#v", events)
	}
	if _, active := processor.players[testOtherPlayerID]; !active {
		t.Fatal("present runtime player was removed")
	}
}

func TestGameLogProcessorChatDoesNotCreateGhostSession(t *testing.T) {
	processor := newGameLogProcessor()
	line := `[2026.08.03-15.10.00:000][10]LogGameMode: Display: (ALL) ` +
		`Chat Only, ` + testPlayerID + `: "hello"`
	events := processor.processLine(line)
	if len(events) != 1 || events[0].Kind != "chat" {
		t.Fatalf("chat events = %#v", events)
	}
	if len(processor.players) != 0 {
		t.Fatalf("chat created an authenticated session: %#v", processor.players)
	}
}

func TestManagerPersistsRuntimeInferredLogoutAndClosedSession(t *testing.T) {
	directory := t.TempDir()
	start := time.Date(2026, 8, 3, 16, 0, 0, 0, time.Local)
	processor := newGameLogProcessor()
	login := authenticatedGameLogSession(
		t,
		processor,
		testPlayerID,
		"Persistent Player",
		"203.0.113.49",
		start,
	)
	manager := &Manager{
		rconLogPath:       filepath.Join(directory, "rcon.log"),
		playerHistoryFile: filepath.Join(directory, "players.json"),
		playerHistory: playerHistoryFile{
			Version: playerHistoryVersion,
		},
		fleetSettings: fleetSettingsFile{Role: FleetRoleStandalone},
	}
	manager.recordLiveGameLogEvents([]gameLogEvent{login})

	presentAt := start.Add(2 * time.Second)
	manager.setRuntimeState(RuntimeBridgeSummary{
		Ready:                 true,
		Status:                "ready",
		PlayerControllerCount: 1,
		SampledAt:             presentAt,
	}, []RuntimeTarget{{
		Kind:      "player_controller",
		PlayFabID: testPlayerID,
	}})
	manager.reconcileRuntimeGameLogPlayers(processor, presentAt)

	missingAt := start.Add(10 * time.Second)
	manager.setRuntimeState(RuntimeBridgeSummary{
		Ready:     true,
		Status:    "ready",
		SampledAt: missingAt,
	}, nil)
	manager.reconcileRuntimeGameLogPlayers(processor, missingAt)
	closedAt := missingAt.Add(runtimePlayerMissingGrace)
	manager.setRuntimeState(RuntimeBridgeSummary{
		Ready:     true,
		Status:    "ready",
		SampledAt: closedAt,
	}, nil)
	manager.reconcileRuntimeGameLogPlayers(processor, closedAt)

	if len(manager.rconEvents) != 2 ||
		!manager.rconEvents[1].Inferred {
		t.Fatalf("persistent RCON events = %#v", manager.rconEvents)
	}
	if len(manager.playerHistory.Players) != 1 ||
		len(manager.playerHistory.Players[0].Connections) != 1 ||
		manager.playerHistory.Players[0].Connections[0].LeftAt == nil ||
		!manager.playerHistory.Players[0].Connections[0].LeftAt.Equal(closedAt) {
		t.Fatalf("closed player history = %+v", manager.playerHistory)
	}

	var persisted playerHistoryFile
	if err := readJSON(manager.playerHistoryFile, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Players[0].Connections[0].LeftAt == nil {
		t.Fatal("inferred logout was not saved to player history")
	}
	reloaded := &Manager{rconLogPath: manager.rconLogPath}
	if err := reloaded.loadRCONEventLog(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.rconEvents) != 2 ||
		!reloaded.rconEvents[1].Inferred {
		t.Fatalf("reloaded inferred metadata = %#v", reloaded.rconEvents)
	}

	restartedProcessor := newGameLogProcessor()
	authenticatedGameLogSession(
		t,
		restartedProcessor,
		testPlayerID,
		"Persistent Player",
		"203.0.113.49",
		start,
	)
	manager.discardPersistentlyClosedGameLogSessions(restartedProcessor)
	if len(restartedProcessor.players) != 0 {
		t.Fatalf("closed session was reopened after restart: %#v", restartedProcessor.players)
	}

	if info, err := os.Stat(manager.rconLogPath); err != nil ||
		info.Mode().Perm() != 0600 {
		t.Fatalf("RCON history permissions: info=%v err=%v", info, err)
	}
}

func TestRuntimeEmptyRCONHistoryCompactsStaleMatchStateTail(t *testing.T) {
	now := time.Now()
	manager := &Manager{
		runtimeSummary: RuntimeBridgeSummary{
			Ready:     true,
			Status:    "ready",
			SampledAt: now,
		},
		rconEvents: []RCONEvent{
			{Sequence: 1, Time: now, Kind: "login", Text: "Login: Player (" + testPlayerID + ") logged in"},
			{Sequence: 2, Time: now, Kind: "matchstate", Text: "MatchState: In progress"},
			{Sequence: 3, Time: now, Kind: "matchstate", Text: matchStateLeavingMapText},
			{Sequence: 4, Time: now, Kind: "matchstate", Text: matchStateWaitingToStartText},
			{Sequence: 5, Time: now, Kind: "matchstate", Text: matchStateLeavingMapText},
			{Sequence: 6, Time: now, Kind: "matchstate", Text: matchStateWaitingToStartText},
			{Sequence: 7, Time: now, Kind: "login", Text: "Login: Player (" + testPlayerID + ") logged out", Inferred: true},
			{Sequence: 8, Time: now, Kind: "fleet", Text: "(Other Server) <Guest> left the server."},
			{Sequence: 9, Time: now, Kind: "matchstate", Text: matchStateLeavingMapText},
			{Sequence: 10, Time: now, Kind: "matchstate", Text: matchStateWaitingToStartText},
		},
	}
	events := manager.rconHistory(rconBrowserHistoryLimit)
	want := []uint64{1, 2, 3, 7, 8}
	if len(events) != len(want) {
		t.Fatalf("runtime-compacted events = %#v", events)
	}
	for index, sequence := range want {
		if events[index].Sequence != sequence {
			t.Fatalf("event %d sequence = %d, want %d", index, events[index].Sequence, sequence)
		}
	}
	if len(manager.rconEvents) != 10 {
		t.Fatal("view compaction rewrote append-only RCON history")
	}

	manager.runtimeSummary.Ready = false
	if events := manager.rconHistory(rconBrowserHistoryLimit); len(events) != 10 {
		t.Fatalf("unavailable runtime compacted history: %#v", events)
	}
}
