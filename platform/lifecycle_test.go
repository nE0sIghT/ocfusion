// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
