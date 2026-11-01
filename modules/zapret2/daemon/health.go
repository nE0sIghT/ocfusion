// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type MonitorConfig struct {
	Enabled  bool         `json:"enabled"`
	Interval int          `json:"interval_minutes"`
	Targets  []ScanTarget `json:"targets"`
}

type HealthResult struct {
	Path        string      `json:"path,omitempty"`
	Target      ScanTarget  `json:"target"`
	Strategy    string      `json:"strategy"`
	Processed   bool        `json:"processed"`
	Observation TargetProbe `json:"observation"`
	Failures    int         `json:"consecutive_failures"`
}

type HealthState struct {
	RuleID   string                 `json:"rule_id,omitempty"`
	Rules    map[string]HealthState `json:"rules,omitempty"`
	State    string                 `json:"state"`
	Revision uint64                 `json:"revision"`
	Checked  int64                  `json:"checked"`
	Next     int64                  `json:"next_check"`
	Results  []HealthResult         `json:"results"`
}

func validateMonitor(c *Config) error {
	if c.Monitor.Interval < 5 || c.Monitor.Interval > 1440 {
		return errors.New("monitor interval must be between 5 and 1440 minutes")
	}
	if c.Monitor.Enabled && len(c.Monitor.Targets) == 0 {
		return errors.New("choose monitoring targets")
	}
	if len(c.Monitor.Targets) > 0 {
		request := ScanRequest{Targets: c.Monitor.Targets}
		if err := normalizeScanTargets(&request); err != nil {
			return err
		}
		c.Monitor.Targets = request.Targets
	}
	return nil
}

func healthStatus() HealthState {
	var state HealthState
	data, err := os.ReadFile(scanPath("health.json"))
	if err != nil || len(data) > 65536 || json.Unmarshal(data, &state) != nil {
		state.State = "IDLE"
	}
	return state
}

func ruleHealthStatus(id string) HealthState {
	var state HealthState
	data, err := os.ReadFile(scanPath("health-rule-" + id + ".json"))
	if err != nil || len(data) > 8192 || json.Unmarshal(data, &state) != nil {
		state.State = "IDLE"
	}
	return state
}

func publicHealthStatus() HealthState {
	state := healthStatus()
	state.Rules = map[string]HealthState{}
	if c, err := loadConfig(); err == nil {
		for _, rule := range c.Rules {
			entry := ruleHealthStatus(rule.ID)
			if state.RuleID == rule.ID && (state.State == "CHECKING" || state.State == "PAUSED") {
				entry = state
				entry.Rules = nil
			}
			for i := range entry.Results {
				entry.Results[i].Target.Path = ""
			}
			state.Rules[rule.ID] = entry
		}
	}
	for i := range state.Results {
		state.Results[i].Target.Path = ""
	}
	return state
}

func portSelected(ports []int, port int) bool {
	for _, value := range ports {
		if value == port {
			return true
		}
	}
	return false
}

func configuredProbePreset(c Config, target ScanTarget, address string) (Preset, string) {
	baseline := Preset{ID: "baseline", Name: "Without processing", Protocol: scanProtocol(target)}
	if !c.Enabled {
		return baseline, "Without processing"
	}
	for _, selected := range processingConfigs(c) {
		if !selectorMatches(selected, target, address) {
			continue
		}
		name := "Default strategy"
		if selected.SelectorPrefix != "" {
			for _, rule := range c.Rules {
				if "rule-"+rule.ID+"-" == selected.SelectorPrefix {
					name = rule.Name
				}
			}
		}
		preset := Preset{ID: "health", Name: name, Protocol: scanProtocol(target), Settings: strategyProjection(selected)}
		if preset.Protocol == "tcp" && (selected.TCP == "off" || !portSelected(selected.TCPPorts, target.Port)) {
			return baseline, name
		}
		if preset.Protocol == "udp" && (selected.UDP == "off" || !portSelected(selected.UDPPorts, target.Port)) {
			return baseline, name
		}
		if preset.Protocol == "stun" {
			found := false
			for _, profile := range selected.Applications {
				if profile.ID != "stun" {
					continue
				}
				ranges, _ := parsePortRanges(profile.Ports)
				for _, ports := range ranges {
					if target.Port >= ports.First && target.Port <= ports.Last {
						found = true
					}
				}
				if found {
					preset.Settings["applications"] = []ApplicationProfile{profile}
					break
				}
			}
			if !found {
				return baseline, name
			}
		}
		return canonicalScanPreset(preset), name
	}
	return baseline, "Without processing"
}

