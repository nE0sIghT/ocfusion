// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsRedactionAndSecretPreservingPatch(t *testing.T) {
	old := root
	root = t.TempDir()
	defer func() {
		root = old
	}()

	p := Profile{Schema: 1, Server: "https://vpn.example.org/?camouflage=secret", Protocol: "anyconnect", Username: "asus", Password: "PASSWORD_NOT_PUBLIC", CA: "CA_NOT_PUBLIC", Certificate: "CERT_NOT_PUBLIC", PrivateKey: "KEY_NOT_PUBLIC", Enabled: true}
	if e := saveJSON(filepath.Join(root, "profile.json"), p); e != nil {
		t.Fatal(e)
	}

	raw, e := readSettings()
	if e != nil {
		t.Fatal(e)
	}

	for _, secret := range []string{p.Password, p.CA, p.Certificate, p.PrivateKey} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("credential leaked in public settings")
		}
	}

	var public profileSettings
	json.Unmarshal(raw, &public)
	if !public.HasPassword || !public.HasCA || !public.HasCertificate || !public.HasPrivateKey {
		t.Fatal("presence flags missing")
	}

	patch, _ := json.Marshal(map[string]any{"revision": public.Revision, "fields": map[string]any{"server": "https://vpn.example.org/?another=value", "no_dtls": true, "mtu": 1400}})
	if e = patchProfile(patch); e != nil {
		t.Fatal(e)
	}

	got, e := loadProfile()
	if e != nil {
		t.Fatal(e)
	}

	if got.Password != p.Password || got.CA != p.CA || got.Certificate != p.Certificate || got.PrivateKey != p.PrivateKey || got.Protocol != p.Protocol {
		t.Fatal("partial patch destroyed credentials")
	}

	if !got.NoDTLS || got.MTU != 1400 || got.Server != "https://vpn.example.org/?another=value" {
		t.Fatal("settings not applied")
	}

	before, _ := os.ReadFile(filepath.Join(root, "profile.json"))
	if patchProfile(patch) != errProfileChanged {
		t.Fatal("stale revision accepted")
	}

	after, _ := os.ReadFile(filepath.Join(root, "profile.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("stale patch changed profile")
	}

	raw, _ = readSettings()
	json.Unmarshal(raw, &public)

	patch, _ = json.Marshal(map[string]any{"revision": public.Revision, "fields": map[string]any{"ca_pem": nil, "password": nil}})
	if e = patchProfile(patch); e != nil {
		t.Fatal(e)
	}

	got, _ = loadProfile()
	if got.CA != "" || got.Password != "" || got.PrivateKey != p.PrivateKey {
		t.Fatal("explicit secret deletion failed")
	}

	info, _ := os.Stat(filepath.Join(root, "profile.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("wrong permissions")
	}
}

func TestSettingsRejectInvalidAndCreateNewProfile(t *testing.T) {
	old := root
	root = t.TempDir()
	defer func() {
		root = old
	}()

	raw, e := readSettings()
	if e != nil {
		t.Fatal(e)
	}

	var s profileSettings
	json.Unmarshal(raw, &s)
	if s.Revision != "" || !s.Enabled || s.Protocol != "anyconnect" {
		t.Fatal("new profile defaults")
	}

	valid := `{"revision":"","fields":{"server":"https://vpn.example.org/?token","password":"secret","enabled":true}}`
	if e = patchProfile([]byte(valid)); e != nil {
		t.Fatal(e)
	}

	raw, _ = readSettings()
	json.Unmarshal(raw, &s)

	before, _ := os.ReadFile(filepath.Join(root, "profile.json"))

	for _, fields := range []string{`{"unknown":"x"}`, `{"mtu":12}`, `{"private_key_pem":"key"}`, `{"password":null}`, `{"server":"http://bad"}`, `{"enabled":"true"}`} {
		patch := []byte(`{"revision":"` + s.Revision + `","fields":` + fields + `}`)
		if patchProfile(patch) == nil {
			t.Fatal("invalid patch accepted", fields)
		}

		after, _ := os.ReadFile(filepath.Join(root, "profile.json"))
		if !bytes.Equal(before, after) {
			t.Fatal("invalid patch changed profile")
		}
	}

	p, _ := loadProfile()
	p.Server = "https://vpn.example.org/?" + strings.Repeat("x", 2500)
	saveJSON(filepath.Join(root, "profile.json"), p)
	if _, e = readSettings(); e == nil {
		t.Fatal("unbounded settings response")
	}
}

func TestModuleConfigurationEnvelope(t *testing.T) {
	previous := root
	root = t.TempDir()
	t.Cleanup(func() {
		root = previous
	})

	envelope, _ := json.Marshal(map[string]any{"api": 1, "config": json.RawMessage(validProfile())})
	if _, err := moduleOperation("module-validate", bytes.NewReader(envelope)); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(root, "profile.json")); !os.IsNotExist(err) {
		t.Fatal("validation changed storage")
	}

	if _, err := moduleOperation("module-import", bytes.NewReader(envelope)); err != nil {
		t.Fatal(err)
	}

	settings, err := moduleOperation("module-settings", nil)
	if err != nil {
		t.Fatal(err)
	}

	var public profileSettings
	if err = json.Unmarshal(settings, &public); err != nil || public.Revision == "" || !public.HasPassword {
		t.Fatal("module settings unavailable", err)
	}

	patch, _ := json.Marshal(map[string]any{"config": map[string]any{"revision": public.Revision, "fields": map[string]any{"mtu": 1400}}})
	if _, err = moduleOperation("module-patch", bytes.NewReader(patch)); err != nil {
		t.Fatal(err)
	}

	profile, err := loadProfile()
	if err != nil || profile.MTU != 1400 || profile.Password != "test-only" {
		t.Fatal("module patch lost profile fields", err)
	}

	if _, err = moduleOperation("module-import", strings.NewReader(`{"config":{"schema":1,"server":"http://invalid"}}`)); err == nil {
		t.Fatal("invalid module configuration accepted")
	}

	profile, _ = loadProfile()
	if profile.MTU != 1400 {
		t.Fatal("failed import changed prior configuration")
	}
}
