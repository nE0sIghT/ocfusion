// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"testing"
)

func TestDependenciesRequireCompatibleProviderAndRejectCycles(t *testing.T) {
	provider := Installed{Manifest: Module{ID: "vpn", ProvidesAPI: 1}, Enabled: true}
	client := Installed{Manifest: Module{ID: "client", Dependencies: []Dependency{{ID: "vpn", API: 1}}}, Enabled: true}
	modules := map[string]Installed{"vpn": provider, "client": client}
	order, err := dependencyOrder(modules)
	if err != nil || len(order) != 2 || order[0] != "vpn" || order[1] != "client" {
		t.Fatalf("dependency order: %v %v", order, err)
	}

	delete(modules, "vpn")
	if _, err := dependencyOrder(modules); err == nil {
		t.Fatal("accepted missing provider")
	}

	provider.Manifest.ProvidesAPI = 2
	modules["vpn"] = provider
	if _, err := dependencyOrder(modules); err == nil {
		t.Fatal("accepted incompatible API")
	}

	provider.Manifest.ProvidesAPI = 1
	provider.Enabled = false
	modules["vpn"] = provider
	if _, err := dependencyOrder(modules); err == nil {
		t.Fatal("accepted disabled provider")
	}

	provider.Enabled = true
	provider.Manifest.Dependencies = []Dependency{{ID: "client", API: 1}}
	client.Manifest.ProvidesAPI = 1
	modules["vpn"] = provider
	modules["client"] = client
	if _, err := dependencyOrder(modules); err == nil {
		t.Fatal("accepted cycle")
	}
}

func TestProviderCannotBeDisabledOrRemovedWhileRequired(t *testing.T) {
	manager := &Manager{registry: Registry{Modules: map[string]Installed{
		"vpn":    {Manifest: Module{ID: "vpn", ProvidesAPI: 1}, Enabled: true},
		"client": {Manifest: Module{ID: "client", Dependencies: []Dependency{{ID: "vpn", API: 1}}}, Enabled: true},
	}}}
	if err := manager.checkDependents("vpn"); err == nil {
		t.Fatal("accepted provider removal")
	}

	provider := manager.registry.Modules["vpn"]
	provider.Enabled = false
	if err := manager.checkReplacement(provider); err == nil {
		t.Fatal("accepted provider disable")
	}

	if err := manager.checkDependents("client"); err != nil {
		t.Fatal(err)
	}
}
