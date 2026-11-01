// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "strconv"

func quicIPv6Only(method string) bool {
	return method == "hopbyhop-quic" || method == "destopt-quic" || method == "hopbyhop-destopt-quic"
}

func quicStrategy(c Config) []string {
	c = protocolConfig(c, "udp")
	args := []string{"--payload=quic_initial"}
	if quicIPv6Only(c.UDP) {
		args = append(args, "--filter-l3=ipv6")
		options := ":ip6_hopbyhop"
		if c.UDP == "destopt-quic" {
			options = ":ip6_destopt"
		} else if c.UDP == "hopbyhop-destopt-quic" {
			options += ":ip6_destopt"
		}
		return append(args, "--lua-desync=send"+options, "--lua-desync=drop")
	}
	if c.UDP == "fake-quic" || c.UDP == "fake-fragment-quic" {
		options := ""
		if c.UDPProtection == "ttl" {
			options = fakeOptions(c, false)
		}
		args = append(args, "--lua-desync=fake:blob=fake_default_quic"+options+":repeats="+strconv.Itoa(c.Repeats))
	}
	if c.UDP == "fragment-quic" || c.UDP == "fake-fragment-quic" {
		args = append(args, "--lua-desync=send:ipfrag:ipfrag_pos_udp="+strconv.Itoa(c.UDPFragment), "--lua-desync=drop")
	}
	return args
}

// Mirrors the QUIC technique families in pinned zapret2 blockcheck2.d/standard/90-quic.sh.
// Candidate values are bounded data, never external worker arguments or Lua.
func quicCandidates() []Preset {
	result := []Preset{}
	for _, repeats := range []int{1, 2, 5, 10, 20} {
		result = append(result, Preset{ID: "quic-repeat-" + strconv.Itoa(repeats), Name: "QUIC fake packets - " + strconv.Itoa(repeats), Protocol: "udp", Settings: map[string]any{"udp_strategy": "fake-quic", "udp_protection": "none", "fake_repeats": repeats}})
	}
	for _, method := range []string{"fragment-quic", "fake-fragment-quic"} {
		name := "QUIC IP fragmentation"
		if method == "fake-fragment-quic" {
			name = "QUIC fake packets and fragmentation"
		}
		for _, position := range []int{8, 16, 32, 64} {
			result = append(result, Preset{ID: method + "-" + strconv.Itoa(position), Name: name + " - " + strconv.Itoa(position), Protocol: "udp", Settings: map[string]any{"udp_strategy": method, "udp_fragment": position, "udp_protection": "none", "fake_repeats": 1}})
		}
	}
	for _, entry := range []struct{ method, name string }{
		{"hopbyhop-quic", "QUIC IPv6 Hop-by-Hop"},
		{"destopt-quic", "QUIC IPv6 Destination Options"},
		{"hopbyhop-destopt-quic", "QUIC IPv6 extension headers"},
	} {
		result = append(result, Preset{ID: entry.method, Name: entry.name, Protocol: "udp", Settings: map[string]any{"udp_strategy": entry.method}})
	}
	return result
}

func hasIPv6Target(targets []ScanTarget, indices []int) bool {
	for _, index := range indices {
		if targets[index].Family == 6 {
			return true
		}
	}
	return false
}

func refineQUIC(seed Preset, mode string) []Preset {
	result := []Preset{{ID: "baseline", Name: "Without processing", Protocol: "udp"}, seed}
	seen := map[string]bool{candidateSignature(seed): true}
	add := func(next Preset) {
		next = canonicalScanPreset(next)
		next.ID = candidateID(next)
		signature := candidateSignature(next)
		if !seen[signature] && len(result) < scanResultLimit {
			seen[signature] = true
			result = append(result, next)
		}
	}
	base := presetConfig(defaultConfig(), seed)
	if mode == "lighten" {
		base.Repeats = 1
		add(Preset{Name: "Fewer fake packets", Protocol: "udp", Settings: strategyProjection(base)})
		if base.UDP == "fake-fragment-quic" {
			for _, method := range []string{"fake-quic", "fragment-quic"} {
				base.UDP = method
				add(Preset{Name: methodNameQUIC(method), Protocol: "udp", Settings: strategyProjection(base)})
			}
		}
		return result
	}
	for _, candidate := range quicCandidates() {
		if quicIPv6Only(candidate.Settings["udp_strategy"].(string)) != quicIPv6Only(base.UDP) {
			continue
		}
		add(candidate)
	}
	if !quicIPv6Only(base.UDP) {
		for _, delta := range []int{-1, -2, -3} {
			base.UDPProtection, base.AutoTTL, base.AutoDelta = "ttl", true, delta
			add(Preset{Name: "Automatic TTL - " + strconv.Itoa(delta), Protocol: "udp", Settings: strategyProjection(base)})
		}
	}
	return result
}

func methodNameQUIC(method string) string {
	if method == "fragment-quic" {
		return "QUIC IP fragmentation"
	}
	return "QUIC fake packets"
}
