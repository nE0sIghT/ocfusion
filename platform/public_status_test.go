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

func TestPublicStatusPublication(t *testing.T) {
	oldWeb, oldRAM := webRoot, ram
	webRoot, ram = t.TempDir(), t.TempDir()
	t.Cleanup(func() {
		webRoot, ram = oldWeb, oldRAM
	})

	source := filepath.Join(ram, "modules", "example", "public-status.json")
	os.MkdirAll(filepath.Dir(source), 0700)
	manager := &Manager{}
	check := func(content, state string, wantData bool) {
		t.Helper()
		if err := os.WriteFile(source, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}

		manager.publishModuleStatus("example", state)
		raw, err := os.ReadFile(filepath.Join(webRoot, "extensions-status", "example.json"))
		if err != nil {
			t.Fatal(err)
		}

		var result struct {
			Module string          `json:"module"`
			State  string          `json:"state"`
			Data   json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Module != "example" || result.State != state {
			t.Fatalf("invalid snapshot: %s", raw)
		}
		if (string(result.Data) != "null") != wantData {
			t.Fatalf("unexpected publication: %s", raw)
		}
	}

	check(`{"state":"UP"}`, "RUNNING", true)
	check(`{"state":"UP"}`, "STOPPED", false)
	check("not JSON", "RUNNING", false)
	check(`{"large":"`+strings.Repeat("x", publicStatusLimit)+`"}`, "RUNNING", false)
	check(`{"state":"UP"}`, "RUNNING", true)

	old := time.Now().Add(-time.Minute)
	os.Chtimes(source, old, old)
	manager.publishModuleStatus("example", "RUNNING")
	raw, _ := os.ReadFile(filepath.Join(webRoot, "extensions-status", "example.json"))
	if !strings.Contains(string(raw), `"data":null`) {
		t.Fatal("stale status published", string(raw))
	}

	os.Remove(source)
	os.Symlink(filepath.Join(webRoot, "extensions-status", "example.json"), source)
	manager.publishModuleStatus("example", "RUNNING")
	raw, _ = os.ReadFile(filepath.Join(webRoot, "extensions-status", "example.json"))
	if !strings.Contains(string(raw), `"data":null`) {
		t.Fatal("symlink followed", string(raw))
	}
}

func TestModulePublishesThroughManagerProvidedPath(t *testing.T) {
	oldBase, oldRAM, oldWeb := base, ram, webRoot
	base, ram, webRoot = t.TempDir(), t.TempDir(), t.TempDir()
	t.Cleanup(func() {
		base, ram, webRoot = oldBase, oldRAM, oldWeb
	})

	directory := t.TempDir()
	fixture(t, directory, "1.0", "echo '{}'")
	script := `#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
case "$1" in
    prepare)
        printf '{}\n'
        ;;
    run)
        umask 077
        printf '{"state":"UP"}\n' > "$EXT_PUBLIC_STATUS_PATH"
        trap 'exit 0' TERM

        while :; do
            sleep 0.1
        done
        ;;
esac
`
	if err := os.WriteFile(filepath.Join(directory, "bin/module"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	item, err := readPackage(directory)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{registry: Registry{Schema: 1, Modules: map[string]Installed{"example": item}}, workers: map[string]*Worker{}}
	defer manager.stop("example")

	if err := manager.start("example"); err != nil {
		t.Fatal(err)
	}
	manager.publish()
	destination := filepath.Join(webRoot, "extensions-status", "example.json")
	raw, err := os.ReadFile(destination)
	if err != nil || !strings.Contains(string(raw), `"data":{"state":"UP"}`) {
		t.Fatal("module status not published", string(raw), err)
	}

	manager.stop("example")
	manager.publish()
	raw, err = os.ReadFile(destination)
	if err != nil || !strings.Contains(string(raw), `"data":null`) || !strings.Contains(string(raw), `"state":"STOPPED"`) {
		t.Fatal("stopped status retained live data", string(raw), err)
	}
}
