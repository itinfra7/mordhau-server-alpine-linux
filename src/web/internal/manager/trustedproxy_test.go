package manager

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testTrustedProxy = "192.0.2.200/32"

func newTrustedProxyTestManager(t *testing.T, access AccessConfig) *Manager {
	t.Helper()
	root := t.TempDir()
	managerUpdatePath := filepath.Join(root, "manager-update.json")
	if err := writeJSONAtomic(
		managerUpdatePath,
		initialManagerUpdateState(),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	return &Manager{
		access: access,
		trustedProxies: []netip.Prefix{
			netip.MustParsePrefix(testTrustedProxy),
		},
		loginAttempts:          make(map[string]*loginAttempt),
		auditPath:              filepath.Join(root, "mordhau-web.log"),
		managerUpdateStateFile: managerUpdatePath,
	}
}

func newProxyRequest(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, "http://manager.example"+path, body)
	request.RemoteAddr = "192.0.2.200:43210"
	return request
}

func readAuditRecords(t *testing.T, path string) []auditRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	records := make([]auditRecord, 0)
	decoder := json.NewDecoder(file)
	for {
		var record auditRecord
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func TestParseTrustedProxyCanonicalizesAddressesAndPrefixes(t *testing.T) {
	tests := map[string]string{
		"192.0.2.200":               "192.0.2.200/32",
		"192.0.2.200/32":            "192.0.2.200/32",
		"192.0.2.199/24":            "192.0.2.0/24",
		"::ffff:192.0.2.200/128":    "192.0.2.200/32",
		"::ffff:192.0.2.199/120":    "192.0.2.0/24",
		"2001:db8:1234::1/64":       "2001:db8:1234::/64",
		"  2001:db8:1234::1/128 \t": "2001:db8:1234::1/128",
	}
	for input, expected := range tests {
		prefix, err := ParseTrustedProxy(input)
		if err != nil {
			t.Fatalf("ParseTrustedProxy(%q): %v", input, err)
		}
		if prefix.String() != expected {
			t.Fatalf("ParseTrustedProxy(%q) = %q, want %q", input, prefix, expected)
		}
	}

	for _, input := range []string{
		"",
		"not-an-address",
		"0.0.0.0/0",
		"::/0",
		"224.0.0.1/32",
		"ff02::1/128",
		"::ffff:192.0.2.200/95",
		"fe80::1%eth0",
	} {
		if _, err := ParseTrustedProxy(input); err == nil {
			t.Fatalf("ParseTrustedProxy(%q) unexpectedly succeeded", input)
		}
	}
}

func TestUntrustedPeerIgnoresAllForwardingHeaders(t *testing.T) {
	manager := newTrustedProxyTestManager(t, AccessConfig{BasePolicy: "all_allow"})
	var clientIP, peerIP netip.Addr
	handler := manager.requestAddressMiddleware(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			var err error
			clientIP, err = requestIP(request)
			if err != nil {
				t.Fatal(err)
			}
			peerIP, err = requestPeerIP(request)
			if err != nil {
				t.Fatal(err)
			}
			response.WriteHeader(http.StatusNoContent)
		},
	))

	request := httptest.NewRequest(http.MethodGet, "http://manager.example/login", nil)
	request.RemoteAddr = "[::ffff:198.51.100.40]:54321"
	request.Header.Add("X-Forwarded-For", "203.0.113.10")
	request.Header.Add("X-Forwarded-For", "malformed")
	request.Header.Set("X-Real-IP", "0.0.0.0")
	request.Header.Set("Forwarded", "for=\"[ff02::1]\"")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("direct request status = %d", response.Code)
	}
	expected := netip.MustParseAddr("198.51.100.40")
	if clientIP != expected || peerIP != expected {
		t.Fatalf("direct addresses = client %s, peer %s", clientIP, peerIP)
	}
}

func TestEmptyTrustedProxyListKeepsDirectAddressing(t *testing.T) {
	manager := &Manager{}
	request := newProxyRequest(http.MethodGet, "/login", nil)
	request.Header.Set("X-Forwarded-For", "198.51.100.10")
	address := manager.resolveRequestAddress(request)
	if address.failureReason != "" ||
		address.trustedProxy ||
		address.clientIP.String() != "192.0.2.200" {
		t.Fatalf("empty trusted-proxy result = %#v", address)
	}
}

func TestTrustedProxyAcceptsCanonicalIPv4AndIPv6(t *testing.T) {
	manager := newTrustedProxyTestManager(t, AccessConfig{BasePolicy: "all_allow"})
	tests := []struct {
		forwarded string
		expected  string
	}{
		{"198.51.100.10", "198.51.100.10"},
		{"2001:db8:1234::10", "2001:db8:1234::10"},
		{"::ffff:198.51.100.11", "198.51.100.11"},
	}
	for _, test := range tests {
		request := newProxyRequest(http.MethodGet, "/login", nil)
		request.Header.Set("X-Forwarded-For", test.forwarded)
		request.Header.Set("X-Real-IP", "203.0.113.200")
		request.Header.Set("Forwarded", "for=203.0.113.201")
		address := manager.resolveRequestAddress(request)
		if address.failureReason != "" {
			t.Fatalf("resolve %q: %s", test.forwarded, address.failureReason)
		}
		if !address.trustedProxy ||
			address.clientIP.String() != test.expected ||
			address.peerIP.String() != "192.0.2.200" {
			t.Fatalf("resolve %q = %#v", test.forwarded, address)
		}
	}
}

