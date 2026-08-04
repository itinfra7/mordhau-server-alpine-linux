package manager

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed static/*
var staticFiles embed.FS

func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", m.indexHandler)
	mux.HandleFunc("/login", m.loginHandler)
	mux.HandleFunc("/logout", m.withSession(m.logoutHandler))
	mux.HandleFunc("/static/", m.staticHandler)
	for _, route := range m.managerAPIRoutes() {
		mux.HandleFunc(route.Path, m.withSession(route.Handler))
	}
	mux.HandleFunc("/api/nodes/", m.withSession(m.fleetNodeGatewayHandler))
	mux.HandleFunc("/api/fleet", m.withSession(m.fleetViewHandler))
	mux.HandleFunc("/api/fleet/identity", m.withSession(m.fleetIdentityHandler))
	mux.HandleFunc("/api/fleet/settings", m.withSession(m.fleetSettingsHandler))
	mux.HandleFunc("/api/fleet/controller", m.withSession(m.fleetControllerHandler))
	mux.HandleFunc("/api/fleet/nodes", m.withSession(m.fleetNodesHandler))
	mux.HandleFunc("/api/fleet/sync", m.withSession(m.fleetSyncHandler))
	return m.securityHeaders(
		m.requestAddressMiddleware(
			m.auditMiddleware(
				m.accessMiddleware(mux),
			),
		),
	)
}

func (m *Manager) steamUpdateStatusHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.currentSteamUpdateView()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) steamUpdateCheckHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	view, err := m.checkSteamUpdate(request.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errSteamUpdateLifecycleBusy) {
			status = http.StatusConflict
		}
		m.auditRequestEvent(
			request,
			session.Username,
			"steam_update_check_failed",
			map[string]string{"error": boundedManagerUpdateText(err.Error(), 256)},
		)
		writeError(response, status, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"steam_update_checked",
		map[string]string{
			"installed_build_id": view.InstalledBuildID,
			"latest_build_id":    view.LatestBuildID,
			"available":          strconv.FormatBool(view.Available),
		},
	)
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) automaticUpdateSettingsHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, m.automaticUpdateView())
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "GET, POST")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var body struct {
		ManagerEnabled       bool   `json:"manager_enabled"`
		SteamEnabled         bool   `json:"steam_enabled"`
		ManagerRestartPolicy string `json:"manager_restart_policy"`
		ManagerScheduledTime string `json:"manager_scheduled_time"`
		SteamRestartPolicy   string `json:"steam_restart_policy"`
		SteamScheduledTime   string `json:"steam_scheduled_time"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	current := m.automaticUpdateView()
	if body.ManagerRestartPolicy == "" {
		body.ManagerRestartPolicy = current.ManagerRestartPolicy
	}
	if body.ManagerScheduledTime == "" {
		body.ManagerScheduledTime = current.ManagerScheduledTime
	}
	if body.SteamRestartPolicy == "" {
		body.SteamRestartPolicy = current.SteamRestartPolicy
	}
	if body.SteamScheduledTime == "" {
		body.SteamScheduledTime = current.SteamScheduledTime
	}
	view, err := m.setAutomaticUpdateSettings(
		body.ManagerEnabled,
		body.SteamEnabled,
		body.ManagerRestartPolicy,
		body.ManagerScheduledTime,
		body.SteamRestartPolicy,
		body.SteamScheduledTime,
	)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"automatic_update_settings_saved",
		map[string]string{
			"manager_enabled":        strconv.FormatBool(body.ManagerEnabled),
			"manager_restart_policy": body.ManagerRestartPolicy,
			"manager_scheduled_time": body.ManagerScheduledTime,
			"steam_enabled":          strconv.FormatBool(body.SteamEnabled),
			"steam_restart_policy":   body.SteamRestartPolicy,
			"steam_scheduled_time":   body.SteamScheduledTime,
		},
	)
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) scheduledServerRestartSettingsHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, m.scheduledServerRestartView())
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "GET, POST")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var body struct {
		Enabled       bool     `json:"enabled"`
		ScheduledTime string   `json:"scheduled_time"`
		Weekdays      []string `json:"weekdays"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.setScheduledServerRestartSettings(
		body.Enabled,
		body.ScheduledTime,
		body.Weekdays,
	)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"scheduled_server_restart_settings_saved",
		map[string]string{
			"enabled":        strconv.FormatBool(body.Enabled),
			"scheduled_time": body.ScheduledTime,
			"weekdays":       strings.Join(view.Weekdays, ","),
		},
	)
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) managerUpdateStatusHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.currentManagerUpdateView()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) managerUpdateCheckHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	view, err := m.checkManagerUpdate(request.Context())
	if err != nil {
		m.auditRequestEvent(
			request,
			session.Username,
			"manager_update_check_failed",
			map[string]string{"error": boundedManagerUpdateText(err.Error(), 256)},
		)
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"manager_update_checked",
		map[string]string{
			"installed_version": view.InstalledVersion,
			"latest_version":    view.LatestVersion,
			"available":         strconv.FormatBool(view.Available),
		},
	)
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) managerUpdateApplyHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var body struct {
		TargetVersion string `json:"target_version"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	clientIP, err := requestIP(request)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid client IP")
		return
	}
	view, err := m.beginManagerUpdate(
		body.TargetVersion,
		session.Username,
		clientIP.String(),
	)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, errManagerUpdateBusy),
			errors.Is(err, errManagerUpdateUnavailable),
			errors.Is(err, errManagerUpdateLifecycleBusy):
			status = http.StatusConflict
		case errors.Is(err, errManagerUpdateStale):
			status = http.StatusBadRequest
		}
		m.auditRequestEvent(
			request,
			session.Username,
			"manager_update_request_failed",
			map[string]string{
				"target_version": body.TargetVersion,
				"error":          boundedManagerUpdateText(err.Error(), 256),
			},
		)
		writeError(response, status, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"manager_update_requested",
		map[string]string{"target_version": body.TargetVersion},
	)
	writeJSON(response, http.StatusAccepted, view)
}

func (m *Manager) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		response.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(response, request)
	})
}

func (m *Manager) accessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ip, err := requestIP(request)
		if err != nil || !m.isAccessAllowed(ip) {
			http.Error(response, "Access denied by the MORDHAU manager network policy.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (m *Manager) withSession(handler func(http.ResponseWriter, *http.Request, Session)) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, ok := m.sessionForRequest(request)
		if !ok {
			if strings.HasPrefix(request.URL.Path, "/api/") {
				writeError(response, http.StatusUnauthorized, "authentication required")
			} else {
				http.Redirect(response, request, "/login", http.StatusSeeOther)
			}
			return
		}
		handler(response, request, session)
	}
}

func (m *Manager) indexHandler(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(response, request)
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := m.sessionForRequest(request); !ok {
		http.Redirect(response, request, "/login", http.StatusSeeOther)
		return
	}
	serveEmbedded(response, "static/index.html", "text/html; charset=utf-8")
}

func (m *Manager) staticHandler(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(request.URL.Path, "/")
	switch name {
	case "static/app.css":
		response.Header().Set("Cache-Control", "no-store")
		serveEmbedded(response, name, "text/css; charset=utf-8")
	case "static/app.js", "static/theme.js":
		response.Header().Set("Cache-Control", "no-store")
		serveEmbedded(response, name, "application/javascript; charset=utf-8")
	default:
		http.NotFound(response, request)
	}
}

func serveEmbedded(response http.ResponseWriter, name, contentType string) {
	data, err := fs.ReadFile(staticFiles, name)
	if err != nil {
		http.Error(response, "asset unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", contentType)
	if strings.HasSuffix(name, ".html") {
		response.Header().Set("Cache-Control", "no-store")
	}
	_, _ = response.Write(data)
}

func (m *Manager) loginHandler(response http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		if _, ok := m.sessionForRequest(request); ok {
			http.Redirect(response, request, "/", http.StatusSeeOther)
			return
		}
		m.renderLogin(response, "")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "GET, POST")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !stateChangeOriginAllowed(request) {
		m.auditRequestEvent(request, "unauthenticated", "login_blocked", map[string]string{
			"reason": "cross_site",
		})
		http.Error(response, "cross-site login request denied", http.StatusForbidden)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 16<<10)
	if err := request.ParseForm(); err != nil {
		m.auditRequestEvent(request, "unauthenticated", "login_failed", map[string]string{
			"reason": "invalid_form",
		})
		m.renderLogin(response, "Invalid login request.")
		return
	}
	username := strings.TrimSpace(request.FormValue("username"))
	password := request.FormValue("password")
	ip, err := requestIP(request)
	if err != nil {
		m.auditRequestEvent(request, username, "login_failed", map[string]string{
			"reason": "invalid_client_address",
		})
		http.Error(response, "invalid client address", http.StatusBadRequest)
		return
	}
	ipText := ip.String()
	now := time.Now()
	if !m.loginPermitted(ipText, now) {
		m.auditRequestEvent(request, username, "login_rate_limited", nil)
		m.renderLogin(response, "Too many login attempts. Try again later.")
		return
	}
	if !m.authenticate(username, password) {
		m.recordLoginFailure(ipText, now)
		m.auditRequestEvent(request, username, "login_failed", map[string]string{
			"reason": "invalid_credentials",
		})
		time.Sleep(250 * time.Millisecond)
		m.renderLogin(response, "Invalid username or password.")
		return
	}
	m.clearLoginFailures(ipText)
	remember := request.FormValue("remember") == "on"
	token, session, err := m.createSession(username, remember)
	if err != nil {
		m.auditRequestEvent(request, username, "login_failed", map[string]string{
			"reason": "session_creation_failed",
		})
		http.Error(response, "failed to create session", http.StatusInternalServerError)
		return
	}
	cookie := &http.Cookie{
		Name:     "mordhau_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	}
	if remember {
		cookie.Expires = session.ExpiresAt
		cookie.MaxAge = int(time.Until(session.ExpiresAt).Seconds())
	}
	http.SetCookie(response, cookie)
	m.auditRequestEvent(request, username, "login_succeeded", map[string]string{
		"remember": strconv.FormatBool(remember),
	})
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (m *Manager) renderLogin(response http.ResponseWriter, message string) {
	data, err := fs.ReadFile(staticFiles, "static/login.html")
	if err != nil {
		http.Error(response, "login page unavailable", http.StatusInternalServerError)
		return
	}
	page := strings.ReplaceAll(string(data), "{{ERROR}}", html.EscapeString(message))
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(page))
}

func (m *Manager) logoutHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		http.Error(response, "invalid CSRF token", http.StatusForbidden)
		return
	}
	cookie, _ := request.Cookie("mordhau_session")
	if cookie != nil {
		m.deleteSession(cookie.Value)
	}
	m.auditRequestEvent(request, session.Username, "logout", nil)
	http.SetCookie(response, &http.Cookie{
		Name:     "mordhau_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	response.WriteHeader(http.StatusNoContent)
}

func (m *Manager) meHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ip, _ := requestIP(request)
	writeJSON(response, http.StatusOK, map[string]any{
		"username":   session.Username,
		"csrf":       session.CSRF,
		"current_ip": ip.String(),
	})
}

func (m *Manager) snapshotHandler(response http.ResponseWriter, request *http.Request, _ Session) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(response, http.StatusOK, m.snapshot())
}

func (m *Manager) runtimeStatusHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(response, http.StatusOK, m.runtimeStatusView())
}

func runtimeBridgeHTTPStatus(err error) int {
	if errors.Is(err, errRuntimeBridgeUnavailable) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, errInvalidRuntimeRequest) {
		return http.StatusBadRequest
	}
	var bridgeErr *runtimeBridgeProtocolError
	if !errors.As(err, &bridgeErr) {
		if errors.Is(err, context.Canceled) {
			return http.StatusRequestTimeout
		}
		return http.StatusBadGateway
	}
	switch bridgeErr.Code {
	case "target_not_found", "property_not_found":
		return http.StatusNotFound
	case "stale_value":
		return http.StatusConflict
	case "invalid_value", "invalid_request", "property_type_unavailable":
		return http.StatusBadRequest
	case "response_too_large":
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusConflict
	}
}

func (m *Manager) runtimeTargetHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.runtimeTarget(
		request.Context(),
		request.URL.Query().Get("id"),
	)
	if err != nil {
		writeError(response, runtimeBridgeHTTPStatus(err), err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) runtimePropertyHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change runtimePropertyChangeRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.changeRuntimeProperty(request.Context(), change)
	if err != nil {
		m.auditRequestEvent(
			request,
			session.Username,
			"runtime_property_change_failed",
			map[string]string{
				"target_id":       change.TargetID,
				"declaring_class": change.DeclaringClass,
				"property":        change.Name,
				"array_index":     strconv.Itoa(change.ArrayIndex),
				"error":           err.Error(),
			},
		)
		writeError(response, runtimeBridgeHTTPStatus(err), err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"runtime_property_changed",
		map[string]string{
			"target_id":             view.Target.ID,
			"target_kind":           view.Target.Kind,
			"target_class":          view.Target.Class,
			"declaring_class":       view.Property.DeclaringClass,
			"property":              view.Property.Name,
			"array_index":           strconv.Itoa(view.Property.ArrayIndex),
			"replication_scope":     view.Property.Replication.Scope,
			"replication_condition": view.Property.Replication.Condition,
		},
	)
	writeJSON(response, http.StatusOK, view)
}

func playerHTTPStatus(err error) int {
	switch {
	case errors.Is(err, errPlayerInvalid),
		errors.Is(err, errPlayerCommentInvalid):
		return http.StatusBadRequest
	case errors.Is(err, errPlayerNotFound):
		return http.StatusNotFound
	case errors.Is(err, errPlayerCommentLimit):
		return http.StatusConflict
	case errors.Is(err, errPlayerServerStopped):
		return http.StatusConflict
	case errors.Is(err, errPlayerRestrictionSync):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func (m *Manager) playersHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = m.refreshPlayerRestrictions(false)
	writeJSON(response, http.StatusOK, m.playersView())
}

func (m *Manager) playerDetailHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	detail, err := m.playerDetail(request.URL.Query().Get("playfab_id"))
	if err != nil {
		writeError(response, playerHTTPStatus(err), err.Error())
		return
	}
	writeJSON(response, http.StatusOK, detail)
}

func (m *Manager) playerRestrictionHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change playerRestrictionRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	detail, err := m.setPlayerRestrictionWithOptions(
		change.PlayFabID,
		change.Restriction,
		change.Enabled,
		change.DurationMinutes,
		change.Reason,
		session.Username,
	)
	reason := strings.TrimSpace(change.Reason)
	auditDetails := map[string]string{
		"playfab_id":       change.PlayFabID,
		"restriction":      change.Restriction,
		"enabled":          strconv.FormatBool(change.Enabled),
		"duration_minutes": strconv.Itoa(change.DurationMinutes),
		"reason_present":   strconv.FormatBool(reason != ""),
		"reason_characters": strconv.Itoa(
			utf8.RuneCountInString(reason),
		),
	}
	if err != nil {
		auditDetails["error"] = err.Error()
		m.auditRequestEvent(
			request,
			session.Username,
			"player_restriction_change_failed",
			auditDetails,
		)
		writeError(response, playerHTTPStatus(err), err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"player_restriction_changed",
		auditDetails,
	)
	writeJSON(response, http.StatusOK, detail)
}

func (m *Manager) playerActionHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change playerActionRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	detail, err := m.playerAction(
		change.PlayFabID,
		change.Action,
		change.Reason,
		change.Message,
	)
	reason := strings.TrimSpace(change.Reason)
	auditDetails := map[string]string{
		"playfab_id":     change.PlayFabID,
		"action":         change.Action,
		"reason_present": strconv.FormatBool(reason != ""),
		"reason_characters": strconv.Itoa(
			utf8.RuneCountInString(reason),
		),
	}
	if change.Action == "warn" {
		auditDetails["message_characters"] = strconv.Itoa(
			utf8.RuneCountInString(strings.TrimSpace(change.Message)),
		)
	}
	if err != nil {
		auditDetails["error"] = safeAuditText(err.Error(), 200)
		m.auditRequestEvent(
			request,
			session.Username,
			"player_action_failed",
			auditDetails,
		)
		writeError(response, playerHTTPStatus(err), err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"player_action_completed",
		auditDetails,
	)
	writeJSON(response, http.StatusOK, detail)
}

func (m *Manager) playerCommentHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var comment playerCommentRequest
	if err := decodeJSON(response, request, &comment); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	detail, err := m.addPlayerComment(
		comment.PlayFabID,
		session.Username,
		comment.Body,
	)
	if err != nil {
		m.auditRequestEvent(
			request,
			session.Username,
			"player_comment_add_failed",
			map[string]string{
				"playfab_id": comment.PlayFabID,
				"error":      err.Error(),
			},
		)
		writeError(response, playerHTTPStatus(err), err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"player_comment_added",
		map[string]string{
			"playfab_id": comment.PlayFabID,
			"characters": strconv.Itoa(utf8.RuneCountInString(comment.Body)),
			"utf8_bytes": strconv.Itoa(len(comment.Body)),
		},
	)
	writeJSON(response, http.StatusCreated, detail)
}

func (m *Manager) eventsHandler(response http.ResponseWriter, request *http.Request, _ Session) {
	if request.Method != http.MethodGet {
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
	send := func() bool {
		data, err := json.Marshal(m.snapshot())
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(response, "event: snapshot\ndata: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send() {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

func (m *Manager) serverActionHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	switch body.Action {
	case "start", "stop", "restart", "update":
	default:
		writeError(response, http.StatusBadRequest, "unsupported server action")
		return
	}
	if m.managerUpdateRunning() {
		writeError(
			response,
			http.StatusConflict,
			"a manager update is already running",
		)
		return
	}
	if err := m.requestOperation(
		body.Action,
		session.Username,
		auditClientIP(request),
		auditPeerIP(request),
	); err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "server_action_requested", map[string]string{
		"action": body.Action,
	})
	writeJSON(response, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (m *Manager) recoverySettingsHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, m.recoveryView())
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "GET, POST")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var body struct {
		Enabled       *bool `json:"enabled"`
		MaxAttempts   *int  `json:"max_attempts"`
		WindowMinutes *int  `json:"window_minutes"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.setRecoverySettings(
		body.Enabled,
		body.MaxAttempts,
		body.WindowMinutes,
	)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "crash_recovery_settings_changed",
		map[string]string{
			"enabled":        strconv.FormatBool(view.Settings.Enabled),
			"max_attempts":   strconv.Itoa(view.Settings.MaxAttempts),
			"window_minutes": strconv.Itoa(view.Settings.WindowMinutes),
		})
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) recoveryRetryHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	view, err := m.retryRecoveryNow()
	if err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "crash_recovery_retry_reset", nil)
	writeJSON(response, http.StatusAccepted, view)
}

func (m *Manager) monitoringHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(response, http.StatusOK, m.monitoringView())
}

func (m *Manager) monitoringSettingsHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change monitoringSettingsRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.setMonitoringSettings(change)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"monitoring_settings_changed",
		map[string]string{
			"webhook_configured": strconv.FormatBool(view.Settings.WebhookConfigured),
			"log_size_mib":       strconv.Itoa(view.Settings.LogSizeMiB),
			"log_backups":        strconv.Itoa(view.Settings.LogBackups),
		},
	)
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) monitoringWebhookTestHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	if err := m.testMonitoringWebhook(); err != nil {
		m.auditRequestEvent(
			request,
			session.Username,
			"monitoring_webhook_test_failed",
			map[string]string{"error": safeAuditText(err.Error(), 160)},
		)
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"monitoring_webhook_test_succeeded",
		nil,
	)
	writeJSON(response, http.StatusOK, m.monitoringView())
}

func (m *Manager) monitoringMetricsHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(
		response,
		http.StatusOK,
		m.metricsHistoryView(request.URL.Query().Get("range")),
	)
}

func (m *Manager) monitoringLogsHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query, err := parseLogSearchQuery(request, logSearchMaximumLimit)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.searchManagedLogs(request.Context(), query)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) monitoringLogsExportHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query, err := parseLogSearchQuery(request, logExportMaximumLimit)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if request.URL.Query().Get("limit") == "" {
		query.Limit = logExportMaximumLimit
	}
	view, err := m.searchManagedLogs(request.Context(), query)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	response.Header().Set(
		"Content-Disposition",
		`attachment; filename="mordhau-control-logs.jsonl"`,
	)
	encoder := json.NewEncoder(response)
	encoder.SetEscapeHTML(false)
	for _, record := range view.Records {
		if err := encoder.Encode(record); err != nil {
			return
		}
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"monitoring_logs_exported",
		map[string]string{
			"source":  query.Source,
			"records": strconv.Itoa(len(view.Records)),
		},
	)
}

func (m *Manager) mapCatalogHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.mapCatalog(request.Context())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errMapCatalogUnavailable) {
			status = http.StatusServiceUnavailable
		}
		writeError(response, status, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) mapRotationHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.mapRotationView(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) mapRotationSaveHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change mapRotationSaveRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.saveMapRotation(request.Context(), change)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errRevisionConflict) {
			status = http.StatusConflict
		} else if errors.Is(err, errLifecycleBusy) {
			status = http.StatusLocked
		}
		m.auditRequestEvent(
			request,
			session.Username,
			"map_rotation_save_failed",
			map[string]string{
				"entries": strconv.Itoa(len(change.Entries)),
				"error":   safeAuditText(err.Error(), 200),
			},
		)
		writeError(response, status, err.Error())
		return
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"map_rotation_saved",
		map[string]string{
			"entries": strconv.Itoa(len(change.Entries)),
			"staged":  strconv.FormatBool(view.Staged),
		},
	)
	writeJSON(response, http.StatusOK, view)
}

type mapChangeRequest struct {
	ModeID string `json:"mode_id"`
	Map    string `json:"map"`
}

func (m *Manager) mapChangeHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change mapChangeRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if change.ModeID == "" || len(change.ModeID) > 64 ||
		!validMordhauObjectName(change.Map) {
		writeError(response, http.StatusBadRequest, errMapSelectionInvalid.Error())
		return
	}
	view, err := m.mapCatalog(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, err.Error())
		return
	}
	mode, selectedMap, ok := findMapSelection(view, change.ModeID, change.Map)
	if !ok {
		m.auditRequestEvent(
			request,
			session.Username,
			"map_change_rejected",
			map[string]string{
				"mode_id": change.ModeID,
				"map":     change.Map,
				"reason":  errMapSelectionInvalid.Error(),
			},
		)
		writeError(response, http.StatusBadRequest, errMapSelectionInvalid.Error())
		return
	}
	if !m.mapGameServerRunning() {
		writeError(response, http.StatusConflict, "the dedicated server is not running")
		return
	}

	command := "changelevel " + selectedMap.Name
	m.rconCommandMu.Lock()
	defer m.rconCommandMu.Unlock()

	execute := m.runRCONCommand
	if m.rconCommandExecute != nil {
		execute = m.rconCommandExecute
	}
	result, err := execute(command)
	if err == nil {
		for _, line := range result.Lines {
			if strings.Contains(strings.ToLower(line), "failed to change level") {
				err = errors.New(line)
				break
			}
		}
	}
	if err != nil {
		m.addRCONEvent("command-error", "Map change failed: "+err.Error())
		m.auditRequestEvent(
			request,
			session.Username,
			"map_change_failed",
			map[string]string{
				"map":       selectedMap.Name,
				"game_mode": mode.Class,
				"source":    selectedMap.Source,
				"error":     err.Error(),
			},
		)
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	m.addRCONEvent(
		"command",
		session.Username+" changed map to "+selectedMap.Name+" · "+mode.Name,
	)
	for _, line := range result.Lines {
		m.addRCONEvent("response", line)
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"map_change_executed",
		map[string]string{
			"map":       selectedMap.Name,
			"game_mode": mode.Class,
			"source":    selectedMap.Source,
		},
	)
	writeJSON(response, http.StatusOK, map[string]any{
		"status":    "accepted",
		"map":       selectedMap,
		"game_mode": mode,
	})
}

func (m *Manager) rconMessageHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.sendUnicodeRCONMessage(body.Message); err != nil {
		status := http.StatusConflict
		if errors.Is(err, errInvalidUnicodeMessage) {
			status = http.StatusBadRequest
		}
		writeError(response, status, err.Error())
		return
	}

	m.addRCONEvent("outbound", session.Username+": "+body.Message)
	m.publishFleetEvent(
		FleetEventWebSAY,
		"",
		body.Message,
	)
	m.auditRequestEvent(request, session.Username, "unicode_server_message_sent", map[string]string{
		"characters": strconv.Itoa(utf8.RuneCountInString(body.Message)),
		"utf8_bytes": strconv.Itoa(len(body.Message)),
	})
	writeJSON(response, http.StatusOK, map[string]string{"status": "sent"})
}

func (m *Manager) rconCommandHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var body struct {
		Command string `json:"command"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}

	command, err := normalizeRCONCommand(body.Command)
	if err != nil {
		m.auditRequestEvent(request, session.Username, "rcon_command_rejected", map[string]string{
			"reason": err.Error(),
		})
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	commandFields := strings.Fields(command)
	details := map[string]string{
		"command_name": strings.ToLower(commandFields[0]),
		"characters":   strconv.Itoa(utf8.RuneCountInString(command)),
		"utf8_bytes":   strconv.Itoa(len(command)),
	}

	m.rconCommandMu.Lock()
	defer m.rconCommandMu.Unlock()

	events := make([]RCONEvent, 0, 3)
	if event, added := m.addRCONEvent(
		"command",
		session.Username+" > "+command,
	); added {
		events = append(events, event)
	}

	execute := m.runRCONCommand
	if m.rconCommandExecute != nil {
		execute = m.rconCommandExecute
	}
	result, err := execute(command)
	if err != nil {
		if event, added := m.addRCONEvent(
			"command-error",
			"RCON command failed: "+err.Error(),
		); added {
			events = append(events, event)
		}
		details["error"] = err.Error()
		m.auditRequestEvent(request, session.Username, "rcon_command_failed", details)
		writeError(response, http.StatusConflict, err.Error())
		return
	}

	if len(result.Lines) == 0 {
		if event, added := m.addRCONEvent(
			"response",
			"(no response text)",
		); added {
			events = append(events, event)
		}
	} else {
		for _, line := range result.Lines {
			if event, added := m.addRCONEvent("response", line); added {
				events = append(events, event)
			}
		}
	}
	if result.Truncated {
		if event, added := m.addRCONEvent(
			"response",
			"(response truncated by the web output limit)",
		); added {
			events = append(events, event)
		}
	}

	details["response_lines"] = strconv.Itoa(len(result.Lines))
	details["response_truncated"] = strconv.FormatBool(result.Truncated)
	if message, ok := rconSAYMessage(command); ok {
		m.publishFleetEvent(
			FleetEventRCONSAY,
			"",
			message,
		)
	}
	m.auditRequestEvent(request, session.Username, "rcon_command_executed", details)
	events = m.rconEventsForView(events)
	writeJSON(response, http.StatusOK, map[string]any{
		"status":             "executed",
		"response_lines":     len(result.Lines),
		"response_truncated": result.Truncated,
		"events":             events,
	})
}

func (m *Manager) rconHistoryHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"events": m.rconHistory(rconBrowserHistoryLimit),
	})
}

func (m *Manager) languageHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var body struct {
		Language string `json:"language"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.setLanguage(body.Language); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "language_changed", map[string]string{
		"language": body.Language,
	})
	writeJSON(response, http.StatusOK, map[string]string{"language": body.Language})
}

func (m *Manager) configHandler(response http.ResponseWriter, request *http.Request, _ Session) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.configView(request.URL.Query().Get("file"))
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) configMutationHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var mutation ConfigMutation
	if err := decodeJSON(response, request, &mutation); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.mutateConfig(mutation)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errRevisionConflict) || errors.Is(err, errLifecycleBusy) {
			status = http.StatusConflict
		}
		writeError(response, status, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "configuration_changed",
		configMutationAuditDetails(mutation, view.Staged))
	if mutation.File == "Game.ini" {
		_, _ = m.refreshModCacheAfterConfigurationChange()
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) configDiscardHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	if err := m.discardPending(); err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "pending_configuration_discarded", nil)
	_, _ = m.refreshModCacheAfterConfigurationChange()
	response.WriteHeader(http.StatusNoContent)
}

func (m *Manager) modsHandler(response http.ResponseWriter, request *http.Request, _ Session) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.cachedModManagementView()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) customPaksHandler(
	response http.ResponseWriter,
	request *http.Request,
	_ Session,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.customPaksView()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func customPakHTTPStatus(err error) int {
	var maximumBodyError *http.MaxBytesError
	switch {
	case errors.As(err, &maximumBodyError), errors.Is(err, errCustomPakTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, errCustomPakStorage):
		return http.StatusInsufficientStorage
	case errors.Is(err, errCustomPakNotFound):
		return http.StatusNotFound
	case errors.Is(err, errLifecycleBusy), errors.Is(err, errCustomPakConflict):
		return http.StatusConflict
	case errors.Is(err, errCustomPakInvalid),
		errors.Is(err, errCustomPakProtected),
		errors.Is(err, errCustomPakEmpty),
		errors.Is(err, errCustomPakDeleteAbsent):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func (m *Manager) customPakUploadHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	_ = http.NewResponseController(response).SetReadDeadline(time.Now().Add(2 * time.Hour))
	request.Body = http.MaxBytesReader(
		response,
		request.Body,
		customPakMaximumUploadBytes+(2<<20),
	)
	reader, err := request.MultipartReader()
	if err != nil {
		writeError(response, http.StatusBadRequest, "upload must use multipart form data")
		return
	}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			writeError(response, http.StatusBadRequest, "a PAK file is required")
			return
		}
		if nextErr != nil {
			writeError(response, customPakHTTPStatus(nextErr), nextErr.Error())
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_ = part.Close()
			continue
		}
		name := part.FileName()
		view, written, uploadErr := m.stageCustomPakUpload(name, part)
		_ = part.Close()
		if uploadErr != nil {
			m.auditRequestEvent(request, session.Username, "custompak_upload_failed",
				map[string]string{
					"name":  name,
					"error": uploadErr.Error(),
				})
			writeError(response, customPakHTTPStatus(uploadErr), uploadErr.Error())
			return
		}
		m.auditRequestEvent(request, session.Username, "custompak_uploaded",
			map[string]string{
				"name":  name,
				"bytes": strconv.FormatInt(written, 10),
			})
		writeJSON(response, http.StatusCreated, view)
		return
	}
}

type customPakEnabledRequest struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (m *Manager) customPakEnabledHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change customPakEnabledRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.setCustomPakEnabled(change.Name, change.Enabled)
	if err != nil {
		writeError(response, customPakHTTPStatus(err), err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "custompak_state_staged",
		map[string]string{
			"name":    change.Name,
			"enabled": strconv.FormatBool(change.Enabled),
		})
	writeJSON(response, http.StatusOK, view)
}

type customPakNameRequest struct {
	Name string `json:"name"`
}

func (m *Manager) customPakDeleteHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change customPakNameRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.stageCustomPakDeletion(change.Name)
	if err != nil {
		writeError(response, customPakHTTPStatus(err), err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "custompak_deletion_staged",
		map[string]string{"name": change.Name})
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) customPakDeleteCancelHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid CSRF token")
		return
	}
	var change customPakNameRequest
	if err := decodeJSON(response, request, &change); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.cancelCustomPakDeletion(change.Name)
	if err != nil {
		writeError(response, customPakHTTPStatus(err), err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "custompak_deletion_canceled",
		map[string]string{"name": change.Name})
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) modRefreshHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	view, err := m.refreshModCache()
	result := "completed"
	if err != nil || view.Refresh.LastError != "" {
		result = "failed"
	}
	m.auditRequestEvent(request, session.Username, "mod_metadata_refresh_requested",
		map[string]string{"result": result})
	if err != nil {
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) modRefreshSettingsHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		Minutes         *int    `json:"minutes"`
		RestartOnUpdate *bool   `json:"restart_on_update"`
		RestartPolicy   *string `json:"restart_policy"`
		ScheduledTime   *string `json:"scheduled_time"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.setModRefreshSettingsWithPolicy(
		body.Minutes,
		body.RestartOnUpdate,
		body.RestartPolicy,
		body.ScheduledTime,
	)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	details := make(map[string]string)
	if body.Minutes != nil {
		details["minutes"] = strconv.Itoa(*body.Minutes)
	}
	if body.RestartOnUpdate != nil {
		details["restart_on_update"] = strconv.FormatBool(*body.RestartOnUpdate)
	}
	if body.RestartPolicy != nil {
		details["restart_policy"] = *body.RestartPolicy
	}
	if body.ScheduledTime != nil {
		details["scheduled_time"] = *body.ScheduledTime
	}
	m.auditRequestEvent(
		request,
		session.Username,
		"mod_refresh_settings_changed",
		details,
	)
	writeJSON(response, http.StatusOK, view)
}

func (m *Manager) modPlanHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		Reference string `json:"reference"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := m.modInstallPlan(body.Reference)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, plan)
}

func (m *Manager) modAddHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		Reference string `json:"reference"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := m.modInstallPlan(body.Reference)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	change, err := m.addConfiguredMods(planModIDs(plan))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errLifecycleBusy) {
			status = http.StatusConflict
		}
		writeError(response, status, err.Error())
		return
	}
	_, _ = m.refreshModCacheAfterConfigurationChange()
	m.auditRequestEvent(request, session.Username, "mod_configuration_changed", map[string]string{
		"action":           "add",
		"mod_id":           strconv.Itoa(plan.Target.ID),
		"dependency_count": strconv.Itoa(len(plan.Dependencies)),
		"added_ids":        formatAuditIDs(change.Added),
		"reenabled_ids":    formatAuditIDs(change.Reenabled),
		"changed":          strconv.FormatBool(change.Changed),
		"staged":           strconv.FormatBool(change.Staged),
	})
	writeJSON(response, http.StatusOK, change)
}

func (m *Manager) modEnabledHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		ID      int  `json:"id"`
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	change, err := m.setConfiguredModEnabled(body.ID, body.Enabled)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errLifecycleBusy) {
			status = http.StatusConflict
		}
		writeError(response, status, err.Error())
		return
	}
	_, _ = m.refreshModCacheAfterConfigurationChange()
	m.auditRequestEvent(request, session.Username, "mod_configuration_changed", map[string]string{
		"action":  "set_enabled",
		"mod_id":  strconv.Itoa(body.ID),
		"enabled": strconv.FormatBool(body.Enabled),
		"staged":  strconv.FormatBool(change.Staged),
	})
	writeJSON(response, http.StatusOK, change)
}

func (m *Manager) modRemoveHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		ID        int    `json:"id"`
		RemoveIDs []int  `json:"remove_ids"`
		Revision  uint64 `json:"revision"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.cachedModManagementView()
	if err != nil {
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	if body.Revision != 0 && body.Revision != view.Revision {
		writeError(response, http.StatusConflict, "mod metadata changed; inspect the removal plan again")
		return
	}
	plan, err := buildModRemovalPlan(view, body.ID)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	allowed := make(map[int]bool, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		allowed[candidate.ID] = true
	}
	removeIDs := body.RemoveIDs
	if len(removeIDs) == 0 {
		removeIDs = []int{body.ID}
	}
	if len(removeIDs) > len(plan.Candidates) {
		writeError(response, http.StatusBadRequest, "removal selection contains too many entries")
		return
	}
	targetIncluded := false
	seen := make(map[int]bool, len(removeIDs))
	for _, id := range removeIDs {
		if !allowed[id] || seen[id] {
			writeError(response, http.StatusBadRequest, "removal selection is not part of the current dependency plan")
			return
		}
		seen[id] = true
		targetIncluded = targetIncluded || id == body.ID
	}
	if !targetIncluded {
		writeError(response, http.StatusBadRequest, "removal selection must include the selected mod")
		return
	}
	change, err := m.removeConfiguredMods(removeIDs)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errLifecycleBusy) {
			status = http.StatusConflict
		}
		writeError(response, status, err.Error())
		return
	}
	_, _ = m.refreshModCacheAfterConfigurationChange()
	m.auditRequestEvent(request, session.Username, "mod_configuration_changed", map[string]string{
		"action":      "remove",
		"mod_id":      strconv.Itoa(body.ID),
		"removed_ids": formatAuditIDs(change.Removed),
		"staged":      strconv.FormatBool(change.Staged),
	})
	writeJSON(response, http.StatusOK, change)
}

func (m *Manager) modRemovePlanHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.cachedModManagementView()
	if err != nil {
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	plan, err := buildModRemovalPlan(view, body.ID)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, plan)
}

func (m *Manager) modIOSettingsHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		APIKey  string `json:"api_key"`
		APIBase string `json:"api_base"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := m.saveModIOSettings(body.APIKey, body.APIBase)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	_, _ = m.refreshModCacheAfterConfigurationChange()
	m.auditRequestEvent(request, session.Username, "modio_settings_saved", map[string]string{
		"api_base": settings.APIBase,
		"game_id":  strconv.Itoa(settings.GameID),
	})
	writeJSON(response, http.StatusOK, settings)
}

func (m *Manager) modIOSettingsClearHandler(
	response http.ResponseWriter,
	request *http.Request,
	session Session,
) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	disabled := false
	if _, err := m.setModRefreshSettings(nil, &disabled); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := m.clearModIOSettings(); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = m.refreshModCacheAfterConfigurationChange()
	m.auditRequestEvent(request, session.Username, "modio_settings_cleared", nil)
	response.WriteHeader(http.StatusNoContent)
}

func (m *Manager) accountsHandler(response http.ResponseWriter, request *http.Request, _ Session) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m.mu.RLock()
	accounts := m.publicAccountsLocked()
	m.mu.RUnlock()
	writeJSON(response, http.StatusOK, map[string]any{"accounts": accounts})
}

func (m *Manager) accountCreateHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.createAccount(body.Username, body.Password); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "account_created", map[string]string{
		"target_account": body.Username,
	})
	response.WriteHeader(http.StatusCreated)
}

func (m *Manager) accountEditHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		OldUsername string `json:"old_username"`
		Username    string `json:"username"`
		Password    string `json:"password"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.editAccount(body.OldUsername, body.Username, body.Password); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "account_edited", map[string]string{
		"old_account":      body.OldUsername,
		"new_account":      body.Username,
		"password_changed": strconv.FormatBool(body.Password != ""),
	})
	response.WriteHeader(http.StatusNoContent)
}

func (m *Manager) accountDeleteHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.deleteAccount(body.Username); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "account_deleted", map[string]string{
		"target_account": body.Username,
	})
	response.WriteHeader(http.StatusNoContent)
}

func (m *Manager) accessHandler(response http.ResponseWriter, request *http.Request, _ Session) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ip, _ := requestIP(request)
	writeJSON(response, http.StatusOK, map[string]any{
		"config":     m.accessConfig(),
		"current_ip": ip.String(),
	})
}

func (m *Manager) accessBaseHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		Policy string `json:"policy"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	ip, err := requestIP(request)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid client IP")
		return
	}
	if err := m.setBasePolicy(body.Policy, ip); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "access_base_policy_changed", map[string]string{
		"policy": body.Policy,
	})
	writeJSON(response, http.StatusOK, m.accessConfig())
}

func (m *Manager) accessRuleHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		ID      string  `json:"id"`
		Action  string  `json:"action"`
		Network string  `json:"network"`
		Comment *string `json:"comment"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := m.saveAccessRule(body.ID, body.Action, body.Network, body.Comment)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	event := "access_rule_created"
	if body.ID != "" {
		event = "access_rule_edited"
	}
	m.auditRequestEvent(request, session.Username, event, map[string]string{
		"rule_id":            rule.ID,
		"action":             rule.Action,
		"network":            rule.Network,
		"comment_changed":    strconv.FormatBool(body.Comment != nil),
		"comment_present":    strconv.FormatBool(rule.Comment != ""),
		"comment_characters": strconv.Itoa(utf8.RuneCountInString(rule.Comment)),
	})
	writeJSON(response, http.StatusOK, m.accessConfig())
}

func (m *Manager) accessRuleDeleteHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.deleteAccessRule(body.ID); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "access_rule_deleted", map[string]string{
		"rule_id": body.ID,
	})
	response.WriteHeader(http.StatusNoContent)
}

func (m *Manager) servicesHandler(response http.ResponseWriter, request *http.Request, _ Session) {
	if request.Method != http.MethodGet {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(response, http.StatusOK, currentServiceSettings())
}

func (m *Manager) serviceModeHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		Service   string `json:"service"`
		Automatic bool   `json:"automatic"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := setServiceAutomatic(body.Service, body.Automatic); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "service_boot_mode_changed", map[string]string{
		"service":   body.Service,
		"automatic": strconv.FormatBool(body.Automatic),
	})
	writeJSON(response, http.StatusOK, currentServiceSettings())
}

func (m *Manager) webPortHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		Port int `json:"port"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := setSavedWebPort(body.Port); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "web_port_changed", map[string]string{
		"port": strconv.Itoa(body.Port),
	})
	writeJSON(response, http.StatusOK, currentServiceSettings())
}

func (m *Manager) serverPortsHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var ports ServerPorts
	if err := decodeJSON(response, request, &ports); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.setSavedServerPorts(ports); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "server_ports_changed", map[string]string{
		"game":   strconv.Itoa(ports.Game),
		"rcon":   strconv.Itoa(ports.RCON),
		"beacon": strconv.Itoa(ports.Beacon),
		"query":  strconv.Itoa(ports.Query),
	})
	writeJSON(response, http.StatusOK, currentServiceSettings())
}

func (m *Manager) startMapHandler(response http.ResponseWriter, request *http.Request, session Session) {
	if request.Method != http.MethodPost || !validCSRF(request, session) {
		writeError(response, http.StatusForbidden, "invalid request")
		return
	}
	var body struct {
		StartMap string `json:"start_map"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := setSavedStartMap(body.StartMap); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	saved := savedStartMap()
	m.auditRequestEvent(request, session.Username, "start_map_changed", map[string]string{
		"configured": strconv.FormatBool(saved != ""),
		"start_map":  saved,
	})
	writeJSON(response, http.StatusOK, currentServiceSettings())
}

func formatAuditIDs(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

func stateChangeOriginAllowed(request *http.Request) bool {
	return !strings.EqualFold(
		strings.TrimSpace(request.Header.Get("Sec-Fetch-Site")),
		"cross-site",
	)
}

func validCSRF(request *http.Request, session Session) bool {
	if !stateChangeOriginAllowed(request) {
		return false
	}
	provided := request.Header.Get("X-CSRF-Token")
	return len(provided) == len(session.CSRF) &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(session.CSRF)) == 1
}

func decodeJSON(response http.ResponseWriter, request *http.Request, value any) error {
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid JSON request")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("request must contain one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON request")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("port must be between 1 and 65535")
	}
	return port, nil
}
