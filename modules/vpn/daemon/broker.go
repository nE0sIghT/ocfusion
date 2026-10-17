// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type Broker struct {
	resolver *ResolverLease
	registry Connections
	services map[string]*Service
	dns      DNSRouter
}

func newBroker(registry Connections) *Broker {
	broker := &Broker{registry: registry, services: map[string]*Service{}}
	broker.dns.policy = readDNSPolicy()
	for id, manifest := range registry.Items {
		broker.services[id] = &Service{manifest: manifest, owner: manifest.Client}
	}

	return broker
}

func (b *Broker) handle(request Request) (Response, error) {
	if request.API != 1 || !identifier.MatchString(request.Client) {
		return Response{}, errors.New("invalid VPN owner or API")
	}

	if request.Operation == "resolver-info" || request.Operation == "resolver-register" || request.Operation == "resolver-release" {
		return b.resolverRequest(request)
	}

	if request.Operation == "dns-state" {
		return Response{OK: true, DNSStatus: &b.dns.status}, nil
	}

	if !identifier.MatchString(request.Profile) {
		return Response{}, errors.New("invalid VPN profile")
	}

	if request.Operation == "dns-select" {
		policy := DNSPolicy{}
		if request.PreferDNS {
			policy = DNSPolicy{Client: request.Client, Profile: request.Profile}
		} else if b.dns.policy.Client != request.Client || b.dns.policy.Profile != request.Profile {
			return Response{}, errors.New("DNS selection belongs to another connection")
		}

		if err := b.dns.selectPolicy(policy); err != nil {
			return Response{}, err
		}

		b.dns.reconcile(b.services)
		return Response{OK: true, DNSStatus: &b.dns.status}, nil
	}

	if request.Operation == "list" {
		response := Response{OK: true}
		for _, id := range connectionIDs(b.registry) {
			item := b.registry.Items[id]
			if item.Client == request.Client {
				response.Connections = append(response.Connections, item)
			}
		}

		return response, nil
	}

	if request.Operation == "claim" {
		previousNative := nvget("vpnc_clientlist")
		manifest, err := allocateConnection(b.registry, request.Client, request.Profile)
		if err != nil {
			return Response{}, err
		}
		if _, exists := b.registry.Items[manifest.ID]; !exists {
			b.registry.Items[manifest.ID] = manifest
			if err = writeConnections(b.registry); err != nil {
				delete(b.registry.Items, manifest.ID)
				nvset("vpnc_clientlist", previousNative)
				runner("nvram", "commit")
				return Response{}, err
			}
			b.services[manifest.ID] = &Service{manifest: manifest, owner: manifest.Client}
		}

		return Response{OK: true, Manifest: manifest}, nil
	}

	service := b.services[request.Connection]
	if service == nil || service.manifest.Client != request.Client || service.manifest.Profile != request.Profile {
		return Response{}, errors.New("VPN connection not owned by requester")
	}
	if request.Operation == "release" {
		assigned, err := networks(service.manifest)
		if err != nil || len(assigned) != 0 {
			return Response{}, errors.New("reassign native networks before removal")
		}
		service.down()
		for _, network := range service.networks {
			unbind(service.manifest, network, "")
		}

		previousNative := nvget("vpnc_clientlist")
		if err = removeNative(service.manifest); err != nil {
			return Response{}, err
		}
		delete(b.registry.Items, request.Connection)
		if err = writeConnections(b.registry); err != nil {
			b.registry.Items[request.Connection] = service.manifest
			nvset("vpnc_clientlist", previousNative)
			runner("nvram", "commit")
			return Response{}, err
		}
		runner("ip", "route", "del", "unreachable", "default", "metric", "32760", "table", strconv.Itoa(service.manifest.Table))
		delete(b.services, request.Connection)
		if b.dns.policy.Client == request.Client && b.dns.policy.Profile == request.Profile {
			if err := b.dns.selectPolicy(DNSPolicy{}); err != nil {
				return Response{}, err
			}
		}
		b.dns.reconcile(b.services)

		return Response{OK: true, Manifest: service.manifest}, nil
	}

	if err := b.checkNetworks(request.Connection); err != nil {
		return Response{}, err
	}

	service.dnsOnly = b.dns.policy.Client == request.Client && b.dns.policy.Profile == request.Profile
	response, err := service.handle(request)
	b.dns.reconcile(b.services)
	response.DNSStatus = &b.dns.status
	return response, err
}

func (b *Broker) reconcile() {
	statuses := map[string]any{}
	for _, id := range connectionIDs(b.registry) {
		service := b.services[id]
		err := b.checkNetworks(id)
		if err == nil {
			err = service.reconcile(true)
		} else {
			service.down()
		}
		state := "BLOCKED"
		if service.event != nil && err == nil {
			state = "UP"
		}
		statuses[id] = map[string]any{"state": state, "client": service.manifest.Client, "profile": service.manifest.Profile, "networks": service.networks}
	}

	b.dns.reconcile(b.services)
	b.reconcileResolver()
	saveJSON(filepath.Join(runDir, "status.json"), map[string]any{"connections": statuses, "dns": b.dns.status})
}

func (b *Broker) down() {
	b.dns.reset()
	if b.resolver != nil {
		resolverCapture(b.resolver.Registration, "del")
	}
	for _, service := range b.services {
		service.down()
	}
}

func (b *Broker) checkNetworks(target string) error {
	own, err := networks(b.registry.Items[target])
	if err != nil {
		return err
	}
	bridges := map[string]bool{}
	for _, network := range own {
		bridges[network.Bridge] = true
	}
	for id, manifest := range b.registry.Items {
		if id == target {
			continue
		}
		assigned, err := networks(manifest)
		if err != nil {
			continue
		}
		for _, network := range assigned {
			if bridges[network.Bridge] {
				return errors.New("bridge assigned to multiple VPN connections")
			}
		}
	}

	return nil
}
