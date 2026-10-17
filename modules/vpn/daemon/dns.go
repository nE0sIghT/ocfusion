// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type DNSPolicy struct {
	Client  string `json:"client,omitempty"`
	Profile string `json:"profile,omitempty"`
}

type DNSStatus struct {
	Policy DNSPolicy `json:"policy"`
	Path   string    `json:"path"`
	Reason string    `json:"reason,omitempty"`
}

type DNSEndpoint struct {
	Address  string
	Port     int
	Protocol string
}

type DNSRouter struct {
	policy    DNSPolicy
	active    string
	endpoints []DNSEndpoint
	mark      string
	table     string
	priority  string
	status    DNSStatus
}

var dnsConfigRoot = "/etc"
var dnsResolvRoot = "/tmp"

func readDNSPolicy() DNSPolicy {
	var policy DNSPolicy
	data, err := os.ReadFile(filepath.Join(root, "dns-policy.json"))
	if err == nil {
		json.Unmarshal(data, &policy)
	}

	return policy
}

func (d *DNSRouter) selectPolicy(policy DNSPolicy) error {
	if policy.Client != "" && (!identifier.MatchString(policy.Client) || !identifier.MatchString(policy.Profile)) {
		return errors.New("invalid DNS connection owner")
	}

	if err := saveJSON(filepath.Join(root, "dns-policy.json"), policy); err != nil {
		return err
	}

	d.policy = policy
	return nil
}

func dnsEndpoint(address string, port int, protocol string) (DNSEndpoint, bool) {
	ip := net.ParseIP(address)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || port < 1 || port > 65535 {
		return DNSEndpoint{}, false
	}

	return DNSEndpoint{Address: ip.String(), Port: port, Protocol: protocol}, true
}

func discoverDNS() ([]DNSEndpoint, error) {
	paths, _ := filepath.Glob(filepath.Join(dnsConfigRoot, "dnsmasq*.conf"))
	stubby, _ := filepath.Glob(filepath.Join(dnsConfigRoot, "stubby", "*.yml"))
	paths = append(paths, stubby...)
	visited := map[string]bool{}
	endpoints := map[DNSEndpoint]bool{}

	for len(paths) > 0 {
		path := paths[0]
		paths = paths[1:]
		if visited[path] {
			continue
		}

		visited[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("native DNS configuration unavailable")
		}

		address := ""
		port := 853
		flushTLS := func() {
			if endpoint, valid := dnsEndpoint(address, port, "tcp"); valid {
				endpoints[endpoint] = true
			}
		}

		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasSuffix(path, ".yml") {
				if strings.HasPrefix(line, "- address_data:") {
					flushTLS()
					address = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "- address_data:")), "\"'")
					port = 853
				} else if strings.HasPrefix(line, "tls_port:") {
					port, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "tls_port:")))
				}

				continue
			}

			for _, key := range []string{"servers-file=", "resolv-file="} {
				if strings.HasPrefix(line, key) {
					next := strings.TrimPrefix(line, key)
					if filepath.IsAbs(next) && (strings.HasPrefix(next, dnsConfigRoot+"/") || strings.HasPrefix(next, dnsResolvRoot+"/")) {
						paths = append(paths, next)
					}
				}
			}

			target := ""
			if strings.HasPrefix(line, "nameserver ") {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					target = fields[1]
				}
			} else if strings.HasPrefix(line, "server=") {
				target = strings.TrimPrefix(line, "server=")
				if strings.Contains(target, "/") {
					target = target[strings.LastIndex(target, "/")+1:]
				}
			}

			pieces := strings.Split(target, "#")
			dnsPort := 53
			if len(pieces) == 2 {
				dnsPort, _ = strconv.Atoi(pieces[1])
			}

			for _, protocol := range []string{"udp", "tcp"} {
				if endpoint, valid := dnsEndpoint(pieces[0], dnsPort, protocol); valid {
					endpoints[endpoint] = true
				}
			}
		}

		if strings.HasSuffix(path, ".yml") {
			flushTLS()
		}
	}

	result := make([]DNSEndpoint, 0, len(endpoints))
	for endpoint := range endpoints {
		result = append(result, endpoint)
	}

	sort.Slice(result, func(i, j int) bool {
		return fmt.Sprint(result[i]) < fmt.Sprint(result[j])
	})

	if len(result) == 0 {
		return nil, errors.New("no native DNS upstreams discovered")
	}

	return result, nil
}

var restoreMaskPattern = regexp.MustCompile(`--nfmask\s+(0x[0-9a-fA-F]+|[0-9]+)`)

var markPattern = regexp.MustCompile(`(?:fwmark|--set-xmark|--set-mark|--mark)\s+(0x[0-9a-fA-F]+|[0-9]+)(?:/(0x[0-9a-fA-F]+|[0-9]+))?`)

