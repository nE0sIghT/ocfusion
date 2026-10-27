// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultAndEmptySelectionNeverIntercept(t *testing.T) {
	for _, data := range []string{"", "null", `{"enabled":true}`} {
		c, err := parseConfig([]byte(data))
		if err != nil || hasSelection(c) {
			t.Fatal("empty destination lists must not become a global rule", c, err)
		}
	}
}

func TestNormalizeTargetsAndRejectCodeOrFlags(t *testing.T) {
	c, err := parseConfig([]byte(`{"networks":["203.0.113.19","2001:db8:1::5/64"],"domains":["Example.COM."]}`))
	if err != nil || c.Networks[0] != "203.0.113.19/32" || c.Networks[1] != "2001:db8:1::/64" || c.Domains[0] != "example.com" {
		t.Fatal(c, err)
	}

	for _, data := range []string{
		`{"networks":["0.0.0.0/0"]}`,
		`{"networks":["::ffff:192.0.2.1"]}`,
		`{"networks":["1.1.1.1 --jump ACCEPT"]}`,
		`{"domains":["https://example.com"]}`,
		`{"domains":["*.example.com"]}`,
		`{"domains":["--lua-init=os.execute()"]}`,
		`{"tcp_ports":[443,443]}`,
		`{"tcp_strategy":"luaexec:code=anything"}`,
		`{"lua_init":"anything"}`,
	} {
		if _, err := parseConfig([]byte(data)); err == nil {
			t.Fatal("unsafe settings accepted", data)
		}
	}
}

func TestFirewallScopesBothDirectionsAndReservesForeignMarks(t *testing.T) {
	old := execute
	t.Cleanup(func() {
		execute = old
	})
	execute = func(tool string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		if strings.Contains(call, "route show") {
			if args[0] == "-6" {
				return "default via fe80::1 dev ppp0", nil
			}
			return "default dev ppp0", nil
		}
		return "-A OUTPUT -j MARK --set-xmark 0x40000000/0x40000000\n-A POSTROUTING -j NFQUEUE --queue-num 200", nil
	}

	c := defaultConfig()
	c.Networks = []string{"203.0.113.0/24", "2001:db8::/32"}
	r, err := allocate(c)
	if err != nil || r.Queue != 201 || r.Mark != "0x20000000" || len(r.Rules) != 4 {
		t.Fatal(r, err)
	}
	for _, rule := range r.Rules {
		args := strings.Join(rule.Args, " ")
		if !strings.Contains(args, "ppp0") || !strings.Contains(args, "--queue-bypass") || !strings.Contains(args, "--connbytes 1:12") {
			t.Fatal("rule lost WAN, initial-packet bound or bypass", rule)
		}
		if rule.Chain == "POSTROUTING" && !strings.Contains(args, " -d ") {
			t.Fatal("outbound destination not restricted", rule)
		}
		if rule.Chain == "PREROUTING" && !strings.Contains(args, " -s ") {
			t.Fatal("reply source not restricted", rule)
		}
	}
}

func TestDomainAndIPRestrictionsAreCombinedWithoutLuaInjection(t *testing.T) {
	c := defaultConfig()
	c.Domains = []string{"example.com"}
	c.UDP = "fake-quic"
	r := Resources{Queue: 201, Mark: "0x20000000"}
	args := workerArgs(c, r, "/private/worker", 62000)
	joined := strings.Join(args, " ")
	if strings.Count(joined, "--hostlist=/private/worker/domains.txt") != 2 || !strings.Contains(joined, "--uid=62000:62000") || !strings.Contains(joined, ":ip_ttl=3:ip6_ttl=3:repeats=3") {
		t.Fatal(args)
	}
	for _, arg := range args {
		if strings.Contains(arg, "luaexec") || strings.HasPrefix(arg, "--daemon") || strings.HasPrefix(arg, "--hostlist-auto") {
			t.Fatal("unexpected worker capability", arg)
		}
	}
}

func TestCleanupOnlyOwnedRulesAndPreservesFailedJournal(t *testing.T) {
	oldDir, oldExecute := runDir, execute
	runDir = t.TempDir()
	t.Cleanup(func() {
		runDir, execute = oldDir, oldExecute
	})
	r := Resources{Queue: 200, Rules: []Rule{{Tool: "iptables", Chain: "POSTROUTING", Args: []string{"-m", "comment", "--comment", "ocfusion:zapret2", "-j", "NFQUEUE", "--queue-num", "200", "--queue-bypass"}}}}
	if err := writeJSON(filepath.Join(runDir, "resources.json"), r); err != nil {
		t.Fatal(err)
	}
	deletions := 0
	execute = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), " -D ") {
			deletions++
			return "", os.ErrPermission
		}
		return "", nil
	}
	if cleanup() == nil {
		t.Fatal("failed removal was hidden")
	}
	if _, err := os.Stat(filepath.Join(runDir, "resources.json")); err != nil {
		t.Fatal("recovery record was lost", err)
	}

	execute = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), " -D ") {
			deletions++
		}
		return "", nil
	}
	if cleanup() != nil || deletions != 2 {
		t.Fatal("cleanup retry did not remove owned rule")
	}
	raw, _ := json.Marshal(Resources{Queue: 200, Rules: []Rule{{Tool: "iptables", Chain: "OUTPUT", Args: []string{"-j", "ACCEPT"}}}})
	os.WriteFile(filepath.Join(runDir, "resources.json"), raw, 0600)
	if cleanup() == nil || deletions != 2 {
		t.Fatal("foreign rule accepted for recovery")
	}
}

func TestOverlappingPrefixesDoNotQueuePacketsTwice(t *testing.T) {
	c, err := parseConfig([]byte(`{"networks":["203.0.113.7","203.0.113.0/24","203.0.113.8/32","2001:db8::/32","2001:db8:1::/48"]}`))
	if err != nil || len(c.Networks) != 2 || c.Networks[0] != "203.0.113.0/24" || c.Networks[1] != "2001:db8::/32" {
		t.Fatal(c, err)
	}
}

func TestConnectionMarkRestoreCannotEraseLoopProtection(t *testing.T) {
	old := execute
	t.Cleanup(func() {
		execute = old
	})
	execute = func(tool string, args ...string) (string, error) {
		return "-A OUTPUT -j CONNMARK --restore-mark", nil
	}
	c := defaultConfig()
	c.Networks = []string{"203.0.113.0/24"}
	if _, err := allocate(c); err == nil || !strings.Contains(err.Error(), "mark") {
		t.Fatal("unprotected loop-prevention mark accepted", err)
	}
}
