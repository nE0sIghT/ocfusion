// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func fixtureSDN(i, bridge, ip string) (string, string) {
	return "<" + i + ">VPN>1>12>" + i + ">2>5>0>0>0>0>0>0>0>1>0>0>0>0>WEB", "<" + i + ">" + bridge + ">" + ip + ">255.255.255.0>1>192.168.77.10>192.168.77.200>86400>>>>0>0>0>0"
}

func TestWebBindingsAreAuthoritative(t *testing.T) {
	s, a := fixtureSDN("3", "br3", "192.168.77.1")
	s2, a2 := fixtureSDN("4", "br4", "192.168.78.1")
	ns, e := discover(s+s2, a+a2, 5)
	if e != nil || len(ns) != 2 {
		t.Fatalf("multiple web networks: %+v %v", ns, e)
	}

	if ns[0].CIDR != "192.168.77.0/24" {
		t.Fatal(ns)
	}
	// User removes the assignment: no manual/manifest binding must put it back.

	ns, e = discover(strings.ReplaceAll(s, ">2>5>", ">2>0>"), a, 5)
	if e != nil || len(ns) != 0 {
		t.Fatalf("removed web binding: %+v %v", ns, e)
	}

	for _, bad := range []string{strings.Replace(s, ">1>0>0>0>0>WEB", ">0>0>0>0>0>WEB", 1)} {
		if _, e = discover(bad, a, 5); e == nil {
			t.Fatal("missing kill switch accepted")
		}
	}

	_, e = discover(s, strings.Replace(a, "br3", "br0", 1), 5)
	if e == nil {
		t.Fatal("shared main bridge accepted")
	}

	f := strings.Split(strings.TrimPrefix(a, "<"), ">")
	f[14] = "1"
	_, e = discover(s, "<"+strings.Join(f, ">"), 5)
	if e == nil {
		t.Fatal("IPv6-enabled SDN accepted")
	}
}

func TestTerminalRoutePreventsFallback(t *testing.T) {
	old := runner
	defer func() {
		runner = old
	}()

	var calls []string
	runner = func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))

		return "", nil
	}
	m := Manifest{Index: 5, Table: 51998, Priority: 90}
	if e := closeTunnel(m); e != nil {
		t.Fatal(e)
	}

	if !strings.Contains(calls[0], "replace unreachable default metric 32760 table 51998") {
		t.Fatal(calls)
	}

	for _, c := range calls {
		if strings.Contains(c, "flush") {
			t.Fatal("global/table flush is forbidden", c)
		}
	}
}

func TestNoRuleDuplication(t *testing.T) {
	old := runner
	defer func() {
		runner = old
	}()

	adds := 0
	runner = func(n string, a ...string) (string, error) {
		if strings.Join(a, " ") == "rule show" {
			return "90: from all iif br3 lookup 51998", nil
		}

		adds++

		return "", nil
	}
	if e := routeRule(Manifest{Table: 51998, Priority: 90}, Network{Bridge: "br3"}, "add"); e != nil || adds != 0 {
		t.Fatal(e, adds)
	}
}

func TestProtectOnlyOwnedSDN(t *testing.T) {
	owned, sub := fixtureSDN("3", "br3", "192.168.77.1")
	f := strings.Split(strings.TrimPrefix(owned, "<"), ">")
	f[14] = "0"
	owned = "<" + strings.Join(f, ">")
	other := "<4>unrelated>1>14>4>7>6>0>0>0>0>0>0>0>0>0>extra-field"
	got, changed, e := protectiveSDN(owned+other, 5)
	if e != nil || len(changed) != 1 || changed[0] != "3" {
		t.Fatal(got, changed, e)
	}

	if !strings.HasSuffix(got, other) {
		t.Fatal("unrelated SDN modified")
	}

	if _, e = discover(got, sub, 5); e != nil {
		t.Fatal(e)
	}

	twice, changes, e := protectiveSDN(got, 5)
	if e != nil || twice != got || len(changes) != 0 {
		t.Fatal("non-idempotent native protection")
	}
}

func TestAddressChangeRemovesStaleTunnelAddress(t *testing.T) {
	old := runner
	defer func() {
		runner = old
	}()

	var changes []string
	runner = func(n string, a ...string) (string, error) {
		s := strings.Join(a, " ")
		if strings.Contains(s, "address show") {
			return "inet 10.2.3.4/32 scope global ocvpn0", nil
		}

		changes = append(changes, s)

		return "", nil
	}
	if e := setTunAddress(Manifest{Interface: "ocvpn0"}, net.ParseIP("10.2.3.5")); e != nil {
		t.Fatal(e)
	}

	if len(changes) != 2 || changes[0] != "-4 address flush dev ocvpn0" || changes[1] != "address add 10.2.3.5/32 dev ocvpn0" {
		t.Fatal(changes)
	}

	changes = nil
	if e := setTunAddress(Manifest{Interface: "ocvpn0"}, net.ParseIP("10.2.3.4")); e != nil || len(changes) != 0 {
		t.Fatal("unchanged address mutated", e, changes)
	}
}

func TestStockLegacyXtables(t *testing.T) {
	previous := runner
	defer func() {
		runner = previous
	}()

	for _, tool := range []string{"iptables", "ip6tables"} {
		for _, table := range []string{"filter", "nat", "mangle"} {
			calls := 0
			runner = func(name string, args ...string) (string, error) {
				calls++
				if name != tool {
					t.Fatalf("unexpected tool: %s", name)
				}

				for _, arg := range args {
					if arg == "-w" || arg == "--wait" {
						t.Fatal("unsupported stock option")
					}
				}

				if strings.Contains(strings.Join(args, " "), "-C FORWARD") {
					return "", errors.New("rule absent")
				}

				return "", nil
			}
			if e := xt(tool, table, "add", "FORWARD", "-i", "br55", "-j", "REJECT"); e != nil {
				t.Fatal(e)
			}

			if calls != 2 {
				t.Fatalf("check/insert calls: %d", calls)
			}
		}
	}

	runner = func(name string, args ...string) (string, error) {
		return "stock extension diagnostic", errors.New("failed")
	}
	e := xt("iptables", "filter", "add", "FORWARD", "-j", "REJECT")
	if e == nil || !strings.Contains(e.Error(), "stock extension diagnostic") {
		t.Fatal("lost firewall diagnostic")
	}
}

