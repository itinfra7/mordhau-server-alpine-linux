package manager

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	runtimeBridgeSampleInterval = time.Second
	runtimeBridgeStaleAfter     = 5 * time.Second
	runtimeBridgeCallTimeout    = 20 * time.Second
	runtimeBridgeResponseLimit  = 9 << 20
	runtimeBridgeValueLimit     = 120 << 10
	runtimeTargetCacheLifetime  = time.Second
	runtimeBridgeStatusLimit    = 4 << 20
	runtimeEnumValueLimit       = 1024
	runtimePlayerLevelPoll      = 2 * time.Second
	runtimePlayerLevelInitial   = 5 * time.Second
	runtimePlayerLevelRefresh   = 5 * time.Minute
	runtimePlayerLevelRetry     = 10 * time.Second
)

var (
	errRuntimeBridgeUnavailable = errors.New("runtime bridge is unavailable")
	errInvalidRuntimeRequest    = errors.New("invalid runtime request")
	runtimeDecimalPattern       = regexp.MustCompile(
		`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`,
	)
)

type runtimeBridgeStatusFile struct {
	Version               int             `json:"version"`
	Ready                 bool            `json:"ready"`
	PlayerControllerCount int             `json:"player_controller_count"`
	GameModeClass         string          `json:"game_mode_class"`
	TargetCount           int             `json:"target_count"`
	Targets               []RuntimeTarget `json:"targets"`
}

type runtimeTargetCacheEntry struct {
	View      RuntimeTargetView
	ExpiresAt time.Time
}

type runtimePlayerLevelTarget struct {
	PlayerSlot         int
	PlayFabID          string
	PlayerControllerID string
}

type runtimeBridgeProtocolError struct {
	Code         string
	Message      string
	CurrentValue string
}

func (err *runtimeBridgeProtocolError) Error() string {
	if err == nil || strings.TrimSpace(err.Message) == "" {
		return "runtime bridge request failed"
	}
	return err.Message
}

func (m *Manager) runtimePaths() (status, request, response string) {
	status = m.runtimeStatusPath
	request = m.runtimeRequestPath
	response = m.runtimeResponsePath
	if status == "" {
		status = runtimeBridgeStatusPath
	}
	if request == "" {
		request = runtimeBridgeRequestPath
	}
	if response == "" {
		response = runtimeBridgeResponsePath
	}
	return
}

func (m *Manager) setRuntimeState(summary RuntimeBridgeSummary, targets []RuntimeTarget) {
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	m.runtimeSummary = summary
	m.runtimeTargets = append([]RuntimeTarget(nil), targets...)
	if len(m.runtimeTargetCache) == 0 {
		return
	}
	live := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		live[target.ID] = struct{}{}
	}
	now := time.Now()
	for targetID, cached := range m.runtimeTargetCache {
		_, exists := live[targetID]
		if !exists || !now.Before(cached.ExpiresAt) {
			delete(m.runtimeTargetCache, targetID)
		}
	}
}

func (m *Manager) runtimeSummaryView() RuntimeBridgeSummary {
	m.runtimeMu.RLock()
	defer m.runtimeMu.RUnlock()
	return m.runtimeSummary
}

func (m *Manager) runtimeStatusView() RuntimeStatusView {
	m.runtimeMu.RLock()
	defer m.runtimeMu.RUnlock()
	targets := append([]RuntimeTarget(nil), m.runtimeTargets...)
	return RuntimeStatusView{
		Version:               1,
		OK:                    m.runtimeSummary.Ready,
		Ready:                 m.runtimeSummary.Ready,
		PlayerControllerCount: m.runtimeSummary.PlayerControllerCount,
		TargetCount:           len(targets),
		Targets:               targets,
	}
}

