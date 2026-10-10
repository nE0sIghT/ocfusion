// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Registry struct {
	Schema  int                  `json:"schema"`
	Modules map[string]Installed `json:"modules"`
}

func atomicJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}

	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}

	tmp := path + ".new"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}

	return os.Rename(tmp, path)
}

func (m *Manager) save() error {
	return atomicJSON(base+"/registry.json", m.registry)
}

// Check before removing the shared platform, including when its daemon is stopped.
func checkPlatformRemoval(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	var registry Registry
	if err = json.Unmarshal(b, &registry); err != nil {
		return err
	}

	for id, item := range registry.Modules {
		if item.Path != "/tmp/opt/ocfusion" && !strings.HasPrefix(item.Path, "/tmp/opt/ocfusion/modules/") {
			return fmt.Errorf("remove additional module %s before uninstalling OCFusion", id)
		}
	}

	return nil
}
