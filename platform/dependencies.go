// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"sort"
)

func dependencyOrder(modules map[string]Installed) ([]string, error) {
	order := []string{}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error

	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("module dependency cycle at %s", id)
		}

		if visited[id] {
			return nil
		}

		item, exists := modules[id]
		if !exists {
			return fmt.Errorf("required module %s is not installed", id)
		}

		visiting[id] = true
		for _, dependency := range item.Manifest.Dependencies {
			provider, exists := modules[dependency.ID]
			if !exists || dependency.API < 1 || provider.Manifest.ProvidesAPI != dependency.API {
				return fmt.Errorf("module %s requires %s API %d", id, dependency.ID, dependency.API)
			}

			if item.Enabled && !provider.Enabled {
				return fmt.Errorf("module %s requires enabled module %s", id, dependency.ID)
			}

			if err := visit(dependency.ID); err != nil {
				return err
			}
		}

		visiting[id] = false
		visited[id] = true
		order = append(order, id)

		return nil
	}

	ids := []string{}
	for id := range modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}

	return order, nil
}

func (m *Manager) checkReplacement(next Installed) error {
	modules := map[string]Installed{}
	for id, item := range m.registry.Modules {
		modules[id] = item
	}

	modules[next.Manifest.ID] = next
	_, err := dependencyOrder(modules)

	return err
}

func (m *Manager) checkDependents(id string) error {
	for other, item := range m.registry.Modules {
		for _, dependency := range item.Manifest.Dependencies {
			if dependency.ID == id {
				return fmt.Errorf("module %s is required by %s", id, other)
			}
		}
	}

	return nil
}
