package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
	"golang.org/x/crypto/argon2"
)

type Manager struct {
	mu             sync.RWMutex
	accounts       AccountFile
	sessions       SessionFile
	access         AccessConfig
	op             Operation
	operationPath  string
	operationStart func(action, username, clientIP, peerIP string) error
	trustedProxies []netip.Prefix

	metricsMu sync.RWMutex
	metrics   Metrics
	cpu       cpuSample

	monitoringMu             sync.RWMutex
	monitoringSettings       monitoringSettingsFile
	monitoringStatus         MonitoringStatus
	metricHistory            []MetricHistoryPoint
	metricWritesSinceCompact int
	monitoringSettingsFile   string
	metricsHistoryFile       string
	monitoringAlertQueue     chan monitoringAlert
	monitoringAlertLast      map[string]time.Time
	monitoringNow            func() time.Time

	recoveryMu               sync.RWMutex
	recoverySettings         recoverySettingsFile
	recoveryState            recoveryStateFile
	recoveryObservedRunning  bool
	recoveryObservedPID      int
	recoverySettingsFile     string
	recoveryStateFile        string
	recoveryDesiredStateFile string
	recoveryLaunchStateFile  string
	recoveryConsoleLogFile   string
	recoveryServerProcess    func() (int, bool)
	recoveryOperationStart   func(action, username, clientIP, peerIP string) error
	recoveryLifecycleBusy    func() bool
	recoveryNow              func() time.Time

	runtimeMu            sync.RWMutex
	runtimeSummary       RuntimeBridgeSummary
	runtimeTargets       []RuntimeTarget
	runtimeTargetCache   map[string]runtimeTargetCacheEntry
	runtimeCommandMu     sync.Mutex
	runtimeStatusPath    string
	runtimeRequestPath   string
	runtimeResponsePath  string
	runtimeServerProcess func() (int, bool)

	rconMu               sync.RWMutex
	eventSourceConnected bool
	eventSourceStatus    string
	rconEvents           []RCONEvent
	rconSequence         uint64
	rconLogMu            sync.Mutex
	rconLogPath          string
	rconCommandMu        sync.Mutex
	rconCommandExecute   func(command string) (rconCommandResult, error)
	currentMap           string
	currentGameMode      string

	playersMu                  sync.RWMutex
	playerHistory              playerHistoryFile
	playerHistoryFile          string
	playerArchiveDirectory     string
	playerCurrentLogFile       string
	playerServerProcess        func() (int, bool)
	playerRestrictionMu        sync.Mutex
	playerRestrictionLastSync  time.Time
	playerRestrictionLastError string

	geoIPMu              sync.RWMutex
	geoIPUpdateMu        sync.Mutex
	geoIPReader          *maxminddb.Reader
	geoIPStatus          GeoIPStatus
	geoIPDatabaseFile    string
	geoIPStateFile       string
	geoIPIgnoreFile      string
	geoIPIgnoredPrefixes []netip.Prefix
	geoIPIgnoreError     string
	geoIPDownloadBaseURL string
	geoIPHTTPClient      *http.Client
	geoIPNow             func() time.Time

	loginMu       sync.Mutex
	loginAttempts map[string]*loginAttempt

	configMu sync.Mutex
	modioMu  sync.Mutex

	customPaksMu   sync.Mutex
	customPakPaths customPakPaths

	mapCatalogMu        sync.Mutex
	mapCatalogCache     mapCatalogCache
	mapCatalogRepakPath string
	mapCatalogViewBuild func(context.Context) (MapCatalogView, error)
	mapServerProcess    func() (int, bool)

	modsMu                 sync.RWMutex
	modRefreshSettingsMu   sync.Mutex
	modRefreshSettings     modRefreshSettingsFile
	modCache               ModManagementView
	modCacheReady          bool
	modRevision            uint64
	modRefreshing          bool
	modRefreshDone         chan struct{}
	modLastAttempt         time.Time
	modLastSuccess         time.Time
	modNextRefresh         time.Time
	modLastError           string
	modRefreshWake         chan struct{}
	modManagementViewBuild func() (ModManagementView, error)
	modIOSettingsFile      string
	modRefreshSettingsFile string
	modUpdateStateFile     string
	modUpdateState         modUpdateStateFile
	modUpdateStateLoaded   bool
	modRestartWake         chan struct{}
	modServerProcess       func() (int, bool)
	modRestartMessageSend  func(string) error

	auditMu   sync.Mutex
	auditPath string

	managerUpdateMu          sync.Mutex
	managerUpdateHTTPClient  *http.Client
	managerUpdateLatestURL   string
	managerUpdateStateFile   string
	managerUpdateVersionFile string
	managerUpdateBinary      string
	managerUpdateLogFile     string
	managerUpdateLockFile    string
	managerUpdateNow         func() time.Time
	managerUpdateWorkerStart func(string) error

	steamUpdateMu            sync.Mutex
	steamUpdateStateFile     string
	steamUpdateManifestFile  string
	steamUpdateConsoleFile   string
	steamUpdateCommand       string
	steamUpdateLifecycleLock string
	steamUpdateNow           func() time.Time
	steamUpdateRemoteBuild   func(context.Context) (string, error)
	steamUpdateWake          chan struct{}

	automaticUpdateMu          sync.RWMutex
	automaticUpdateState       automaticUpdateStateFile
	automaticUpdateStateFile   string
	automaticUpdateWake        chan struct{}
	automaticUpdateNow         func() time.Time
	automaticUpdateMessageSend func(string) error
	automaticUpdateProcess     func() (int, bool)

	scheduledRestartMu            sync.RWMutex
	scheduledRestartState         scheduledServerRestartStateFile
	scheduledRestartStateFile     string
	scheduledRestartWake          chan struct{}
	scheduledRestartNow           func() time.Time
	scheduledRestartMessageSend   func(string) error
	scheduledRestartServerProcess func() (int, bool)

	fleetMu               sync.RWMutex
	fleetMutationMu       sync.Mutex
	fleetSettings         fleetSettingsFile
	fleetStatuses         map[string]FleetNodeStatus
	fleetSettingsFile     string
	fleetIdentityKeyFile  string
	fleetIdentityCertFile string
	fleetIdentityMu       sync.Mutex
	fleetIdentityCache    *fleetIdentity
	fleetWake             chan struct{}
	fleetNow              func() time.Time

	fleetEventMu          sync.Mutex
	fleetEventSequence    uint64
	fleetSubscriberID     uint64
	fleetSubscribers      map[uint64]chan FleetEvent
	fleetRecentDeliveries map[string]time.Time
	fleetBootID           string
	fleetRouteQueue       chan FleetEvent
	fleetDeliverQueues    []chan fleetDelivery
	fleetMessageSend      func(string) error
	fleetClientMu         sync.Mutex
	fleetClients          map[string]fleetCachedHTTPClient
}

