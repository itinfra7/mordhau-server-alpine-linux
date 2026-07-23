package manager

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

const (
	rconResponseValue = 0
	rconExecCommand   = 2
	rconAuthResponse  = 2
	rconAuth          = 3

	rconListenAllCommand     = "listen allon"
	rconListenAllSuccess     = "Now listening to all broadcast channels"
	rconListenCustomCommand  = "listen custom"
	rconListenCustomSuccess  = "Now listening to custom broadcasts"
	rconInvalidBroadcast     = "Invalid broadcast option!"
	rconBroadcastOptionsHelp = "Valid options include allon, alloff, chat, login, matchstate, killfeed, scorefeed, custom, and punishment"

	unicodeBridgePayloadPrefix = "unicode.say "
	unicodeBridgeCommandPrefix = "string " + unicodeBridgePayloadPrefix
	unicodeBridgeAcknowledged  = "unicode.say ok"
	unicodeBridgeFilePrefix    = "mordhau-unicode-bridge-"
	unicodeBridgeFileExtension = ".utf8"
	unicodeBridgeTokenLength   = 24
	unicodeBridgeSpoolDir      = rootDir + "/Mordhau/Saved/PlayerFiles"
	unicodeMessageMaxRunes     = 512
	unicodeMessageMaxBytes     = 2048
)

var errInvalidUnicodeMessage = errors.New("invalid Unicode server message")

type rconPacket struct {
	ID   int32
	Type int32
	Body []byte
}

type rconSettings struct {
	Password string `json:"password"`
	Port     int    `json:"port"`
}

func (settings rconSettings) valid() bool {
	return settings.Password != "" && settings.Port >= 1 && settings.Port <= 65535
}

func sameRCONSettings(left, right rconSettings) bool {
	return left.Password == right.Password && left.Port == right.Port
}

func rconCandidates(current, cached *rconSettings) []rconSettings {
	candidates := make([]rconSettings, 0, 2)
	if current != nil && current.valid() {
		candidates = append(candidates, *current)
	}
	if cached != nil && cached.valid() &&
		(current == nil || !sameRCONSettings(*current, *cached)) {
		candidates = append(candidates, *cached)
	}
	return candidates
}

func (m *Manager) setRCONState(connected bool, status string) {
	m.rconMu.Lock()
	m.rconConnected = connected
	m.rconStatus = status
	m.rconMu.Unlock()
}

func (m *Manager) addRCONEvent(kind, text string) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\x00", ""))
	if text == "" {
		return
	}
	m.rconMu.Lock()
	m.rconSequence++
	m.rconEvents = append(m.rconEvents, RCONEvent{
		Sequence: m.rconSequence,
		Time:     time.Now(),
		Text:     text,
		Kind:     kind,
	})
	if len(m.rconEvents) > 1000 {
		m.rconEvents = append([]RCONEvent(nil), m.rconEvents[len(m.rconEvents)-800:]...)
	}
	m.rconMu.Unlock()
}

