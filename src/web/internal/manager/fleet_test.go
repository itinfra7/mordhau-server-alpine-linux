package manager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func createTestFleetIdentity(t *testing.T, directory string) fleetIdentity {
	t.Helper()
	keyPath := filepath.Join(directory, "identity-key.pem")
	certificatePath := filepath.Join(directory, "identity-cert.pem")
	if err := createFleetIdentity(keyPath, certificatePath, time.Now()); err != nil {
		t.Fatal(err)
	}
	identity, err := loadFleetIdentityAt(keyPath, certificatePath)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func encodedFleetPublicKey(identity fleetIdentity) string {
	return base64.RawStdEncoding.EncodeToString(identity.PublicKey)
}

func TestFleetConnectionKeyRoundTripAndPrivatePermissions(t *testing.T) {
	directory := t.TempDir()
	identity := createTestFleetIdentity(t, directory)
	key, err := encodeFleetConnectionKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := parseFleetConnectionKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if payload.NodeID != identity.NodeID ||
		payload.PublicKey != encodedFleetPublicKey(identity) {
		t.Fatalf("connection key payload mismatch: %+v", payload)
	}
	for _, name := range []string{"identity-key.pem", "identity-cert.pem"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s permissions = %o, want 600", name, info.Mode().Perm())
		}
	}
}

