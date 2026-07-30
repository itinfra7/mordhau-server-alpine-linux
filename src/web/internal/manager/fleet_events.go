package manager

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	FleetEventAllChat         = "all_chat"
	FleetEventTeamChat        = "team_chat"
	FleetEventWebSAY          = "web_say"
	FleetEventRCONSAY         = "rcon_say"
	FleetEventPlayerLogin     = "player_login"
	FleetEventPlayerLogout    = "player_logout"
	fleetRecentEventRetention = 10 * time.Minute
	fleetDeliveryShardCount   = 16
	fleetDeliveryQueueSize    = 128
)

type FleetEvent struct {
	ID           string    `json:"id"`
	OriginNodeID string    `json:"origin_node_id"`
	Sequence     uint64    `json:"sequence"`
	Time         time.Time `json:"time"`
	Category     string    `json:"category"`
	PlayerName   string    `json:"player_name,omitempty"`
	Message      string    `json:"message,omitempty"`
}

type fleetDelivery struct {
	Peer  *fleetManagedPeer
	Event FleetEvent
}

func validFleetEventCategory(category string) bool {
	switch category {
	case FleetEventAllChat,
		FleetEventTeamChat,
		FleetEventWebSAY,
		FleetEventRCONSAY,
		FleetEventPlayerLogin,
		FleetEventPlayerLogout:
		return true
	default:
		return false
	}
}

func sanitizeFleetText(value string, maximumRunes int) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maximumRunes {
		value = string(runes[:maximumRunes])
	}
	return value
}

func normalizeFleetEvent(event FleetEvent) (FleetEvent, error) {
	if !validFleetNodeID(event.OriginNodeID) {
		return FleetEvent{}, errors.New("invalid fleet event origin")
	}
	if !validFleetEventID(event) {
		return FleetEvent{}, errors.New("invalid fleet event ID")
	}
	if !validFleetEventCategory(event.Category) {
		return FleetEvent{}, errors.New("invalid fleet event category")
	}
	event.PlayerName = sanitizeFleetText(event.PlayerName, 128)
	event.Message = sanitizeFleetText(event.Message, unicodeMessageMaxRunes)
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	switch event.Category {
	case FleetEventAllChat, FleetEventTeamChat:
		if event.PlayerName == "" || event.Message == "" {
			return FleetEvent{}, errors.New("fleet chat event is incomplete")
		}
	case FleetEventWebSAY, FleetEventRCONSAY:
		if event.Message == "" {
			return FleetEvent{}, errors.New("fleet SAY event is empty")
		}
	case FleetEventPlayerLogin, FleetEventPlayerLogout:
		if event.PlayerName == "" {
			return FleetEvent{}, errors.New("fleet player lifecycle event is incomplete")
		}
	}
	return event, nil
}

func validFleetEventID(event FleetEvent) bool {
	if event.Sequence == 0 || len(event.ID) > 160 {
		return false
	}
	parts := strings.Split(event.ID, ":")
	if len(parts) != 3 ||
		parts[0] != event.OriginNodeID ||
		parts[1] == "" ||
		len(parts[1]) > 64 {
		return false
	}
	for index := 0; index < len(parts[1]); index++ {
		character := parts[1][index]
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '_' &&
			character != '-' {
			return false
		}
	}
	sequence, err := strconv.ParseUint(parts[2], 10, 64)
	return err == nil && sequence == event.Sequence
}

func (m *Manager) publishFleetEvent(
	category string,
	playerName string,
	message string,
) {
	m.publishFleetEventAt(
		m.fleetNow(),
		category,
		playerName,
		message,
	)
}

