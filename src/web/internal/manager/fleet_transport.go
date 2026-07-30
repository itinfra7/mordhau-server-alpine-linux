package manager

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	fleetHeaderActor     = "X-Mordhau-Fleet-Actor"
	fleetHeaderClientIP  = "X-Mordhau-Fleet-Client-IP"
	fleetHeaderRequestID = "X-Mordhau-Fleet-Request-ID"
	fleetHeaderPath      = "X-Mordhau-Fleet-Path"
	fleetHeaderQuery     = "X-Mordhau-Fleet-Query"
)

type fleetHello struct {
	Protocol       int             `json:"protocol"`
	NodeID         string          `json:"node_id"`
	ManagerVersion string          `json:"manager_version"`
	Status         FleetNodeStatus `json:"status"`
	Time           time.Time       `json:"time"`
}

type fleetEventDeliveryRequest struct {
	Alias string     `json:"alias"`
	Event FleetEvent `json:"event"`
}

type fleetFeedEnvelope struct {
	Type   string           `json:"type"`
	Time   time.Time        `json:"time"`
	Event  *FleetEvent      `json:"event,omitempty"`
	Status *FleetNodeStatus `json:"status,omitempty"`
}

type fleetPeerWorkerState struct {
	cancel    context.CancelFunc
	signature string
}

type fleetCachedHTTPClient struct {
	signature string
	client    *http.Client
}

func fleetPeerTransportSignature(peer fleetManagedPeer) string {
	data, _ := json.Marshal(peer)
	return string(data)
}

func fleetPeerClientSignature(peer fleetManagedPeer) string {
	return peer.Address + "\x00" + peer.PublicKey
}

func fleetSettingsTransportSignature(settings fleetSettingsFile) string {
	type signature struct {
		Role          string
		ListenAddress string
		Controller    *fleetControllerPeer
	}
	data, _ := json.Marshal(signature{
		Role:          settings.Role,
		ListenAddress: settings.ListenAddress,
		Controller:    settings.Controller,
	})
	return string(data)
}

func fleetListenerNetwork(listenAddress string) string {
	host, _, err := net.SplitHostPort(listenAddress)
	if err == nil {
		if address, parseErr := netip.ParseAddr(host); parseErr == nil &&
			address.Unmap().Is4() {
			return "tcp4"
		}
	}
	return "tcp6"
}

func (m *Manager) fleetSupervisorLoop(ctx context.Context) {
	defer m.closeFleetHTTPClients()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var (
		listenerCancel    context.CancelFunc
		listenerDone      <-chan struct{}
		listenerSignature string
		workers           = make(map[string]fleetPeerWorkerState)
	)
	reconcile := func() {
		if listenerDone != nil {
			select {
			case <-listenerDone:
				listenerCancel = nil
				listenerDone = nil
				listenerSignature = ""
			default:
			}
		}
		settings := m.currentFleetSettings()
		m.pruneFleetHTTPClients(settings)
		signature := fleetSettingsTransportSignature(settings)
		managedReady := settings.Role == FleetRoleManaged &&
			settings.Controller != nil
		if !managedReady || signature != listenerSignature {
			if listenerCancel != nil {
				listenerCancel()
				listenerCancel = nil
			}
			listenerSignature = ""
		}
		if managedReady && listenerCancel == nil && listenerDone == nil {
			listenerContext, cancel := context.WithCancel(ctx)
			listenerCancel = cancel
			listenerSignature = signature
			settingsCopy := copyFleetSettings(settings)
			done := make(chan struct{})
			listenerDone = done
			go func() {
				defer close(done)
				m.runFleetManagedServer(listenerContext, settingsCopy)
			}()
		}

		active := make(map[string]bool)
		if settings.Role == FleetRoleController {
			for _, peer := range settings.Managed {
				active[peer.NodeID] = true
				peerSignature := fleetPeerTransportSignature(peer)
				if worker, exists := workers[peer.NodeID]; exists &&
					worker.signature == peerSignature {
					continue
				}
				if worker, exists := workers[peer.NodeID]; exists {
					worker.cancel()
				}
				workerContext, cancel := context.WithCancel(ctx)
				workers[peer.NodeID] = fleetPeerWorkerState{
					cancel:    cancel,
					signature: peerSignature,
				}
				go m.fleetPeerWorker(workerContext, peer.NodeID)
			}
		}
		for nodeID, worker := range workers {
			if !active[nodeID] {
				worker.cancel()
				delete(workers, nodeID)
			}
		}
	}

	reconcile()
	for {
		select {
		case <-ctx.Done():
			if listenerCancel != nil {
				listenerCancel()
			}
			for _, worker := range workers {
				worker.cancel()
			}
			return
		case <-m.fleetWake:
			reconcile()
		case <-ticker.C:
			reconcile()
		}
	}
}

