// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EgressTarget describes an existing VPN, never a request to allocate one.
type EgressTarget struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	Interface string `json:"interface,omitempty"`
	Address   string `json:"address,omitempty"`
	Ready     bool   `json:"ready"`
}

type EgressRegistration struct {
	Local   []string `json:"local_addresses,omitempty"`
	Target  string   `json:"target"`
	Bridge  string   `json:"bridge"`
	Port    int      `json:"port"`
	Allowed []string `json:"allowed"`
	UID     int      `json:"uid,omitempty"`
	Tunnel  string   `json:"tunnel,omitempty"`
	Address string   `json:"address,omitempty"`
	CIDR    string   `json:"cidr,omitempty"`
	Ready   bool     `json:"ready"`
}

type EgressLease struct {
	Owner        string
	Registration EgressRegistration
	Table        string
	Heartbeat    time.Time
	Interface    string
}

func (b *Broker) egressTargets() []EgressTarget {
	targets := []EgressTarget{}
	managed := map[int]bool{}
	for _, id := range connectionIDs(b.registry) {
		m := b.registry.Items[id]
		managed[m.Index] = true
		s := b.services[id]
		t := EgressTarget{ID: "managed:" + id, Name: m.Name, Protocol: m.Client}
		if s != nil && s.event != nil && time.Since(s.heartbeat) <= 12*time.Second {
			t.Interface = s.event.Interface
			t.Ready = true
		}

		targets = append(targets, t)
	}

	for _, row := range strings.Split(nvget("vpnc_clientlist"), "<") {
		fields := strings.Split(row, ">")
		if len(fields) < 7 {
			continue
		}

		index, err := strconv.Atoi(fields[6])
		if err != nil || index < 1 || index > 255 || managed[index] {
			continue
		}

		prefix := fmt.Sprintf("vpnc%d_", index)
		identity := sha256.Sum256([]byte(strings.Join(fields[:3], ">")))
		t := EgressTarget{ID: fmt.Sprintf("native:%d:%x", index, identity[:6]), Name: fields[0], Protocol: fields[1], Interface: nvget(prefix + "ifname")}
		t.Ready = fields[5] == "1" && nvget(prefix+"state_t") == "2"
		targets = append(targets, t)
	}

	for i := range targets {
		t := &targets[i]
		if !t.Ready || !ifaceRE.MatchString(t.Interface) {
			t.Ready = false
			continue
		}

		// Verify a live point-to-point/TUN interface. A stale native key must never
		// turn a physical WAN or LAN interface into a proxy route.
		link, err := runner("ip", "-o", "link", "show", "dev", t.Interface)
		if err != nil || !strings.Contains(link, "POINTOPOINT") || !strings.Contains(link, "UP") {
			t.Ready = false
			continue
		}

		output, err := runner("ip", "-4", "-o", "address", "show", "dev", t.Interface)
		if err != nil {
			t.Ready = false
			continue
		}

		fields := strings.Fields(output)
		for j, field := range fields {
			if field == "inet" && j+1 < len(fields) {
				ip, _, err := net.ParseCIDR(fields[j+1])
				if err == nil && ip.To4() != nil {
					t.Address = ip.String()
				}
			}
		}

		t.Ready = t.Address != ""
	}

	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	return targets
}

func validateEgress(reg EgressRegistration, networks []Network) (EgressRegistration, error) {
	if reg.Port < 1024 || reg.Port > 65535 || len(reg.Allowed) > 32 {
		return reg, errors.New("invalid egress listener or ACL")
	}

	found := false
	for _, n := range networks {
		if n.Bridge == reg.Bridge {
			reg.Address, reg.CIDR = n.Address, n.CIDR
			found = true
		}
	}

	if !found {
		return reg, errors.New("LAN bridge unavailable")
	}

	_, subnet, _ := net.ParseCIDR(reg.CIDR)
	for _, entry := range reg.Allowed {
		ip := net.ParseIP(entry)
		if ip != nil && ip.To4() != nil && subnet.Contains(ip) {
			continue
		}

		ip, network, err := net.ParseCIDR(entry)
		if err != nil || ip.To4() == nil || !subnet.Contains(ip) {
			return reg, errors.New("ACL address outside selected LAN")
		}

		a, _ := subnet.Mask.Size()
		c, _ := network.Mask.Size()
		if c < a {
			return reg, errors.New("ACL exceeds selected LAN")
		}
	}

	reg.UID = 0
	reg.Ready = false
	return reg, nil
}

