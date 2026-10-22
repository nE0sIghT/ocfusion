// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

var version = "dev"
var revision = "unknown"
var runDir = "/tmp/ocfusion-http-proxy"

type Config struct {
	Revision uint64   `json:"revision"`
	Enabled  bool     `json:"enabled"`
	Target   string   `json:"target"`
	Bridge   string   `json:"bridge"`
	Port     int      `json:"port"`
	Allowed  []string `json:"allowed"`
	DNS      []string `json:"dns"`
}

type Registration struct {
	Local   []string `json:"local_addresses"`
	Tunnel  string   `json:"tunnel"`
	Target  string   `json:"target"`
	Bridge  string   `json:"bridge"`
	Port    int      `json:"port"`
	Allowed []string `json:"allowed"`
	UID     int      `json:"uid"`
	Address string   `json:"address"`
	CIDR    string   `json:"cidr"`
	Ready   bool     `json:"ready"`
}

type Target struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Ready    bool   `json:"ready"`
}

type Network struct {
	Bridge  string `json:"bridge"`
	Address string `json:"router_address"`
	CIDR    string `json:"cidr"`
}

func parseConfig(data []byte) (Config, error) {
	c := Config{Port: 3128, Bridge: "br0", DNS: []string{"1.1.1.1", "9.9.9.9"}}
	if len(data) == 0 || string(data) == "null" {
		return c, nil
	}

	if len(data) > 8192 {
		return c, errors.New("settings exceed bounds")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF {
		return c, errors.New("invalid proxy settings")
	}

	if c.Port < 1024 || c.Port > 65535 || len(c.DNS) < 1 || len(c.DNS) > 4 || len(c.Allowed) > 32 || len(c.Target) > 96 || len(c.Bridge) > 15 {
		return c, errors.New("invalid proxy settings")
	}

	for _, s := range append(append([]string{}, c.Allowed...), c.DNS...) {
		if strings.ContainsAny(s, "\r\n\t ") {
			return c, errors.New("invalid address")
		}
	}

	for _, s := range c.DNS {
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
			return c, errors.New("DNS requires a unicast IPv4 address")
		}
	}

	for _, s := range c.Allowed {
		ip := net.ParseIP(s)
		if ip != nil && ip.To4() != nil {
			continue
		}

		ip, _, err := net.ParseCIDR(s)
		if err != nil || ip.To4() == nil {
			return c, errors.New("ACL requires IPv4 addresses or subnets")
		}
	}

	if c.Enabled && (c.Target == "" || c.Bridge == "") {
		return c, errors.New("select a VPN and LAN")
	}

	return c, nil
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(os.Getenv("EXT_CONFIG_PATH"))
	if os.IsNotExist(err) {
		return parseConfig(nil)
	}

	if err != nil {
		return Config{}, err
	}

	return parseConfig(data)
}

func proxyConfig(c Config, r Registration) string {
	var out strings.Builder
	out.WriteString("maxconn 128\nnscache 1024\n")
	for _, dns := range c.DNS {
		out.WriteString("nserver " + dns + "\n")
	}

	out.WriteString("auth iponly\n")
	blocked := append([]string{"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4", "255.255.255.255", r.Address}, r.Local...)
	out.WriteString("deny * * " + strings.Join(blocked, ",") + "\n")
	allowed := c.Allowed
	if len(allowed) == 0 {
		allowed = []string{r.CIDR}
	}

	out.WriteString("allow * " + strings.Join(allowed, ",") + "\ndeny *\nproxy -4 -p" + strconv.Itoa(c.Port) + " -i" + r.Address + "\n")
	return out.String()
}

func validRegistration(c Config, r Registration) bool {
	if r.UID < 60000 || r.UID >= 61000 || net.ParseIP(r.Address) == nil {
		return false
	}

	_, subnet, err := net.ParseCIDR(r.CIDR)
	if err != nil || !subnet.Contains(net.ParseIP(r.Address)) {
		return false
	}

	for _, dns := range c.DNS {
		if dns == r.Address {
			return false
		}

		for _, address := range r.Local {
			if net.ParseIP(address) == nil || dns == address {
				return false
			}
		}
	}

	return true
}
