// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Batched stdin is necessary for large service sets and atomic chain updates.
// Names and tokens are generated from bounded typed data, never shell text.
var destinationInput = func(tool, input string, args ...string) error {
	allowed := map[string][]string{
		"ipset":             {"/usr/sbin/ipset", "/sbin/ipset"},
		"iptables-restore":  {"/usr/sbin/iptables-restore", "/sbin/iptables-restore"},
		"ip6tables-restore": {"/usr/sbin/ip6tables-restore", "/sbin/ip6tables-restore"},
	}
	for _, path := range allowed[tool] {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Stdin = strings.NewReader(input)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("destination %s update failed: %w", tool, err)
		}
		return nil
	}
	return errors.New("destination routing tool unavailable")
}

func (l *DestinationLease) prefix() string {
	hash := sha256.Sum256([]byte(l.Owner))
	return fmt.Sprintf("ocfd_%x", hash[:5])
}

func (l *DestinationLease) chain(family int) string {
	return l.prefix() + "_" + strconv.Itoa(family)
}

func (l *DestinationLease) set(id string, family int) string {
	hash := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%s_%x_%d", l.prefix(), hash[:4], family)
}

func (l *DestinationLease) comment() string {
	return "ocfusion:destination:" + l.Owner
}

func (l *DestinationLease) mask() string {
	mask := uint64(0)
	for _, route := range l.Routes {
		value, _ := strconv.ParseUint(strings.Split(route.Mark, "/")[0], 0, 32)
		mask |= value
	}
	return fmt.Sprintf("0x%x", mask)
}

func (l *DestinationLease) install(dns *DestinationDNS, exclusions [][]string, bridges []string) error {
	var sets strings.Builder
	l.State = nil
	chains := map[int][]string{4: {}, 6: {}}
	for _, policy := range l.Policies {
		addresses, status := l.addresses(policy, dns)
		if len(addresses) > 65536 {
			return errors.New("resolved destination set exceeds limits")
		}
		status.State = "NORMAL"
		if policy.Action == "dpi" {
			status.State = "DPI"
		} else if policy.Action == "vpn" {
			status.State = "BLOCKED"
			route := l.Routes[policy.ID]
			if route.Interface != "" {
				status.State = "VPN"
				status.Interface, status.Source, status.Mark = route.Interface, route.Source, strings.Split(route.Mark, "/")[0]
			} else if policy.Failure == "wan" && time.Since(l.Heartbeat) <= 12*time.Second {
				status.State = "WAN"
			}
		}
		l.State = append(l.State, status)
		for _, family := range []int{4, 6} {
			set := l.set(policy.ID, family)
			ipFamily := "inet"
			if family == 6 {
				ipFamily = "inet6"
			}
			staging := set + "t"
			fmt.Fprintf(&sets, "create %s hash:net family %s maxelem 65536 -exist\ncreate %s hash:net family %s maxelem 65536 -exist\nflush %s\n", set, ipFamily, staging, ipFamily, staging)
			for _, value := range addresses {
				prefix, err := netip.ParsePrefix(value)
				if err != nil || prefix.Addr().Is4() != (family == 4) {
					continue
				}
				fmt.Fprintf(&sets, "add %s %s -exist\n", staging, prefix)
			}
			fmt.Fprintf(&sets, "swap %s %s\n", staging, set)
			if policy.Action == "vpn" {
				history := set + "h"
				fmt.Fprintf(&sets, "create %s hash:net family %s timeout 86400 maxelem 65536 -exist\n", history, ipFamily)
				for _, value := range addresses {
					prefix, err := netip.ParsePrefix(value)
					if err == nil && prefix.Addr().Is4() == (family == 4) {
						fmt.Fprintf(&sets, "add %s %s timeout 86400 -exist\n", history, prefix)
					}
				}
			}
			match := "-m set --match-set " + set + " dst"
			if policy.Action != "vpn" {
				chains[family] = append(chains[family], match+" -j RETURN")
				continue
			}
			mark := l.Routes[policy.ID].Mark
			bit := strings.Split(mark, "/")[0]
			chains[family] = append(chains[family],
				"-m set --match-set "+set+"h dst -m conntrack --ctdir ORIGINAL --ctstate ESTABLISHED,RELATED -m connmark --mark "+mark+" -j CONNMARK --restore-mark --nfmask "+bit+" --ctmask "+bit,
				"-m mark --mark "+mark+" -j RETURN",
				match+" -j MARK --set-xmark "+mark,
				"-m mark --mark "+mark+" -j CONNMARK --save-mark --nfmask "+bit+" --ctmask "+bit,
				"-m mark --mark "+mark+" -j RETURN",
			)
		}
	}
	signature, _ := json.Marshal([]any{sets.String(), chains, exclusions, bridges, l.mask()})
	hash := fmt.Sprintf("%x", sha256.Sum256(signature))
	if l.Applied == hash && time.Since(l.Repaired) < 30*time.Second {
		return nil
	}
	if err := destinationInput("ipset", sets.String(), "restore"); err != nil {
		return err
	}
	// Install IPv6 protection before enabling the IPv4 forwarding path.
	for _, family := range []int{6, 4} {
		tool := "iptables"
		if family == 6 {
			tool = "ip6tables"
		}
		chain := l.chain(family)
		if _, err := runner(tool, "-t", "mangle", "-S", chain); err != nil {
			if _, err := runner(tool, "-t", "mangle", "-N", chain); err != nil {
				return err
			}
		}
		var rules strings.Builder
		fmt.Fprintf(&rules, "*mangle\n-F %s\n", chain)
		appendRule := func(args string) {
			fmt.Fprintf(&rules, "-A %s %s\n", chain, args)
		}
		appendRule("-m addrtype --dst-type LOCAL -j RETURN")
		localRoutes, err := runner("ip", "-"+strconv.Itoa(family), "route", "show", "table", "main")
		if err != nil {
			return err
		}
		for _, line := range strings.Split(localRoutes, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 2 && fields[1] == "dev" {
				for _, bridge := range bridges {
					if fields[2] == bridge {
						if prefix, err := netip.ParsePrefix(fields[0]); err == nil {
							appendRule("-d " + prefix.String() + " -j RETURN")
						}
					}
				}
			}
		}
		for _, args := range exclusions {
			// An IPv4 source exclusion is irrelevant to an IPv6 chain. All
			// interface exclusions remain authoritative for both families.
			compatible := true
			for index := 0; index+1 < len(args); index++ {
				if (args[index] == "-s" || args[index] == "-d") && strings.Contains(args[index+1], ":") != (family == 6) {
					compatible = false
				}
			}
			if !compatible {
				continue
			}
			appendRule(strings.Join(args, " "))
		}
		for _, rule := range chains[family] {
			appendRule(rule)
		}
		rules.WriteString("COMMIT\n")
		if err := destinationInput(tool+"-restore", rules.String(), "--noflush"); err != nil {
			return err
		}
		for _, bridge := range bridges {
			if err := xt(tool, "mangle", "add", "PREROUTING", "-i", bridge, "-m", "comment", "--comment", l.comment(), "-j", chain); err != nil {
				return err
			}
		}
	}
	l.Applied, l.Repaired = hash, time.Now()
	return nil
}

