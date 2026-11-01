// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var packetOptionKeys = []string{"fake_ttl", "fake_repeats", "fake_fooling", "auto_ttl", "auto_ttl_delta", "auto_ttl_min", "auto_ttl_max", "packet_limit"}

type StrategySelection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Checked   bool   `json:"checked"`
	Signature string `json:"signature"`
}

func knownProtocolScope(scope string) bool {
	if scope == "tcp" || scope == "udp" {
		return true
	}
	_, ok := applicationKind(scope)
	return ok
}

// Unspecified options retain the established flat configuration values.
func protocolConfig(c Config, scope string) Config {
	options := c.ProtocolOptions[scope]
	if len(options) > 0 {
		data, _ := json.Marshal(options)
		json.Unmarshal(data, &c)
	}
	return c
}

func validateProtocolOptions(c Config) error {
	if len(c.ProtocolOptions) > 2+len(applicationKinds()) {
		return errors.New("invalid protocol settings")
	}
	allowed := map[string]bool{}
	for _, key := range packetOptionKeys {
		allowed[key] = true
	}
	for scope, options := range c.ProtocolOptions {
		if !knownProtocolScope(scope) || len(options) > len(allowed) {
			return errors.New("invalid protocol settings")
		}
		for key, value := range options {
			if !allowed[key] || value == nil {
				return errors.New("invalid protocol setting")
			}
		}
		data, _ := json.Marshal(options)
		next := c
		next.ProtocolOptions = nil
		if json.Unmarshal(data, &next) != nil || next.TTL < 1 || next.TTL > 255 || next.Repeats < 1 || next.Repeats > 20 {
			return errors.New("protocol settings exceed limits")
		}
		if err := validateStrategy(next); err != nil {
			return err
		}
	}
	return nil
}

func protocolSnapshot(c Config, scope string) map[string]any {
	c = protocolConfig(c, scope)
	all := strategyProjection(c)
	result := map[string]any{}
	for _, key := range packetOptionKeys {
		result[key] = all[key]
	}
	switch scope {
	case "tcp":
		for _, key := range []string{"tcp_strategy", "tcp_ports", "split_position", "split_positions", "sequence_overlap", "fake_sni"} {
			result[key] = all[key]
		}
	case "udp":
		result["udp_strategy"], result["udp_ports"] = all["udp_strategy"], all["udp_ports"]
	default:
		profiles := []ApplicationProfile{}
		for _, profile := range c.Applications {
			if profile.ID == scope {
				profiles = append(profiles, profile)
			}
		}
		result["applications"] = profiles
	}
	data, _ := json.Marshal(result)
	json.Unmarshal(data, &result)
	return result
}

// This checksum tracks display identity, not authentication or authorization.
func protocolSignature(c Config, scope string) string {
	data, _ := json.Marshal(protocolSnapshot(c, scope))
	digest := fnv.New64a()
	digest.Write(data)
	return fmt.Sprintf("%016x", digest.Sum64())
}

func validateStrategySelections(rule *DestinationRule, c Config) error {
	if len(rule.Selections) > 2+len(applicationKinds()) {
		return errors.New("invalid strategy selection")
	}
	for scope, selection := range rule.Selections {
		selection.Name = strings.TrimSpace(selection.Name)
		if !knownProtocolScope(scope) || selection.ID == "" || selection.Name == "" || len(selection.ID) > 96 || len(selection.Name) > 384 || utf8.RuneCountInString(selection.Name) > 96 || strings.IndexFunc(selection.Name, unicode.IsControl) >= 0 {
			return errors.New("invalid strategy selection")
		}
		if selection.Signature != protocolSignature(c, scope) {
			delete(rule.Selections, scope)
			continue
		}
		rule.Selections[scope] = selection
	}
	return nil
}
