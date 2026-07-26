package manager

import "time"

const (
	rootDir                   = "/root/mordhau"
	stateDir                  = rootDir + "/.manager"
	logDir                    = rootDir + "/log"
	runtimeDir                = stateDir + "/runtime"
	pendingDir                = stateDir + "/pending"
	backupDir                 = stateDir + "/backups"
	configDir                 = rootDir + "/Mordhau/Saved/Config/WindowsServer"
	gameLogPath               = rootDir + "/Mordhau/Saved/Logs/Mordhau.log"
	accountsPath              = stateDir + "/accounts.json"
	sessionsPath              = stateDir + "/sessions.json"
	accessPath                = stateDir + "/access.json"
	languagePath              = stateDir + "/language"
	rconStatePath             = stateDir + "/rcon-last.json"
	operationStatePath        = stateDir + "/operation.json"
	disabledINIPath           = stateDir + "/disabled-ini-entries.json"
	webAuditLogPath           = logDir + "/mordhau-web.log"
	rconEventLogPath          = logDir + "/mordhau-rcon.log"
	defaultAccount            = rootDir + "/default_web_account.txt"
	serverScript              = rootDir + "/server.sh"
	mordhauPIDPath            = runtimeDir + "/mordhau.pid"
	runtimeBridgeStatusPath   = runtimeDir + "/runtime-bridge-status.json"
	runtimeBridgeRequestPath  = runtimeDir + "/runtime-bridge-request.txt"
	runtimeBridgeResponsePath = runtimeDir + "/runtime-bridge-response.json"
	defaultRCONPort           = 7778
	emergencyDuration         = 30 * time.Minute
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
	Comment   string     `json:"comment,omitempty"`
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
	Action      string    `json:"action"`
	Running     bool      `json:"running"`
	Successful  bool      `json:"successful"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	Requested   string    `json:"requested_by,omitempty"`
	RequestedIP string    `json:"requested_ip,omitempty"`
	Output      string    `json:"output,omitempty"`
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
	Metrics              Metrics              `json:"metrics"`
	ServerRunning        bool                 `json:"server_running"`
	ServerPID            int                  `json:"server_pid,omitempty"`
	Language             string               `json:"language"`
	Languages            []Language           `json:"languages"`
	PendingConfig        bool                 `json:"pending_config"`
	Operation            Operation            `json:"operation"`
	EventSourceConnected bool                 `json:"event_source_connected"`
	EventSourceStatus    string               `json:"event_source_status"`
	ServerEvents         []RCONEvent          `json:"server_events"`
	ModRevision          uint64               `json:"mod_revision"`
	RuntimeBridge        RuntimeBridgeSummary `json:"runtime_bridge"`
	GeneratedAt          time.Time            `json:"generated_at"`
}

type RuntimeBridgeSummary struct {
	Ready                 bool      `json:"ready"`
	Status                string    `json:"status"`
	PlayerControllerCount int       `json:"player_controller_count"`
	GameModeClass         string    `json:"game_mode_class,omitempty"`
	SampledAt             time.Time `json:"sampled_at,omitempty"`
}

type RuntimeTarget struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Class      string `json:"class"`
	PlayerSlot int    `json:"player_slot"`
	PlayerName string `json:"player_name,omitempty"`
	PlayFabID  string `json:"playfab_id,omitempty"`
}

type RuntimeReplication struct {
	Net       bool   `json:"net"`
	RepSkip   bool   `json:"rep_skip"`
	RepNotify bool   `json:"rep_notify"`
	Scope     string `json:"scope"`
	Condition string `json:"condition"`
	RepIndex  uint16 `json:"rep_index"`
}

type RuntimeProperty struct {
	DeclaringClass    string             `json:"declaring_class"`
	Name              string             `json:"name"`
	Type              string             `json:"type"`
	ArrayIndex        int                `json:"array_index"`
	ArrayDim          int                `json:"array_dim"`
	ElementSize       int                `json:"element_size"`
	Offset            int                `json:"offset"`
	Flags             string             `json:"flags"`
	Editable          bool               `json:"editable"`
	ReadOnlyReason    string             `json:"read_only_reason"`
	RepNotifyFunction string             `json:"rep_notify_function"`
	Value             *string            `json:"value"`
	EnumValues        []string           `json:"enum_values,omitempty"`
	Editor            RuntimeEditor      `json:"editor"`
	Replication       RuntimeReplication `json:"replication"`
}

type RuntimeEditor struct {
	Kind string `json:"kind"`
	Min  string `json:"min,omitempty"`
	Max  string `json:"max,omitempty"`
	Step string `json:"step,omitempty"`
	Help string `json:"help,omitempty"`
}

type RuntimeStatusView struct {
	Version               int             `json:"version"`
	RequestID             string          `json:"request_id"`
	OK                    bool            `json:"ok"`
	Ready                 bool            `json:"ready"`
	PlayerControllerCount int             `json:"player_controller_count"`
	TargetCount           int             `json:"target_count"`
	Targets               []RuntimeTarget `json:"targets"`
	Error                 *RuntimeError   `json:"error,omitempty"`
}

type RuntimeTargetView struct {
	Version               int               `json:"version"`
	RequestID             string            `json:"request_id"`
	OK                    bool              `json:"ok"`
	PlayerControllerCount int               `json:"player_controller_count"`
	Target                RuntimeTarget     `json:"target"`
	ClassChain            []string          `json:"class_chain"`
	Properties            []RuntimeProperty `json:"properties"`
	PropertyCount         int               `json:"property_count"`
	NetworkNote           string            `json:"network_note"`
	Error                 *RuntimeError     `json:"error,omitempty"`
}

type RuntimePropertyChange struct {
	DeclaringClass string             `json:"declaring_class"`
	Name           string             `json:"name"`
	ArrayIndex     int                `json:"array_index"`
	OldValue       string             `json:"old_value"`
	NewValue       string             `json:"new_value"`
	Replication    RuntimeReplication `json:"replication"`
}

type RuntimePropertyChangeView struct {
	Version   int                   `json:"version"`
	RequestID string                `json:"request_id"`
	OK        bool                  `json:"ok"`
	Target    RuntimeTarget         `json:"target"`
	Property  RuntimePropertyChange `json:"property"`
	Error     *RuntimeError         `json:"error,omitempty"`
}

type RuntimeError struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	CurrentValue string `json:"current_value,omitempty"`
}

type PublicAccount struct {
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type disabledINIEntry struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Section  string `json:"section"`
	Position int    `json:"position"`
	Key      string `json:"key"`
	Value    string `json:"value"`
}

type disabledINIFile struct {
	Version  int                  `json:"version"`
	Sections []disabledINISection `json:"sections"`
	Entries  []disabledINIEntry   `json:"entries"`
}

type disabledINISection struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}