func (m *Manager) publishFleetEventAt(
	eventTime time.Time,
	category string,
	playerName string,
	message string,
) {
	settings := m.currentFleetSettings()
	if settings.Role == FleetRoleStandalone || !settings.LocalSync.enabled(category) {
		return
	}
	identity, err := m.loadFleetIdentity()
	if err != nil {
		return
	}

	m.fleetEventMu.Lock()
	m.fleetEventSequence++
	sequence := m.fleetEventSequence
	event := FleetEvent{
		ID: fmt.Sprintf(
			"%s:%s:%d",
			identity.NodeID,
			m.fleetBootID,
			sequence,
		),
		OriginNodeID: identity.NodeID,
		Sequence:     sequence,
		Time:         eventTime,
		Category:     category,
		PlayerName:   playerName,
		Message:      message,
	}
	event, err = normalizeFleetEvent(event)
	if err != nil {
		m.fleetEventMu.Unlock()
		return
	}
	subscriberDropped := false
	for _, subscriber := range m.fleetSubscribers {
		select {
		case subscriber <- event:
		default:
			subscriberDropped = true
		}
	}
	routeDropped := false
	if settings.Role == FleetRoleController {
		select {
		case m.fleetRouteQueue <- event:
		default:
			routeDropped = true
		}
	}
	m.fleetEventMu.Unlock()

	if subscriberDropped {
		m.auditActorEvent(
			"system",
			"local",
			"fleet_event_dropped",
			map[string]string{
				"reason":   "feed_subscriber_full",
				"category": event.Category,
			},
		)
	}
	if routeDropped {
		m.auditActorEvent(
			"system",
			"local",
			"fleet_event_dropped",
			map[string]string{
				"reason":   "route_queue_full",
				"category": event.Category,
			},
		)
	}
}

func (m *Manager) subscribeFleetEvents() (uint64, <-chan FleetEvent) {
	channel := make(chan FleetEvent, 128)
	m.fleetEventMu.Lock()
	m.fleetSubscriberID++
	id := m.fleetSubscriberID
	m.fleetSubscribers[id] = channel
	m.fleetEventMu.Unlock()
	return id, channel
}

func (m *Manager) unsubscribeFleetEvents(id uint64) {
	m.fleetEventMu.Lock()
	delete(m.fleetSubscribers, id)
	m.fleetEventMu.Unlock()
}

func (m *Manager) enqueueFleetRoute(event FleetEvent) {
	select {
	case m.fleetRouteQueue <- event:
	default:
		m.auditActorEvent(
			"system",
			"local",
			"fleet_event_dropped",
			map[string]string{"reason": "route_queue_full", "category": event.Category},
		)
	}
}

func (m *Manager) fleetEventSeen(event FleetEvent) bool {
	now := m.fleetNow()
	m.fleetEventMu.Lock()
	defer m.fleetEventMu.Unlock()
	for id, seenAt := range m.fleetRecentDeliveries {
		if now.Sub(seenAt) > fleetRecentEventRetention {
			delete(m.fleetRecentDeliveries, id)
		}
	}
	if _, exists := m.fleetRecentDeliveries[event.ID]; exists {
		return true
	}
	m.fleetRecentDeliveries[event.ID] = now
	return false
}

func (m *Manager) fleetBrokerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-m.fleetRouteQueue:
			m.routeFleetEvent(event)
		}
	}
}

func (m *Manager) routeFleetEvent(event FleetEvent) {
	event, err := normalizeFleetEvent(event)
	if err != nil || m.fleetEventSeen(event) {
		return
	}
	settings := m.currentFleetSettings()
	if settings.Role != FleetRoleController {
		return
	}

	localNodeID := m.localFleetNodeID()
	_, originPolicy, originExists := fleetEventOrigin(
		settings,
		localNodeID,
		event.OriginNodeID,
	)
	if !originExists || !originPolicy.enabled(event.Category) {
		return
	}

	if event.OriginNodeID != localNodeID &&
		settings.LocalSync.enabled(event.Category) {
		m.enqueueFleetDelivery(fleetDelivery{
			Event: event,
		})
	}
	for index := range settings.Managed {
		peer := settings.Managed[index]
		if peer.NodeID == event.OriginNodeID || !peer.Sync.enabled(event.Category) {
			continue
		}
		peerCopy := peer
		m.enqueueFleetDelivery(fleetDelivery{
			Peer:  &peerCopy,
			Event: event,
		})
	}
}

