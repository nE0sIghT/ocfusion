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
	b, e := os.ReadFile(os.Getenv("ASUS_STATE_JS_FIXTURE"))
	if e != nil {
		t.Skip("extracted firmware unavailable")
	}

	patched, e := stateOverlay(string(b))
	if e != nil {
		t.Fatal(e)
	}

	if !strings.Contains(patched, statusAnchor+"\n"+extensionMarker+"\nbanner_code += `"+extensionIcon) {
		t.Fatal("icon not first in header")
	}

	if strings.Count(patched, "asus_extensions_link") != 1 || !strings.Contains(patched, "margin-right:16px") {
		t.Fatal("duplicate/missing separation")
	}

	if _, e = stateOverlay(patched); e == nil {
		t.Fatal("accepted patched page as original")
	}

	if _, e = stateOverlay(string(b) + "changed"); e == nil {
		t.Fatal("accepted incompatible firmware")
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
