// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func protocolProfile(args []string, marker string) string {
	for _, profile := range strings.Split(strings.Join(args, " "), " --new ") {
		if strings.Contains(profile, marker) {
			return profile
		}
	}
	return ""
}

func TestProtocolPacketOptionsDoNotLeakAcrossProfiles(t *testing.T) {
	c := defaultConfig()
	c.Enabled = true
	c.Networks = []string{"203.0.113.0/24"}
	c.TCP, c.UDP = "fake-disorder", "fake-quic"
	c.Applications = []ApplicationProfile{applicationDefaults("dtls", "fake"), applicationDefaults("wireguard", "fake")}
	before := workerProfiles(c, "/fixture")
	c.ProtocolOptions = map[string]map[string]any{"tcp": {"fake_ttl": 9, "fake_repeats": 7, "packet_limit": 24}, "dtls": {"fake_ttl": 15}}
	after := workerProfiles(c, "/fixture")
	tcp := protocolProfile(after, "--filter-l7=http,tls")
	if !strings.Contains(tcp, "ip_ttl=9") || !strings.Contains(tcp, "repeats=7") || !strings.Contains(tcp, "--out-range=-n24") {
		t.Fatal(tcp)
	}
	for _, marker := range []string{"--filter-l7=quic", "--filter-l7=wireguard"} {
		if protocolProfile(before, marker) != protocolProfile(after, marker) {
			t.Fatal("TCP selection changed an unrelated profile", marker)
		}
	}
	dtls := protocolProfile(after, "--filter-l7=dtls")
	if !strings.Contains(dtls, "ip_ttl=15") {
		t.Fatal(dtls)
	}
	c.ProtocolOptions["udp"] = map[string]any{"fake_ttl": 6, "fake_repeats": 11}
	changed := workerProfiles(c, "/fixture")
	if protocolProfile(changed, "--filter-l7=http,tls") != tcp || protocolProfile(changed, "--filter-l7=dtls") != dtls {
		t.Fatal("QUIC options changed TCP or application profiles")
	}
	quic := protocolProfile(changed, "--filter-l7=quic")
	if !strings.Contains(quic, "ip_ttl=6") || !strings.Contains(quic, "repeats=11") {
		t.Fatal(quic)
	}
}

func TestProtocolOptionsAreBoundedAndCannotInjectWorkerArguments(t *testing.T) {
	for _, options := range []map[string]map[string]any{
		{"tcp": {"fake_ttl": 0}},
		{"udp": {"fake_repeats": 21}},
		{"udp": {"auto_ttl_min": 100, "auto_ttl_max": 20}},
		{"tcp": {"lua_init": "bad"}},
		{"tcp": {"enabled": true}},
		{"unknown": {"fake_ttl": 3}},
		{"tcp": {"fake_ttl": "3"}},
	} {
		c := defaultConfig()
		c.ProtocolOptions = options
		data, _ := json.Marshal(c)
		if _, err := parseConfig(data); err == nil {
			t.Fatal("unbounded protocol options accepted", options)
		}
	}
}

func TestNamedStrategySurvivesReloadAndOnlyItsOwnEditsInvalidateIt(t *testing.T) {
	c := defaultConfig()
	c.UDP = "fake-quic"
	rule := DestinationRule{ID: "example", Name: "Example", Enabled: true, Domains: []string{"example.com"}}
	rule.Selections = map[string]StrategySelection{
		"tcp": {ID: "disorder", Name: "Reordered segments", Checked: true, Signature: protocolSignature(c, "tcp")},
		"udp": {ID: "quic", Name: "QUIC fake packets", Checked: true, Signature: protocolSignature(c, "udp")},
	}
	c.Rules = []DestinationRule{rule}
	data, _ := json.Marshal(c)
	loaded, err := parseConfig(data)
	if err != nil || len(loaded.Rules[0].Selections) != 2 {
		t.Fatal(err, loaded.Rules)
	}
	original := loaded.Rules[0].Selections["udp"]
	loaded.Rules[0].Settings = map[string]any{"protocol_options": map[string]any{"tcp": map[string]any{"fake_ttl": 9}}}
	data, _ = json.Marshal(loaded)
	changed, err := parseConfig(data)
	if err != nil || len(changed.Rules[0].Selections) != 1 || !reflect.DeepEqual(changed.Rules[0].Selections["udp"], original) {
		t.Fatal(err, changed.Rules)
	}
	if protocolSignature(c, "tcp") == protocolSignature(ruleConfig(changed, changed.Rules[0]), "tcp") {
		t.Fatal("signature ignored actual rule packet options")
	}

	before := packetConfiguration(c)
	c.Rules[0].Selections = nil
	if !reflect.DeepEqual(before, packetConfiguration(c)) {
		t.Fatal("display metadata restarted packet processing")
	}
}
