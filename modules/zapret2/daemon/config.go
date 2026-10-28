// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var version = "dev"
var revision = "unknown"
var runDir = "/tmp/ocfusion-zapret2"
var domainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var interfacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,14}$`)

type Config struct {
	RouteMask      uint32               `json:"-"`
	Monitor        MonitorConfig        `json:"monitor"`
	Rules          []DestinationRule    `json:"rules,omitempty"`
	SelectorPrefix string               `json:"-"`
	Applications   []ApplicationProfile `json:"applications"`
	Lists          []string             `json:"lists"`
	ManagedSets    bool                 `json:"-"`
	Revision       uint64               `json:"revision"`
	Enabled        bool                 `json:"enabled"`
	Networks       []string             `json:"networks"`
	Domains        []string             `json:"domains"`
	TCPPorts       []int                `json:"tcp_ports"`
	UDPPorts       []int                `json:"udp_ports"`
	TCP            string               `json:"tcp_strategy"`
	UDP            string               `json:"udp_strategy"`
	Split          int                  `json:"split_position"`
	Positions      string               `json:"split_positions"`
	Overlap        int                  `json:"sequence_overlap"`
	TTL            int                  `json:"fake_ttl"`
	Repeats        int                  `json:"fake_repeats"`
	Fooling        string               `json:"fake_fooling"`
	FakeSNI        string               `json:"fake_sni"`
	AutoTTL        bool                 `json:"auto_ttl"`
	AutoDelta      int                  `json:"auto_ttl_delta"`
	AutoMin        int                  `json:"auto_ttl_min"`
	AutoMax        int                  `json:"auto_ttl_max"`
	PacketLimit    int                  `json:"packet_limit"`
}

func defaultConfig() Config {
	return Config{
		Monitor:     MonitorConfig{Interval: 15},
		TCPPorts:    []int{80, 443},
		UDPPorts:    []int{443},
		TCP:         "split",
		UDP:         "off",
		Split:       2,
		TTL:         3,
		Repeats:     3,
		Fooling:     "ttl",
		AutoDelta:   -1,
		AutoMin:     3,
		AutoMax:     20,
		PacketLimit: 12,
	}
}

func parseConfig(data []byte) (Config, error) {
	c := defaultConfig()
	if len(data) == 0 || string(data) == "null" {
		return c, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 16384 || decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF {
		return c, errors.New("invalid settings")
	}

	if len(c.Networks) > 64 || len(c.Domains) > 128 || c.Split < 1 || c.Split > 1500 || c.TTL < 1 || c.TTL > 255 || c.Repeats < 1 || c.Repeats > 20 {
		return c, errors.New("settings exceed limits")
	}

	seenLists := map[string]bool{}
	if len(c.Lists) > len(serviceCatalog()) {
		return c, errors.New("too many service lists")
	}
	for _, id := range c.Lists {
		if _, known := knownService(id); !known || seenLists[id] {
			return c, errors.New("unknown or duplicate service list")
		}
		seenLists[id] = true
	}

	for _, ports := range [][]int{c.TCPPorts, c.UDPPorts} {
		if len(ports) > 15 {
			return c, errors.New("at most 15 ports per protocol")
		}

		seen := map[int]bool{}
		for _, port := range ports {
			if port < 1 || port > 65535 || seen[port] {
				return c, errors.New("invalid or duplicate port")
			}

			seen[port] = true
		}
	}

	c.FakeSNI = strings.ToLower(strings.TrimSuffix(c.FakeSNI, "."))
	if c.Positions != "" {
		markers := strings.Split(c.Positions, ",")
		for i := range markers {
			markers[i] = strings.TrimSpace(markers[i])
		}
		c.Positions = strings.Join(markers, ",")
	}

	if err := validateApplications(&c); err != nil {
		return c, err
	}

	if err := validateStrategy(c); err != nil {
		return c, err
	}

	for i, value := range c.Networks {
		if address, err := netip.ParseAddr(value); err == nil {
			value = netip.PrefixFrom(address, address.BitLen()).String()
		}

		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().Is4In6() || prefix.Bits() == 0 || prefix.Addr().IsMulticast() || prefix.Addr().IsLoopback() || prefix.Addr().IsUnspecified() {
			return c, errors.New("invalid destination IP or subnet")
		}

		c.Networks[i] = prefix.Masked().String()
	}

	// Overlapping prefixes must not queue the same packet more than once.
	normalized := []string{}
	for _, value := range c.Networks {
		prefix, _ := netip.ParsePrefix(value)
		covered := false
		for _, previous := range normalized {
			other, _ := netip.ParsePrefix(previous)
			if other.Bits() <= prefix.Bits() && other.Contains(prefix.Addr()) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}

		kept := []string{}
		for _, previous := range normalized {
			other, _ := netip.ParsePrefix(previous)
			if prefix.Bits() > other.Bits() || !prefix.Contains(other.Addr()) {
				kept = append(kept, previous)
			}
		}
		normalized = append(kept, value)
	}
	c.Networks = normalized

	for i, value := range c.Domains {
		value = strings.ToLower(strings.TrimSuffix(value, "."))
		if len(value) > 253 || !domainPattern.MatchString(value) || net.ParseIP(value) != nil {
			return c, errors.New("enter domain names without URLs or wildcards")
		}

		c.Domains[i] = value
	}

	if err := validateMonitor(&c); err != nil {
		return c, err
	}
	if err := validateRules(&c); err != nil {
		return c, err
	}
	return c, nil
}

func loadConfig() (Config, error) {
	path := os.Getenv("EXT_CONFIG_PATH")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) || path == "" {
		return defaultConfig(), nil
	}

	if err != nil {
		return Config{}, err
	}

	return parseConfig(data)
}

func portList(ports []int) string {
	values := make([]string, len(ports))
	for i, port := range ports {
		values[i] = strconv.Itoa(port)
	}

	return strings.Join(values, ",")
}

func hasSelection(c Config) bool {
	if len(c.Networks)+len(c.Domains) > 0 {
		return true
	}
	for _, rule := range c.Rules {
		if rule.Enabled && len(rule.Domains)+len(rule.Networks) > 0 {
			return true
		}
	}
	return false
}
