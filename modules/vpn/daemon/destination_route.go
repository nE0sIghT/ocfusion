// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (b *Broker) allocateDestinationRoute() (*DestinationRoute, error) {
	state := ""
	for _, family := range []string{"-4", "-6"} {
		out, err := runner("ip", family, "rule", "show")
		if err != nil {
			return nil, err
		}
		state += "\n" + out
	}
	for _, tool := range []string{"iptables", "ip6tables"} {
		out, err := runner(tool, "-t", "mangle", "-S")
		if err != nil {
			return nil, err
		}
		state += "\n" + out
	}
	for _, lease := range b.destinations {
		for _, route := range lease.Routes {
			state += "\n" + route.Priority + ": fwmark " + route.Mark + " lookup " + route.Table
		}
	}
	result := &DestinationRoute{}
	used := usedDNSMask(state)
	for bit := uint64(1 << 30); bit >= 1<<16; bit >>= 1 {
		if used&bit == 0 {
			result.Mark = fmt.Sprintf("0x%x/0x%x", bit, bit)
			break
		}
	}
	if result.Mark == "" {
		return nil, errors.New("no free destination mark")
	}
	for priority := 1; priority < 20; priority++ {
		occupied := false
		for _, line := range strings.Split(state, "\n") {
			occupied = occupied || strings.HasPrefix(strings.TrimSpace(line), strconv.Itoa(priority)+":")
		}
		if !occupied {
			result.Priority = strconv.Itoa(priority)
			break
		}
	}
	for table := 52400; table < 52500; table++ {
		name := strconv.Itoa(table)
		if strings.Contains(state, "lookup "+name) {
			continue
		}
		free := true
		for _, family := range []string{"-4", "-6"} {
			output, err := runner("ip", family, "route", "show", "table", name)
			free = free && err == nil && output == ""
		}
		if free {
			result.Table = name
			break
		}
	}
	if result.Table == "" || result.Priority == "" {
		return nil, errors.New("no free destination routing resource")
	}
	return result, nil
}

func (r *DestinationRoute) reconcile(target EgressTarget, fallback bool) error {
	key := fmt.Sprintf("%s:%s:%t", target.Interface, target.Address, fallback)
	if r.Applied == key && time.Since(r.Repaired) < 30*time.Second {
		return nil
	}
	for _, family := range []string{"-4", "-6"} {
		terminal := "unreachable"
		if fallback {
			terminal = "throw"
		}
		if _, err := runner("ip", family, "route", "replace", terminal, "default", "table", r.Table, "metric", "32760"); err != nil {
			return err
		}
		// Route types are distinct keys; remove the old opposite terminal.
		opposite := "throw"
		if fallback {
			opposite = "unreachable"
		}
		runner("ip", family, "route", "del", opposite, "default", "table", r.Table, "metric", "32760")
		out, err := runner("ip", family, "rule", "show")
		if err != nil {
			return err
		}
		present := false
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), r.Priority+":") {
				if !strings.Contains(line, "fwmark "+r.Mark+" lookup "+r.Table) {
					return errors.New("destination routing priority conflict")
				}
				present = true
			}
		}
		if !present {
			if _, err := runner("ip", family, "rule", "add", "fwmark", r.Mark, "lookup", r.Table, "priority", r.Priority); err != nil {
				return err
			}
		}
	}
	if target.Ready {
		if _, err := runner("ip", "-4", "route", "replace", "default", "dev", target.Interface, "src", target.Address, "table", r.Table, "metric", "10"); err != nil {
			return err
		}
	} else {
		if _, err := runner("ip", "-4", "route", "del", "default", "table", r.Table, "metric", "10"); err != nil {
			output, readErr := runner("ip", "-4", "route", "show", "table", r.Table)
			if readErr != nil || strings.Contains(output, "metric 10") {
				return errors.New("destination VPN route still active")
			}
		}
	}
	r.Interface, r.Source = target.Interface, target.Address
	r.Applied, r.Repaired = key, time.Now()
	return nil
}