func (m *Manager) sampleRuntimeBridgeStatus() {
	statusPath, _, _ := m.runtimePaths()
	now := time.Now()
	summary := RuntimeBridgeSummary{
		Status:    "unavailable",
		SampledAt: now,
	}
	running := serverRunning
	if m.runtimeServerProcess != nil {
		running = func() bool {
			_, active := m.runtimeServerProcess()
			return active
		}
	}
	if !running() {
		summary.Status = "server_stopped"
		m.setRuntimeState(summary, nil)
		return
	}

	info, err := os.Stat(statusPath)
	if err != nil {
		summary.Status = "starting"
		m.setRuntimeState(summary, nil)
		return
	}
	statusAge := now.Sub(info.ModTime())
	if !info.Mode().IsRegular() || info.Size() < 2 ||
		info.Size() > runtimeBridgeStatusLimit ||
		statusAge < -runtimeBridgeStaleAfter {
		summary.Status = "invalid_status"
		m.setRuntimeState(summary, nil)
		return
	}
	if statusAge > runtimeBridgeStaleAfter {
		summary.Status = "stale"
		summary.SampledAt = info.ModTime()
		m.setRuntimeState(summary, nil)
		return
	}
	var status runtimeBridgeStatusFile
	if err := readJSON(statusPath, &status); err != nil {
		summary.Status = "invalid_status"
		m.setRuntimeState(summary, nil)
		return
	}
	if status.Version != 1 || !status.Ready || status.PlayerControllerCount < 0 ||
		status.PlayerControllerCount > 1024 ||
		status.TargetCount < 0 ||
		status.TargetCount != len(status.Targets) ||
		len(status.Targets) > 3072 ||
		!utf8.ValidString(status.GameModeClass) ||
		len(status.GameModeClass) > 191 {
		summary.Status = "invalid_status"
		m.setRuntimeState(summary, nil)
		return
	}
	targetIDs := make(map[string]struct{}, len(status.Targets))
	controllerSlots := make(map[int]struct{}, status.PlayerControllerCount)
	playerTargetSlots := make(map[string]struct{}, len(status.Targets))
	gameModeTargets := 0
	gameStateTargets := 0
	for _, target := range status.Targets {
		if !validRuntimeTargetID(target.ID) ||
			!validRuntimeTargetKind(target.Kind) ||
			target.Class == "" ||
			!utf8.ValidString(target.Class) ||
			len(target.Class) > 191 ||
			!validRuntimePlayerName(target.PlayerName) ||
			!validRuntimePlayFabID(target.PlayFabID) ||
			!validPlayerPlatformIdentity(
				target.Platform,
				target.PlatformAccountID,
			) ||
			(target.Kind != "player_controller" &&
				(target.Platform != "" ||
					target.PlatformAccountID != "")) ||
			target.PlayerSlot < -1 ||
			target.PlayerSlot > 1023 {
			summary.Status = "invalid_status"
			m.setRuntimeState(summary, nil)
			return
		}
		if _, exists := targetIDs[target.ID]; exists {
			summary.Status = "invalid_status"
			m.setRuntimeState(summary, nil)
			return
		}
		targetIDs[target.ID] = struct{}{}
		switch target.Kind {
		case "game_mode":
			gameModeTargets++
			if target.PlayerSlot != -1 ||
				target.PlayerName != "" ||
				target.PlayFabID != "" ||
				(status.GameModeClass != "" && target.Class != status.GameModeClass) {
				summary.Status = "invalid_status"
				m.setRuntimeState(summary, nil)
				return
			}
		case "game_state":
			gameStateTargets++
			if target.PlayerSlot != -1 ||
				target.PlayerName != "" ||
				target.PlayFabID != "" {
				summary.Status = "invalid_status"
				m.setRuntimeState(summary, nil)
				return
			}
		default:
			if target.PlayerSlot < 0 {
				summary.Status = "invalid_status"
				m.setRuntimeState(summary, nil)
				return
			}
			if target.Kind != "player_controller" &&
				(target.PlayerName != "" || target.PlayFabID != "") {
				summary.Status = "invalid_status"
				m.setRuntimeState(summary, nil)
				return
			}
			slotKey := fmt.Sprintf("%s:%d", target.Kind, target.PlayerSlot)
			if _, exists := playerTargetSlots[slotKey]; exists {
				summary.Status = "invalid_status"
				m.setRuntimeState(summary, nil)
				return
			}
			playerTargetSlots[slotKey] = struct{}{}
			if target.Kind == "player_controller" {
				controllerSlots[target.PlayerSlot] = struct{}{}
			}
		}
	}
	if gameModeTargets > 1 || gameStateTargets > 1 ||
		len(controllerSlots) != status.PlayerControllerCount {
		summary.Status = "invalid_status"
		m.setRuntimeState(summary, nil)
		return
	}
	for slot := 0; slot < status.PlayerControllerCount; slot++ {
		if _, exists := controllerSlots[slot]; !exists {
			summary.Status = "invalid_status"
			m.setRuntimeState(summary, nil)
			return
		}
	}
	for slotKey := range playerTargetSlots {
		separator := strings.LastIndexByte(slotKey, ':')
		slot, err := parseRuntimeSlot(slotKey[separator+1:])
		if err != nil {
			summary.Status = "invalid_status"
			m.setRuntimeState(summary, nil)
			return
		}
		if _, exists := controllerSlots[slot]; !exists {
			summary.Status = "invalid_status"
			m.setRuntimeState(summary, nil)
			return
		}
	}
	summary.Ready = true
	summary.Status = "ready"
	summary.PlayerControllerCount = status.PlayerControllerCount
	summary.GameModeClass = status.GameModeClass
	summary.SampledAt = info.ModTime()
	m.setRuntimeState(summary, status.Targets)
	m.recordPlayerPlatformObservations(
		runtimePlayerPlatformObservations(status.Targets),
	)
}