func usedDNSMask(rules string) uint64 {
	used := uint64(0)
	for _, match := range markPattern.FindAllStringSubmatch(rules, -1) {
		value := uint64(0xffffffff)
		if match[2] != "" {
			value, _ = strconv.ParseUint(match[2], 0, 32)
		}

		used |= value
	}

	for _, line := range strings.Split(rules, "\n") {
		if !strings.Contains(line, "--restore-mark") {
			continue
		}

		mask := uint64(0xffffffff)
		if match := restoreMaskPattern.FindStringSubmatch(line); match != nil {
			mask, _ = strconv.ParseUint(match[1], 0, 32)
		}

		used |= mask
	}

	return used
}

func unusedDNSMark(rules string) string {
	used := usedDNSMask(rules)
	for bit := uint64(1 << 28); bit >= 1<<16; bit >>= 1 {
		if used&bit == 0 {
			return fmt.Sprintf("0x%x/0x%x", bit, bit)
		}
	}

	return ""
}

func (d *DNSRouter) allocate() error {
	rules, err := runner("ip", "rule", "show")
	if err != nil {
		return err
	}

	mangle, err := runner("iptables", "-t", "mangle", "-S")
	if err != nil {
		return err
	}

	if d.mark != "" {
		foreign := []string{}
		for _, line := range strings.Split(mangle, "\n") {
			if !strings.Contains(line, "ocfusion:dns-route") {
				foreign = append(foreign, line)
			}
		}
		for _, line := range strings.Split(rules, "\n") {
			if !strings.Contains(line, "fwmark "+d.mark+" lookup "+d.table) {
				foreign = append(foreign, line)
				if strings.HasPrefix(strings.TrimSpace(line), d.priority+":") {
					return errors.New("DNS routing priority was claimed by another owner")
				}
			}
		}
		bit, _ := strconv.ParseUint(strings.Split(d.mark, "/")[0], 0, 32)
		if usedDNSMask(strings.Join(foreign, "\n"))&bit != 0 {
			return errors.New("DNS routing mark was claimed by another owner")
		}
		return nil
	}

	d.mark = unusedDNSMark(rules + "\n" + mangle)
	if d.mark == "" {
		return errors.New("no free DNS routing mark")
	}

	for priority := 1; priority < 20; priority++ {
		if !regexp.MustCompile(fmt.Sprintf(`(?m)^\s*%d:`, priority)).MatchString(rules) {
			d.priority = strconv.Itoa(priority)
			break
		}
	}

	for table := 52100; table < 52120; table++ {
		name := strconv.Itoa(table)
		contents, err := runner("ip", "route", "show", "table", name)
		if err == nil && strings.TrimSpace(contents) == "" && !strings.Contains(rules, "lookup "+name) {
			d.table = name
			break
		}
	}

	if d.priority == "" || d.table == "" {
		d.mark = ""
		return errors.New("no free DNS routing resource")
	}

	return nil
}

func (d *DNSRouter) endpointRule(endpoint DNSEndpoint, operation string) error {
	return xt("iptables", "mangle", operation, "OUTPUT", "-d", endpoint.Address, "-p", endpoint.Protocol, "--dport", strconv.Itoa(endpoint.Port), "-m", "addrtype", "!", "--dst-type", "LOCAL", "-m", "comment", "--comment", "ocfusion:dns-route", "-j", "MARK", "--set-xmark", d.mark)
}

func (d *DNSRouter) removeRoutingRule() error {
	_, err := runner("ip", "rule", "del", "fwmark", d.mark, "lookup", d.table, "priority", d.priority)
	if err == nil {
		return nil
	}

	rules, readErr := runner("ip", "rule", "show")
	if readErr != nil {
		return errors.Join(err, readErr)
	}

	for _, line := range strings.Split(rules, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), d.priority+":") && strings.Contains(line, "fwmark "+d.mark+" lookup "+d.table) {
			return err
		}
	}

	return nil
}

func (d *DNSRouter) removeDefaultRoute() error {
	_, err := runner("ip", "route", "del", "default", "dev", d.active, "table", d.table)
	if err == nil {
		return nil
	}

	routes, readErr := runner("ip", "route", "show", "table", d.table)
	if readErr != nil {
		return errors.Join(err, readErr)
	}

	for _, line := range strings.Split(routes, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "default ") && strings.Contains(" "+line+" ", " dev "+d.active+" ") {
			return err
		}
	}

	return nil
}

func (d *DNSRouter) reset() error {
	var failure error
	for _, endpoint := range d.endpoints {
		failure = errors.Join(failure, d.endpointRule(endpoint, "del"))
	}

	if d.active != "" {
		failure = errors.Join(failure, xt("iptables", "nat", "del", "POSTROUTING", "-o", d.active, "-m", "mark", "--mark", d.mark, "-m", "comment", "--comment", "ocfusion:dns-nat", "-j", "MASQUERADE"))
		failure = errors.Join(failure, d.removeRoutingRule(), d.removeDefaultRoute())
	}

	if failure != nil {
		d.status.Path = "ERROR"
		d.status.Reason = "DNS rule cleanup failed; retrying"
		return failure
	}

	d.active = ""
	d.endpoints = nil
	os.Remove(filepath.Join(runDir, "dns-routing.json"))
	return nil
}

