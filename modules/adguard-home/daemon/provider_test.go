// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "testing"

func TestDNSPathRequiresConfirmedProviderState(t *testing.T) {
	for _, test := range []struct {
		name string
		ok   bool
		path string
		want string
	}{
		{"VPN", true, "VPN", "VPN"},
		{"WAN", true, "WAN", "WAN"},
		{"unavailable", false, "", "UNKNOWN"},
		{"rejected", false, "WAN", "UNKNOWN"},
		{"missing", true, "", "UNKNOWN"},
		{"invalid", true, "other", "UNKNOWN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := ProviderResponse{OK: test.ok}
			response.DNS.Path = test.path
			if got := response.DNSPath(); got != test.want {
				t.Fatalf("path = %q, want %q", got, test.want)
			}
		})
	}
}
