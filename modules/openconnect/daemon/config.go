// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var version = "dev"
var revision = "unknown"

var root = "/jffs/ocfusion"
var runDir = "/tmp/ocfusion"
var ifaceRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,14}$`)

type Profile struct {
	OTPMode     string   `json:"otp_mode,omitempty"`
	OTPSecret   string   `json:"otp_secret,omitempty"`
	OTPField    string   `json:"otp_field,omitempty"`
	NativeDNS   bool     `json:"native_dns,omitempty"`
	Schema      int      `json:"schema"`
	Server      string   `json:"server"`
	Protocol    string   `json:"protocol"`
	Username    string   `json:"username"`
	Password    string   `json:"password,omitempty"`
	Authgroup   string   `json:"authgroup,omitempty"`
	ServerCert  string   `json:"servercert,omitempty"`
	CA          string   `json:"ca_pem,omitempty"`
	Certificate string   `json:"certificate_pem,omitempty"`
	PrivateKey  string   `json:"private_key_pem,omitempty"`
	DNS         []string `json:"dns,omitempty"`
	NoDTLS      bool     `json:"no_dtls,omitempty"`
	MTU         int      `json:"mtu,omitempty"`
	Enabled     bool     `json:"enabled"`
}
type Manifest struct {
	ID        string            `json:"id,omitempty"`
	Client    string            `json:"client,omitempty"`
	Profile   string            `json:"profile,omitempty"`
	Interface string            `json:"interface,omitempty"`
	Name      string            `json:"name,omitempty"`
	Schema    int               `json:"schema"`
	Unit      int               `json:"unit"`
	Index     int               `json:"vpnc_idx"`
	Table     int               `json:"table"`
	Priority  int               `json:"priority"`
	Firmware  string            `json:"firmware"`
	Bundle    string            `json:"bundle"`
	BundleSHA map[string]string `json:"bundle_sha256"`
}
type Network struct {
	Index      string `json:"sdn_idx"`
	Bridge     string `json:"bridge"`
	CIDR       string `json:"cidr"`
	Address    string `json:"router_address"`
	KillSwitch bool   `json:"kill_switch"`
	IPv6       bool   `json:"ipv6_enabled"`
}

func parseProfile(b []byte) (Profile, error) {
	var p Profile
	if len(b) > 16384 {
		return p, errors.New("profile exceeds 16 KiB")
	}

	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, fmt.Errorf("invalid JSON/schema: %w", err)
	}

	var extra any
	if d.Decode(&extra) != io.EOF {
		return p, errors.New("trailing data")
	}

	if p.Schema != 1 {
		return p, errors.New("schema must be 1")
	}

	u, e := url.Parse(p.Server)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(p.Server, "\r\n\x00") {
		return p, errors.New("server must be an https URL without credentials")
	}

	if p.Protocol == "" {
		p.Protocol = "anyconnect"
	}

	switch p.Protocol {
	case "anyconnect", "nc", "pulse", "gp", "f5", "fortinet", "array":
	default:
		return p, errors.New("unsupported protocol")
	}

	for _, v := range []string{p.Username, p.Password, p.Authgroup, p.ServerCert} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return p, errors.New("newline/NUL in credential or option")
		}
	}

	if p.MTU != 0 && (p.MTU < 576 || p.MTU > 1500) {
		return p, errors.New("mtu must be 576..1500")
	}

	if (p.Certificate == "") != (p.PrivateKey == "") {
		return p, errors.New("certificate and key must be provided together")
	}

	if p.Password == "" && p.PrivateKey == "" {
		return p, errors.New("password or client certificate/key required for unattended login")
	}

	for _, s := range p.DNS {
		if ip := net.ParseIP(s); ip == nil || ip.To4() == nil {
			return p, errors.New("dns must contain IPv4 addresses")
		}
	}

	if err := validateOTP(p); err != nil {
		return p, err
	}

	canonical, _ := json.MarshalIndent(p, "", "  ")
	if len(canonical)+1 > 16384 {
		return p, errors.New("stored profile exceeds 16 KiB")
	}

	return p, nil
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

func loadManifest() (Manifest, error) {
	var m Manifest
	b, e := os.ReadFile(filepath.Join(root, "manifest.json"))
	if e == nil {
		e = json.Unmarshal(b, &m)
	}

	if e == nil && (m.Schema != 1 || m.Unit < 1 || m.Unit > 5 || m.Index < 5 || m.Index > 20 || m.Table < 1000 || m.Priority < 50) {
		e = errors.New("invalid manifest")
	}

	return m, e
}

func loadProfile() (Profile, error) {
	b, e := os.ReadFile(filepath.Join(root, "profile.json"))
	if e != nil {
		return Profile{}, e
	}

	return parseProfile(b)
}

func versionCommit() string {
	if len(revision) > 7 {
		return revision[:7]
	}

	return revision
}
