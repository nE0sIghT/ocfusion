// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type DestinationRule struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Enabled  bool           `json:"enabled"`
	Lists    []string       `json:"lists"`
	Domains  []string       `json:"domains"`
	Networks []string       `json:"networks"`
	Settings map[string]any `json:"settings"`
}

var ruleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
var tuningKeys = []string{"applications", "tcp_ports", "udp_ports", "tcp_strategy", "udp_strategy", "split_position", "split_positions", "sequence_overlap", "fake_ttl", "fake_repeats", "fake_fooling", "fake_sni", "auto_ttl", "auto_ttl_delta", "auto_ttl_min", "auto_ttl_max", "packet_limit"}

func strategyProjection(c Config) map[string]any {
	data, _ := json.Marshal(c)
	var fields map[string]any
	json.Unmarshal(data, &fields)
	result := map[string]any{}
	for _, key := range tuningKeys {
		result[key] = fields[key]
	}
	return result
}

func tunedConfig(base Config, settings map[string]any) (Config, error) {
	allowed := strategyProjection(base)
	for key, value := range settings {
		if _, ok := allowed[key]; !ok {
			return Config{}, errors.New("invalid rule strategy setting")
		}
		allowed[key] = value
	}
	data, err := json.Marshal(allowed)
	if err != nil {
		return Config{}, err
	}
	return parseConfig(data)
}

func ruleConfig(base Config, rule DestinationRule) Config {
	c, _ := tunedConfig(base, rule.Settings)
	c.Domains, c.Networks, c.Lists = rule.Domains, rule.Networks, rule.Lists
	c.ManagedSets = len(rule.Lists) > 0
	c.SelectorPrefix = "rule-" + rule.ID + "-"
	return c
}

func validateRules(c *Config) error {
	if len(c.Rules) > 6 {
		return errors.New("at most six destination rules")
	}
	seen := map[string]bool{}
	for i := range c.Rules {
		rule := &c.Rules[i]
		rule.Name = strings.TrimSpace(rule.Name)
		if !ruleIDPattern.MatchString(rule.ID) || seen[rule.ID] || rule.Name == "" || len(rule.Name) > 256 || utf8.RuneCountInString(rule.Name) > 64 || strings.IndexFunc(rule.Name, unicode.IsControl) >= 0 {
			return errors.New("invalid or duplicate destination rule")
		}
		seen[rule.ID] = true
		next, err := tunedConfig(*c, rule.Settings)
		if err != nil {
			return err
		}
		next.Domains, next.Networks, next.Lists = rule.Domains, rule.Networks, rule.Lists
		data, _ := json.Marshal(next)
		normalized, err := parseConfig(data)
		if err != nil {
			return err
		}
		rule.Domains, rule.Networks, rule.Lists = normalized.Domains, normalized.Networks, normalized.Lists
	}
	return nil
}

func selectedServiceIDs(c Config) []string {
	ids := append([]string{}, c.Lists...)
	for _, rule := range c.Rules {
		if rule.Enabled {
			ids = append(ids, rule.Lists...)
		}
	}
	return uniqueEntries(ids)
}

func processingConfigs(c Config) []Config {
	result := []Config{}
	for _, rule := range c.Rules {
		if rule.Enabled && len(rule.Domains)+len(rule.Networks) > 0 {
			result = append(result, ruleConfig(c, rule))
		}
	}
	c.Rules = nil
	c.SelectorPrefix = ""
	result = append(result, c)
	return result
}

func selectorMatches(c Config, target ScanTarget, address string) bool {
	ip, err := netip.ParseAddr(address)
	if err == nil {
		for _, value := range c.Networks {
			prefix, err := netip.ParsePrefix(value)
			if err == nil && prefix.Contains(ip) {
				return true
			}
		}
	}
	if target.Protocol == "stun" {
		return false
	}
	for _, domain := range c.Domains {
		domain = strings.TrimPrefix(domain, ".")
		if target.Host == domain || strings.HasSuffix(target.Host, "."+domain) {
			return true
		}
	}
	return false
}

func packetConfiguration(c Config) Config {
	c.Revision = 0
	c.Monitor = MonitorConfig{}
	c.Rules = append([]DestinationRule{}, c.Rules...)
	for i := range c.Rules {
		c.Rules[i].Name = ""
	}
	return c
}
