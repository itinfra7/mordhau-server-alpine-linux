package manager

import (
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
	mux.HandleFunc("/api/me", m.withSession(m.meHandler))
	mux.HandleFunc("/api/snapshot", m.withSession(m.snapshotHandler))
	mux.HandleFunc("/api/events", m.withSession(m.eventsHandler))
	mux.HandleFunc("/api/server/action", m.withSession(m.serverActionHandler))
	mux.HandleFunc("/api/rcon/history", m.withSession(m.rconHistoryHandler))
	mux.HandleFunc("/api/rcon/message", m.withSession(m.rconMessageHandler))
	mux.HandleFunc("/api/language", m.withSession(m.languageHandler))
	mux.HandleFunc("/api/config", m.withSession(m.configHandler))
	mux.HandleFunc("/api/config/mutate", m.withSession(m.configMutationHandler))
	mux.HandleFunc("/api/config/discard", m.withSession(m.configDiscardHandler))
	mux.HandleFunc("/api/mods", m.withSession(m.modsHandler))
	mux.HandleFunc("/api/mods/refresh", m.withSession(m.modRefreshHandler))
	mux.HandleFunc("/api/mods/refresh/settings", m.withSession(m.modRefreshSettingsHandler))
	mux.HandleFunc("/api/mods/plan", m.withSession(m.modPlanHandler))
	mux.HandleFunc("/api/mods/add", m.withSession(m.modAddHandler))
	mux.HandleFunc("/api/mods/enabled", m.withSession(m.modEnabledHandler))
	mux.HandleFunc("/api/mods/remove", m.withSession(m.modRemoveHandler))
	mux.HandleFunc("/api/modio/settings", m.withSession(m.modIOSettingsHandler))
	mux.HandleFunc("/api/modio/settings/clear", m.withSession(m.modIOSettingsClearHandler))
	mux.HandleFunc("/api/accounts", m.withSession(m.accountsHandler))
	mux.HandleFunc("/api/accounts/create", m.withSession(m.accountCreateHandler))
	mux.HandleFunc("/api/accounts/edit", m.withSession(m.accountEditHandler))
	mux.HandleFunc("/api/accounts/delete", m.withSession(m.accountDeleteHandler))
	mux.HandleFunc("/api/access", m.withSession(m.accessHandler))
	mux.HandleFunc("/api/access/base", m.withSession(m.accessBaseHandler))
	mux.HandleFunc("/api/access/rule", m.withSession(m.accessRuleHandler))
	mux.HandleFunc("/api/access/rule/delete", m.withSession(m.accessRuleDeleteHandler))
	mux.HandleFunc("/api/services", m.withSession(m.servicesHandler))
	mux.HandleFunc("/api/services/mode", m.withSession(m.serviceModeHandler))
	mux.HandleFunc("/api/services/web-port", m.withSession(m.webPortHandler))
	mux.HandleFunc("/api/services/server-ports", m.withSession(m.serverPortsHandler))
	mux.HandleFunc("/api/services/start-map", m.withSession(m.startMapHandler))
	return m.securityHeaders(
		m.requestAddressMiddleware(
			m.auditMiddleware(
				m.accessMiddleware(mux),
			),
		),
	)
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
	startOperation := m.startOperation
	if m.operationStart != nil {
		startOperation = m.operationStart
	}
	if err := startOperation(
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
	m.auditRequestEvent(request, session.Username, "unicode_server_message_sent", map[string]string{
		"characters": strconv.Itoa(utf8.RuneCountInString(body.Message)),
		"utf8_bytes": strconv.Itoa(len(body.Message)),
	})
	writeJSON(response, http.StatusOK, map[string]string{"status": "sent"})
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
		Minutes int `json:"minutes"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	view, err := m.setModRefreshInterval(body.Minutes)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	m.auditRequestEvent(request, session.Username, "mod_refresh_interval_changed",
		map[string]string{"minutes": strconv.Itoa(body.Minutes)})
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
		ID int `json:"id"`
	}
	if err := decodeJSON(response, request, &body); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	change, err := m.removeConfiguredMod(body.ID)
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
		"action": "remove",
		"mod_id": strconv.Itoa(body.ID),
		"staged": strconv.FormatBool(change.Staged),
	})
	writeJSON(response, http.StatusOK, change)
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
	if err := setSavedServerPorts(ports); err != nil {
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
