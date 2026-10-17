// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"hash/fnv"
	"os"
	"path/filepath"
)

//go:embed catalog.json
var catalogJSON []byte

type FilterSource struct {
	ID          uint64 `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	Recommended bool   `json:"recommended,omitempty"`
}

func filterCatalog() []FilterSource {
	var sources []FilterSource
	json.Unmarshal(catalogJSON, &sources)

	for index := range sources {
		sources[index].ID = filterID(sources[index].URL)
	}

	return sources
}

func defaultConfig() Config {
	return Config{Filters: []string{filterCatalog()[0].URL}}
}

func filterID(address string) uint64 {
	hash := fnv.New64a()
	hash.Write([]byte(address))

	return hash.Sum64() & ((1 << 53) - 1)
}

func queueFilterRefresh() error {
	config, err := loadConfig()
	if err != nil || !config.Enabled {
		return errors.New("Enable protection before updating lists.")
	}

	file, err := os.OpenFile(filepath.Join(runDir, "refresh"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	return file.Close()
}

func filterStatus() (map[string]any, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	_, pending := os.Stat(filepath.Join(runDir, "refresh"))
	result := map[string]any{"filters": []any{}, "updating": pending == nil, "enabled": config.Enabled}
	if !config.Enabled {
		return result, nil
	}

	raw, err := privateAPI("/control/filtering/status")
	if err != nil {
		return nil, err
	}

	filters := []map[string]any{}
	items, _ := raw["filters"].([]any)
	for _, item := range items {
		filter, ok := item.(map[string]any)
		if !ok {
			continue
		}

		safe := map[string]any{}
		for _, key := range []string{"id", "rules_count", "last_updated", "update_failed"} {
			safe[key] = filter[key]
		}
		filters = append(filters, safe)
	}
	result["filters"] = filters

	return result, nil
}
