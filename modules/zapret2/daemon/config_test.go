// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Unit fixtures must not depend on queues or /proc permissions of the host.
// Individual allocation/readiness tests override this with kernel snapshots.
func init() {
	readNFQueues = func() ([]byte, error) {
		return nil, nil
	}
}

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
		if rule.Chain == "PREROUTING" && strings.Contains(args, "-p tcp") && !strings.Contains(args, "--tcp-flags SYN,ACK SYN,ACK") {
			t.Fatal("incoming TCP data must bypass the queue", args)
		}
		if rule.Chain == "POSTROUTING" && strings.Contains(args, "--tcp-flags") {
			t.Fatal("outgoing TLS fragments must remain eligible", args)
		}
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

func TestDomainProfileHasNoImplicitIPRequirement(t *testing.T) {
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
		return "-A POSTROUTING -m comment --comment \"ocfusion:zapret2\" -j NFQUEUE --queue-num 200 --queue-bypass", nil
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
		return "-A POSTROUTING -m comment --comment \"ocfusion:zapret2\" -j NFQUEUE --queue-num 200 --queue-bypass", nil
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

func TestIPOrDomainProfilesAndFirewallCandidates(t *testing.T) {
	c := defaultConfig()
	c.Networks = []string{"203.0.113.0/24"}
	c.Domains = []string{"example.com"}
	c.UDP = "fake-quic"
	args := workerArgs(c, Resources{Queue: 200, Mark: "0x40000000"}, "/worker", 62000)
	profiles := [][]string{{}}
	for _, arg := range args {
		if arg == "--new" {
			profiles = append(profiles, []string{})
		} else {
			profiles[len(profiles)-1] = append(profiles[len(profiles)-1], arg)
		}
	}
	if len(profiles) != 4 {
		t.Fatal("expected independent TCP/UDP IP and domain profiles", profiles)
	}
	for i, profile := range profiles {
		joined := strings.Join(profile, " ")
		hasIP := strings.Contains(joined, "--ipset=/worker/networks.txt")
		hasHost := strings.Contains(joined, "--hostlist=/worker/domains.txt")
		if hasIP == hasHost || (i < 2) != hasIP {
			t.Fatal("IP and host selectors became an intersection", profile)
		}
	}

	old := execute
	t.Cleanup(func() {
		execute = old
	})
	execute = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "route show") {
			return "default dev ppp0", nil
		}
		return "", nil
	}
	r, err := allocate(c)
	if err != nil || len(r.Rules) != 8 {
		t.Fatal(r, err)
	}
	for _, rule := range r.Rules {
		joined := " " + strings.Join(rule.Args, " ") + " "
		if strings.Contains(joined, " -d ") || strings.Contains(joined, " -s ") {
			t.Fatal("domain candidates outside the IP list were excluded", rule)
		}
	}
}

func TestAdvancedStrategiesAreBoundedAndMatchUpstreamNames(t *testing.T) {
	for strategy, function := range map[string]string{
		"split": "multisplit", "disorder": "multidisorder",
		"fake": "fake", "fake-split": "multisplit",
		"fake-disorder": "multidisorder", "fakedsplit": "fakedsplit",
		"fakeddisorder": "fakeddisorder", "hostfakesplit": "hostfakesplit",
	} {
		c := defaultConfig()
		c.TCP = strategy
		c.Positions = "host"
		c.Fooling = "badseq"
		c.FakeSNI = "example.net"
		c.AutoTTL = true
		c.Overlap = 32
		if err := validateStrategy(c); err != nil {
			t.Fatal(strategy, err)
		}
		joined := strings.Join(tcpStrategy(c), " ")
		if !strings.Contains(joined, "--lua-desync="+function+":") {
			t.Fatal(strategy, joined)
		}
		if strings.HasPrefix(strategy, "fake") && strategy != "fakedsplit" && strategy != "fakeddisorder" {
			if !strings.Contains(joined, "blob=fake_default_http") || !strings.Contains(joined, "blob=fake_default_tls") || !strings.Contains(joined, ":tcp_seq=-10000") {
				t.Fatal("fake protection or protocol-specific blob missing", joined)
			}
		}
	}
	c, err := parseConfig([]byte(`{"split_positions":"host, midsld+1,-10","fake_sni":"Example.NET."}`))
	if err != nil || c.Positions != "host,midsld+1,-10" || c.FakeSNI != "example.net" {
		t.Fatal(c, err)
	}
	for _, data := range []string{
		`{"split_positions":"host:code=evil"}`,
		`{"split_positions":"2;evil"}`,
		`{"tcp_strategy":"fakedsplit","split_positions":"host,midsld"}`,
		`{"fake_sni":"example.com:luaexec"}`,
		`{"fake_fooling":"fool=luaexec"}`,
		`{"auto_ttl_min":30,"auto_ttl_max":20}`,
		`{"packet_limit":65535}`,
	} {
		if _, err := parseConfig([]byte(data)); err == nil {
			t.Fatal("unbounded strategy accepted", data)
		}
	}
	opts := fakeOptions(c, false)
	if strings.Contains(opts, "tcp_") || strings.Contains(opts, "badsum") {
		t.Fatal("TCP-only fooling leaked to UDP", opts)
	}
}