func TestFleetDefaultsToStandaloneWithEverySyncCategoryOff(t *testing.T) {
	directory := t.TempDir()
	manager := &Manager{
		fleetSettingsFile: filepath.Join(directory, "fleet.json"),
	}
	if err := manager.loadOrCreateFleetSettings(); err != nil {
		t.Fatal(err)
	}
	settings := manager.currentFleetSettings()
	if settings.Role != FleetRoleStandalone {
		t.Fatalf("default role = %q, want standalone", settings.Role)
	}
	for _, category := range []string{
		FleetEventAllChat,
		FleetEventTeamChat,
		FleetEventWebSAY,
		FleetEventRCONSAY,
		FleetEventPlayerLogin,
		FleetEventPlayerLogout,
	} {
		if settings.LocalSync.enabled(category) {
			t.Fatalf("default policy enabled %q", category)
		}
	}
	info, err := os.Stat(manager.fleetSettingsFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("fleet settings permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestFleetStatusCannotReturnAfterManagedServerRemoval(t *testing.T) {
	nodeID := "abcdefghijklmnopqrstuvwxyz234567"
	manager := &Manager{
		fleetSettings: fleetSettingsFile{
			Version:       fleetSettingsVersion,
			Role:          FleetRoleController,
			Alias:         "Controller",
			ListenAddress: fleetDefaultListenAddress,
			Managed: []fleetManagedPeer{{
				NodeID:  nodeID,
				Alias:   "Managed",
				Address: "192.0.2.20:8091",
			}},
		},
		fleetStatuses: map[string]FleetNodeStatus{
			nodeID: {Connected: true},
		},
		fleetSettingsFile: filepath.Join(t.TempDir(), "fleet.json"),
	}
	if err := manager.saveFleetSettings(fleetSettingsFile{
		Version:       fleetSettingsVersion,
		Role:          FleetRoleStandalone,
		Alias:         "Standalone",
		ListenAddress: fleetDefaultListenAddress,
	}); err != nil {
		t.Fatal(err)
	}

	manager.updateFleetStatus(nodeID, FleetNodeStatus{Connected: true})
	if _, exists := manager.fleetStatuses[nodeID]; exists {
		t.Fatal("a canceled Managed Server worker restored stale status")
	}
}

func TestFleetConcurrentIdentityGenerationIsStable(t *testing.T) {
	directory := t.TempDir()
	manager := &Manager{
		fleetIdentityKeyFile:  filepath.Join(directory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(directory, "identity-cert.pem"),
		fleetNow:              time.Now,
	}
	const generatorCount = 16
	nodeIDs := make(chan string, generatorCount)
	errors := make(chan error, generatorCount)
	var generators sync.WaitGroup
	generators.Add(generatorCount)
	for index := 0; index < generatorCount; index++ {
		go func() {
			defer generators.Done()
			identity, err := manager.ensureFleetIdentity()
			if err != nil {
				errors <- err
				return
			}
			nodeIDs <- identity.NodeID
		}()
	}
	generators.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	close(nodeIDs)
	var expected string
	for nodeID := range nodeIDs {
		if expected == "" {
			expected = nodeID
		}
		if nodeID != expected {
			t.Fatalf("concurrent identity node ID = %q, want %q", nodeID, expected)
		}
	}
	if expected == "" {
		t.Fatal("no fleet identity was generated")
	}
}

func TestFleetTLSConfigsPinMutualEd25519Identities(t *testing.T) {
	controllerDirectory := filepath.Join(t.TempDir(), "controller")
	managedDirectory := filepath.Join(t.TempDir(), "managed")
	if err := os.MkdirAll(controllerDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(managedDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	controller := createTestFleetIdentity(t, controllerDirectory)
	managed := createTestFleetIdentity(t, managedDirectory)
	controllerCertificate, err := x509.ParseCertificate(controller.TLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	managedCertificate, err := x509.ParseCertificate(managed.TLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	manager := &Manager{}
	serverConfig := manager.managedFleetTLSConfig(fleetSettingsFile{
		Controller: &fleetControllerPeer{
			NodeID:    controller.NodeID,
			PublicKey: encodedFleetPublicKey(controller),
		},
	}, managed)
	if err := serverConfig.VerifyConnection(tlsState(controllerCertificate)); err != nil {
		t.Fatalf("server rejected pinned Controller: %v", err)
	}
	if err := serverConfig.VerifyConnection(tlsState(managedCertificate)); err == nil {
		t.Fatal("server accepted the wrong client identity")
	}

	clientConfig := fleetClientTLSConfig(
		controller,
		encodedFleetPublicKey(managed),
	)
	if err := clientConfig.VerifyConnection(tlsState(managedCertificate)); err != nil {
		t.Fatalf("Controller rejected pinned Managed Server: %v", err)
	}
	if err := clientConfig.VerifyConnection(tlsState(controllerCertificate)); err == nil {
		t.Fatal("Controller accepted the wrong server identity")
	}
	if clientConfig.MinVersion != clientConfig.MaxVersion ||
		clientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatal("fleet client is not restricted to TLS 1.3")
	}
}

func TestFleetHTTPClientReuseAndPruning(t *testing.T) {
	directory := t.TempDir()
	identity := createTestFleetIdentity(t, directory)
	manager := &Manager{
		fleetIdentityKeyFile:  filepath.Join(directory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(directory, "identity-cert.pem"),
		fleetClients:          make(map[string]fleetCachedHTTPClient),
	}
	peer := fleetManagedPeer{
		NodeID:    "managed-node",
		Address:   "127.0.0.1:8091",
		PublicKey: encodedFleetPublicKey(identity),
	}
	first, err := manager.fleetHTTPClient(peer)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.fleetHTTPClient(peer)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("unchanged Managed Server transport was not reused")
	}
	peer.Address = "127.0.0.1:8092"
	third, err := manager.fleetHTTPClient(peer)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("changed Managed Server transport reused the stale client")
	}
	manager.pruneFleetHTTPClients(fleetSettingsFile{
		Role: FleetRoleStandalone,
	})
	if len(manager.fleetClients) != 0 {
		t.Fatal("Standalone mode retained fleet HTTP clients")
	}
}

func TestFleetInternalAPIRequiresPinnedIdentityAndExpectedSourceIP(t *testing.T) {
	controllerDirectory := filepath.Join(t.TempDir(), "controller")
	managedDirectory := filepath.Join(t.TempDir(), "managed")
	if err := os.MkdirAll(controllerDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(managedDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	controllerIdentity := createTestFleetIdentity(t, controllerDirectory)
	managedIdentity := createTestFleetIdentity(t, managedDirectory)
	settings := fleetSettingsFile{
		Version:       fleetSettingsVersion,
		Role:          FleetRoleManaged,
		Alias:         "Managed",
		ListenAddress: "127.0.0.1:8091",
		Controller: &fleetControllerPeer{
			NodeID:    controllerIdentity.NodeID,
			Address:   "127.0.0.1",
			PublicKey: encodedFleetPublicKey(controllerIdentity),
		},
	}
	managed := &Manager{
		fleetSettings:         settings,
		fleetIdentityKeyFile:  filepath.Join(managedDirectory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(managedDirectory, "identity-cert.pem"),
		fleetNow:              time.Now,
		fleetSubscribers:      make(map[uint64]chan FleetEvent),
		auditPath:             filepath.Join(managedDirectory, "audit.jsonl"),
		rconEvents: []RCONEvent{{
			Sequence: 1,
			Time:     time.Now(),
			Kind:     "login",
			Text: "Login: 2026.07.30-08.17.20: ExamplePlayer " +
				"(1111222233334444) logged in",
		}},
	}
	server := httptest.NewUnstartedServer(managed.fleetInternalHandler())
	server.TLS = managed.managedFleetTLSConfig(settings, managedIdentity)
	server.StartTLS()
	defer server.Close()

	controller := &Manager{
		fleetIdentityKeyFile:  filepath.Join(controllerDirectory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(controllerDirectory, "identity-cert.pem"),
	}
	peer := fleetManagedPeer{
		NodeID:    managedIdentity.NodeID,
		Address:   server.Listener.Addr().String(),
		PublicKey: encodedFleetPublicKey(managedIdentity),
	}
	client, err := controller.fleetHTTPClient(peer)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.Get(fleetPeerURL(peer, "/fleet/v1/hello"))
	if err != nil {
		t.Fatal(err)
	}
	var hello fleetHello
	if err := json.NewDecoder(response.Body).Decode(&hello); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		hello.NodeID != managedIdentity.NodeID ||
		hello.Protocol != fleetProtocolVersion {
		t.Fatalf("unexpected hello response: status=%d hello=%+v", response.StatusCode, hello)
	}

	proxyRequest, err := http.NewRequest(
		http.MethodGet,
		fleetPeerURL(peer, "/fleet/v1/proxy"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	proxyRequest.Header.Set(fleetHeaderActor, "admin")
	proxyRequest.Header.Set(fleetHeaderClientIP, "203.0.113.20")
	proxyRequest.Header.Set(fleetHeaderRequestID, "test-request")
	proxyRequest.Header.Set(fleetHeaderPath, "/api/access")
	proxyRequest.Header.Set("X-Forwarded-For", "198.51.100.99")
	response, err = client.Do(proxyRequest)
	if err != nil {
		t.Fatal(err)
	}
	var accessView struct {
		CurrentIP string `json:"current_ip"`
	}
	if err := json.NewDecoder(response.Body).Decode(&accessView); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		accessView.CurrentIP != "203.0.113.20" {
		t.Fatalf(
			"unexpected proxied API response: status=%d view=%+v",
			response.StatusCode,
			accessView,
		)
	}

	proxyRequest, err = http.NewRequest(
		http.MethodGet,
		fleetPeerURL(peer, "/fleet/v1/proxy"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	proxyRequest.Header.Set(fleetHeaderActor, "admin")
	proxyRequest.Header.Set(fleetHeaderClientIP, "203.0.113.20")
	proxyRequest.Header.Set(fleetHeaderRequestID, "event-history-request")
	proxyRequest.Header.Set(fleetHeaderPath, "/api/server/events/history")
	response, err = client.Do(proxyRequest)
	if err != nil {
		t.Fatal(err)
	}
	var history struct {
		Events []RCONEvent `json:"events"`
	}
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		len(history.Events) != 1 ||
		history.Events[0].Text !=
			"(Managed Server) <ExamplePlayer> joined the server." {
		t.Fatalf(
			"unexpected proxied event history: status=%d events=%+v",
			response.StatusCode,
			history.Events,
		)
	}

	settings.Controller.Address = "127.0.0.2"
	managed.fleetMu.Lock()
	managed.fleetSettings = settings
	managed.fleetMu.Unlock()
	response, err = client.Get(fleetPeerURL(peer, "/fleet/v1/hello"))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected source IP status = %d, want 403", response.StatusCode)
	}
}

func TestFleetInternalDeliveryUsesPinnedTransportAndDestinationPolicy(t *testing.T) {
	controllerDirectory := filepath.Join(t.TempDir(), "controller")
	managedDirectory := filepath.Join(t.TempDir(), "managed")
	if err := os.MkdirAll(controllerDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(managedDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	controllerIdentity := createTestFleetIdentity(t, controllerDirectory)
	managedIdentity := createTestFleetIdentity(t, managedDirectory)
	settings := fleetSettingsFile{
		Version:       fleetSettingsVersion,
		Role:          FleetRoleManaged,
		Alias:         "Managed",
		ListenAddress: "127.0.0.1:8091",
		Controller: &fleetControllerPeer{
			NodeID:    controllerIdentity.NodeID,
			Address:   "127.0.0.1",
			PublicKey: encodedFleetPublicKey(controllerIdentity),
		},
	}
	var delivered []string
	managed := &Manager{
		fleetSettings:         settings,
		fleetSettingsFile:     filepath.Join(managedDirectory, "fleet.json"),
		fleetIdentityKeyFile:  filepath.Join(managedDirectory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(managedDirectory, "identity-cert.pem"),
		fleetNow:              time.Now,
		fleetRecentDeliveries: make(map[string]time.Time),
		auditPath:             filepath.Join(managedDirectory, "audit.jsonl"),
		rconLogPath:           filepath.Join(managedDirectory, "rcon.jsonl"),
		fleetMessageSend: func(message string) error {
			delivered = append(delivered, message)
			return nil
		},
	}
	server := httptest.NewUnstartedServer(managed.fleetInternalHandler())
	server.TLS = managed.managedFleetTLSConfig(settings, managedIdentity)
	server.StartTLS()
	defer server.Close()

	controller := &Manager{
		fleetIdentityKeyFile:  filepath.Join(controllerDirectory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(controllerDirectory, "identity-cert.pem"),
	}
	peer := fleetManagedPeer{
		NodeID:    managedIdentity.NodeID,
		Address:   server.Listener.Addr().String(),
		PublicKey: encodedFleetPublicKey(managedIdentity),
		Sync: FleetSyncPolicy{
			WebSAY: true,
		},
	}
	if err := controller.syncFleetPeerPolicy(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	if !managed.currentFleetSettings().LocalSync.WebSAY {
		t.Fatal("Controller policy was not applied to the Managed Server")
	}
	event := FleetEvent{
		ID:           controllerIdentity.NodeID + ":test:1",
		OriginNodeID: controllerIdentity.NodeID,
		Sequence:     1,
		Time:         time.Now(),
		Category:     FleetEventWebSAY,
		Message:      "안녕하세요",
	}
	if err := controller.deliverFleetEventRemote(
		context.Background(),
		peer,
		"Dread",
		event,
	); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 ||
		delivered[0] != "(Dread Server · WEB SAY) 안녕하세요" {
		t.Fatalf("unexpected delivered messages: %#v", delivered)
	}

	peer.Sync.WebSAY = false
	if err := controller.syncFleetPeerPolicy(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	event.ID = controllerIdentity.NodeID + ":test:2"
	event.Sequence = 2
	if err := controller.deliverFleetEventRemote(
		context.Background(),
		peer,
		"Dread",
		event,
	); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 {
		t.Fatalf("disabled destination accepted an event: %#v", delivered)
	}
}

func tlsState(certificate *x509.Certificate) tls.ConnectionState {
	return tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificate},
	}
}

func TestFleetAddressValidation(t *testing.T) {
	if _, err := normalizeFleetAlias("Dread\u202eServer"); err == nil {
		t.Fatal("accepted a bidirectional formatting character in a fleet alias")
	}
	if value, err := normalizeFleetListenAddress("[::]:8091"); err != nil ||
		value != "[::]:8091" {
		t.Fatalf("IPv6 listener = %q, %v", value, err)
	}
	if value, err := normalizeFleetPeerAddress("[fd00::20]:8091"); err != nil ||
		value != "[fd00::20]:8091" {
		t.Fatalf("IPv6 peer = %q, %v", value, err)
	}
	if network := fleetListenerNetwork("0.0.0.0:8091"); network != "tcp4" {
		t.Fatalf("IPv4 listener network = %q, want tcp4", network)
	}
	if network := fleetListenerNetwork("[::]:8091"); network != "tcp6" {
		t.Fatalf("IPv6 listener network = %q, want tcp6", network)
	}
	for _, invalid := range []string{
		"0.0.0.0:8091",
		"[::]:8091",
		"example.com:8091",
		"192.0.2.20",
	} {
		if _, err := normalizeFleetPeerAddress(invalid); err == nil {
			t.Fatalf("accepted invalid Managed Server address %q", invalid)
		}
	}
	for _, invalid := range []string{"0.0.0.0", "::", "224.0.0.1"} {
		if _, err := normalizeFleetControllerAddress(invalid); err == nil {
			t.Fatalf("accepted invalid Controller address %q", invalid)
		}
	}
}

func TestFleetGatewayPathAllowsOnlyExplicitNodeAPIs(t *testing.T) {
	nodeID, path, ok := parseFleetGatewayPath(
		"/api/nodes/abcdefghijklmnopqrstuvwxyz234567/snapshot",
	)
	if !ok ||
		nodeID != "abcdefghijklmnopqrstuvwxyz234567" ||
		path != "/api/snapshot" {
		t.Fatalf("unexpected parsed route: %q %q %v", nodeID, path, ok)
	}
	for _, invalid := range []string{
		"/api/nodes/",
		"/api/nodes/node",
		"/api/nodes/node/../fleet",
		"/api/nodes/node.with.dot/snapshot",
		"/api/snapshot",
	} {
		if _, _, ok := parseFleetGatewayPath(invalid); ok {
			t.Fatalf("accepted invalid fleet gateway path %q", invalid)
		}
	}
}

func TestFleetGatewayRejectsStateChangesWithoutBrowserCSRF(t *testing.T) {
	directory := t.TempDir()
	identity := createTestFleetIdentity(t, directory)
	manager := &Manager{
		fleetSettings: fleetSettingsFile{
			Version:       fleetSettingsVersion,
			Role:          FleetRoleController,
			Alias:         "Controller",
			ListenAddress: fleetDefaultListenAddress,
		},
		fleetIdentityKeyFile:  filepath.Join(directory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(directory, "identity-cert.pem"),
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/nodes/"+identity.NodeID+"/language",
		nil,
	)
	response := httptest.NewRecorder()
	manager.fleetNodeGatewayHandler(response, request, Session{
		Username: "admin",
		CSRF:     "expected-token",
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestFleetSAYParsingAndSourceLabels(t *testing.T) {
	for command, expected := range map[string]string{
		"say hello":       "hello",
		`SAY "안녕하세요 мир"`: "안녕하세요 мир",
		"say 'bonjour'":   "bonjour",
	} {
		message, ok := rconSAYMessage(command)
		if !ok || message != expected {
			t.Fatalf("rconSAYMessage(%q) = %q, %v", command, message, ok)
		}
	}
	if _, ok := rconSAYMessage("status"); ok {
		t.Fatal("non-SAY RCON command was classified as RCON SAY")
	}
	for _, test := range []struct {
		category string
		expected string
	}{
		{FleetEventAllChat, "(Dread Server) <플레이어> : команда"},
		{FleetEventTeamChat, "(Dread Server · TEAM) <플레이어> : команда"},
		{FleetEventWebSAY, "(Dread Server · WEB SAY) команда"},
		{FleetEventRCONSAY, "(Dread Server · RCON SAY) команда"},
		{FleetEventPlayerLogin, "(Dread Server) <플레이어> joined the server."},
		{FleetEventPlayerLogout, "(Dread Server) <플레이어> left the server."},
	} {
		event := FleetEvent{
			Category:   test.category,
			PlayerName: "플레이어",
			Message:    "команда",
		}
		message, err := formatFleetEventMessage("Dread", event)
		if err != nil {
			t.Fatal(err)
		}
		if message != test.expected {
			t.Fatalf(
				"category %q message = %q, want %q",
				test.category,
				message,
				test.expected,
			)
		}
	}
}

func TestFleetEventIDMustMatchOriginBootAndSequence(t *testing.T) {
	origin := "abcdefghijklmnopqrstuvwxyz234567"
	valid := FleetEvent{
		ID:           origin + ":boot_20260730:42",
		OriginNodeID: origin,
		Sequence:     42,
		Category:     FleetEventWebSAY,
		Message:      "hello",
	}
	if _, err := normalizeFleetEvent(valid); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	for _, invalid := range []FleetEvent{
		func() FleetEvent {
			event := valid
			event.ID = origin + ":boot_20260730:41"
			return event
		}(),
		func() FleetEvent {
			event := valid
			event.ID = origin + ":boot:extra:42"
			return event
		}(),
		func() FleetEvent {
			event := valid
			event.ID = origin + ":bad boot:42"
			return event
		}(),
		func() FleetEvent {
			event := valid
			event.OriginNodeID = "ABCDEF0123456789"
			return event
		}(),
	} {
		if _, err := normalizeFleetEvent(invalid); err == nil {
			t.Fatalf("invalid event accepted: %+v", invalid)
		}
	}
}

func TestFleetGameLogMappingIncludesForcedSessionClose(t *testing.T) {
	directory := t.TempDir()
	identity := createTestFleetIdentity(t, directory)
	manager := &Manager{
		fleetSettings: fleetSettingsFile{
			Version:       fleetSettingsVersion,
			Role:          FleetRoleManaged,
			Alias:         "Dread",
			ListenAddress: fleetDefaultListenAddress,
			LocalSync: FleetSyncPolicy{
				AllChat:         true,
				TeamChat:        true,
				PlayerLifecycle: true,
			},
		},
		fleetIdentityKeyFile:  filepath.Join(directory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(directory, "identity-cert.pem"),
		fleetNow:              time.Now,
		fleetBootID:           "test-boot",
		fleetSubscribers:      make(map[uint64]chan FleetEvent),
		playerHistoryFile:     filepath.Join(directory, "players.json"),
	}
	_, events := manager.subscribeFleetEvents()
	now := time.Now()
	for _, test := range []struct {
		event    gameLogEvent
		category string
	}{
		{
			gameLogEvent{
				Time:        now,
				Kind:        "chat",
				PlayerID:    "1111222233334444",
				PlayerName:  "ExamplePlayer",
				ChatChannel: "ALL",
				ChatMessage: "hello",
			},
			FleetEventAllChat,
		},
		{
			gameLogEvent{
				Time:        now,
				Kind:        "chat",
				PlayerID:    "1111222233334444",
				PlayerName:  "ExamplePlayer",
				ChatChannel: "TEAM",
				ChatMessage: "defend",
			},
			FleetEventTeamChat,
		},
		{
			gameLogEvent{
				Time:         now,
				PlayerAction: "login",
				PlayerID:     "1111222233334444",
				PlayerName:   "ExamplePlayer",
			},
			FleetEventPlayerLogin,
		},
		{
			gameLogEvent{
				Time:         now,
				PlayerAction: "logout",
				PlayerID:     "1111222233334444",
				PlayerName:   "ExamplePlayer",
			},
			FleetEventPlayerLogout,
		},
	} {
		manager.publishFleetGameLogEvent(test.event)
		select {
		case published := <-events:
			if published.Category != test.category ||
				published.OriginNodeID != identity.NodeID {
				t.Fatalf("unexpected published event: %+v", published)
			}
		default:
			t.Fatalf("game-log category %q was not published", test.category)
		}
	}

	processor := newGameLogProcessor()
	processor.players["1111222233334444"] = gameLogPlayer{
		name:     "ExamplePlayer",
		joinedAt: now,
	}
	manager.closeLivePlayerSessions(processor, now.Add(time.Minute))
	select {
	case published := <-events:
		if published.Category != FleetEventPlayerLogout ||
			published.PlayerName != "ExamplePlayer" {
			t.Fatalf("unexpected forced-close event: %+v", published)
		}
	default:
		t.Fatal("forced session close did not publish a logout event")
	}
}

func TestFleetConcurrentPublicationRetainsSequenceOrder(t *testing.T) {
	directory := t.TempDir()
	createTestFleetIdentity(t, directory)
	manager := &Manager{
		fleetSettings: fleetSettingsFile{
			Version:       fleetSettingsVersion,
			Role:          FleetRoleManaged,
			Alias:         "Managed",
			ListenAddress: fleetDefaultListenAddress,
			LocalSync: FleetSyncPolicy{
				AllChat: true,
			},
		},
		fleetIdentityKeyFile:  filepath.Join(directory, "identity-key.pem"),
		fleetIdentityCertFile: filepath.Join(directory, "identity-cert.pem"),
		fleetNow:              time.Now,
		fleetBootID:           "test-boot",
		fleetSubscribers:      make(map[uint64]chan FleetEvent),
	}
	_, events := manager.subscribeFleetEvents()
	const eventCount = 64
	var publishers sync.WaitGroup
	publishers.Add(eventCount)
	for index := 0; index < eventCount; index++ {
		go func() {
			defer publishers.Done()
			manager.publishFleetEvent(
				FleetEventAllChat,
				"ExamplePlayer",
				"hello",
			)
		}()
	}
	publishers.Wait()
	for expected := uint64(1); expected <= eventCount; expected++ {
		select {
		case event := <-events:
			if event.Sequence != expected {
				t.Fatalf(
					"published sequence = %d, want %d",
					event.Sequence,
					expected,
				)
			}
		default:
			t.Fatalf("published event %d is missing", expected)
		}
	}
}

func TestFleetDeliveryQueuesPreserveDestinationOrder(t *testing.T) {
	manager := &Manager{fleetDeliverQueues: newFleetDeliveryQueues()}
	peer := &fleetManagedPeer{NodeID: "managed-node"}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		manager.enqueueFleetDelivery(fleetDelivery{
			Peer: peer,
			Event: FleetEvent{
				Sequence: sequence,
				Category: FleetEventAllChat,
			},
		})
	}
	shard := fleetDeliveryShard(fleetDelivery{Peer: peer}) %
		len(manager.fleetDeliverQueues)
	queue := manager.fleetDeliverQueues[shard]
	for expected := uint64(1); expected <= 3; expected++ {
		select {
		case delivery := <-queue:
			if delivery.Event.Sequence != expected {
				t.Fatalf(
					"delivery sequence = %d, want %d",
					delivery.Event.Sequence,
					expected,
				)
			}
		default:
			t.Fatalf("delivery %d was not queued", expected)
		}
	}
}

func TestFleetRoutingRequiresOriginAndDestinationOptIn(t *testing.T) {
	for _, test := range []struct {
		name              string
		originEnabled     bool
		destinationEnable bool
		wantDelivery      bool
	}{
		{"both enabled", true, true, true},
		{"origin disabled", false, true, false},
		{"destination disabled", true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			identity := createTestFleetIdentity(t, directory)
			peerDirectory := t.TempDir()
			peerIdentity := createTestFleetIdentity(t, peerDirectory)
			manager := &Manager{
				fleetSettings: fleetSettingsFile{
					Version: fleetSettingsVersion,
					Role:    FleetRoleController,
					Alias:   "Controller",
					LocalSync: FleetSyncPolicy{
						AllChat: test.originEnabled,
					},
					Managed: []fleetManagedPeer{{
						NodeID:    peerIdentity.NodeID,
						Alias:     "Duel",
						Address:   "192.0.2.20:8091",
						PublicKey: encodedFleetPublicKey(peerIdentity),
						Sync: FleetSyncPolicy{
							AllChat: test.destinationEnable,
						},
					}},
				},
				fleetIdentityKeyFile:  filepath.Join(directory, "identity-key.pem"),
				fleetIdentityCertFile: filepath.Join(directory, "identity-cert.pem"),
				fleetRecentDeliveries: make(map[string]time.Time),
				fleetDeliverQueues:    newFleetDeliveryQueues(),
				fleetNow:              time.Now,
			}
			manager.routeFleetEvent(FleetEvent{
				ID:           identity.NodeID + ":test:1",
				OriginNodeID: identity.NodeID,
				Sequence:     1,
				Time:         time.Now(),
				Category:     FleetEventAllChat,
				PlayerName:   "Player",
				Message:      "Hello",
			})
			shard := fleetDeliveryShard(fleetDelivery{
				Peer: &manager.fleetSettings.Managed[0],
			}) % len(manager.fleetDeliverQueues)
			queue := manager.fleetDeliverQueues[shard]
			select {
			case delivery := <-queue:
				if !test.wantDelivery {
					t.Fatalf("unexpected delivery: %+v", delivery)
				}
				if delivery.Peer == nil ||
					delivery.Peer.NodeID != peerIdentity.NodeID {
					t.Fatalf("wrong destination: %+v", delivery)
				}
			default:
				if test.wantDelivery {
					t.Fatal("expected a fleet delivery")
				}
			}
		})
	}
}
