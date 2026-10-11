// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExpiredClientClosesTunnelAndKeepsTerminalRoute(t *testing.T) {
	original := runner
	t.Cleanup(func() {
		runner = original
	})

	manifest := Manifest{Schema: 1, Interface: "ocvpn0", Unit: 4, Index: 7, Table: 51998, Priority: 90, Firmware: "test_102_build"}
	commands := []string{}
	runner = func(name string, args ...string) (string, error) {
		command := name + " " + strings.Join(args, " ")
		commands = append(commands, command)
		switch command {
		case "nvram get firmver":
			return "test", nil
		case "nvram get buildno":
			return "102", nil
		case "nvram get extendno":
			return "build", nil
		case "nvram get vpnc_clientlist":
			return fmt.Sprintf("OCFusion (managed)>OpenVPN>%d>>>0>%d>>>0>0>Web", manifest.Unit, manifest.Index), nil
		}

		return "", nil
	}

	service := &Service{manifest: manifest, owner: "client", event: &Tunnel{Interface: "ocvpn0", Address: "192.168.199.1"}, heartbeat: time.Now().Add(-time.Minute)}
	if err := service.reconcile(true); err == nil {
		t.Fatal("expired client considered active")
	}

	if service.event != nil {
		t.Fatal("expired tunnel retained")
	}

	combined := strings.Join(commands, "\n")
	if !strings.Contains(combined, "ip route replace unreachable default metric 32760 table 51998") || !strings.Contains(combined, "ip route del default dev ocvpn0 metric 10 table 51998") {
		t.Fatal("terminal route or tunnel closure missing", combined)
	}
}

func TestAPIRejectsUnknownVersionAndForeignOwner(t *testing.T) {
	service := &Service{owner: "client"}
	for _, request := range []Request{
		{API: 2, Client: "client", Operation: "state"},
		{API: 1, Client: "other", Operation: "apply"},
		{API: 1, Client: "../client", Operation: "state"},
	} {
		if _, err := service.handle(request); err == nil {
			t.Fatal("accepted invalid request", request)
		}
	}
}