func destinationExclusions() ([][]string, error) {
	// Exclude assignments even when their VPN is down; WAN-only is a policy,
	// not a test of current tunnel availability.
	if value := nvget("vpnc_default_wan"); value != "" && value != "0" {
		return [][]string{{"-j", "RETURN"}}, nil
	}
	out := [][]string{}
	subnets := nvget("subnet_rl")
	lan := nvget("lan_ifname")
	for _, row := range strings.Split(nvget("sdn_rl"), "<") {
		fields := strings.Split(row, ">")
		if len(fields) < 16 || fields[2] != "1" || fields[6] == "0" || fields[6] == "" {
			continue
		}
		found := false
		for _, subnet := range strings.Split(subnets, "<") {
			values := strings.Split(subnet, ">")
			if len(values) > 1 && values[0] == fields[4] && ifaceRE.MatchString(values[1]) {
				out = append(out, []string{"-i", values[1], "-j", "RETURN"})
				found = true
			}
		}
		if !found && fields[4] == "0" && ifaceRE.MatchString(lan) {
			out = append(out, []string{"-i", lan, "-j", "RETURN"})
			found = true
		}
		if !found {
			return nil, errors.New("native VPN network unavailable")
		}
	}
	for _, row := range strings.Split(nvget("vpnc_dev_policy_list"), "<") {
		fields := strings.Split(row, ">")
		if len(fields) < 4 || fields[0] != "1" || fields[3] == "0" || fields[3] == "" {
			continue
		}
		args := []string{}
		if fields[1] != "" && fields[1] != "0.0.0.0" {
			if _, err := netip.ParseAddr(fields[1]); err != nil {
				if _, err := netip.ParsePrefix(fields[1]); err != nil {
					return nil, errors.New("native VPN device policy unavailable")
				}
			}
			args = append(args, "-s", fields[1])
		}
		if fields[2] != "" && fields[2] != "0.0.0.0" {
			if _, err := netip.ParseAddr(fields[2]); err != nil {
				if _, err := netip.ParsePrefix(fields[2]); err != nil {
					return nil, errors.New("native VPN destination policy unavailable")
				}
			}
			args = append(args, "-d", fields[2])
		}
		if len(fields) > 4 && fields[4] != "" {
			if !ifaceRE.MatchString(fields[4]) {
				return nil, errors.New("native VPN interface policy unavailable")
			}
			args = append(args, "-i", fields[4])
		}
		if len(args) == 0 {
			return nil, errors.New("native VPN policy has no source")
		}
		out = append(out, append(args, "-j", "RETURN"))
	}
	return out, nil
}

func destinationLANInterfaces() []string {
	bridges := []string{}
	seen := map[string]bool{}
	add := func(bridge string) {
		if ifaceRE.MatchString(bridge) && !seen[bridge] {
			seen[bridge] = true
			bridges = append(bridges, bridge)
		}
	}
	add(nvget("lan_ifname"))
	for _, row := range strings.Split(nvget("subnet_rl"), "<") {
		fields := strings.Split(row, ">")
		if len(fields) > 1 {
			add(fields[1])
		}
	}
	sort.Strings(bridges)
	return bridges
}

func (r *DestinationRoute) remove() error {
	for _, family := range []string{"-4", "-6"} {
		if _, err := runner("ip", family, "rule", "del", "fwmark", r.Mark, "lookup", r.Table, "priority", r.Priority); err != nil {
			out, readErr := runner("ip", family, "rule", "show")
			if readErr != nil || strings.Contains(out, "fwmark "+r.Mark+" lookup "+r.Table) {
				return errors.New("destination routing cleanup failed")
			}
		}
		for _, terminal := range []string{"", "unreachable", "throw"} {
			args := []string{family, "route", "del"}
			if terminal != "" {
				args = append(args, terminal)
			}
			metric := "32760"
			if terminal == "" {
				metric = "10"
			}
			args = append(args, "default", "table", r.Table, "metric", metric)
			runner("ip", args...)
		}
	}
	return nil
}
