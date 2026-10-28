// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ScanRequest struct {
	ID       string       `json:"id"`
	Revision uint64       `json:"revision"`
	Host     string       `json:"host,omitempty"`
	Port     int          `json:"port,omitempty"`
	Targets  []ScanTarget `json:"targets,omitempty"`
	Mode     string       `json:"mode,omitempty"`
	From     string       `json:"from,omitempty"`
	Parent   string       `json:"parent,omitempty"`
	seed     *Preset
}

type TargetProbe struct {
	Target       int    `json:"target"`
	Attempt      int    `json:"attempt,omitempty"`
	Success      bool   `json:"success"`
	Stage        string `json:"stage"`
	Bytes        int64  `json:"bytes,omitempty"`
	Milliseconds int64  `json:"milliseconds,omitempty"`
	Status       int    `json:"http_status,omitempty"`
}

type ProbeResult struct {
	Preset       string        `json:"preset"`
	Confirmed    bool          `json:"confirmed,omitempty"`
	Remembered   bool          `json:"remembered,omitempty"`
	HistorySaved bool          `json:"history_saved,omitempty"`
	Name         string        `json:"name,omitempty"`
	Protocol     string        `json:"protocol,omitempty"`
	Success      int           `json:"success"`
	Attempts     int           `json:"attempts"`
	Milliseconds int64         `json:"milliseconds"`
	Error        string        `json:"error,omitempty"`
	Status       int           `json:"http_status,omitempty"`
	Bytes        int64         `json:"bytes,omitempty"`
	Details      []TargetProbe `json:"details,omitempty"`
}

type ScanState struct {
	ScanRequest
	State         string        `json:"state"`
	Results       []ProbeResult `json:"results"`
	Current       string        `json:"current,omitempty"`
	CurrentTarget int           `json:"current_target"`
	Error         string        `json:"error,omitempty"`
}

func scanPath(name string) string {
	return filepath.Join(runDir, "scan-"+name)
}

func scanResources() string {
	return scanPath("resources.json")
}

func scanStatus() ScanState {
	var state ScanState
	data, err := os.ReadFile(scanPath("status.json"))
	if err != nil || len(data) > 32768 || json.Unmarshal(data, &state) != nil {
		state.State = "IDLE"
	}
	return state
}

func resetScan() {
	state := scanStatus()
	if state.State == "RUNNING" || state.State == "QUEUED" {
		state.State = "CANCELLED"
		writeJSON(scanPath("status.json"), state)
	}
	os.Remove(scanPath("request.json"))
	os.Remove(scanPath("cancel"))
	os.Remove(scanPath("lock"))
}

func decodeScan(data []byte, result any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 4096 || decoder.Decode(result) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid scan request")
	}
	return nil
}

func requestScan(data []byte) (ScanState, error) {
	var request ScanRequest
	if err := decodeScan(data, &request); err != nil {
		return ScanState{}, err
	}
	c, err := loadConfig()
	if err != nil || request.Revision != c.Revision || request.ID != "" {
		return ScanState{}, errors.New("settings changed; refresh before testing")
	}
	if request.Mode == "" {
		request.Mode = "search"
	}
	if request.Mode != "search" && request.Mode != "refine" && request.Mode != "lighten" && request.Mode != "confirm" && request.Mode != "history" {
		return ScanState{}, errors.New("invalid discovery mode")
	}
	if request.Mode == "search" && (request.From != "" || request.Parent != "") {
		return ScanState{}, errors.New("invalid discovery mode")
	}
	if request.Mode == "history" {
		for _, entry := range loadHistory() {
			if entry.ID == request.From {
				preset := Preset{ID: "memory-" + entry.ID, Name: entry.Name, Protocol: entry.Protocol, Settings: entry.Settings}
				request.seed = &preset
				request.Targets = entry.Targets
				break
			}
		}
		if request.seed == nil || request.Parent != "" {
			return ScanState{}, errors.New("history entry unavailable")
		}
	}
	if request.Mode != "search" && request.Mode != "history" {
		previous := scanStatus()
		if previous.ID != request.Parent || previous.Revision != c.Revision || previous.State != "COMPLETE" {
			return ScanState{}, errors.New("test results changed; refresh before refining")
		}
		valid := false
		for _, result := range previous.Results {
			valid = valid || result.Preset == request.From
		}
		if !valid {
			return ScanState{}, errors.New("choose a strategy result")
		}
		preset, seedErr := scanCandidateSettings(request.From)
		if seedErr != nil {
			return ScanState{}, seedErr
		}
		request.seed = &preset
		request.Targets = nil
		for _, target := range previous.Targets {
			if scanProtocol(target) == preset.Protocol {
				request.Targets = append(request.Targets, target)
			}
		}
		if len(request.Targets) == 0 {
			return ScanState{}, errors.New("no matching test targets")
		}
	}
	if err := normalizeScanTargets(&request); err != nil {
		return ScanState{}, err
	}
	// A live daemon owns the scan and all of its child processes.
	status, err := os.Stat(scanPath("service.json"))
	if err != nil || time.Since(status.ModTime()) > 15*time.Second {
		return ScanState{}, errors.New("module service unavailable")
	}
	if _, recoveryErr := os.Stat(scanResources()); recoveryErr == nil && healthStatus().State != "CHECKING" {
		return ScanState{}, errors.New("temporary resources require cleanup")
	}
	if err = os.Mkdir(scanPath("lock"), 0700); err != nil {
		return ScanState{}, errors.New("a scan is already running")
	}
	token := make([]byte, 12)
	if _, err = rand.Read(token); err != nil {
		os.Remove(scanPath("lock"))
		return ScanState{}, err
	}
	request.ID = hex.EncodeToString(token)
	if request.seed != nil {
		if err = writeJSON(scanPath("seed.json"), request.seed); err != nil {
			os.Remove(scanPath("lock"))
			return ScanState{}, err
		}
	} else {
		os.Remove(scanPath("seed.json"))
	}
	state := ScanState{ScanRequest: request, State: "QUEUED", Results: []ProbeResult{}}
	os.Remove(scanPath("cancel"))
	if err = writeJSON(scanPath("status.json"), state); err == nil {
		err = writeJSON(scanPath("request.json"), request)
	}
	if err != nil {
		os.Remove(scanPath("lock"))
	}
	return sanitizedScanState(state), err
}