func (b *Broker) egressRequest(request Request) (Response, error) {
	networks, err := resolverNetworks()
	if err != nil && request.Operation != "egress-release" {
		return Response{}, err
	}

	if request.Operation == "egress-list" {
		return Response{OK: true, Targets: b.egressTargets(), Networks: networks}, nil
	}

	if b.egress == nil {
		b.egress = map[string]*EgressLease{}
	}

	lease := b.egress[request.Client]
	if request.Operation == "egress-release" {
		if lease != nil {
			if egressUIDActive(lease.Registration.UID) {
				return Response{}, errors.New("stop egress worker before releasing its lease")
			}

			// The consumer must terminate its worker before releasing its lease.
			if err := lease.remove(); err != nil {
				return Response{}, err
			}

			delete(b.egress, request.Client)
			if err := b.saveEgress(); err != nil {
				b.egress[request.Client] = lease
				return Response{}, err
			}
		}

		return Response{OK: true}, nil
	}

	if request.Egress == nil {
		return Response{}, errors.New("egress registration required")
	}

	reg, err := validateEgress(*request.Egress, networks)
	if err != nil {
		return Response{}, err
	}

	targetFound := false
	for _, target := range b.egressTargets() {
		if target.ID == reg.Target {
			targetFound = true
		}
	}

	if !targetFound {
		return Response{}, errors.New("VPN target unavailable")
	}

	if lease != nil && (lease.Registration.Address != reg.Address || lease.Registration.CIDR != reg.CIDR || lease.Registration.Target != reg.Target || lease.Registration.Bridge != reg.Bridge || lease.Registration.Port != reg.Port || strings.Join(lease.Registration.Allowed, ",") != strings.Join(reg.Allowed, ",")) {
		return Response{}, errors.New("release previous egress before changing settings")
	}

	if lease == nil && len(b.egress) >= 16 {
		return Response{}, errors.New("egress lease capacity reached")
	}

	if lease == nil {
		lease, err = b.allocateEgress(request.Client, reg)
		if err != nil {
			return Response{}, err
		}

		b.egress[request.Client] = lease
	}

	addresses, err := localEgressAddresses()
	if err != nil {
		return Response{}, err
	}

	lease.Registration.Local = addresses
	lease.Heartbeat = time.Now()
	// Save identity/resources before enabling the worker, for provider recovery.
	if err := b.saveEgress(); err != nil {
		return Response{}, err
	}

	if err := lease.reconcile(b.egressTargets()); err != nil {
		return Response{}, err
	}

	return Response{OK: true, Egress: &lease.Registration}, nil
}

func (b *Broker) saveEgress() error {
	return saveJSON(filepath.Join(runDir, "egress.json"), b.egress)
}

func (b *Broker) allocateEgress(owner string, reg EgressRegistration) (*EgressLease, error) {
	rules, err := runner("ip", "-4", "rule", "show")
	if err != nil {
		return nil, err
	}

	ipv6, err := runner("ip", "-6", "rule", "show")
	if err != nil {
		return nil, err
	}
	rules += "\n" + ipv6
	lease := &EgressLease{Owner: owner, Registration: reg}
	reserved := map[string]bool{}
	for _, existing := range b.egress {
		reserved[existing.Table] = true
	}

	for table := 52200; table < 52300; table += 2 {
		name := strconv.Itoa(table)
		if reserved[name] {
			continue
		}

		free := true
		for _, family := range []string{"-4", "-6"} {
			for _, candidate := range []int{table, table + 1} {
				id := strconv.Itoa(candidate)
				contents, err := runner("ip", family, "route", "show", "table", id)
				if err != nil || strings.TrimSpace(contents) != "" || strings.Contains(rules, "lookup "+id) {
					free = false
				}
			}
		}

		if free {
			lease.Table = name
			break
		}
	}

	passwd, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil, err
	}

	used := map[int]bool{}
	for _, line := range strings.Split(string(passwd), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 2 {
			uid, _ := strconv.Atoi(fields[2])
			used[uid] = true
		}
	}

	for _, existing := range b.egress {
		used[existing.Registration.UID] = true
	}

	for uid := 60000; uid < 61000; uid++ {
		if !used[uid] && !egressUIDActive(uid) {
			lease.Registration.UID = uid
			break
		}
	}

	if lease.Table == "" || lease.Registration.UID == 0 {
		return nil, errors.New("no free egress resource")
	}

	return lease, nil
}

