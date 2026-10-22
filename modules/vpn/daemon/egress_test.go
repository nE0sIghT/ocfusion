// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEgressACLStaysWithinLAN(t *testing.T) {
	networks := []Network{{Bridge: "br0", Address: "192.0.2.1", CIDR: "192.0.2.0/24"}}
	for _, allowed := range [][]string{{"192.0.2.8"}, {"192.0.2.128/25"}, nil} {
		reg, err := validateEgress(EgressRegistration{Bridge: "br0", Port: 3128, Allowed: allowed}, networks)
		if err != nil || reg.Address != "192.0.2.1" {
			t.Fatal(reg, err)
		}
	}

	for _, allowed := range [][]string{{"0.0.0.0/0"}, {"192.0.3.1"}, {"192.0.2.1\nallow *"}, {"::1"}} {
		if _, err := validateEgress(EgressRegistration{Bridge: "br0", Port: 3128, Allowed: allowed}, networks); err == nil {
			t.Fatal("accepted", allowed)
		}
	}

	if _, err := validateEgress(EgressRegistration{Bridge: "eth0", Port: 3128}, networks); err == nil {
		t.Fatal("accepted WAN")
	}
}

func egressFixture(t *testing.T) (*EgressLease, *[]string) {
	t.Helper()
	old := runner
	t.Cleanup(func() { runner = old })
	calls := []string{}
	runner = func(tool string, args ...string) (string, error) {
		command := tool + " " + strings.Join(args, " ")
		calls = append(calls, command)
		if strings.Contains(command, " -C ") {
			return "", errors.New("absent")
		}

		if command == "ip -4 route show table 52200" {
			return "default dev tun11 metric 10\nunreachable default metric 32760", nil
		}

		return "", nil
	}

	lease := &EgressLease{Owner: "consumer", Registration: EgressRegistration{UID: 60000, Address: "192.0.2.1", CIDR: "192.0.2.0/24", Bridge: "br0", Port: 3128, Target: "native:7"}, Table: "52200", Interface: "tun11", Heartbeat: time.Now()}
	return lease, &calls
}

func TestEgressTunnelLossBlocksBothFamilies(t *testing.T) {
	lease, calls := egressFixture(t)
	if err := lease.reconcile(nil); err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(*calls, "\n")
	for _, required := range []string{
		"ip -4 route replace unreachable default table 52200 metric 32760",
		"ip -6 route replace unreachable default table 52200 metric 32760",
		"ip -4 route del default table 52200 metric 10",
		"ip -4 rule add from 192.0.2.1/32 to 192.0.2.0/24 ipproto tcp sport 3128 uidrange 60000-60000 lookup 52201 priority 0",
		"ip -4 rule add uidrange 60000-60000 lookup 52200 priority 0",
		"ip -6 rule add uidrange 60000-60000 lookup 52200 priority 0",
		"-i br0 -s 192.0.2.0/24 -d 192.0.2.1 -p tcp --dport 3128 -j ACCEPT",
	} {
		if !strings.Contains(joined, required) {
			t.Fatal("missing", required, joined)
		}
	}

	if lease.Registration.Ready || strings.Contains(joined, "route replace default dev") || strings.Contains(joined, "--uid-owner") {
		t.Fatal("invalid isolation", joined)
	}
}

func TestEgressPolicyRecognitionAndIdempotence(t *testing.T) {
	for _, uid := range []string{"60000", "60000-60000"} {
		line := "0: from all uidrange " + uid + " lookup 52200"
		if !egressRulePresent(line, "60000", "52200") || egressRulePresent(line, "60001", "52200") || egressRulePresent(line, "60000", "52202") {
			t.Fatal(line)
		}
	}

	old := runner
	t.Cleanup(func() { runner = old })
	runner = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), " add ") || strings.Contains(strings.Join(args, " "), " del ") {
			t.Fatal("rewrote live rules", args)
		}

		return "0: from 192.0.2.1 to 192.0.2.0/24 uidrange 60000 lookup 52201\n0: from all uidrange 60000 lookup 52200", nil
	}

	l := EgressLease{Registration: EgressRegistration{UID: 60000}, Table: "52200"}
	if err := l.ensurePolicies(); err != nil {
		t.Fatal(err)
	}
}

func TestEgressExpiredLeaseDoesNotUseLiveVPN(t *testing.T) {
	lease, calls := egressFixture(t)
	lease.Heartbeat = time.Now().Add(-time.Minute)
	if err := lease.reconcile([]EgressTarget{{ID: "native:7", Ready: true, Interface: "tun11", Address: "198.51.100.1"}}); err != nil {
		t.Fatal(err)
	}

	if lease.Registration.Ready || strings.Contains(strings.Join(*calls, "\n"), "route replace default dev") {
		t.Fatal("expired lease acquired tunnel")
	}
}

func TestEgressCatalogRedactsCredentials(t *testing.T) {
	old := runner
	t.Cleanup(func() { runner = old })
	runner = func(tool string, args ...string) (string, error) {
		if tool == "nvram" && len(args) > 1 && args[1] == "vpnc_clientlist" {
			return "Office>OpenVPN>1>private-user>private-password>1>7", nil
		}

		return "", nil
	}

	b := newBroker(Connections{})
	targets := b.egressTargets()
	data, _ := json.Marshal(targets)
	if len(targets) != 1 || !strings.HasPrefix(targets[0].ID, "native:7:") || targets[0].Ready || strings.Contains(string(data), "private-") {
		t.Fatal(string(data))
	}
}

func TestEgressRecoveryRejectsUnsafeUID(t *testing.T) {
	old := runDir
	runDir = t.TempDir()
	t.Cleanup(func() { runDir = old })
	data, _ := json.Marshal(map[string]*EgressLease{"consumer": {Owner: "consumer", Table: "52200", Registration: EgressRegistration{UID: 0, Bridge: "br0", Address: "192.0.2.1", CIDR: "192.0.2.0/24", Port: 3128}}})
	os.WriteFile(filepath.Join(runDir, "egress.json"), data, 0600)
	if err := newBroker(Connections{}).recoverEgress(); err == nil {
		t.Fatal("accepted root as proxy UID")
	}
}

func TestEgressRejectsEarlierWANPolicy(t *testing.T) {
	l := EgressLease{Registration: EgressRegistration{UID: 60000}, Table: "52200"}
	if err := l.checkPriority("0: from all lookup local\n0: from all lookup main"); err == nil {
		t.Fatal("accepted earlier WAN lookup")
	}
	if err := l.checkPriority("0: from all lookup local\n0: from all uidrange 60001 lookup 52202"); err != nil {
		t.Fatal(err)
	}
}