type loginAttempt struct {
	Failures  []time.Time
	BlockedTo time.Time
}

func New(trustedProxies ...netip.Prefix) (*Manager, error) {
	customPaks := defaultCustomPakPaths()
	for _, dir := range []string{
		stateDir,
		runtimeDir,
		geoIPDir,
		pendingDir,
		backupDir,
		logDir,
		unicodeBridgeSpoolDir,
		customPaks.activeDir,
		customPaks.inactiveDir,
		customPaks.uploadDir,
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return nil, err
		}
	}
	if err := cleanupUnicodeBridgeSpoolAt(unicodeBridgeSpoolDir); err != nil {
		return nil, err
	}

	m := &Manager{
		loginAttempts:             make(map[string]*loginAttempt),
		eventSourceStatus:         "Waiting for server",
		auditPath:                 webAuditLogPath,
		operationPath:             operationStatePath,
		rconLogPath:               rconEventLogPath,
		modRefreshWake:            make(chan struct{}, 1),
		modRestartWake:            make(chan struct{}, 1),
		runtimeStatusPath:         runtimeBridgeStatusPath,
		runtimeRequestPath:        runtimeBridgeRequestPath,
		runtimeResponsePath:       runtimeBridgeResponsePath,
		runtimeTargetCache:        make(map[string]runtimeTargetCacheEntry),
		customPakPaths:            customPaks,
		playerHistoryFile:         playerHistoryPath,
		playerArchiveDirectory:    logDir,
		playerCurrentLogFile:      gameLogPath,
		playerServerProcess:       serverProcess,
		mapServerProcess:          serverProcess,
		geoIPDatabaseFile:         geoIPDatabasePath,
		geoIPStateFile:            geoIPStatePath,
		geoIPIgnoreFile:           geoIPIgnorePath,
		geoIPDownloadBaseURL:      defaultGeoIPDownloadBaseURL,
		geoIPHTTPClient:           defaultGeoIPHTTPClient,
		geoIPNow:                  time.Now,
		recoverySettingsFile:      recoverySettingsPath,
		recoveryStateFile:         recoveryStatePath,
		recoveryDesiredStateFile:  serverDesiredStatePath,
		recoveryLaunchStateFile:   serverLaunchStatePath,
		recoveryConsoleLogFile:    serverConsoleLogPath,
		recoveryServerProcess:     serverProcess,
		recoveryLifecycleBusy:     lifecycleOperationBusy,
		recoveryNow:               time.Now,
		monitoringSettingsFile:    monitoringSettingsPath,
		metricsHistoryFile:        metricsHistoryPath,
		monitoringAlertQueue:      make(chan monitoringAlert, 32),
		monitoringAlertLast:       make(map[string]time.Time),
		monitoringNow:             time.Now,
		managerUpdateHTTPClient:   defaultManagerUpdateHTTPClient(),
		managerUpdateLatestURL:    managerUpdateLatestReleaseURL,
		managerUpdateStateFile:    managerUpdateStatePath,
		managerUpdateVersionFile:  managerVersionPath,
		managerUpdateBinary:       managerBinaryPath,
		managerUpdateLogFile:      managerUpdateLogPath,
		managerUpdateLockFile:     managerUpdateLockPath,
		managerUpdateNow:          time.Now,
		steamUpdateStateFile:      steamUpdateStatePath,
		steamUpdateManifestFile:   steamAppManifestPath,
		steamUpdateConsoleFile:    steamConsoleLogPath,
		steamUpdateCommand:        steamCMDPath,
		steamUpdateLifecycleLock:  lifecycleLockPath,
		steamUpdateNow:            time.Now,
		steamUpdateWake:           make(chan struct{}, 1),
		automaticUpdateStateFile:  automaticUpdateStatePath,
		automaticUpdateWake:       make(chan struct{}, 1),
		automaticUpdateNow:        time.Now,
		scheduledRestartStateFile: scheduledServerRestartStatePath,
		scheduledRestartWake:      make(chan struct{}, 1),
		scheduledRestartNow:       time.Now,
		fleetStatuses:             make(map[string]FleetNodeStatus),
		fleetSettingsFile:         fleetSettingsPath,
		fleetIdentityKeyFile:      fleetIdentityKeyPath,
		fleetIdentityCertFile:     fleetIdentityCertPath,
		fleetWake:                 make(chan struct{}, 1),
		fleetNow:                  time.Now,
		fleetSubscribers:          make(map[uint64]chan FleetEvent),
		fleetRecentDeliveries:     make(map[string]time.Time),
		fleetRouteQueue:           make(chan FleetEvent, 256),
		fleetDeliverQueues:        newFleetDeliveryQueues(),
		fleetClients:              make(map[string]fleetCachedHTTPClient),
	}
	m.managerUpdateWorkerStart = m.startManagerUpdateWorker
	m.steamUpdateRemoteBuild = m.querySteamRemoteBuild
	for _, prefix := range trustedProxies {
		canonical, err := canonicalTrustedProxyPrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", prefix, err)
		}
		m.trustedProxies = append(m.trustedProxies, canonical)
	}
	if err := m.loadOrCreateMonitoring(); err != nil {
		return nil, err
	}
	if err := m.initializeAuditLog(); err != nil {
		return nil, fmt.Errorf("initialize web audit log: %w", err)
	}
	if err := m.loadOrCreateOperationState(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateRecoveryState(); err != nil {
		return nil, err
	}
	if err := m.loadRCONEventLog(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateAccounts(); err != nil {
		return nil, err
	}
	if err := m.loadSessions(); err != nil {
		return nil, err
	}
	if err := m.loadAccess(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreatePlayerHistory(); err != nil {
		return nil, err
	}
	m.initializeGeoIP()
	if err := m.ensureLanguage(); err != nil {
		return nil, err
	}
	if err := initializeDisabledINIState(); err != nil {
		return nil, err
	}
	if err := m.ensureRCONConfig(); err != nil {
		return nil, err
	}
	if err := m.ensureServerEventLogConfig(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateModRefreshSettings(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateModUpdateState(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateManagerUpdateState(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateSteamUpdateState(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateAutomaticUpdateState(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateScheduledServerRestartState(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateFleetSettings(); err != nil {
		return nil, err
	}
	if m.currentFleetSettings().Role != FleetRoleStandalone {
		if _, err := m.ensureFleetIdentity(); err != nil {
			return nil, fmt.Errorf("initialize fleet identity: %w", err)
		}
	}
	bootID, err := randomToken(12)
	if err != nil {
		return nil, fmt.Errorf("generate fleet boot ID: %w", err)
	}
	m.fleetBootID = bootID
	return m, nil
}

func (m *Manager) StartBackground(ctx context.Context) {
	m.auditActorEvent("system", "local", "web_manager_started", nil)
	go m.metricsLoop(ctx)
	go m.monitoringAlertLoop(ctx)
	go m.recoveryLoop(ctx)
	go m.runtimeBridgeStatusLoop(ctx)
	go m.runtimePlayerLevelLoop(ctx)
	go m.gameLogLoop(ctx)
	go m.playerRestrictionExpiryLoop(ctx)
	go m.geoIPUpdateLoop(ctx)
	go m.cleanupLoop(ctx)
	go m.modRefreshLoop(ctx)
	go m.modRestartLoop(ctx)
	go m.managerUpdateCheckLoop(ctx)
	go m.steamUpdateCheckLoop(ctx)
	go m.automaticUpdateLoop(ctx)
	go m.scheduledServerRestartLoop(ctx)
	go m.fleetSupervisorLoop(ctx)
	go m.fleetBrokerLoop(ctx)
	for _, deliveries := range m.fleetDeliverQueues {
		go m.fleetDeliveryLoop(ctx, deliveries)
	}
	go func() {
		<-ctx.Done()
		m.auditActorEvent("system", "local", "web_manager_stopping", nil)
	}()
}

func randomString(alphabet string, length int) (string, error) {
	if length < 1 || len(alphabet) < 2 {
		return "", errors.New("invalid random string parameters")
	}
	out := make([]byte, length)
	for i := range out {
		var one [1]byte
		limit := 256 - (256 % len(alphabet))
		for {
			if _, err := rand.Read(one[:]); err != nil {
				return "", err
			}
			if int(one[0]) < limit {
				out[i] = alphabet[int(one[0])%len(alphabet)]
				break
			}
		}
	}
	return string(out), nil
}

func randomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomPassword() (string, error) {
	lower := "abcdefghijklmnopqrstuvwxyz"
	upper := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits := "0123456789"
	all := lower + upper + digits

	a, err := randomString(lower, 1)
	if err != nil {
		return "", err
	}
	b, err := randomString(upper, 1)
	if err != nil {
		return "", err
	}
	c, err := randomString(digits, 1)
	if err != nil {
		return "", err
	}
	rest, err := randomString(all, 5)
	if err != nil {
		return "", err
	}
	chars := []byte(a + b + c + rest)
	for i := len(chars) - 1; i > 0; i-- {
		var one [1]byte
		limit := 256 - (256 % (i + 1))
		for {
			if _, err := rand.Read(one[:]); err != nil {
				return "", err
			}
			if int(one[0]) < limit {
				j := int(one[0]) % (i + 1)
				chars[i], chars[j] = chars[j], chars[i]
				break
			}
		}
	}
	return string(chars), nil
}

func passwordDigest(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 32*1024, 2, 32)
}

func makePasswordHash(password string) (saltText, hashText string, err error) {
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return "", "", err
	}
	hash := passwordDigest(password, salt)
	return base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash), nil
}

func verifyPassword(account Account, password string) bool {
	salt, err := base64.RawStdEncoding.DecodeString(account.Salt)
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(account.Hash)
	if err != nil {
		return false
	}
	actual := passwordDigest(password, salt)
	if len(actual) != len(expected) {
		return false
	}
	var difference byte
	for i := range actual {
		difference |= actual[i] ^ expected[i]
	}
	return difference == 0
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, mode)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp."+id)
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp, mode); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (m *Manager) loadOrCreateAccounts() error {
	if err := readJSON(accountsPath, &m.accounts); err == nil {
		if len(m.accounts.Accounts) == 0 {
			return errors.New("accounts file contains no accounts")
		}
		return os.Chmod(accountsPath, 0600)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load accounts: %w", err)
	}

	username, err := randomString("abcdefghijklmnopqrstuvwxyz0123456789", 4)
	if err != nil {
		return err
	}
	password, err := randomPassword()
	if err != nil {
		return err
	}
	salt, hash, err := makePasswordHash(password)
	if err != nil {
		return err
	}
	now := time.Now()
	m.accounts = AccountFile{
		Version: 1,
		Accounts: []Account{{
			Username:  username,
			Salt:      salt,
			Hash:      hash,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}
	if err := writeJSONAtomic(accountsPath, m.accounts, 0600); err != nil {
		return err
	}
	credentials := []byte("Username: " + username + "\nPassword: " + password + "\n")
	if err := writeFileAtomic(defaultAccount, credentials, 0600); err != nil {
		return err
	}
	return nil
}

func (m *Manager) loadSessions() error {
	if err := readJSON(sessionsPath, &m.sessions); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load sessions: %w", err)
		}
		m.sessions = SessionFile{Version: 1, Sessions: []Session{}}
		return writeJSONAtomic(sessionsPath, m.sessions, 0600)
	}
	m.purgeSessionsLocked(time.Now())
	return writeJSONAtomic(sessionsPath, m.sessions, 0600)
}

func (m *Manager) loadAccess() error {
	if err := readJSON(accessPath, &m.access); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load access rules: %w", err)
		}
		m.access = AccessConfig{
			Version:    1,
			BasePolicy: "all_allow",
			Rules:      []AccessRule{},
		}
		return writeJSONAtomic(accessPath, m.access, 0600)
	}
	if m.access.BasePolicy != "all_allow" && m.access.BasePolicy != "all_deny" {
		return errors.New("invalid access base policy")
	}
	if m.access.Rules == nil {
		m.access.Rules = []AccessRule{}
	}
	m.purgeAccessLocked(time.Now())
	return writeJSONAtomic(accessPath, m.access, 0600)
}

func (m *Manager) ensureLanguage() error {
	data, err := os.ReadFile(languagePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	language := strings.TrimSpace(string(data))
	if !validLanguage(language) {
		language = "en"
	}
	return writeFileAtomic(languagePath, []byte(language+"\n"), 0600)
}

func validLanguage(code string) bool {
	for _, language := range supportedLanguages {
		if code == language.Code {
			return true
		}
	}
	return false
}

func (m *Manager) currentLanguage() string {
	data, err := os.ReadFile(languagePath)
	if err != nil {
		return "en"
	}
	value := strings.TrimSpace(string(data))
	if !validLanguage(value) {
		return "en"
	}
	return value
}

func (m *Manager) setLanguage(code string) error {
	if !validLanguage(code) {
		return errors.New("unsupported language")
	}
	return writeFileAtomic(languagePath, []byte(code+"\n"), 0600)
}

func (m *Manager) publicAccountsLocked() []PublicAccount {
	result := make([]PublicAccount, 0, len(m.accounts.Accounts))
	for _, account := range m.accounts.Accounts {
		result = append(result, PublicAccount{
			Username:  account.Username,
			CreatedAt: account.CreatedAt,
			UpdatedAt: account.UpdatedAt,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Username < result[j].Username
	})
	return result
}

func (m *Manager) purgeSessionsLocked(now time.Time) bool {
	accounts := make(map[string]bool)
	for _, account := range m.accounts.Accounts {
		accounts[account.Username] = true
	}
	kept := m.sessions.Sessions[:0]
	changed := false
	for _, session := range m.sessions.Sessions {
		if now.Before(session.ExpiresAt) && accounts[session.Username] {
			kept = append(kept, session)
		} else {
			changed = true
		}
	}
	m.sessions.Sessions = kept
	return changed
}

func (m *Manager) purgeAccessLocked(now time.Time) bool {
	kept := m.access.Rules[:0]
	changed := false
	for _, rule := range m.access.Rules {
		if rule.Temporary && rule.ExpiresAt != nil && !now.Before(*rule.ExpiresAt) {
			changed = true
			continue
		}
		kept = append(kept, rule)
	}
	m.access.Rules = kept
	return changed
}

func (m *Manager) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.mu.Lock()
			if m.purgeSessionsLocked(now) {
				_ = writeJSONAtomic(sessionsPath, m.sessions, 0600)
			}
			if m.purgeAccessLocked(now) {
				_ = writeJSONAtomic(accessPath, m.access, 0600)
			}
			m.mu.Unlock()
			m.pruneLoginAttempts(now)
			m.pruneManagedLogs(now)
		}
	}
}
