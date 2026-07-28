package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	testPlayerID      = "FEDCBA9876543210"
	testOtherPlayerID = "0123456789ABCDEF"
	testSteamID64     = "76561197960265728"
)

func testPlayerLogLines(
	playerID string,
	name string,
	address string,
	start time.Time,
	duration time.Duration,
) []string {
	logTime := func(value time.Time) string {
		return value.Format("2006.01.02-15.04.05") + ":000"
	}
	return []string{
		"[" + logTime(start.Add(-2*time.Second)) + "][1]LogNet: NotifyAcceptedConnection: " +
			"Name: DR_Catacombs, [UNetConnection] RemoteAddr: " + address + ":45000, " +
			"Name: IpConnection_1, Driver: GameNetDriver IpNetDriver_1, IsServer: YES, " +
			"PC: NULL, Owner: NULL, UniqueId: INVALID",
		"[" + logTime(start.Add(-time.Second)) + "][2]LogNet: Login request: ?Name=" +
			name + " userId: MordhauOnlineSubsystem:" + playerID +
			" platform: MordhauOnlineSubsystem",
		"[" + logTime(start) + "][3]LogMordhauGameSession: Player authentication for " +
			name + " (" + playerID + ") completed successfully",
		"[" + logTime(start.Add(duration/2)) + "][4]LogGameMode: Display: (ALL) " +
			name + ", " + playerID + `: "hello"`,
		"[" + logTime(start.Add(duration)) + "][5]LogNet: UChannel::CleanUp: " +
			"ChIndex == 0. Closing connection. [UNetConnection] RemoteAddr: " +
			address + ":45000, Name: IpConnection_1, Driver: GameNetDriver " +
			"IpNetDriver_1, IsServer: YES, PC: PlayerController_1, " +
			"Owner: PlayerController_1, UniqueId: MordhauOnlineSubsystem:" + playerID,
	}
}

func TestGameLogProcessorCorrelatesPlayerIdentityAndCanonicalAddress(t *testing.T) {
	start := time.Date(2026, 7, 27, 12, 0, 0, 0, time.Local)
	processor := newGameLogProcessor()
	var events []gameLogEvent
	for _, line := range testPlayerLogLines(
		testPlayerID,
		"테스트유저",
		"203.0.113.45",
		start,
		30*time.Second,
	) {
		events = append(events, processor.processLine(line)...)
	}
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	login := events[0]
	if login.PlayerAction != "login" ||
		login.PlayerID != testPlayerID ||
		login.PlayerName != "테스트유저" ||
		!login.PlayerNameAuthenticated ||
		login.PlayerIP != "203.0.113.45" ||
		!login.PlayerJoinedAt.Equal(start) {
		t.Fatalf("login identity = %+v", login)
	}
	if events[1].PlayerAction != "observe" ||
		events[1].PlayerNameAuthenticated ||
		events[1].PlayerIP != "203.0.113.45" {
		t.Fatalf("chat observation = %+v", events[1])
	}
	logout := events[2]
	if logout.PlayerAction != "logout" ||
		logout.PlayerNameAuthenticated ||
		logout.PlayerIP != "203.0.113.45" ||
		!logout.PlayerJoinedAt.Equal(start) ||
		!logout.Time.Equal(start.Add(30*time.Second)) {
		t.Fatalf("logout identity = %+v", logout)
	}

	ipv6Body := "LogNet: NotifyAcceptedConnection: Name: Map, [UNetConnection] " +
		"RemoteAddr: [2001:db8::10]:45000, Name: IpConnection_2, " +
		"Driver: GameNetDriver IpNetDriver_2, IsServer: YES, UniqueId: INVALID"
	if address, ok := parseMordhauAcceptedGameConnection(ipv6Body); !ok ||
		address != "2001:db8::10" {
		t.Fatalf("IPv6 address = %q, %t", address, ok)
	}
}

func TestPlayerDetailUsesEmptyJSONArrays(t *testing.T) {
	detail := playerDetail(playerRecord{
		PlayFabID: testPlayerID,
	}, time.Now())

	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal player detail: %v", err)
	}
	for _, expected := range []string{
		`"nicknames":[]`,
		`"addresses":[]`,
		`"comments":[]`,
	} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("player detail JSON %s does not contain %s", encoded, expected)
		}
	}
}