func (l *EgressLease) comment() string {
	return "ocfusion:egress:" + l.Owner
}

func (l *EgressLease) replyTable() string {
	table, _ := strconv.Atoi(l.Table)
	return strconv.Itoa(table + 1)
}

func (l *EgressLease) rule(tool, table, op, chain string, args ...string) error {
	args = append(args, "-m", "comment", "--comment", l.comment())
	return xt(tool, table, op, chain, args...)
}

func (l *EgressLease) policyRules() [][]string {
	r := l.Registration
	uid := fmt.Sprintf("%d-%d", r.UID, r.UID)
	// Equal-priority rules are appended after the native local lookup, before
	// stock DNS and VPN policies. The reply exception must precede the default.
	return [][]string{
		{"-4", "from", r.Address + "/32", "to", r.CIDR, "ipproto", "tcp", "sport", strconv.Itoa(r.Port), "uidrange", uid, "lookup", l.replyTable(), "priority", "0"},
		{"-4", "uidrange", uid, "lookup", l.Table, "priority", "0"},
		{"-6", "uidrange", uid, "lookup", l.Table, "priority", "0"},
	}
}

func egressRulePresent(output, uid, table string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		ownsUID := false
		ownsTable := false
		for i, field := range fields {
			if i+1 >= len(fields) {
				continue
			}

			if field == "uidrange" && (fields[i+1] == uid || fields[i+1] == uid+"-"+uid) {
				ownsUID = true
			}

			if field == "lookup" && fields[i+1] == table {
				ownsTable = true
			}
		}

		if ownsUID && ownsTable {
			return true
		}
	}

	return false
}

func (l *EgressLease) ensurePolicies() error {
	uid := strconv.Itoa(l.Registration.UID)
	rules := map[string]string{}
	complete := true
	for _, family := range []string{"-4", "-6"} {
		output, err := runner("ip", family, "rule", "show")
		if err != nil {
			return err
		}

		if err := l.checkPriority(output); err != nil {
			return err
		}
		rules[family] = output
	}

	for _, rule := range l.policyRules() {
		if !egressRulePresent(rules[rule[0]], uid, rule[len(rule)-3]) {
			complete = false
		}
	}

	if !complete {
		// Temporary terminal rules keep repairs fail-closed while reordering
		// this UID's selectors. They remain after an interrupted repair.
		for _, family := range []string{"-4", "-6"} {
			if _, err := runner("ip", family, "rule", "add", "priority", "0", "uidrange", uid+"-"+uid, "prohibit"); err != nil {
				return err
			}
		}

		for _, rule := range l.policyRules() {
			table := rule[len(rule)-3]
			if egressRulePresent(rules[rule[0]], uid, table) {
				args := append([]string{rule[0], "rule", "del"}, rule[1:]...)
				if _, err := runner("ip", args...); err != nil {
					return err
				}
			}
		}

		for _, rule := range l.policyRules() {
			args := append([]string{rule[0], "rule", "add"}, rule[1:]...)
			if _, err := runner("ip", args...); err != nil {
				return err
			}
		}
	}

	return l.removeTemporaryRules()
}

func localEgressAddresses() ([]string, error) {
	output, err := runner("ip", "-4", "-o", "address", "show")
	if err != nil {
		return nil, err
	}

	addresses := []string{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for i, field := range fields {
			if field == "inet" && i+1 < len(fields) {
				ip, _, err := net.ParseCIDR(fields[i+1])
				if err == nil {
					addresses = append(addresses, ip.String())
				}
			}
		}
	}

	sort.Strings(addresses)
	return addresses, nil
}

