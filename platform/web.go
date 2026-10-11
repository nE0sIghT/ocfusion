// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var webRoot = "/tmp/var/wwwext"

func (m *Manager) resourceConflict(next Installed) error {
	for id, other := range m.registry.Modules {
		if id == next.Manifest.ID {
			continue
		}

		for kind, value := range next.Manifest.Resources {
			if value != "" && other.Manifest.Resources[kind] == value {
				return errors.New("resource already owned by another module")
			}
		}
	}

	return nil
}

func publishUI(item Installed, payload string) error {
	if item.Manifest.UI == "" && len(item.Manifest.StockPages) == 0 {
		return nil
	}

	parent := filepath.Dir(item.Manifest.UI)
	if item.Manifest.UI != "" && parent == "." {
		return errors.New("module UI must be in a dedicated directory")
	}

	for rel := range item.Hashes {
		publish := item.Manifest.UI != "" && strings.HasPrefix(rel, parent+"/")
		for _, contribution := range item.Manifest.StockPages {
			if rel == contribution.Script {
				publish = true
			}
		}
		if !publish {
			continue
		}

		b, e := os.ReadFile(filepath.Join(payload, rel))
		if e != nil {
			return e
		}

		path := filepath.Join(webRoot, "extensions-modules", item.Manifest.ID, rel)
		if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			return e
		}

		if e = os.WriteFile(path, b, 0644); e != nil {
			return e
		}
	}

	return nil
}

func (m *Manager) publish() {
	m.publishContributions()

	modules := []map[string]any{}

	for id, item := range m.registry.Modules {
		state := "STOPPED"
		if w := m.workers[id]; w != nil && !m.stopping {
			state = w.state
		}

		ui := ""
		if item.Manifest.UI != "" {
			ui = "/user/extensions-modules/" + id + "/" + item.Manifest.UI
		}

		m.publishModuleStatus(id, state)

		modules = append(modules, map[string]any{
			"id":      id,
			"name":    item.Manifest.Name,
			"version": item.Manifest.Version,
			"enabled": item.Enabled,
			"state":   state,
			"ui":      ui,
			"status":  "/user/extensions-status/" + id + ".json",
		})
	}

	state := "READY"
	if m.stopping {
		state = "STOPPING"
	}
	if m.stopped {
		state = "STOPPED"
	}
	if _, err := os.Stat(installMarker); err == nil {
		state = "INSTALLING"
	}

	b, _ := json.Marshal(map[string]any{"version": version, "modules": modules, "state": state, "updated_at": time.Now().UnixMilli()})
	os.MkdirAll(webRoot, 0755)

	path := filepath.Join(webRoot, "extensions-modules.json")
	if os.WriteFile(path+".new", b, 0644) == nil {
		os.Rename(path+".new", path)
	}
}

func (m *Manager) publishContributions() {
	contributions := []map[string]string{}
	for id, item := range m.registry.Modules {
		if !item.Enabled {
			continue
		}

		for _, page := range item.Manifest.StockPages {
			contributions = append(contributions, map[string]string{"path": page.Path, "script": "/user/extensions-modules/" + id + "/" + page.Script})
		}
	}

	data, _ := json.Marshal(contributions)
	source := "(function () {\n    const pages = " + string(data) + ";\n    for (const page of pages) {\n        if (location.pathname === page.path) {\n            const script = document.createElement('script');\n            script.src = page.script;\n            document.head.appendChild(script);\n        }\n    }\n})();\n"
	atomicWrite(filepath.Join(webRoot, "ocfusion-contributions.js"), []byte(source), 0644)
}
