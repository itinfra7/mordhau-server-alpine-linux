package manager

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

func requestIP(request *http.Request) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}, err
	}
	return ip.Unmap(), nil
}

func normalizeNetwork(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked(), nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, errors.New("invalid IPv4/IPv6 address or CIDR network")
	}
	address = address.Unmap()
	bits := 128
	if address.Is4() {
		bits = 32
	}
	return netip.PrefixFrom(address, bits), nil
}

func accessAllowed(ip netip.Addr, config AccessConfig, now time.Time) bool {
	ip = ip.Unmap()
	bestBits := -1
	bestAction := ""
	for _, rule := range config.Rules {
		if rule.Temporary && rule.ExpiresAt != nil && !now.Before(*rule.ExpiresAt) {
			continue
		}
		prefix, err := netip.ParsePrefix(rule.Network)
		if err != nil {
			continue
		}
		prefix = prefix.Masked()
		if prefix.Addr().Is4() != ip.Is4() || !prefix.Contains(ip) {
			continue
		}
		// An active exact-address emergency rule must keep the administrator
		// connected even if an older deny rule has the same prefix.
		if rule.Temporary && rule.Action == "allow" && prefix.Bits() == ip.BitLen() {
			return true
		}
		bits := prefix.Bits()
		if bits > bestBits || (bits == bestBits && rule.Action == "deny") {
			bestBits = bits
			bestAction = rule.Action
		}
	}
	if bestBits >= 0 {
		return bestAction == "allow"
	}
	return config.BasePolicy == "all_allow"
}

func (m *Manager) isAccessAllowed(ip netip.Addr) bool {
	m.mu.RLock()
	config := AccessConfig{
		Version:    m.access.Version,
		BasePolicy: m.access.BasePolicy,
		Rules:      append([]AccessRule(nil), m.access.Rules...),
	}
	m.mu.RUnlock()
	return accessAllowed(ip, config, time.Now())
}

func (m *Manager) accessConfig() AccessConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return AccessConfig{
		Version:    m.access.Version,
		BasePolicy: m.access.BasePolicy,
		Rules:      append([]AccessRule{}, m.access.Rules...),
	}
}

func (m *Manager) setBasePolicy(policy string, currentIP netip.Addr) error {
	if policy != "all_allow" && policy != "all_deny" {
		return errors.New("invalid base policy")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeAccessLocked(time.Now())
	m.access.BasePolicy = policy
	if policy == "all_deny" {
		prefix := netip.PrefixFrom(currentIP.Unmap(), currentIP.Unmap().BitLen()).String()
		expires := time.Now().Add(emergencyDuration)
		found := false
		for i := range m.access.Rules {
			rule := &m.access.Rules[i]
			if rule.Temporary && rule.Action == "allow" && rule.Network == prefix {
				rule.ExpiresAt = &expires
				found = true
				break
			}
		}
		if !found {
			id, err := randomID()
			if err != nil {
				return err
			}
			m.access.Rules = append(m.access.Rules, AccessRule{
				ID:        id,
				Action:    "allow",
				Network:   prefix,
				Temporary: true,
				ExpiresAt: &expires,
				CreatedAt: time.Now(),
			})
		}
	}
	return writeJSONAtomic(accessPath, m.access, 0600)
}

func (m *Manager) saveAccessRule(id, action, network string) error {
	if action != "allow" && action != "deny" {
		return errors.New("rule action must be allow or deny")
	}
	prefix, err := normalizeNetwork(network)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeAccessLocked(time.Now())
	if id != "" {
		for index := range m.access.Rules {
			if m.access.Rules[index].ID == id {
				if m.access.Rules[index].Temporary {
					return errors.New("temporary emergency rules cannot be edited")
				}
				m.access.Rules[index].Action = action
				m.access.Rules[index].Network = prefix.String()
				return writeJSONAtomic(accessPath, m.access, 0600)
			}
		}
		return errors.New("access rule not found")
	}
	id, err = randomID()
	if err != nil {
		return err
	}
	m.access.Rules = append(m.access.Rules, AccessRule{
		ID:        id,
		Action:    action,
		Network:   prefix.String(),
		CreatedAt: time.Now(),
	})
	return writeJSONAtomic(accessPath, m.access, 0600)
}

func (m *Manager) deleteAccessRule(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := -1
	for i, rule := range m.access.Rules {
		if rule.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("access rule not found")
	}
	m.access.Rules = append(m.access.Rules[:index], m.access.Rules[index+1:]...)
	return writeJSONAtomic(accessPath, m.access, 0600)
}
