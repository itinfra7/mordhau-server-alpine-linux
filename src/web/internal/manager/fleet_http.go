package manager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type fleetSettingsRequest struct {
	Role          string `json:"role"`
	Alias         string `json:"alias"`
	ListenAddress string `json:"listen_address"`
}

type fleetControllerRequest struct {
	Address       string `json:"address"`
	ConnectionKey string `json:"connection_key"`
}

type fleetNodeRequest struct {
	NodeID        string `json:"node_id,omitempty"`
	Alias         string `json:"alias,omitempty"`
	Address       string `json:"address,omitempty"`
	ConnectionKey string `json:"connection_key,omitempty"`
}

type fleetSyncRequest struct {
	NodeID string          `json:"node_id"`
	Sync   FleetSyncPolicy `json:"sync"`
}

func requireFleetCSRF(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) bool {
	if validCSRF(request, session) {
		return true
	}
	writeError(response, http.StatusForbidden, "invalid CSRF token")
	return false
}

func (m *Manager) fleetViewHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(response, http.StatusOK, m.fleetView())
}

func (m *Manager) fleetIdentityHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireFleetCSRF(response, request, session) {
		return
	}
	wasReady := m.localFleetNodeID() != ""
	if _, err := m.ensureFleetIdentity(); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if !wasReady {
		m.auditRequestEvent(
			request,
			session.Username,
			"fleet_identity_generated",
			nil,
		)
	}
	writeJSON(response, http.StatusOK, m.fleetView())
}

func (m *Manager) fleetSettingsHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireFleetCSRF(response, request, session) {
		return
	}
	m.fleetMutationMu.Lock()
	defer m.fleetMutationMu.Unlock()
	var body fleetSettingsRequest
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	role := strings.TrimSpace(strings.ToLower(body.Role))
	if !validFleetRole(role) {
		writeError(response, http.StatusBadRequest, "invalid fleet role")
		return
	}
	alias, err := normalizeFleetAlias(body.Alias)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	listenAddress, err := normalizeFleetListenAddress(body.ListenAddress)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if role != FleetRoleStandalone {
		if _, err := m.ensureFleetIdentity(); err != nil {
			writeError(response, http.StatusInternalServerError, err.Error())
			return
		}
	}
	settings := m.currentFleetSettings()
	previousRole := settings.Role
	settings.Role = role
	settings.Alias = alias
	settings.ListenAddress = listenAddress
	if err := m.saveFleetSettings(settings); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"fleet_settings_saved",
		map[string]string{
			"previous_role": previousRole,
			"role":          role,
			"alias":         alias,
			"listen":        listenAddress,
		},
	)
	writeJSON(response, http.StatusOK, m.fleetView())
}

func (m *Manager) fleetControllerHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost && request.Method != http.MethodDelete {
		response.Header().Set("Allow", "POST, DELETE")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireFleetCSRF(response, request, session) {
		return
	}
	m.fleetMutationMu.Lock()
	defer m.fleetMutationMu.Unlock()
	settings := m.currentFleetSettings()
	if settings.Role != FleetRoleManaged {
		writeError(response, http.StatusConflict, "server is not in Managed Server mode")
		return
	}
	if request.Method == http.MethodDelete {
		settings.Controller = nil
		if err := m.saveFleetSettings(settings); err != nil {
			writeError(response, http.StatusInternalServerError, err.Error())
			return
		}
		m.auditRequestEvent(
			request,
			session.Username,
			"fleet_controller_removed",
			nil,
		)
		writeJSON(response, http.StatusOK, m.fleetView())
		return
	}

	var body fleetControllerRequest
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	address, err := normalizeFleetControllerAddress(body.Address)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	var key fleetConnectionKeyPayload
	if strings.TrimSpace(body.ConnectionKey) == "" {
		if settings.Controller == nil {
			writeError(response, http.StatusBadRequest, "controller connection key is required")
			return
		}
		key = fleetConnectionKeyPayload{
			Version:   fleetProtocolVersion,
			NodeID:    settings.Controller.NodeID,
			PublicKey: settings.Controller.PublicKey,
		}
	} else {
		key, err = parseFleetConnectionKey(body.ConnectionKey)
		if err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
	}
	identity, err := m.ensureFleetIdentity()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if key.NodeID == identity.NodeID {
		writeError(response, http.StatusBadRequest, "controller key belongs to this server")
		return
	}
	settings.Controller = &fleetControllerPeer{
		NodeID:    key.NodeID,
		Address:   address,
		PublicKey: key.PublicKey,
	}
	if err := m.saveFleetSettings(settings); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"fleet_controller_saved",
		map[string]string{
			"controller_node_id": key.NodeID,
			"controller_address": address,
		},
	)
	writeJSON(response, http.StatusOK, m.fleetView())
}

