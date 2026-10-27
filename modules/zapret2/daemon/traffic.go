// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"sort"
	"strconv"
	"strings"
)

type ApplicationProfile struct {
	ID       string `json:"id"`
	Method   string `json:"method"`
	Ports    string `json:"ports"`
	Packets  int    `json:"packets"`
	Position int    `json:"position"`
	Repeats  int    `json:"repeats"`
	Fragment int    `json:"fragment"`
	Padding  int    `json:"padding"`
}

type ApplicationKind struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	L7        string   `json:"-"`
	Payload   string   `json:"-"`
	Blob      string   `json:"-"`
	Methods   []string `json:"methods"`
}

func applicationKinds() []ApplicationKind {
	tcp := []string{"split", "disorder", "fake", "fake-split", "fake-disorder", "fragment"}
	udp := []string{"fake", "fragment", "fake-fragment", "length"}
	return []ApplicationKind{
		{"mtproto", "Telegram MTProto", "tcp", "mtproto", "mtproto_initial", "0x00000000", tcp},
		{"dtls", "DTLS / OpenConnect", "udp", "dtls", "dtls_client_hello", "ocf_dtls", udp},
		{"wireguard", "WireGuard", "udp", "wireguard", "wireguard_initiation,wireguard_response,wireguard_cookie", "ocf_wireguard", udp},
		{"stun", "STUN / calls", "udp", "stun", "stun", "ocf_stun", udp},
		{"discord", "Discord discovery", "udp", "discord", "discord_ip_discovery", "ocf_discord", udp},
		{"unknown-udp", "Voice, games and other UDP", "udp", "unknown", "unknown", "0x00000000", udp},
		{"unknown-tcp", "Other TCP", "tcp", "unknown", "unknown", "0x00000000", tcp},
	}
}

func applicationKind(id string) (ApplicationKind, bool) {
	for _, kind := range applicationKinds() {
		if kind.ID == id {
			return kind, true
		}
	}
	return ApplicationKind{}, false
}

type portRange struct{ First, Last int }

func parsePortRanges(value string) ([]portRange, error) {
	value = strings.TrimSpace(value)
	if value == "*" {
		return []portRange{{1, 65535}}, nil
	}
	if len(value) > 256 || value == "" {
		return nil, errors.New("invalid port selection")
	}
	values := strings.Split(value, ",")
	if len(values) > 15 {
		return nil, errors.New("too many port ranges")
	}
	ranges := []portRange{}
	for _, value := range values {
		parts := strings.Split(strings.TrimSpace(value), "-")
		if len(parts) > 2 {
			return nil, errors.New("invalid port range")
		}
		first, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || first < 1 || first > 65535 {
			return nil, errors.New("invalid port")
		}
		last := first
		if len(parts) == 2 {
			last, err = strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil || last < first || last > 65535 {
				return nil, errors.New("invalid port range")
			}
		}
		ranges = append(ranges, portRange{first, last})
	}
	return mergePortRanges(ranges), nil
}

func mergePortRanges(ranges []portRange) []portRange {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].First < ranges[j].First })
	merged := []portRange{}
	for _, item := range ranges {
		if len(merged) > 0 && item.First <= merged[len(merged)-1].Last+1 {
			if item.Last > merged[len(merged)-1].Last {
				merged[len(merged)-1].Last = item.Last
			}
		} else {
			merged = append(merged, item)
		}
	}
	return merged
}

func portRangeText(item portRange, separator string) string {
	value := strconv.Itoa(item.First)
	if item.Last != item.First {
		value += separator + strconv.Itoa(item.Last)
	}
	return value
}

func normalizePortRanges(value string) (string, error) {
	ranges, err := parsePortRanges(value)
	if err != nil {
		return "", err
	}
	if len(ranges) == 1 && ranges[0] == (portRange{1, 65535}) {
		return "*", nil
	}
	values := []string{}
	for _, item := range ranges {
		values = append(values, portRangeText(item, "-"))
	}
	return strings.Join(values, ","), nil
}

func validateApplications(c *Config) error {
	if len(c.Applications) > len(applicationKinds()) {
		return errors.New("too many application profiles")
	}
	seen := map[string]bool{}
	for i := range c.Applications {
		profile := &c.Applications[i]
		kind, known := applicationKind(profile.ID)
		if !known || seen[profile.ID] {
			return errors.New("unknown or duplicate application protocol")
		}
		seen[profile.ID] = true
		allowed := false
		for _, method := range kind.Methods {
			allowed = allowed || method == profile.Method
		}
		if !allowed || profile.Packets < 1 || profile.Packets > 16 || profile.Position < 1 || profile.Position > 1500 || profile.Repeats < 1 || profile.Repeats > 20 || profile.Fragment < 8 || profile.Fragment > 1400 || profile.Fragment%8 != 0 || profile.Padding < 1 || profile.Padding > 256 {
			return errors.New("invalid application strategy settings")
		}
		ports, err := normalizePortRanges(profile.Ports)
		if err != nil {
			return err
		}
		profile.Ports = ports
	}
	return nil
}

func usesEngineSets(c Config) bool {
	return c.ManagedSets || len(c.Applications) > 0
}

func processingPacketLimit(c Config) int {
	limit := c.PacketLimit
	for _, profile := range c.Applications {
		// Include TCP setup/ACK packets before the selected number of data packets.
		if profile.Packets+3 > limit {
			limit = profile.Packets + 3
		}
	}
	return limit
}

func firewallPortGroups(c Config, transport string) []string {
	ports, method := c.TCPPorts, c.TCP
	if transport == "udp" {
		ports, method = c.UDPPorts, c.UDP
	}
	ranges := []portRange{}
	if method != "off" {
		for _, port := range ports {
			ranges = append(ranges, portRange{port, port})
		}
	}
	if len(c.Networks) > 0 {
		for _, profile := range c.Applications {
			kind, _ := applicationKind(profile.ID)
			if kind.Transport == transport {
				values, _ := parsePortRanges(profile.Ports)
				ranges = append(ranges, values...)
			}
		}
	}
	ranges = mergePortRanges(ranges)
	if len(ranges) == 1 && ranges[0] == (portRange{1, 65535}) {
		return []string{"*"}
	}
	// xt_multiport counts a range as two slots. Disjoint groups prevent a packet
	// from being queued twice even when several application profiles overlap.
	groups := []string{}
	values := []string{}
	slots := 0
	for _, item := range ranges {
		count := 1
		if item.First != item.Last {
			count = 2
		}
		if slots+count > 15 {
			groups = append(groups, strings.Join(values, ","))
			values = nil
			slots = 0
		}
		values = append(values, portRangeText(item, ":"))
		slots += count
	}
	if len(values) > 0 {
		groups = append(groups, strings.Join(values, ","))
	}
	return groups
}

func applicationCatalog() []map[string]any {
	result := []map[string]any{}
	for _, kind := range applicationKinds() {
		result = append(result, map[string]any{"id": kind.ID, "name": kind.Name, "transport": kind.Transport, "methods": kind.Methods, "defaults": applicationDefaults(kind.ID, kind.Methods[0])})
	}
	return result
}

func mergeApplicationProfiles(previous, replacements []ApplicationProfile) []ApplicationProfile {
	byID := map[string]ApplicationProfile{}
	for _, profile := range previous {
		byID[profile.ID] = profile
	}
	for _, profile := range replacements {
		byID[profile.ID] = profile
	}
	result := []ApplicationProfile{}
	for _, kind := range applicationKinds() {
		if profile, ok := byID[kind.ID]; ok {
			result = append(result, profile)
		}
	}
	return result
}
