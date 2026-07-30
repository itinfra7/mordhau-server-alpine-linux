package manager

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	fleetSettingsVersion = 1
	fleetProtocolVersion = 1

	FleetRoleStandalone = "standalone"
	FleetRoleController = "controller"
	FleetRoleManaged    = "managed"

	fleetDefaultListenAddress = "0.0.0.0:8091"
	fleetConnectionKeyPrefix  = "mfc1."
	fleetAliasMaxRunes        = 32
)

type FleetSyncPolicy struct {
	AllChat         bool `json:"all_chat"`
	TeamChat        bool `json:"team_chat"`
	WebSAY          bool `json:"web_say"`
	RCONSAY         bool `json:"rcon_say"`
	PlayerLifecycle bool `json:"player_lifecycle"`
}

func (policy FleetSyncPolicy) enabled(category string) bool {
	switch category {
	case FleetEventAllChat:
		return policy.AllChat
	case FleetEventTeamChat:
		return policy.TeamChat
	case FleetEventWebSAY:
		return policy.WebSAY
	case FleetEventRCONSAY:
		return policy.RCONSAY
	case FleetEventPlayerLogin, FleetEventPlayerLogout:
		return policy.PlayerLifecycle
	default:
		return false
	}
}

type fleetControllerPeer struct {
	NodeID    string `json:"node_id"`
	Address   string `json:"address"`
	PublicKey string `json:"public_key"`
}

type fleetManagedPeer struct {
	NodeID    string          `json:"node_id"`
	Alias     string          `json:"alias"`
	Address   string          `json:"address"`
	PublicKey string          `json:"public_key"`
	Sync      FleetSyncPolicy `json:"sync"`
}

type fleetSettingsFile struct {
	Version       int                  `json:"version"`
	Role          string               `json:"role"`
	Alias         string               `json:"alias"`
	ListenAddress string               `json:"listen_address"`
	LocalSync     FleetSyncPolicy      `json:"local_sync"`
	Controller    *fleetControllerPeer `json:"controller,omitempty"`
	Managed       []fleetManagedPeer   `json:"managed,omitempty"`
}

type fleetConnectionKeyPayload struct {
	Version   int    `json:"version"`
	NodeID    string `json:"node_id"`
	PublicKey string `json:"public_key"`
}

type fleetIdentity struct {
	NodeID    string
	PublicKey []byte
	TLS       tls.Certificate
}