func (m *Manager) fleetNodesHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost &&
		request.Method != http.MethodPut &&
		request.Method != http.MethodDelete {
		response.Header().Set("Allow", "POST, PUT, DELETE")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireFleetCSRF(response, request, session) {
		return
	}
	m.fleetMutationMu.Lock()
	defer m.fleetMutationMu.Unlock()
	settings := m.currentFleetSettings()
	if settings.Role != FleetRoleController {
		writeError(response, http.StatusConflict, "server is not in Fleet Controller mode")
		return
	}
	var body fleetNodeRequest
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	switch request.Method {
	case http.MethodPost:
		alias, err := normalizeFleetAlias(body.Alias)
		if err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		address, err := normalizeFleetPeerAddress(body.Address)
		if err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		key, err := parseFleetConnectionKey(body.ConnectionKey)
		if err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		if key.NodeID == m.localFleetNodeID() {
			writeError(response, http.StatusBadRequest, "managed-server key belongs to this server")
			return
		}
		settings.Managed = append(settings.Managed, fleetManagedPeer{
			NodeID:    key.NodeID,
			Alias:     alias,
			Address:   address,
			PublicKey: key.PublicKey,
			Sync:      FleetSyncPolicy{},
		})
		if err := m.saveFleetSettings(settings); err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		m.auditRequestEvent(
			request,
			session.Username,
			"fleet_node_added",
			map[string]string{
				"node_id": key.NodeID,
				"alias":   alias,
				"address": address,
			},
		)
	case http.MethodPut:
		index := fleetManagedIndex(settings.Managed, strings.TrimSpace(body.NodeID))
		if index < 0 {
			writeError(response, http.StatusNotFound, "managed server was not found")
			return
		}
		alias, err := normalizeFleetAlias(body.Alias)
		if err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		address, err := normalizeFleetPeerAddress(body.Address)
		if err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		settings.Managed[index].Alias = alias
		settings.Managed[index].Address = address
		if err := m.saveFleetSettings(settings); err != nil {
			writeError(response, http.StatusBadRequest, err.Error())
			return
		}
		m.auditRequestEvent(
			request,
			session.Username,
			"fleet_node_edited",
			map[string]string{
				"node_id": body.NodeID,
				"alias":   alias,
				"address": address,
			},
		)
	case http.MethodDelete:
		nodeID := strings.TrimSpace(body.NodeID)
		index := fleetManagedIndex(settings.Managed, nodeID)
		if index < 0 {
			writeError(response, http.StatusNotFound, "managed server was not found")
			return
		}
		removed := settings.Managed[index]
		settings.Managed = append(
			settings.Managed[:index],
			settings.Managed[index+1:]...,
		)
		if err := m.saveFleetSettings(settings); err != nil {
			writeError(response, http.StatusInternalServerError, err.Error())
			return
		}
		m.auditRequestEvent(
			request,
			session.Username,
			"fleet_node_removed",
			map[string]string{
				"node_id": nodeID,
				"alias":   removed.Alias,
			},
		)
	}
	writeJSON(response, http.StatusOK, m.fleetView())
}

func fleetManagedIndex(peers []fleetManagedPeer, nodeID string) int {
	for index := range peers {
		if peers[index].NodeID == nodeID {
			return index
		}
	}
	return -1
}

func (m *Manager) fleetSyncHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireFleetCSRF(response, request, session) {
		return
	}
	m.fleetMutationMu.Lock()
	defer m.fleetMutationMu.Unlock()
	var body fleetSyncRequest
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	nodeID := strings.TrimSpace(body.NodeID)
	settings := m.currentFleetSettings()
	localNodeID := m.localFleetNodeID()
	switch settings.Role {
	case FleetRoleController:
		if nodeID == localNodeID {
			settings.LocalSync = body.Sync
		} else {
			index := fleetManagedIndex(settings.Managed, nodeID)
			if index < 0 {
				writeError(response, http.StatusNotFound, "fleet server was not found")
				return
			}
			settings.Managed[index].Sync = body.Sync
		}
	case FleetRoleManaged:
		if nodeID != localNodeID {
			writeError(response, http.StatusNotFound, "fleet server was not found")
			return
		}
		settings.LocalSync = body.Sync
	default:
		writeError(response, http.StatusConflict, "Server Fleet is not enabled")
		return
	}
	if err := m.saveFleetSettings(settings); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"fleet_sync_policy_saved",
		map[string]string{
			"node_id":          nodeID,
			"all_chat":         strconv.FormatBool(body.Sync.AllChat),
			"team_chat":        strconv.FormatBool(body.Sync.TeamChat),
			"web_say":          strconv.FormatBool(body.Sync.WebSAY),
			"rcon_say":         strconv.FormatBool(body.Sync.RCONSAY),
			"player_lifecycle": strconv.FormatBool(body.Sync.PlayerLifecycle),
		},
	)
	writeJSON(response, http.StatusOK, m.fleetView())
}