func (d *DNSRouter) reconcile(services map[string]*Service) {
	d.status = DNSStatus{Policy: d.policy, Path: "WAN"}
	desired := ""
	for _, service := range services {
		if service.manifest.Client == d.policy.Client && service.manifest.Profile == d.policy.Profile && service.event != nil {
			desired = service.manifest.Interface
		}
	}

	if desired == "" {
		if d.active != "" {
			for _, endpoint := range d.endpoints {
				deleteLocalDNSFlows(endpoint)
			}

			if d.reset() != nil {
				return
			}
			runner("service", "restart_stubby")
		}

		if d.policy.Client != "" {
			d.status.Reason = "VPN unavailable"
		}

		return
	}

	endpoints, err := discoverDNS()
	if err == nil {
		for _, endpoint := range endpoints {
			if net.ParseIP(endpoint.Address).To4() == nil {
				err = errors.New("native IPv6 DNS requires WAN")
				break
			}
		}
	}

	if err == nil {
		err = d.allocate()
	}

	if err != nil {
		d.reset()
		d.status.Reason = err.Error()
		return
	}

	changed := desired != d.active || fmt.Sprint(endpoints) != fmt.Sprint(d.endpoints)
	if changed {
		if d.reset() != nil {
			return
		}
		d.active = desired
		d.endpoints = endpoints
		if err := d.saveResources(); err != nil {
			d.reset()
			d.status.Reason = "DNS recovery record unavailable"
			return
		}
	}

	_, err = runner("ip", "route", "replace", "default", "dev", desired, "table", d.table)
	if err == nil {
		rules, readErr := runner("ip", "rule", "show")
		err = readErr
		if err == nil && !strings.Contains(rules, "fwmark "+d.mark+" lookup "+d.table) {
			_, err = runner("ip", "rule", "add", "fwmark", d.mark, "lookup", d.table, "priority", d.priority)
		}
	}

	if err == nil {
		err = xt("iptables", "nat", "add", "POSTROUTING", "-o", desired, "-m", "mark", "--mark", d.mark, "-m", "comment", "--comment", "ocfusion:dns-nat", "-j", "MASQUERADE")
	}

	for _, endpoint := range endpoints {
		if err != nil {
			break
		}

		err = d.endpointRule(endpoint, "add")
	}

	if err != nil {
		d.reset()
		d.status.Reason = "DNS routing preparation failed"
		return
	}

	if changed {
		for _, endpoint := range endpoints {
			deleteLocalDNSFlows(endpoint)
		}

		runner("service", "restart_stubby")
	}

	d.status.Path = "VPN"
}

func deleteLocalDNSFlows(endpoint DNSEndpoint) {
	addresses, err := runner("ip", "-4", "address", "show")
	if err != nil {
		return
	}

	fields := strings.Fields(addresses)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "inet" {
			continue
		}

		address := strings.Split(fields[i+1], "/")[0]
		if net.ParseIP(address) != nil {
			runner("/usr/sbin/conntrack", "-D", "-p", endpoint.Protocol, "--orig-src", address, "--orig-dst", endpoint.Address, "--dport", strconv.Itoa(endpoint.Port))
		}
	}
}

type dnsResources struct {
	Interface string        `json:"interface"`
	Mark      string        `json:"mark"`
	Table     string        `json:"table"`
	Priority  string        `json:"priority"`
	Endpoints []DNSEndpoint `json:"endpoints"`
}

func (d *DNSRouter) saveResources() error {
	return saveJSON(filepath.Join(runDir, "dns-routing.json"), dnsResources{
		Interface: d.active, Mark: d.mark, Table: d.table,
		Priority: d.priority, Endpoints: d.endpoints,
	})
}

func recoverDNSRouting() {
	data, err := os.ReadFile(filepath.Join(runDir, "dns-routing.json"))
	if err != nil {
		return
	}

	var resources dnsResources
	if json.Unmarshal(data, &resources) != nil || !ifaceRE.MatchString(resources.Interface) {
		return
	}

	table, _ := strconv.Atoi(resources.Table)
	priority, _ := strconv.Atoi(resources.Priority)
	if table < 52100 || table >= 52120 || priority < 1 || priority >= 20 || !regexp.MustCompile(`^0x[0-9a-f]+/0x[0-9a-f]+$`).MatchString(resources.Mark) {
		return
	}

	for _, endpoint := range resources.Endpoints {
		if _, valid := dnsEndpoint(endpoint.Address, endpoint.Port, endpoint.Protocol); !valid || (endpoint.Protocol != "tcp" && endpoint.Protocol != "udp") {
			return
		}
	}

	previous := DNSRouter{active: resources.Interface, mark: resources.Mark, table: resources.Table, priority: resources.Priority, endpoints: resources.Endpoints}
	previous.reset()
	runner("service", "restart_stubby")
}
