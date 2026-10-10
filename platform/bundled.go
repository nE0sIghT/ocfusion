// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func bundledPackages(directory string) (map[string]Installed, error) {
	packages := map[string]Installed{}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			return nil, errors.New("bundled modules must be package directories")
		}

		item, err := readPackage(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}

		if entry.Name() != item.Manifest.ID {
			return nil, errors.New("module directory does not match manifest ID")
		}

		packages[item.Manifest.ID] = item
	}

	return packages, nil
}

func prepareBundled(directory string) error {
	packages, err := bundledPackages(directory)
	if err != nil {
		return err
	}

	order, err := dependencyOrder(packages)
	if err != nil {
		return err
	}

	for _, id := range order {
		item := packages[id]
		if item.Manifest.Install {
			if _, err := invoke(item, item.Path, "install", nil); err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Manager) registerSet(directory string) (any, error) {
	packages, err := bundledPackages(directory)
	if err != nil {
		return nil, err
	}

	next := map[string]Installed{}
	for id, item := range m.registry.Modules {
		next[id] = item
	}

	for id, item := range packages {
		if old, exists := next[id]; exists {
			item.Enabled = old.Enabled
		}

		next[id] = item
	}

	order, err := dependencyOrder(next)
	if err != nil {
		return nil, err
	}

	previous := m.registry.Modules
	m.registry.Modules = next
	for _, id := range order {
		item := next[id]
		if err := m.resourceConflict(item); err != nil {
			m.registry.Modules = previous
			return nil, err
		}
	}

	if err := m.save(); err != nil {
		m.registry.Modules = previous

		return nil, err
	}

	for index := len(order) - 1; index >= 0; index-- {
		id := order[index]
		if _, changed := packages[id]; changed {
			m.stopDependents(id)
			m.stop(id)
			delete(m.workers, id)
		}
	}

	return m.registry, nil
}

func checkBundled(directory string) error {
	packages, err := bundledPackages(directory)
	if err != nil {
		return err
	}

	for id := range packages {
		ready := false
		state := "STOPPED"
		for attempt := 0; attempt < 40; attempt++ {
			response, err := rpc(Request{Op: "status", ID: id})
			if err == nil && response.OK {
				data, _ := json.Marshal(response.Data)
				var status struct {
					State   string `json:"state"`
					Enabled bool   `json:"enabled"`
				}
				json.Unmarshal(data, &status)
				state = status.State
				if status.State == "RUNNING" || !status.Enabled {
					ready = true
					break
				}
			}

			time.Sleep(time.Second)
		}

		if !ready {
			return fmt.Errorf("module %s did not start", id)
		}

		fmt.Printf("CHECK module=%s state=%s\n", id, state)
		response, diagnosticErr := rpc(Request{Op: "action", ID: id, Action: "diagnose"})
		if diagnosticErr == nil && response.OK {
			data, _ := json.Marshal(response.Data)
			fmt.Printf("CHECK module=%s diagnostic=%s\n", id, data)
		}
	}

	return nil
}

func removeBundled(directory string) error {
	packages, err := bundledPackages(directory)
	if err != nil {
		return err
	}

	order, err := dependencyOrder(packages)
	if err != nil {
		return err
	}

	for i := len(order) - 1; i >= 0; i-- {
		response, err := rpc(Request{Op: "remove", ID: order[i]})
		if err != nil {
			return err
		}

		if !response.OK && response.Error != "module not installed" {
			return errors.New(response.Error)
		}
	}

	return nil
}
