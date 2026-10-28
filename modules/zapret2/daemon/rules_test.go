// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDestinationRuleValidationAndIsolation(t *testing.T) {
	for _, raw := range []string{
		`{"rules":[{"id":"../file","name":"Test","settings":{}}]}`,
		`{"rules":[{"id":"one","name":"Test","settings":{"enabled":true}}]}`,
		`{"rules":[{"id":"one","name":"Test","settings":{"networks":["1.1.1.1"]}}]}`,
		`{"rules":[{"id":"one","name":"Test","settings":{"lua_init":"arbitrary"}}]}`,
		`{"rules":[{"id":"one","name":"Test","settings":{"tcp_strategy":"arbitrary"}}]}`,
		`{"rules":[{"id":"one","name":"Test","domains":["https://example.com"],"settings":{}}]}`,
	} {
		if _, err := parseConfig([]byte(raw)); err == nil {
			t.Fatal("unsafe rule accepted", raw)
		}
	}
	c, err := parseConfig([]byte(`{"enabled":true,"tcp_strategy":"off","rules":[{"id":"video","name":"Video","enabled":true,"networks":["203.0.113.19"],"domains":["Example.com"],"settings":{"tcp_strategy":"split","tcp_ports":[8443]}}]}`))
	if err != nil || !hasSelection(c) {
		t.Fatal(c, err)
	}
	if c.Rules[0].Networks[0] != "203.0.113.19/32" || c.Rules[0].Domains[0] != "example.com" {
		t.Fatal(c.Rules)
	}
	selected := ruleConfig(c, c.Rules[0])
	if selected.TCP != "split" || c.TCP != "off" || len(c.Networks) != 0 {
		t.Fatal("rule leaked into default settings")
	}
	if got := strings.Join(firewallPortGroups(c, "tcp"), ","); got != "8443" {
		t.Fatal(got)
	}
	c.Rules = append(c.Rules, c.Rules[0])
	data, _ := json.Marshal(c)
	if _, err := parseConfig(data); err == nil {
		t.Fatal("duplicate rule accepted")
	}
}

func TestRuleEngineOrderingAndPassBoundaries(t *testing.T) {
	c := defaultConfig()
	c.Networks = []string{"203.0.113.0/24"}
	c.Rules = []DestinationRule{
		{ID: "first", Name: "First", Enabled: true, Networks: []string{"203.0.113.1/32"}, Settings: map[string]any{"tcp_strategy": "split"}},
		{ID: "second", Name: "Second", Enabled: true, Domains: []string{"example.com"}, Settings: map[string]any{"tcp_strategy": "disorder"}},
	}
	text := strings.Join(workerArgs(c, Resources{Queue: 202, Mark: "0x40000000"}, "/worker", 62000), " ")
	first := strings.Index(text, "/worker/rule-first-networks.txt")
	pass := strings.Index(text, "--filter-tcp=* --ipset=/worker/rule-first-networks.txt")
	second := strings.Index(text, "/worker/rule-second-domains.txt")
	fallback := strings.LastIndex(text, "/worker/networks.txt")
	if first < 0 || pass < first || second < pass || fallback < second {
		t.Fatal(text)
	}
	for _, profile := range strings.Split(text, " --new ")[1:] {
		if !strings.Contains(profile, "--ipset=") && !strings.Contains(profile, "--hostlist=") {
			t.Fatal("unscoped rule profile", profile)
		}
	}
}

func TestConfiguredHealthStrategyHonorsFirstMatchAndOff(t *testing.T) {
	c := defaultConfig()
	c.Enabled = true
	c.Networks = []string{"203.0.113.0/24"}
	c.Rules = []DestinationRule{{ID: "one", Name: "Explicit", Enabled: true, Domains: []string{"example.com"}, Settings: map[string]any{"tcp_strategy": "off"}}}
	target := ScanTarget{Host: "www.example.com", Port: 443, Protocol: "https"}
	preset, name := configuredProbePreset(c, target, "203.0.113.1")
	if preset.ID != "baseline" || name != "Explicit" {
		t.Fatal(preset, name)
	}
	c.Rules[0].Settings["tcp_strategy"] = "disorder"
	preset, name = configuredProbePreset(c, target, "203.0.113.1")
	if preset.Settings["tcp_strategy"] != "disorder" || name != "Explicit" {
		t.Fatal(preset, name)
	}
	target.Host = "other.example.org"
	preset, name = configuredProbePreset(c, target, "203.0.113.1")
	if preset.ID == "baseline" || name != "Default strategy" {
		t.Fatal(preset, name)
	}
	target.Port = 8443
	preset, _ = configuredProbePreset(c, target, "203.0.113.1")
	if preset.ID != "baseline" {
		t.Fatal("processed an unconfigured port")
	}
}

