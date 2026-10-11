// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

func xt(tool, table, op, chain string, args ...string) error {
	// Stock GT-BE98 uses iptables 1.4.12.2, which has -C but no -w.
	prefix := []string{}
	if table != "filter" {
		prefix = append(prefix, "-t", table)
	}

	check := append(append([]string{}, prefix...), "-C", chain)
	check = append(check, args...)
	_, exists := runner(tool, check...)
	if op == "add" && exists == nil {
		return nil
	}

	if op == "del" && exists != nil {
		return nil
	}

	action := "-I"
	if op == "del" {
		action = "-D"
	}

	cmd := append(prefix, action, chain)
	if op == "add" {
		cmd = append(cmd, "1")
	}

	cmd = append(cmd, args...)
	out, e := runner(tool, cmd...)
	if e != nil {
		return fmt.Errorf("%s %s %s: %w: %s", tool, action, chain, e, strings.TrimSpace(out))
	}

	return nil
}

func guard(m Manifest, n Network, op string) error {
	args := []string{"-i", n.Bridge, "!", "-o", tunnelInterface(m), "-m", "comment", "--comment", ruleComment(m, "guard"), "-j", "REJECT"}
	if e := xt("iptables", "filter", op, "FORWARD", args...); e != nil {
		return e
	}

	return xt("ip6tables", "filter", op, "FORWARD", "-i", n.Bridge, "-m", "comment", "--comment", ruleComment(m, "ipv6"), "-j", "REJECT")
}

func routeRule(m Manifest, n Network, op string) error {
	table := strconv.Itoa(m.Table)
	pref := strconv.Itoa(m.Priority)
	if op == "add" {
		s, e := runner("ip", "rule", "show")
		if e != nil {
			return e
		}

		found := false

		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), pref+":") && strings.Contains(line, "iif "+n.Bridge+" ") && strings.Contains(line, "lookup "+table) {
				found = true
			}
		}

		if found {
			return nil
		}

		_, e = runner("ip", "rule", "add", "iif", n.Bridge, "lookup", table, "priority", pref)

		return e
	}

	_, e := runner("ip", "rule", "del", "iif", n.Bridge, "lookup", table, "priority", pref)

	return e
}

func setTerminal(m Manifest) error {
	_, e := runner("ip", "route", "replace", "unreachable", "default", "metric", "32760", "table", strconv.Itoa(m.Table))

	return e
}

func closeTunnel(m Manifest) error {
	if e := setTerminal(m); e != nil {
		return e
	}
	// Delete only our metric-10 tunnel default; never flush someone else's table.

	runner("ip", "route", "del", "default", "dev", tunnelInterface(m), "metric", "10", "table", strconv.Itoa(m.Table))
	nvset(fmt.Sprintf("vpnc%d_state_t", m.Index), "4")
	nvset(fmt.Sprintf("vpnc%d_sbstate_t", m.Index), "0")

	return nil
}

func dnsRules(m Manifest, n Network, dns, op string) error {
	for _, proto := range []string{"udp", "tcp"} {
		e := xt("iptables", "nat", op, "PREROUTING", "-i", n.Bridge, "-p", proto, "--dport", "53", "-m", "comment", "--comment", ruleComment(m, "dns"), "-j", "DNAT", "--to-destination", dns)
		if e != nil {
			return e
		}
	}

	return nil
}

func forwarding(m Manifest, n Network, op string) error {
	if e := xt("iptables", "filter", op, "FORWARD", "-i", n.Bridge, "-o", tunnelInterface(m), "-m", "comment", "--comment", ruleComment(m, "out"), "-j", "ACCEPT"); e != nil {
		return e
	}

	if e := xt("iptables", "filter", op, "FORWARD", "-i", tunnelInterface(m), "-o", n.Bridge, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-m", "comment", "--comment", ruleComment(m, "in"), "-j", "ACCEPT"); e != nil {
		return e
	}

	if e := xt("iptables", "nat", op, "POSTROUTING", "-s", n.CIDR, "-o", tunnelInterface(m), "-m", "comment", "--comment", ruleComment(m, "nat"), "-j", "MASQUERADE"); e != nil {
		return e
	}

	return xt("iptables", "mangle", op, "FORWARD", "-i", n.Bridge, "-o", tunnelInterface(m), "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-m", "comment", "--comment", ruleComment(m, "mss"), "-j", "TCPMSS", "--clamp-mss-to-pmtu")
}

func prepare(m Manifest, ns []Network) error {
	if e := setTerminal(m); e != nil {
		return e
	}

	if len(ns) > 0 {
		if e := ensureComment(os.ReadFile); e != nil {
			return e
		}
	}

	for _, n := range ns {
		if _, e := os.Stat("/sys/class/net/" + n.Bridge); e != nil {
			return fmt.Errorf("bridge %s not ready", n.Bridge)
		}

		if e := guard(m, n, "add"); e != nil {
			return e
		}

		if e := routeRule(m, n, "add"); e != nil {
			return e
		}
	}

	return nil
}