func scanAddressAllowed(address netip.Addr) bool {
	return address.Is4() && address.IsGlobalUnicast() && !address.IsPrivate()
}

func pendingScan() (ScanRequest, bool) {
	var request ScanRequest
	data, err := os.ReadFile(scanPath("request.json"))
	if err != nil || decodeScan(data, &request) != nil {
		return request, false
	}
	os.Remove(scanPath("request.json"))
	if request.Mode != "search" {
		data, _ := os.ReadFile(scanPath("seed.json"))
		var preset Preset
		if json.Unmarshal(data, &preset) != nil {
			return request, false
		}
		request.seed = &preset
	}
	return request, true
}

func cancelRequestedScan(data []byte) error {
	var request struct {
		ID string `json:"id"`
	}
	if decodeScan(data, &request) != nil || request.ID == "" || request.ID != scanStatus().ID {
		return errors.New("scan changed; refresh before cancelling")
	}
	return os.WriteFile(scanPath("cancel"), []byte(request.ID), 0600)
}

func scanCancelled(c Config) bool {
	state := scanStatus()
	data, _ := os.ReadFile(scanPath("cancel"))
	return state.Revision != c.Revision || string(data) == state.ID
}

func scanRules(r Resources, iface, address string, port, source int, bypass string) []Rule {
	return scanRulesForProtocol(r, iface, address, port, source, bypass, "tcp")
}

func scanRulesForProtocol(r Resources, iface, address string, port, source int, bypass, protocol string) []Rule {
	mark := []string{"-m", "mark", "!", "--mark", r.Mark + "/" + r.Mark}
	queue := append(mark, "-m", "comment", "--comment", "ocfusion:zapret2-scan", "-j", "NFQUEUE", "--queue-num", strconv.Itoa(r.Queue), "--queue-bypass")
	outgoing := []string{"-o", iface, "-p", protocol, "-d", address, "--dport", strconv.Itoa(port), "--sport", strconv.Itoa(source)}
	incoming := []string{"-i", iface, "-p", protocol, "-s", address, "--sport", strconv.Itoa(port), "--dport", strconv.Itoa(source)}
	rules := []Rule{{Tool: "iptables", Chain: "OUTPUT", Args: append(outgoing, queue...)}}
	if bypass != "" {
		// Mark only this reply tuple before the regular module's PREROUTING rules.
		args := append(append([]string{}, incoming...), "-m", "comment", "--comment", "ocfusion:zapret2-scan", "-j", "MARK", "--set-xmark", bypass+"/"+bypass)
		rules = append(rules, Rule{Tool: "iptables", Chain: "PREROUTING", Args: args})
	}
	rules = append(rules, Rule{Tool: "iptables", Chain: "PREROUTING", Args: append(incoming, queue...)})
	return rules
}

func routeForScan(address, iface string) (string, error) {
	output, err := execute("ip", "-4", "route", "get", address, "oif", iface)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	device, source := "", ""
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "dev" {
			device = fields[i+1]
		}
		if fields[i] == "src" {
			source = fields[i+1]
		}
	}
	if device != iface || net.ParseIP(source).To4() == nil {
		return "", errors.New("direct WAN route unavailable")
	}
	return source, nil
}

func sanitizedScanState(state ScanState) ScanState {
	state.Targets = append([]ScanTarget{}, state.Targets...)
	for i := range state.Targets {
		state.Targets[i].Path = ""
	}
	return state
}

func publicScanStatus() ScanState {
	return sanitizedScanState(scanStatus())
}