func filteredRCONLines(text string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\x00", ""))
		if line == "" || line == rconBroadcastOptionsHelp {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *Manager) addRCONText(text string) {
	for _, line := range filteredRCONLines(text) {
		if isRCONChatLine(line) {
			continue
		}
		m.addRCONEvent("rcon", line)
	}
}

func (m *Manager) rconLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if !serverRunning() {
			m.setRCONState(false, "waiting for server")
			if !waitContext(ctx, 2*time.Second) {
				return
			}
			continue
		}

		current, currentErr := activeRCONSettings()
		var currentSettings *rconSettings
		if currentErr == nil {
			currentSettings = &current
		}

		cachedSettings, cachedErr := loadLastRCONSettings()
		if cachedErr != nil && !errors.Is(cachedErr, os.ErrNotExist) {
			cachedSettings = nil
		}

		candidates := rconCandidates(currentSettings, cachedSettings)
		if len(candidates) == 0 {
			status := "RCON settings are unavailable"
			if currentErr != nil {
				status = currentErr.Error()
			}
			m.setRCONState(false, status)
			if !waitContext(ctx, 5*time.Second) {
				return
			}
			continue
		}

		var connection net.Conn
		var connectedSettings rconSettings
		for _, candidate := range candidates {
			address := net.JoinHostPort("127.0.0.1", strconv.Itoa(candidate.Port))
			m.setRCONState(false, "connecting to "+address)
			candidateConnection, err := net.DialTimeout("tcp", address, 5*time.Second)
			if err != nil {
				continue
			}

			err = authenticateRCON(candidateConnection, candidate.Password)
			if err == nil {
				err = m.enableAllRCONBroadcasts(candidateConnection)
			}
			if err != nil {
				_ = candidateConnection.Close()
				continue
			}

			connection = candidateConnection
			connectedSettings = candidate
			break
		}

		if connection == nil {
			m.setRCONState(false, "RCON unavailable, authentication failed, or broadcast subscription failed; retrying")
			if !waitContext(ctx, 5*time.Second) {
				return
			}
			continue
		}

		if err := saveLastRCONSettings(connectedSettings); err != nil {
			m.addRCONEvent("system", "RCON connected, but its reconnect state could not be saved")
		}

		usingCurrent := currentSettings != nil &&
			sameRCONSettings(connectedSettings, *currentSettings)
		if usingCurrent {
			m.setRCONState(true, "connected; listening to all broadcasts")
			m.addRCONEvent("system", "RCON connected; all broadcasts enabled")
		} else {
			m.setRCONState(true, "connected with previous running-server settings; saved changes apply after restart")
			m.addRCONEvent("system", "RCON reconnected with the running server's previous settings; all broadcasts enabled")
		}

		err := m.consumeRCON(ctx, connection)
		_ = connection.Close()
		m.setRCONState(false, "RCON disconnected; retrying")
		if ctx.Err() == nil && serverRunning() {
			if err != nil && !errors.Is(err, io.EOF) {
				m.addRCONEvent("system", "RCON connection closed: "+err.Error())
			}
			if !waitContext(ctx, 3*time.Second) {
				return
			}
		}
	}
}

func loadLastRCONSettings() (*rconSettings, error) {
	var settings rconSettings
	if err := readJSON(rconStatePath, &settings); err != nil {
		return nil, err
	}
	if !settings.valid() {
		return nil, errors.New("saved RCON settings are invalid")
	}
	return &settings, nil
}

func saveLastRCONSettings(settings rconSettings) error {
	if !settings.valid() {
		return errors.New("refusing to save invalid RCON settings")
	}
	return writeJSONAtomic(rconStatePath, settings, 0600)
}

func activeRCONSettings() (rconSettings, error) {
	data, err := os.ReadFile(configPath("Game.ini", false))
	if err != nil {
		return rconSettings{}, fmt.Errorf("read active RCON settings: %w", err)
	}
	const section = "/Script/Mordhau.MordhauGameSession"
	password, ok := iniValue(data, section, "RconPassword")
	if !ok || password == "" {
		return rconSettings{}, errors.New("RconPassword is not configured")
	}
	return rconSettings{Password: password, Port: savedServerPorts().RCON}, nil
}

func validateUnicodeMessage(message string) error {
	if !utf8.ValidString(message) {
		return fmt.Errorf("%w: message is not valid UTF-8", errInvalidUnicodeMessage)
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("%w: message cannot be empty", errInvalidUnicodeMessage)
	}
	if len(message) > unicodeMessageMaxBytes {
		return fmt.Errorf(
			"%w: message exceeds %d UTF-8 bytes",
			errInvalidUnicodeMessage,
			unicodeMessageMaxBytes,
		)
	}
	if utf8.RuneCountInString(message) > unicodeMessageMaxRunes {
		return fmt.Errorf(
			"%w: message exceeds %d characters",
			errInvalidUnicodeMessage,
			unicodeMessageMaxRunes,
		)
	}
	for _, character := range message {
		if unicode.IsControl(character) {
			return fmt.Errorf(
				"%w: control characters are not allowed",
				errInvalidUnicodeMessage,
			)
		}
	}
	return nil
}

