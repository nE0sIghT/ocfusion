// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "testing"

func TestEgressDemandAllowsRouterOnlyConnection(t *testing.T) {
	previous := vpnCall
	t.Cleanup(func() { vpnCall = previous })
	vpnCall = func(request vpnRequest) (vpnResponse, error) {
		if request.Operation != "dns-state" {
			t.Fatal(request.Operation)
		}
		return vpnResponse{OK: true, EgressDemand: true}, nil
	}
	if !dnsOnlySelected() {
		t.Fatal("ignored router egress consumer")
	}
	vpnCall = func(request vpnRequest) (vpnResponse, error) {
		return vpnResponse{OK: true}, nil
	}
	if dnsOnlySelected() {
		t.Fatal("invented router demand")
	}
}