func (m *Manager) runtimeBridgeStatusLoop(ctx context.Context) {
	m.sampleRuntimeBridgeStatus()
	ticker := time.NewTicker(runtimeBridgeSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sampleRuntimeBridgeStatus()
		}
	}
}

func runtimePlayerPlatformObservations(
	targets []RuntimeTarget,
) []playerPlatformObservation {
	observations := make([]playerPlatformObservation, 0, len(targets))
	for _, target := range targets {
		if target.Kind != "player_controller" ||
			!validMordhauPlayerID(target.PlayFabID) ||
			!validPlayerPlatformIdentity(
				target.Platform,
				target.PlatformAccountID,
			) ||
			target.Platform == "" {
			continue
		}
		observations = append(observations, playerPlatformObservation{
			PlayFabID:         strings.ToUpper(target.PlayFabID),
			Platform:          target.Platform,
			PlatformAccountID: target.PlatformAccountID,
		})
	}
	return observations
}

func (m *Manager) runtimePlayerLevelTargets() []runtimePlayerLevelTarget {
	m.runtimeMu.RLock()
	targets := append([]RuntimeTarget(nil), m.runtimeTargets...)
	ready := m.runtimeSummary.Ready
	m.runtimeMu.RUnlock()
	if !ready {
		return nil
	}
	result := make([]runtimePlayerLevelTarget, 0, len(targets))
	for _, target := range targets {
		if target.Kind == "player_controller" &&
			validMordhauPlayerID(target.PlayFabID) {
			result = append(result, runtimePlayerLevelTarget{
				PlayerSlot:         target.PlayerSlot,
				PlayFabID:          strings.ToUpper(target.PlayFabID),
				PlayerControllerID: target.ID,
			})
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].PlayerSlot < result[right].PlayerSlot
	})
	return result
}

func runtimePlayerLevel(view RuntimeTargetView) (int, bool) {
	if view.Target.Kind != "player_controller" ||
		view.AccountProgress == nil ||
		view.AccountProgress.XP < 0 ||
		view.AccountProgress.Level < 1 ||
		view.AccountProgress.Level > playerMaximumLevel {
		return 0, false
	}
	return view.AccountProgress.Level, true
}

func (m *Manager) runtimePlayerLevelTargetCurrent(
	target runtimePlayerLevelTarget,
) bool {
	for _, current := range m.runtimePlayerLevelTargets() {
		if current.PlayerSlot == target.PlayerSlot &&
			current.PlayFabID == target.PlayFabID &&
			current.PlayerControllerID == target.PlayerControllerID {
			return true
		}
	}
	return false
}

func (m *Manager) sampleRuntimePlayerLevel(
	ctx context.Context,
	target runtimePlayerLevelTarget,
) error {
	view, err := m.runtimeTarget(ctx, target.PlayerControllerID)
	if err != nil {
		return err
	}
	level, ok := runtimePlayerLevel(view)
	if !ok {
		return errors.New("runtime inventory did not expose valid account progress")
	}
	if !m.runtimePlayerLevelTargetCurrent(target) {
		return errors.New("runtime player target changed while sampling level")
	}
	m.recordPlayerLevelObservations([]playerLevelObservation{{
		PlayFabID: target.PlayFabID,
		Level:     level,
	}})
	return nil
}

