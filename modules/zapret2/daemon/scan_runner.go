// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"time"
)

var discoveryResolve = resolveScanTargets
var discoveryProbe = performProtocolProbe

type resolvedScanTarget struct {
	Direct    bool
	Address   string
	Interface string
	Source    string
	Error     string
}

func resolveScanTargets(ctx context.Context, targets []ScanTarget) []resolvedScanTarget {
	resolved := make([]resolvedScanTarget, len(targets))
	for i, target := range targets {
		deadline, cancel := context.WithTimeout(ctx, 8*time.Second)
		network, family := "ip4", "-4"
		if target.Family == 6 {
			network, family = "ip6", "-6"
		}
		addresses, err := net.DefaultResolver.LookupNetIP(deadline, network, target.Host)
		cancel()
		if err != nil {
			resolved[i].Error = "dns"
			continue
		}
		for _, address := range addresses {
			if scanAddressAllowed(address.Unmap()) {
				resolved[i].Address = address.Unmap().String()
				break
			}
		}
		if resolved[i].Address == "" {
			resolved[i].Error = "address"
			continue
		}
		iface, err := wan(family)
		if err != nil {
			resolved[i].Error = "route"
			continue
		}
		resolved[i].Interface = iface
		resolved[i].Source, err = routeForScan(resolved[i].Address, iface)
		if err != nil {
			resolved[i].Error = "route"
		}
	}
	return resolved
}

func scanCandidateSettings(id string) (Preset, error) {
	var candidates map[string]Preset
	data, err := os.ReadFile(scanPath("candidates.json"))
	if err != nil || len(data) > 32768 || json.Unmarshal(data, &candidates) != nil {
		return Preset{}, errors.New("test settings unavailable")
	}
	preset, ok := candidates[id]
	if !ok || id == "baseline" || preset.Settings == nil {
		return Preset{}, errors.New("choose a strategy result")
	}
	return preset, nil
}

func performScan(ctx context.Context, request ScanRequest, c Config, bypass string) (failure error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	state := ScanState{ScanRequest: request, State: "RUNNING", Results: []ProbeResult{}}
	defer func() {
		cleanupErr := cleanupAt(scanResources(), "ocfusion:zapret2-scan")
		state.Current = ""
		state.State = "COMPLETE"
		if failure != nil {
			state.State, state.Error = "ERROR", "Test could not be completed"
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			state.State, state.Error = "ERROR", "Test time limit reached"
		} else if ctx.Err() != nil {
			state.State, state.Error = "CANCELLED", ""
		}
		if cleanupErr != nil {
			state.State, state.Error = "ERROR", "Temporary rule cleanup failed"
			failure = errors.Join(failure, cleanupErr)
		}
		writeJSON(scanPath("status.json"), state)
		os.Remove(scanPath("lock"))
	}()
	if err := cleanupAt(scanResources(), "ocfusion:zapret2-scan"); err != nil {
		return err
	}
	if request.Revision != c.Revision {
		return errors.New("settings changed")
	}
	writeJSON(scanPath("status.json"), state)
	resolved := discoveryResolve(ctx, request.Targets)
	saved := map[string]Preset{}
	if err := writeJSON(scanPath("candidates.json"), saved); err != nil {
		return err
	}
	for _, protocol := range []string{"tcp", "udp", "stun"} {
		indices := []int{}
		for i, target := range request.Targets {
			if scanProtocol(target) == protocol {
				indices = append(indices, i)
			}
		}
		if len(indices) == 0 {
			continue
		}
		candidates := scanCandidates(protocol)
		if request.seed != nil {
			candidates = refinedCandidates(*request.seed, request.Mode)
			if request.Mode == "confirm" || request.Mode == "history" {
				candidates = []Preset{*request.seed}
			}
		} else {
			targets := []ScanTarget{}
			routes := []resolvedScanTarget{}
			for _, i := range indices {
				targets = append(targets, request.Targets[i])
				routes = append(routes, resolved[i])
			}
			candidates = historyCandidates(candidates, targets, routes)
		}
		for _, preset := range candidates {
			if quicIPv6Only(presetConfig(c, preset).UDP) && !hasIPv6Target(request.Targets, indices) {
				continue
			}
			if len(state.Results) >= scanResultLimit {
				break
			}
			result := ProbeResult{Preset: protocol + ":" + preset.ID, Name: preset.Name, Protocol: protocol, Remembered: strings.HasPrefix(preset.ID, "memory-")}
			if preset.ID != "baseline" {
				saved[result.Preset] = preset
				if err := writeJSON(scanPath("candidates.json"), saved); err != nil {
					return err
				}
			}
			state.Current = result.Preset
			attempts := 1
			if request.Mode == "confirm" || request.Mode == "history" {
				attempts = 3
			}
			for attempt := 0; attempt < attempts; attempt++ {
				for _, i := range indices {
					if quicIPv6Only(presetConfig(c, preset).UDP) && request.Targets[i].Family != 6 {
						continue
					}
					if ctx.Err() != nil {
						return ctx.Err()
					}
					state.CurrentTarget = i
					writeJSON(scanPath("status.json"), state)
					detail := TargetProbe{Target: i, Stage: resolved[i].Error}
					if detail.Stage == "" {
						trial, stop := context.WithTimeout(ctx, 9*time.Second)
						observation, err := discoveryProbe(trial, request.Targets[i], c, preset, resolved[i], bypass)
						stop()
						if errors.Is(err, errScanSetup) {
							return err
						}
						detail = observation
						detail.Target = i
						detail.Success = err == nil
					}
					if request.Mode == "confirm" || request.Mode == "history" {
						detail.Attempt = attempt + 1
					}
					result.Details = append(result.Details, detail)
					result.Attempts++
					if detail.Success {
						result.Success++
						result.Milliseconds += detail.Milliseconds
						result.Status = detail.Status
					} else {
						result.Error = "Some targets failed"
					}
					if detail.Bytes > result.Bytes {
						result.Bytes = detail.Bytes
					}
					if _, err := os.Stat(scanResources()); err == nil {
						return errors.New("temporary resources remain")
					}
				}
			}
			if result.Success > 0 {
				result.Milliseconds /= int64(result.Success)
			}
			result.Confirmed = (request.Mode == "confirm" || request.Mode == "history") && result.Attempts == 3*len(indices) && result.Success == result.Attempts
			if result.Confirmed {
				result.HistorySaved = rememberConfirmed(request, preset, result, resolved, indices) == nil
			}

			state.Results = append(state.Results, result)
			writeJSON(scanPath("status.json"), state)
		}
	}
	return nil
}