func (m *Manager) fleetNodeGatewayHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	settings := m.currentFleetSettings()
	if settings.Role != FleetRoleController {
		writeError(response, http.StatusConflict, "server is not in Fleet Controller mode")
		return
	}
	nodeID, apiPath, ok := parseFleetGatewayPath(request.URL.Path)
	if !ok {
		http.NotFound(response, request)
		return
	}
	handler, ok := m.fleetRemoteRoute(apiPath)
	if !ok {
		http.NotFound(response, request)
		return
	}
	if request.Method != http.MethodGet &&
		!requireFleetCSRF(response, request, session) {
		return
	}
	if nodeID == m.localFleetNodeID() {
		localRequest := request.Clone(request.Context())
		localURL := *request.URL
		localURL.Path = apiPath
		localURL.RawPath = ""
		localRequest.URL = &localURL
		handler(response, localRequest, session)
		return
	}
	peer, ok := m.fleetPeer(nodeID)
	if !ok {
		writeError(response, http.StatusNotFound, "managed server was not found")
		return
	}
	m.proxyFleetNodeRequest(response, request, session, peer, apiPath)
}

func parseFleetGatewayPath(path string) (string, string, bool) {
	remainder := strings.TrimPrefix(path, "/api/nodes/")
	if remainder == path {
		return "", "", false
	}
	nodeID, suffix, found := strings.Cut(remainder, "/")
	if !found || nodeID == "" || suffix == "" ||
		!validFleetNodeID(nodeID) {
		return "", "", false
	}
	apiPath := "/api/" + strings.TrimPrefix(suffix, "/")
	if strings.Contains(apiPath, "..") {
		return "", "", false
	}
	return nodeID, apiPath, true
}

func (m *Manager) proxyFleetNodeRequest(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
	peer fleetManagedPeer,
	apiPath string,
) {
	clientIP, err := requestIP(request)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid client IP")
		return
	}
	requestID, err := randomToken(12)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "could not create request ID")
		return
	}
	client, err := m.fleetHTTPClient(peer)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}

	proxyRequest, err := http.NewRequestWithContext(
		request.Context(),
		request.Method,
		fleetPeerURL(peer, "/fleet/v1/proxy"),
		request.Body,
	)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	proxyRequest.ContentLength = request.ContentLength
	proxyRequest.Header.Set(fleetHeaderActor, session.Username)
	proxyRequest.Header.Set(fleetHeaderClientIP, clientIP.Unmap().String())
	proxyRequest.Header.Set(fleetHeaderRequestID, requestID)
	proxyRequest.Header.Set(fleetHeaderPath, apiPath)
	proxyRequest.Header.Set(fleetHeaderQuery, request.URL.RawQuery)
	copyRequestHeader(request, proxyRequest, "Content-Type")
	copyRequestHeader(request, proxyRequest, "Accept")
	copyRequestHeader(request, proxyRequest, "If-None-Match")
	copyRequestHeader(request, proxyRequest, "If-Modified-Since")

	started := time.Now()
	proxyResponse, err := client.Do(proxyRequest)
	if err != nil {
		m.auditRequestEvent(
			request,
			session.Username,
			"fleet_remote_request_failed",
			map[string]string{
				"request_id": requestID,
				"node_id":    peer.NodeID,
				"path":       apiPath,
				"error":      safeAuditText(err.Error(), 200),
			},
		)
		writeError(response, http.StatusBadGateway, "managed server is unavailable")
		return
	}
	defer proxyResponse.Body.Close()
	copyResponseHeader(proxyResponse, response, "Content-Type")
	copyResponseHeader(proxyResponse, response, "Content-Disposition")
	copyResponseHeader(proxyResponse, response, "Cache-Control")
	copyResponseHeader(proxyResponse, response, "ETag")
	copyResponseHeader(proxyResponse, response, "Last-Modified")
	response.WriteHeader(proxyResponse.StatusCode)
	written, copyErr := copyFleetResponseBody(response, proxyResponse)
	details := map[string]string{
		"request_id":  requestID,
		"node_id":     peer.NodeID,
		"path":        apiPath,
		"status":      strconv.Itoa(proxyResponse.StatusCode),
		"bytes":       strconv.FormatInt(written, 10),
		"duration_ms": strconv.FormatInt(time.Since(started).Milliseconds(), 10),
	}
	if copyErr != nil && !errors.Is(copyErr, context.Canceled) {
		details["error"] = safeAuditText(copyErr.Error(), 200)
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"fleet_remote_request_completed",
		details,
	)
}

func copyRequestHeader(source, target *http.Request, name string) {
	if value := source.Header.Get(name); value != "" {
		target.Header.Set(name, value)
	}
}

func copyResponseHeader(
	source *http.Response,
	target http.ResponseWriter,
	name string,
) {
	if value := source.Header.Get(name); value != "" {
		target.Header().Set(name, value)
	}
}

func copyFleetResponseBody(
	response http.ResponseWriter,
	source *http.Response,
) (int64, error) {
	if !strings.HasPrefix(
		strings.ToLower(source.Header.Get("Content-Type")),
		"text/event-stream",
	) {
		return io.Copy(response, source.Body)
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		return 0, errors.New("fleet gateway does not support streaming")
	}
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		count, readErr := source.Body.Read(buffer)
		if count > 0 {
			written, writeErr := response.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			flusher.Flush()
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}
