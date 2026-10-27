// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

func candidateID(preset Preset) string {
	data, _ := json.Marshal(preset.Settings)
	digest := sha256.Sum256(append([]byte(preset.Protocol), data...))
	return "variant-" + hex.EncodeToString(digest[:6])
}

func refinedCandidates(seed Preset, mode string) []Preset {
	result := []Preset{{ID: "baseline", Name: "Without processing", Protocol: seed.Protocol}, seed}
	seen := map[string]bool{candidateSignature(seed): true}
	add := func(label string, changes map[string]any) {
		settings := map[string]any{}
		for key, value := range seed.Settings {
			settings[key] = value
		}
		for key, value := range changes {
			settings[key] = value
		}
		next := canonicalScanPreset(Preset{Name: label, Protocol: seed.Protocol, Settings: settings})
		next.ID = candidateID(next)
		signature := candidateSignature(next)
		if !seen[signature] && len(result) < 18 {
			seen[signature] = true
			result = append(result, next)
		}
	}
	if seed.Protocol == "stun" {
		base := presetConfig(defaultConfig(), seed)
		if mode == "lighten" {
			for _, profile := range base.Applications {
				if profile.ID != "stun" {
					continue
				}
				profile.Repeats, profile.Packets, profile.Padding = 1, 1, 1
				add("Fewer application packets", map[string]any{"applications": []ApplicationProfile{profile}})
				if profile.Method == "fake-fragment" {
					for _, method := range []string{"fake", "fragment"} {
						profile.Method = method
						add("STUN - "+stunMethodName(method), map[string]any{"applications": []ApplicationProfile{profile}})
					}
				}
			}
			return result
		}
		for _, method := range []string{"fake", "fragment", "fake-fragment", "length"} {
			for _, repeats := range []int{1, 3, 6} {
				profile := applicationDefaults("stun", method)
				profile.Repeats = repeats
				add("STUN - "+stunMethodName(method)+" / "+strconv.Itoa(repeats), map[string]any{"applications": mergeApplicationProfiles(base.Applications, []ApplicationProfile{profile})})
			}
		}
		return result
	}
	if mode == "lighten" {
		changes := map[string]any{"fake_repeats": 1}
		if seed.Protocol == "tcp" {
			changes["sequence_overlap"] = 0
		}
		add("Fewer fake packets", changes)
		if seed.Protocol == "tcp" {
			add("No sequence overlap", map[string]any{"sequence_overlap": 0})
			add("Split only", map[string]any{"tcp_strategy": "split", "split_positions": "1", "sequence_overlap": 0})
			add("Reorder only", map[string]any{"tcp_strategy": "disorder", "split_positions": "1", "sequence_overlap": 0})
		}
		return result
	}
	for _, repeats := range []int{1, 3, 6, 10} {
		changes := map[string]any{"fake_repeats": repeats}
		if seed.Protocol == "tcp" {
			changes["tcp_strategy"] = "fake-split"
		}
		add("Fake repeats - "+strconv.Itoa(repeats), changes)
	}
	for _, delta := range []int{-1, -2, -3} {
		changes := map[string]any{"auto_ttl": true, "auto_ttl_delta": delta}
		if seed.Protocol == "tcp" {
			changes["tcp_strategy"] = "fake-split"
		}
		add("Automatic TTL - "+strconv.Itoa(delta), changes)
	}
	if seed.Protocol == "tcp" {
		for _, fooling := range []string{"badseq", "md5", "badsum"} {
			add("Fake protection - "+fooling, map[string]any{"fake_fooling": fooling, "tcp_strategy": "fake-split"})
		}
		for _, position := range []string{"1", "2", "host", "midsld", "1,midsld"} {
			add("Split position - "+position, map[string]any{"split_positions": position, "tcp_strategy": "fake-split"})
		}
		add("Alternative fake server name", map[string]any{"fake_sni": "www.google.com", "tcp_strategy": "fake-split"})
	}
	return result
}

func candidateSignature(preset Preset) string {
	c := presetConfig(defaultConfig(), preset)
	c.Networks, c.Domains = []string{"203.0.113.1/32"}, nil
	c.TCPPorts, c.UDPPorts = []int{443}, []int{443}
	if preset.Protocol == "tcp" {
		c.UDP = "off"
		c.Applications = nil
	}
	if preset.Protocol == "udp" {
		c.TCP = "off"
		c.Applications = nil
	}
	if preset.Protocol == "stun" {
		c.TCP, c.UDP = "off", "off"
	}
	return strings.Join(workerArgs(c, Resources{Queue: 200, Mark: "0x40000000"}, "/worker", 62000), " ")
}
