// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

func TestQUICDiscoveryCoversPinnedBlockcheckFamilies(t *testing.T) {
	candidates := scanCandidates("udp")
	if len(candidates) > scanResultLimit {
		t.Fatal("candidates exceed discovery budget", len(candidates))
	}
	seen := map[string]bool{}
	for _, preset := range candidates[1:] {
		c := probeWorkerConfig(defaultConfig(), preset, ScanTarget{Protocol: "http3", Port: 443}, "2001:4860:4860::8888")
		data, _ := json.Marshal(c)
		if _, err := parseConfig(data); err != nil {
			t.Fatalf("%s: %v", preset.ID, err)
		}
		args := strings.Join(quicStrategy(c), " ")
		if seen[args] {
			t.Fatal("duplicate effective QUIC candidate", preset.ID)
		}
		seen[args] = true
		if c.UDP != preset.Settings["udp_strategy"] || c.Networks[0] != "2001:4860:4860::8888/128" {
			t.Fatal("probe replaced the selected method", c)
		}
		if c.UDP == "fragment-quic" && strings.Contains(args, "--lua-desync=fake") {
			t.Fatal("fragment-only strategy emitted fake packets")
		}
		if c.UDPProtection == "none" && strings.Contains(args, "ip_ttl=") {
			t.Fatal("unrequested TTL protection", args)
		}
	}
	for _, method := range []string{"fake-quic", "fragment-quic", "fake-fragment-quic", "hopbyhop-quic", "destopt-quic", "hopbyhop-destopt-quic"} {
		found := false
		for _, preset := range candidates[1:] {
			if preset.Settings["udp_strategy"] == method {
				found = true
			}
		}
		if !found {
			t.Fatal("missing blockcheck family", method)
		}
	}
}

func TestQUICConfigurationRejectsUnboundedOptions(t *testing.T) {
	for _, raw := range []string{
		`{"udp_strategy":"fragment-quic","udp_fragment":7}`,
		`{"udp_strategy":"fragment-quic","udp_fragment":9}`,
		`{"udp_strategy":"fragment-quic","udp_fragment":1208}`,
		`{"udp_strategy":"send:anything"}`,
		`{"udp_protection":"lua"}`,
	} {
		if _, err := parseConfig([]byte(raw)); err == nil {
			t.Fatal("invalid QUIC settings accepted", raw)
		}
	}
}

func TestIPv6QUICRoutesAndRulesRemainSocketScoped(t *testing.T) {
	previous := execute
	t.Cleanup(func() { execute = previous })
	execute = func(tool string, args ...string) (string, error) {
		if tool != "ip" || strings.Join(args, " ") != "-6 route get 2001:4860:4860::8888 oif eth0" {
			t.Fatal(tool, args)
		}
		return "2001:4860:4860::8888 dev eth0 src 2001:4860::2", nil
	}
	if source, err := routeForScan("2001:4860:4860::8888", "eth0"); err != nil || source != "2001:4860::2" {
		t.Fatal(source, err)
	}
	for _, rule := range scanRulesForProtocol(Resources{Queue: 200, Mark: "0x40000000"}, "eth0", "2001:4860:4860::8888", 443, 45321, "0x20000000", "udp") {
		args := strings.Join(rule.Args, " ")
		if rule.Tool != "ip6tables" || !strings.Contains(args, "45321") || !strings.Contains(args, "2001:4860:4860::8888") || !strings.Contains(args, "-p udp") {
			t.Fatal(rule)
		}
	}
	for _, address := range []string{"::1", "fd00::1", "fe80::1", "2001:db8::1"} {
		if scanAddressAllowed(netip.MustParseAddr(address)) {
			t.Fatal("non-public IPv6 target", address)
		}
	}
	if !scanAddressAllowed(netip.MustParseAddr("2001:4860:4860::8888")) {
		t.Fatal("public IPv6 rejected")
	}
	request := ScanRequest{Targets: []ScanTarget{{Host: "example.com", Port: 443, Protocol: "http3", Family: 6}}}
	if err := normalizeScanTargets(&request); err != nil {
		t.Fatal(err)
	}
	request.Targets[0].Protocol = "https"
	if normalizeScanTargets(&request) == nil {
		t.Fatal("IPv6 TCP probe advertised without socket support")
	}
}

func TestQUICSearchSkipsIPv6MethodsOnlyForIPv4Targets(t *testing.T) {
	discoveryFixture(t)
	for _, family := range []int{4, 6} {
		calls := 0
		discoveryProbe = func(_ context.Context, target ScanTarget, c Config, preset Preset, _ resolvedScanTarget, _ string) (TargetProbe, error) {
			calls++
			if family == 4 && quicIPv6Only(presetConfig(c, preset).UDP) {
				t.Fatal("IPv6-only method tested on IPv4", preset.ID)
			}
			return TargetProbe{Stage: "complete", Bytes: 65536}, nil
		}
		request := ScanRequest{ID: "quic", Mode: "search", Targets: []ScanTarget{{Host: "example.com", Port: 443, Protocol: "http3", Family: family, Path: "/"}}}
		if err := performScan(context.Background(), request, defaultConfig(), ""); err != nil {
			t.Fatal(err)
		}
		expected := 15
		if family == 6 {
			expected = 18
		}
		if calls != expected || len(scanStatus().Results) != expected {
			t.Fatal("search lost strategy families", family, calls, len(scanStatus().Results))
		}
	}
}