func (l *EgressLease) reconcile(targets []EgressTarget) error {
	r := &l.Registration
	r.Ready = false
	// Both families terminate lookup even when the tunnel is absent. Unlike
	// firewall marks, UID rules survive a native firewall ruleset reset.
	for _, family := range []string{"-4", "-6"} {
		if _, err := runner("ip", family, "route", "replace", "unreachable", "default", "table", l.Table, "metric", "32760"); err != nil {
			return err
		}
	}

	if _, err := runner("ip", "-4", "route", "replace", "unreachable", "default", "table", l.replyTable(), "metric", "32760"); err != nil {
		return err
	}

	if _, err := runner("ip", "-4", "route", "replace", r.CIDR, "dev", r.Bridge, "src", r.Address, "table", l.replyTable()); err != nil {
		return err
	}

	if err := l.ensurePolicies(); err != nil {
		return err
	}

	target := EgressTarget{}
	if time.Since(l.Heartbeat) <= 12*time.Second {
		for _, item := range targets {
			if item.ID == r.Target && item.Ready {
				target = item
			}
		}
	}

	if !target.Ready {
		routes, err := runner("ip", "-4", "route", "show", "table", l.Table)
		if err != nil {
			return err
		}

		for _, line := range strings.Split(routes, "\n") {
			if strings.HasPrefix(line, "default ") && strings.Contains(line, "metric 10") {
				if _, err := runner("ip", "-4", "route", "del", "default", "table", l.Table, "metric", "10"); err != nil {
					return err
				}
			}
		}
	} else if _, err := runner("ip", "-4", "route", "replace", "default", "dev", target.Interface, "src", target.Address, "table", l.Table, "metric", "10"); err != nil {
		return err
	}

	if err := l.listenerRules("add"); err != nil {
		return err
	}

	r.Tunnel = target.Interface + ":" + target.Address
	r.Ready = target.Ready
	l.Interface = target.Interface
	return nil
}

func (l *EgressLease) listenerRules(operation string) error {
	r := l.Registration
	port := strconv.Itoa(r.Port)
	if err := l.rule("iptables", "filter", operation, "INPUT", "-d", r.Address, "-p", "tcp", "--dport", port, "-j", "REJECT"); err != nil {
		return err
	}

	allowed := r.Allowed
	if len(allowed) == 0 {
		allowed = []string{r.CIDR}
	}

	for _, source := range allowed {
		if err := l.rule("iptables", "filter", operation, "INPUT", "-i", r.Bridge, "-s", source, "-d", r.Address, "-p", "tcp", "--dport", port, "-j", "ACCEPT"); err != nil {
			return err
		}
	}

	return nil
}

func (b *Broker) reconcileEgress() {
	targets := b.egressTargets()
	for _, lease := range b.egress {
		lease.reconcile(targets)
	}

	b.saveEgress()
}

func (l *EgressLease) remove() error {
	if err := l.listenerRules("del"); err != nil {
		return err
	}

	uid := strconv.Itoa(l.Registration.UID)
	for _, rule := range l.policyRules() {
		output, err := runner("ip", rule[0], "rule", "show")
		if err != nil {
			return err
		}

		if !egressRulePresent(output, uid, rule[len(rule)-3]) {
			continue
		}

		args := append([]string{rule[0], "rule", "del"}, rule[1:]...)
		if _, err := runner("ip", args...); err != nil {
			return err
		}
	}

	for _, family := range []string{"-4", "-6"} {
		for _, table := range []string{l.Table, l.replyTable()} {
			output, err := runner("ip", family, "route", "show", "table", table)
			if err != nil {
				return err
			}

			for _, line := range strings.Split(output, "\n") {
				args := []string{family, "route", "del"}
				switch {
				case strings.HasPrefix(line, "unreachable default") && strings.Contains(line, "metric 32760"):
					args = append(args, "unreachable", "default", "metric", "32760")
				case table == l.Table && strings.HasPrefix(line, "default ") && strings.Contains(line, "metric 10"):
					args = append(args, "default", "metric", "10")
				case table == l.replyTable() && strings.HasPrefix(line, l.Registration.CIDR+" ") && strings.Contains(line, "dev "+l.Registration.Bridge+" "):
					args = append(args, l.Registration.CIDR, "dev", l.Registration.Bridge)
				default:
					continue
				}

				args = append(args, "table", table)
				if _, err := runner("ip", args...); err != nil {
					return err
				}
			}
		}
	}

	return l.removeTemporaryRules()
}

