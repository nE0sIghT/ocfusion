// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var execute = func(tool string, args ...string) (string, error) {
	paths := map[string][]string{
		"ip":        {"/usr/sbin/ip", "/sbin/ip"},
		"iptables":  {"/usr/sbin/iptables", "/sbin/iptables"},
		"ip6tables": {"/usr/sbin/ip6tables", "/sbin/ip6tables"},
	}
	for _, path := range paths[tool] {
		if _, err := os.Stat(path); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			data, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
			return string(data), err
		}
	}

	return "", errors.New("required network tool unavailable")
}

var readNFQueues = func() ([]byte, error) {
	return os.ReadFile("/proc/net/netfilter/nfnetlink_queue")
}

type Rule struct {
	Tool  string   `json:"tool"`
	Chain string   `json:"chain"`
	Args  []string `json:"args"`
}

type Resources struct {
	Queue int    `json:"queue"`
	Mark  string `json:"mark"`
	Rules []Rule `json:"rules"`
	Acct  bool   `json:"enabled_accounting"`
}

func writeJSON(path string, data any) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return writeJSONBytes(path, raw)
}

func writeCompactJSON(path string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return writeJSONBytes(path, raw)
}

func writeJSONBytes(path string, raw []byte) error {
	if err := os.WriteFile(path+".new", raw, 0600); err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}

func ensureRule(rule Rule) error {
	base := []string{"-t", "mangle", "-C", rule.Chain}
	_, err := execute(rule.Tool, append(base, rule.Args...)...)
	if err == nil {
		return nil
	}

	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		return err
	}

	op := "-A"
	if strings.Contains(strings.Join(rule.Args, " "), "ocfusion:zapret2-scan") {
		op = "-I"
	}

	_, err = execute(rule.Tool, append([]string{"-t", "mangle", op, rule.Chain}, rule.Args...)...)
	return err
}

func cleanup() error {
	return cleanupAt(filepath.Join(runDir, "resources.json"), "ocfusion:zapret2")
}

func cleanupAt(path, owner string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}

	var r Resources
	if err != nil || len(data) > 524288 || json.Unmarshal(data, &r) != nil || r.Queue < 200 || r.Queue > 255 || len(r.Rules) > 1024 {
		return errors.New("invalid packet-processing recovery record")
	}

	chains := map[string]Rule{}
	for _, rule := range r.Rules {
		if (rule.Tool != "iptables" && rule.Tool != "ip6tables") || (rule.Chain != "POSTROUTING" && rule.Chain != "PREROUTING" && !(owner == "ocfusion:zapret2-scan" && rule.Chain == "OUTPUT")) {
			return errors.New("invalid packet-processing recovery rule")
		}

		comment := false
		for i, arg := range rule.Args {
			if arg == "--comment" && i+1 < len(rule.Args) && rule.Args[i+1] == owner {
				comment = true
			}
		}

		if !comment {
			return errors.New("unowned packet-processing recovery rule")
		}

		chains[rule.Tool+":"+rule.Chain] = rule
	}

	var failures error
	for _, rule := range chains {
		failures = errors.Join(failures, removeOwnedRules(rule.Tool, rule.Chain, owner))
	}
	if failures != nil {
		return failures
	}

	// Accounting is shared kernel state. Leave it enabled rather than undoing
	// another service's subsequent dependency on it. No traffic rule remains.
	return os.Remove(path)
}

