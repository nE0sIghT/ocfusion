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
	"strings"
)

var version = "dev"
var root = "/jffs/ocfusion-vpn"
var runDir = "/tmp/ocfusion-vpn"
var ifaceRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,14}$`)

type Manifest struct {
	ID        string `json:"id,omitempty"`
	Client    string `json:"client,omitempty"`
	Profile   string `json:"profile,omitempty"`
	Interface string `json:"interface,omitempty"`
	Name      string `json:"name,omitempty"`
	Schema    int    `json:"schema"`
	Unit      int    `json:"unit"`
	Index     int    `json:"vpnc_idx"`
	Table     int    `json:"table"`
	Priority  int    `json:"priority"`
	Firmware  string `json:"firmware"`
}
type Network struct {
	Index      string `json:"sdn_idx"`
	Bridge     string `json:"bridge"`
	CIDR       string `json:"cidr"`
	Address    string `json:"router_address"`
	KillSwitch bool   `json:"kill_switch"`
	IPv6       bool   `json:"ipv6_enabled"`
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	f, e := os.CreateTemp(filepath.Dir(path), ".ocf-")
	if e != nil {
		return e
	}

	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}

	if e == nil {
		e = f.Sync()
	}

	ce := f.Close()
	if e == nil {
		e = ce
	}

	if e != nil {
		return e
	}

	return os.Rename(name, path)
}
func saveJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}

	return atomicWrite(path, append(b, '\n'), 0600)
}

// The fixed-format tables are data, never shell input. Preserve index associations.
func discover(sdn, subnets string, idx int) ([]Network, error) {
	sub := map[string][]string{}

	for _, r := range strings.Split(subnets, "<") {
		f := strings.Split(r, ">")
		if len(f) >= 15 {
			sub[f[0]] = f
		}
	}

	var out []Network
	bridges := map[string]bool{}

	for _, r := range strings.Split(sdn, "<") {
		f := strings.Split(r, ">")
		if len(f) < 16 || f[2] != "1" || f[6] != fmt.Sprint(idx) {
			continue
		}

		n, e := parseNetwork(f, sub[f[4]])
		if e != nil {
			return nil, e
		}

		if bridges[n.Bridge] {
			return nil, errors.New("duplicate bridge binding")
		}

		bridges[n.Bridge] = true
		out = append(out, n)
	}

	return out, nil
}

func parseNetwork(s, sub []string) (Network, error) {
	var n Network
	if len(sub) < 15 {
		return n, errors.New("missing subnet_rl for VPN SDN")
	}

	if s[4] == "0" || sub[1] == "br0" || !ifaceRE.MatchString(sub[1]) {
		return n, errors.New("VPN SDN needs a dedicated bridge, never br0")
	}

	ip := net.ParseIP(sub[2])
	maskIP := net.ParseIP(sub[3])
	if ip == nil || ip.To4() == nil || maskIP == nil || maskIP.To4() == nil {
		return n, errors.New("invalid SDN IPv4 subnet")
	}

	mask := net.IPMask(maskIP.To4())
	ones, bits := mask.Size()
	if bits != 32 || ones < 8 || ones > 30 {
		return n, errors.New("unsupported SDN netmask")
	}

	nw := net.IPNet{IP: ip.To4().Mask(mask), Mask: mask}
	n = Network{Index: s[0], Bridge: sub[1], CIDR: nw.String(), Address: sub[2], KillSwitch: s[14] == "1", IPv6: sub[14] != "0" && sub[14] != ""}
	if !n.KillSwitch {
		return n, errors.New("enable SDN kill switch in ASUS web before using VPN")
	}

	if n.IPv6 {
		return n, errors.New("disable IPv6 for this SDN in ASUS web (IPv4-only release)")
	}

	return n, nil
}