func unbind(m Manifest, n Network, dns string) {
	if dns != "" {
		dnsRules(m, n, dns, "del")
	}

	forwarding(m, n, "del")
	routeRule(m, n, "del")
	guard(m, n, "del")
}

func setTunAddress(m Manifest, ip net.IP) error {
	addresses, e := runner("ip", "-4", "address", "show", "dev", tunnelInterface(m))
	if e != nil {
		return e
	}

	fields := strings.Fields(addresses)

	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "inet" && fields[i+1] == ip.String()+"/32" {
			return nil
		}
	}

	if _, e = runner("ip", "-4", "address", "flush", "dev", tunnelInterface(m)); e != nil {
		return e
	}

	_, e = runner("ip", "address", "add", ip.String()+"/32", "dev", tunnelInterface(m))

	return e
}

func applyConnect(m Manifest, ns []Network, tunnel Tunnel) (retDNS string, retErr error) {
	if tunnel.Interface != tunnelInterface(m) {
		return "", errors.New("unexpected TUN name")
	}

	ip := net.ParseIP(tunnel.Address)
	if ip == nil || ip.To4() == nil {
		return "", errors.New("missing VPN IPv4 address")
	}

	for _, n := range ns {
		_, nw, _ := net.ParseCIDR(n.CIDR)
		if nw.Contains(ip) {
			return "", errors.New("VPN address conflicts with SDN")
		}
	}

	dns := ""
	candidates := tunnel.DNS

	for _, d := range candidates {
		if x := net.ParseIP(d); x != nil && x.To4() != nil {
			dns = d
			break
		}
	}

	if dns == "" {
		return "", errors.New("no IPv4 VPN DNS: supply dns in profile")
	}

	defer func() {
		if retErr != nil {
			closeTunnel(m)

			for _, n := range ns {
				dnsRules(m, n, dns, "del")
				forwarding(m, n, "del")
			}
		}
	}()
	if e := prepare(m, ns); e != nil {
		return "", e
	}

	mtu := tunnel.MTU

	if mtu < 576 || mtu > 1500 {
		mtu = 1300
	}

	if _, e := runner("ip", "link", "set", "dev", tunnelInterface(m), "mtu", strconv.Itoa(mtu), "up"); e != nil {
		return "", e
	}
	// Strict reverse-path filtering would reject Internet replies arriving on TUN
	// because the router main default remains WAN. Scope loose validation to our TUN.

	if e := os.WriteFile("/proc/sys/net/ipv4/conf/"+tunnelInterface(m)+"/rp_filter", []byte("2\n"), 0600); e != nil {
		return "", fmt.Errorf("TUN reverse-path filtering: %w", e)
	}

	if e := setTunAddress(m, ip); e != nil {
		return "", e
	}

	for _, n := range ns {
		if e := dnsRules(m, n, dns, "add"); e != nil {
			return "", e
		}

		if e := forwarding(m, n, "add"); e != nil {
			return "", e
		}
	}
	// Last operation opens external forwarding through TUN, terminal route remains.

	if _, e := runner("ip", "route", "replace", "default", "dev", tunnelInterface(m), "metric", "10", "table", strconv.Itoa(m.Table)); e != nil {
		return "", e
	}

	for k, val := range map[string]string{"ifname": tunnelInterface(m), "ipaddr": ip.String(), "dns": dns, "state_t": "2", "sbstate_t": "0"} {
		if e := nvset(fmt.Sprintf("vpnc%d_%s", m.Index, k), val); e != nil {
			return "", e
		}
	}

	return dns, nil
}

// Userspace libxt_comment.so does not imply that the kernel match is loaded.
func ensureComment(read func(string) ([]byte, error)) error {
	ready := func() bool {
		for _, path := range []string{"/proc/net/ip_tables_matches", "/proc/net/ip6_tables_matches"} {
			b, e := read(path)
			if e != nil {
				return false
			}

			found := false

			for _, word := range strings.Fields(string(b)) {
				if word == "comment" {
					found = true
					break
				}
			}

			if !found {
				return false
			}
		}

		return true
	}
	if ready() {
		return nil
	}

	out, e := runner("/sbin/modprobe", "xt_comment")
	if e != nil {
		return fmt.Errorf("load stock xt_comment: %w: %s", e, strings.TrimSpace(out))
	}

	if !ready() {
		return errors.New("xt_comment loaded but comment match is not registered for IPv4/IPv6")
	}

	return nil
}

func prepareKernel() error {
	if _, e := os.Stat("/dev/net/tun"); e != nil {
		out, e := runner("/sbin/modprobe", "tun")
		if e != nil {
			return fmt.Errorf("load stock tun: %w: %s", e, strings.TrimSpace(out))
		}

		if _, e = os.Stat("/dev/net/tun"); e != nil {
			return e
		}
	}

	return ensureComment(os.ReadFile)
}

const fusionName = "OCFusion (managed)"

func tunnelInterface(m Manifest) string {
	return m.Interface
}

func ruleComment(m Manifest, kind string) string {
	if m.ID == "" || m.Interface == "ocvpn0" {
		return "ocfusion:" + kind
	}

	return "ocfusion:" + m.ID + ":" + kind
}
