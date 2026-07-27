package manager

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	gameLogPollInterval = 250 * time.Millisecond
	gameLogReadLimit    = 4 << 20
)

type gameLogFollower struct {
	path        string
	initialized bool
	missing     bool
	info        os.FileInfo
	offset      int64
	partial     []byte
}

type gameLogEvent struct {
	Time           time.Time
	Kind           string
	Text           string
	PlayerAction   string
	PlayerID       string
	PlayerName     string
	PlayerIP       string
	PlayerJoinedAt time.Time
}

type gameLogPlayer struct {
	name     string
	ip       string
	joinedAt time.Time
}

type pendingGameConnection struct {
	ip       string
	accepted time.Time
}

type gameLogProcessor struct {
	players     map[string]gameLogPlayer
	pending     map[string]gameLogPlayer
	connections []pendingGameConnection
}

func newGameLogProcessor() *gameLogProcessor {
	return &gameLogProcessor{
		players: make(map[string]gameLogPlayer),
		pending: make(map[string]gameLogPlayer),
	}
}

func (processor *gameLogProcessor) reset() {
	clear(processor.players)
	clear(processor.pending)
	processor.connections = nil
}

func (follower *gameLogFollower) initialize(onLine func(string)) (bool, error) {
	if follower.initialized {
		return follower.info != nil, nil
	}

	file, err := os.Open(follower.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			follower.initialized = true
			follower.missing = true
			return false, nil
		}
		return false, err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 128<<10)
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			if strings.HasSuffix(line, "\n") {
				line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
				onLine(line)
			} else {
				follower.partial = append(follower.partial[:0], line...)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return false, readErr
		}
	}

	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, err
	}
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	follower.initialized = true
	follower.missing = false
	follower.info = info
	follower.offset = offset
	return true, nil
}

func (follower *gameLogFollower) readNewLines() (
	lines []string,
	replaced bool,
	available bool,
	err error,
) {
	file, err := os.Open(follower.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			replaced = follower.info != nil
			follower.missing = true
			follower.info = nil
			follower.offset = 0
			follower.partial = nil
			return nil, replaced, false, nil
		}
		return nil, false, false, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, false, false, err
	}
	if follower.info == nil ||
		!os.SameFile(follower.info, info) ||
		info.Size() < follower.offset {
		follower.offset = 0
		follower.partial = nil
		replaced = true
	}
	follower.info = info
	follower.missing = false

	if _, err := file.Seek(follower.offset, io.SeekStart); err != nil {
		return nil, replaced, false, err
	}
	data, err := io.ReadAll(io.LimitReader(file, gameLogReadLimit))
	if err != nil {
		return nil, replaced, false, err
	}
	follower.offset += int64(len(data))
	if len(data) == 0 {
		return nil, replaced, true, nil
	}

	data = append(follower.partial, data...)
	parts := bytes.Split(data, []byte{'\n'})
	follower.partial = append(follower.partial[:0], parts[len(parts)-1]...)

	lines = make([]string, 0, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		lines = append(lines, strings.TrimSuffix(string(part), "\r"))
	}
	return lines, replaced, true, nil
}

func parseMordhauLogTime(value string) (time.Time, bool) {
	millisecondSeparator := strings.LastIndexByte(value, ':')
	if millisecondSeparator < 0 || len(value)-millisecondSeparator != 4 {
		return time.Time{}, false
	}
	milliseconds, err := strconv.Atoi(value[millisecondSeparator+1:])
	if err != nil || milliseconds < 0 || milliseconds > 999 {
		return time.Time{}, false
	}
	base, err := time.ParseInLocation(
		"2006.01.02-15.04.05",
		value[:millisecondSeparator],
		time.Local,
	)
	if err != nil {
		return time.Time{}, false
	}
	return base.Add(time.Duration(milliseconds) * time.Millisecond), true
}