func (m *Manager) runtimePlayerLevelLoop(ctx context.Context) {
	firstSeen := make(map[string]time.Time)
	lastAttempt := make(map[string]time.Time)
	lastSuccess := make(map[string]time.Time)
	ticker := time.NewTicker(runtimePlayerLevelPoll)
	defer ticker.Stop()

	sample := func() {
		now := time.Now()
		targets := m.runtimePlayerLevelTargets()
		live := make(map[string]struct{}, len(targets))
		for _, target := range targets {
			live[target.PlayerControllerID] = struct{}{}
			seenAt, seen := firstSeen[target.PlayerControllerID]
			if !seen {
				firstSeen[target.PlayerControllerID] = now
				continue
			}
			if now.Sub(seenAt) < runtimePlayerLevelInitial {
				continue
			}
			if succeeded := lastSuccess[target.PlayerControllerID]; !succeeded.IsZero() &&
				now.Sub(succeeded) < runtimePlayerLevelRefresh {
				continue
			}
			if attempted := lastAttempt[target.PlayerControllerID]; !attempted.IsZero() &&
				now.Sub(attempted) < runtimePlayerLevelRetry {
				continue
			}
			lastAttempt[target.PlayerControllerID] = now
			if err := m.sampleRuntimePlayerLevel(ctx, target); err == nil {
				lastSuccess[target.PlayerControllerID] = time.Now()
			}
			if ctx.Err() != nil {
				return
			}
		}
		for targetID := range lastAttempt {
			if _, exists := live[targetID]; !exists {
				delete(firstSeen, targetID)
				delete(lastAttempt, targetID)
				delete(lastSuccess, targetID)
			}
		}
		for targetID := range firstSeen {
			if _, exists := live[targetID]; !exists {
				delete(firstSeen, targetID)
			}
		}
	}

	sample()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample()
		}
	}
}

func validRuntimeTargetKind(kind string) bool {
	switch kind {
	case "game_mode", "game_state", "player_controller", "player_state", "pawn":
		return true
	default:
		return false
	}
}

func parseRuntimeSlot(value string) (int, error) {
	var slot int
	if value == "" {
		return 0, errInvalidRuntimeRequest
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errInvalidRuntimeRequest
		}
		slot = slot*10 + int(character-'0')
		if slot > 1023 {
			return 0, errInvalidRuntimeRequest
		}
	}
	return slot, nil
}

func validRuntimeTargetID(value string) bool {
	if len(value) < 5 || len(value) > 127 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validRuntimeIdentifier(value string) bool {
	if value == "" || len(value) > 255 || !utf8.ValidString(value) ||
		strings.IndexByte(value, 0) >= 0 {
		return false
	}
	return true
}

func validRuntimePlayerName(value string) bool {
	return len(value) <= 511 &&
		utf8.ValidString(value) &&
		strings.IndexByte(value, 0) < 0
}

func validRuntimePlayFabID(value string) bool {
	if len(value) > 127 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' ||
			character == '.' || character == ':' {
			continue
		}
		return false
	}
	return true
}

func validRuntimeValue(value string) bool {
	return len(value) <= runtimeBridgeValueLimit &&
		utf8.ValidString(value) &&
		strings.IndexByte(value, 0) < 0
}

