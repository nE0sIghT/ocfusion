// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const scanBodyLimit = 64 * 1024
const scanResultLimit = 18

type ScanTarget struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Path     string `json:"path,omitempty"`
}

func normalizeScanTargets(request *ScanRequest) error {
	if len(request.Targets) == 0 {
		request.Targets = []ScanTarget{{Host: request.Host, Port: request.Port, Protocol: "https", Path: "/"}}
	}
	if len(request.Targets) > 4 {
		return errors.New("choose up to four test targets")
	}
	seen := map[ScanTarget]bool{}
	for i := range request.Targets {
		target := &request.Targets[i]
		target.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target.Host), "."))
		if target.Protocol == "" {
			target.Protocol = "https"
		}
		if target.Protocol != "http" && target.Protocol != "https" && target.Protocol != "https12" && target.Protocol != "http3" && target.Protocol != "stun" {
			return errors.New("invalid test protocol")
		}
		if len(target.Host) > 253 || !domainPattern.MatchString(target.Host) || net.ParseIP(target.Host) != nil || target.Port < 1 || target.Port > 65535 {
			return errors.New("choose a domain and service port")
		}
		if target.Protocol == "stun" {
			if target.Path != "" && target.Path != "/" {
				return errors.New("STUN has no HTTP path")
			}
			target.Path = ""
		} else {
			if target.Path == "" {
				target.Path = "/"
			}
			path, err := url.ParseRequestURI(target.Path)
			if err != nil || len(target.Path) > 512 || !strings.HasPrefix(target.Path, "/") || strings.HasPrefix(target.Path, "//") || path.IsAbs() || path.Host != "" || path.Fragment != "" {
				return errors.New("use an HTTP path without a server address")
			}
		}
		if seen[*target] {
			return errors.New("duplicate test target")
		}
		seen[*target] = true
	}
	request.Host, request.Port = request.Targets[0].Host, request.Targets[0].Port
	return nil
}

func scanProtocol(target ScanTarget) string {
	if target.Protocol == "https" || target.Protocol == "https12" || target.Protocol == "http" {
		return "tcp"
	}
	if target.Protocol == "http3" {
		return "udp"
	}
	return "stun"
}

func scanCandidates(protocol string) []Preset {
	result := []Preset{{ID: "baseline", Name: "Without processing", Protocol: protocol}}
	if protocol == "stun" {
		for _, method := range []string{"fake", "fragment", "fake-fragment", "length"} {
			result = append(result, Preset{ID: "stun-" + method, Name: "STUN - " + stunMethodName(method), Protocol: "stun", Settings: map[string]any{"applications": []ApplicationProfile{applicationDefaults("stun", method)}, "auto_ttl": true}})
		}
		return canonicalScanCandidates(result)
	}
	for _, preset := range presetCatalog() {
		if preset.Protocol == protocol {
			result = append(result, preset)
		}
	}
	if protocol == "udp" {
		for _, repeats := range []int{2, 10} {
			c := defaultConfig()
			c.Repeats, c.UDP, c.AutoTTL = repeats, "fake-quic", true
			result = append(result, Preset{ID: "quic-" + strconv.Itoa(repeats), Name: "QUIC fake packets - " + strconv.Itoa(repeats), Protocol: "udp", Settings: map[string]any{"udp_strategy": c.UDP, "fake_repeats": repeats, "auto_ttl": true}})
		}
	}
	return canonicalScanCandidates(result)
}

func canonicalScanCandidates(candidates []Preset) []Preset {
	for i := range candidates {
		candidates[i] = canonicalScanPreset(candidates[i])
	}
	return candidates
}

func canonicalScanPreset(preset Preset) Preset {
	if preset.ID == "baseline" {
		return preset
	}
	c := presetConfig(defaultConfig(), preset)
	c = protocolConfig(c, preset.Protocol)
	settings := map[string]any{
		"fake_ttl": c.TTL, "auto_ttl": c.AutoTTL, "auto_ttl_delta": c.AutoDelta,
		"auto_ttl_min": c.AutoMin, "auto_ttl_max": c.AutoMax,
	}
	switch preset.Protocol {
	case "tcp":
		settings["tcp_strategy"] = c.TCP
		settings["split_position"] = c.Split
		settings["split_positions"] = c.Positions
		settings["sequence_overlap"] = c.Overlap
		settings["fake_fooling"] = c.Fooling
		settings["fake_sni"] = c.FakeSNI
		settings["fake_repeats"] = c.Repeats
		settings["packet_limit"] = c.PacketLimit
	case "udp":
		settings["udp_strategy"] = c.UDP
		settings["fake_repeats"] = c.Repeats
	case "stun":
		settings["applications"] = c.Applications
	}
	preset.Settings = settings
	return preset
}

func stunMethodName(method string) string {
	switch method {
	case "fake":
		return "Fake packets"
	case "fragment":
		return "IP fragmentation"
	case "fake-fragment":
		return "Fake packets and fragmentation"
	case "length":
		return "UDP padding"
	}
	return method
}