func (m *Manager) managedFleetTLSConfig(
	settings fleetSettingsFile,
	identity fleetIdentity,
) *tls.Config {
	controller := *settings.Controller
	return &tls.Config{
		Certificates: []tls.Certificate{identity.TLS},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 {
				return errors.New("fleet controller must present exactly one certificate")
			}
			if !fleetPublicKeyEqual(
				controller.PublicKey,
				state.PeerCertificates[0],
			) {
				return errors.New("fleet controller certificate is not trusted")
			}
			certificate := state.PeerCertificates[0]
			now := time.Now()
			if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
				return errors.New("fleet controller certificate is outside its validity period")
			}
			return nil
		},
	}
}

func (m *Manager) runFleetManagedServer(
	ctx context.Context,
	settings fleetSettingsFile,
) {
	identity, err := m.ensureFleetIdentity()
	if err != nil {
		log.Printf("start fleet listener: %v", err)
		return
	}
	listener, err := net.Listen(
		fleetListenerNetwork(settings.ListenAddress),
		settings.ListenAddress,
	)
	if err != nil {
		log.Printf("listen for fleet controller on %s: %v", settings.ListenAddress, err)
		m.auditActorEvent(
			"system",
			"local",
			"fleet_listener_failed",
			map[string]string{
				"address": settings.ListenAddress,
				"error":   safeAuditText(err.Error(), 200),
			},
		)
		return
	}
	tlsListener := tls.NewListener(
		listener,
		m.managedFleetTLSConfig(settings, identity),
	)
	server := &http.Server{
		Handler:           m.fleetInternalHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
		_ = listener.Close()
	}()
	m.auditActorEvent(
		"system",
		"local",
		"fleet_listener_started",
		map[string]string{"address": settings.ListenAddress},
	)
	defer m.auditActorEvent(
		"system",
		"local",
		"fleet_listener_stopped",
		map[string]string{"address": settings.ListenAddress},
	)
	err = server.Serve(tlsListener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) &&
		ctx.Err() == nil {
		log.Printf("fleet listener: %v", err)
	}
}

func (m *Manager) fleetInternalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/fleet/v1/hello", m.fleetInternalHelloHandler)
	mux.HandleFunc("/fleet/v1/proxy", m.fleetInternalProxyHandler)
	mux.HandleFunc("/fleet/v1/feed", m.fleetInternalFeedHandler)
	mux.HandleFunc("/fleet/v1/deliver", m.fleetInternalDeliverHandler)
	mux.HandleFunc("/fleet/v1/policy", m.fleetInternalPolicyHandler)
	return m.securityHeaders(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if !m.authorizeFleetController(response, request) {
			return
		}
		mux.ServeHTTP(response, request)
	}))
}

func (m *Manager) authorizeFleetController(
	response http.ResponseWriter,
	request *http.Request,
) bool {
	settings := m.currentFleetSettings()
	if settings.Role != FleetRoleManaged || settings.Controller == nil {
		http.Error(response, "fleet managed-server mode is not active", http.StatusServiceUnavailable)
		return false
	}
	if request.TLS == nil || len(request.TLS.PeerCertificates) != 1 ||
		!fleetPublicKeyEqual(settings.Controller.PublicKey, request.TLS.PeerCertificates[0]) {
		http.Error(response, "untrusted fleet controller", http.StatusUnauthorized)
		return false
	}
	peer, err := remotePeerIP(request)
	if err != nil {
		http.Error(response, "invalid fleet controller address", http.StatusBadRequest)
		return false
	}
	expected, err := netip.ParseAddr(settings.Controller.Address)
	if err != nil || peer.Unmap() != expected.Unmap() {
		http.Error(response, "fleet controller address is not allowed", http.StatusForbidden)
		return false
	}
	return true
}

func installedFleetManagerVersion() string {
	version, err := readInstalledManagerVersion(managerVersionPath)
	if err != nil {
		return "unknown"
	}
	return version
}

func (m *Manager) fleetInternalHelloHandler(
	response http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	identity, err := m.loadFleetIdentity()
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "fleet identity is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, fleetHello{
		Protocol:       fleetProtocolVersion,
		NodeID:         identity.NodeID,
		ManagerVersion: installedFleetManagerVersion(),
		Status:         m.localFleetStatus(),
		Time:           m.fleetNow(),
	})
}

