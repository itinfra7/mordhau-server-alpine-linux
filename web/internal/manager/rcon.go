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
	"strconv"
	"strings"
	"time"
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
				err = writeRCONPacket(candidateConnection, rconPacket{
					ID:   102,
					Type: rconExecCommand,
					Body: []byte("listen all"),
				})
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
			m.setRCONState(false, "RCON unavailable or authentication failed; retrying")
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
			m.addRCONEvent("system", "RCON connected; listen all enabled")
		} else {
			m.setRCONState(true, "connected with running-server credentials; Game.ini changes apply after restart")
			m.addRCONEvent("system", "RCON reconnected with the running server's previous credentials; listen all enabled")
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
	portText, ok := iniValue(data, section, "RconPort")
	if !ok {
		return rconSettings{}, errors.New("RconPort is not configured")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return rconSettings{}, errors.New("RconPort is invalid")
	}
	return rconSettings{Password: password, Port: port}, nil
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
		for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			m.addRCONEvent("rcon", line)
		}
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
