// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type VPNTarget struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Ready    bool   `json:"ready"`
}

type RoutePolicy struct {
	ID       string   `json:"id"`
	Action   string   `json:"action"`
	Target   string   `json:"target,omitempty"`
	Failure  string   `json:"failure,omitempty"`
	Networks []string `json:"networks"`
	Domains  []string `json:"domains"`
}

type RouteStatus struct {
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

type RouteReply struct {
	OK      bool          `json:"ok"`
	Targets []VPNTarget   `json:"targets"`
	Status  []RouteStatus `json:"destination_status"`
	Mask    string        `json:"destination_mask"`
}

var vpnRequest = func(operation string, policies []RoutePolicy) (RouteReply, error) {
	var reply RouteReply
	connection, err := net.DialTimeout("unix", "/tmp/ocfusion-vpn/control.sock", time.Second)
	if err != nil {
		return reply, errors.New("VPN service unavailable")
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(20 * time.Second))
	request := map[string]any{"api": 1, "client": "zapret2", "operation": operation}
	if operation == "destination-probe" && len(policies) == 1 {
		request["destination_probe"] = policies[0].ID
	} else if policies != nil {
		request["destinations"] = policies
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return reply, errors.New("VPN request failed")
	}
	if json.NewDecoder(io.LimitReader(connection, 32768)).Decode(&reply) != nil || !reply.OK {
		return reply, errors.New("VPN routing unavailable")
	}
	return reply, nil
}

func ruleAction(rule DestinationRule) string {
	if rule.Action == "" {
		return "dpi"
	}
	return rule.Action
}

func validateRuleAction(rule *DestinationRule) error {
	action := ruleAction(*rule)
	if action != "dpi" && action != "vpn" && action != "normal" {
		return errors.New("invalid connection method")
	}
	if action != "vpn" {
		if rule.Target != "" || rule.Failure != "" {
			return errors.New("VPN settings require a VPN rule")
		}
		return nil
	}
	if rule.Failure == "" {
		rule.Failure = "block"
	}
	if (rule.Failure != "wan" && rule.Failure != "block") || len(rule.Target) > 128 || !(strings.HasPrefix(rule.Target, "managed:") || strings.HasPrefix(rule.Target, "native:")) {
		return errors.New("choose a VPN profile")
	}
	return nil
}

func routePolicies(c Config) []RoutePolicy {
	policies := []RoutePolicy{}
	hasVPN := false
	for _, rule := range c.Rules {
		if !rule.Enabled {
			continue
		}
		policies = append(policies, RoutePolicy{ID: rule.ID, Action: ruleAction(rule), Target: rule.Target, Failure: rule.Failure, Networks: rule.Networks, Domains: rule.Domains})
		hasVPN = hasVPN || ruleAction(rule) == "vpn"
	}
	if !hasVPN || !c.Enabled {
		return nil
	}
	if len(c.Networks)+len(c.Domains) > 0 {
		policies = append(policies, RoutePolicy{ID: "default", Action: "dpi", Networks: c.Networks, Domains: c.Domains})
	}
	return policies
}

func reconcileRoutes(c *Config) error {
	policies := routePolicies(*c)
	path := scanPath("vpn-routing.json")
	if len(policies) == 0 {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		if _, err := vpnRequest("destination-release", nil); err != nil {
			return err
		}
		return os.Remove(path)
	}
	// Remember that a lease may exist before sending the request. A lost reply
	// must not cause an untracked lease on disable or uninstall.
	if err := writeJSON(path, map[string]any{"state": "WAITING"}); err != nil {
		return err
	}
	reply, err := vpnRequest("destination-register", policies)
	if err != nil {
		// vpnRequest returns fixed redacted messages, never provider stderr.
		writeJSON(path, map[string]string{"state": "ERROR", "message": err.Error()})
		return err
	}
	mask, err := strconv.ParseUint(reply.Mask, 0, 32)
	if err != nil {
		return errors.New("invalid VPN routing response")
	}
	c.RouteMask = uint32(mask)
	return writeJSON(path, reply)
}

func routingStatus() []RouteStatus {
	data, err := os.ReadFile(scanPath("vpn-routing.json"))
	if err != nil || len(data) > 32768 {
		return nil
	}
	var reply RouteReply
	json.Unmarshal(data, &reply)
	return reply.Status
}

func hasDPISelection(c Config) bool {
	for _, selected := range processingConfigs(c) {
		if len(selected.Domains)+len(selected.Networks) == 0 {
			continue
		}
		if (selected.TCP != "off" && len(selected.TCPPorts) > 0) || (selected.UDP != "off" && len(selected.UDPPorts) > 0) || len(selected.Applications) > 0 {
			return true
		}
	}
	return false
}