func removeDestinationComment(tool, table, chain, owner string) error {
	out, err := runner(tool, "-t", table, "-S", chain)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		args := strings.Fields(line)
		if len(args) < 4 || args[0] != "-A" || args[1] != chain {
			continue
		}
		owned := false
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--comment" && strings.Trim(args[i+1], "\"") == owner {
				owned = true
			}
		}
		if owned {
			for i := range args {
				args[i] = strings.Trim(args[i], "\"")
			}
			args[0] = "-D"
			if _, err := runner(tool, append([]string{"-t", table}, args...)...); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *DestinationLease) remove() error {
	for _, tool := range []string{"iptables", "ip6tables"} {
		for _, entry := range [][2]string{{"mangle", "PREROUTING"}, {"mangle", "FORWARD"}, {"filter", "FORWARD"}, {"nat", "POSTROUTING"}} {
			if tool == "ip6tables" && entry[0] == "nat" {
				continue
			}
			if err := removeDestinationComment(tool, entry[0], entry[1], l.comment()); err != nil {
				return err
			}
		}
		family := 4
		if tool == "ip6tables" {
			family = 6
		}
		chain := l.chain(family)
		if _, err := runner(tool, "-t", "mangle", "-S", chain); err == nil {
			if _, err = runner(tool, "-t", "mangle", "-F", chain); err != nil {
				return err
			}
			if _, err = runner(tool, "-t", "mangle", "-X", chain); err != nil {
				return err
			}
		}
	}
	for _, route := range l.Routes {
		if err := route.remove(); err != nil {
			return err
		}
	}
	// Enumerate only names in our deterministic private namespace, including
	// selectors removed from the latest configuration.
	out, err := runner("ipset", "list", "-name")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(out) {
		if strings.HasPrefix(name, l.prefix()+"_") {
			if _, err := runner("ipset", "destroy", name); err != nil {
				return err
			}
		}
	}
	return nil
}
