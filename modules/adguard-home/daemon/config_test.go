// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeBackendsPreserveNetworks(t *testing.T) {
	registration := Registration{Networks: []Network{
		{Bridge: "br0", Address: "192.168.1.1", CIDR: "192.168.1.0/24"},
		{Bridge: "br55", Address: "192.168.52.1", CIDR: "192.168.52.0/24"},
	}}
	config := Config{Clients: []ClientRule{{Address: "192.168.52.10", Filtering: false}}}
	native, _, err := nativeConfig(config, registration)
	if err != nil {
		t.Fatal(err)
	}

	clients := native["clients"].(map[string]any)["persistent"].([]map[string]any)
	if len(clients) != 3 || clients[2]["upstreams"].([]string)[0] != "192.168.52.1" {
		t.Fatal(clients)
	}

	encoded, _ := json.Marshal(native)
	if strings.Contains(string(encoded), "8.8.8.8") || strings.Contains(string(encoded), "1.1.1.1") {
		t.Fatal("introduced unconfigured public resolver")
	}
}

func TestCustomUpstreamPreservesTLSHostname(t *testing.T) {
	native, overrides, err := nativeConfig(Config{Upstreams: []Upstream{{URL: "https://dns.example.org/dns-query", Address: "192.0.2.1"}}}, Registration{Networks: []Network{{Address: "192.168.1.1"}}})
	if err != nil || overrides["dns.example.org"] != "192.0.2.1" {
		t.Fatal(overrides, err)
	}

	if native["dns"].(map[string]any)["upstream_dns"].([]string)[0] != "https://dns.example.org/dns-query" {
		t.Fatal("TLS hostname replaced by IP")
	}
}

func TestRejectUnsafeSettings(t *testing.T) {
	for _, config := range []string{
		`{"upstreams":[{"url":"https://user:password@dns.example"}]}`,
		`{"filters":["http://example.org/filter"]}`,
		`{"clients":[{"address":"invalid"}]}`,
		`{"upstreams":[{"url":"quic://dns.example"}]}`,
		`{"unknown":true}`,
	} {
		if _, err := parseConfig([]byte(config)); err == nil {
			t.Fatal("accepted", config)
		}
	}
}

func TestNativeClientCannotSpanBackends(t *testing.T) {
	registration := Registration{Networks: []Network{{Address: "192.168.1.1", CIDR: "192.168.1.0/24"}, {Address: "192.168.2.1", CIDR: "192.168.2.0/24"}}}
	for _, address := range []string{"192.168.1.0/16", "198.51.100.1"} {
		if _, _, err := nativeConfig(Config{Clients: []ClientRule{{Address: address}}}, registration); err == nil {
			t.Fatal("accepted ambiguous native backend", address)
		}
	}
}
