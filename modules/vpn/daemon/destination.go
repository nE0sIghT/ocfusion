// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DestinationPolicy struct {
	ID       string   `json:"id"`
	Action   string   `json:"action"`
	Target   string   `json:"target,omitempty"`
	Failure  string   `json:"failure,omitempty"`
	Networks []string `json:"networks"`
	Domains  []string `json:"domains"`
}

type DestinationRoute struct {
	Mark      string    `json:"mark"`
	Table     string    `json:"table"`
	Priority  string    `json:"priority"`
	Interface string    `json:"interface,omitempty"`
	Source    string    `json:"source,omitempty"`
	Applied   string    `json:"-"`
	Repaired  time.Time `json:"-"`
}

type DestinationLease struct {
	Owner     string                       `json:"owner"`
	Policies  []DestinationPolicy          `json:"policies"`
	Routes    map[string]*DestinationRoute `json:"routes"`
	Heartbeat time.Time                    `json:"heartbeat"`
	Applied   string                       `json:"-"`
	Repaired  time.Time                    `json:"-"`
	State     []DestinationStatus          `json:"-"`
}

type DestinationStatus struct {
	Failed    int    `json:"failed,omitempty"`
	Interface string `json:"interface,omitempty"`
	Source    string `json:"source,omitempty"`
	Mark      string `json:"mark,omitempty"`
	ID        string `json:"id"`
	State     string `json:"state"`
	Addresses int    `json:"addresses"`
	Pending   int    `json:"pending"`
	Stale     int    `json:"stale"`
}

func validateDestinationPolicies(policies []DestinationPolicy) error {
	if len(policies) > 7 {
		return errors.New("too many destination policies")
	}
	seen := map[string]bool{}
	total := 0
	for _, policy := range policies {
		if !identifier.MatchString(policy.ID) || seen[policy.ID] {
			return errors.New("invalid destination policy identity")
		}
		seen[policy.ID] = true
		if policy.Action != "vpn" && policy.Action != "dpi" && policy.Action != "normal" {
			return errors.New("invalid destination action")
		}
		if policy.Action == "vpn" {
			if len(policy.Target) > 128 || !(strings.HasPrefix(policy.Target, "managed:") || strings.HasPrefix(policy.Target, "native:")) || (policy.Failure != "block" && policy.Failure != "wan") {
				return errors.New("invalid destination VPN")
			}
		} else if policy.Target != "" || policy.Failure != "" {
			return errors.New("VPN settings require a VPN action")
		}
		if len(policy.Networks) > 16384 || len(policy.Domains) > 2048 {
			return errors.New("destination selection exceeds limits")
		}
		total += len(policy.Domains)
		for _, value := range policy.Networks {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() || !prefix.Addr().IsGlobalUnicast() || prefix != prefix.Masked() {
				return errors.New("invalid destination prefix")
			}
		}
		for _, host := range policy.Domains {
			if !validDestinationDomain(host) {
				return errors.New("invalid destination domain")
			}
		}
	}
	if total > 4096 {
		return errors.New("too many destination domains")
	}
	return nil
}

