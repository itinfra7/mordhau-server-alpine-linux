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

const webPortPath = stateDir + "/web-port"

type ServiceSettings struct {
	MordhauAutomatic bool `json:"mordhau_automatic"`
	WebAutomatic     bool `json:"web_automatic"`
	WebPort          int  `json:"web_port"`
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
	return writeFileAtomic(webPortPath, []byte(strconv.Itoa(port)+"\n"), 0600)
}
