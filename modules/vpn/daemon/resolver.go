// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ResolverRegistration struct {
	Port     int       `json:"port"`
	Mark     string    `json:"mark,omitempty"`
	Networks []Network `json:"networks,omitempty"`
}

type ResolverLease struct {
	Owner        string
	Registration ResolverRegistration
	Heartbeat    time.Time
}

func resolverNetworks() ([]Network, error) {
	output, err := runner("ip", "-4", "address", "show")
	if err != nil {
		return nil, err
	}

	result := []Network{}
	bridge := ""
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasSuffix(fields[0], ":") {
			bridge = strings.TrimSuffix(strings.Split(fields[1], "@")[0], ":")
		}

		if len(fields) < 2 || fields[0] != "inet" || !strings.HasPrefix(bridge, "br") || !ifaceRE.MatchString(bridge) {
			continue
		}

		address, subnet, err := net.ParseCIDR(fields[1])
		if err != nil || address.To4() == nil {
			continue
		}

		result = append(result, Network{Bridge: bridge, Address: address.String(), CIDR: subnet.String()})
	}

	for i, network := range result {
		_, subnet, _ := net.ParseCIDR(network.CIDR)
		for _, other := range result[i+1:] {
			_, otherSubnet, _ := net.ParseCIDR(other.CIDR)
			if subnet.Contains(net.ParseIP(other.Address)) || otherSubnet.Contains(net.ParseIP(network.Address)) {
				return nil, errors.New("overlapping native DNS networks")
			}
		}
	}

	if len(result) == 0 {
		return nil, errors.New("native DNS networks unavailable")
	}

	return result, nil
}

func resolverCapture(registration ResolverRegistration, operation string) error {
	for _, protocol := range []string{"udp", "tcp"} {
		if err := xt("iptables", "nat", operation, "OUTPUT", "-d", "127.0.0.1", "-p", protocol, "--dport", "53", "-m", "mark", "!", "--mark", registration.Mark+"/"+registration.Mark, "-m", "comment", "--comment", "ocfusion:resolver", "-j", "REDIRECT", "--to-ports", strconv.Itoa(registration.Port)); err != nil {
			return err
		}
	}

	for _, network := range registration.Networks {
		for _, protocol := range []string{"udp", "tcp"} {
			common := []string{"-d", network.Address, "-p", protocol, "--dport", "53", "-m", "comment", "--comment", "ocfusion:resolver", "-j", "REDIRECT", "--to-ports", strconv.Itoa(registration.Port)}
			incoming := append([]string{"-i", network.Bridge}, common...)
			if err := xt("iptables", "nat", operation, "PREROUTING", incoming...); err != nil {
				return err
			}

			local := append([]string{"-m", "mark", "!", "--mark", registration.Mark + "/" + registration.Mark}, common...)
			if err := xt("iptables", "nat", operation, "OUTPUT", local...); err != nil {
				return err
			}

			if err := xt("iptables", "filter", operation, "INPUT", "-i", network.Bridge, "-d", network.Address, "-p", protocol, "--dport", strconv.Itoa(registration.Port), "-m", "comment", "--comment", "ocfusion:resolver", "-j", "ACCEPT"); err != nil {
				return err
			}
		}
	}

	return nil
}

func (b *Broker) resolverRequest(request Request) (Response, error) {
	if request.Operation == "resolver-info" {
		if err := b.dns.allocate(); err != nil {
			return Response{}, err
		}

		networks, err := resolverNetworks()
		if err != nil {
			return Response{}, err
		}

		return Response{OK: true, Resolver: &ResolverRegistration{Mark: strings.Split(b.dns.mark, "/")[0], Networks: networks}}, nil
	}

	if b.resolver != nil && b.resolver.Owner != request.Client {
		return Response{}, errors.New("router DNS filter owned by another module")
	}

	if request.Operation == "resolver-release" {
		if b.resolver != nil {
			if err := resolverCapture(b.resolver.Registration, "del"); err != nil {
				return Response{}, err
			}
		}

		b.resolver = nil
		os.Remove(filepath.Join(runDir, "resolver-routing.json"))
		return Response{OK: true}, nil
	}

	if request.Resolver == nil || request.Resolver.Port < 1024 || request.Resolver.Port > 65535 {
		return Response{}, errors.New("invalid resolver port")
	}

	response, err := b.resolverRequest(Request{Operation: "resolver-info"})
	if err != nil {
		return Response{}, err
	}

	registration := *response.Resolver
	registration.Port = request.Resolver.Port
	if b.resolver != nil && fmt.Sprint(b.resolver.Registration) != fmt.Sprint(registration) {
		if err := resolverCapture(b.resolver.Registration, "del"); err != nil {
			return Response{}, err
		}
	}

	if err := saveJSON(filepath.Join(runDir, "resolver-routing.json"), registration); err != nil {
		return Response{}, err
	}

	if err := resolverCapture(registration, "add"); err != nil {
		if resolverCapture(registration, "del") != nil {
			b.resolver = &ResolverLease{Owner: request.Client, Registration: registration}
		} else {
			b.resolver = nil
		}
		return Response{}, err
	}

	b.resolver = &ResolverLease{Owner: request.Client, Registration: registration, Heartbeat: time.Now()}
	return Response{OK: true, Resolver: &registration}, nil
}

func (b *Broker) reconcileResolver() {
	if b.resolver == nil {
		return
	}

	if time.Since(b.resolver.Heartbeat) > 12*time.Second {
		if resolverCapture(b.resolver.Registration, "del") == nil {
			b.resolver = nil
			os.Remove(filepath.Join(runDir, "resolver-routing.json"))
		}
		return
	}

	resolverCapture(b.resolver.Registration, "add")
}

func recoverResolverCapture() {
	path := filepath.Join(runDir, "resolver-routing.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var registration ResolverRegistration
	if json.Unmarshal(data, &registration) != nil || registration.Port < 1024 || registration.Port > 65535 || len(registration.Networks) > 64 {
		return
	}

	mark, err := strconv.ParseUint(registration.Mark, 0, 32)
	if err != nil || mark == 0 || mark&(mark-1) != 0 {
		return
	}

	for _, network := range registration.Networks {
		if net.ParseIP(network.Address) == nil || !ifaceRE.MatchString(network.Bridge) {
			return
		}
	}

	if resolverCapture(registration, "del") == nil {
		os.Remove(path)
	}
}