func (b *Broker) destinationRequest(request Request) (Response, error) {
	if request.Operation == "destination-list" {
		return Response{OK: true, Targets: b.egressTargets()}, nil
	}
	if b.destinations == nil {
		b.destinations = map[string]*DestinationLease{}
	}
	lease := b.destinations[request.Client]
	if request.Operation == "destination-probe" {
		return b.destinationProbe(lease, request.DestinationProbe)
	}
	if request.Operation == "destination-release" {
		if lease != nil {
			if err := lease.remove(); err != nil {
				return Response{}, err
			}
			delete(b.destinations, request.Client)
			if err := b.saveDestinations(); err != nil {
				lease.Heartbeat = time.Time{}
				b.destinations[request.Client] = lease
				return Response{}, err
			}
		}
		return Response{OK: true}, nil
	}
	if err := validateDestinationPolicies(request.Destinations); err != nil {
		return Response{}, err
	}
	if len(request.Destinations) == 0 {
		return Response{}, errors.New("empty destination lease")
	}
	allDomains := map[string]bool{}
	for owner, active := range b.destinations {
		if owner == request.Client {
			continue
		}
		for _, policy := range active.Policies {
			for _, host := range policy.Domains {
				allDomains[host] = true
			}
		}
	}
	for _, policy := range request.Destinations {
		for _, host := range policy.Domains {
			allDomains[host] = true
		}
	}
	if len(allDomains) > 4096 {
		return Response{}, errors.New("destination DNS capacity reached")
	}
	previous := lease
	if lease == nil {
		if len(b.destinations) >= 4 {
			return Response{}, errors.New("destination lease capacity reached")
		}
		lease = &DestinationLease{Owner: request.Client}
	}
	candidate := *lease
	candidate.Routes = map[string]*DestinationRoute{}
	for id, route := range lease.Routes {
		candidate.Routes[id] = route
	}
	lease = &candidate
	b.destinations[request.Client] = lease
	journaled := false
	defer func() {
		if !journaled {
			if previous == nil {
				delete(b.destinations, request.Client)
			} else {
				b.destinations[request.Client] = previous
			}
		}
	}()
	changed := !reflect.DeepEqual(lease.Policies, request.Destinations)
	// A heartbeat renews existing selectors without rebuilding sets/chains.
	lease.Heartbeat = time.Now()
	lease.Policies = request.Destinations
	for _, policy := range lease.Policies {
		if policy.Action == "vpn" && lease.Routes[policy.ID] == nil {
			route, err := b.allocateDestinationRoute()
			if err != nil {
				return Response{}, err
			}
			lease.Routes[policy.ID] = route
			changed = true
		}
	}
	// Journal every allocation before touching kernel state.
	if changed {
		if err := b.saveDestinations(); err != nil {
			return Response{}, err
		}
	}
	journaled = true
	if err := b.reconcileDestinations(); err != nil {
		return Response{}, err
	}
	return Response{OK: true, DestinationStatus: lease.State, DestinationMask: lease.mask()}, nil
}

func (b *Broker) saveDestinations() error {
	return saveJSON(filepath.Join(runDir, "destinations.json"), b.destinations)
}

func (l *DestinationLease) addresses(policy DestinationPolicy, dns *DestinationDNS) ([]string, DestinationStatus) {
	seen := map[string]bool{}
	status := DestinationStatus{ID: policy.ID}
	for _, prefix := range policy.Networks {
		seen[prefix] = true
	}
	for _, host := range policy.Domains {
		answer := dns.addresses(host)
		if len(answer.Addresses) == 0 {
			status.Pending++
		}
		if answer.Failed {
			status.Failed++
			if answer.Retained {
				status.Stale++
			}
		}
		for _, prefix := range answer.Addresses {
			seen[prefix] = true
		}
	}
	addresses := []string{}
	for prefix := range seen {
		addresses = append(addresses, prefix)
	}
	sort.Strings(addresses)
	status.Addresses = len(addresses)
	return addresses, status
}

func (b *Broker) reconcileDestinations() error {
	domains := map[string]bool{}
	for _, lease := range b.destinations {
		for _, policy := range lease.Policies {
			for _, host := range policy.Domains {
				domains[host] = true
			}
		}
	}
	wanted := []string{}
	for host := range domains {
		wanted = append(wanted, host)
	}
	sort.Strings(wanted)
	b.destinationDNS.refresh(wanted)
	if len(b.destinations) == 0 {
		return nil
	}
	exclusions, err := destinationExclusions()
	if err != nil {
		for _, lease := range b.destinations {
			for _, route := range lease.Routes {
				route.reconcile(EgressTarget{}, false)
			}
		}
		return err
	}
	bridges := destinationLANInterfaces()
	if len(bridges) == 0 {
		return errors.New("LAN network unavailable")
	}
	targets := b.egressTargets()
	for _, lease := range b.destinations {
		for _, policy := range lease.Policies {
			if policy.Action != "vpn" {
				continue
			}
			target := EgressTarget{}
			fresh := time.Since(lease.Heartbeat) <= 12*time.Second
			for _, candidate := range targets {
				if candidate.ID == policy.Target && fresh && candidate.Ready {
					target = candidate
				}
			}
			route := lease.Routes[policy.ID]
			if err := route.reconcile(target, !target.Ready && fresh && policy.Failure == "wan"); err != nil {
				return err
			}
			if target.Ready {
				mark := route.Mark
				for _, bridge := range bridges {
					if err := xt("iptables", "filter", "add", "FORWARD", "-i", bridge, "-o", target.Interface, "-m", "mark", "--mark", mark, "-m", "comment", "--comment", lease.comment(), "-j", "ACCEPT"); err != nil {
						return err
					}
					if err := xt("iptables", "filter", "add", "FORWARD", "-i", target.Interface, "-o", bridge, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-m", "comment", "--comment", lease.comment(), "-j", "ACCEPT"); err != nil {
						return err
					}
				}
				if err := xt("iptables", "nat", "add", "POSTROUTING", "-o", target.Interface, "-m", "mark", "--mark", mark, "-m", "comment", "--comment", lease.comment(), "-j", "MASQUERADE"); err != nil {
					return err
				}
				if err := xt("iptables", "mangle", "add", "FORWARD", "-o", target.Interface, "-m", "mark", "--mark", mark, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-m", "comment", "--comment", lease.comment(), "-j", "TCPMSS", "--clamp-mss-to-pmtu"); err != nil {
					return err
				}
			}
		}
		if err := lease.install(&b.destinationDNS, exclusions, bridges); err != nil {
			return err
		}
		if err := b.pruneDestinationRoutes(lease); err != nil {
			return err
		}
	}
	return nil
}

