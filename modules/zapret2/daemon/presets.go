// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "encoding/json"

// These bounded starting points use the techniques documented by zapret2.
// No external command lines, Lua code or downloaded strategy files are accepted.
type Preset struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Protocol    string         `json:"protocol"`
	Settings    map[string]any `json:"settings"`
}

func presets() []Preset {
	return []Preset{
		{"split", "Simple split", "Split the handshake after its first byte.", "tcp", map[string]any{"tcp_strategy": "split", "split_positions": "1"}},
		{"hostname", "Hostname split", "Split within the server name.", "tcp", map[string]any{"tcp_strategy": "split", "split_positions": "1,midsld"}},
		{"disorder", "Reordered segments", "Send handshake segments out of order.", "tcp", map[string]any{"tcp_strategy": "disorder", "split_positions": "1,midsld"}},
		{"fake-sequence", "Fake and split", "Combine fake packets with an out-of-window TCP sequence and splitting.", "tcp", map[string]any{"tcp_strategy": "fake-split", "split_positions": "1,midsld", "fake_fooling": "badseq", "auto_ttl": true}},
		{"fake-md5", "Fake and reorder", "Combine TCP MD5 fake packets with reordered segments.", "tcp", map[string]any{"tcp_strategy": "fake-disorder", "split_positions": "1,midsld", "fake_fooling": "md5", "auto_ttl": true}},
		{"interleave", "Interleaved segments", "Alternate fake and real segments with TCP sequence protection.", "tcp", map[string]any{"tcp_strategy": "fakedsplit", "split_positions": "midsld", "fake_fooling": "badseq", "auto_ttl": true}},
		{"hostfake", "Fake hostname", "Split the handshake around a fake server name.", "tcp", map[string]any{"tcp_strategy": "hostfakesplit", "fake_fooling": "md5", "auto_ttl": true}},
		{"quic", "QUIC fake packets", "Send repeated fake QUIC Initial packets. Not tested by the HTTPS scanner.", "udp", map[string]any{"udp_strategy": "fake-quic", "fake_repeats": 6, "auto_ttl": true}},
	}
}

func presetConfig(base Config, preset Preset) Config {
	// Reset all tuning for reproducible tests, preserving selectors and ports.
	next := defaultConfig()
	next.Revision, next.Enabled = base.Revision, base.Enabled
	next.Networks, next.Domains = base.Networks, base.Domains
	next.TCPPorts, next.UDPPorts = base.TCPPorts, base.UDPPorts
	data, _ := json.Marshal(preset.Settings)
	_ = json.Unmarshal(data, &next)
	return next
}

func presetCatalog() []Preset {
	catalog := presets()
	for i := range catalog {
		c := presetConfig(defaultConfig(), catalog[i])
		data, _ := json.Marshal(c)
		settings := map[string]any{}
		_ = json.Unmarshal(data, &settings)
		for _, key := range []string{"revision", "enabled", "networks", "domains", "tcp_ports", "udp_ports"} {
			delete(settings, key)
		}
		if catalog[i].Protocol == "tcp" {
			delete(settings, "udp_strategy")
		} else {
			// QUIC shares fake-packet timing with TCP. Show this effect in the form.
			for _, key := range []string{"tcp_strategy", "split_position", "split_positions", "sequence_overlap", "fake_fooling", "fake_sni", "packet_limit"} {
				delete(settings, key)
			}
		}
		catalog[i].Settings = settings
	}
	return catalog
}