func validRuntimeEnumValues(propertyType string, values []string) bool {
	if len(values) == 0 {
		return true
	}
	if propertyType != "ByteProperty" && propertyType != "EnumProperty" {
		return false
	}
	if len(values) > runtimeEnumValueLimit {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) > 255 ||
			!utf8.ValidString(value) ||
			strings.IndexByte(value, 0) >= 0 {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func runtimeIntegerEditor(kind, min, max string) RuntimeEditor {
	return RuntimeEditor{
		Kind: kind,
		Min:  min,
		Max:  max,
		Step: "1",
	}
}

func runtimeEnumContains(values []string, current string) bool {
	for _, value := range values {
		if value == current {
			return true
		}
	}
	return false
}

func runtimeEnumValueIsSentinel(value string) bool {
	return value == "MAX" || strings.HasSuffix(value, "_MAX")
}

func filterRuntimeEnumValues(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if !runtimeEnumValueIsSentinel(value) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func normalizeRuntimeProperty(property *RuntimeProperty) {
	if property == nil {
		return
	}
	property.EnumValues = filterRuntimeEnumValues(property.EnumValues)
	if !property.Editable || property.Value == nil {
		property.Editor = RuntimeEditor{Kind: "read_only"}
		return
	}
	switch property.Type {
	case "BoolProperty":
		property.Editor = RuntimeEditor{Kind: "boolean"}
	case "ByteProperty":
		if len(property.EnumValues) > 0 {
			if runtimeEnumContains(property.EnumValues, *property.Value) {
				property.Editor = RuntimeEditor{Kind: "select"}
			} else {
				property.Editable = false
				property.ReadOnlyReason = "current_enum_value_unavailable"
				property.Editor = RuntimeEditor{Kind: "read_only"}
			}
		} else if validRuntimeInteger(*property.Value, "0", "255") {
			property.Editor = runtimeIntegerEditor("integer", "0", "255")
		} else {
			property.Editable = false
			property.ReadOnlyReason = "enum_values_unavailable"
			property.Editor = RuntimeEditor{Kind: "read_only"}
		}
	case "EnumProperty":
		if runtimeEnumContains(property.EnumValues, *property.Value) {
			property.Editor = RuntimeEditor{Kind: "select"}
		} else {
			property.Editable = false
			property.ReadOnlyReason = "enum_values_unavailable"
			property.Editor = RuntimeEditor{Kind: "read_only"}
		}
	case "Int8Property":
		property.Editor = runtimeIntegerEditor("integer", "-128", "127")
	case "Int16Property":
		property.Editor = runtimeIntegerEditor("integer", "-32768", "32767")
	case "IntProperty":
		property.Editor = runtimeIntegerEditor(
			"integer",
			"-2147483648",
			"2147483647",
		)
	case "Int64Property":
		property.Editor = runtimeIntegerEditor(
			"integer",
			"-9223372036854775808",
			"9223372036854775807",
		)
	case "UInt16Property":
		property.Editor = runtimeIntegerEditor("integer", "0", "65535")
	case "UInt32Property":
		property.Editor = runtimeIntegerEditor("integer", "0", "4294967295")
	case "UInt64Property":
		property.Editor = runtimeIntegerEditor(
			"integer",
			"0",
			"18446744073709551615",
		)
	case "FloatProperty":
		property.Editor = RuntimeEditor{
			Kind: "number",
			Min:  "-3.4028234663852886e+38",
			Max:  "3.4028234663852886e+38",
			Step: "any",
		}
	case "DoubleProperty":
		property.Editor = RuntimeEditor{
			Kind: "number",
			Min:  "-1.7976931348623157e+308",
			Max:  "1.7976931348623157e+308",
			Step: "any",
		}
	case "NameProperty":
		property.Editor = RuntimeEditor{
			Kind: "name",
			Help: "Enter one Unreal name without control characters.",
		}
	case "StrProperty":
		property.Editor = RuntimeEditor{Kind: "string"}
	case "TextProperty":
		property.Editor = RuntimeEditor{
			Kind: "unreal_text",
			Help: "Enter a valid Unreal FText export such as INVTEXT(\"text\").",
		}
	case "StructProperty", "ArrayProperty", "SetProperty", "MapProperty":
		property.Editor = RuntimeEditor{
			Kind: "unreal_text",
			Help: "Enter balanced Unreal exported-text syntax; Unreal validates the complete value before applying it.",
		}
	default:
		property.Editable = false
		property.ReadOnlyReason = "unsupported_editor_type"
		property.Editor = RuntimeEditor{Kind: "read_only"}
	}
}

func validRuntimeInteger(value, minimum, maximum string) bool {
	if value == "" {
		return false
	}
	start := 0
	if value[0] == '-' || value[0] == '+' {
		start = 1
	}
	if start == len(value) {
		return false
	}
	for index := start; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return false
	}
	minimumValue, minimumOK := new(big.Int).SetString(minimum, 10)
	maximumValue, maximumOK := new(big.Int).SetString(maximum, 10)
	return minimumOK && maximumOK &&
		parsed.Cmp(minimumValue) >= 0 &&
		parsed.Cmp(maximumValue) <= 0
}

func validRuntimeNumber(value string, bits int) bool {
	if !runtimeDecimalPattern.MatchString(value) {
		return false
	}
	number, err := strconv.ParseFloat(value, bits)
	mantissa := value
	if exponent := strings.IndexAny(mantissa, "eE"); exponent >= 0 {
		mantissa = mantissa[:exponent]
	}
	textIsZero := strings.IndexAny(mantissa, "123456789") < 0
	return err == nil &&
		!math.IsInf(number, 0) &&
		!math.IsNaN(number) &&
		(number != 0 || textIsZero)
}

func validRuntimeName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validBalancedRuntimeText(value string) bool {
	stack := make([]rune, 0, 16)
	var quote rune
	escaped := false
	for _, character := range value {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '"' || character == '\'' {
			quote = character
			continue
		}
		switch character {
		case '(', '[', '{':
			stack = append(stack, character)
		case ')', ']', '}':
			if len(stack) == 0 {
				return false
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if (open == '(' && character != ')') ||
				(open == '[' && character != ']') ||
				(open == '{' && character != '}') {
				return false
			}
		}
	}
	return quote == 0 && !escaped && len(stack) == 0
}

func validateRuntimePropertyValue(
	property RuntimeProperty,
	value string,
) error {
	if !property.Editable || property.Value == nil {
		return fmt.Errorf("%w: runtime property is read-only", errInvalidRuntimeRequest)
	}
	valid := false
	switch property.Editor.Kind {
	case "boolean":
		valid = value == "True" || value == "False"
	case "select":
		for _, option := range property.EnumValues {
			if value == option {
				valid = true
				break
			}
		}
	case "integer":
		valid = validRuntimeInteger(value, property.Editor.Min, property.Editor.Max)
	case "number":
		bits := 64
		if property.Type == "FloatProperty" {
			bits = 32
		}
		valid = validRuntimeNumber(value, bits)
	case "name":
		valid = validRuntimeName(value)
	case "string":
		valid = true
	case "unreal_text":
		valid = validBalancedRuntimeText(value)
	}
	if !valid {
		return fmt.Errorf(
			"%w: value does not match %s",
			errInvalidRuntimeRequest,
			property.Type,
		)
	}
	return nil
}

func runtimeHex(value string) string {
	return hex.EncodeToString([]byte(value))
}

func (m *Manager) runtimeBridgeExchange(
	parent context.Context,
	requestForID func(string) string,
	response any,
) error {
	if parent == nil || requestForID == nil || response == nil {
		return errors.New("invalid runtime bridge exchange")
	}
	if !m.runtimeSummaryView().Ready {
		m.sampleRuntimeBridgeStatus()
		if !m.runtimeSummaryView().Ready {
			return errRuntimeBridgeUnavailable
		}
	}

	m.runtimeCommandMu.Lock()
	defer m.runtimeCommandMu.Unlock()

	requestID, err := randomID()
	if err != nil {
		return err
	}
	requestText := requestForID(requestID)
	if len(requestText) < 4 || len(requestText) > 512<<10 ||
		!utf8.ValidString(requestText) {
		return errors.New("invalid runtime bridge request")
	}
	_, requestPath, responsePath := m.runtimePaths()
	if err := writeFileAtomic(requestPath, []byte(requestText+"\n"), 0600); err != nil {
		return fmt.Errorf("write runtime bridge request: %w", err)
	}

	ctx, cancel := context.WithTimeout(parent, runtimeBridgeCallTimeout)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return errors.New("runtime bridge request timed out")
			}
			return ctx.Err()
		case <-ticker.C:
			info, err := os.Stat(responsePath)
			if err != nil {
				continue
			}
			if info.Size() < 2 || info.Size() > runtimeBridgeResponseLimit {
				continue
			}
			data, err := os.ReadFile(responsePath)
			if err != nil {
				continue
			}
			var envelope struct {
				Version   int           `json:"version"`
				RequestID string        `json:"request_id"`
				OK        bool          `json:"ok"`
				Error     *RuntimeError `json:"error"`
			}
			if err := json.Unmarshal(data, &envelope); err != nil ||
				envelope.Version != 1 ||
				envelope.RequestID != requestID {
				continue
			}
			if err := json.Unmarshal(data, response); err != nil {
				return fmt.Errorf("decode runtime bridge response: %w", err)
			}
			if !envelope.OK {
				if envelope.Error == nil {
					return &runtimeBridgeProtocolError{
						Code:    "unknown",
						Message: "Runtime bridge request failed.",
					}
				}
				return &runtimeBridgeProtocolError{
					Code:         envelope.Error.Code,
					Message:      envelope.Error.Message,
					CurrentValue: envelope.Error.CurrentValue,
				}
			}
			return nil
		}
	}
}

