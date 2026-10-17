// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRemovalRebootNoticePersistsAcrossRetries(t *testing.T) {
	previous := ram
	ram = t.TempDir()
	defer func() { ram = previous }()
	marker := filepath.Join(ram, "removal-reboot-required")

	for _, data := range []string{`{}`, `{"reboot_required":false}`} {
		result, err := recordRemovalResult(json.RawMessage(data))
		if err != nil || result.RebootRequired {
			t.Fatal(result, err)
		}
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unexpected reboot marker", err)
	}

	result, err := recordRemovalResult(json.RawMessage(`{"reboot_required":true}`))
	if err != nil || !result.RebootRequired {
		t.Fatal(result, err)
	}

	if _, err := recordRemovalResult(json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}

	if info, err := os.Stat(marker); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("pending reboot lost or exposed", err)
	}

	if _, err := recordRemovalResult(json.RawMessage(`{"reboot_required":"yes"}`)); err == nil {
		t.Fatal("invalid reboot flag accepted")
	}
}

func TestManagerRemovalHonorsHookResult(t *testing.T) {
	for _, test := range []struct {
		name    string
		script  string
		failure bool
	}{
		{name: "reboot", script: `echo '{"reboot_required":true}'`},
		{name: "hook failure", script: "exit 1", failure: true},
		{name: "invalid flag", script: `echo '{"reboot_required":"yes"}'`, failure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldBase, oldRam, oldWeb := base, ram, webRoot
			base, ram, webRoot = t.TempDir(), t.TempDir(), t.TempDir()
			defer func() { base, ram, webRoot = oldBase, oldRam, oldWeb }()
			directory := t.TempDir()
			fixture(t, directory, "1.0", "echo '{}'")
			if err := os.WriteFile(filepath.Join(directory, "bin/module"), []byte("#!/bin/sh\n"+test.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}

			m := &Manager{registry: Registry{Schema: 1, Modules: map[string]Installed{}}, workers: map[string]*Worker{}}
			if _, err := m.handle(Request{Op: "register", Path: directory}); err != nil {
				t.Fatal(err)
			}

			result, err := m.handle(Request{Op: "remove", ID: "example"})
			_, retained := m.registry.Modules["example"]
			if test.failure {
				if err == nil || !retained {
					t.Fatal("failed cleanup removed registry entry", err)
				}
				return
			}

			if err != nil || retained || !result.(RemovalResult).RebootRequired {
				t.Fatal("removal result lost", result, err)
			}
		})
	}
}