type FleetNodeStatus struct {
	Connected      bool      `json:"connected"`
	LastSeen       time.Time `json:"last_seen,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	ManagerVersion string    `json:"manager_version,omitempty"`
	Protocol       int       `json:"protocol,omitempty"`
	ServerRunning  bool      `json:"server_running"`
	PlayerCount    int       `json:"player_count"`
	Metrics        Metrics   `json:"metrics"`
}

type FleetNodeView struct {
	NodeID  string          `json:"node_id"`
	Alias   string          `json:"alias"`
	Address string          `json:"address,omitempty"`
	Local   bool            `json:"local"`
	Sync    FleetSyncPolicy `json:"sync"`
	Status  FleetNodeStatus `json:"status"`
}

type FleetControllerView struct {
	NodeID  string `json:"node_id"`
	Address string `json:"address"`
}

type FleetView struct {
	Version        int                  `json:"version"`
	Protocol       int                  `json:"protocol"`
	Role           string               `json:"role"`
	Alias          string               `json:"alias"`
	NodeID         string               `json:"node_id,omitempty"`
	IdentityReady  bool                 `json:"identity_ready"`
	ConnectionKey  string               `json:"connection_key,omitempty"`
	ListenAddress  string               `json:"listen_address"`
	LocalSync      FleetSyncPolicy      `json:"local_sync"`
	Controller     *FleetControllerView `json:"controller,omitempty"`
	Nodes          []FleetNodeView      `json:"nodes"`
	TeamChatNotice string               `json:"team_chat_notice"`
	GeneratedAt    time.Time            `json:"generated_at"`
}

func validFleetRole(role string) bool {
	switch role {
	case FleetRoleStandalone, FleetRoleController, FleetRoleManaged:
		return true
	default:
		return false
	}
}

func normalizeFleetAlias(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("server display name must be valid UTF-8")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("server display name cannot be empty")
	}
	if utf8.RuneCountInString(value) > fleetAliasMaxRunes {
		return "", fmt.Errorf(
			"server display name must not exceed %d characters",
			fleetAliasMaxRunes,
		)
	}
	for _, character := range value {
		if unicode.IsControl(character) ||
			unicode.In(character, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", errors.New(
				"server display name must not contain control or formatting characters",
			)
		}
	}
	return value, nil
}

func defaultFleetAlias() string {
	hostname, err := os.Hostname()
	if err == nil {
		if alias, aliasErr := normalizeFleetAlias(hostname); aliasErr == nil {
			return alias
		}
	}
	return "MORDHAU"
}

func normalizeFleetListenAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return "", errors.New("fleet listen address must use IP:port")
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil || address.Zone() != "" {
		return "", errors.New("fleet listen address must contain a valid IPv4 or IPv6 address")
	}
	address = address.Unmap()
	if address.IsMulticast() {
		return "", errors.New("fleet listen address must not be multicast")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("fleet listen port must be between 1 and 65535")
	}
	return net.JoinHostPort(address.String(), strconv.Itoa(port)), nil
}

func normalizeFleetControllerAddress(value string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || address.Zone() != "" {
		return "", errors.New("controller address must be one IPv4 or IPv6 address")
	}
	address = address.Unmap()
	if address.IsUnspecified() || address.IsMulticast() {
		return "", errors.New("controller address must be a usable unicast address")
	}
	return address.String(), nil
}

func normalizeFleetPeerAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return "", errors.New("managed server address must use IP:port")
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil || address.Zone() != "" {
		return "", errors.New("managed server address must contain a valid IPv4 or IPv6 address")
	}
	address = address.Unmap()
	if address.IsUnspecified() || address.IsMulticast() {
		return "", errors.New("managed server address must be a usable unicast address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("managed server port must be between 1 and 65535")
	}
	return net.JoinHostPort(address.String(), strconv.Itoa(port)), nil
}

func fleetNodeID(publicKeyDER []byte) string {
	sum := sha256.Sum256(publicKeyDER)
	return strings.ToLower(
		base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:20]),
	)
}

func validFleetNodeID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < 'a' || character > 'z') &&
			(character < '2' || character > '7') {
			return false
		}
	}
	return true
}

func parseFleetConnectionKey(value string) (fleetConnectionKeyPayload, error) {
	value = strings.TrimSpace(value)
	if len(value) > 4096 {
		return fleetConnectionKeyPayload{}, errors.New("fleet connection key is too large")
	}
	if !strings.HasPrefix(value, fleetConnectionKeyPrefix) {
		return fleetConnectionKeyPayload{}, errors.New("invalid fleet connection key prefix")
	}
	data, err := base64.RawURLEncoding.DecodeString(
		strings.TrimPrefix(value, fleetConnectionKeyPrefix),
	)
	if err != nil {
		return fleetConnectionKeyPayload{}, errors.New("invalid fleet connection key encoding")
	}
	var payload fleetConnectionKeyPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fleetConnectionKeyPayload{}, errors.New("invalid fleet connection key")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fleetConnectionKeyPayload{}, errors.New("invalid fleet connection key")
	}
	if payload.Version != fleetProtocolVersion {
		return fleetConnectionKeyPayload{}, errors.New("unsupported fleet connection key version")
	}
	publicKeyDER, err := base64.RawStdEncoding.DecodeString(payload.PublicKey)
	if err != nil {
		return fleetConnectionKeyPayload{}, errors.New("invalid fleet public key")
	}
	publicKey, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		return fleetConnectionKeyPayload{}, errors.New("invalid fleet public key")
	}
	if _, ok := publicKey.(ed25519.PublicKey); !ok {
		return fleetConnectionKeyPayload{}, errors.New("fleet public key must use Ed25519")
	}
	if !validFleetNodeID(payload.NodeID) ||
		fleetNodeID(publicKeyDER) != payload.NodeID {
		return fleetConnectionKeyPayload{}, errors.New("fleet connection key node ID mismatch")
	}
	return payload, nil
}

func encodeFleetConnectionKey(identity fleetIdentity) (string, error) {
	payload := fleetConnectionKeyPayload{
		Version:   fleetProtocolVersion,
		NodeID:    identity.NodeID,
		PublicKey: base64.RawStdEncoding.EncodeToString(identity.PublicKey),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return fleetConnectionKeyPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func writeFleetIdentity(
	keyPath string,
	certificatePath string,
	privateKey ed25519.PrivateKey,
	certificateDER []byte,
) error {
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})
	certificatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificateDER,
	})
	if err := writeFileAtomic(keyPath, keyPEM, 0600); err != nil {
		return err
	}
	if err := writeFileAtomic(certificatePath, certificatePEM, 0600); err != nil {
		return err
	}
	return nil
}

func createFleetIdentity(keyPath, certificatePath string, now time.Time) error {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate fleet identity: %w", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("marshal fleet public key: %w", err)
	}
	nodeID := fleetNodeID(publicKeyDER)
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return fmt.Errorf("generate fleet certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "mordhau-fleet-" + nodeID,
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		publicKey,
		privateKey,
	)
	if err != nil {
		return fmt.Errorf("create fleet certificate: %w", err)
	}
	return writeFleetIdentity(keyPath, certificatePath, privateKey, certificateDER)
}

func loadFleetIdentityAt(keyPath, certificatePath string) (fleetIdentity, error) {
	certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	if err != nil {
		return fleetIdentity{}, err
	}
	if len(certificate.Certificate) != 1 {
		return fleetIdentity{}, errors.New("fleet identity must contain exactly one certificate")
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return fleetIdentity{}, err
	}
	publicKeyDER := parsed.RawSubjectPublicKeyInfo
	publicKey, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		return fleetIdentity{}, err
	}
	if _, ok := publicKey.(ed25519.PublicKey); !ok {
		return fleetIdentity{}, errors.New("fleet identity does not use Ed25519")
	}
	now := time.Now()
	if now.Before(parsed.NotBefore) || now.After(parsed.NotAfter) {
		return fleetIdentity{}, errors.New(
			"fleet identity certificate is outside its validity period",
		)
	}
	return fleetIdentity{
		NodeID:    fleetNodeID(publicKeyDER),
		PublicKey: append([]byte(nil), publicKeyDER...),
		TLS:       certificate,
	}, nil
}

func (m *Manager) loadFleetIdentity() (fleetIdentity, error) {
	m.fleetIdentityMu.Lock()
	defer m.fleetIdentityMu.Unlock()
	return m.loadFleetIdentityLocked()
}

func (m *Manager) loadFleetIdentityLocked() (fleetIdentity, error) {
	if m.fleetIdentityCache != nil {
		return *m.fleetIdentityCache, nil
	}
	identity, err := loadFleetIdentityAt(
		m.fleetIdentityKeyFile,
		m.fleetIdentityCertFile,
	)
	if err == nil {
		for _, path := range []string{
			m.fleetIdentityKeyFile,
			m.fleetIdentityCertFile,
		} {
			info, statErr := os.Lstat(path)
			if statErr != nil {
				return fleetIdentity{}, statErr
			}
			if !info.Mode().IsRegular() {
				return fleetIdentity{}, errors.New("fleet identity path is not a regular file")
			}
			if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
				return fleetIdentity{}, chmodErr
			}
		}
		identityCopy := identity
		m.fleetIdentityCache = &identityCopy
	}
	return identity, err
}

func (m *Manager) ensureFleetIdentity() (fleetIdentity, error) {
	m.fleetIdentityMu.Lock()
	defer m.fleetIdentityMu.Unlock()
	identity, err := m.loadFleetIdentityLocked()
	if err == nil {
		return identity, nil
	}
	keyInfo, keyErr := os.Lstat(m.fleetIdentityKeyFile)
	certInfo, certErr := os.Lstat(m.fleetIdentityCertFile)
	keyMissing := errors.Is(keyErr, os.ErrNotExist)
	certMissing := errors.Is(certErr, os.ErrNotExist)
	if !keyMissing || !certMissing {
		if (keyErr == nil && !keyInfo.Mode().IsRegular()) ||
			(certErr == nil && !certInfo.Mode().IsRegular()) {
			return fleetIdentity{}, errors.New("fleet identity path is not a regular file")
		}
		return fleetIdentity{}, fmt.Errorf("load fleet identity: %w", err)
	}
	if err := createFleetIdentity(
		m.fleetIdentityKeyFile,
		m.fleetIdentityCertFile,
		m.fleetNow(),
	); err != nil {
		return fleetIdentity{}, err
	}
	return m.loadFleetIdentityLocked()
}

func validFleetPublicKey(encoded string, nodeID string) bool {
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || fleetNodeID(data) != nodeID {
		return false
	}
	publicKey, err := x509.ParsePKIXPublicKey(data)
	if err != nil {
		return false
	}
	_, ok := publicKey.(ed25519.PublicKey)
	return ok
}

func validateFleetSettings(settings fleetSettingsFile) error {
	if settings.Version != fleetSettingsVersion {
		return errors.New("unsupported fleet settings version")
	}
	if !validFleetRole(settings.Role) {
		return errors.New("invalid fleet role")
	}
	if _, err := normalizeFleetAlias(settings.Alias); err != nil {
		return err
	}
	if _, err := normalizeFleetListenAddress(settings.ListenAddress); err != nil {
		return err
	}
	switch settings.Role {
	case FleetRoleManaged:
		if settings.Controller == nil {
			return nil
		}
		if _, err := normalizeFleetControllerAddress(settings.Controller.Address); err != nil {
			return err
		}
		if !validFleetPublicKey(settings.Controller.PublicKey, settings.Controller.NodeID) {
			return errors.New("invalid configured controller public key")
		}
	case FleetRoleController:
		seenNode := make(map[string]bool)
		seenAlias := map[string]bool{
			strings.ToLower(settings.Alias): true,
		}
		seenAddress := make(map[string]bool)
		for _, peer := range settings.Managed {
			alias, err := normalizeFleetAlias(peer.Alias)
			if err != nil {
				return err
			}
			address, err := normalizeFleetPeerAddress(peer.Address)
			if err != nil {
				return err
			}
			if !validFleetPublicKey(peer.PublicKey, peer.NodeID) {
				return errors.New("invalid managed server public key")
			}
			aliasKey := strings.ToLower(alias)
			if seenNode[peer.NodeID] || seenAlias[aliasKey] || seenAddress[address] {
				return errors.New("fleet managed servers must have unique IDs, names, and addresses")
			}
			seenNode[peer.NodeID] = true
			seenAlias[aliasKey] = true
			seenAddress[address] = true
		}
	}
	return nil
}

func (m *Manager) loadOrCreateFleetSettings() error {
	path := m.fleetSettingsFile
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if !info.Mode().IsRegular() {
			return errors.New("fleet settings path is not a regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect fleet settings: %w", statErr)
	}
	var settings fleetSettingsFile
	if statErr == nil {
		if err := readJSON(path, &settings); err != nil {
			return fmt.Errorf("load fleet settings: %w", err)
		}
		if err := validateFleetSettings(settings); err != nil {
			return fmt.Errorf("validate fleet settings: %w", err)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
		m.fleetSettings = settings
		return nil
	}
	settings = fleetSettingsFile{
		Version:       fleetSettingsVersion,
		Role:          FleetRoleStandalone,
		Alias:         defaultFleetAlias(),
		ListenAddress: fleetDefaultListenAddress,
	}
	if err := writeJSONAtomic(path, settings, 0600); err != nil {
		return err
	}
	m.fleetSettings = settings
	return nil
}

func copyFleetSettings(settings fleetSettingsFile) fleetSettingsFile {
	copyValue := settings
	if settings.Controller != nil {
		controller := *settings.Controller
		copyValue.Controller = &controller
	}
	copyValue.Managed = append([]fleetManagedPeer(nil), settings.Managed...)
	return copyValue
}

func (m *Manager) currentFleetSettings() fleetSettingsFile {
	m.fleetMu.RLock()
	defer m.fleetMu.RUnlock()
	return copyFleetSettings(m.fleetSettings)
}

func (m *Manager) saveFleetSettings(settings fleetSettingsFile) error {
	if err := validateFleetSettings(settings); err != nil {
		return err
	}
	m.fleetMu.Lock()
	if err := writeJSONAtomic(m.fleetSettingsFile, settings, 0600); err != nil {
		m.fleetMu.Unlock()
		return err
	}
	m.fleetSettings = copyFleetSettings(settings)
	allowedStatuses := make(map[string]bool)
	if settings.Role == FleetRoleController {
		for _, peer := range settings.Managed {
			allowedStatuses[peer.NodeID] = true
		}
	}
	for nodeID := range m.fleetStatuses {
		if !allowedStatuses[nodeID] {
			delete(m.fleetStatuses, nodeID)
		}
	}
	m.fleetMu.Unlock()
	m.signalFleetSupervisor()
	return nil
}

func (m *Manager) signalFleetSupervisor() {
	select {
	case m.fleetWake <- struct{}{}:
	default:
	}
}

func fleetPublicKeyEqual(expected string, certificate *x509.Certificate) bool {
	if certificate == nil {
		return false
	}
	expectedDER, err := base64.RawStdEncoding.DecodeString(expected)
	if err != nil || len(expectedDER) != len(certificate.RawSubjectPublicKeyInfo) {
		return false
	}
	return subtle.ConstantTimeCompare(
		expectedDER,
		certificate.RawSubjectPublicKeyInfo,
	) == 1
}

func (m *Manager) localFleetNodeID() string {
	identity, err := m.loadFleetIdentity()
	if err != nil {
		return ""
	}
	return identity.NodeID
}

func (m *Manager) localFleetStatus() FleetNodeStatus {
	m.metricsMu.RLock()
	metrics := m.metrics
	m.metricsMu.RUnlock()
	status := FleetNodeStatus{
		Connected:      true,
		LastSeen:       m.fleetNow(),
		ManagerVersion: installedFleetManagerVersion(),
		Protocol:       fleetProtocolVersion,
		ServerRunning:  serverRunning(),
		Metrics:        metrics,
	}
	runtime := m.runtimeSummaryView()
	if runtime.Ready {
		status.PlayerCount = runtime.PlayerControllerCount
	}
	return status
}

func (m *Manager) fleetView() FleetView {
	settings := m.currentFleetSettings()
	view := FleetView{
		Version:        fleetSettingsVersion,
		Protocol:       fleetProtocolVersion,
		Role:           settings.Role,
		Alias:          settings.Alias,
		ListenAddress:  settings.ListenAddress,
		LocalSync:      settings.LocalSync,
		TeamChatNotice: "TEAM chat is displayed server-wide on destination servers because teams are not shared across servers.",
		GeneratedAt:    m.fleetNow(),
	}
	if identity, err := m.loadFleetIdentity(); err == nil {
		view.IdentityReady = true
		view.NodeID = identity.NodeID
		view.ConnectionKey, _ = encodeFleetConnectionKey(identity)
	}
	if settings.Controller != nil {
		view.Controller = &FleetControllerView{
			NodeID:  settings.Controller.NodeID,
			Address: settings.Controller.Address,
		}
	}

	view.Nodes = append(view.Nodes, FleetNodeView{
		NodeID: view.NodeID,
		Alias:  settings.Alias,
		Local:  true,
		Sync:   settings.LocalSync,
		Status: m.localFleetStatus(),
	})

	if settings.Role == FleetRoleController {
		m.fleetMu.RLock()
		for _, peer := range settings.Managed {
			status := m.fleetStatuses[peer.NodeID]
			view.Nodes = append(view.Nodes, FleetNodeView{
				NodeID:  peer.NodeID,
				Alias:   peer.Alias,
				Address: peer.Address,
				Sync:    peer.Sync,
				Status:  status,
			})
		}
		m.fleetMu.RUnlock()
	}
	return view
}

func (m *Manager) fleetPeer(nodeID string) (fleetManagedPeer, bool) {
	settings := m.currentFleetSettings()
	if settings.Role != FleetRoleController {
		return fleetManagedPeer{}, false
	}
	for _, peer := range settings.Managed {
		if peer.NodeID == nodeID {
			return peer, true
		}
	}
	return fleetManagedPeer{}, false
}

func (m *Manager) updateFleetStatus(nodeID string, status FleetNodeStatus) {
	status.LastError = safeAuditText(status.LastError, 256)
	m.fleetMu.Lock()
	if m.fleetSettings.Role != FleetRoleController {
		m.fleetMu.Unlock()
		return
	}
	configured := false
	for _, peer := range m.fleetSettings.Managed {
		if peer.NodeID == nodeID {
			configured = true
			break
		}
	}
	if !configured {
		m.fleetMu.Unlock()
		return
	}
	previous, existed := m.fleetStatuses[nodeID]
	m.fleetStatuses[nodeID] = status
	m.fleetMu.Unlock()
	if existed &&
		previous.Connected == status.Connected &&
		(previous.LastError == status.LastError || status.Connected) {
		return
	}
	event := "fleet_node_disconnected"
	if status.Connected {
		event = "fleet_node_connected"
	}
	details := map[string]string{"node_id": nodeID}
	if status.LastError != "" {
		details["error"] = status.LastError
	}
	m.auditActorEvent("system", "local", event, details)
}