func TestKernelCommentBootstrap(t *testing.T) {
	old := runner
	defer func() {
		runner = old
	}()

	loaded := false
	calls := 0
	read := func(path string) ([]byte, error) {
		if loaded {
			return []byte("tcp\ncomment\nudp\n"), nil
		}

		return []byte("tcp\nconntrack\n"), nil
	}
	runner = func(name string, args ...string) (string, error) {
		calls++
		if name != "/sbin/modprobe" || len(args) != 1 || args[0] != "xt_comment" {
			t.Fatal("unexpected bootstrap")
		}

		loaded = true

		return "", nil
	}
	if e := ensureComment(read); e != nil {
		t.Fatal(e)
	}

	if e := ensureComment(read); e != nil {
		t.Fatal(e)
	}

	if calls != 1 {
		t.Fatal("non-idempotent module load")
	}

	loaded = false
	runner = func(string, ...string) (string, error) {
		return "", nil
	}
	if e := ensureComment(read); e == nil {
		t.Fatal("accepted unregistered module")
	}

	runner = func(string, ...string) (string, error) {
		return "module missing", errors.New("failed")
	}
	if e := ensureComment(read); e == nil || !strings.Contains(e.Error(), "module missing") {
		t.Fatal("lost module diagnostic")
	}
}

func TestRulesUseOnlyConnectionResources(t *testing.T) {
	previous := runner
	defer func() { runner = previous }()
	calls := []string{}
	runner = func(name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "-C ") {
			return "", errors.New("missing rule")
		}
		calls = append(calls, name+" "+strings.Join(args, " "))

		return "", nil
	}
	manifest := Manifest{ID: "connection-8", Interface: "ocvpn1", Index: 8, Table: 51999, Priority: 91}
	network := Network{Bridge: "br56", CIDR: "192.168.53.0/24"}
	for _, err := range []error{guard(manifest, network, "add"), forwarding(manifest, network, "add"), closeTunnel(manifest)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	commands := strings.Join(calls, "\n")
	if strings.Contains(commands, "ocvpn0") || strings.Contains(commands, "51998") || strings.Contains(commands, "vpnc7_") {
		t.Fatal("other connection resources used", commands)
	}
	if !strings.Contains(commands, "ocfusion:connection-8:") || !strings.Contains(commands, "dev ocvpn1") || !strings.Contains(commands, "table 51999") {
		t.Fatal("connection-specific ownership missing", commands)
	}
}

func TestRemoveDNSRedirectsPreservesOtherOwners(t *testing.T) {
	previous := runner
	t.Cleanup(func() { runner = previous })
	var deleted []string
	runner = func(name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "-S PREROUTING") {
			return "-A PREROUTING -i br55 -p udp --dport 53 -m comment --comment \"ocfusion:dns\" -j DNAT --to-destination 10.0.0.53\n" +
				"-A PREROUTING -i br55 -p tcp --dport 53 -m comment --comment ocfusion:dns -j DNAT --to-destination 10.0.0.53\n" +
				"-A PREROUTING -p udp --dport 53 -m comment --comment ocfusion:connection-9:dns -j DNAT --to-destination 10.0.0.54\n" +
				"-A PREROUTING -p udp --dport 53 -m comment --comment ocfusion:resolver -j REDIRECT --to-ports 53535", nil
		}

		deleted = append(deleted, name+" "+strings.Join(args, " "))

		return "", nil
	}

	if err := removeDNSRedirects(Manifest{Interface: "ocvpn0"}); err != nil {
		t.Fatal(err)
	}

	if len(deleted) != 2 {
		t.Fatal(deleted)
	}

	for _, call := range deleted {
		if !strings.Contains(call, "-D PREROUTING -i br55") || strings.Contains(call, "connection-9") || strings.Contains(call, "resolver") {
			t.Fatal(call)
		}
	}
}

func TestTunnelSetupDoesNotRequireOrInterceptDNS(t *testing.T) {
	previous := runner
	t.Cleanup(func() { runner = previous })
	stop := errors.New("stop before host interface mutation")
	var calls []string
	runner = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		if strings.Contains(call, "ip link set") {
			return "", stop
		}

		return "", nil
	}

	for _, dns := range [][]string{nil, {"10.0.0.53"}} {
		_, err := applyConnection(Manifest{Interface: "ocvpn0", Table: 51998}, nil,
			Tunnel{Interface: "ocvpn0", Address: "10.0.0.2", DNS: dns})
		if !errors.Is(err, stop) {
			t.Fatalf("DNS affected tunnel setup: %v", err)
		}
	}

	for _, call := range calls {
		if strings.Contains(call, "DNAT") || strings.Contains(call, "--dport 53") {
			t.Fatal("DNS interception created", call)
		}
	}
}

func TestDNSRedirectCleanupFailureIsReported(t *testing.T) {
	previous := runner
	t.Cleanup(func() { runner = previous })
	failure := errors.New("cannot read firewall rules")
	runner = func(string, ...string) (string, error) {
		return "", failure
	}

	if !errors.Is(removeDNSRedirects(Manifest{}), failure) {
		t.Fatal("cleanup failure hidden")
	}
}
