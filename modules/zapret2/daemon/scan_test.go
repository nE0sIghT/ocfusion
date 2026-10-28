// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresetCatalogIsBoundedAndReproducible(t *testing.T) {
	base := defaultConfig()
	base.Networks = []string{"203.0.113.0/24"}
	base.Domains = []string{"example.com"}
	base.Revision = 8
	base.Enabled = true
	seen := map[string]bool{}
	for _, preset := range presetCatalog() {
		if seen[preset.ID] || preset.ID == "" {
			t.Fatal("duplicate preset", preset.ID)
		}
		seen[preset.ID] = true
		c := presetConfig(base, preset)
		data, _ := json.Marshal(c)
		if _, err := parseConfig(data); err != nil {
			t.Fatalf("preset %s: %v", preset.ID, err)
		}
		if c.Revision != 8 || !c.Enabled || c.Networks[0] != base.Networks[0] || c.Domains[0] != base.Domains[0] {
			t.Fatal("preset changed user selection")
		}
		for _, key := range []string{"enabled", "revision", "domains", "networks", "tcp_ports", "udp_ports", "rules", "monitor"} {
			if _, ok := preset.Settings[key]; ok {
				t.Fatal("catalog can overwrite selectors", key)
			}
		}
	}
}

func TestScanOnlyUsesPublicIPv4(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.1.1", "224.0.0.1", "::1", "2001:db8::1"} {
		if scanAddressAllowed(netip.MustParseAddr(value)) {
			t.Fatal("non-public destination accepted", value)
		}
	}
	if !scanAddressAllowed(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IPv4 rejected")
	}
}

func TestScanRulesOnlyInterceptOwnedSocketTuple(t *testing.T) {
	r := Resources{Queue: 202, Mark: "0x20000000"}
	rules := scanRules(r, "ppp0", "203.0.113.12", 443, 45678, "0x40000000")
	if len(rules) != 3 || rules[0].Chain != "OUTPUT" || rules[1].Chain != "PREROUTING" || rules[2].Chain != "PREROUTING" {
		t.Fatal(rules)
	}
	for _, rule := range rules {
		text := strings.Join(rule.Args, " ")
		for _, required := range []string{"ppp0", "203.0.113.12", "443", "45678", "ocfusion:zapret2-scan"} {
			if !strings.Contains(text, required) {
				t.Fatal("unscoped test rule", text)
			}
		}
		if strings.Contains(text, "NFQUEUE") && !strings.Contains(text, "! --mark 0x20000000/0x20000000") {
			t.Fatal("scan worker loop mark missing", text)
		}
	}
	if !strings.Contains(strings.Join(rules[1].Args, " "), "--set-xmark 0x40000000/0x40000000") {
		t.Fatal("normal worker bypass missing")
	}
}

func TestScanRequestRevisionLockAndCancellation(t *testing.T) {
	previous := runDir
	runDir = t.TempDir()
	defer func() { runDir = previous }()
	configPath := filepath.Join(runDir, "config.json")
	t.Setenv("EXT_CONFIG_PATH", configPath)
	c := defaultConfig()
	c.Revision = 4
	c.Domains = []string{"example.com"}
	if err := writeJSON(configPath, c); err != nil {
		t.Fatal(err)
	}
	publish("DISABLED")
	for _, raw := range []string{
		`{"revision":3,"host":"example.com","port":443}`,
		`{"revision":4,"host":"https://example.com","port":443}`,
		`{"revision":4,"host":"example.com","port":0}`,
		`{"revision":4,"host":"example.com","port":443,"argv":"bad"}`,
	} {
		if _, err := requestScan([]byte(raw)); err == nil {
			t.Fatal("invalid scan accepted", raw)
		}
	}
	state, err := requestScan([]byte(`{"revision":4,"host":"Example.net.","port":8443}`))
	if err != nil || state.State != "QUEUED" || state.Host != "example.net" || state.Port != 8443 {
		t.Fatal(state, err)
	}
	if _, err = requestScan([]byte(`{"revision":4,"host":"example.com","port":443}`)); err == nil {
		t.Fatal("concurrent scan accepted")
	}
	if cancelRequestedScan([]byte(`{"id":"wrong"}`)) == nil {
		t.Fatal("stale cancellation accepted")
	}
	request, ok := pendingScan()
	if !ok || request.ID != state.ID {
		t.Fatal(request)
	}
	if _, ok := pendingScan(); ok {
		t.Fatal("request delivered twice")
	}
	raw, _ := json.Marshal(map[string]string{"id": state.ID})
	if err := cancelRequestedScan(raw); err != nil || !scanCancelled(c) {
		t.Fatal("cancellation not delivered", err)
	}
	resetScan()
	if scanStatus().State != "CANCELLED" {
		t.Fatal("interrupted scan not recovered")
	}
	data, _ := os.ReadFile(configPath)
	saved, err := parseConfig(data)
	if err != nil || saved.Revision != 4 || saved.Enabled {
		t.Fatal("scan modified saved settings")
	}
}

func TestScanRouteRequiresBoundWAN(t *testing.T) {
	old := execute
	defer func() { execute = old }()
	execute = func(tool string, args ...string) (string, error) {
		if strings.Join(args, " ") != "-4 route get 203.0.113.1 oif ppp0" {
			t.Fatal(args)
		}
		return "203.0.113.1 dev ocvpn0 src 192.0.2.1", nil
	}
	if _, err := routeForScan("203.0.113.1", "ppp0"); err == nil {
		t.Fatal("VPN route accepted")
	}
	execute = func(string, ...string) (string, error) {
		return "203.0.113.1 via 192.0.2.254 dev ppp0 src 192.0.2.1", nil
	}
	if source, err := routeForScan("203.0.113.1", "ppp0"); err != nil || source != "192.0.2.1" {
		t.Fatal(source, err)
	}
}

func TestScanRuleInstallationAndRecoveryOrder(t *testing.T) {
	previous := runDir
	oldExecute := execute
	runDir = t.TempDir()
	defer func() {
		runDir = previous
		execute = oldExecute
	}()
	resources := Resources{Queue: 203, Mark: "0x20000000"}
	resources.Rules = scanRules(resources, "ppp0", "203.0.113.20", 443, 41234, "0x40000000")
	calls := []string{}
	absent := exec.Command("/bin/sh", "-c", "exit 1").Run()
	execute = func(tool string, args ...string) (string, error) {
		text := strings.Join(args, " ")
		if strings.Contains(text, " -C ") {
			// Force installation, then pretend the rule is present during removal.
			if _, err := os.Stat(scanPath("installed")); err != nil {
				return "", absent
			}
		}
		calls = append(calls, text)
		return "", nil
	}
	if err := installScanRules(resources); err != nil {
		t.Fatal(err)
	}
	insertions := []string{}
	for _, call := range calls {
		if strings.Contains(call, " -I ") {
			insertions = append(insertions, call)
		}
	}
	if len(insertions) != 3 || !strings.Contains(insertions[0], "PREROUTING") || !strings.Contains(insertions[0], "NFQUEUE") || !strings.Contains(insertions[1], "--set-xmark") || !strings.Contains(insertions[2], "OUTPUT") {
		t.Fatal("wrong head insertion order", insertions)
	}
	if err := os.WriteFile(scanPath("installed"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupAt(scanResources(), "ocfusion:zapret2"); err == nil {
		t.Fatal("regular cleanup accepted scanner ownership")
	}
	if err := cleanupAt(scanResources(), "ocfusion:zapret2-scan"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scanResources()); !os.IsNotExist(err) {
		t.Fatal("recovery journal retained after successful cleanup")
	}
}