func parseMordhauLogEnvelope(line string) (time.Time, string, bool) {
	if !strings.HasPrefix(line, "[") {
		return time.Time{}, "", false
	}
	timestampEnd := strings.IndexByte(line, ']')
	if timestampEnd < 2 {
		return time.Time{}, "", false
	}
	eventTime, ok := parseMordhauLogTime(line[1:timestampEnd])
	if !ok {
		return time.Time{}, "", false
	}
	frameEndRelative := strings.IndexByte(line[timestampEnd+1:], ']')
	if frameEndRelative < 0 {
		return time.Time{}, "", false
	}
	frameEnd := timestampEnd + 1 + frameEndRelative
	return eventTime, strings.TrimSpace(line[frameEnd+1:]), true
}

func validMordhauPlayerID(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, character := range []byte(value) {
		if !((character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f') ||
			(character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func parseMordhauLoginRequest(body string) (string, string, bool) {
	const marker = "LogNet: Login request: ?Name="
	const idMarker = " userId: MordhauOnlineSubsystem:"
	const platformMarker = " platform:"
	if !strings.HasPrefix(body, marker) {
		return "", "", false
	}
	remainder := body[len(marker):]
	nameEnd := strings.Index(remainder, idMarker)
	if nameEnd < 0 {
		return "", "", false
	}
	name := strings.TrimSpace(remainder[:nameEnd])
	remainder = remainder[nameEnd+len(idMarker):]
	idEnd := strings.Index(remainder, platformMarker)
	if idEnd < 0 {
		return "", "", false
	}
	playerID := strings.TrimSpace(remainder[:idEnd])
	if name == "" || !validMordhauPlayerID(playerID) {
		return "", "", false
	}
	return name, playerID, true
}

func parseMordhauAuthentication(body string) (string, string, bool) {
	const marker = "LogMordhauGameSession: Player authentication for "
	const suffix = " completed successfully"
	if !strings.HasPrefix(body, marker) || !strings.HasSuffix(body, suffix) {
		return "", "", false
	}
	identity := strings.TrimSuffix(strings.TrimPrefix(body, marker), suffix)
	idStart := strings.LastIndex(identity, " (")
	if idStart < 1 || !strings.HasSuffix(identity, ")") {
		return "", "", false
	}
	name := strings.TrimSpace(identity[:idStart])
	playerID := strings.TrimSuffix(identity[idStart+2:], ")")
	if name == "" || !validMordhauPlayerID(playerID) {
		return "", "", false
	}
	return name, playerID, true
}

func parseMordhauRemoteAddress(body string) (string, bool) {
	const marker = "RemoteAddr: "
	start := strings.Index(body, marker)
	if start < 0 {
		return "", false
	}
	value := body[start+len(marker):]
	if end := strings.IndexByte(value, ','); end >= 0 {
		value = value[:end]
	}
	value = strings.TrimSpace(value)
	addressPort, err := netip.ParseAddrPort(value)
	if err == nil {
		return normalizePlayerAddress(addressPort.Addr().Unmap().String())
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	return normalizePlayerAddress(value)
}

func parseMordhauAcceptedGameConnection(body string) (string, bool) {
	const marker = "LogNet: NotifyAcceptedConnection:"
	if !strings.HasPrefix(body, marker) ||
		!strings.Contains(body, "Driver: GameNetDriver ") ||
		!strings.Contains(body, "UniqueId: INVALID") {
		return "", false
	}
	return parseMordhauRemoteAddress(body)
}

func parseMordhauGameConnectionClose(body string) (string, string, bool) {
	const marker = "LogNet: UChannel::CleanUp: ChIndex == 0. Closing connection."
	const idMarker = "UniqueId: MordhauOnlineSubsystem:"
	if !strings.HasPrefix(body, marker) ||
		!strings.Contains(body, "Driver: GameNetDriver ") {
		return "", "", false
	}
	idStart := strings.LastIndex(body, idMarker)
	if idStart < 0 {
		return "", "", false
	}
	playerID := body[idStart+len(idMarker):]
	if idEnd := strings.IndexByte(playerID, ','); idEnd >= 0 {
		playerID = playerID[:idEnd]
	}
	playerID = strings.TrimSpace(playerID)
	if !validMordhauPlayerID(playerID) {
		return "", "", false
	}
	address, _ := parseMordhauRemoteAddress(body)
	return playerID, address, true
}

func parseMordhauChatPayload(body string) (
	chat string,
	playerID string,
	name string,
	ok bool,
) {
	const marker = "LogGameMode: Display: "
	markerIndex := strings.Index(body, marker)
	if markerIndex < 0 {
		return "", "", "", false
	}
	payload := strings.TrimSpace(body[markerIndex+len(marker):])
	channelEnd := strings.Index(payload, ") ")
	if !strings.HasPrefix(payload, "(") || channelEnd < 2 {
		return "", "", "", false
	}
	channel := payload[:channelEnd+1]
	remainder := payload[channelEnd+2:]

	if !strings.HasSuffix(remainder, `"`) {
		return "", "", "", false
	}
	var (
		identitySeparator int
		messageStart      int
	)
	for searchStart := 0; searchStart < len(remainder); {
		relative := strings.Index(remainder[searchStart:], `: "`)
		if relative < 0 {
			return "", "", "", false
		}
		candidateMessageStart := searchStart + relative
		identity := remainder[:candidateMessageStart]
		candidateSeparator := strings.LastIndex(identity, ", ")
		if candidateSeparator > 0 &&
			validMordhauPlayerID(identity[candidateSeparator+2:]) {
			identitySeparator = candidateSeparator
			messageStart = candidateMessageStart
			break
		}
		searchStart = candidateMessageStart + 1
	}
	if messageStart == 0 {
		return "", "", "", false
	}
	name = remainder[:identitySeparator]
	playerID = remainder[identitySeparator+2 : messageStart]
	message := strings.TrimSuffix(remainder[messageStart+3:], `"`)

	chat = fmt.Sprintf(
		"Chat: %s, %s, %s %s",
		playerID,
		name,
		channel,
		message,
	)
	return strings.ToValidUTF8(chat, "�"), playerID, name, true
}

func parseMordhauChatLogLine(line string) (string, bool) {
	_, body, ok := parseMordhauLogEnvelope(line)
	if !ok {
		return "", false
	}
	chat, _, _, ok := parseMordhauChatPayload(body)
	return chat, ok
}

func formatMordhauMatchState(state string) string {
	switch state {
	case "EnteringMap":
		return "Entering map"
	case "WaitingToStart":
		return "Waiting to start"
	case "InProgress":
		return "In progress"
	case "WaitingPostMatch":
		return "Waiting post match"
	case "LeavingMap":
		return "Leaving map"
	case "Aborted":
		return "Aborted"
	}

	var result strings.Builder
	for index, character := range state {
		if index > 0 && unicode.IsUpper(character) {
			result.WriteByte(' ')
			character = unicode.ToLower(character)
		}
		result.WriteRune(character)
	}
	return result.String()
}

func parseMordhauMatchState(body string) (string, bool) {
	const marker = "LogGameMode: Display: Match State Changed from "
	if !strings.HasPrefix(body, marker) {
		return "", false
	}
	change := strings.TrimPrefix(body, marker)
	newStateStart := strings.LastIndex(change, " to ")
	if newStateStart < 0 {
		return "", false
	}
	newState := strings.TrimSpace(change[newStateStart+4:])
	if newState == "" {
		return "", false
	}
	return "MatchState: " + formatMordhauMatchState(newState), true
}

func hasMordhauEventTimestamp(payload string) bool {
	if len(payload) < len("2006.01.02-15.04.05:") {
		return false
	}
	_, err := time.Parse("2006.01.02-15.04.05", payload[:19])
	return err == nil && payload[19] == ':'
}

func parseMordhauFeed(body string) (string, string, bool) {
	const marker = "LogGameMode: Display: "
	if !strings.HasPrefix(body, marker) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(body, marker))
	if !hasMordhauEventTimestamp(payload) {
		return "", "", false
	}

	for _, fragment := range []string{
		") got an assist kill for the death of ",
		") killed ",
		") committed suicide",
		") teamkilled ",
	} {
		if strings.Contains(payload, fragment) {
			return "killfeed", "Killfeed: " + payload, true
		}
	}
	if strings.Contains(payload, " score changed by ") &&
		strings.Contains(payload, " points and is now ") {
		return "scorefeed", "Scorefeed: " + payload, true
	}
	return "", "", false
}

func parseMordhauPunishment(body string) (string, bool) {
	const marker = "LogMordhauGameSession: "
	if !strings.HasPrefix(body, marker) {
		return "", false
	}
	message := strings.TrimSpace(strings.TrimPrefix(body, marker))
	for _, verbosity := range []string{"Display: ", "Warning: ", "Error: "} {
		message = strings.TrimPrefix(message, verbosity)
	}
	for _, prefix := range []string{
		"Kicked player ",
		"Ban for PlayFab ID ",
		"Banned PlayFab ID ",
		"Unbanned PlayFab ID ",
		"Muted PlayFab ID ",
		"Unmuted PlayFab ID ",
	} {
		if strings.HasPrefix(message, prefix) {
			return "Punishment: " + message, true
		}
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "votekick") ||
		strings.Contains(lower, "vote kick") ||
		strings.Contains(lower, "teamkill ban") {
		return "Punishment: " + message, true
	}
	return "", false
}

func (processor *gameLogProcessor) addConnection(address string, accepted time.Time) {
	if address == "" || accepted.IsZero() {
		return
	}
	processor.pruneConnections(accepted)
	processor.connections = append(processor.connections, pendingGameConnection{
		ip:       address,
		accepted: accepted,
	})
}

func (processor *gameLogProcessor) pruneConnections(now time.Time) {
	keep := processor.connections[:0]
	for _, connection := range processor.connections {
		age := now.Sub(connection.accepted)
		if age >= 0 && age <= playerPendingConnectionMaxAge {
			keep = append(keep, connection)
		}
	}
	processor.connections = keep
}

func (processor *gameLogProcessor) nextConnection(now time.Time) string {
	processor.pruneConnections(now)
	if len(processor.connections) == 0 {
		return ""
	}
	address := processor.connections[0].ip
	processor.connections = processor.connections[1:]
	return address
}

func (processor *gameLogProcessor) closePlayerSessions(at time.Time) []gameLogEvent {
	if at.IsZero() || len(processor.players) == 0 {
		return nil
	}
	events := make([]gameLogEvent, 0, len(processor.players))
	for playerID, player := range processor.players {
		if player.joinedAt.IsZero() {
			continue
		}
		events = append(events, gameLogEvent{
			Time:           at,
			PlayerAction:   "logout",
			PlayerID:       playerID,
			PlayerName:     player.name,
			PlayerIP:       player.ip,
			PlayerJoinedAt: player.joinedAt,
		})
	}
	sort.Slice(events, func(left, right int) bool {
		return events[left].PlayerID < events[right].PlayerID
	})
	return events
}

func (processor *gameLogProcessor) processLine(line string) []gameLogEvent {
	eventTime, body, ok := parseMordhauLogEnvelope(line)
	if !ok {
		return nil
	}

	if address, ok := parseMordhauAcceptedGameConnection(body); ok {
		processor.addConnection(address, eventTime)
		return nil
	}
	if name, playerID, ok := parseMordhauLoginRequest(body); ok {
		processor.pending[playerID] = gameLogPlayer{
			name: name,
			ip:   processor.nextConnection(eventTime),
		}
		return nil
	}
	if name, playerID, ok := parseMordhauAuthentication(body); ok {
		pending := processor.pending[playerID]
		if pending.name != "" {
			name = pending.name
		}
		existing, alreadyActive := processor.players[playerID]
		if alreadyActive {
			existing.name = name
			if existing.ip == "" {
				existing.ip = pending.ip
			}
			processor.players[playerID] = existing
			delete(processor.pending, playerID)
			return nil
		}
		player := gameLogPlayer{
			name:     name,
			ip:       pending.ip,
			joinedAt: eventTime,
		}
		processor.players[playerID] = player
		delete(processor.pending, playerID)
		return []gameLogEvent{{
			Time: eventTime,
			Kind: "login",
			Text: fmt.Sprintf(
				"Login: %s: %s (%s) logged in",
				eventTime.Format("2006.01.02-15.04.05"),
				name,
				playerID,
			),
			PlayerAction:   "login",
			PlayerID:       playerID,
			PlayerName:     name,
			PlayerIP:       player.ip,
			PlayerJoinedAt: eventTime,
		}}
	}
	if chat, playerID, name, ok := parseMordhauChatPayload(body); ok {
		player := processor.players[playerID]
		player.name = name
		processor.players[playerID] = player
		delete(processor.pending, playerID)
		return []gameLogEvent{{
			Time:           eventTime,
			Kind:           "chat",
			Text:           chat,
			PlayerAction:   "observe",
			PlayerID:       playerID,
			PlayerName:     name,
			PlayerIP:       player.ip,
			PlayerJoinedAt: player.joinedAt,
		}}
	}
	if playerID, address, ok := parseMordhauGameConnectionClose(body); ok {
		player, active := processor.players[playerID]
		delete(processor.players, playerID)
		delete(processor.pending, playerID)
		if !active {
			return nil
		}
		return []gameLogEvent{{
			Time: eventTime,
			Kind: "login",
			Text: fmt.Sprintf(
				"Login: %s: %s (%s) logged out",
				eventTime.Format("2006.01.02-15.04.05"),
				player.name,
				playerID,
			),
			PlayerAction:   "logout",
			PlayerID:       playerID,
			PlayerName:     player.name,
			PlayerIP:       address,
			PlayerJoinedAt: player.joinedAt,
		}}
	}
	if text, ok := parseMordhauMatchState(body); ok {
		return []gameLogEvent{{Time: eventTime, Kind: "matchstate", Text: text}}
	}
	if kind, text, ok := parseMordhauFeed(body); ok {
		return []gameLogEvent{{Time: eventTime, Kind: kind, Text: text}}
	}
	if text, ok := parseMordhauPunishment(body); ok {
		return []gameLogEvent{{Time: eventTime, Kind: "punishment", Text: text}}
	}
	return nil
}

func (manager *Manager) setEventSourceState(connected bool, status string) {
	manager.rconMu.Lock()
	manager.eventSourceConnected = connected
	manager.eventSourceStatus = status
	manager.rconMu.Unlock()
}

func (manager *Manager) gameLogLoop(ctx context.Context) {
	follower := &gameLogFollower{path: gameLogPath}
	processor := newGameLogProcessor()
	ticker := time.NewTicker(gameLogPollInterval)
	defer ticker.Stop()

	var lastReadError string
	wasRunning := serverRunning()
	if _, err := follower.initialize(func(line string) {
		if wasRunning {
			_ = processor.processLine(line)
		}
	}); err != nil {
		lastReadError = err.Error()
		log.Printf("initialize MORDHAU game-log follower: %v", err)
	}

	for {
		if ctx.Err() != nil {
			return
		}
		running := serverRunning()
		if !running {
			if wasRunning {
				manager.recordPlayerGameEvents(processor.closePlayerSessions(time.Now()))
				processor.reset()
			}
			wasRunning = false
			manager.setEventSourceState(false, "Waiting for server")
		} else {
			wasRunning = true
			if !follower.initialized {
				_, err := follower.initialize(func(line string) {
					_ = processor.processLine(line)
				})
				if err != nil {
					if err.Error() != lastReadError {
						log.Printf("initialize MORDHAU game-log follower: %v", err)
					}
					lastReadError = err.Error()
					manager.setEventSourceState(false, "Mordhau.log unavailable")
				}
			}

			if follower.initialized {
				lines, replaced, available, err := follower.readNewLines()
				if replaced {
					manager.recordPlayerGameEvents(processor.closePlayerSessions(time.Now()))
					processor.reset()
				}
				if err != nil {
					if err.Error() != lastReadError {
						log.Printf("follow MORDHAU game log: %v", err)
					}
					lastReadError = err.Error()
					manager.setEventSourceState(false, "Mordhau.log unavailable")
				} else {
					lastReadError = ""
					if available {
						manager.setEventSourceState(true, "Live · Mordhau.log")
					} else {
						manager.setEventSourceState(false, "Waiting for Mordhau.log")
					}
					var playerEvents []gameLogEvent
					for _, line := range lines {
						for _, event := range processor.processLine(line) {
							manager.addRCONEventAt(event.Time, event.Kind, event.Text)
							if event.PlayerAction != "" {
								playerEvents = append(playerEvents, event)
							}
						}
					}
					manager.recordPlayerGameEvents(playerEvents)
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