func TestPlayersViewSortsRecentConnectionsAndCountsOpenSession(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.Local)
	older := now.Add(-2 * time.Hour)
	recent := now.Add(-time.Hour)
	manager := &Manager{
		playerHistory: playerHistoryFile{
			Version: playerHistoryVersion,
			Players: []playerRecord{
				{
					PlayFabID:     testPlayerID,
					LastConnected: older,
				},
				{
					PlayFabID:     testOtherPlayerID,
					LastConnected: recent,
					Connections: []playerConnection{{
						JoinedAt: recent,
					}},
				},
			},
		},
		playerServerProcess: func() (int, bool) { return 0, false },
	}

	view := manager.playersView()
	if len(view.Players) != 2 ||
		view.Players[0].PlayFabID != testOtherPlayerID ||
		view.Players[1].PlayFabID != testPlayerID {
		t.Fatalf("player order = %#v", view.Players)
	}
	detail := playerDetail(manager.playerHistory.Players[1], now)
	if !detail.Connected || detail.TotalSeconds != 3600 {
		t.Fatalf("open-session detail = %+v", detail)
	}
}

func TestPlayerHistoryAcceptsNicknamesOnlyFromAuthenticatedLogin(t *testing.T) {
	start := time.Date(2026, 7, 27, 12, 0, 0, 0, time.Local)
	events := []gameLogEvent{
		{
			Time:                    start,
			PlayerAction:            "login",
			PlayerID:                testPlayerID,
			PlayerName:              "첫 이름",
			PlayerNameAuthenticated: true,
			PlayerIP:                "203.0.113.45",
			PlayerJoinedAt:          start,
		},
		{
			Time:           start.Add(5 * time.Second),
			PlayerAction:   "observe",
			PlayerID:       testPlayerID,
			PlayerName:     "RuntimeInjected",
			PlayerIP:       "203.0.113.45",
			PlayerJoinedAt: start,
		},
		{
			Time:           start.Add(30 * time.Second),
			PlayerAction:   "logout",
			PlayerID:       testPlayerID,
			PlayerName:     "RuntimeInjected",
			PlayerIP:       "203.0.113.45",
			PlayerJoinedAt: start,
		},
	}
	history := playerHistoryFile{Version: playerHistoryVersion}
	if !applyPlayerGameEvents(&history, events) {
		t.Fatal("new session did not change history")
	}
	if applyPlayerGameEvents(&history, events) {
		t.Fatal("duplicate session changed history")
	}
	if len(history.Players) != 1 {
		t.Fatalf("players = %#v", history.Players)
	}
	player := history.Players[0]
	if player.LastNickname != "첫 이름" ||
		len(player.Nicknames) != 1 ||
		player.Nicknames[0].Value != "첫 이름" ||
		len(player.Addresses) != 1 ||
		len(player.Connections) != 1 {
		t.Fatalf("player identity = %+v", player)
	}
	if total := playerTotalSeconds(player, start.Add(time.Hour)); total != 30 {
		t.Fatalf("total seconds = %d, want 30", total)
	}
}

func TestPlayerHistoryRecordsLaterAuthenticatedUnicodeNickname(t *testing.T) {
	start := time.Date(2026, 7, 27, 12, 0, 0, 0, time.Local)
	history := playerHistoryFile{Version: playerHistoryVersion}
	for _, event := range []gameLogEvent{
		{
			Time:                    start,
			PlayerAction:            "login",
			PlayerID:                testPlayerID,
			PlayerName:              "첫 이름",
			PlayerNameAuthenticated: true,
			PlayerIP:                "203.0.113.45",
			PlayerJoinedAt:          start,
		},
		{
			Time:           start.Add(time.Minute),
			PlayerAction:   "logout",
			PlayerID:       testPlayerID,
			PlayerName:     "첫 이름",
			PlayerIP:       "203.0.113.45",
			PlayerJoinedAt: start,
		},
		{
			Time:                    start.Add(time.Hour),
			PlayerAction:            "login",
			PlayerID:                testPlayerID,
			PlayerName:              "Русский 이름",
			PlayerNameAuthenticated: true,
			PlayerIP:                "203.0.113.45",
			PlayerJoinedAt:          start.Add(time.Hour),
		},
	} {
		if !applyPlayerGameEvent(&history, event) {
			t.Fatalf("event did not change history: %+v", event)
		}
	}

	player := history.Players[0]
	if player.LastNickname != "Русский 이름" ||
		len(player.Nicknames) != 2 ||
		player.Nicknames[0].Value != "첫 이름" ||
		player.Nicknames[1].Value != "Русский 이름" {
		t.Fatalf("player identity = %+v", player)
	}
}

