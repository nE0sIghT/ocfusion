// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func destinationFixture(t *testing.T) (*Broker, *[]string, *[]string) {
	t.Helper()
	oldRun, oldInput, oldDir := runner, destinationInput, runDir
	runDir = t.TempDir()
	commands := []string{}
	batches := []string{}
	runner = func(name string, args ...string) (string, error) {
		text := name + " " + strings.Join(args, " ")
		commands = append(commands, text)
		if text == "nvram get lan_ifname" {
			return "br0", nil
		}
		return "", nil
	}
	destinationInput = func(tool, input string, args ...string) error {
		batches = append(batches, tool+" "+strings.Join(args, " ")+"\n"+input)
		return nil
	}
	t.Cleanup(func() {
		runner, destinationInput, runDir = oldRun, oldInput, oldDir
	})
	return newBroker(Connections{Schema: 1, Items: map[string]Manifest{}}), &commands, &batches
}

func TestDestinationVPNFailureModesAndFamilies(t *testing.T) {
	_, commands, _ := destinationFixture(t)
	route := &DestinationRoute{Mark: "0x10000/0x10000", Table: "52400", Priority: "3"}
	if err := route.reconcile(EgressTarget{Ready: true, Interface: "tun11", Address: "192.0.2.3"}, false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*commands, "\n")
	for _, required := range []string{"ip -6 route replace unreachable default table 52400", "ip -4 route replace default dev tun11 src 192.0.2.3", "fwmark 0x10000/0x10000 lookup 52400 priority 3"} {
		if !strings.Contains(joined, required) {
			t.Fatal("missing route isolation", required, joined)
		}
	}
	*commands = nil
	if err := route.reconcile(EgressTarget{}, true); err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(*commands, "\n")
	if !strings.Contains(joined, "-4 route replace throw default") || !strings.Contains(joined, "-6 route replace throw default") || !strings.Contains(joined, "-4 route del default table 52400 metric 10") {
		t.Fatal("WAN fallback incomplete", joined)
	}
	*commands = nil
	if err := route.reconcile(EgressTarget{}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(*commands, "\n"), "route replace throw") {
		t.Fatal("block mode permitted WAN")
	}
}

func TestDestinationChainOrderOwnershipAndNoLocalCapture(t *testing.T) {
	_, _, batches := destinationFixture(t)
	lease := &DestinationLease{Owner: "consumer", Heartbeat: time.Now(), Routes: map[string]*DestinationRoute{"vpn": {Mark: "0x10000/0x10000", Table: "52400", Priority: "2"}}, Policies: []DestinationPolicy{
		{ID: "normal", Action: "normal", Networks: []string{"203.0.113.1/32"}},
		{ID: "vpn", Action: "vpn", Target: "managed:owner", Failure: "block", Networks: []string{"203.0.113.0/24"}},
	}}
	cache := DestinationDNS{answers: map[string]DestinationAnswer{}}
	if err := lease.install(&cache, [][]string{{"-i", "br55", "-j", "RETURN"}}, []string{"br0", "br55"}); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(*batches, "\n")
	if !strings.Contains(text, "swap "+lease.set("vpn", 4)+"t "+lease.set("vpn", 4)) {
		t.Fatal("sets not swapped", text)
	}
	chain := ""
	for _, batch := range *batches {
		if strings.HasPrefix(batch, "iptables-restore ") {
			chain = batch
		}
	}
	local := strings.Index(chain, "--dst-type LOCAL -j RETURN")
	native := strings.Index(chain, "-i br55 -j RETURN")
	first := strings.Index(chain, lease.set("normal", 4)+" dst -j RETURN")
	second := strings.Index(chain, lease.set("vpn", 4)+" dst -j MARK")
	if local < 0 || native < local || first < native || second < first {
		t.Fatal("first-match/native precedence broken", chain)
	}
	if strings.Contains(text, "OUTPUT") || strings.Contains(text, "-F PREROUTING") || strings.Contains(text, "-F FORWARD") {
		t.Fatal("unexpected interception or broad reset", text)
	}
	if !strings.Contains(chain, "--ctdir ORIGINAL --ctstate ESTABLISHED,RELATED -m connmark --mark 0x10000/0x10000") || !strings.Contains(chain, "CONNMARK --restore-mark --nfmask 0x10000 --ctmask 0x10000") {
		t.Fatal("existing connections are not pinned with owned bits", chain)
	}
	count := len(*batches)
	if err := lease.install(&cache, [][]string{{"-i", "br55", "-j", "RETURN"}}, []string{"br0", "br55"}); err != nil || len(*batches) != count {
		t.Fatal("unchanged policy rebuilt sets/chains", err)
	}
}

