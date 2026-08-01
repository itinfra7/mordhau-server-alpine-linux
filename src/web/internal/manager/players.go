package manager

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	playerHistoryVersion          = 1
	playerMaximumRecords          = 100000
	playerMaximumNicknames        = 256
	playerMaximumAddresses        = 256
	playerMaximumConnections      = 10000
	playerMaximumComments         = 1000
	playerMaximumNicknameRunes    = 128
	playerMaximumCommentRunes     = 2000
	playerMaximumCommentBytes     = 8192
	playerMaximumLevel            = 2147483647
	playerLogScannerMaximumBytes  = 16 << 20
	playerRestrictionCacheLife    = 15 * time.Second
	playerRestrictionExpiryPoll   = 15 * time.Second
	playerRestrictionMaximumMins  = 525600
	playerPendingConnectionMaxAge = 2 * time.Minute
	playerMaximumSessionTimeline  = 200
	playerMaximumModerationReason = 160
)

var (
	errPlayerInvalid         = errors.New("invalid player")
	errPlayerNotFound        = errors.New("player not found")
	errPlayerCommentInvalid  = errors.New("invalid player comment")
	errPlayerCommentLimit    = errors.New("player comment limit reached")
	errPlayerServerStopped   = errors.New("the game server must be running")
	errPlayerRestrictionSync = errors.New("player restriction state is unavailable")
)