func TestCleanupRemovesDuplicatesAndKeepsForeignRules(t *testing.T) {
	oldDir, oldExecute := runDir, execute
	runDir = t.TempDir()
	t.Cleanup(func() {
		runDir, execute = oldDir, oldExecute
	})
	rule := Rule{Tool: "iptables", Chain: "POSTROUTING", Args: []string{"--comment", "ocfusion:zapret2"}}
	path := filepath.Join(runDir, "resources.json")
	if err := writeJSON(path, Resources{Queue: 200, Rules: []Rule{rule, rule}}); err != nil {
		t.Fatal(err)
	}

	owned := `-A POSTROUTING -m comment --comment "ocfusion:zapret2" -j NFQUEUE --queue-num 200 --queue-bypass`
	foreign := `-A POSTROUTING -m comment --comment "other-service" -j NFQUEUE --queue-num 201`
	deletions := 0
	execute = func(tool string, args ...string) (string, error) {
		if args[2] == "-S" {
			return owned + "\n" + foreign + "\n" + owned, nil
		}
		if args[2] != "-D" || strings.Contains(strings.Join(args, " "), "other-service") {
			t.Fatal("unexpected mutation", args)
		}
		deletions++
		return "", nil
	}
	if err := cleanup(); err != nil || deletions != 2 {
		t.Fatal("duplicate recovery failed", deletions, err)
	}

	if err := writeJSON(path, Resources{Queue: 200, Rules: []Rule{rule}}); err != nil {
		t.Fatal(err)
	}
	execute = func(tool string, args ...string) (string, error) {
		return "", os.ErrPermission
	}
	if cleanup() == nil {
		t.Fatal("unreadable firewall treated as empty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("unreadable firewall lost recovery record", err)
	}
}

func TestFirewallCheckFailureCannotInstallDuplicates(t *testing.T) {
	oldExecute := execute
	t.Cleanup(func() {
		execute = oldExecute
	})
	lockError := exec.Command("/bin/sh", "-c", "exit 4").Run()
	for _, checkError := range []error{lockError, os.ErrPermission} {
		calls := 0
		execute = func(tool string, args ...string) (string, error) {
			calls++
			if args[2] != "-C" {
				t.Fatal("failed check caused firewall mutation", args)
			}
			return "", checkError
		}
		if ensureRule(Rule{Tool: "iptables", Chain: "POSTROUTING"}) == nil || calls != 1 {
			t.Fatal("firewall check failure was hidden", checkError)
		}
	}
}

func TestSecondAdapterCannotCleanUpActiveInstance(t *testing.T) {
	oldDir := runDir
	runDir = t.TempDir()
	t.Cleanup(func() {
		runDir = oldDir
	})
	lock, err := os.OpenFile(filepath.Join(runDir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runDir, "resources.json")
	if err := os.WriteFile(path, []byte("invalid journal"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatal("second adapter reached recovery", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "recovery record") {
		t.Fatal("released instance lock prevented recovery", err)
	}
}

func TestAllocationRespectsKernelQueueSnapshotAndReadErrors(t *testing.T) {
	oldExecute, oldRead := execute, readNFQueues
	t.Cleanup(func() {
		execute, readNFQueues = oldExecute, oldRead
	})
	execute = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "route show") {
			return "default dev ppp0", nil
		}
		return "", nil
	}
	readNFQueues = func() ([]byte, error) {
		return []byte("200 1234 0 2 65535 0 0 0 1\n"), nil
	}
	c := defaultConfig()
	c.Networks = []string{"203.0.113.0/24"}
	resources, err := allocate(c)
	if err != nil || resources.Queue != 201 || !queueReady(200) || queueReady(201) {
		t.Fatal(resources, err)
	}
	readNFQueues = func() ([]byte, error) {
		return nil, os.ErrPermission
	}
	if _, err := allocate(c); err == nil || queueReady(200) {
		t.Fatal("unreadable kernel queue state accepted")
	}
}