func validUnicodeBridgeToken(token string) bool {
	if len(token) != unicodeBridgeTokenLength {
		return false
	}
	for _, character := range token {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func unicodeRCONCommand(token string) ([]byte, error) {
	if !validUnicodeBridgeToken(token) {
		return nil, errors.New("invalid Unicode bridge file token")
	}
	return []byte(unicodeBridgeCommandPrefix + token), nil
}

func unicodeBridgeFilename(token string) (string, error) {
	if !validUnicodeBridgeToken(token) {
		return "", errors.New("invalid Unicode bridge file token")
	}
	return unicodeBridgeFilePrefix + token + unicodeBridgeFileExtension, nil
}

func isManagedUnicodeBridgeFilename(name string) bool {
	if !strings.HasPrefix(name, unicodeBridgeFilePrefix) ||
		!strings.HasSuffix(name, unicodeBridgeFileExtension) {
		return false
	}
	token := strings.TrimSuffix(
		strings.TrimPrefix(name, unicodeBridgeFilePrefix),
		unicodeBridgeFileExtension,
	)
	return validUnicodeBridgeToken(token)
}

func cleanupUnicodeBridgeSpoolAt(directory string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Unicode bridge spool directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !isManagedUnicodeBridgeFilename(entry.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale Unicode bridge spool file: %w", err)
		}
	}
	return nil
}

func stageUnicodeMessageAt(directory, message string) (string, string, error) {
	if err := validateUnicodeMessage(message); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", "", fmt.Errorf("create Unicode bridge spool directory: %w", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return "", "", fmt.Errorf("secure Unicode bridge spool directory: %w", err)
	}

	for attempt := 0; attempt < 8; attempt++ {
		token, err := randomString("0123456789", unicodeBridgeTokenLength)
		if err != nil {
			return "", "", fmt.Errorf("generate Unicode bridge file token: %w", err)
		}
		name, err := unicodeBridgeFilename(token)
		if err != nil {
			return "", "", err
		}
		path := filepath.Join(directory, name)
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("check Unicode bridge spool file: %w", err)
		}
		if err := writeFileAtomic(path, []byte(message), 0600); err != nil {
			return "", "", fmt.Errorf("stage Unicode bridge message: %w", err)
		}
		return token, path, nil
	}
	return "", "", errors.New("could not allocate a unique Unicode bridge file token")
}

func currentRCONCandidates() []rconSettings {
	current, currentErr := activeRCONSettings()
	var currentSettings *rconSettings
	if currentErr == nil {
		currentSettings = &current
	}

	cached, cachedErr := loadLastRCONSettings()
	if cachedErr != nil {
		cached = nil
	}
	return rconCandidates(currentSettings, cached)
}

func executeRCONCommand(connection net.Conn, password string, command []byte) error {
	if err := authenticateRCON(connection, password); err != nil {
		return err
	}
	if err := enableCustomRCONBroadcasts(connection); err != nil {
		return err
	}
	if err := connection.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return err
	}
	if err := writeRCONPacket(connection, rconPacket{
		ID:   103,
		Type: rconExecCommand,
		Body: command,
	}); err != nil {
		return err
	}
	for attempts := 0; attempts < 8; attempts++ {
		packet, err := readRCONPacket(connection)
		if err != nil {
			return err
		}
		if bytes.Contains(packet.Body, []byte(unicodeBridgeAcknowledged)) {
			return connection.SetDeadline(time.Time{})
		}
	}
	return errors.New("the Unicode server bridge did not acknowledge the message")
}

func enableCustomRCONBroadcasts(connection net.Conn) error {
	if err := connection.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return err
	}
	if err := writeRCONPacket(connection, rconPacket{
		ID:   102,
		Type: rconExecCommand,
		Body: []byte(rconListenCustomCommand),
	}); err != nil {
		return err
	}
	for attempts := 0; attempts < 8; attempts++ {
		packet, err := readRCONPacket(connection)
		if err != nil {
			return err
		}
		for _, line := range filteredRCONLines(string(packet.Body)) {
			switch line {
			case rconListenCustomSuccess:
				return connection.SetDeadline(time.Time{})
			case rconInvalidBroadcast:
				return errors.New("RCON rejected the custom-broadcast subscription command")
			}
		}
	}
	return errors.New("RCON did not confirm the custom-broadcast subscription")
}

func (m *Manager) sendUnicodeRCONMessage(message string) error {
	token, stagedPath, err := stageUnicodeMessageAt(unicodeBridgeSpoolDir, message)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(stagedPath)
	}()
	command, err := unicodeRCONCommand(token)
	if err != nil {
		return err
	}
	if !serverRunning() {
		return errors.New("the dedicated server is not running")
	}

	candidates := currentRCONCandidates()
	if len(candidates) == 0 {
		return errors.New("RCON settings are unavailable")
	}

	var lastErr error
	for _, candidate := range candidates {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(candidate.Port))
		connection, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		err = executeRCONCommand(connection, candidate.Password, command)
		_ = connection.Close()
		if err != nil {
			lastErr = err
			continue
		}
		_ = saveLastRCONSettings(candidate)
		return nil
	}

	if lastErr == nil {
		lastErr = errors.New("no RCON connection candidate succeeded")
	}
	return fmt.Errorf("send Unicode message over RCON: %w", lastErr)
}

