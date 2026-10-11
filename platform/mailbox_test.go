// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGenericMailboxCommitsOnlyCompleteVerifiedUploads(t *testing.T) {
	previousRAM := ram
	previousCommand := runModuleCommand
	ram = t.TempDir()
	t.Cleanup(func() {
		ram = previousRAM
		runModuleCommand = previousCommand
	})

	committed := false
	raw := []byte(`{"secret":"` + strings.Repeat("x", 6000) + `"}`)
	runModuleCommand = func(args ...string) (json.RawMessage, error) {
		if args[0] != "config" || args[1] != "example" {
			t.Fatal(args)
		}

		data, err := os.ReadFile(args[2])
		if err != nil || string(data) != string(raw) {
			t.Fatal("invalid committed content", err)
		}

		committed = true
		return json.RawMessage(`{"ok":true}`), nil
	}

	digest := sha256.Sum256(raw)
	upload := strings.Repeat("a", 32)
	mailbox := &webMailbox{}
	counter := 0
	send := func(request webRequest, want bool) {
		counter++
		request.ID = fmt.Sprintf("%032x", counter)
		data, _ := json.Marshal(request)
		_, ok, _ := mailbox.apply(data, time.Now())
		if ok != want {
			t.Fatalf("action %s: %v %s", request.Action, ok, mailbox.LastMessage)
		}
	}

	send(webRequest{Action: "upload.begin", Module: "example", Upload: upload, Size: len(raw), SHA256: hex.EncodeToString(digest[:])}, true)
	send(webRequest{Action: "upload.finish", Upload: upload}, false)
	if committed {
		t.Fatal("partial upload committed")
	}

	for offset := 0; offset < len(raw); offset += 1024 {
		end := min(offset+1024, len(raw))
		send(webRequest{Action: "upload.chunk", Upload: upload, Offset: offset, Data: raw[offset:end]}, true)
	}

	send(webRequest{Action: "upload.finish", Upload: upload}, true)
	if !committed || strings.Contains(mailbox.LastMessage, "xxxx") {
		t.Fatal("missing commit or exposed secret")
	}

	send(webRequest{Action: "upload.begin", Module: "example", Upload: upload, Size: 16385, SHA256: hex.EncodeToString(digest[:])}, false)
	send(webRequest{Action: "exec"}, false)
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

func TestPlatformPingDoesNotInvokeModules(t *testing.T) {
	previous := runModuleCommand
	t.Cleanup(func() {
		runModuleCommand = previous
	})
	runModuleCommand = func(args ...string) (json.RawMessage, error) {
		t.Fatal("ping invoked a module", args)
		return nil, nil
	}

	mailbox := &webMailbox{}
	raw := []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","action":"platform.ping"}`)
	if _, ok, _ := mailbox.apply(raw, time.Now()); !ok || !strings.Contains(string(mailbox.LastData), `"pong":true`) {
		t.Fatal("ping failed", mailbox.LastMessage)
	}
}