func TestTrustedProxyRejectsInvalidForwardingBeforeAuthentication(t *testing.T) {
	tests := []struct {
		name     string
		values   []string
		expected string
	}{
		{"missing", nil, requestAddressMissingForwarded},
		{"empty", []string{""}, requestAddressMissingForwarded},
		{"duplicate", []string{"198.51.100.10", "198.51.100.11"}, requestAddressMultipleForwarded},
		{"comma chain", []string{"198.51.100.10, 198.51.100.11"}, requestAddressChainedForwarded},
		{"malformed", []string{"not-an-address"}, requestAddressInvalidForwarded},
		{"IPv6 zone", []string{"fe80::1%eth0"}, requestAddressInvalidForwarded},
		{"unspecified IPv4", []string{"0.0.0.0"}, requestAddressUnusableForwarded},
		{"unspecified IPv6", []string{"::"}, requestAddressUnusableForwarded},
		{"multicast IPv4", []string{"224.0.0.1"}, requestAddressUnusableForwarded},
		{"multicast IPv6", []string{"ff02::1"}, requestAddressUnusableForwarded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newTrustedProxyTestManager(t, AccessConfig{BasePolicy: "all_allow"})
			request := newProxyRequest(http.MethodGet, "/api/me", nil)
			for _, value := range test.values {
				request.Header.Add("X-Forwarded-For", value)
			}
			response := httptest.NewRecorder()
			manager.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 before authentication", response.Code)
			}
			records := readAuditRecords(t, manager.auditPath)
			if len(records) != 1 {
				t.Fatalf("audit record count = %d", len(records))
			}
			record := records[0]
			if record.Account != "unauthenticated" ||
				record.ClientIP != "" ||
				record.PeerIP != "192.0.2.200" ||
				record.Status != http.StatusBadRequest ||
				record.Details["address_error"] != test.expected {
				t.Fatalf("unexpected rejection audit: %#v", record)
			}
		})
	}
}

func TestAllDenyUsesResolvedClientInsteadOfProxyPeer(t *testing.T) {
	allowedClient := "198.51.100.10"
	manager := newTrustedProxyTestManager(t, AccessConfig{
		BasePolicy: "all_deny",
		Rules: []AccessRule{
			{Action: "allow", Network: allowedClient + "/32"},
		},
	})

	allowedRequest := newProxyRequest(http.MethodGet, "/login", nil)
	allowedRequest.Header.Set("X-Forwarded-For", allowedClient)
	allowedResponse := httptest.NewRecorder()
	manager.Handler().ServeHTTP(allowedResponse, allowedRequest)
	if allowedResponse.Code != http.StatusOK {
		t.Fatalf("allowed client status = %d", allowedResponse.Code)
	}

	deniedRequest := newProxyRequest(http.MethodGet, "/login", nil)
	deniedRequest.Header.Set("X-Forwarded-For", "198.51.100.11")
	deniedResponse := httptest.NewRecorder()
	manager.Handler().ServeHTTP(deniedResponse, deniedRequest)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("denied client status = %d", deniedResponse.Code)
	}

	proxyOnly := newTrustedProxyTestManager(t, AccessConfig{
		BasePolicy: "all_deny",
		Rules: []AccessRule{
			{Action: "allow", Network: testTrustedProxy},
		},
	})
	proxiedRequest := newProxyRequest(http.MethodGet, "/login", nil)
	proxiedRequest.Header.Set("X-Forwarded-For", "198.51.100.12")
	proxiedResponse := httptest.NewRecorder()
	proxyOnly.Handler().ServeHTTP(proxiedResponse, proxiedRequest)
	if proxiedResponse.Code != http.StatusForbidden {
		t.Fatalf("proxy allow rule admitted an unlisted client: %d", proxiedResponse.Code)
	}
}

func TestDirectAccessIgnoresSpoofedForwardingPolicyAddress(t *testing.T) {
	manager := newTrustedProxyTestManager(t, AccessConfig{
		BasePolicy: "all_deny",
		Rules: []AccessRule{
			{Action: "allow", Network: "198.51.100.20/32"},
		},
	})
	request := httptest.NewRequest(http.MethodGet, "http://manager.example/login", nil)
	request.RemoteAddr = "198.51.100.20:54321"
	request.Header.Set("X-Forwarded-For", "203.0.113.99")
	request.Header.Set("X-Real-IP", "203.0.113.99")
	request.Header.Set("Forwarded", "for=203.0.113.99")
	response := httptest.NewRecorder()
	manager.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("direct access status = %d", response.Code)
	}
}