func (m *Manager) fleetInternalFeedHandler(
	response http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("Connection", "keep-alive")

	subscriberID, events := m.subscribeFleetEvents()
	defer m.unsubscribeFleetEvents(subscriberID)
	send := func(envelope fleetFeedEnvelope) bool {
		data, err := json.Marshal(envelope)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(response, "event: fleet\ndata: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	initialStatus := m.localFleetStatus()
	if !send(fleetFeedEnvelope{
		Type:   "heartbeat",
		Time:   m.fleetNow(),
		Status: &initialStatus,
	}) {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event := <-events:
			eventCopy := event
			if !send(fleetFeedEnvelope{
				Type:  "event",
				Time:  m.fleetNow(),
				Event: &eventCopy,
			}) {
				return
			}
		case <-ticker.C:
			status := m.localFleetStatus()
			if !send(fleetFeedEnvelope{
				Type:   "heartbeat",
				Time:   m.fleetNow(),
				Status: &status,
			}) {
				return
			}
		}
	}
}

func (m *Manager) fleetInternalDeliverHandler(
	response http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var delivery fleetEventDeliveryRequest
	if err := decodeJSON(response, request, &delivery); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	alias, err := normalizeFleetAlias(delivery.Alias)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	event, err := normalizeFleetEvent(delivery.Event)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if !m.currentFleetSettings().LocalSync.enabled(event.Category) {
		writeJSON(response, http.StatusOK, map[string]string{"status": "disabled"})
		return
	}
	if m.fleetEventSeen(event) {
		writeJSON(response, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}
	if err := m.deliverFleetEventLocal(alias, event); err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	m.auditActorEvent(
		"fleet-controller",
		settingsControllerClientIP(request),
		"fleet_event_delivered",
		map[string]string{
			"origin_node_id": event.OriginNodeID,
			"category":       event.Category,
		},
	)
	writeJSON(response, http.StatusOK, map[string]string{"status": "delivered"})
}

func settingsControllerClientIP(request *http.Request) string {
	peer, err := remotePeerIP(request)
	if err != nil {
		return "unknown"
	}
	return peer.String()
}

func (m *Manager) fleetInternalPolicyHandler(
	response http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var policy FleetSyncPolicy
	if err := decodeJSON(response, request, &policy); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.fleetMutationMu.Lock()
	defer m.fleetMutationMu.Unlock()
	settings := m.currentFleetSettings()
	if settings.Role != FleetRoleManaged {
		writeError(response, http.StatusConflict, "server is not in managed mode")
		return
	}
	if settings.LocalSync == policy {
		writeJSON(response, http.StatusOK, policy)
		return
	}
	settings.LocalSync = policy
	if err := m.saveFleetSettings(settings); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	m.auditActorEvent(
		"fleet-controller",
		settingsControllerClientIP(request),
		"fleet_sync_policy_applied",
		map[string]string{
			"all_chat":         strconv.FormatBool(policy.AllChat),
			"team_chat":        strconv.FormatBool(policy.TeamChat),
			"web_say":          strconv.FormatBool(policy.WebSAY),
			"rcon_say":         strconv.FormatBool(policy.RCONSAY),
			"player_lifecycle": strconv.FormatBool(policy.PlayerLifecycle),
		},
	)
	writeJSON(response, http.StatusOK, policy)
}

func (m *Manager) fleetInternalProxyHandler(
	response http.ResponseWriter,
	request *http.Request,
) {
	path := strings.TrimSpace(request.Header.Get(fleetHeaderPath))
	handler, ok := m.fleetRemoteRoute(path)
	if !ok {
		http.Error(response, "fleet API path is not allowed", http.StatusNotFound)
		return
	}
	rawActor := strings.TrimSpace(request.Header.Get(fleetHeaderActor))
	actor := safeAuditAccount(rawActor)
	if rawActor == "" || actor != rawActor {
		http.Error(response, "invalid fleet actor", http.StatusBadRequest)
		return
	}
	clientIP, err := netip.ParseAddr(
		strings.TrimSpace(request.Header.Get(fleetHeaderClientIP)),
	)
	if err != nil || clientIP.Zone() != "" ||
		clientIP.IsUnspecified() || clientIP.IsMulticast() {
		http.Error(response, "invalid fleet actor address", http.StatusBadRequest)
		return
	}
	peerIP, err := remotePeerIP(request)
	if err != nil {
		http.Error(response, "invalid fleet peer address", http.StatusBadRequest)
		return
	}
	requestID := safeAuditText(request.Header.Get(fleetHeaderRequestID), 64)
	if requestID == "" {
		http.Error(response, "missing fleet request ID", http.StatusBadRequest)
		return
	}
	rawQuery := request.Header.Get(fleetHeaderQuery)
	if strings.ContainsAny(rawQuery, "\r\n") {
		http.Error(response, "invalid fleet API query", http.StatusBadRequest)
		return
	}

	request.URL.Path = path
	request.URL.RawPath = ""
	request.URL.RawQuery = rawQuery
	for _, name := range []string{
		"Authorization",
		"Cookie",
		"Forwarded",
		"X-Forwarded-For",
		"X-Real-IP",
	} {
		request.Header.Del(name)
	}
	request.Header.Set("X-CSRF-Token", "fleet-internal")
	request.Header.Del("Sec-Fetch-Site")
	request = request.WithContext(context.WithValue(
		request.Context(),
		requestAddressContextKey{},
		requestAddress{
			clientIP: clientIP.Unmap(),
			peerIP:   peerIP.Unmap(),
		},
	))
	session := Session{
		Username: "controller/" + actor,
		CSRF:     "fleet-internal",
	}
	m.auditNetworkActorEvent(
		session.Username,
		clientIP.String(),
		peerIP.String(),
		"fleet_remote_request",
		map[string]string{
			"request_id": requestID,
			"path":       path,
			"method":     request.Method,
		},
	)
	handler(response, request, session)
}

func fleetClientTLSConfig(
	identity fleetIdentity,
	expectedPublicKey string,
) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{identity.TLS},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		// Public CA and DNS-name verification are deliberately replaced by
		// the pinned Ed25519 identity checked below.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 {
				return errors.New("managed server must present exactly one certificate")
			}
			certificate := state.PeerCertificates[0]
			if !fleetPublicKeyEqual(expectedPublicKey, certificate) {
				return errors.New("managed server certificate is not trusted")
			}
			now := time.Now()
			if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
				return errors.New("managed server certificate is outside its validity period")
			}
			return nil
		},
	}
}

