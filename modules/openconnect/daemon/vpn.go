// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	Generation string            `json:"generation"`
	Reason     string            `json:"reason"`
	Vars       map[string]string `json:"vars"`
}

type Tunnel struct {
	Interface string   `json:"interface"`
	Address   string   `json:"address"`
	DNS       []string `json:"dns"`
	MTU       int      `json:"mtu"`
}

type vpnRequest struct {
	PreferDNS  bool    `json:"prefer_dns,omitempty"`
	API        int     `json:"api"`
	Operation  string  `json:"operation"`
	Client     string  `json:"client"`
	Profile    string  `json:"profile"`
	Connection string  `json:"connection,omitempty"`
	Tunnel     *Tunnel `json:"tunnel,omitempty"`
}

type vpnResponse struct {
	DNSStatus json.RawMessage `json:"dns_status,omitempty"`
	OK        bool            `json:"ok"`
	Manifest  Manifest        `json:"manifest"`
	Networks  []Network       `json:"networks"`
	DNS       string          `json:"dns,omitempty"`
	Error     string          `json:"error,omitempty"`
}

var vpnCall = callVPN

func callVPN(request vpnRequest) (vpnResponse, error) {
	var response vpnResponse
	request.API = 1
	request.Client = "openconnect"
	request.Profile = profileID
	if request.Operation != "claim" && request.Connection == "" {
		if manifest, err := loadManifest(); err == nil {
			request.Connection = manifest.ID
		}
	}
	connection, err := net.DialTimeout("unix", "/tmp/ocfusion-vpn/control.sock", 2*time.Second)
	if err != nil {
		return response, errors.New("VPN service unavailable")
	}

	defer connection.Close()
	connection.SetDeadline(time.Now().Add(25 * time.Second))
	if err = json.NewEncoder(connection).Encode(request); err != nil {
		return response, err
	}

	if err = json.NewDecoder(connection).Decode(&response); err != nil {
		return response, err
	}

	if !response.OK {
		return response, errors.New(response.Error)
	}

	return response, nil
}

func networks(m Manifest) ([]Network, error) {
	response, err := vpnCall(vpnRequest{Operation: "state"})

	return response.Networks, err
}

func ensureNativePolicy(m Manifest) error {
	_, err := vpnCall(vpnRequest{Operation: "prepare"})

	return err
}

func prepare(m Manifest, networks []Network) error {
	return ensureNativePolicy(m)
}

func closeTunnel(m Manifest) error {
	_, err := vpnCall(vpnRequest{Operation: "down"})

	return err
}

func applyConnect(m Manifest, profile Profile, networks []Network, event Event) (string, error) {
	dns := profile.DNS
	if len(dns) == 0 {
		dns = strings.Fields(event.Vars["INTERNAL_IP4_DNS"])
	}

	mtu := profile.MTU
	if mtu == 0 {
		mtu, _ = strconv.Atoi(event.Vars["INTERNAL_IP4_MTU"])
	}

	tunnel := &Tunnel{Interface: event.Vars["TUNDEV"], Address: event.Vars["INTERNAL_IP4_ADDRESS"], DNS: dns, MTU: mtu}
	response, err := vpnCall(vpnRequest{Operation: "apply", Tunnel: tunnel})

	return response.DNS, err
}

func dnsOnlySelected() bool {
	response, err := vpnCall(vpnRequest{Operation: "dns-state"})
	if err != nil {
		return false
	}

	var status struct {
		Policy struct {
			Client  string `json:"client"`
			Profile string `json:"profile"`
		} `json:"policy"`
	}
	if json.Unmarshal(response.DNSStatus, &status) != nil {
		return false
	}

	return status.Policy.Client == "openconnect" && status.Policy.Profile == profileID
}