func TestDestinationExcludesNativeAssignmentsEvenWhenDown(t *testing.T) {
	_, _, _ = destinationFixture(t)
	baseRunner := runner
	runner = func(name string, args ...string) (string, error) {
		if name == "nvram" && len(args) == 2 {
			switch args[1] {
			case "sdn_rl":
				return "<4>VPN>1>12>4>2>7>0>0>0>0>0>0>0>1>0", nil
			case "subnet_rl":
				return "<4>br55>192.0.2.1>255.255.255.0", nil
			case "vpnc_dev_policy_list":
				return "<1>192.0.2.20>>3>", nil
			}
		}
		return baseRunner(name, args...)
	}
	exclusions, err := destinationExclusions()
	if err != nil || len(exclusions) != 2 || strings.Join(exclusions[0], " ") != "-i br55 -j RETURN" || strings.Join(exclusions[1], " ") != "-s 192.0.2.20 -j RETURN" {
		t.Fatal(exclusions, err)
	}
}

func TestDestinationExpiryDisablesConfiguredWANFallback(t *testing.T) {
	broker, commands, _ := destinationFixture(t)
	broker.destinations = map[string]*DestinationLease{"consumer": {Owner: "consumer", Heartbeat: time.Now().Add(-time.Minute), Policies: []DestinationPolicy{{ID: "one", Action: "vpn", Target: "native:2:012345abcdef", Failure: "wan", Networks: []string{"203.0.113.1/32"}}}, Routes: map[string]*DestinationRoute{"one": {Mark: "0x10000/0x10000", Table: "52400", Priority: "2"}}}}
	if err := broker.reconcileDestinations(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(*commands, "\n"), "route replace throw") {
		t.Fatal("expired lease fell back to WAN")
	}
	if broker.destinations["consumer"].State[0].State != "BLOCKED" {
		t.Fatal("expired lease status was not blocked")
	}
}

func TestDestinationRecoveryRejectsUnownedResources(t *testing.T) {
	broker, _, _ := destinationFixture(t)
	lease := &DestinationLease{Owner: "consumer", Policies: []DestinationPolicy{{ID: "one", Action: "vpn", Target: "managed:owner", Failure: "block"}}, Routes: map[string]*DestinationRoute{"one": {Mark: "0x10000/0x10000", Table: "main", Priority: "2"}}}
	if err := saveJSON(filepath.Join(runDir, "destinations.json"), map[string]*DestinationLease{"consumer": lease}); err != nil {
		t.Fatal(err)
	}
	if broker.recoverDestinations() == nil {
		t.Fatal("foreign routing table accepted")
	}
}

func TestDestinationDNSUsesTTLAndKeepsLastGood(t *testing.T) {
	_, _, _ = destinationFixture(t)
	oldLookup := lookupDestination
	t.Cleanup(func() {
		lookupDestination = oldLookup
	})
	lookupDestination = func(ctx context.Context, host string) (DestinationAnswer, error) {
		return DestinationAnswer{Addresses: []string{"203.0.113.1/32"}, Updated: time.Now(), Next: time.Now().Add(2 * time.Minute)}, nil
	}
	cache := &DestinationDNS{}
	cache.refresh([]string{"example.com"})
	<-cache.done
	good := cache.addresses("example.com")
	if len(good.Addresses) != 1 || time.Until(good.Next) < time.Minute {
		t.Fatal(good)
	}
	lookupDestination = func(ctx context.Context, host string) (DestinationAnswer, error) {
		return DestinationAnswer{}, errors.New("temporary DNS failure")
	}
	cache.mu.Lock()
	entry := cache.answers["example.com"]
	entry.Next = time.Now().Add(-time.Second)
	cache.answers["example.com"] = entry
	cache.mu.Unlock()
	cache.refresh([]string{"example.com"})
	<-cache.done
	stale := cache.addresses("example.com")
	if !stale.Failed || len(stale.Addresses) != 1 || stale.Addresses[0] != good.Addresses[0] {
		t.Fatal("last-good addresses discarded", stale)
	}
}

func TestDNSQueryTTLAndWrongTransaction(t *testing.T) {
	_, _, _ = destinationFixture(t)
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	oldServers := destinationNameservers
	destinationNameservers = func() []string { return []string{server.LocalAddr().String()} }
	t.Cleanup(func() {
		destinationNameservers = oldServers
	})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for index := 0; index < 3; index++ {
			buffer := make([]byte, 4096)
			count, peer, err := server.ReadFrom(buffer)
			if err != nil {
				return
			}
			var query dnsmessage.Message
			if query.Unpack(buffer[:count]) != nil {
				return
			}
			query.Response = true
			if index == 2 {
				query.ID++
			}
			if query.Questions[0].Type == dnsmessage.TypeA {
				query.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 5}}}}
			}
			wire, _ := query.Pack()
			server.WriteTo(wire, peer)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	answer, err := resolveDestination(ctx, "example.com")
	if err != nil || len(answer.Addresses) != 1 || time.Until(answer.Next) < 119*time.Second {
		t.Fatal(answer, err)
	}
	if _, err := dnsExchange(ctx, server.LocalAddr().String(), "example.com", dnsmessage.TypeA); err == nil {
		t.Fatal("mismatched DNS transaction accepted")
	}
	<-finished
}