func TestRuleServiceSelectionsStayIndependent(t *testing.T) {
	store := &ServiceStore{entries: map[string]*serviceEntry{"telegram": {snapshot: ServiceSnapshot{Domains: []string{"telegram.org"}, Networks: []string{"203.0.113.0/24"}}}}}
	c := defaultConfig()
	c.Domains = []string{"example.com"}
	c.Rules = []DestinationRule{{ID: "telegram", Name: "Telegram", Enabled: true, Lists: []string{"telegram"}}}
	effective := store.effective(c)
	if len(effective.Domains) != 1 || len(effective.Rules[0].Domains) != 1 || len(c.Rules[0].Domains) != 0 {
		t.Fatal("list scope leaked", effective, c)
	}
	if len(selectedServiceIDs(c)) != 1 {
		t.Fatal(selectedServiceIDs(c))
	}
	c.Rules[0].Enabled = false
	if len(selectedServiceIDs(c)) != 0 || hasSelection(Config{Rules: c.Rules}) {
		t.Fatal("disabled rule selected destinations")
	}
}

func TestPinnedEngineAcceptsOrderedRulesAndPassProfiles(t *testing.T) {
	binary := os.Getenv("OCF_NFQWS2_TEST_BINARY")
	directory := os.Getenv("OCF_NFQWS2_TEST_LUA")
	if binary == "" || directory == "" {
		t.Skip("requires pinned native engine")
	}
	worker := t.TempDir()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worker, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := defaultConfig()
	c.Networks = []string{"203.0.113.0/24"}
	c.Rules = []DestinationRule{
		{ID: "one", Name: "One", Enabled: true, Domains: []string{"example.com"}, Networks: []string{"203.0.113.1/32"}, Settings: map[string]any{"tcp_strategy": "off", "applications": []ApplicationProfile{applicationDefaults("stun", "fake")}}},
		{ID: "two", Name: "Two", Enabled: true, Domains: []string{"example.org"}, Settings: map[string]any{"tcp_strategy": "fake-split"}},
	}
	for _, selected := range processingConfigs(c) {
		for suffix, values := range map[string][]string{"domains.txt": selected.Domains, "networks.txt": selected.Networks} {
			if err := os.WriteFile(filepath.Join(worker, selected.SelectorPrefix+suffix), []byte(strings.Join(values, "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	args := workerArgs(c, Resources{Queue: 202, Mark: "0x40000000"}, worker, 62000)
	args = append(args[:2], args[3:]...)
	args = append(args, "--dry-run")
	if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestMonitoringAndLabelsDoNotChangePacketIdentity(t *testing.T) {
	base := defaultConfig()
	base.Rules = []DestinationRule{{ID: "one", Name: "Original", Enabled: true, Domains: []string{"example.com"}, Settings: map[string]any{"tcp_strategy": "split"}}}
	next := base
	next.Rules = append([]DestinationRule{}, base.Rules...)
	next.Rules[0].Name = "Renamed"
	next.Revision = 9
	next.Monitor = MonitorConfig{Enabled: true, Interval: 30, Targets: []ScanTarget{{Host: "example.com", Port: 443, Protocol: "https"}}}
	if !reflect.DeepEqual(packetConfiguration(base), packetConfiguration(next)) {
		t.Fatal("metadata restarted packet policy")
	}
	if base.Rules[0].Name != "Original" {
		t.Fatal("identity projection mutated configuration")
	}
	next.TCP = "off"
	if reflect.DeepEqual(packetConfiguration(base), packetConfiguration(next)) {
		t.Fatal("strategy change was ignored")
	}
}
