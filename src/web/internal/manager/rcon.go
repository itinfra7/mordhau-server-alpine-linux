package manager

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
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

	rconListenCustomCommand   = "listen custom"
	rconListenCustomSuccess   = "Now listening to custom broadcasts"
	rconInvalidBroadcast      = "Invalid broadcast option!"
	rconBroadcastOptionsHelp  = "Valid options include allon, alloff, chat, login, matchstate, killfeed, scorefeed, custom, and punishment"
	rconConnectedEvent        = "RCON connected; all broadcasts enabled"
	rconPreviousEvent         = "RCON reconnected with the running server's previous settings; all broadcasts enabled"
	rconClosedEventPrefix     = "RCON connection closed:"
	rconAdminCommandPacketID  = 4101
	rconCommandTimeout        = 8 * time.Second
	rconCommandIdleTimeout    = 750 * time.Millisecond
	rconCommandMaxRunes       = 512
	rconCommandMaxBytes       = 2048
	rconCommandMaxOutputBytes = 128 << 10
	rconCommandMaxOutputLines = 398

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

var (
	errInvalidRCONCommand    = errors.New("invalid RCON command")
	errInvalidUnicodeMessage = errors.New("invalid Unicode server message")
)

type rconPacket struct {
	ID   int32
	Type int32
	Body []byte
}

type rconSettings struct {
	Password string `json:"password"`
	Port     int    `json:"port"`
}

type rconCommandResult struct {
	Lines       []string
	Truncated   bool
	outputBytes int
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

func (m *Manager) addRCONEvent(kind, text string) (RCONEvent, bool) {
	return m.addRCONEventAt(time.Now(), kind, text)
}

func (m *Manager) addRCONEventAt(
	eventTime time.Time,
	kind string,
	text string,
) (RCONEvent, bool) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\x00", ""))
	if text == "" || isRCONTransportStatusEvent(kind, text) {
		return RCONEvent{}, false
	}
	if eventTime.IsZero() {
		eventTime = time.Now()
	}
	m.rconMu.Lock()
	m.rconSequence++
	event := RCONEvent{
		Sequence: m.rconSequence,
		Time:     eventTime,
		Text:     text,
		Kind:     kind,
	}
	m.rconEvents = retainRCONEvent(m.rconEvents, event)
	persistErr := m.appendRCONEventLog(event)
	m.rconMu.Unlock()
	if persistErr != nil {
		log.Printf("append RCON event log: %v", persistErr)
	}
	return event, true
}

func isRCONTransportStatusEvent(kind, text string) bool {
	if kind != "system" {
		return false
	}
	return text == rconConnectedEvent ||
		text == rconPreviousEvent ||
		strings.HasPrefix(text, rconClosedEventPrefix)
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

func normalizeRCONCommand(command string) (string, error) {
	if !utf8.ValidString(command) {
		return "", fmt.Errorf("%w: command is not valid UTF-8", errInvalidRCONCommand)
	}
	for _, character := range command {
		if unicode.IsControl(character) {
			return "", fmt.Errorf(
				"%w: control characters are not allowed",
				errInvalidRCONCommand,
			)
		}
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("%w: command cannot be empty", errInvalidRCONCommand)
	}
	if len(command) > rconCommandMaxBytes {
		return "", fmt.Errorf(
			"%w: command exceeds %d UTF-8 bytes",
			errInvalidRCONCommand,
			rconCommandMaxBytes,
		)
	}
	if utf8.RuneCountInString(command) > rconCommandMaxRunes {
		return "", fmt.Errorf(
			"%w: command exceeds %d characters",
			errInvalidRCONCommand,
			rconCommandMaxRunes,
		)
	}
	return command, nil
}

func (result *rconCommandResult) appendText(text string) {
	if result.Truncated {
		return
	}
	for _, line := range filteredRCONLines(text) {
		if len(result.Lines) >= rconCommandMaxOutputLines {
			result.Truncated = true
			return
		}
		remaining := rconCommandMaxOutputBytes - result.outputBytes
		if remaining <= 0 {
			result.Truncated = true
			return
		}
		if len(line) > remaining {
			line = strings.ToValidUTF8(line[:remaining], "")
			result.Truncated = true
		}
		if line != "" {
			result.Lines = append(result.Lines, line)
			result.outputBytes += len(line)
		}
		if result.Truncated {
			return
		}
	}
}

func executeRCONAdminCommand(
	connection net.Conn,
	password string,
	command string,
	language string,
) (rconCommandResult, bool, error) {
	var result rconCommandResult
	if err := authenticateRCON(connection, password); err != nil {
		return result, false, err
	}

	responseDeadline := time.Now().Add(rconCommandTimeout)
	if err := connection.SetDeadline(responseDeadline); err != nil {
		return result, false, err
	}
	defer func() {
		_ = connection.SetDeadline(time.Time{})
	}()
	commandSent := true
	if err := writeRCONPacket(connection, rconPacket{
		ID:   rconAdminCommandPacketID,
		Type: rconExecCommand,
		Body: []byte(command),
	}); err != nil {
		return result, commandSent, err
	}

	receivedResponse := false
	for {
		packet, err := readRCONPacket(connection)
		if err != nil {
			var networkError net.Error
			responseFinished := receivedResponse &&
				(errors.Is(err, io.EOF) ||
					(errors.As(err, &networkError) && networkError.Timeout()))
			if responseFinished {
				return result, commandSent, nil
			}
			if errors.As(err, &networkError) && networkError.Timeout() {
				return result, commandSent, errors.New("timed out waiting for the RCON command response")
			}
			return result, commandSent, err
		}
		if packet.ID != rconAdminCommandPacketID {
			continue
		}

		receivedResponse = true
		result.appendText(decodeRCON(packet.Body, language))
		if result.Truncated {
			return result, commandSent, nil
		}

		nextDeadline := time.Now().Add(rconCommandIdleTimeout)
		if nextDeadline.After(responseDeadline) {
			nextDeadline = responseDeadline
		}
		if err := connection.SetReadDeadline(nextDeadline); err != nil {
			return result, commandSent, err
		}
	}
}

func (m *Manager) runRCONCommand(command string) (rconCommandResult, error) {
	var result rconCommandResult
	if !serverRunning() {
		return result, errors.New("the dedicated server is not running")
	}

	candidates := currentRCONCandidates()
	if len(candidates) == 0 {
		return result, errors.New("RCON settings are unavailable")
	}

	var lastErr error
	for _, candidate := range candidates {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(candidate.Port))
		connection, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		var commandSent bool
		result, commandSent, err = executeRCONAdminCommand(
			connection,
			candidate.Password,
			command,
			m.currentLanguage(),
		)
		_ = connection.Close()
		if err != nil {
			lastErr = err
			if commandSent {
				return result, fmt.Errorf("execute RCON command: %w", lastErr)
			}
			continue
		}
		_ = saveLastRCONSettings(candidate)
		return result, nil
	}

	if lastErr == nil {
		lastErr = errors.New("no RCON connection candidate succeeded")
	}
	return result, fmt.Errorf("execute RCON command: %w", lastErr)
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
			_ = connection.SetDeadline(time.Time{})
			return nil
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
