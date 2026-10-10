// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTransactionRestoresPrivateModuleAndPlatformState(t *testing.T) {
	previousBase := base
	previousStateRoot := stateRoot
	previousRunner := runner
	stateRoot = t.TempDir()
	base = filepath.Join(stateRoot, "ocfusion-platform")
	t.Cleanup(func() {
		base = previousBase
		stateRoot = previousStateRoot
		runner = previousRunner
	})

	runner = func(name string, args ...string) (string, error) {
		return "", nil
	}

	moduleState := filepath.Join(stateRoot, "ocfusion-example")
	os.MkdirAll(moduleState, 0700)
	secret := filepath.Join(moduleState, "credentials.json")
	os.WriteFile(secret, []byte("original private credentials"), 0600)
	os.MkdirAll(base, 0700)
	registry := filepath.Join(base, "registry.json")
	os.WriteFile(registry, []byte("original registry"), 0600)

	directory := t.TempDir()
	packageDirectory := filepath.Join(directory, "example")
	os.MkdirAll(filepath.Join(packageDirectory, "bin"), 0755)
	os.WriteFile(filepath.Join(packageDirectory, "bin/module"), []byte("#!/bin/sh\nexit 0\n"), 0755)
	manifest := Module{Schema: 1, ID: "example", Name: "Example", Version: "1", API: 1, Entry: "bin/module", StatePaths: []string{moduleState}}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(packageDirectory, "module.json"), data, 0644)
	backup := filepath.Join(t.TempDir(), "backup")
	if err := backupState(directory, backup); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(secret, []byte("changed"), 0644)
	os.WriteFile(registry, []byte("changed"), 0600)
	if err := restoreState(backup); err != nil {
		t.Fatal(err)
	}

	restored, _ := os.ReadFile(secret)
	info, _ := os.Stat(secret)
	restoredRegistry, _ := os.ReadFile(registry)
	if string(restored) != "original private credentials" || info.Mode().Perm() != 0600 || string(restoredRegistry) != "original registry" {
		t.Fatal("rollback lost private state or permissions")
	}
}