func (m *Manager) fleetHTTPClient(peer fleetManagedPeer) (*http.Client, error) {
	identity, err := m.loadFleetIdentity()
	if err != nil {
		return nil, err
	}
	signature := fleetPeerClientSignature(peer)
	m.fleetClientMu.Lock()
	if cached, exists := m.fleetClients[peer.NodeID]; exists {
		if cached.signature == signature {
			m.fleetClientMu.Unlock()
			return cached.client, nil
		}
		cached.client.CloseIdleConnections()
		delete(m.fleetClients, peer.NodeID)
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		DisableCompression:    true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		ExpectContinueTimeout: time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: fleetClientTLSConfig(identity, peer.PublicKey),
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(
			_ *http.Request,
			_ []*http.Request,
		) error {
			return http.ErrUseLastResponse
		},
	}
	if m.fleetClients == nil {
		m.fleetClients = make(map[string]fleetCachedHTTPClient)
	}
	m.fleetClients[peer.NodeID] = fleetCachedHTTPClient{
		signature: signature,
		client:    client,
	}
	m.fleetClientMu.Unlock()
	return client, nil
}

func (m *Manager) pruneFleetHTTPClients(settings fleetSettingsFile) {
	allowed := make(map[string]string)
	if settings.Role == FleetRoleController {
		for _, peer := range settings.Managed {
			allowed[peer.NodeID] = fleetPeerClientSignature(peer)
		}
	}
	m.fleetClientMu.Lock()
	defer m.fleetClientMu.Unlock()
	for nodeID, cached := range m.fleetClients {
		if allowed[nodeID] == cached.signature {
			continue
		}
		cached.client.CloseIdleConnections()
		delete(m.fleetClients, nodeID)
	}
}

func (m *Manager) closeFleetHTTPClients() {
	m.fleetClientMu.Lock()
	defer m.fleetClientMu.Unlock()
	for nodeID, cached := range m.fleetClients {
		cached.client.CloseIdleConnections()
		delete(m.fleetClients, nodeID)
	}
}

func fleetPeerURL(peer fleetManagedPeer, path string) string {
	return "https://" + peer.Address + path
}

func (m *Manager) fleetJSONRequest(
	ctx context.Context,
	peer fleetManagedPeer,
	method string,
	path string,
	body io.Reader,
	output any,
) error {
	client, err := m.fleetHTTPClient(peer)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		fleetPeerURL(peer, path),
		body,
	)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = response.Status
		}
		return fmt.Errorf("managed server returned %s: %s", response.Status, message)
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output); err != nil {
		return err
	}
	return nil
}

