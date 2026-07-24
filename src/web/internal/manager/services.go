package manager

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	webPortPath     = stateDir + "/web-port"
	startMapPath    = stateDir + "/start-map"
	serverPortsPath = stateDir + "/server-ports"
)

type ServerPorts struct {
	Game   int `json:"game"`
	RCON   int `json:"rcon"`
	Beacon int `json:"beacon"`
	Query  int `json:"query"`
}

type ServiceSettings struct {
	MordhauAutomatic bool        `json:"mordhau_automatic"`
	WebAutomatic     bool        `json:"web_automatic"`
	WebPort          int         `json:"web_port"`
	StartMap         string      `json:"start_map"`
	ServerPorts      ServerPorts `json:"server_ports"`
}

func defaultServerPorts() ServerPorts {
	return ServerPorts{
		Game:   7777,
		RCON:   7778,
		Beacon: 15000,
		Query:  27015,
	}
}

func automaticService(name string) bool {
	_, err := os.Lstat(filepath.Join("/etc/runlevels/default", name))
	return err == nil
}

func savedWebPort() int {
	data, err := os.ReadFile(webPortPath)
	if err != nil {
		return 8080
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port < 1 || port > 65535 {
		return 8080
	}
	return port
}

func currentServiceSettings() ServiceSettings {
	return ServiceSettings{
		MordhauAutomatic: automaticService("mordhau-server"),
		WebAutomatic:     automaticService("mordhau-web"),
		WebPort:          savedWebPort(),
		StartMap:         savedStartMap(),
		ServerPorts:      savedServerPorts(),
	}
}

func setServiceAutomatic(service string, automatic bool) error {
	var openRCName string
	switch service {
	case "mordhau":
		openRCName = "mordhau-server"
	case "web":
		openRCName = "mordhau-web"
	default:
		return errors.New("unsupported service")
	}
	if automaticService(openRCName) == automatic {
		return nil
	}
	action := "del"
	if automatic {
		action = "add"
	}
	command := exec.Command("/sbin/rc-update", action, openRCName, "default")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("rc-update failed: %s", message)
	}
	return nil
}

func setSavedWebPort(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if err := validateServerPortsForWeb(savedServerPorts(), port); err != nil {
		return err
	}
	return writeFileAtomic(webPortPath, []byte(strconv.Itoa(port)+"\n"), 0600)
}

func validateServerPorts(ports ServerPorts) error {
	values := []struct {
		name string
		port int
	}{
		{"game", ports.Game},
		{"RCON", ports.RCON},
		{"beacon", ports.Beacon},
		{"query", ports.Query},
	}
	seen := make(map[int]string, len(values))
	for _, value := range values {
		if value.port < 1 || value.port > 65535 {
			return fmt.Errorf("%s port must be between 1 and 65535", value.name)
		}
		if previous, exists := seen[value.port]; exists {
			return fmt.Errorf("%s and %s ports must be different", previous, value.name)
		}
		seen[value.port] = value.name
	}
	return nil
}

func validateServerPortsForWeb(ports ServerPorts, webPort int) error {
	if err := validateServerPorts(ports); err != nil {
		return err
	}
	for name, port := range map[string]int{
		"game": ports.Game, "RCON": ports.RCON, "beacon": ports.Beacon, "query": ports.Query,
	} {
		if port == webPort {
			return fmt.Errorf("%s port must differ from the web service port", name)
		}
	}
	return nil
}

func parseServerPorts(data []byte) (ServerPorts, error) {
	var ports ServerPorts
	seen := make(map[string]bool, 4)
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || seen[key] {
			return ServerPorts{}, errors.New("invalid server ports file")
		}
		port, err := strconv.Atoi(value)
		if err != nil {
			return ServerPorts{}, errors.New("invalid server ports file")
		}
		switch key {
		case "game":
			ports.Game = port
		case "rcon":
			ports.RCON = port
		case "beacon":
			ports.Beacon = port
		case "query":
			ports.Query = port
		default:
			return ServerPorts{}, errors.New("invalid server ports file")
		}
		seen[key] = true
	}
	if len(seen) != 4 {
		return ServerPorts{}, errors.New("invalid server ports file")
	}
	if err := validateServerPorts(ports); err != nil {
		return ServerPorts{}, err
	}
	return ports, nil
}

func formatServerPorts(ports ServerPorts) []byte {
	return []byte(fmt.Sprintf(
		"game=%d\nrcon=%d\nbeacon=%d\nquery=%d\n",
		ports.Game,
		ports.RCON,
		ports.Beacon,
		ports.Query,
	))
}

func savedServerPorts() ServerPorts {
	data, err := os.ReadFile(serverPortsPath)
	if err != nil {
		return defaultServerPorts()
	}
	ports, err := parseServerPorts(data)
	if err != nil {
		return defaultServerPorts()
	}
	return ports
}

func setSavedServerPorts(ports ServerPorts) error {
	if err := validateServerPortsForWeb(ports, savedWebPort()); err != nil {
		return err
	}
	return writeFileAtomic(serverPortsPath, formatServerPorts(ports), 0600)
}

func validateStartMap(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 160 || strings.HasPrefix(value, "-") {
		return errors.New("start map is invalid")
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("_./?=:+-", character) {
			continue
		}
		return errors.New("start map may contain only letters, digits, _, -, ., /, ?, =, :, and +")
	}
	return nil
}

func savedStartMap() string {
	data, err := os.ReadFile(startMapPath)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(data))
	if validateStartMap(value) != nil {
		return ""
	}
	return value
}

func setSavedStartMap(value string) error {
	value = strings.TrimSpace(value)
	if err := validateStartMap(value); err != nil {
		return err
	}
	return writeFileAtomic(startMapPath, []byte(value+"\n"), 0600)
}