func authenticateRCON(connection net.Conn, password string) error {
	if err := connection.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return err
	}
	if err := writeRCONPacket(connection, rconPacket{
		ID:   101,
		Type: rconAuth,
		Body: []byte(password),
	}); err != nil {
		return err
	}
	for attempts := 0; attempts < 4; attempts++ {
		packet, err := readRCONPacket(connection)
		if err != nil {
			return err
		}
		if packet.ID == -1 {
			return errors.New("RCON rejected credentials")
		}
		if packet.ID == 101 && packet.Type == rconAuthResponse {
			return connection.SetDeadline(time.Time{})
		}
	}
	return errors.New("RCON did not return an authentication response")
}

func (m *Manager) enableAllRCONBroadcasts(connection net.Conn) error {
	if err := connection.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return err
	}
	if err := writeRCONPacket(connection, rconPacket{
		ID:   102,
		Type: rconExecCommand,
		Body: []byte(rconListenAllCommand),
	}); err != nil {
		return err
	}
	for attempts := 0; attempts < 8; attempts++ {
		packet, err := readRCONPacket(connection)
		if err != nil {
			return err
		}
		if packet.Type != rconResponseValue && len(packet.Body) == 0 {
			continue
		}
		enabled := false
		text := decodeRCON(packet.Body, m.currentLanguage())
		for _, line := range filteredRCONLines(text) {
			switch line {
			case rconListenAllSuccess:
				enabled = true
			case rconInvalidBroadcast:
				return errors.New("RCON rejected the all-broadcast subscription command")
			default:
				if !isRCONChatLine(line) {
					m.addRCONEvent("rcon", line)
				}
			}
		}
		if enabled {
			return connection.SetDeadline(time.Time{})
		}
	}
	return errors.New("RCON did not confirm the all-broadcast subscription")
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) consumeRCON(ctx context.Context, connection net.Conn) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := connection.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
			return err
		}
		packet, err := readRCONPacket(connection)
		if err != nil {
			return err
		}
		if packet.Type != rconResponseValue && len(packet.Body) == 0 {
			continue
		}
		text := decodeRCON(packet.Body, m.currentLanguage())
		m.addRCONText(text)
	}
}

func writeRCONPacket(writer io.Writer, packet rconPacket) error {
	size := 4 + 4 + len(packet.Body) + 2
	if size > 16<<20 {
		return errors.New("RCON packet is too large")
	}
	buffer := bytes.NewBuffer(make([]byte, 0, size+4))
	_ = binary.Write(buffer, binary.LittleEndian, int32(size))
	_ = binary.Write(buffer, binary.LittleEndian, packet.ID)
	_ = binary.Write(buffer, binary.LittleEndian, packet.Type)
	_, _ = buffer.Write(packet.Body)
	_ = buffer.WriteByte(0)
	_ = buffer.WriteByte(0)
	_, err := io.Copy(writer, buffer)
	return err
}

func readRCONPacket(reader io.Reader) (rconPacket, error) {
	var size int32
	if err := binary.Read(reader, binary.LittleEndian, &size); err != nil {
		return rconPacket{}, err
	}
	if size < 10 || size > 16<<20 {
		return rconPacket{}, fmt.Errorf("invalid RCON packet size: %d", size)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return rconPacket{}, err
	}
	packet := rconPacket{
		ID:   int32(binary.LittleEndian.Uint32(payload[0:4])),
		Type: int32(binary.LittleEndian.Uint32(payload[4:8])),
		Body: append([]byte(nil), payload[8:len(payload)-2]...),
	}
	return packet, nil
}

func decodeRCON(data []byte, language string) string {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if utf8.Valid(data) {
		return string(data)
	}

	var fallback encoding.Encoding
	switch language {
	case "ko":
		fallback = korean.EUCKR
	case "zh-Hans":
		fallback = simplifiedchinese.GB18030
	case "zh-Hant":
		fallback = traditionalchinese.Big5
	case "ru":
		fallback = charmap.Windows1251
	default:
		fallback = charmap.Windows1252
	}
	decoded, _, err := transform.Bytes(fallback.NewDecoder(), data)
	if err == nil && utf8.Valid(decoded) {
		return string(decoded)
	}
	return strings.ToValidUTF8(string(data), "�")
}
