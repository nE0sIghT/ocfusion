// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type MonitorConfig struct {
	Enabled  bool         `json:"enabled"`
	Interval int          `json:"interval_minutes"`
	Targets  []ScanTarget `json:"targets"`
}

type HealthResult struct {
	Target      ScanTarget  `json:"target"`
	Strategy    string      `json:"strategy"`
	Processed   bool        `json:"processed"`
	Observation TargetProbe `json:"observation"`
	Failures    int         `json:"consecutive_failures"`
}

type HealthState struct {
	State    string         `json:"state"`
	Revision uint64         `json:"revision"`
	Checked  int64          `json:"checked"`
	Next     int64          `json:"next_check"`
	Results  []HealthResult `json:"results"`
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
	if err != nil || len(data) > 8192 || json.Unmarshal(data, &state) != nil {
		state.State = "IDLE"
	}
	return state
}

func publicHealthStatus() HealthState {
	state := healthStatus()
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
	}
	if decodeScan(data, &request) != nil {
		return errors.New("invalid monitoring request")
	}
	c, err := loadConfig()
	if err != nil || c.Revision != request.Revision || len(c.Monitor.Targets) == 0 {
		return errors.New("save monitoring targets before testing")
	}
	service, err := os.Stat(scanPath("service.json"))
	if err != nil || time.Since(service.ModTime()) > 15*time.Second {
		return errors.New("module service unavailable")
	}
	return writeJSON(scanPath("health-request.json"), request)
}

func healthDue(c Config, workerRunning bool) bool {
	path := scanPath("health-request.json")
	if data, err := os.ReadFile(path); err == nil {
		os.Remove(path)
		var request struct {
			Revision uint64 `json:"revision"`
		}
		return decodeScan(data, &request) == nil && request.Revision == c.Revision && len(c.Monitor.Targets) > 0
	}
	state := healthStatus()
	return c.Enabled && workerRunning && c.Monitor.Enabled && len(c.Monitor.Targets) > 0 && (state.Revision != c.Revision || time.Now().Unix() >= state.Next)
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
	state := HealthState{State: "CHECKING", Revision: c.Revision, Checked: time.Now().Unix(), Results: []HealthResult{}}
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
		if row.Observation.Stage == "" {
			preset, name := configuredProbePreset(c, target, resolved[i].Address)
			row.Strategy, row.Processed = name, preset.ID != "baseline"
			trial, cancel := context.WithTimeout(ctx, 9*time.Second)
			observation, err := discoveryProbe(trial, target, c, preset, resolved[i], bypass)
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
					if old.Target == target {
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
	state := healthStatus()
	if _, err := os.Stat(scanPath("health-request.json")); err == nil {
		state.State = "QUEUED"
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
