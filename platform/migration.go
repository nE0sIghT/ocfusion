// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
)

// Rewrite only package locations owned by the old platform namespace.
func migrateRegistryPaths(registry *Registry) bool {
	changed := false

	for id, item := range registry.Modules {
		for _, path := range []*string{&item.Path, &item.Previous} {
			const legacy = "/tmp/opt/asus-extensions/modules/"
			if strings.HasPrefix(*path, legacy) {
				*path = packageRoot + "/" + strings.TrimPrefix(*path, legacy)
				changed = true
			}
		}

		registry.Modules[id] = item
	}

	return changed
}