func fleetEventOrigin(
	settings fleetSettingsFile,
	localNodeID string,
	originNodeID string,
) (string, FleetSyncPolicy, bool) {
	if originNodeID == localNodeID && localNodeID != "" {
		return settings.Alias, settings.LocalSync, true
	}
	for _, peer := range settings.Managed {
		if peer.NodeID == originNodeID {
			return peer.Alias, peer.Sync, true
		}
	}
	return "", FleetSyncPolicy{}, false
}

func fleetManagedPeerByID(
	settings fleetSettingsFile,
	nodeID string,
) (fleetManagedPeer, bool) {
	for _, peer := range settings.Managed {
		if peer.NodeID == nodeID {
			return peer, true
		}
	}
	return fleetManagedPeer{}, false
}

func (m *Manager) enqueueFleetDelivery(delivery fleetDelivery) {
	if len(m.fleetDeliverQueues) == 0 {
		m.auditActorEvent(
			"system",
			"local",
			"fleet_event_dropped",
			map[string]string{
				"reason":   "delivery_workers_unavailable",
				"category": delivery.Event.Category,
			},
		)
		return
	}
	queue := m.fleetDeliverQueues[fleetDeliveryShard(delivery)%len(m.fleetDeliverQueues)]
	select {
	case queue <- delivery:
	default:
		m.auditActorEvent(
			"system",
			"local",
			"fleet_event_dropped",
			map[string]string{
				"reason":   "delivery_queue_full",
				"category": delivery.Event.Category,
			},
		)
	}
}

func fleetDeliveryShard(delivery fleetDelivery) int {
	key := "local"
	if delivery.Peer != nil {
		key = delivery.Peer.NodeID
	}
	hash := uint32(2166136261)
	for index := 0; index < len(key); index++ {
		hash ^= uint32(key[index])
		hash *= 16777619
	}
	return int(hash & 0x7fffffff)
}

func newFleetDeliveryQueues() []chan fleetDelivery {
	queues := make([]chan fleetDelivery, fleetDeliveryShardCount)
	for index := range queues {
		queues[index] = make(chan fleetDelivery, fleetDeliveryQueueSize)
	}
	return queues
}

func (m *Manager) fleetDeliveryLoop(
	ctx context.Context,
	deliveries <-chan fleetDelivery,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case delivery := <-deliveries:
			settings := m.currentFleetSettings()
			if settings.Role != FleetRoleController {
				continue
			}
			originAlias, originPolicy, originExists := fleetEventOrigin(
				settings,
				m.localFleetNodeID(),
				delivery.Event.OriginNodeID,
			)
			if !originExists || !originPolicy.enabled(delivery.Event.Category) {
				continue
			}
			if delivery.Peer == nil {
				if !settings.LocalSync.enabled(delivery.Event.Category) {
					continue
				}
				if err := m.deliverFleetEventLocal(
					originAlias,
					delivery.Event,
				); err != nil {
					m.auditActorEvent(
						"system",
						"local",
						"fleet_event_delivery_failed",
						map[string]string{
							"node_id":  m.localFleetNodeID(),
							"category": delivery.Event.Category,
							"error":    safeAuditText(err.Error(), 200),
						},
					)
				}
				continue
			}
			currentPeer, exists := fleetManagedPeerByID(
				settings,
				delivery.Peer.NodeID,
			)
			if !exists || !currentPeer.Sync.enabled(delivery.Event.Category) {
				continue
			}
			deliveryContext, cancel := context.WithTimeout(ctx, 8*time.Second)
			err := m.deliverFleetEventRemote(
				deliveryContext,
				currentPeer,
				originAlias,
				delivery.Event,
			)
			cancel()
			if err != nil {
				m.auditActorEvent(
					"system",
					"local",
					"fleet_event_delivery_failed",
					map[string]string{
						"node_id":  currentPeer.NodeID,
						"category": delivery.Event.Category,
						"error":    safeAuditText(err.Error(), 200),
					},
				)
			}
		}
	}
}