type playerKnownValue struct {
	Value      string    `json:"value"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type playerConnection struct {
	JoinedAt time.Time  `json:"joined_at"`
	LeftAt   *time.Time `json:"left_at,omitempty"`
	IP       string     `json:"ip,omitempty"`
}

type PlayerComment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type PlayerRestrictionLease struct {
	ExpiresAt time.Time `json:"expires_at"`
	SetBy     string    `json:"set_by"`
	Reason    string    `json:"reason,omitempty"`
}

type playerRecord struct {
	PlayFabID         string                  `json:"playfab_id"`
	LastNickname      string                  `json:"last_nickname,omitempty"`
	LastLevel         *int                    `json:"last_level,omitempty"`
	Platform          string                  `json:"platform,omitempty"`
	PlatformAccountID string                  `json:"platform_account_id,omitempty"`
	LastConnected     time.Time               `json:"last_connected_at,omitempty"`
	Nicknames         []playerKnownValue      `json:"nicknames,omitempty"`
	Addresses         []playerKnownValue      `json:"addresses,omitempty"`
	Connections       []playerConnection      `json:"connections,omitempty"`
	Muted             bool                    `json:"muted"`
	Banned            bool                    `json:"banned"`
	MuteLease         *PlayerRestrictionLease `json:"mute_lease,omitempty"`
	BanLease          *PlayerRestrictionLease `json:"ban_lease,omitempty"`
	Comments          []PlayerComment         `json:"comments,omitempty"`
}

type playerImportedLog struct {
	Name             string `json:"name"`
	Size             int64  `json:"size"`
	ModifiedUnixNano int64  `json:"modified_unix_nano"`
}

type playerHistoryFile struct {
	Version      int                 `json:"version"`
	Revision     uint64              `json:"revision"`
	Players      []playerRecord      `json:"players"`
	ImportedLogs []playerImportedLog `json:"imported_logs,omitempty"`
}

type PlayerSummary struct {
	PlayFabID         string          `json:"playfab_id"`
	LastNickname      string          `json:"last_nickname,omitempty"`
	LastLevel         *int            `json:"last_level,omitempty"`
	Platform          string          `json:"platform,omitempty"`
	PlatformAccountID string          `json:"platform_account_id,omitempty"`
	LastConnected     time.Time       `json:"last_connected_at,omitempty"`
	Nicknames         []string        `json:"nicknames,omitempty"`
	TotalSeconds      int64           `json:"total_seconds"`
	LastLocation      *PlayerLocation `json:"last_location,omitempty"`
	Connected         bool            `json:"connected"`
	Muted             bool            `json:"muted"`
	Banned            bool            `json:"banned"`
}

type PlayerKnownValue struct {
	Value      string          `json:"value"`
	LastSeenAt time.Time       `json:"last_seen_at"`
	Location   *PlayerLocation `json:"location,omitempty"`
}

type PlayerDetail struct {
	PlayFabID         string                  `json:"playfab_id"`
	LastNickname      string                  `json:"last_nickname,omitempty"`
	LastLevel         *int                    `json:"last_level,omitempty"`
	Platform          string                  `json:"platform,omitempty"`
	PlatformAccountID string                  `json:"platform_account_id,omitempty"`
	LastConnected     time.Time               `json:"last_connected_at,omitempty"`
	Connected         bool                    `json:"connected"`
	ActiveSince       *time.Time              `json:"active_since,omitempty"`
	TotalSeconds      int64                   `json:"total_seconds"`
	Nicknames         []PlayerKnownValue      `json:"nicknames"`
	Addresses         []PlayerKnownValue      `json:"addresses"`
	Muted             bool                    `json:"muted"`
	Banned            bool                    `json:"banned"`
	MuteLease         *PlayerRestrictionLease `json:"mute_lease,omitempty"`
	BanLease          *PlayerRestrictionLease `json:"ban_lease,omitempty"`
	Sessions          []PlayerSession         `json:"sessions"`
	Comments          []PlayerComment         `json:"comments"`
	GeneratedAt       time.Time               `json:"generated_at"`
}

type PlayerSession struct {
	JoinedAt    time.Time       `json:"joined_at"`
	LeftAt      *time.Time      `json:"left_at,omitempty"`
	IP          string          `json:"ip,omitempty"`
	Location    *PlayerLocation `json:"location,omitempty"`
	DurationSec int64           `json:"duration_seconds"`
	Active      bool            `json:"active"`
}

type ConnectedPlayer struct {
	PlayerSlot        int             `json:"player_slot"`
	PlayFabID         string          `json:"playfab_id,omitempty"`
	Nickname          string          `json:"nickname,omitempty"`
	Level             *int            `json:"level,omitempty"`
	PingMS            *int            `json:"ping_ms,omitempty"`
	Platform          string          `json:"platform"`
	PlatformAccountID string          `json:"platform_account_id,omitempty"`
	Location          *PlayerLocation `json:"location,omitempty"`
}

type PlayerRestrictionStatus struct {
	ServerRunning bool       `json:"server_running"`
	Available     bool       `json:"available"`
	LastSyncedAt  *time.Time `json:"last_synced_at,omitempty"`
	Error         string     `json:"error,omitempty"`
}

type PlayersView struct {
	Players      []PlayerSummary         `json:"players"`
	Revision     uint64                  `json:"revision"`
	Restrictions PlayerRestrictionStatus `json:"restrictions"`
	GeoIP        GeoIPStatus             `json:"geoip"`
	GeneratedAt  time.Time               `json:"generated_at"`
}

type playerRestrictionRequest struct {
	PlayFabID       string `json:"playfab_id"`
	Restriction     string `json:"restriction"`
	Enabled         bool   `json:"enabled"`
	DurationMinutes int    `json:"duration_minutes,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type playerActionRequest struct {
	PlayFabID string `json:"playfab_id"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
}

type playerCommentRequest struct {
	PlayFabID string `json:"playfab_id"`
	Body      string `json:"body"`
}

func (m *Manager) playerHistoryPath() string {
	if m.playerHistoryFile != "" {
		return m.playerHistoryFile
	}
	return playerHistoryPath
}

func (m *Manager) playerArchivePath() string {
	if m.playerArchiveDirectory != "" {
		return m.playerArchiveDirectory
	}
	return logDir
}

func (m *Manager) playerCurrentLogPath() string {
	if m.playerCurrentLogFile != "" {
		return m.playerCurrentLogFile
	}
	return gameLogPath
}

func (m *Manager) playerGameServerRunning() bool {
	if m.playerServerProcess != nil {
		_, running := m.playerServerProcess()
		return running
	}
	return serverRunning()
}

func normalizePlayerNickname(value string) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > playerMaximumNicknameRunes {
		return "", false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", false
		}
	}
	return value, true
}

func normalizePlayerAddress(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return "", false
	}
	address = address.Unmap()
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() ||
		address.IsLinkLocalUnicast() {
		return "", false
	}
	return address.String(), true
}

func validSteamID64(value string) bool {
	if len(value) != 17 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validEpicAccountID(value string) bool {
	if len(value) < 3 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' ||
			character == '.' || character == ':') {
			return false
		}
	}
	return true
}

func validPlayerPlatformIdentity(platform, accountID string) bool {
	if platform == "" && accountID == "" {
		return true
	}
	switch platform {
	case "Steam":
		return validSteamID64(accountID)
	case "Epic":
		return validEpicAccountID(accountID)
	default:
		return false
	}
}

func validPlayerRestrictionLease(lease *PlayerRestrictionLease) bool {
	if lease == nil {
		return true
	}
	return !lease.ExpiresAt.IsZero() &&
		safeAuditAccount(lease.SetBy) == lease.SetBy &&
		utf8.ValidString(lease.Reason) &&
		len(lease.Reason) <= playerMaximumModerationReason
}

func clonePlayerRestrictionLease(
	lease *PlayerRestrictionLease,
) *PlayerRestrictionLease {
	if lease == nil {
		return nil
	}
	copy := *lease
	return &copy
}

func normalizePlayerComment(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%w: comment must be valid UTF-8", errPlayerCommentInvalid)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: comment cannot be empty", errPlayerCommentInvalid)
	}
	if len(value) > playerMaximumCommentBytes ||
		utf8.RuneCountInString(value) > playerMaximumCommentRunes {
		return "", fmt.Errorf(
			"%w: comment must not exceed %d characters or %d UTF-8 bytes",
			errPlayerCommentInvalid,
			playerMaximumCommentRunes,
			playerMaximumCommentBytes,
		)
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return "", fmt.Errorf(
				"%w: comment contains an unsupported control character",
				errPlayerCommentInvalid,
			)
		}
	}
	return value, nil
}

func validPlayerComment(comment PlayerComment) bool {
	if comment.ID == "" || len(comment.ID) > 64 ||
		safeAuditAccount(comment.Author) != comment.Author ||
		comment.CreatedAt.IsZero() {
		return false
	}
	_, err := normalizePlayerComment(comment.Body)
	return err == nil
}

func validImportedPlayerLog(logFile playerImportedLog) bool {
	return logFile.Name != "" &&
		logFile.Name == filepath.Base(logFile.Name) &&
		strings.HasPrefix(logFile.Name, "Mordhau_") &&
		strings.HasSuffix(strings.ToLower(logFile.Name), ".log") &&
		logFile.Size >= 0
}

func validatePlayerHistory(history *playerHistoryFile) error {
	if history.Version != playerHistoryVersion {
		return fmt.Errorf("unsupported player history version %d", history.Version)
	}
	if len(history.Players) > playerMaximumRecords {
		return errors.New("player history contains too many records")
	}
	seenPlayers := make(map[string]struct{}, len(history.Players))
	for index := range history.Players {
		player := &history.Players[index]
		if !validMordhauPlayerID(player.PlayFabID) {
			return fmt.Errorf("player history contains an invalid PlayFabID")
		}
		key := strings.ToUpper(player.PlayFabID)
		if _, duplicate := seenPlayers[key]; duplicate {
			return fmt.Errorf("player history contains duplicate PlayFabID %q", player.PlayFabID)
		}
		seenPlayers[key] = struct{}{}
		player.PlayFabID = key

		if len(player.Nicknames) > playerMaximumNicknames ||
			len(player.Addresses) > playerMaximumAddresses ||
			len(player.Connections) > playerMaximumConnections ||
			len(player.Comments) > playerMaximumComments {
			return fmt.Errorf("player history record %q exceeds a storage limit", player.PlayFabID)
		}
		if player.LastLevel != nil &&
			(*player.LastLevel < 1 ||
				*player.LastLevel > playerMaximumLevel) {
			return fmt.Errorf("player history record %q contains an invalid level", player.PlayFabID)
		}
		if !validPlayerPlatformIdentity(
			player.Platform,
			player.PlatformAccountID,
		) {
			return fmt.Errorf(
				"player history record %q contains an invalid platform identity",
				player.PlayFabID,
			)
		}
		if !validPlayerRestrictionLease(player.MuteLease) ||
			!validPlayerRestrictionLease(player.BanLease) {
			return fmt.Errorf(
				"player history record %q contains an invalid restriction lease",
				player.PlayFabID,
			)
		}
		for _, nickname := range player.Nicknames {
			normalized, ok := normalizePlayerNickname(nickname.Value)
			if !ok || normalized != nickname.Value || nickname.LastSeenAt.IsZero() {
				return fmt.Errorf("player history record %q contains an invalid nickname", player.PlayFabID)
			}
		}
		for _, address := range player.Addresses {
			normalized, ok := normalizePlayerAddress(address.Value)
			if !ok || normalized != address.Value || address.LastSeenAt.IsZero() {
				return fmt.Errorf("player history record %q contains an invalid address", player.PlayFabID)
			}
		}
		for _, connection := range player.Connections {
			if connection.JoinedAt.IsZero() ||
				(connection.LeftAt != nil && connection.LeftAt.Before(connection.JoinedAt)) {
				return fmt.Errorf("player history record %q contains an invalid connection", player.PlayFabID)
			}
			if connection.IP != "" {
				normalized, ok := normalizePlayerAddress(connection.IP)
				if !ok || normalized != connection.IP {
					return fmt.Errorf("player history record %q contains an invalid connection address", player.PlayFabID)
				}
			}
		}
		for _, comment := range player.Comments {
			if !validPlayerComment(comment) {
				return fmt.Errorf("player history record %q contains an invalid comment", player.PlayFabID)
			}
		}
	}
	seenLogs := make(map[string]struct{}, len(history.ImportedLogs))
	for _, imported := range history.ImportedLogs {
		if !validImportedPlayerLog(imported) {
			return errors.New("player history contains an invalid imported log")
		}
		if _, duplicate := seenLogs[imported.Name]; duplicate {
			return fmt.Errorf("player history contains duplicate imported log %q", imported.Name)
		}
		seenLogs[imported.Name] = struct{}{}
	}
	return nil
}

func normalizeLegacyPlayerProgress(history *playerHistoryFile) bool {
	changed := false
	for index := range history.Players {
		if history.Players[index].LastLevel != nil &&
			*history.Players[index].LastLevel == 0 {
			history.Players[index].LastLevel = nil
			changed = true
		}
	}
	if changed {
		history.Revision++
	}
	return changed
}

func removeLinkLocalPlayerAddresses(history *playerHistoryFile) bool {
	changed := false
	for playerIndex := range history.Players {
		player := &history.Players[playerIndex]
		addresses := player.Addresses[:0]
		for _, known := range player.Addresses {
			address, err := netip.ParseAddr(strings.TrimSpace(known.Value))
			if err == nil && address.Unmap().IsLinkLocalUnicast() {
				changed = true
				continue
			}
			addresses = append(addresses, known)
		}
		player.Addresses = addresses

		for connectionIndex := range player.Connections {
			connection := &player.Connections[connectionIndex]
			address, err := netip.ParseAddr(strings.TrimSpace(connection.IP))
			if err == nil && address.Unmap().IsLinkLocalUnicast() {
				connection.IP = ""
				changed = true
			}
		}
	}
	if changed {
		history.Revision++
	}
	return changed
}

func (m *Manager) loadOrCreatePlayerHistory() error {
	path := m.playerHistoryPath()
	created := false
	migrated := false
	history := playerHistoryFile{
		Version: playerHistoryVersion,
		Players: make([]playerRecord, 0),
	}
	if err := readJSON(path, &history); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load player history: %w", err)
		}
		created = true
	} else {
		migrated = normalizeLegacyPlayerProgress(&history)
		if removeLinkLocalPlayerAddresses(&history) {
			migrated = true
		}
		if err := validatePlayerHistory(&history); err != nil {
			return err
		}
	}

	m.playersMu.Lock()
	m.playerHistory = history
	changed, err := m.importPlayerLogsLocked()
	if err == nil && (changed || created || migrated) {
		err = m.savePlayerHistoryLocked()
	}
	m.playersMu.Unlock()
	if err != nil {
		return fmt.Errorf("initialize player history: %w", err)
	}
	return nil
}

func (m *Manager) savePlayerHistoryLocked() error {
	m.playerHistory.Version = playerHistoryVersion
	if m.playerHistory.Players == nil {
		m.playerHistory.Players = make([]playerRecord, 0)
	}
	return writeJSONAtomic(m.playerHistoryPath(), m.playerHistory, 0600)
}

func (m *Manager) currentPlayerRevision() uint64 {
	m.playersMu.RLock()
	defer m.playersMu.RUnlock()
	return m.playerHistory.Revision
}

func playerRecordIndex(history *playerHistoryFile, playFabID string) int {
	for index := range history.Players {
		if strings.EqualFold(history.Players[index].PlayFabID, playFabID) {
			return index
		}
	}
	return -1
}

func ensurePlayerRecord(history *playerHistoryFile, playFabID string) (*playerRecord, bool) {
	playFabID = strings.ToUpper(playFabID)
	if index := playerRecordIndex(history, playFabID); index >= 0 {
		return &history.Players[index], false
	}
	history.Players = append(history.Players, playerRecord{PlayFabID: playFabID})
	return &history.Players[len(history.Players)-1], true
}

func updatePlayerKnownValue(
	values *[]playerKnownValue,
	value string,
	seenAt time.Time,
	maximum int,
) bool {
	if value == "" || seenAt.IsZero() {
		return false
	}
	for index := range *values {
		if (*values)[index].Value != value {
			continue
		}
		if !seenAt.After((*values)[index].LastSeenAt) {
			return false
		}
		(*values)[index].LastSeenAt = seenAt
		return true
	}
	if len(*values) >= maximum {
		oldest := 0
		for index := 1; index < len(*values); index++ {
			if (*values)[index].LastSeenAt.Before((*values)[oldest].LastSeenAt) {
				oldest = index
			}
		}
		(*values)[oldest] = playerKnownValue{Value: value, LastSeenAt: seenAt}
		return true
	}
	*values = append(*values, playerKnownValue{Value: value, LastSeenAt: seenAt})
	return true
}

func updatePlayerAuthenticatedNickname(
	player *playerRecord,
	name string,
	seenAt time.Time,
) bool {
	changed := false
	if normalized, ok := normalizePlayerNickname(name); ok {
		if updatePlayerKnownValue(
			&player.Nicknames,
			normalized,
			seenAt,
			playerMaximumNicknames,
		) {
			changed = true
		}
		latest := 0
		for index := 1; index < len(player.Nicknames); index++ {
			if player.Nicknames[index].LastSeenAt.After(
				player.Nicknames[latest].LastSeenAt,
			) {
				latest = index
			}
		}
		if len(player.Nicknames) > 0 &&
			player.LastNickname != player.Nicknames[latest].Value {
			player.LastNickname = player.Nicknames[latest].Value
			changed = true
		}
	}
	return changed
}

func updatePlayerAddress(
	player *playerRecord,
	address string,
	seenAt time.Time,
) bool {
	if normalized, ok := normalizePlayerAddress(address); ok {
		return updatePlayerKnownValue(
			&player.Addresses,
			normalized,
			seenAt,
			playerMaximumAddresses,
		)
	}
	return false
}

func playerConnectionIndex(player *playerRecord, joinedAt time.Time) int {
	if joinedAt.IsZero() {
		return -1
	}
	for index := range player.Connections {
		if player.Connections[index].JoinedAt.Equal(joinedAt) {
			return index
		}
	}
	return -1
}

func applyPlayerGameEvent(history *playerHistoryFile, event gameLogEvent) bool {
	if !validMordhauPlayerID(event.PlayerID) ||
		event.PlayerAction == "" ||
		event.Time.IsZero() {
		return false
	}
	player, created := ensurePlayerRecord(history, event.PlayerID)
	changed := created
	if event.PlayerAction == "login" &&
		event.PlayerNameAuthenticated &&
		updatePlayerAuthenticatedNickname(player, event.PlayerName, event.Time) {
		changed = true
	}
	if updatePlayerAddress(player, event.PlayerIP, event.Time) {
		changed = true
	}

	switch event.PlayerAction {
	case "login":
		if event.PlayerJoinedAt.IsZero() {
			event.PlayerJoinedAt = event.Time
		}
		if index := playerConnectionIndex(player, event.PlayerJoinedAt); index >= 0 {
			connection := &player.Connections[index]
			if connection.IP == "" {
				if normalized, ok := normalizePlayerAddress(event.PlayerIP); ok {
					connection.IP = normalized
					changed = true
				}
			}
		} else if len(player.Connections) < playerMaximumConnections {
			connection := playerConnection{JoinedAt: event.PlayerJoinedAt}
			if normalized, ok := normalizePlayerAddress(event.PlayerIP); ok {
				connection.IP = normalized
			}
			player.Connections = append(player.Connections, connection)
			changed = true
		}
		if player.LastConnected.IsZero() || event.PlayerJoinedAt.After(player.LastConnected) {
			player.LastConnected = event.PlayerJoinedAt
			changed = true
		}

	case "logout":
		index := playerConnectionIndex(player, event.PlayerJoinedAt)
		if index < 0 && event.PlayerJoinedAt.IsZero() {
			for candidate := len(player.Connections) - 1; candidate >= 0; candidate-- {
				if player.Connections[candidate].LeftAt == nil {
					index = candidate
					break
				}
			}
		}
		if index >= 0 {
			connection := &player.Connections[index]
			if normalized, ok := normalizePlayerAddress(event.PlayerIP); ok {
				if connection.IP != normalized {
					previous := connection.IP
					connection.IP = normalized
					changed = true
					if previous != "" {
						usedElsewhere := false
						for otherIndex := range player.Connections {
							if otherIndex != index &&
								player.Connections[otherIndex].IP == previous {
								usedElsewhere = true
								break
							}
						}
						if !usedElsewhere {
							for addressIndex := range player.Addresses {
								if player.Addresses[addressIndex].Value == previous {
									player.Addresses = append(
										player.Addresses[:addressIndex],
										player.Addresses[addressIndex+1:]...,
									)
									break
								}
							}
						}
					}
				}
			}
			if connection.LeftAt == nil && !event.Time.Before(connection.JoinedAt) {
				leftAt := event.Time
				connection.LeftAt = &leftAt
				changed = true
			}
		}

	case "observe":
	default:
		return false
	}
	return changed
}

func applyPlayerGameEvents(history *playerHistoryFile, events []gameLogEvent) bool {
	changed := false
	for _, event := range events {
		if applyPlayerGameEvent(history, event) {
			changed = true
		}
	}
	return changed
}

func (m *Manager) recordPlayerGameEvents(events []gameLogEvent) {
	if len(events) == 0 {
		return
	}
	m.playersMu.Lock()
	defer m.playersMu.Unlock()
	if !applyPlayerGameEvents(&m.playerHistory, events) {
		return
	}
	m.playerHistory.Revision++
	if err := m.savePlayerHistoryLocked(); err != nil {
		log.Printf("save MORDHAU player history: %v", err)
	}
}

type playerLevelObservation struct {
	PlayFabID string
	Level     int
}

type playerPlatformObservation struct {
	PlayFabID         string
	Platform          string
	PlatformAccountID string
}

func applyPlayerPlatformObservations(
	history *playerHistoryFile,
	observations []playerPlatformObservation,
) bool {
	changed := false
	for _, observation := range observations {
		if !validMordhauPlayerID(observation.PlayFabID) ||
			!validPlayerPlatformIdentity(
				observation.Platform,
				observation.PlatformAccountID,
			) ||
			observation.Platform == "" {
			continue
		}
		player, created := ensurePlayerRecord(history, observation.PlayFabID)
		if created ||
			player.Platform != observation.Platform ||
			player.PlatformAccountID != observation.PlatformAccountID {
			player.Platform = observation.Platform
			player.PlatformAccountID = observation.PlatformAccountID
			changed = true
		}
	}
	return changed
}

func (m *Manager) recordPlayerPlatformObservations(
	observations []playerPlatformObservation,
) {
	if len(observations) == 0 {
		return
	}
	m.playersMu.Lock()
	defer m.playersMu.Unlock()
	if !applyPlayerPlatformObservations(&m.playerHistory, observations) {
		return
	}
	m.playerHistory.Revision++
	if err := m.savePlayerHistoryLocked(); err != nil {
		log.Printf("save MORDHAU player platform identities: %v", err)
	}
}

func applyPlayerLevelObservations(
	history *playerHistoryFile,
	observations []playerLevelObservation,
) bool {
	changed := false
	for _, observation := range observations {
		if !validMordhauPlayerID(observation.PlayFabID) ||
			observation.Level < 1 ||
			observation.Level > playerMaximumLevel {
			continue
		}
		player, created := ensurePlayerRecord(history, observation.PlayFabID)
		if created ||
			player.LastLevel == nil ||
			*player.LastLevel != observation.Level {
			level := observation.Level
			player.LastLevel = &level
			changed = true
		}
	}
	return changed
}

func (m *Manager) recordPlayerLevelObservations(
	observations []playerLevelObservation,
) {
	if len(observations) == 0 {
		return
	}
	m.playersMu.Lock()
	defer m.playersMu.Unlock()
	if !applyPlayerLevelObservations(&m.playerHistory, observations) {
		return
	}
	m.playerHistory.Revision++
	if err := m.savePlayerHistoryLocked(); err != nil {
		log.Printf("save MORDHAU player levels: %v", err)
	}
}

func importedPlayerLogMatches(
	imported []playerImportedLog,
	name string,
	info os.FileInfo,
) bool {
	_ = info
	for _, item := range imported {
		if item.Name == name {
			return true
		}
	}
	return false
}

func setImportedPlayerLog(
	imported *[]playerImportedLog,
	name string,
	info os.FileInfo,
) {
	record := playerImportedLog{
		Name:             name,
		Size:             info.Size(),
		ModifiedUnixNano: info.ModTime().UnixNano(),
	}
	for index := range *imported {
		if (*imported)[index].Name == name {
			(*imported)[index] = record
			return
		}
	}
	*imported = append(*imported, record)
	sort.Slice(*imported, func(left, right int) bool {
		return (*imported)[left].Name < (*imported)[right].Name
	})
}

func scanPlayerLogHistory(
	path string,
	closeAtEnd bool,
	history *playerHistoryFile,
) (bool, error) {
	reader, err := openGameLogReader(context.Background(), path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	processor := newGameLogProcessor()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 128<<10), playerLogScannerMaximumBytes)
	changed := false
	var lastTime time.Time
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if eventTime, _, ok := parseMordhauLogEnvelope(line); ok {
			lastTime = eventTime
		}
		if applyPlayerGameEvents(history, processor.processLine(line)) {
			changed = true
		}
	}
	scanErr := scanner.Err()
	closeErr := reader.Close()
	if scanErr != nil {
		return false, scanErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if closeAtEnd && !lastTime.IsZero() &&
		applyPlayerGameEvents(history, processor.closePlayerSessions(lastTime)) {
		changed = true
	}
	return changed, nil
}

func (m *Manager) importPlayerLogsLocked() (bool, error) {
	changed := false
	archiveDirectory := m.playerArchivePath()
	entries, err := os.ReadDir(archiveDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	for _, entry := range entries {
		name := entry.Name()
		canonicalName, validName := canonicalArchivedGameLogName(name)
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!validName {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() ||
			importedPlayerLogMatches(
				m.playerHistory.ImportedLogs,
				canonicalName,
				info,
			) {
			continue
		}
		importedChanged, err := scanPlayerLogHistory(
			filepath.Join(archiveDirectory, name),
			true,
			&m.playerHistory,
		)
		if err != nil {
			return false, fmt.Errorf("scan archived game log %q: %w", name, err)
		}
		if importedChanged {
			changed = true
		}
		setImportedPlayerLog(
			&m.playerHistory.ImportedLogs,
			canonicalName,
			info,
		)
		changed = true
	}

	currentChanged, err := scanPlayerLogHistory(
		m.playerCurrentLogPath(),
		!m.playerGameServerRunning(),
		&m.playerHistory,
	)
	if err != nil {
		return false, fmt.Errorf("scan current game log: %w", err)
	}
	if currentChanged {
		changed = true
	}
	if changed {
		m.playerHistory.Revision++
	}
	return changed, nil
}

func playerConnectedAt(player playerRecord) (bool, *time.Time) {
	var active *time.Time
	for index := range player.Connections {
		connection := &player.Connections[index]
		if connection.LeftAt != nil {
			continue
		}
		if active == nil || connection.JoinedAt.After(*active) {
			joinedAt := connection.JoinedAt
			active = &joinedAt
		}
	}
	return active != nil, active
}

func playerTotalSeconds(player playerRecord, now time.Time) int64 {
	var total time.Duration
	for _, connection := range player.Connections {
		end := now
		if connection.LeftAt != nil {
			end = *connection.LeftAt
		}
		if end.After(connection.JoinedAt) {
			total += end.Sub(connection.JoinedAt)
		}
	}
	return int64(total / time.Second)
}

func sortedPlayerKnownValues(values []playerKnownValue) []PlayerKnownValue {
	out := make([]PlayerKnownValue, 0, len(values))
	for _, value := range values {
		out = append(out, PlayerKnownValue{
			Value:      value.Value,
			LastSeenAt: value.LastSeenAt,
		})
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].LastSeenAt.After(out[right].LastSeenAt)
	})
	return out
}

func sortedPlayerComments(comments []PlayerComment) []PlayerComment {
	out := make([]PlayerComment, len(comments))
	copy(out, comments)
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].CreatedAt.After(out[right].CreatedAt)
	})
	return out
}

func playerSessions(player playerRecord, now time.Time) []PlayerSession {
	start := 0
	if len(player.Connections) > playerMaximumSessionTimeline {
		start = len(player.Connections) - playerMaximumSessionTimeline
	}
	out := make([]PlayerSession, 0, len(player.Connections)-start)
	for index := start; index < len(player.Connections); index++ {
		connection := player.Connections[index]
		end := now
		active := connection.LeftAt == nil
		if connection.LeftAt != nil {
			end = *connection.LeftAt
		}
		duration := int64(0)
		if end.After(connection.JoinedAt) {
			duration = int64(end.Sub(connection.JoinedAt) / time.Second)
		}
		out = append(out, PlayerSession{
			JoinedAt:    connection.JoinedAt,
			LeftAt:      connection.LeftAt,
			IP:          connection.IP,
			DurationSec: duration,
			Active:      active,
		})
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].JoinedAt.After(out[right].JoinedAt)
	})
	return out
}

func playerSummary(player playerRecord, now time.Time) PlayerSummary {
	connected, _ := playerConnectedAt(player)
	nicknames := sortedPlayerKnownValues(player.Nicknames)
	names := make([]string, 0, len(nicknames))
	for _, nickname := range nicknames {
		names = append(names, nickname.Value)
	}
	var lastLevel *int
	if player.LastLevel != nil {
		value := *player.LastLevel
		lastLevel = &value
	}
	return PlayerSummary{
		PlayFabID:         player.PlayFabID,
		LastNickname:      player.LastNickname,
		LastLevel:         lastLevel,
		Platform:          player.Platform,
		PlatformAccountID: player.PlatformAccountID,
		LastConnected:     player.LastConnected,
		Nicknames:         names,
		TotalSeconds:      playerTotalSeconds(player, now),
		Connected:         connected,
		Muted:             player.Muted,
		Banned:            player.Banned,
	}
}

func latestPlayerAddress(player playerRecord) string {
	if len(player.Addresses) == 0 {
		return ""
	}
	latest := 0
	for index := 1; index < len(player.Addresses); index++ {
		if player.Addresses[index].LastSeenAt.After(
			player.Addresses[latest].LastSeenAt,
		) {
			latest = index
		}
	}
	return player.Addresses[latest].Value
}

func playerDetail(player playerRecord, now time.Time) PlayerDetail {
	connected, activeSince := playerConnectedAt(player)
	var lastLevel *int
	if player.LastLevel != nil {
		value := *player.LastLevel
		lastLevel = &value
	}
	return PlayerDetail{
		PlayFabID:         player.PlayFabID,
		LastNickname:      player.LastNickname,
		LastLevel:         lastLevel,
		Platform:          player.Platform,
		PlatformAccountID: player.PlatformAccountID,
		LastConnected:     player.LastConnected,
		Connected:         connected,
		ActiveSince:       activeSince,
		TotalSeconds:      playerTotalSeconds(player, now),
		Nicknames:         sortedPlayerKnownValues(player.Nicknames),
		Addresses:         sortedPlayerKnownValues(player.Addresses),
		Muted:             player.Muted,
		Banned:            player.Banned,
		MuteLease:         clonePlayerRestrictionLease(player.MuteLease),
		BanLease:          clonePlayerRestrictionLease(player.BanLease),
		Sessions:          playerSessions(player, now),
		Comments:          sortedPlayerComments(player.Comments),
		GeneratedAt:       now,
	}
}

func (m *Manager) playerRestrictionStatus() PlayerRestrictionStatus {
	m.playerRestrictionMu.Lock()
	lastSync := m.playerRestrictionLastSync
	lastError := m.playerRestrictionLastError
	m.playerRestrictionMu.Unlock()
	status := PlayerRestrictionStatus{
		ServerRunning: m.playerGameServerRunning(),
		Error:         lastError,
	}
	status.Available = status.ServerRunning && !lastSync.IsZero() && lastError == ""
	if !lastSync.IsZero() {
		synced := lastSync
		status.LastSyncedAt = &synced
	}
	if !status.ServerRunning && status.Error == "" {
		status.Error = errPlayerServerStopped.Error()
	}
	return status
}

func (m *Manager) playersView() PlayersView {
	now := time.Now()
	m.playersMu.RLock()
	players := make([]PlayerSummary, 0, len(m.playerHistory.Players))
	for _, player := range m.playerHistory.Players {
		summary := playerSummary(player, now)
		summary.LastLocation = m.playerLocationForAddress(
			latestPlayerAddress(player),
		)
		players = append(players, summary)
	}
	revision := m.playerHistory.Revision
	m.playersMu.RUnlock()
	sort.SliceStable(players, func(left, right int) bool {
		if !players[left].LastConnected.Equal(players[right].LastConnected) {
			return players[left].LastConnected.After(players[right].LastConnected)
		}
		return players[left].PlayFabID < players[right].PlayFabID
	})
	return PlayersView{
		Players:      players,
		Revision:     revision,
		Restrictions: m.playerRestrictionStatus(),
		GeoIP:        m.geoIPStatusView(),
		GeneratedAt:  now,
	}
}

func (m *Manager) playerDetail(playFabID string) (PlayerDetail, error) {
	if !validMordhauPlayerID(playFabID) {
		return PlayerDetail{}, errPlayerInvalid
	}
	m.playersMu.RLock()
	index := playerRecordIndex(&m.playerHistory, playFabID)
	if index < 0 {
		m.playersMu.RUnlock()
		return PlayerDetail{}, errPlayerNotFound
	}
	record := m.playerHistory.Players[index]
	m.playersMu.RUnlock()
	detail := playerDetail(record, time.Now())
	for index := range detail.Addresses {
		detail.Addresses[index].Location = m.playerLocationForAddress(
			detail.Addresses[index].Value,
		)
	}
	for index := range detail.Sessions {
		detail.Sessions[index].Location = m.playerLocationForAddress(
			detail.Sessions[index].IP,
		)
	}
	return detail, nil
}

func (m *Manager) connectedPlayersView() []ConnectedPlayer {
	m.runtimeMu.RLock()
	targets := append([]RuntimeTarget(nil), m.runtimeTargets...)
	ready := m.runtimeSummary.Ready
	m.runtimeMu.RUnlock()
	if !ready {
		return []ConnectedPlayer{}
	}

	m.playersMu.RLock()
	records := make(map[string]playerRecord, len(m.playerHistory.Players))
	for _, player := range m.playerHistory.Players {
		records[player.PlayFabID] = player
	}
	m.playersMu.RUnlock()

	players := make([]ConnectedPlayer, 0)
	for _, target := range targets {
		if target.Kind != "player_controller" {
			continue
		}
		player := ConnectedPlayer{
			PlayerSlot:        target.PlayerSlot,
			PlayFabID:         strings.ToUpper(target.PlayFabID),
			Nickname:          target.PlayerName,
			PingMS:            target.PingMS,
			Platform:          target.Platform,
			PlatformAccountID: target.PlatformAccountID,
		}
		record, exists := records[player.PlayFabID]
		if exists {
			if player.Nickname == "" {
				player.Nickname = record.LastNickname
			}
			if record.LastLevel != nil {
				level := *record.LastLevel
				player.Level = &level
			}
			if player.Platform == "" {
				player.Platform = record.Platform
				player.PlatformAccountID = record.PlatformAccountID
			}
			player.Location = m.playerLocationForAddress(
				latestPlayerAddress(record),
			)
		}
		if player.Platform == "" {
			player.Platform = "Unknown"
		}
		players = append(players, player)
	}
	sort.SliceStable(players, func(left, right int) bool {
		return players[left].PlayerSlot < players[right].PlayerSlot
	})
	return players
}

func parsePlayerRestrictionList(lines []string) map[string]struct{} {
	players := make(map[string]struct{})
	for _, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" || strings.HasPrefix(strings.ToLower(value), "no ") {
			continue
		}
		if comma := strings.IndexByte(value, ','); comma >= 0 {
			value = strings.TrimSpace(value[:comma])
		} else if space := strings.IndexAny(value, " \t"); space >= 0 {
			value = strings.TrimSpace(value[:space])
		}
		if validMordhauPlayerID(value) {
			players[strings.ToUpper(value)] = struct{}{}
		}
	}
	return players
}

func (m *Manager) executePlayerRCONCommand(command string) (rconCommandResult, error) {
	if m.rconCommandExecute != nil {
		return m.rconCommandExecute(command)
	}
	return m.runRCONCommand(command)
}

func (m *Manager) queryPlayerRestrictionsLocked() (
	banned map[string]struct{},
	muted map[string]struct{},
	err error,
) {
	banResult, err := m.executePlayerRCONCommand("banlist")
	if err != nil {
		return nil, nil, fmt.Errorf("query ban list: %w", err)
	}
	muteResult, err := m.executePlayerRCONCommand("mutelist")
	if err != nil {
		return nil, nil, fmt.Errorf("query mute list: %w", err)
	}
	return parsePlayerRestrictionList(banResult.Lines),
		parsePlayerRestrictionList(muteResult.Lines),
		nil
}

func (m *Manager) applyPlayerRestrictions(
	banned map[string]struct{},
	muted map[string]struct{},
) error {
	m.playersMu.Lock()
	defer m.playersMu.Unlock()
	changed := false
	for index := range m.playerHistory.Players {
		player := &m.playerHistory.Players[index]
		_, isBanned := banned[player.PlayFabID]
		_, isMuted := muted[player.PlayFabID]
		if player.Banned != isBanned {
			player.Banned = isBanned
			changed = true
		}
		if !isBanned && player.BanLease != nil {
			player.BanLease = nil
			changed = true
		}
		if player.Muted != isMuted {
			player.Muted = isMuted
			changed = true
		}
		if !isMuted && player.MuteLease != nil {
			player.MuteLease = nil
			changed = true
		}
	}
	if !changed {
		return nil
	}
	m.playerHistory.Revision++
	return m.savePlayerHistoryLocked()
}

func (m *Manager) refreshPlayerRestrictions(force bool) error {
	m.playerRestrictionMu.Lock()
	defer m.playerRestrictionMu.Unlock()
	if !force &&
		!m.playerRestrictionLastSync.IsZero() &&
		time.Since(m.playerRestrictionLastSync) < playerRestrictionCacheLife {
		if m.playerRestrictionLastError != "" {
			return fmt.Errorf("%w: %s", errPlayerRestrictionSync, m.playerRestrictionLastError)
		}
		return nil
	}
	if !m.playerGameServerRunning() {
		m.playerRestrictionLastError = errPlayerServerStopped.Error()
		return errPlayerServerStopped
	}

	m.rconCommandMu.Lock()
	banned, muted, err := m.queryPlayerRestrictionsLocked()
	m.rconCommandMu.Unlock()
	if err != nil {
		m.playerRestrictionLastError = err.Error()
		return fmt.Errorf("%w: %v", errPlayerRestrictionSync, err)
	}
	if err := m.applyPlayerRestrictions(banned, muted); err != nil {
		m.playerRestrictionLastError = err.Error()
		return err
	}
	m.playerRestrictionLastSync = time.Now()
	m.playerRestrictionLastError = ""
	return nil
}

func playerRestrictionCommand(
	playFabID string,
	restriction string,
	enabled bool,
) (string, error) {
	return playerRestrictionCommandWithReason(
		playFabID,
		restriction,
		enabled,
		"WebControl",
	)
}

func playerRestrictionCommandWithReason(
	playFabID string,
	restriction string,
	enabled bool,
	reason string,
) (string, error) {
	if !validMordhauPlayerID(playFabID) {
		return "", errPlayerInvalid
	}
	playFabID = strings.ToUpper(playFabID)
	switch restriction {
	case "mute":
		if enabled {
			return "mute " + playFabID + " 0", nil
		}
		return "unmute " + playFabID, nil
	case "ban":
		if enabled {
			return "ban " + playFabID + " 0 " + reason, nil
		}
		return "unban " + playFabID, nil
	default:
		return "", fmt.Errorf("%w: unsupported restriction", errPlayerInvalid)
	}
}

func (m *Manager) setPlayerRestriction(
	playFabID string,
	restriction string,
	enabled bool,
) (PlayerDetail, error) {
	return m.setPlayerRestrictionWithOptions(
		playFabID,
		restriction,
		enabled,
		0,
		"WebControl",
		"system",
	)
}

func normalizeModerationReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "WebControl", nil
	}
	if len(reason) > playerMaximumModerationReason {
		return "", fmt.Errorf(
			"%w: reason must not exceed %d characters",
			errPlayerInvalid,
			playerMaximumModerationReason,
		)
	}
	for _, character := range reason {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == ' ' || character == '-' ||
			character == '_' || character == '.' ||
			character == ',' || character == ':') {
			return "", fmt.Errorf(
				"%w: moderation reasons support printable ASCII letters, numbers, spaces, and .,_:-",
				errPlayerInvalid,
			)
		}
	}
	return reason, nil
}

func (m *Manager) setPlayerRestrictionWithOptions(
	playFabID string,
	restriction string,
	enabled bool,
	durationMinutes int,
	reason string,
	actor string,
) (PlayerDetail, error) {
	if durationMinutes < 0 || durationMinutes > playerRestrictionMaximumMins ||
		(!enabled && durationMinutes != 0) {
		return PlayerDetail{}, fmt.Errorf(
			"%w: duration must be 0-%d minutes",
			errPlayerInvalid,
			playerRestrictionMaximumMins,
		)
	}
	normalizedReason, err := normalizeModerationReason(reason)
	if err != nil {
		return PlayerDetail{}, err
	}
	command, err := playerRestrictionCommandWithReason(
		playFabID,
		restriction,
		enabled,
		normalizedReason,
	)
	if err != nil {
		return PlayerDetail{}, err
	}
	playFabID = strings.ToUpper(playFabID)
	m.playersMu.RLock()
	found := playerRecordIndex(&m.playerHistory, playFabID) >= 0
	m.playersMu.RUnlock()
	if !found {
		return PlayerDetail{}, errPlayerNotFound
	}
	if !m.playerGameServerRunning() {
		return PlayerDetail{}, errPlayerServerStopped
	}

	m.playerRestrictionMu.Lock()
	defer m.playerRestrictionMu.Unlock()
	var lease *PlayerRestrictionLease
	if enabled && durationMinutes > 0 {
		lease = &PlayerRestrictionLease{
			ExpiresAt: time.Now().Add(time.Duration(durationMinutes) * time.Minute),
			SetBy:     safeAuditAccount(actor),
			Reason:    normalizedReason,
		}
	}
	m.playersMu.Lock()
	index := playerRecordIndex(&m.playerHistory, playFabID)
	if index < 0 {
		m.playersMu.Unlock()
		return PlayerDetail{}, errPlayerNotFound
	}
	player := &m.playerHistory.Players[index]
	var previousLease *PlayerRestrictionLease
	switch restriction {
	case "mute":
		previousLease = clonePlayerRestrictionLease(player.MuteLease)
	case "ban":
		previousLease = clonePlayerRestrictionLease(player.BanLease)
	}
	leaseChanged := !restrictionLeasesEqual(previousLease, lease)
	previousRevision := m.playerHistory.Revision
	if leaseChanged {
		switch restriction {
		case "mute":
			player.MuteLease = clonePlayerRestrictionLease(lease)
		case "ban":
			player.BanLease = clonePlayerRestrictionLease(lease)
		}
		m.playerHistory.Revision++
		if err := m.savePlayerHistoryLocked(); err != nil {
			switch restriction {
			case "mute":
				player.MuteLease = previousLease
			case "ban":
				player.BanLease = previousLease
			}
			m.playerHistory.Revision = previousRevision
			m.playersMu.Unlock()
			m.playerRestrictionLastError = err.Error()
			return PlayerDetail{}, err
		}
	}
	m.playersMu.Unlock()
	restoreLease := func() {
		if !leaseChanged {
			return
		}
		m.playersMu.Lock()
		index := playerRecordIndex(&m.playerHistory, playFabID)
		if index >= 0 {
			switch restriction {
			case "mute":
				m.playerHistory.Players[index].MuteLease =
					clonePlayerRestrictionLease(previousLease)
			case "ban":
				m.playerHistory.Players[index].BanLease =
					clonePlayerRestrictionLease(previousLease)
			}
			m.playerHistory.Revision++
			_ = m.savePlayerHistoryLocked()
		}
		m.playersMu.Unlock()
	}

	m.rconCommandMu.Lock()
	_, commandErr := m.executePlayerRCONCommand(command)
	var banned, muted map[string]struct{}
	if commandErr == nil {
		banned, muted, commandErr = m.queryPlayerRestrictionsLocked()
	}
	m.rconCommandMu.Unlock()
	if commandErr != nil {
		restoreLease()
		m.playerRestrictionLastError = commandErr.Error()
		return PlayerDetail{}, fmt.Errorf("%w: %v", errPlayerRestrictionSync, commandErr)
	}

	_, isBanned := banned[playFabID]
	_, isMuted := muted[playFabID]
	actual := isMuted
	if restriction == "ban" {
		actual = isBanned
	}
	if actual != enabled {
		restoreLease()
		m.playerRestrictionLastError = "server did not confirm the requested restriction state"
		return PlayerDetail{}, fmt.Errorf(
			"%w: server did not confirm the requested %s state",
			errPlayerRestrictionSync,
			restriction,
		)
	}
	if err := m.applyPlayerRestrictions(banned, muted); err != nil {
		m.playerRestrictionLastError = err.Error()
		return PlayerDetail{}, err
	}
	m.playerRestrictionLastSync = time.Now()
	m.playerRestrictionLastError = ""
	return m.playerDetail(playFabID)
}

func restrictionLeasesEqual(
	left *PlayerRestrictionLease,
	right *PlayerRestrictionLease,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.ExpiresAt.Equal(right.ExpiresAt) &&
		left.SetBy == right.SetBy &&
		left.Reason == right.Reason
}

type expiredPlayerRestriction struct {
	PlayFabID   string
	Restriction string
}

func (m *Manager) expiredPlayerRestrictions(now time.Time) []expiredPlayerRestriction {
	m.playersMu.RLock()
	defer m.playersMu.RUnlock()
	expired := make([]expiredPlayerRestriction, 0)
	for _, player := range m.playerHistory.Players {
		if player.Muted && player.MuteLease != nil &&
			!now.Before(player.MuteLease.ExpiresAt) {
			expired = append(expired, expiredPlayerRestriction{
				PlayFabID: player.PlayFabID, Restriction: "mute",
			})
		}
		if player.Banned && player.BanLease != nil &&
			!now.Before(player.BanLease.ExpiresAt) {
			expired = append(expired, expiredPlayerRestriction{
				PlayFabID: player.PlayFabID, Restriction: "ban",
			})
		}
	}
	return expired
}

func (m *Manager) expirePlayerRestrictions() {
	if !m.playerGameServerRunning() {
		return
	}
	for _, restriction := range m.expiredPlayerRestrictions(time.Now()) {
		if _, err := m.setPlayerRestrictionWithOptions(
			restriction.PlayFabID,
			restriction.Restriction,
			false,
			0,
			"WebControl",
			"system",
		); err != nil {
			m.auditActorEvent(
				"system",
				"local",
				"player_restriction_expiry_failed",
				map[string]string{
					"playfab_id":  restriction.PlayFabID,
					"restriction": restriction.Restriction,
					"error":       safeAuditText(err.Error(), 160),
				},
			)
			continue
		}
		m.auditActorEvent(
			"system",
			"local",
			"player_restriction_expired",
			map[string]string{
				"playfab_id":  restriction.PlayFabID,
				"restriction": restriction.Restriction,
			},
		)
	}
}

func (m *Manager) playerRestrictionExpiryLoop(ctx context.Context) {
	m.expirePlayerRestrictions()
	ticker := time.NewTicker(playerRestrictionExpiryPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.expirePlayerRestrictions()
		}
	}
}

func (m *Manager) playerAction(
	playFabID string,
	action string,
	reason string,
	message string,
) (PlayerDetail, error) {
	if !validMordhauPlayerID(playFabID) {
		return PlayerDetail{}, errPlayerInvalid
	}
	playFabID = strings.ToUpper(playFabID)
	m.playersMu.RLock()
	index := playerRecordIndex(&m.playerHistory, playFabID)
	var nickname string
	if index >= 0 {
		nickname = m.playerHistory.Players[index].LastNickname
	}
	m.playersMu.RUnlock()
	if index < 0 {
		return PlayerDetail{}, errPlayerNotFound
	}
	if !m.playerGameServerRunning() {
		return PlayerDetail{}, errPlayerServerStopped
	}

	switch action {
	case "kick":
		normalized, err := normalizeModerationReason(reason)
		if err != nil {
			return PlayerDetail{}, err
		}
		m.rconCommandMu.Lock()
		_, err = m.executePlayerRCONCommand(
			"kick " + playFabID + " " + normalized,
		)
		m.rconCommandMu.Unlock()
		if err != nil {
			return PlayerDetail{}, fmt.Errorf("%w: kick failed: %v", errPlayerRestrictionSync, err)
		}
	case "warn":
		message = strings.TrimSpace(message)
		if err := validateUnicodeMessage(message); err != nil {
			return PlayerDetail{}, errPlayerInvalid
		}
		label := nickname
		if label == "" {
			label = playFabID
		}
		if err := m.sendUnicodeRCONMessage(
			"[Admin warning for " + label + "] " + message,
		); err != nil {
			return PlayerDetail{}, fmt.Errorf("%w: warning failed: %v", errPlayerRestrictionSync, err)
		}
	default:
		return PlayerDetail{}, fmt.Errorf("%w: unsupported player action", errPlayerInvalid)
	}
	return m.playerDetail(playFabID)
}

func (m *Manager) addPlayerComment(
	playFabID string,
	author string,
	body string,
) (PlayerDetail, error) {
	if !validMordhauPlayerID(playFabID) {
		return PlayerDetail{}, errPlayerInvalid
	}
	normalized, err := normalizePlayerComment(body)
	if err != nil {
		return PlayerDetail{}, err
	}
	id, err := randomID()
	if err != nil {
		return PlayerDetail{}, err
	}
	comment := PlayerComment{
		ID:        id,
		Author:    safeAuditAccount(author),
		Body:      normalized,
		CreatedAt: time.Now(),
	}
	if !validPlayerComment(comment) {
		return PlayerDetail{}, errPlayerCommentInvalid
	}

	m.playersMu.Lock()
	index := playerRecordIndex(&m.playerHistory, playFabID)
	if index < 0 {
		m.playersMu.Unlock()
		return PlayerDetail{}, errPlayerNotFound
	}
	player := &m.playerHistory.Players[index]
	if len(player.Comments) >= playerMaximumComments {
		m.playersMu.Unlock()
		return PlayerDetail{}, errPlayerCommentLimit
	}
	player.Comments = append(player.Comments, comment)
	m.playerHistory.Revision++
	if err := m.savePlayerHistoryLocked(); err != nil {
		m.playersMu.Unlock()
		return PlayerDetail{}, err
	}
	record := *player
	m.playersMu.Unlock()
	return playerDetail(record, time.Now()), nil
}