func TestDestinationJournalPrecedesFirewall(t *testing.T) {
	broker, _, _ := destinationFixture(t)
	destinationInput = func(tool, input string, args ...string) error {
		if _, err := os.Stat(filepath.Join(runDir, "destinations.json")); err != nil {
			t.Fatal("kernel update preceded recovery journal", err)
		}
		return nil
	}
	request := Request{API: 1, Client: "consumer", Operation: "destination-register", Destinations: []DestinationPolicy{{ID: "one", Action: "vpn", Target: "native:2:012345abcdef", Failure: "block", Networks: []string{"203.0.113.0/24"}}}}
	reply, err := broker.handle(request)
	if err != nil || !reply.OK || reply.DestinationMask == "0x0" {
		t.Fatal(reply, err)
	}
}

func TestDestinationAllocationFailureLeavesNoPartialLease(t *testing.T) {
	broker, commands, batches := destinationFixture(t)
	baseRunner := runner
	runner = func(tool string, args ...string) (string, error) {
		if tool == "iptables" && strings.Join(args, " ") == "-t mangle -S" {
			return "-A PREROUTING -j MARK --set-xmark 0x0/0xffffffff", nil
		}
		return baseRunner(tool, args...)
	}
	request := Request{API: 1, Client: "consumer", Operation: "destination-register", Destinations: []DestinationPolicy{{ID: "one", Action: "vpn", Target: "managed:one", Failure: "block", Networks: []string{"203.0.113.0/24"}}}}
	if _, err := broker.handle(request); err == nil {
		t.Fatal("resource exhaustion was ignored")
	}
	if len(broker.destinations) != 0 || len(*batches) != 0 {
		t.Fatal("failed allocation left a partially valid policy")
	}
	for _, command := range *commands {
		if strings.Contains(command, "route replace") || strings.Contains(command, "rule add") {
			t.Fatal("failed allocation changed kernel state", command)
		}
	}
}

func TestDestinationCleanupLeavesForeignRules(t *testing.T) {
	_, commands, _ := destinationFixture(t)
	baseRunner := runner
	runner = func(tool string, args ...string) (string, error) {
		if strings.Join(args, " ") == "-t mangle -S PREROUTING" {
			return "-A PREROUTING -i br0 -m comment --comment \"ocfusion:destination:consumer\" -j owned\n-A PREROUTING -m comment --comment \"foreign-owner\" -j ACCEPT", nil
		}
		return baseRunner(tool, args...)
	}
	if err := removeDestinationComment("iptables", "mangle", "PREROUTING", "ocfusion:destination:consumer"); err != nil {
		t.Fatal(err)
	}
	if len(*commands) != 1 || !strings.Contains((*commands)[0], "-D PREROUTING") || strings.Contains((*commands)[0], "foreign-owner") {
		t.Fatal("cleanup changed foreign traffic", *commands)
	}
}

func TestPartialDNSFailureRetainsOnlyFailedFamily(t *testing.T) {
	_, _, _ = destinationFixture(t)
	oldLookup := lookupDestination
	t.Cleanup(func() {
		lookupDestination = oldLookup
	})
	cache := &DestinationDNS{answers: map[string]DestinationAnswer{"example.com": {Addresses: []string{"203.0.113.1/32", "2001:db8::1/128"}, Updated: time.Now().Add(-time.Hour)}}}
	lookupDestination = func(ctx context.Context, host string) (DestinationAnswer, error) {
		return DestinationAnswer{Addresses: []string{"203.0.113.2/32"}, Missing: []int{6}, Failed: true, Updated: time.Now(), Next: time.Now().Add(time.Minute)}, nil
	}
	cache.refresh([]string{"example.com"})
	<-cache.done
	answer := cache.addresses("example.com")
	text := strings.Join(answer.Addresses, ",")
	if len(answer.Addresses) != 2 || !strings.Contains(text, "203.0.113.2/32") || !strings.Contains(text, "2001:db8::1/128") || !answer.Retained {
		t.Fatal("partial failure discarded protected addresses or new A reply", answer)
	}
}