func requestHealth(data []byte) error {
	var request struct {
		Revision uint64 `json:"revision"`
		RuleID   string `json:"rule_id"`
	}
	if decodeScan(data, &request) != nil {
		return errors.New("invalid monitoring request")
	}
	c, err := loadConfig()
	if err != nil || c.Revision != request.Revision {
		return errors.New("save monitoring targets before testing")
	}
	found := false
	for _, rule := range c.Rules {
		if rule.ID == request.RuleID && rule.Enabled && len(rule.Monitor.Targets) > 0 {
			found = true
		}
	}
	if !found {
		return errors.New("save monitoring targets before testing")
	}
	service, err := os.Stat(scanPath("service.json"))
	if err != nil || time.Since(service.ModTime()) > 15*time.Second {
		return errors.New("module service unavailable")
	}
	return writeJSON(scanPath("health-request.json"), request)
}

func healthDue(c Config, processingActive bool) bool {
	var requested string
	if data, err := os.ReadFile(scanPath("health-request.json")); err == nil {
		os.Remove(scanPath("health-request.json"))
		var request struct {
			Revision uint64 `json:"revision"`
			RuleID   string `json:"rule_id"`
		}
		if decodeScan(data, &request) == nil && request.Revision == c.Revision {
			requested = request.RuleID
		}
	}
	for _, rule := range c.Rules {
		if !rule.Enabled || len(rule.Monitor.Targets) == 0 {
			continue
		}
		state := ruleHealthStatus(rule.ID)
		scheduled := c.Enabled && processingActive && rule.Monitor.Enabled && (state.Revision != c.Revision || time.Now().Unix() >= state.Next)
		if rule.ID == requested || (requested == "" && scheduled) {
			return writeJSON(scanPath("health-selection.json"), rule.ID) == nil
		}
	}
	return false
}

func healthCancelled(c Config) bool {
	if healthStatus().Revision != c.Revision {
		return true
	}
	_, err := os.Stat(scanPath("request.json"))
	return err == nil
}