func TestEmergencyAllowTargetsResolvedClient(t *testing.T) {
	manager := newTrustedProxyTestManager(t, AccessConfig{BasePolicy: "all_allow"})
	request := newProxyRequest(http.MethodPost, "/api/access/base", nil)
	request.Header.Set("X-Forwarded-For", "198.51.100.30")
	var config AccessConfig
	handler := manager.requestAddressMiddleware(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			ip, err := requestIP(request)
			if err != nil {
				t.Fatal(err)
			}
			config = AccessConfig{BasePolicy: "all_deny"}
			if err := ensureEmergencyAccess(
				&config,
				ip,
				time.Date(2026, 7, 24, 1, 2, 3, 0, time.UTC),
			); err != nil {
				t.Fatal(err)
			}
			response.WriteHeader(http.StatusNoContent)
		},
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if len(config.Rules) != 1 ||
		config.Rules[0].Network != "198.51.100.30/32" ||
		!config.Rules[0].Temporary {
		t.Fatalf("unexpected emergency rule: %#v", config.Rules)
	}
	if accessAllowed(
		netip.MustParseAddr("192.0.2.200"),
		config,
		time.Date(2026, 7, 24, 1, 3, 0, 0, time.UTC),
	) {
		t.Fatal("emergency access was granted to the proxy peer")
	}
}

func TestProxyAuditRecordsCanonicalClientAndPeerSeparately(t *testing.T) {
	manager := newTrustedProxyTestManager(t, AccessConfig{BasePolicy: "all_allow"})
	request := newProxyRequest(http.MethodGet, "/login", nil)
	request.Header.Set("X-Forwarded-For", "::ffff:198.51.100.40")
	response := httptest.NewRecorder()
	manager.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}

	records := readAuditRecords(t, manager.auditPath)
	if len(records) != 1 {
		t.Fatalf("audit record count = %d", len(records))
	}
	record := records[0]
	if record.ClientIP != "198.51.100.40" ||
		record.PeerIP != "192.0.2.200" ||
		record.Status != http.StatusOK {
		t.Fatalf("unexpected proxy access audit: %#v", record)
	}
}

func TestLoginRateLimitKeysUseResolvedClientAddress(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		expected   string
	}{
		{"proxy", "192.0.2.200:43210", "198.51.100.50", "198.51.100.50"},
		{"direct spoof", "198.51.100.51:43210", "203.0.113.99", "198.51.100.51"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newTrustedProxyTestManager(t, AccessConfig{BasePolicy: "all_allow"})
			form := url.Values{
				"username": {"unknown"},
				"password": {"WrongPassword1"},
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"http://manager.example/login",
				strings.NewReader(form.Encode()),
			)
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("X-Forwarded-For", test.forwarded)
			response := httptest.NewRecorder()
			manager.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("login response status = %d", response.Code)
			}

			manager.loginMu.Lock()
			_, expectedFound := manager.loginAttempts[test.expected]
			_, proxyFound := manager.loginAttempts["192.0.2.200"]
			count := len(manager.loginAttempts)
			manager.loginMu.Unlock()
			if !expectedFound || count != 1 ||
				(test.expected != "192.0.2.200" && proxyFound) {
				t.Fatalf("login attempt keys = %#v", manager.loginAttempts)
			}
		})
	}
}

func TestServerActionRequesterUsesResolvedClientAndPeer(t *testing.T) {
	manager := newTrustedProxyTestManager(t, AccessConfig{BasePolicy: "all_allow"})
	if filepath.Clean(manager.managerUpdateStateFile) ==
		filepath.Clean(managerUpdateStatePath) {
		t.Fatal("trusted-proxy fixture uses installed manager update state")
	}
	var action, username, clientIP, peerIP string
	manager.operationStart = func(
		gotAction, gotUsername, gotClientIP, gotPeerIP string,
	) error {
		action = gotAction
		username = gotUsername
		clientIP = gotClientIP
		peerIP = gotPeerIP
		return nil
	}
	session := Session{Username: "operator", CSRF: "test-csrf-token"}
	request := newProxyRequest(
		http.MethodPost,
		"/api/server/action",
		bytes.NewBufferString(`{"action":"restart"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", session.CSRF)
	request.Header.Set("X-Forwarded-For", "2001:db8:1234::50")

	handler := manager.requestAddressMiddleware(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			manager.serverActionHandler(response, request, session)
		},
	))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("server action status = %d", response.Code)
	}
	if action != "restart" ||
		username != "operator" ||
		clientIP != "2001:db8:1234::50" ||
		peerIP != "192.0.2.200" {
		t.Fatalf(
			"operation requester = action %q, user %q, client %q, peer %q",
			action,
			username,
			clientIP,
			peerIP,
		)
	}
}
