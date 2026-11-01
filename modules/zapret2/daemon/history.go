// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const historyLimit = 8

type HistoryEntry struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Protocol string         `json:"protocol"`
	Updated  int64          `json:"updated"`
	Targets  []ScanTarget   `json:"targets"`
	Network  string         `json:"network"`
	Settings map[string]any `json:"settings"`
	Success  int            `json:"success"`
	Attempts int            `json:"attempts"`
}

func historyPath() string {
	if path := os.Getenv("EXT_CONFIG_PATH"); path != "" {
		return filepath.Join(filepath.Dir(path), "data", "strategy-history.json")
	}
	return filepath.Join(runDir, "history.json")
}

func targetFingerprint(targets []ScanTarget) string {
	values := []string{}
	for _, target := range targets {
		data, _ := json.Marshal(target)
		values = append(values, string(data))
	}
	sort.Strings(values)
	data, _ := json.Marshal(values)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:12])
}

func networkFingerprint(resolved []resolvedScanTarget) string {
	values := []string{}
	for _, target := range resolved {
		if target.Error != "" || target.Source == "" || target.Interface == "" {
			return ""
		}
		values = append(values, target.Interface+":"+target.Source)
	}
	data, _ := json.Marshal(uniqueEntries(values))
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:12])
}

func loadHistory() []HistoryEntry {
	file, err := os.Open(historyPath())
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	var entries []HistoryEntry
	if err != nil || len(data) > 65536 || json.Unmarshal(data, &entries) != nil || len(entries) > historyLimit {
		return nil
	}
	for _, entry := range entries {
		request := ScanRequest{Targets: entry.Targets}
		if normalizeScanTargets(&request) != nil || entry.ID == "" || len(entry.ID) > 48 || len(entry.Name) > 96 || entry.Success != entry.Attempts || entry.Attempts != 3*len(entry.Targets) || entry.Updated <= 0 {
			return nil
		}
		if entry.Protocol != "tcp" && entry.Protocol != "udp" && entry.Protocol != "stun" {
			return nil
		}
		if _, err := tunedConfig(defaultConfig(), entry.Settings); err != nil {
			return nil
		}
	}
	return entries
}

func rememberConfirmed(request ScanRequest, preset Preset, result ProbeResult, resolved []resolvedScanTarget, indices []int) error {
	if !result.Confirmed {
		return nil
	}
	targets := []ScanTarget{}
	routes := []resolvedScanTarget{}
	for _, i := range indices {
		targets = append(targets, request.Targets[i])
		routes = append(routes, resolved[i])
	}
	network := networkFingerprint(routes)
	if network == "" {
		return errors.New("history network unavailable")
	}
	identity := candidateID(preset) + targetFingerprint(targets) + network
	digest := sha256.Sum256([]byte(identity))
	entry := HistoryEntry{ID: hex.EncodeToString(digest[:12]), Name: preset.Name, Protocol: preset.Protocol, Updated: time.Now().Unix(), Targets: targets, Network: network, Settings: preset.Settings, Success: result.Success, Attempts: result.Attempts}
	next := []HistoryEntry{entry}
	for _, previous := range loadHistory() {
		if previous.ID != entry.ID && len(next) < historyLimit {
			next = append(next, previous)
		}
	}
	if err := os.MkdirAll(filepath.Dir(historyPath()), 0700); err != nil {
		return err
	}
	return writeCompactJSON(historyPath(), next)
}

func publicHistory() []HistoryEntry {
	entries := append([]HistoryEntry{}, loadHistory()...)
	for i := range entries {
		entries[i].Settings = nil
		entries[i].Network = ""
		for j := range entries[i].Targets {
			entries[i].Targets[j].Path = ""
		}
	}
	return entries
}

func historyCandidates(candidates []Preset, targets []ScanTarget, resolved []resolvedScanTarget) []Preset {
	if len(candidates) == 0 {
		return candidates
	}
	next := []Preset{candidates[0]}
	seen := map[string]bool{}
	fingerprint := targetFingerprint(targets)
	network := networkFingerprint(resolved)
	for _, entry := range loadHistory() {
		if entry.Protocol == candidates[0].Protocol && entry.Network == network && fingerprint == targetFingerprint(entry.Targets) {
			preset := Preset{ID: "memory-" + entry.ID, Name: entry.Name, Protocol: entry.Protocol, Settings: entry.Settings}
			signature := candidateSignature(preset)
			if !seen[signature] && len(next) < 4 {
				next = append(next, preset)
				seen[signature] = true
			}
		}
	}
	for _, preset := range candidates[1:] {
		signature := candidateSignature(preset)
		if !seen[signature] && len(next) < len(candidates) {
			next = append(next, preset)
			seen[signature] = true
		}
	}
	return next
}