func performHealth(ctx context.Context, c Config, bypass string) (failure error) {
	previous := healthStatus()
	ruleID := ""
	if data, err := os.ReadFile(scanPath("health-selection.json")); err == nil {
		os.Remove(scanPath("health-selection.json"))
		json.Unmarshal(data, &ruleID)
	}
	if ruleID != "" {
		found := false
		for _, rule := range c.Rules {
			if rule.ID == ruleID && rule.Enabled {
				c.Monitor = rule.Monitor
				previous = ruleHealthStatus(ruleID)
				found = true
				break
			}
		}
		if !found {
			return errors.New("invalid monitoring request")
		}
	}
	state := HealthState{RuleID: ruleID, State: "CHECKING", Revision: c.Revision, Checked: time.Now().Unix(), Results: []HealthResult{}}
	writeJSON(scanPath("health.json"), state)
	defer func() {
		cleanupErr := cleanupAt(scanResources(), "ocfusion:zapret2-scan")
		state.Next = time.Now().Add(time.Duration(c.Monitor.Interval) * time.Minute).Unix()
		if failure != nil || cleanupErr != nil {
			state.State = "ERROR"
		}
		if ctx.Err() != nil {
			state.State = "PAUSED"
		}
		if cleanupErr != nil {
			state.State = "ERROR"
		}
		writeJSON(scanPath("health.json"), state)
		if ruleID != "" {
			writeJSON(scanPath("health-rule-"+ruleID+".json"), state)
		}
		failure = errors.Join(failure, cleanupErr)
	}()
	if err := cleanupAt(scanResources(), "ocfusion:zapret2-scan"); err != nil {
		return err
	}
	resolved := discoveryResolve(ctx, c.Monitor.Targets)
	for i, target := range c.Monitor.Targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		row := HealthResult{Target: target, Observation: TargetProbe{Target: i, Stage: resolved[i].Error}}
		if row.Observation.Stage == "" && ruleID != "" {
			matched := ""
			for _, selected := range processingConfigs(c) {
				if selectorMatches(selected, target, resolved[i].Address) {
					matched = selected.SelectorPrefix
					break
				}
			}
			if matched != "rule-"+ruleID+"-" {
				row.Observation.Stage = "rule-selection"
			}
		}
		if row.Observation.Stage == "" {
			preset, name := configuredProbePreset(c, target, resolved[i].Address)
			row.Strategy, row.Processed = name, preset.ID != "baseline"
			row.Path = "WAN"
			if row.Processed {
				row.Path = "DPI"
			}

			probeBypass := bypass
			if len(routePolicies(c)) > 0 {
				reply, routeErr := vpnRequest("destination-probe", []RoutePolicy{{ID: resolved[i].Address}})
				if routeErr != nil || len(reply.Status) != 1 {
					row.Observation.Stage = "route"
				} else if selected := reply.Status[0]; selected.State == "BLOCKED" {
					row.Observation.Stage = "vpn"
					row.Path = "BLOCKED"
				} else if selected.State == "VPN" || selected.State == "WAN" || selected.State == "NORMAL" {
					row.Path = selected.State
					if selected.State == "NORMAL" {
						row.Strategy = "Without processing"
					}
					preset = Preset{ID: "baseline", Protocol: scanProtocol(target)}
					row.Processed = false
					resolved[i].Direct = true
					if selected.State == "VPN" {
						resolved[i].Interface, resolved[i].Source = selected.Interface, selected.Source
						mark, _ := strconv.ParseUint(selected.Mark, 0, 32)
						original, _ := strconv.ParseUint(bypass, 0, 32)
						probeBypass = fmt.Sprintf("0x%x", mark|original)
					}
				}
			}
			trial, cancel := context.WithTimeout(ctx, 9*time.Second)
			observation := row.Observation
			var err error
			if row.Observation.Stage == "" {
				observation, err = discoveryProbe(trial, target, c, preset, resolved[i], probeBypass)
			} else {
				err = errors.New("selected route unavailable")
			}
			cancel()
			if errors.Is(err, errScanSetup) {
				return err
			}
			row.Observation = observation
			row.Observation.Success = err == nil
		}
		if !row.Observation.Success {
			row.Failures = 1
			if previous.Revision == c.Revision {
				for _, old := range previous.Results {
					if old.Target == target && previous.RuleID == ruleID {
						row.Failures += old.Failures
					}
				}
			}
			if row.Failures > 100 {
				row.Failures = 100
			}
		}
		state.Results = append(state.Results, row)
	}
	state.State = "HEALTHY"
	for _, row := range state.Results {
		if row.Failures > 0 {
			state.State = "DEGRADED"
			break
		}
	}
	return nil
}

func healthSummary() HealthState {
	state := publicHealthStatus()
	for id, entry := range state.Rules {
		entry.Results = nil
		state.Rules[id] = entry
	}
	if data, err := os.ReadFile(scanPath("health-request.json")); err == nil {
		var request struct {
			RuleID   string `json:"rule_id"`
			Revision uint64 `json:"revision"`
		}
		if json.Unmarshal(data, &request) == nil {
			if entry, ok := state.Rules[request.RuleID]; ok {
				entry.State = "QUEUED"
				state.Rules[request.RuleID] = entry
			}
		}
	}
	state.Results = nil
	return state
}

func resetHealth() {
	state := healthStatus()
	if state.State == "CHECKING" {
		state.State = "PAUSED"
		writeJSON(scanPath("health.json"), state)
	}
	os.Remove(scanPath("health-request.json"))
}
