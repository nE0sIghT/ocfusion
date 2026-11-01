// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestRuleMonitoringSchedulesAndResultsAreIndependent(t *testing.T) {
	discoveryFixture(t)
	c := defaultConfig()
	c.Enabled = true
	c.Revision = 5
	for _, id := range []string{"one", "two"} {
		c.Rules = append(c.Rules, DestinationRule{ID: id, Name: id, Enabled: true, Domains: []string{id + ".example.com"}, Monitor: MonitorConfig{Enabled: true, Interval: 15, Targets: []ScanTarget{{Host: id + ".example.com", Port: 443, Protocol: "https", Path: "/"}}}})
	}
	discoveryProbe = func(_ context.Context, target ScanTarget, _ Config, _ Preset, _ resolvedScanTarget, _ string) (TargetProbe, error) {
		return TargetProbe{Stage: "complete", Success: true}, nil
	}
	for _, id := range []string{"one", "two"} {
		if !healthDue(c, true) {
			t.Fatal("expected due rule", id)
		}
		if err := performHealth(context.Background(), c, ""); err != nil {
			t.Fatal(err)
		}
		state := ruleHealthStatus(id)
		if state.RuleID != id || len(state.Results) != 1 || state.Results[0].Target.Host != id+".example.com" || state.Next <= time.Now().Unix() {
			t.Fatal(state)
		}
	}
	if healthDue(c, true) {
		t.Fatal("independent schedules were ignored")
	}
	c.Rules[0].Enabled = false
	data, _ := json.Marshal(c)
	os.WriteFile(os.Getenv("EXT_CONFIG_PATH"), data, 0600)
	writeJSON(scanPath("service.json"), map[string]string{"state": "RUNNING"})
	request, _ := json.Marshal(map[string]any{"revision": c.Revision, "rule_id": "one"})
	if requestHealth(request) == nil {
		t.Fatal("disabled rule accepted")
	}
	request, _ = json.Marshal(map[string]any{"revision": c.Revision, "rule_id": "two"})
	if err := requestHealth(request); err != nil {
		t.Fatal(err)
	}
	if !healthDue(c, true) {
		t.Fatal("manual check did not override interval")
	}
}

func TestRuleCheckDoesNotProbeAnotherRulesTarget(t *testing.T) {
	discoveryFixture(t)
	c := defaultConfig()
	c.Enabled = true
	c.Revision = 2
	c.Rules = []DestinationRule{
		{ID: "first", Name: "First", Enabled: true, Domains: []string{"example.com"}},
		{ID: "second", Name: "Second", Enabled: true, Domains: []string{"example.com"}, Monitor: MonitorConfig{Enabled: true, Interval: 15, Targets: []ScanTarget{{Host: "example.com", Port: 443, Protocol: "https", Path: "/"}}}},
	}
	discoveryProbe = func(context.Context, ScanTarget, Config, Preset, resolvedScanTarget, string) (TargetProbe, error) {
		t.Fatal("probe ran with another rule's policy")
		return TargetProbe{}, nil
	}
	if !healthDue(c, true) {
		t.Fatal("check was not scheduled")
	}
	if err := performHealth(context.Background(), c, ""); err != nil {
		t.Fatal(err)
	}
	state := ruleHealthStatus("second")
	if state.State != "DEGRADED" || len(state.Results) != 1 || state.Results[0].Observation.Stage != "rule-selection" {
		t.Fatal(state)
	}
}
