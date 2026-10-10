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
