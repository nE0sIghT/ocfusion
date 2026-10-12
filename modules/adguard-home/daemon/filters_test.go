// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"testing"
)

func TestCatalogDefaultsAndStableCacheIDs(t *testing.T) {
	catalog := filterCatalog()
	config, err := parseConfig(nil)
	if err != nil || config.Enabled || len(config.Filters) != 1 || config.Filters[0] != catalog[0].URL {
		t.Fatal(config, err)
	}

	seen := map[uint64]bool{}
	for _, source := range catalog {
		id := filterID(source.URL)
		if id == 0 || seen[id] {
			t.Fatal("duplicate cache identity", source)
		}
		seen[id] = true
	}

	first, _, err := nativeConfig(Config{Filters: []string{catalog[0].URL, catalog[1].URL}}, Registration{Networks: []Network{{Address: "192.168.1.1", CIDR: "192.168.1.0/24"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, _, _ := nativeConfig(Config{Filters: []string{catalog[1].URL}}, Registration{Networks: []Network{{Address: "192.168.1.1", CIDR: "192.168.1.0/24"}}})
	a := first["filters"].([]map[string]any)[1]["id"]
	b := second["filters"].([]map[string]any)[0]["id"]
	if a != b {
		t.Fatal("selection reordered cached lists")
	}

	data, _ := json.Marshal(Config{Filters: []string{}})
	config, err = parseConfig(data)
	if err != nil || len(config.Filters) != 0 {
		t.Fatal("explicit empty selection replaced", config, err)
	}
}
