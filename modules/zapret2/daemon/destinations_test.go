// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestVPNRulesValidateAndDoNotLaunchPacketProcessing(t *testing.T) {
	c, err := parseConfig([]byte(`{"enabled":true,"rules":[{"id":"video","name":"Video","enabled":true,"action":"vpn","target":"native:2:012345abcdef","domains":["example.com"],"settings":{}}]}`))
	if err != nil || !hasSelection(c) || hasDPISelection(c) || c.Rules[0].Failure != "block" {
		t.Fatal(c, err)
	}
	selected := ruleConfig(c, c.Rules[0])
	if selected.TCP != "off" || selected.UDP != "off" || len(selected.Applications) > 0 {
		t.Fatal("VPN traffic entered desync profiles", selected)
	}
	policies := routePolicies(c)
	if len(policies) != 1 || policies[0].Action != "vpn" || policies[0].Domains[0] != "example.com" {
		t.Fatal(policies)
	}
	c.Enabled = false
	if len(routePolicies(c)) != 0 {
		t.Fatal("disabled module retained routing policy")
	}
	for _, invalid := range []string{
		`{"rules":[{"id":"one","name":"One","action":"vpn"}]}`,
		`{"rules":[{"id":"one","name":"One","action":"normal","target":"managed:other"}]}`,
		`{"rules":[{"id":"default","name":"One"}]}`,
	} {
		if _, err := parseConfig([]byte(invalid)); err == nil {
			t.Fatal("invalid policy accepted", invalid)
		}
	}
}

func TestMixedPoliciesPreserveOrderAndRouteBypass(t *testing.T) {
	old := execute
	t.Cleanup(func() {
		execute = old
	})
	execute = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "route show") {
			return "default dev ppp0", nil
		}
		return "", nil
	}
	c := defaultConfig()
	c.Enabled = true
	c.RouteMask = 0x10000
	c.Networks = []string{"203.0.113.0/24"}
	c.Rules = []DestinationRule{
		{ID: "direct", Name: "Direct", Enabled: true, Action: "normal", Networks: []string{"203.0.113.1/32"}},
		{ID: "vpn", Name: "VPN", Enabled: true, Action: "vpn", Target: "managed:one", Failure: "wan", Networks: []string{"203.0.113.0/25"}},
	}
	policies := routePolicies(c)
	if len(policies) != 3 || policies[0].Action != "normal" || policies[1].Action != "vpn" || policies[2].ID != "default" {
		t.Fatal(policies)
	}
	resources, err := allocate(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range resources.Rules {
		text := strings.Join(rule.Args, " ")
		if !strings.Contains(text, "--mark 0x0/") || !strings.Contains(text, "-m connmark --mark 0x0/0x10000") || !strings.Contains(text, "--queue-bypass") {
			t.Fatal("VPN-marked fallback traffic not excluded from DPI", text)
		}
	}
	args := strings.Join(workerArgs(c, resources, "/worker", 62000), " ")
	if !strings.Contains(args, "--filter-tcp=* --ipset=/worker/rule-vpn-networks.txt") {
		t.Fatal("VPN rule lost its pass boundary", args)
	}
}

func TestHealthChecksSelectedVPNWithoutPacketWorker(t *testing.T) {
	oldDir, oldResolve, oldProbe, oldVPN := runDir, discoveryResolve, discoveryProbe, vpnRequest
	runDir = t.TempDir()
	t.Cleanup(func() {
		runDir, discoveryResolve, discoveryProbe, vpnRequest = oldDir, oldResolve, oldProbe, oldVPN
	})
	c := defaultConfig()
	c.Enabled = true
	c.Rules = []DestinationRule{{ID: "video", Name: "Video", Enabled: true, Action: "vpn", Target: "managed:one", Failure: "block", Domains: []string{"example.com"}}}
	c.Monitor.Targets = []ScanTarget{{Host: "example.com", Protocol: "https", Port: 443, Path: "/"}}
	discoveryResolve = func(ctx context.Context, targets []ScanTarget) []resolvedScanTarget {
		return []resolvedScanTarget{{Address: "203.0.113.10", Interface: "ppp0", Source: "192.0.2.1"}}
	}
	vpnRequest = func(operation string, policies []RoutePolicy) (RouteReply, error) {
		if operation != "destination-probe" || policies[0].ID != "203.0.113.10" {
			t.Fatal(operation, policies)
		}
		return RouteReply{OK: true, Status: []RouteStatus{{ID: "video", State: "VPN", Interface: "ocvpn0", Source: "192.0.2.2", Mark: "0x10000"}}}, nil
	}
	calls := 0
	discoveryProbe = func(ctx context.Context, target ScanTarget, base Config, preset Preset, resolved resolvedScanTarget, bypass string) (TargetProbe, error) {
		calls++
		if !resolved.Direct || resolved.Interface != "ocvpn0" || resolved.Source != "192.0.2.2" || bypass != "0x40010000" || preset.ID != "baseline" {
			t.Fatal(resolved, bypass, preset)
		}
		return TargetProbe{Stage: "complete"}, nil
	}
	if err := performHealth(context.Background(), c, "0x40000000"); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	if state := healthStatus(); state.State != "HEALTHY" {
		data, _ := json.Marshal(state)
		t.Fatal(string(data))
	}
	vpnRequest = func(operation string, policies []RoutePolicy) (RouteReply, error) {
		return RouteReply{OK: true, Status: []RouteStatus{{ID: "video", State: "BLOCKED"}}}, nil
	}
	if err := performHealth(context.Background(), c, "0x40000000"); err != nil || calls != 1 {
		t.Fatal("blocked VPN was probed over WAN", err, calls)
	}
	if state := healthStatus(); state.State != "DEGRADED" || state.Results[0].Observation.Stage != "vpn" {
		t.Fatal(state)
	}
}

func TestSTUNBaselineNeedsNoApplicationStrategy(t *testing.T) {
	oldDir, oldExecute := runDir, execute
	runDir = t.TempDir()
	t.Cleanup(func() {
		runDir, execute = oldDir, oldExecute
	})
	execute = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "route show") {
			return "default dev ppp0", nil
		}
		if strings.Contains(strings.Join(args, " "), "NFQUEUE") {
			t.Fatal("baseline installed packet interception", args)
		}
		return "", nil
	}
	worker, done, err := startProbeWorker(context.Background(), ScanTarget{Host: "example.com", Port: 3478, Protocol: "stun"}, defaultConfig(), Preset{ID: "baseline", Protocol: "stun"}, "203.0.113.10", "ppp0", 42000, "")
	if err != nil || worker != nil || done != nil {
		t.Fatal(worker, done, err)
	}
}
