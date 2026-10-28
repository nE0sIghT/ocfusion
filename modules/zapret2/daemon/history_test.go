// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func discoveryFixture(t *testing.T) {
	t.Helper()
	previousRun, previousResolve, previousProbe := runDir, discoveryResolve, discoveryProbe
	runDir = t.TempDir()
	t.Setenv("EXT_CONFIG_PATH", runDir+"/config.json")
	t.Cleanup(func() { runDir, discoveryResolve, discoveryProbe = previousRun, previousResolve, previousProbe })
	discoveryResolve = func(_ context.Context, targets []ScanTarget) []resolvedScanTarget {
		result := make([]resolvedScanTarget, len(targets))
		for i := range result {
			result[i] = resolvedScanTarget{Address: "203.0.113.1", Interface: "ppp0", Source: "192.0.2.10"}
		}
		return result
	}
}

func TestConfirmationRequiresEveryRepeatAndPersistsOnlySuccess(t *testing.T) {
	discoveryFixture(t)
	c := defaultConfig()
	seed := scanCandidates("tcp")[1]
	request := ScanRequest{ID: "test", Mode: "confirm", Targets: []ScanTarget{{Host: "example.com", Port: 443, Protocol: "https", Path: "/private?key=test"}}, seed: &seed}
	calls := 0
	discoveryProbe = func(context.Context, ScanTarget, Config, Preset, resolvedScanTarget, string) (TargetProbe, error) {
		calls++
		if calls == 3 {
			return TargetProbe{Stage: "body", Bytes: 17000}, errors.New("cutoff")
		}
		return TargetProbe{Stage: "complete", Bytes: 65536}, nil
	}
	if err := performScan(context.Background(), request, c, ""); err != nil {
		t.Fatal(err)
	}
	state := scanStatus()
	if calls != 3 || state.Results[0].Confirmed || len(loadHistory()) != 0 {
		t.Fatal(state, calls)
	}
	calls = 0
	discoveryProbe = func(context.Context, ScanTarget, Config, Preset, resolvedScanTarget, string) (TargetProbe, error) {
		calls++
		return TargetProbe{Stage: "complete", Bytes: 65536}, nil
	}
	if err := performScan(context.Background(), request, c, ""); err != nil {
		t.Fatal(err)
	}
	state = scanStatus()
	if calls != 3 || !state.Results[0].Confirmed || !state.Results[0].HistorySaved {
		t.Fatal(state, calls)
	}
	entries := loadHistory()
	if len(entries) != 1 || entries[0].Targets[0].Path != "/private?key=test" {
		t.Fatal(entries)
	}
	public := publicHistory()
	if public[0].Targets[0].Path != "" || public[0].Settings != nil || public[0].Network != "" {
		t.Fatal("history details leaked", public)
	}
	if err := writeJSON(os.Getenv("EXT_CONFIG_PATH"), c); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(scanPath("service.json"), map[string]string{"state": "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"revision": c.Revision, "mode": "history", "from": entries[0].ID})
	queued, err := requestScan(data)
	if err != nil || queued.Targets[0].Path != "" {
		t.Fatal(queued, err)
	}
	next, ok := pendingScan()
	if !ok || next.seed == nil || next.Targets[0].Path != "/private?key=test" {
		t.Fatal(next, ok)
	}
	resetScan()
	routes := discoveryResolve(context.Background(), request.Targets)
	candidates := historyCandidates(scanCandidates("tcp"), request.Targets, routes)
	if !strings.HasPrefix(candidates[1].ID, "memory-") || len(candidates) > len(scanCandidates("tcp")) {
		t.Fatal(candidates)
	}
	routes[0].Source = "192.0.2.20"
	if strings.HasPrefix(historyCandidates(scanCandidates("tcp"), request.Targets, routes)[1].ID, "memory-") {
		t.Fatal("history from another WAN was prioritized")
	}
	if err := os.WriteFile(historyPath(), []byte(`[{"settings":{"lua_init":"bad"}}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if len(loadHistory()) != 0 {
		t.Fatal("corrupt history accepted")
	}
}

func TestMonitoringChecksAppliedPolicyAndOnlyReportsFailures(t *testing.T) {
	discoveryFixture(t)
	c := defaultConfig()
	c.Enabled = true
	c.Revision = 4
	c.Networks = []string{"203.0.113.0/24"}
	c.Monitor = MonitorConfig{Enabled: true, Interval: 15, Targets: []ScanTarget{{Host: "example.com", Port: 443, Protocol: "https", Path: "/private"}}}
	discoveryProbe = func(_ context.Context, _ ScanTarget, _ Config, preset Preset, _ resolvedScanTarget, _ string) (TargetProbe, error) {
		if preset.ID == "baseline" {
			t.Fatal("configured strategy was bypassed")
		}
		return TargetProbe{Stage: "tls"}, errors.New("failure")
	}
	for i := 1; i <= 2; i++ {
		if err := performHealth(context.Background(), c, ""); err != nil {
			t.Fatal(err)
		}
		state := healthStatus()
		if state.State != "DEGRADED" || state.Results[0].Failures != i {
			t.Fatal(state)
		}
	}
	if publicHealthStatus().Results[0].Target.Path != "" || len(healthSummary().Results) != 0 {
		t.Fatal("monitor details in shared public status")
	}
	if _, err := os.Stat(os.Getenv("EXT_CONFIG_PATH")); !os.IsNotExist(err) {
		t.Fatal("monitor wrote platform configuration")
	}
	discoveryProbe = func(context.Context, ScanTarget, Config, Preset, resolvedScanTarget, string) (TargetProbe, error) {
		return TargetProbe{Stage: "complete"}, nil
	}
	if err := performHealth(context.Background(), c, ""); err != nil || healthStatus().State != "HEALTHY" || healthStatus().Results[0].Failures != 0 {
		t.Fatal(err, healthStatus())
	}
	if healthDue(c, true) {
		t.Fatal("monitor ignored interval")
	}
	if err := os.WriteFile(scanPath("request.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if !healthCancelled(c) {
		t.Fatal("manual discovery did not preempt monitoring")
	}
}
