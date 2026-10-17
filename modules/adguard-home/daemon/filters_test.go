// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		if id == 0 || seen[id] || source.ID != id {
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

func TestFilterStatusDoesNotRepeatLongURLs(t *testing.T) {
	oldRunDir := runDir
	runDir = t.TempDir()
	t.Cleanup(func() { runDir = oldRunDir })
	configPath := filepath.Join(runDir, "config.json")
	os.WriteFile(configPath, []byte(`{"enabled":true}`), 0600)
	t.Setenv("EXT_CONFIG_PATH", configPath)
	listener, err := net.Listen("unix", filepath.Join(runDir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}

	filters := []map[string]any{}
	for index := 0; index < 16; index++ {
		filters = append(filters, map[string]any{
			"id": index + 1, "url": "https://example.org/" + strings.Repeat("x", 1000),
			"rules_count": 42, "last_updated": "2026-10-17T00:00:00Z", "update_failed": false,
		})
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"filters": filters})
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })

	status, err := filterStatus()
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(status)
	if len(data) > 16384 || strings.Contains(string(data), "https://") {
		t.Fatal("status response repeats long list URLs")
	}
	items := status["filters"].([]map[string]any)
	if len(items) != 16 || items[0]["id"] != float64(1) || items[0]["rules_count"] != float64(42) {
		t.Fatal("status projection lost list identity or counters")
	}
}