func (m *Manager) syncFleetPeerPolicy(
	ctx context.Context,
	peer fleetManagedPeer,
) error {
	data, err := json.Marshal(peer.Sync)
	if err != nil {
		return err
	}
	return m.fleetJSONRequest(
		ctx,
		peer,
		http.MethodPost,
		"/fleet/v1/policy",
		strings.NewReader(string(data)),
		nil,
	)
}

func (m *Manager) fleetPeerHello(
	ctx context.Context,
	peer fleetManagedPeer,
) (fleetHello, error) {
	var hello fleetHello
	err := m.fleetJSONRequest(
		ctx,
		peer,
		http.MethodGet,
		"/fleet/v1/hello",
		nil,
		&hello,
	)
	if err != nil {
		return fleetHello{}, err
	}
	if hello.Protocol != fleetProtocolVersion || hello.NodeID != peer.NodeID {
		return fleetHello{}, errors.New("managed server fleet identity or protocol mismatch")
	}
	return hello, nil
}

func (m *Manager) fleetPeerWorker(ctx context.Context, nodeID string) {
	backoff := time.Second
	for {
		peer, ok := m.fleetPeer(nodeID)
		if !ok {
			return
		}
		helloContext, helloCancel := context.WithTimeout(ctx, 8*time.Second)
		hello, helloErr := m.fleetPeerHello(helloContext, peer)
		helloCancel()
		var policyErr error
		if helloErr == nil {
			policyContext, policyCancel := context.WithTimeout(ctx, 8*time.Second)
			policyErr = m.syncFleetPeerPolicy(policyContext, peer)
			policyCancel()
		}
		if ctx.Err() != nil {
			return
		}
		if policyErr != nil || helloErr != nil {
			err := helloErr
			if err == nil {
				err = policyErr
			}
			status := m.currentFleetStatus(nodeID)
			status.Connected = false
			status.LastError = err.Error()
			m.updateFleetStatus(nodeID, status)
		} else {
			backoff = time.Second
			status := fleetStatusFromHello(hello, m.fleetNow())
			m.updateFleetStatus(nodeID, status)
			if err := m.consumeFleetPeerFeed(ctx, peer); err != nil &&
				ctx.Err() == nil {
				status.Connected = false
				status.LastError = err.Error()
				m.updateFleetStatus(nodeID, status)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		} else {
			backoff = 15 * time.Second
		}
	}
}

func fleetStatusFromHello(hello fleetHello, receivedAt time.Time) FleetNodeStatus {
	status := hello.Status
	status.Connected = true
	status.LastSeen = receivedAt
	status.ManagerVersion = safeAuditText(hello.ManagerVersion, 64)
	status.Protocol = hello.Protocol
	return status
}

func (m *Manager) consumeFleetPeerFeed(
	ctx context.Context,
	peer fleetManagedPeer,
) error {
	client, err := m.fleetHTTPClient(peer)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fleetPeerURL(peer, "/fleet/v1/feed"),
		nil,
	)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fleet feed returned %s", response.Status)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var envelope fleetFeedEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope); err != nil {
			return errors.New("managed server sent an invalid fleet feed event")
		}
		receivedAt := m.fleetNow()
		status := m.currentFleetStatus(peer.NodeID)
		status.Connected = true
		status.LastSeen = receivedAt
		status.LastError = ""
		if envelope.Status != nil {
			status = *envelope.Status
			status.Connected = true
			status.LastSeen = receivedAt
			status.LastError = ""
			status.ManagerVersion = safeAuditText(status.ManagerVersion, 64)
		}
		m.updateFleetStatus(peer.NodeID, status)
		if envelope.Type != "event" || envelope.Event == nil {
			continue
		}
		event, err := normalizeFleetEvent(*envelope.Event)
		if err != nil || event.OriginNodeID != peer.NodeID {
			continue
		}
		m.enqueueFleetRoute(event)
	}
	return scanner.Err()
}

func (m *Manager) currentFleetStatus(nodeID string) FleetNodeStatus {
	m.fleetMu.RLock()
	defer m.fleetMu.RUnlock()
	return m.fleetStatuses[nodeID]
}

func (m *Manager) deliverFleetEventRemote(
	ctx context.Context,
	peer fleetManagedPeer,
	alias string,
	event FleetEvent,
) error {
	data, err := json.Marshal(fleetEventDeliveryRequest{
		Alias: alias,
		Event: event,
	})
	if err != nil {
		return err
	}
	return m.fleetJSONRequest(
		ctx,
		peer,
		http.MethodPost,
		"/fleet/v1/deliver",
		strings.NewReader(string(data)),
		nil,
	)
}
