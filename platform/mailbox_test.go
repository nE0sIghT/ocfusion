// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDirectConfigurationBoundsAndCleanup(t *testing.T) {
	previousRAM, previousCommand := ram, runModuleCommand
	ram = t.TempDir()
	t.Cleanup(func() {
		ram, runModuleCommand = previousRAM, previousCommand
	})

	calls := 0
	config := json.RawMessage(`{"secret":"` + strings.Repeat("x", 6000) + `"}`)
	runModuleCommand = func(args ...string) (json.RawMessage, error) {
		calls++
		data, err := os.ReadFile(args[2])
		if args[0] != "config" || args[1] != "example" || err != nil || string(data) != string(config) {
			t.Fatal("configuration changed", args, err)
		}

		return json.RawMessage(`{"ok":true}`), nil
	}

	mailbox := &webMailbox{}
	request := webRequest{ID: strings.Repeat("a", 32), Action: "module.configure", Module: "example", Config: config}
	raw, _ := json.Marshal(request)
	if _, ok, _ := mailbox.apply(raw, time.Now()); !ok {
		t.Fatal(mailbox.LastMessage)
	}

	if _, err := os.Stat(ram + "/web-config.json"); !os.IsNotExist(err) {
		t.Fatal("private configuration retained")
	}

	request.ID = strings.Repeat("b", 32)
	request.Config = json.RawMessage(`{"secret":"` + strings.Repeat("x", 16384) + `"}`)
	raw, _ = json.Marshal(request)
	if _, ok, _ := mailbox.apply(raw, time.Now()); ok || calls != 1 {
		t.Fatal("oversized configuration dispatched")
	}
}

func TestInlineModuleCall(t *testing.T) {
	previousRAM, previousCommand := ram, runModuleCommand
	ram = t.TempDir()
	t.Cleanup(func() {
		ram, runModuleCommand = previousRAM, previousCommand
	})

	calls := 0
	runModuleCommand = func(args ...string) (json.RawMessage, error) {
		calls++
		if len(args) != 4 || args[0] != "call" || args[1] != "example" || args[2] != "settings" {
			t.Fatal(args)
		}

		data, err := os.ReadFile(args[3])
		if err != nil || string(data) != `{"profile":"default"}` {
			t.Fatal("invalid inline configuration", err)
		}

		return json.RawMessage(`{"enabled":true}`), nil
	}

	mailbox := &webMailbox{}
	request := webRequest{ID: strings.Repeat("a", 32), Action: "module.call", Module: "example", Operation: "settings", Config: json.RawMessage(`{"profile":"default"}`)}
	raw, _ := json.Marshal(request)
	if _, ok, _ := mailbox.apply(raw, time.Now()); !ok {
		t.Fatal(mailbox.LastMessage)
	}
	if _, err := os.Stat(ram + "/web-config.json"); !os.IsNotExist(err) {
		t.Fatal("temporary configuration retained", err)
	}
	mailbox.apply(raw, time.Now())
	if calls != 1 {
		t.Fatal("duplicate request invoked twice")
	}

	request.ID = strings.Repeat("b", 32)
	request.Module = "../example"
	raw, _ = json.Marshal(request)
	if _, ok, _ := mailbox.apply(raw, time.Now()); ok {
		t.Fatal("invalid module accepted")
	}
}