func TestPlayerHistoryObservationCannotReprioritizeKnownNickname(t *testing.T) {
	start := time.Date(2026, 7, 27, 12, 0, 0, 0, time.Local)
	history := playerHistoryFile{Version: playerHistoryVersion}
	for _, event := range []gameLogEvent{
		{
			Time:                    start,
			PlayerAction:            "login",
			PlayerID:                testPlayerID,
			PlayerName:              "Earlier",
			PlayerNameAuthenticated: true,
			PlayerJoinedAt:          start,
		},
		{
			Time:                    start.Add(time.Hour),
			PlayerAction:            "login",
			PlayerID:                testPlayerID,
			PlayerName:              "Current",
			PlayerNameAuthenticated: true,
			PlayerJoinedAt:          start.Add(time.Hour),
		},
	} {
		if !applyPlayerGameEvent(&history, event) {
			t.Fatalf("authenticated login did not change history: %+v", event)
		}
	}

	observed := gameLogEvent{
		Time:           start.Add(2 * time.Hour),
		PlayerAction:   "observe",
		PlayerID:       testPlayerID,
		PlayerName:     "Earlier",
		PlayerJoinedAt: start.Add(time.Hour),
	}
	if applyPlayerGameEvent(&history, observed) {
		t.Fatal("mutable observation changed persistent player history")
	}

	player := history.Players[0]
	if player.LastNickname != "Current" ||
		len(player.Nicknames) != 2 ||
		!player.Nicknames[0].LastSeenAt.Equal(start) ||
		!player.Nicknames[1].LastSeenAt.Equal(start.Add(time.Hour)) {
		t.Fatalf("mutable observation reprioritized nickname history: %+v", player)
	}
}

