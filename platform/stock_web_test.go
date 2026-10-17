// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStockHeaderInsertion(t *testing.T) {
	original := "function show_banner() { /* stock header */ }\n"
	if fixture := os.Getenv("ASUS_STATE_JS_FIXTURE"); fixture != "" {
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}

		original = string(data)
	}

	for _, stock := range []string{original, original + "\n// Firmware changed\n", ""} {
		patched := stateOverlay(stock)
		if !strings.HasPrefix(patched, stock+"\n") || !strings.HasSuffix(patched, headerScript) {
			t.Fatal("stock script changed instead of appending the extension")
		}
	}

	if !strings.Contains(headerScript, "status_block") || !strings.Contains(headerScript, "target.insertBefore(link, target.firstChild)") {
		t.Fatal("DOM integration missing")
	}

	for _, src := range []string{"/ocfusion/state.js", "/ocfusion/state.js//deleted"} {
		if !ownWebMount("27 11 0:19 "+src+" /www/state.js rw - tmpfs tmpfs rw", "/www/state.js", "/ocfusion/state.js") {
			t.Fatal("ownership lost")
		}
	}

	if ownWebMount("27 11 0:19 /foreign/state.js /www/state.js rw - tmpfs tmpfs rw", "/www/state.js", "/ocfusion/state.js") {
		t.Fatal("foreign mount accepted")
	}
}

func TestModuleMailboxActionReply(t *testing.T) {
	previous := runModuleCommand
	t.Cleanup(func() {
		runModuleCommand = previous
	})

	var arguments []string
	runModuleCommand = func(args ...string) (json.RawMessage, error) {
		arguments = args

		return json.RawMessage(`{"value":42}`), nil
	}
	var mailbox webMailbox
	request := []byte(`{"id":"0123456789abcdef0123456789abcdef","action":"module.action","module":"heartbeat","operation":"settings"}`)
	_, ok, _ := mailbox.apply(request, time.Now())
	if !ok || string(mailbox.LastData) != `{"value":42}` || strings.Join(arguments, " ") != "call heartbeat settings" {
		t.Fatalf("module reply: %v %s %v", ok, mailbox.LastData, arguments)
	}
}

func TestStockContributionWithoutUI(t *testing.T) {
	previous := webRoot
	webRoot = t.TempDir()
	defer func() { webRoot = previous }()

	payload := t.TempDir()
	os.Mkdir(filepath.Join(payload, "ui"), 0700)
	os.WriteFile(filepath.Join(payload, "ui/fusion.js"), []byte("fixture"), 0600)
	item := Installed{
		Manifest: Module{ID: "provider", StockPages: []StockPage{{Path: "/VPN/vpnc.html", Script: "ui/fusion.js"}}},
		Hashes:   map[string]string{"ui/fusion.js": "verified"},
	}
	if err := publishUI(item, payload); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(webRoot, "extensions-modules/provider/ui/fusion.js"))
	if err != nil || string(data) != "fixture" {
		t.Fatalf("contribution not published: %v", err)
	}
}

func TestStockPageManifestSupport(t *testing.T) {
	for _, path := range []string{"/VPN/vpnc.html", "/Advanced_Wireless_Content.asp"} {
		directory := t.TempDir()
		fixture(t, directory, "1.0", "echo '{}'")
		manifestPath := filepath.Join(directory, "module.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}

		var manifest Module
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatal(err)
		}

		manifest.StockPages = []StockPage{{Path: path, Script: "bin/module"}}
		if err := atomicJSON(manifestPath, manifest); err != nil {
			t.Fatal(err)
		}

		if _, err := readPackage(directory); err != nil {
			t.Fatal("supported contribution rejected", path, err)
		}

		manifest.StockPages[0].Path = "/unknown.asp"
		if err := atomicJSON(manifestPath, manifest); err != nil {
			t.Fatal(err)
		}

		if _, err := readPackage(directory); err == nil {
			t.Fatal("unsupported contribution accepted")
		}
	}
}
