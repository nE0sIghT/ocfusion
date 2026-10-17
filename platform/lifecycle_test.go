// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPublishedLifecycle(t *testing.T) {
	oldWeb, oldMarker := webRoot, installMarker
	webRoot = t.TempDir()
	installMarker = filepath.Join(t.TempDir(), "installing")
	t.Cleanup(func() {
		webRoot, installMarker = oldWeb, oldMarker
	})

	m := &Manager{registry: Registry{Modules: map[string]Installed{}}, workers: map[string]*Worker{}}
	for _, expected := range []string{"READY", "STOPPING", "STOPPED", "INSTALLING"} {
		if expected == "STOPPING" {
			m.stopping = true
		}
		if expected == "STOPPED" {
			m.stopped = true
		}
		if expected == "INSTALLING" {
			if err := os.WriteFile(installMarker, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}

		m.publish()
		data, err := os.ReadFile(filepath.Join(webRoot, "extensions-modules.json"))
		if err != nil {
			t.Fatal(err)
		}

		var status struct {
			State   string `json:"state"`
			Updated int64  `json:"updated_at"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatal(err)
		}
		if status.State != expected || status.Updated == 0 {
			t.Fatalf("unexpected lifecycle: %+v, expected %s", status, expected)
		}
	}
}

func TestPublishedModuleOrderSurvivesStatusUpdates(t *testing.T) {
	oldWeb, oldRAM := webRoot, ram
	webRoot, ram = t.TempDir(), t.TempDir()

	t.Cleanup(func() {
		webRoot, ram = oldWeb, oldRAM
	})

	ids := []string{"vpn", "radio-settings", "openconnect", "adguard-home"}
	m := &Manager{
		registry: Registry{Modules: map[string]Installed{}},
		workers:  map[string]*Worker{},
	}
	for _, id := range ids {
		m.registry.Modules[id] = Installed{
			Manifest: Module{ID: id, Name: id},
		}
		m.workers[id] = &Worker{state: "RUNNING"}
	}

	want := []string{"adguard-home", "openconnect", "radio-settings", "vpn"}
	for update := 0; update < 32; update++ {
		m.workers[ids[update%len(ids)]].state = "STOPPED"
		m.publish()

		data, err := os.ReadFile(filepath.Join(webRoot, "extensions-modules.json"))
		if err != nil {
			t.Fatal(err)
		}

		var status struct {
			Modules []struct {
				ID string `json:"id"`
			} `json:"modules"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatal(err)
		}

		var got []string
		for _, module := range status.Modules {
			got = append(got, module.ID)
		}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("module order changed on update %d: got %v, want %v", update, got, want)
		}
	}
}