func fleetServerLabel(alias string) string {
	alias = sanitizeFleetText(alias, fleetAliasMaxRunes)
	if strings.HasSuffix(strings.ToLower(alias), " server") {
		return alias
	}
	return alias + " Server"
}

func formatFleetEventMessage(alias string, event FleetEvent) (string, error) {
	label := fleetServerLabel(alias)
	var message string
	switch event.Category {
	case FleetEventAllChat:
		message = fmt.Sprintf("(%s) <%s> : %s", label, event.PlayerName, event.Message)
	case FleetEventTeamChat:
		message = fmt.Sprintf("(%s · TEAM) <%s> : %s", label, event.PlayerName, event.Message)
	case FleetEventWebSAY:
		message = fmt.Sprintf("(%s · WEB SAY) %s", label, event.Message)
	case FleetEventRCONSAY:
		message = fmt.Sprintf("(%s · RCON SAY) %s", label, event.Message)
	case FleetEventPlayerLogin:
		message = fmt.Sprintf("(%s) <%s> joined the server.", label, event.PlayerName)
	case FleetEventPlayerLogout:
		message = fmt.Sprintf("(%s) <%s> left the server.", label, event.PlayerName)
	default:
		return "", errors.New("unsupported fleet event category")
	}
	runes := []rune(message)
	if len(runes) > unicodeMessageMaxRunes {
		runes = append(runes[:unicodeMessageMaxRunes-1], '…')
		message = string(runes)
	}
	for len(message) > unicodeMessageMaxBytes {
		runes = []rune(message)
		if len(runes) < 2 {
			return "", errors.New("fleet event message exceeds the Unicode bridge limit")
		}
		runes = append(runes[:len(runes)-2], '…')
		message = string(runes)
	}
	if err := validateUnicodeMessage(message); err != nil {
		return "", err
	}
	return message, nil
}

func (m *Manager) deliverFleetEventLocal(alias string, event FleetEvent) error {
	message, err := formatFleetEventMessage(alias, event)
	if err != nil {
		return err
	}
	send := m.sendUnicodeRCONMessage
	if m.fleetMessageSend != nil {
		send = m.fleetMessageSend
	}
	if err := send(message); err != nil {
		return err
	}
	m.addRCONEvent("fleet", message)
	return nil
}

func rconSAYMessage(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", false
	}
	nameEnd := strings.IndexAny(command, " \t")
	if nameEnd < 0 || !strings.EqualFold(command[:nameEnd], "say") {
		return "", false
	}
	message := strings.TrimSpace(command[nameEnd:])
	if len(message) >= 2 {
		first := message[0]
		last := message[len(message)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			message = strings.TrimSpace(message[1 : len(message)-1])
		}
	}
	message = sanitizeFleetText(message, unicodeMessageMaxRunes)
	return message, message != ""
}

func (m *Manager) publishFleetGameLogEvent(event gameLogEvent) {
	switch {
	case event.Kind == "chat" && event.ChatChannel == "ALL":
		m.publishFleetEventAt(
			event.Time,
			FleetEventAllChat,
			event.PlayerName,
			event.ChatMessage,
		)
	case event.Kind == "chat" && event.ChatChannel == "TEAM":
		m.publishFleetEventAt(
			event.Time,
			FleetEventTeamChat,
			event.PlayerName,
			event.ChatMessage,
		)
	case event.PlayerAction == "login":
		m.publishFleetEventAt(
			event.Time,
			FleetEventPlayerLogin,
			event.PlayerName,
			"",
		)
	case event.PlayerAction == "logout":
		m.publishFleetEventAt(
			event.Time,
			FleetEventPlayerLogout,
			event.PlayerName,
			"",
		)
	}
}