func (l *EgressLease) removeTemporaryRules() error {
	uid := strconv.Itoa(l.Registration.UID)
	for _, family := range []string{"-4", "-6"} {
		output, err := runner("ip", family, "rule", "show")
		if err != nil {
			return err
		}

		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			own := false
			for i, field := range fields {
				if field == "uidrange" && i+1 < len(fields) && (fields[i+1] == uid || fields[i+1] == uid+"-"+uid) {
					own = true
				}
			}

			if strings.HasPrefix(strings.TrimSpace(line), "0:") && own && strings.Contains(line, "prohibit") {
				if _, err := runner("ip", family, "rule", "del", "priority", "0", "uidrange", uid+"-"+uid, "prohibit"); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (b *Broker) recoverEgress() error {
	data, err := os.ReadFile(filepath.Join(runDir, "egress.json"))
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil || len(data) > 65536 || json.Unmarshal(data, &b.egress) != nil {
		return errors.New("invalid saved egress leases")
	}

	used := map[string]bool{}
	for owner, lease := range b.egress {
		if lease == nil || !identifier.MatchString(owner) || lease.Owner != owner {
			return errors.New("invalid saved egress owner")
		}

		r := lease.Registration
		table, _ := strconv.Atoi(lease.Table)
		ip, subnet, ipErr := net.ParseCIDR(r.CIDR)
		if r.UID < 60000 || r.UID >= 61000 || table < 52200 || table >= 52300 || table%2 != 0 ||
			!ifaceRE.MatchString(r.Bridge) || ipErr != nil || ip.To4() == nil || !subnet.Contains(net.ParseIP(r.Address)) {
			return errors.New("invalid saved egress resources")
		}

		if _, err := validateEgress(r, []Network{{Bridge: r.Bridge, Address: r.Address, CIDR: r.CIDR}}); err != nil {
			return err
		}

		for _, key := range []string{"uid:" + strconv.Itoa(r.UID), "table:" + lease.Table} {
			if used[key] {
				return errors.New("duplicate saved egress resource")
			}

			used[key] = true
		}
	}

	for _, lease := range b.egress {
		lease.Heartbeat = time.Time{}
		if err := lease.reconcile(nil); err != nil {
			return errors.New("cannot recover blocked egress routing")
		}
	}

	return nil
}

func egressUIDActive(uid int) bool {
	paths, _ := filepath.Glob("/proc/[0-9]*/status")
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "Uid:") {
				continue
			}

			for _, field := range strings.Fields(line)[1:] {
				value, _ := strconv.Atoi(field)
				if value == uid {
					return true
				}
			}
		}
	}

	return false
}

func (l *EgressLease) checkPriority(output string) error {
	uid := l.Registration.UID
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "0:" {
			continue
		}
		if strings.Contains(line, "from all lookup local") {
			continue
		}
		scoped := false
		for i, field := range fields {
			if field != "uidrange" || i+1 >= len(fields) {
				continue
			}
			values := strings.Split(fields[i+1], "-")
			low, _ := strconv.Atoi(values[0])
			high := low
			if len(values) == 2 {
				high, _ = strconv.Atoi(values[1])
			}
			if uid < low || uid > high {
				scoped = true
				break
			}
			if low == uid && high == uid && (egressRulePresent(line, strconv.Itoa(uid), l.Table) ||
				egressRulePresent(line, strconv.Itoa(uid), l.replyTable()) || strings.Contains(line, "prohibit")) {
				scoped = true
			}
		}
		if !scoped {
			return errors.New("an earlier routing policy conflicts with isolated egress")
		}
	}
	return nil
}
