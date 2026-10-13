// SPDX-License-Identifier: GPL-3.0-or-later
package main

type Requirements struct {
	Architecture string `json:"architecture"`
	PlatformAPI  int    `json:"platform_api"`
}

type Dependency struct {
	ID  string `json:"id"`
	API int    `json:"api"`
}

type Module struct {
	StatePaths   []string          `json:"state_paths,omitempty"`
	Install      bool              `json:"install,omitempty"`
	Schema       int               `json:"schema"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	API          int               `json:"module_api"`
	Entry        string            `json:"entrypoint"`
	UI           string            `json:"ui,omitempty"`
	Actions      []string          `json:"actions,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Requires     Requirements      `json:"requires,omitempty"`
	ProvidesAPI  int               `json:"provides_api,omitempty"`
	Dependencies []Dependency      `json:"dependencies,omitempty"`
	StockPages   []StockPage       `json:"stock_pages,omitempty"`
	Resources    map[string]string `json:"resources,omitempty"`
}

type Installed struct {
	Manifest Module            `json:"manifest"`
	Path     string            `json:"path"`
	Enabled  bool              `json:"enabled"`
	Hashes   map[string]string `json:"hashes"`
	Previous string            `json:"previous,omitempty"`
}

type StockPage struct {
	Path   string `json:"path"`
	Script string `json:"script"`
}