func (b *Broker) recoverDestinations() error {
	data, err := os.ReadFile(filepath.Join(runDir, "destinations.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || len(data) > 8<<20 || json.Unmarshal(data, &b.destinations) != nil || len(b.destinations) > 4 {
		return errors.New("invalid destination recovery record")
	}
	used := map[string]bool{}
	for owner, lease := range b.destinations {
		if lease == nil || owner != lease.Owner || !identifier.MatchString(owner) || validateDestinationPolicies(lease.Policies) != nil || len(lease.Routes) > 14 {
			return errors.New("invalid destination recovery owner")
		}
		for _, route := range lease.Routes {
			if route == nil {
				return errors.New("invalid destination recovery route")
			}
			table, _ := strconv.Atoi(route.Table)
			priority, _ := strconv.Atoi(route.Priority)
			parts := strings.Split(route.Mark, "/")
			mark := uint64(0)
			if len(parts) == 2 && parts[0] == parts[1] {
				mark, _ = strconv.ParseUint(parts[0], 0, 32)
			}
			if table < 52400 || table >= 52500 || priority < 1 || priority >= 20 || mark < 1<<16 || mark > 1<<30 || mark&(mark-1) != 0 || used["t"+route.Table] || used["p"+route.Priority] || used["m"+route.Mark] {
				return errors.New("invalid destination recovery resource")
			}
			used["t"+route.Table], used["p"+route.Priority], used["m"+route.Mark] = true, true, true
			if err := route.reconcile(EgressTarget{}, false); err != nil {
				return err
			}
		}
		for _, policy := range lease.Policies {
			if policy.Action == "vpn" && lease.Routes[policy.ID] == nil {
				return errors.New("missing destination recovery resource")
			}
		}
		lease.Heartbeat = time.Time{}
	}
	return nil
}

func (b *Broker) pruneDestinationRoutes(lease *DestinationLease) error {
	wanted := map[string]bool{}
	for _, policy := range lease.Policies {
		if policy.Action == "vpn" {
			wanted[policy.ID] = true
		}
	}
	changed := false
	for id, route := range lease.Routes {
		if !wanted[id] {
			if err := route.remove(); err != nil {
				return err
			}
			delete(lease.Routes, id)
			changed = true
		}
	}
	if changed {
		lease.Applied = ""
		return b.saveDestinations()
	}
	return nil
}

func (b *Broker) destinationProbe(lease *DestinationLease, address string) (Response, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() {
		return Response{}, errors.New("invalid destination probe")
	}
	if err := b.reconcileDestinations(); err != nil {
		return Response{}, err
	}
	if lease != nil {
		for _, policy := range lease.Policies {
			_, err := runner("ipset", "test", lease.set(policy.ID, 4), ip.String())
			if err != nil {
				var exit *exec.ExitError
				if errors.As(err, &exit) && exit.ExitCode() == 1 {
					continue
				}
				return Response{}, errors.New("destination selection unavailable")
			}
			for _, status := range lease.State {
				if status.ID == policy.ID {
					return Response{OK: true, DestinationStatus: []DestinationStatus{status}}, nil
				}
			}
		}
	}
	return Response{OK: true, DestinationStatus: []DestinationStatus{{State: "NORMAL"}}}, nil
}