func TestPlayerHistoryImportsLogsOnceAndSurvivesReload(t *testing.T) {
	directory := t.TempDir()
	archiveDir := filepath.Join(directory, "log")
	currentDir := filepath.Join(directory, "Saved", "Logs")
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(currentDir, 0700); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 27, 10, 0, 0, 0, time.Local)
	archive := strings.Join(
		testPlayerLogLines(testPlayerID, "First", "198.51.100.4", start, 20*time.Second),
		"\n",
	) + "\n"
	current := strings.Join(
		testPlayerLogLines(
			testPlayerID,
			"Latest",
			"203.0.113.45",
			start.Add(time.Hour),
			40*time.Second,
		),
		"\n",
	) + "\n"
	if err := os.WriteFile(
		filepath.Join(archiveDir, "Mordhau_2026-07-27_10-00-20.log"),
		[]byte(archive),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(currentDir, "Mordhau.log")
	if err := os.WriteFile(currentPath, []byte(current), 0600); err != nil {
		t.Fatal(err)
	}
	historyPath := filepath.Join(directory, "players.json")
	newManager := func() *Manager {
		return &Manager{
			playerHistoryFile:      historyPath,
			playerArchiveDirectory: archiveDir,
			playerCurrentLogFile:   currentPath,
			playerServerProcess:    func() (int, bool) { return 0, false },
		}
	}

	first := newManager()
	if err := first.loadOrCreatePlayerHistory(); err != nil {
		t.Fatal(err)
	}
	detail, err := first.playerDetail(testPlayerID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.LastNickname != "Latest" ||
		detail.TotalSeconds != 60 ||
		len(detail.Nicknames) != 2 ||
		len(detail.Addresses) != 2 {
		t.Fatalf("imported detail = %+v", detail)
	}
	if len(first.playerHistory.ImportedLogs) != 1 {
		t.Fatalf("imported logs = %#v", first.playerHistory.ImportedLogs)
	}

	second := newManager()
	if err := second.loadOrCreatePlayerHistory(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := second.playerDetail(testPlayerID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TotalSeconds != 60 ||
		len(reloaded.Nicknames) != 2 ||
		len(reloaded.Addresses) != 2 {
		t.Fatalf("reloaded detail was duplicated: %+v", reloaded)
	}
}

func TestPlayerRestrictionCommandsAreVerifiedAgainstServerLists(t *testing.T) {
	directory := t.TempDir()
	manager := &Manager{
		playerHistoryFile: filepath.Join(directory, "players.json"),
		playerHistory: playerHistoryFile{
			Version: playerHistoryVersion,
			Players: []playerRecord{{PlayFabID: testPlayerID}},
		},
		playerServerProcess: func() (int, bool) { return 123, true },
	}
	banned := make(map[string]bool)
	muted := make(map[string]bool)
	var commands []string
	manager.rconCommandExecute = func(command string) (rconCommandResult, error) {
		commands = append(commands, command)
		fields := strings.Fields(command)
		switch fields[0] {
		case "ban":
			if len(fields) != 4 || fields[1] != testPlayerID ||
				fields[2] != "0" || fields[3] != "WebControl" {
				t.Fatalf("ban command = %q", command)
			}
			banned[fields[1]] = true
		case "unban":
			delete(banned, fields[1])
		case "mute":
			if len(fields) != 3 || fields[1] != testPlayerID || fields[2] != "0" {
				t.Fatalf("mute command = %q", command)
			}
			muted[fields[1]] = true
		case "unmute":
			delete(muted, fields[1])
		case "banlist":
			var lines []string
			for id := range banned {
				lines = append(lines, id+", 0")
			}
			sort.Strings(lines)
			if len(lines) == 0 {
				lines = []string{"No banned players found in ban list"}
			}
			return rconCommandResult{Lines: lines}, nil
		case "mutelist":
			var lines []string
			for id := range muted {
				lines = append(lines, id+", 0")
			}
			sort.Strings(lines)
			if len(lines) == 0 {
				lines = []string{"No muted players found in ban list"}
			}
			return rconCommandResult{Lines: lines}, nil
		default:
			t.Fatalf("unexpected RCON command %q", command)
		}
		return rconCommandResult{Lines: []string{"processed successfully"}}, nil
	}

	for _, change := range []struct {
		restriction string
		enabled     bool
	}{
		{"mute", true},
		{"ban", true},
		{"mute", false},
		{"ban", false},
	} {
		detail, err := manager.setPlayerRestriction(
			testPlayerID,
			change.restriction,
			change.enabled,
		)
		if err != nil {
			t.Fatalf("%s=%t: %v", change.restriction, change.enabled, err)
		}
		if change.restriction == "mute" && detail.Muted != change.enabled {
			t.Fatalf("mute detail = %+v", detail)
		}
		if change.restriction == "ban" && detail.Banned != change.enabled {
			t.Fatalf("ban detail = %+v", detail)
		}
	}
	for _, expected := range []string{
		"mute " + testPlayerID + " 0",
		"ban " + testPlayerID + " 0 WebControl",
		"unmute " + testPlayerID,
		"unban " + testPlayerID,
	} {
		found := false
		for _, command := range commands {
			if command == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing command %q in %#v", expected, commands)
		}
	}
}

func TestPlayerCommentsPersistAuthorAndAuditActor(t *testing.T) {
	directory := t.TempDir()
	manager := &Manager{
		playerHistoryFile: filepath.Join(directory, "players.json"),
		auditPath:         filepath.Join(directory, "audit.log"),
		playerHistory: playerHistoryFile{
			Version: playerHistoryVersion,
			Players: []playerRecord{{PlayFabID: testPlayerID}},
		},
	}
	if err := manager.initializeAuditLog(); err != nil {
		t.Fatal(err)
	}
	session := Session{Username: "operator", CSRF: "csrf-token"}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://manager.example/api/players/comments",
		strings.NewReader(
			`{"playfab_id":"`+testPlayerID+`","body":"한국어 메모\nsecond line"}`,
		),
	)
	request.Header.Set("X-CSRF-Token", session.CSRF)
	response := httptest.NewRecorder()
	manager.playerCommentHandler(response, request, session)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var detail PlayerDetail
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Comments) != 1 ||
		detail.Comments[0].Author != session.Username ||
		detail.Comments[0].Body != "한국어 메모\nsecond line" {
		t.Fatalf("comments = %#v", detail.Comments)
	}
	auditData, err := os.ReadFile(manager.auditPath)
	if err != nil {
		t.Fatal(err)
	}
	audit := string(auditData)
	for _, expected := range []string{
		`"event":"player_comment_added"`,
		`"account":"operator"`,
		`"playfab_id":"` + testPlayerID + `"`,
	} {
		if !strings.Contains(audit, expected) {
			t.Fatalf("audit is missing %q: %s", expected, audit)
		}
	}
	if strings.Contains(audit, "한국어 메모") {
		t.Fatal("comment body leaked into the audit log")
	}

	if _, err := manager.addPlayerComment(
		testPlayerID,
		"operator",
		"\x00invalid",
	); !errors.Is(err, errPlayerCommentInvalid) {
		t.Fatalf("invalid comment = %v", err)
	}
}
