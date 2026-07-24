package manager

import "time"

const (
	rootDir            = "/root/mordhau"
	stateDir           = rootDir + "/.manager"
	logDir             = rootDir + "/log"
	runtimeDir         = stateDir + "/runtime"
	pendingDir         = stateDir + "/pending"
	backupDir          = stateDir + "/backups"
	configDir          = rootDir + "/Mordhau/Saved/Config/WindowsServer"
	gameLogPath        = rootDir + "/Mordhau/Saved/Logs/Mordhau.log"
	accountsPath       = stateDir + "/accounts.json"
	sessionsPath       = stateDir + "/sessions.json"
	accessPath         = stateDir + "/access.json"
	languagePath       = stateDir + "/language"
	rconStatePath      = stateDir + "/rcon-last.json"
	operationStatePath = stateDir + "/operation.json"
	webAuditLogPath    = logDir + "/mordhau-web.log"
	rconEventLogPath   = logDir + "/mordhau-rcon.log"
	defaultAccount     = rootDir + "/default_web_account.txt"
	serverScript       = rootDir + "/server.sh"
	mordhauPIDPath     = runtimeDir + "/mordhau.pid"
	defaultRCONPort    = 7778
	emergencyDuration  = 30 * time.Minute
)

var supportedLanguages = []Language{
	{Code: "en", Name: "English"},
	{Code: "de", Name: "Deutsch"},
	{Code: "es", Name: "Español"},
	{Code: "zh-Hans", Name: "简体中文"},
	{Code: "fr", Name: "Français"},
	{Code: "it", Name: "Italiano"},
	{Code: "pt", Name: "Português"},
	{Code: "ru", Name: "Русский"},
	{Code: "ko", Name: "한국어"},
	{Code: "zh-Hant", Name: "繁體中文"},
}

type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Account struct {
	Username  string    `json:"username"`
	Salt      string    `json:"salt"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AccountFile struct {
	Version  int       `json:"version"`
	Accounts []Account `json:"accounts"`
}

type Session struct {
	TokenHash string    `json:"token_hash"`
	CSRF      string    `json:"csrf"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
	Remember  bool      `json:"remember"`
	CreatedAt time.Time `json:"created_at"`
}

type SessionFile struct {
	Version  int       `json:"version"`
	Sessions []Session `json:"sessions"`
}

type AccessRule struct {
	ID        string     `json:"id"`
	Action    string     `json:"action"`
	Network   string     `json:"network"`
	Temporary bool       `json:"temporary,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type AccessConfig struct {
	Version    int          `json:"version"`
	BasePolicy string       `json:"base_policy"`
	Rules      []AccessRule `json:"rules"`
}

type Operation struct {
	Action     string    `json:"action"`
	Running    bool      `json:"running"`
	Successful bool      `json:"successful"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Requested  string    `json:"requested_by,omitempty"`
	Output     string    `json:"output,omitempty"`
}

type operationStateFile struct {
	Version   int       `json:"version"`
	Operation Operation `json:"operation"`
}

type Usage struct {
	Total uint64  `json:"total"`
	Used  uint64  `json:"used"`
	Free  uint64  `json:"free"`
	Pct   float64 `json:"percent"`
}

type Metrics struct {
	CPUPercent float64   `json:"cpu_percent"`
	Memory     Usage     `json:"memory"`
	Swap       Usage     `json:"swap"`
	Disk       Usage     `json:"disk"`
	SampledAt  time.Time `json:"sampled_at"`
}

type RCONEvent struct {
	Sequence uint64    `json:"sequence"`
	Time     time.Time `json:"time"`
	Text     string    `json:"text"`
	Kind     string    `json:"kind"`
}

type Snapshot struct {
	Metrics       Metrics     `json:"metrics"`
	ServerRunning bool        `json:"server_running"`
	ServerPID     int         `json:"server_pid,omitempty"`
	Language      string      `json:"language"`
	Languages     []Language  `json:"languages"`
	PendingConfig bool        `json:"pending_config"`
	Operation     Operation   `json:"operation"`
	RCONConnected bool        `json:"rcon_connected"`
	RCONStatus    string      `json:"rcon_status"`
	RCONEvents    []RCONEvent `json:"rcon_events"`
	ModRevision   uint64      `json:"mod_revision"`
	GeneratedAt   time.Time   `json:"generated_at"`
}

type PublicAccount struct {
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