// Read actual rules instead of interpreting a failed -C as absence. Besides
// preserving recovery on xtables errors, this removes duplicate owned rules.
func removeOwnedRules(tool, chain, owner string) error {
	output, err := execute(tool, "-t", "mangle", "-S", chain)
	if err != nil {
		return err
	}

	for _, line := range strings.Split(output, "\n") {
		args := strings.Fields(line)
		if len(args) < 4 || args[0] != "-A" || args[1] != chain {
			continue
		}

		owned := false
		for i := 2; i+1 < len(args); i++ {
			if args[i] == "--comment" && strings.Trim(args[i+1], "\"") == owner {
				owned = true
			}
		}
		if !owned {
			continue
		}

		// Owned rules contain only fixed tokens, numeric selectors and interface
		// names. iptables quotes their comment in -S output; no shell is used.
		for i := range args {
			args[i] = strings.Trim(args[i], "\"")
		}
		args[0] = "-D"
		if _, err := execute(tool, append([]string{"-t", "mangle"}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

func wan(family string) (string, error) {
	output, err := execute("ip", family, "route", "show", "table", "main", "default")
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}

		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "dev" && interfacePattern.MatchString(fields[i+1]) {
				iface := fields[i+1]
				if strings.HasPrefix(iface, "br") || strings.HasPrefix(iface, "tun") || strings.HasPrefix(iface, "ocvpn") || strings.HasPrefix(iface, "wg") {
					continue
				}

				return iface, nil
			}
		}
	}

	return "", errors.New("WAN route unavailable")
}

var markRE = regexp.MustCompile(`(?:--mark|--set-xmark|--set-mark|fwmark)\s+(0x[0-9a-fA-F]+|[0-9]+)(?:/(0x[0-9a-fA-F]+|[0-9]+))?`)
var queueRE = regexp.MustCompile(`--queue-num\s+([0-9]+)`)

func allocate(c Config) (Resources, error) {
	if len(c.Rules) > 0 {
		c.ManagedSets = true
	}
	var r Resources
	state := ""
	for _, tool := range []string{"iptables", "ip6tables"} {
		out, err := execute(tool, "-t", "mangle", "-S")
		if err != nil && tool == "iptables" {
			return r, err
		}

		state += "\n" + out
	}
	out, err := execute("ip", "-4", "rule", "show")
	if err != nil {
		return r, err
	}

	state += "\n" + out
	out, _ = execute("ip", "-6", "rule", "show")
	state += "\n" + out

	used := uint64(0)
	for _, match := range markRE.FindAllStringSubmatch(state, -1) {
		mask := uint64(0xffffffff)
		if match[2] != "" {
			mask, _ = strconv.ParseUint(match[2], 0, 32)
		}
		used |= mask
	}

	// A full CONNMARK restore can erase the loop-prevention bit.
	restoreMask := regexp.MustCompile(`--nfmask\s+(0x[0-9a-fA-F]+|[0-9]+)`)
	for _, line := range strings.Split(state, "\n") {
		if !strings.Contains(line, "--restore-mark") {
			continue
		}

		mask := uint64(0xffffffff)
		if match := restoreMask.FindStringSubmatch(line); match != nil {
			mask, _ = strconv.ParseUint(match[1], 0, 32)
		}
		used |= mask
	}

	for bit := uint64(1 << 30); bit >= 1<<16; bit >>= 1 {
		if bit&used == 0 {
			r.Mark = fmt.Sprintf("0x%x", bit)
			break
		}
	}
	if r.Mark == "" {
		return r, errors.New("no free packet-processing mark")
	}

	queues := map[int]bool{}
	for _, match := range queueRE.FindAllStringSubmatch(state, -1) {
		q, _ := strconv.Atoi(match[1])
		queues[q] = true
	}
	data, readErr := readNFQueues()
	if readErr != nil && !os.IsNotExist(readErr) {
		return r, readErr
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			q, _ := strconv.Atoi(fields[0])
			queues[q] = true
		}
	}
	for q := 200; q <= 255; q++ {
		if !queues[q] {
			r.Queue = q
			break
		}
	}
	if r.Queue == 0 {
		return r, errors.New("no free packet-processing queue")
	}

	for _, family := range []string{"-4", "-6"} {
		networks := []string{}
		// Domain selection must inspect candidate destinations independently of
		// the IP list. The engine applies separate IP OR domain profiles.
		if len(c.Domains) == 0 && !usesEngineSets(c) {
			for _, prefix := range c.Networks {
				if strings.Contains(prefix, ":") == (family == "-6") {
					networks = append(networks, prefix)
				}
			}
		}
		if len(c.Domains) == 0 && !usesEngineSets(c) && len(c.Networks) > 0 && len(networks) == 0 {
			continue
		}
		if len(networks) == 0 {
			networks = []string{""}
		}

		iface, err := wan(family)
		if err != nil {
			if family == "-4" {
				return r, err
			}
			continue
		}
		tool := "iptables"
		if family == "-6" {
			tool = "ip6tables"
		}

		for _, prefix := range networks {
			for _, protocol := range []string{"tcp", "udp"} {
				for _, ports := range firewallPortGroups(c, protocol) {

					for _, incoming := range []bool{false, true} {
						chain, ifaceFlag, addressFlag, portsFlag, direction := "POSTROUTING", "-o", "-d", "--dports", "original"
						if incoming {
							chain, ifaceFlag, addressFlag, portsFlag, direction = "PREROUTING", "-i", "-s", "--sports", "reply"
						}
						args := []string{ifaceFlag, iface, "-p", protocol}
						if prefix != "" {
							args = append(args, addressFlag, prefix)
						}
						if ports != "*" {
							args = append(args, "-m", "multiport", portsFlag, ports)
						}
						markArgs := []string{"-m", "mark", "!", "--mark", r.Mark + "/" + r.Mark}
						if c.RouteMask != 0 {
							bit, _ := strconv.ParseUint(r.Mark, 0, 32)
							markArgs = []string{"-m", "mark", "--mark", fmt.Sprintf("0x0/0x%x", bit|uint64(c.RouteMask))}
						}
						args = append(args, markArgs...)
						if c.RouteMask != 0 {
							// Replies retain native routing, so inspect the connection
							// mark without restoring the VPN mark onto reply packets.
							args = append(args, "-m", "connmark", "--mark", fmt.Sprintf("0x0/0x%x", c.RouteMask))
						}
						// TCP strategies modify outgoing requests only. Automatic TTL
						// needs the SYN/ACK, not incoming application data.
						if incoming && protocol == "tcp" {
							args = append(args, "--tcp-flags", "SYN,ACK", "SYN,ACK")
						}

						args = append(args,
							"-m", "connbytes", "--connbytes", "1:"+strconv.Itoa(processingPacketLimit(c)), "--connbytes-dir", direction, "--connbytes-mode", "packets",
							"-m", "comment", "--comment", "ocfusion:zapret2", "-j", "NFQUEUE", "--queue-num", strconv.Itoa(r.Queue), "--queue-bypass")
						r.Rules = append(r.Rules, Rule{Tool: tool, Chain: chain, Args: args})
					}
				}
			}
		}
	}

	if len(r.Rules) == 0 {
		return r, errors.New("no active protocols")
	}

	return r, nil
}