func (m *Manager) runtimeTarget(
	ctx context.Context,
	targetID string,
) (RuntimeTargetView, error) {
	if !validRuntimeTargetID(targetID) {
		return RuntimeTargetView{}, fmt.Errorf("%w: invalid runtime target", errInvalidRuntimeRequest)
	}
	now := time.Now()
	m.runtimeMu.RLock()
	cached, cachedOK := m.runtimeTargetCache[targetID]
	m.runtimeMu.RUnlock()
	if cachedOK && now.Before(cached.ExpiresAt) {
		return cached.View, nil
	}

	var view RuntimeTargetView
	err := m.runtimeBridgeExchange(
		ctx,
		func(requestID string) string {
			return "V1\t" + requestID + "\tGET\t" + targetID
		},
		&view,
	)
	if err != nil {
		return view, err
	}
	if view.Version != 1 || !view.OK ||
		view.Target.ID != targetID ||
		!validRuntimeTargetKind(view.Target.Kind) ||
		view.Target.Class == "" ||
		!utf8.ValidString(view.Target.Class) ||
		len(view.Target.Class) > 191 ||
		!validRuntimePlayerName(view.Target.PlayerName) ||
		!validRuntimePlayFabID(view.Target.PlayFabID) ||
		!validPlayerPlatformIdentity(
			view.Target.Platform,
			view.Target.PlatformAccountID,
		) ||
		(view.Target.Kind != "player_controller" &&
			(view.Target.Platform != "" ||
				view.Target.PlatformAccountID != "")) ||
		view.Target.PlayerSlot < -1 ||
		view.Target.PlayerSlot > 1023 ||
		((view.Target.Kind == "game_mode" ||
			view.Target.Kind == "game_state") &&
			(view.Target.PlayerSlot != -1 ||
				view.Target.PlayerName != "" ||
				view.Target.PlayFabID != "")) ||
		((view.Target.Kind == "player_controller" ||
			view.Target.Kind == "player_state" ||
			view.Target.Kind == "pawn") &&
			view.Target.PlayerSlot < 0) ||
		(view.Target.Kind != "player_controller" &&
			(view.Target.PlayerName != "" ||
				view.Target.PlayFabID != "")) ||
		(view.AccountProgress != nil &&
			(view.Target.Kind != "player_controller" ||
				view.AccountProgress.XP < 0 ||
				view.AccountProgress.Level < 1 ||
				view.AccountProgress.Level > playerMaximumLevel)) ||
		view.PropertyCount != len(view.Properties) ||
		view.PropertyCount < 0 ||
		view.PropertyCount > 8192 ||
		len(view.ClassChain) < 1 ||
		len(view.ClassChain) > 128 {
		return RuntimeTargetView{}, errors.New("runtime bridge returned an invalid target response")
	}
	classes := make(map[string]struct{}, len(view.ClassChain))
	for _, className := range view.ClassChain {
		if !validRuntimeIdentifier(className) {
			return RuntimeTargetView{}, errors.New("runtime bridge returned an invalid class chain")
		}
		classes[className] = struct{}{}
	}
	if view.ClassChain[0] != view.Target.Class {
		return RuntimeTargetView{}, errors.New("runtime bridge returned a mismatched target class")
	}
	for index := range view.Properties {
		property := &view.Properties[index]
		if _, exists := classes[property.DeclaringClass]; !exists ||
			!validRuntimeIdentifier(property.Name) ||
			!validRuntimeIdentifier(property.Type) ||
			property.ArrayDim < 1 ||
			property.ArrayDim > 1024 ||
			property.ArrayIndex < 0 ||
			property.ArrayIndex >= property.ArrayDim ||
			property.ElementSize < 1 ||
			property.ElementSize > 0x1000000 ||
			property.Offset < 0 ||
			property.Offset > 0x1000000 ||
			(property.Value != nil && !validRuntimeValue(*property.Value)) ||
			!validRuntimeEnumValues(property.Type, property.EnumValues) {
			return RuntimeTargetView{}, errors.New("runtime bridge returned an invalid property")
		}
		normalizeRuntimeProperty(property)
	}
	m.runtimeMu.Lock()
	if m.runtimeTargetCache == nil {
		m.runtimeTargetCache = make(map[string]runtimeTargetCacheEntry)
	}
	m.runtimeTargetCache[targetID] = runtimeTargetCacheEntry{
		View:      view,
		ExpiresAt: now.Add(runtimeTargetCacheLifetime),
	}
	m.runtimeMu.Unlock()
	return view, nil
}

