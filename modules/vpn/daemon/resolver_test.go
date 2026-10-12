// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
	"time"
)

func TestResolverLeaseIsIndependentOfFusion(t *testing.T) {
	broker, values := fixtureBroker(t)
	oldRunDir := runDir
	runDir = t.TempDir()
	t.Cleanup(func() {
		runDir = oldRunDir
	})
	previous := values["vpnc_clientlist"]
	original := runner
	runner = func(name string, args ...string) (string, error) {
		if name == "ip" && strings.Join(args, " ") == "-4 address show" {
			return "1: br0: UP\n    inet 192.168.1.1/24 scope global br0\n2: br55: UP\n    inet 192.168.52.1/24 scope global br55", nil
		}

		return original(name, args...)
	}

	response, err := broker.handle(Request{API: 1, Client: "filter", Operation: "resolver-register", Resolver: &ResolverRegistration{Port: 53535}})
	if err != nil || len(response.Resolver.Networks) != 2 || len(broker.registry.Items) != 0 || values["vpnc_clientlist"] != previous {
		t.Fatalf("%+v %v", response, err)
	}

	if _, err := broker.handle(Request{API: 1, Client: "other-filter", Operation: "resolver-register", Resolver: &ResolverRegistration{Port: 53536}}); err == nil {
		t.Fatal("competing capture accepted")
	}

	broker.resolver.Heartbeat = time.Now().Add(-13 * time.Second)
	broker.reconcileResolver()
	if broker.resolver != nil {
		t.Fatal("expired filter capture retained")
	}
}
