// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var errProfileChanged = errors.New("profile changed; reload settings")

type profileSettings struct {
	Schema         int      `json:"schema"`
	Revision       string   `json:"revision"`
	Server         string   `json:"server"`
	Protocol       string   `json:"protocol"`
	Username       string   `json:"username"`
	Authgroup      string   `json:"authgroup"`
	ServerCert     string   `json:"servercert"`
	DNS            []string `json:"dns"`
	MTU            int      `json:"mtu"`
	NoDTLS         bool     `json:"no_dtls"`
	Enabled        bool     `json:"enabled"`
	HasPassword    bool     `json:"has_password"`
	HasCA          bool     `json:"has_ca"`
	HasCertificate bool     `json:"has_certificate"`
	HasPrivateKey  bool     `json:"has_private_key"`
}

func profileRevision(raw []byte) string {
	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:])
}

func readSettings() (json.RawMessage, error) {
	raw, e := os.ReadFile(filepath.Join(root, "profile.json"))
	p := Profile{Schema: 1, Protocol: "anyconnect", Enabled: true}
	revision := ""
	if e == nil {
		p, e = parseProfile(raw)
		if e != nil {
			return nil, e
		}

		revision = profileRevision(raw)
	} else if !os.IsNotExist(e) {
		return nil, e
	}

	settings := profileSettings{Schema: 1, Revision: revision, Server: p.Server, Protocol: p.Protocol, Username: p.Username, Authgroup: p.Authgroup, ServerCert: p.ServerCert, DNS: p.DNS, MTU: p.MTU, NoDTLS: p.NoDTLS, Enabled: p.Enabled, HasPassword: p.Password != "", HasCA: p.CA != "", HasCertificate: p.Certificate != "", HasPrivateKey: p.PrivateKey != ""}
	b, e := json.Marshal(settings)
	// Response must fit alongside status in the reserved stock 4KiB mailbox.
	if len(b) > 2048 {
		return nil, errors.New("settings response too large; use SSH import")
	}

	return b, e
}

func patchProfile(raw []byte) error {
	var patch struct {
		Revision string                     `json:"revision"`
		Fields   map[string]json.RawMessage `json:"fields"`
	}
	if len(raw) > 16384 {
		return errors.New("settings exceed 16KiB")
	}

	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&patch); e != nil {
		return e
	}

	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing settings data")
	}

	current, e := os.ReadFile(filepath.Join(root, "profile.json"))
	p := Profile{Schema: 1, Protocol: "anyconnect", Enabled: true}
	if e == nil {
		if patch.Revision != profileRevision(current) {
			return errProfileChanged
		}

		p, e = parseProfile(current)
		if e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	} else if patch.Revision != "" {
		return errProfileChanged
	}

	b, _ := json.Marshal(p)
	var fields map[string]json.RawMessage
	json.Unmarshal(b, &fields)

	allowed := map[string]bool{"server": true, "protocol": true, "username": true, "authgroup": true, "servercert": true, "dns": true, "mtu": true, "no_dtls": true, "enabled": true, "password": true, "ca_pem": true, "certificate_pem": true, "private_key_pem": true}
	if len(patch.Fields) == 0 {
		return errors.New("no settings to save")
	}

	for key, value := range patch.Fields {
		if !allowed[key] {
			return errors.New("unknown setting")
		}

		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(fields, key)
		} else {
			fields[key] = value
		}
	}

	merged, e := json.Marshal(fields)
	if e != nil {
		return e
	}

	p, e = parseProfile(merged)
	if e != nil {
		return e
	}

	return saveJSON(filepath.Join(root, "profile.json"), p)
}
