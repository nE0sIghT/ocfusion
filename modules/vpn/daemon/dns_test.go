// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverNativeDNS(t *testing.T) {
	oldConfig, oldResolv := dnsConfigRoot, dnsResolvRoot
	dnsConfigRoot = t.TempDir()
	dnsResolvRoot = t.TempDir()
	t.Cleanup(func() {
		dnsConfigRoot, dnsResolvRoot = oldConfig, oldResolv
	})

	os.MkdirAll(filepath.Join(dnsConfigRoot, "stubby"), 0700)
	os.WriteFile(filepath.Join(dnsConfigRoot, "dnsmasq.conf"), []byte("server=127.0.1.1#53\nservers-file="+dnsResolvRoot+"/servers\n"), 0600)
	os.WriteFile(filepath.Join(dnsResolvRoot, "servers"), []byte("server=/example.org/192.0.2.1#5353\n"), 0600)
	os.WriteFile(filepath.Join(dnsConfigRoot, "stubby", "stubby-0.yml"), []byte("upstream_recursive_servers:\n  - address_data: 192.0.2.2\n    tls_port: 8853\n    tls_auth_name: resolver.example.org\n"), 0600)

	endpoints, err := discoverDNS()
	if err != nil || len(endpoints) != 3 {
		t.Fatalf("endpoints = %v, error = %v", endpoints, err)
	}

	for _, endpoint := range endpoints {
		if endpoint.Address == "127.0.1.1" || endpoint.Port == 53 {
			t.Fatal("local hop or custom port parsed incorrectly", endpoint)
		}
	}
}

func TestDNSMarkPreservesNativeMasks(t *testing.T) {
	mark := unusedDNSMark("--set-xmark 0x1/0x7\nfwmark 0x10000000/0x10000000")
	if mark != "0x8000000/0x8000000" {
		t.Fatal(mark)
	}

	if unusedDNSMark("--mark 0x0/0xffffffff") != "" {
		t.Fatal("allocated a bit already covered by a foreign mask")
	}
}

func TestDNSSelectionOwnershipAndFallback(t *testing.T) {
	broker, _ := fixtureBroker(t)
	_, err := broker.handle(Request{API: 1, Client: "openconnect", Profile: "office", Operation: "dns-select", PreferDNS: true})
	if err != nil {
		t.Fatal(err)
	}

	response, err := broker.handle(Request{API: 1, Client: "openconnect", Operation: "dns-state"})
	if err != nil || response.DNSStatus.Path != "WAN" || response.DNSStatus.Policy.Profile != "office" {
		t.Fatalf("%+v %v", response, err)
	}

	_, err = broker.handle(Request{API: 1, Client: "another", Profile: "office", Operation: "dns-select"})
	if err == nil {
		t.Fatal("another owner cleared policy")
	}
}

func TestDNSConntrackDeletionIsLocalAndScoped(t *testing.T) {
	oldRunner := runner
	t.Cleanup(func() {
		runner = oldRunner
	})

	calls := []string{}
	runner = func(tool string, args ...string) (string, error) {
		if tool == "ip" {
			return "inet 192.0.2.10/24 scope global eth0", nil
		}

		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}

	deleteLocalDNSFlows(DNSEndpoint{Address: "198.51.100.1", Protocol: "tcp", Port: 853})
	if len(calls) != 1 || !strings.Contains(calls[0], "--orig-src 192.0.2.10 --orig-dst 198.51.100.1 --dport 853") {
		t.Fatal(calls)
	}
}

func TestDNSCleanupFailureIsRetried(t *testing.T) {
	previous := runner
	t.Cleanup(func() { runner = previous })
	fail := true
	runner = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "-D OUTPUT") && fail {
			return "", errors.New("fixture deletion failed")
		}
		return "", nil
	}

	dns := DNSRouter{active: "ocvpn0", mark: "0x10000000/0x10000000", endpoints: []DNSEndpoint{{Address: "192.0.2.1", Port: 53, Protocol: "udp"}}}
	if dns.reset() == nil || dns.status.Path != "ERROR" || dns.active == "" {
		t.Fatal("cleanup failure lost retry state")
	}
	fail = false
	if dns.reset() != nil || dns.active != "" {
		t.Fatal("cleanup retry failed")
	}
}

func TestDNSMarkExcludesConnectionRestoreMasks(t *testing.T) {
	if unusedDNSMark("-j CONNMARK --restore-mark") != "" {
		t.Fatal("unmasked connection restore can erase socket marks")
	}

	if unusedDNSMark("-j CONNMARK --restore-mark --nfmask 0x7 --ctmask 0x7") == "" {
		t.Fatal("limited restore should leave high bits available")
	}
}

func TestDNSRoutingCleanupFailuresRetainRecoveryState(t *testing.T) {
	for _, resource := range []string{"rule", "route"} {
		t.Run(resource, func(t *testing.T) {
			previous := runner
			t.Cleanup(func() { runner = previous })
			fail := true
			runner = func(tool string, args ...string) (string, error) {
				call := strings.Join(args, " ")
				if tool == "ip" && strings.HasPrefix(call, resource+" del ") && fail {
					return "", errors.New("fixture deletion failed")
				}

				if tool == "ip" && fail {
					switch call {
					case "rule show":
						return "1: from all fwmark 0x10000000/0x10000000 lookup 52100", nil
					case "route show table 52100":
						return "default dev ocvpn0 scope link", nil
					}
				}

				return "", nil
			}

			dns := DNSRouter{active: "ocvpn0", mark: "0x10000000/0x10000000", table: "52100", priority: "1"}
			if dns.reset() == nil || dns.status.Path != "ERROR" || dns.active == "" {
				t.Fatal("failed routing cleanup lost retry state")
			}

			fail = false
			if dns.reset() != nil || dns.active != "" {
				t.Fatal("routing cleanup retry failed")
			}
		})
	}
}

func TestDNSRoutingCleanupAcceptsAlreadyAbsentResources(t *testing.T) {
	previous := runner
	t.Cleanup(func() { runner = previous })
	runner = func(tool string, args ...string) (string, error) {
		if tool == "ip" && strings.Contains(strings.Join(args, " "), " del ") {
			return "", errors.New("already absent")
		}

		return "", nil
	}

	dns := DNSRouter{active: "ocvpn0", mark: "0x10000000/0x10000000", table: "52100", priority: "1"}
	if err := dns.reset(); err != nil || dns.active != "" {
		t.Fatal("absent routing resources prevented idempotent cleanup", err)
	}
}
