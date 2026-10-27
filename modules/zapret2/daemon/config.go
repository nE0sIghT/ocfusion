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
	Revision uint64   `json:"revision"`
	Enabled  bool     `json:"enabled"`
	Networks []string `json:"networks"`
	Domains  []string `json:"domains"`
	TCPPorts []int    `json:"tcp_ports"`
	UDPPorts []int    `json:"udp_ports"`
	TCP      string   `json:"tcp_strategy"`
	UDP      string   `json:"udp_strategy"`
	Split    int      `json:"split_position"`
	TTL      int      `json:"fake_ttl"`
	Repeats  int      `json:"fake_repeats"`
}

func defaultConfig() Config {
	return Config{TCPPorts: []int{80, 443}, UDPPorts: []int{443}, TCP: "split", UDP: "off", Split: 2, TTL: 3, Repeats: 3}
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

	if (c.TCP != "off" && c.TCP != "split" && c.TCP != "disorder") || (c.UDP != "off" && c.UDP != "fake-quic") {
		return c, errors.New("invalid strategy")
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
	return len(c.Networks)+len(c.Domains) > 0
}
