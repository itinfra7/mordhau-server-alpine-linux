package manager

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxAccessRuleCommentRunes = 160

type accessNetwork struct {
	canonical string
	prefixes  []netip.Prefix
}

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

func normalizeAccessNetwork(value string) (accessNetwork, error) {
	value = strings.TrimSpace(value)
	if prefix, err := netip.ParsePrefix(value); err == nil {
		prefix = prefix.Masked()
		return accessNetwork{
			canonical: prefix.String(),
			prefixes:  []netip.Prefix{prefix},
		}, nil
	}
	if address, err := netip.ParseAddr(value); err == nil {
		address = address.Unmap()
		addressBits := 128
		if address.Is4() {
			addressBits = 32
		}
		prefix := netip.PrefixFrom(address, addressBits)
		return accessNetwork{
			canonical: prefix.String(),
			prefixes:  []netip.Prefix{prefix},
		}, nil
	}

	hyphens := strings.Count(value, "-")
	tildes := strings.Count(value, "~")
	if hyphens+tildes == 0 {
		return accessNetwork{}, errors.New(
			"invalid IPv4/IPv6 address, CIDR network, or inclusive IPv4 range",
		)
	}
	if hyphens+tildes != 1 {
		return accessNetwork{}, errors.New(
			"an IPv4 range must contain exactly one '-' or '~' separator",
		)
	}
	separator := "-"
	if tildes == 1 {
		separator = "~"
	}
	rangeParts := strings.SplitN(value, separator, 2)
	start, startErr := netip.ParseAddr(strings.TrimSpace(rangeParts[0]))
	end, endErr := netip.ParseAddr(strings.TrimSpace(rangeParts[1]))
	if startErr != nil || endErr != nil {
		return accessNetwork{}, errors.New(
			"an IPv4 range must contain two IPv4 addresses",
		)
	}
	start = start.Unmap()
	end = end.Unmap()
	if !start.Is4() || !end.Is4() {
		return accessNetwork{}, errors.New("address ranges are supported for IPv4 only")
	}

	startValue := ipv4Value(start)
	endValue := ipv4Value(end)
	if startValue > endValue {
		return accessNetwork{}, errors.New("IPv4 range start must not exceed its end")
	}
	if startValue == endValue {
		prefix := netip.PrefixFrom(start, 32)
		return accessNetwork{
			canonical: prefix.String(),
			prefixes:  []netip.Prefix{prefix},
		}, nil
	}
	return accessNetwork{
		canonical: start.String() + "-" + end.String(),
		prefixes:  ipv4RangePrefixes(startValue, endValue),
	}, nil
}

func ipv4Value(address netip.Addr) uint32 {
	bytes := address.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

func ipv4Address(value uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], value)
	return netip.AddrFrom4(bytes)
}

func ipv4RangePrefixes(start, end uint32) []netip.Prefix {
	current := uint64(start)
	last := uint64(end)
	prefixes := make([]netip.Prefix, 0, 8)
	for current <= last {
		alignmentBits := bits.TrailingZeros32(uint32(current))
		remaining := last - current + 1
		sizeBits := bits.Len64(remaining) - 1
		hostBits := alignmentBits
		if sizeBits < hostBits {
			hostBits = sizeBits
		}
		prefixes = append(prefixes, netip.PrefixFrom(
			ipv4Address(uint32(current)),
			32-hostBits,
		))
		current += uint64(1) << hostBits
	}
	return prefixes
}

func normalizeAccessRuleComment(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("access rule comment must be valid UTF-8")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", errors.New("access rule comment must not contain control characters")
		}
	}
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > maxAccessRuleCommentRunes {
		return "", errors.New("access rule comment must not exceed 160 characters")
	}
	return value, nil
}

func accessAllowed(ip netip.Addr, config AccessConfig, now time.Time) bool {
	ip = ip.Unmap()
	bestBits := -1
	bestAction := ""
	for _, rule := range config.Rules {
		if rule.Temporary && rule.ExpiresAt != nil && !now.Before(*rule.ExpiresAt) {
			continue
		}
		network, err := normalizeAccessNetwork(rule.Network)
		if err != nil {
			continue
		}
		for _, prefix := range network.prefixes {
			if prefix.Addr().Is4() != ip.Is4() || !prefix.Contains(ip) {
				continue
			}
			// An active exact-address emergency rule must keep the administrator
			// connected even if an older deny rule has the same prefix.
			if rule.Temporary && rule.Action == "allow" && prefix.Bits() == ip.BitLen() {
				return true
			}
			prefixBits := prefix.Bits()
			if prefixBits > bestBits || (prefixBits == bestBits && rule.Action == "deny") {
				bestBits = prefixBits
				bestAction = rule.Action
			}
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

func (m *Manager) saveAccessRule(
	id, action, network string,
	comment *string,
) (AccessRule, error) {
	if action != "allow" && action != "deny" {
		return AccessRule{}, errors.New("rule action must be allow or deny")
	}
	normalized, err := normalizeAccessNetwork(network)
	if err != nil {
		return AccessRule{}, err
	}
	normalizedComment := ""
	if comment != nil {
		normalizedComment, err = normalizeAccessRuleComment(*comment)
		if err != nil {
			return AccessRule{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeAccessLocked(time.Now())
	if id != "" {
		for index := range m.access.Rules {
			if m.access.Rules[index].ID == id {
				if m.access.Rules[index].Temporary {
					return AccessRule{}, errors.New("temporary emergency rules cannot be edited")
				}
				m.access.Rules[index].Action = action
				m.access.Rules[index].Network = normalized.canonical
				if comment != nil {
					m.access.Rules[index].Comment = normalizedComment
				}
				if err := writeJSONAtomic(accessPath, m.access, 0600); err != nil {
					return AccessRule{}, err
				}
				return m.access.Rules[index], nil
			}
		}
		return AccessRule{}, errors.New("access rule not found")
	}
	id, err = randomID()
	if err != nil {
		return AccessRule{}, err
	}
	rule := AccessRule{
		ID:        id,
		Action:    action,
		Network:   normalized.canonical,
		Comment:   normalizedComment,
		CreatedAt: time.Now(),
	}
	m.access.Rules = append(m.access.Rules, rule)
	if err := writeJSONAtomic(accessPath, m.access, 0600); err != nil {
		return AccessRule{}, err
	}
	return rule, nil
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