type runtimePropertyChangeRequest struct {
	TargetID       string `json:"target_id"`
	DeclaringClass string `json:"declaring_class"`
	Name           string `json:"name"`
	ArrayIndex     int    `json:"array_index"`
	ExpectedValue  string `json:"expected_value"`
	NewValue       string `json:"new_value"`
}

func (m *Manager) changeRuntimeProperty(
	ctx context.Context,
	change runtimePropertyChangeRequest,
) (RuntimePropertyChangeView, error) {
	if !validRuntimeTargetID(change.TargetID) ||
		!validRuntimeIdentifier(change.DeclaringClass) ||
		!validRuntimeIdentifier(change.Name) ||
		change.ArrayIndex < 0 || change.ArrayIndex > 1023 ||
		!validRuntimeValue(change.ExpectedValue) ||
		!validRuntimeValue(change.NewValue) {
		return RuntimePropertyChangeView{}, fmt.Errorf(
			"%w: invalid runtime property change",
			errInvalidRuntimeRequest,
		)
	}
	targetView, err := m.runtimeTarget(ctx, change.TargetID)
	if err != nil {
		return RuntimePropertyChangeView{}, err
	}
	var property *RuntimeProperty
	for index := range targetView.Properties {
		candidate := &targetView.Properties[index]
		if candidate.DeclaringClass == change.DeclaringClass &&
			candidate.Name == change.Name &&
			candidate.ArrayIndex == change.ArrayIndex {
			property = candidate
			break
		}
	}
	if property == nil {
		return RuntimePropertyChangeView{}, fmt.Errorf(
			"%w: runtime property was not found",
			errInvalidRuntimeRequest,
		)
	}
	if err := validateRuntimePropertyValue(*property, change.NewValue); err != nil {
		return RuntimePropertyChangeView{}, err
	}
	var view RuntimePropertyChangeView
	err = m.runtimeBridgeExchange(
		ctx,
		func(requestID string) string {
			return strings.Join([]string{
				"V1",
				requestID,
				"SET",
				change.TargetID,
				runtimeHex(change.DeclaringClass),
				runtimeHex(change.Name),
				fmt.Sprintf("%d", change.ArrayIndex),
				runtimeHex(change.ExpectedValue),
				runtimeHex(change.NewValue),
			}, "\t")
		},
		&view,
	)
	if err != nil {
		return view, err
	}
	if view.Version != 1 || !view.OK ||
		view.Target.ID != change.TargetID ||
		view.Property.DeclaringClass != change.DeclaringClass ||
		view.Property.Name != change.Name ||
		view.Property.ArrayIndex != change.ArrayIndex ||
		view.Property.OldValue != change.ExpectedValue ||
		!validRuntimeValue(view.Property.NewValue) {
		return RuntimePropertyChangeView{}, errors.New(
			"runtime bridge returned an invalid property-change response",
		)
	}
	m.runtimeMu.Lock()
	delete(m.runtimeTargetCache, change.TargetID)
	m.runtimeMu.Unlock()
	return view, nil
}
