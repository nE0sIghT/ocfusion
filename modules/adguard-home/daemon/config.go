// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var version = "dev"
var revision = "unknown"
var runDir = "/tmp/ocfusion-adguard-home"

const dnsPort = 53535

type Upstream struct {
	URL     string `json:"url"`
	Address string `json:"address,omitempty"`
}

type ClientRule struct {
	Address   string `json:"address"`
	Filtering bool   `json:"filtering"`
}

type Config struct {
	Revision  uint64       `json:"revision"`
	Enabled   bool         `json:"enabled"`
	Upstreams []Upstream   `json:"upstreams"`
	Filters   []string     `json:"filters"`
	Rules     []string     `json:"rules"`
	Clients   []ClientRule `json:"clients"`
	QueryLog  bool         `json:"query_log"`
}

type Network struct {
	Bridge  string `json:"bridge"`
	Address string `json:"router_address"`
	CIDR    string `json:"cidr"`
}

type Registration struct {
	VPN      bool      `json:"-"`
	Port     int       `json:"port"`
	Mark     string    `json:"mark"`
	Networks []Network `json:"networks"`
}

func parseConfig(data []byte) (Config, error) {
	var config Config
	if len(data) == 0 || string(data) == "null" {
		return defaultConfig(), nil
	}

	if len(data) > 16384 {
		return config, errors.New("configuration exceeds 16 KiB")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, errors.New("invalid AdGuard settings")
	}

	if decoder.Decode(new(any)) != io.EOF ||
		len(config.Upstreams) > 8 || len(config.Filters) > 16 ||
		len(config.Rules) > 128 || len(config.Clients) > 32 {
		return config, errors.New("AdGuard settings exceed bounds")
	}

	hosts := map[string]string{}
	for _, upstream := range config.Upstreams {
		if strings.ContainsAny(upstream.URL, "\r\n\x00") {
			return config, errors.New("invalid upstream")
		}

		if net.ParseIP(upstream.URL) == nil {
			parsed, err := url.Parse(upstream.URL)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "tls") ||
				parsed.User != nil || parsed.Hostname() == "" || parsed.Fragment != "" {
				return config, errors.New("upstream requires an IP, HTTPS URL or TLS URL")
			}
		}

		if upstream.Address != "" {
			parsed, _ := url.Parse(upstream.URL)
			if old := hosts[parsed.Hostname()]; old != "" && old != upstream.Address {
				return config, errors.New("conflicting upstream connection addresses")
			}
			hosts[parsed.Hostname()] = upstream.Address
		}

		if upstream.Address != "" && net.ParseIP(upstream.Address) == nil {
			return config, errors.New("upstream connection address must be an IP")
		}
	}

	for _, address := range config.Filters {
		parsed, err := url.Parse(address)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" || len(address) > 1024 {
			return config, errors.New("filter list requires an HTTPS URL")
		}
	}

	for _, rule := range config.Rules {
		if len(rule) > 512 || strings.ContainsAny(rule, "\r\n\x00") {
			return config, errors.New("invalid filter rule")
		}
	}

	for _, client := range config.Clients {
		_, _, err := net.ParseCIDR(client.Address)
		if net.ParseIP(client.Address) == nil && err != nil {
			return config, errors.New("client requires an IP or CIDR")
		}
	}

	encoded, err := json.Marshal(config)
	if err != nil || len(encoded) > 16384 {
		return config, errors.New("AdGuard settings exceed bounds")
	}

	return config, nil
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(os.Getenv("EXT_CONFIG_PATH"))
	if os.IsNotExist(err) {
		return defaultConfig(), nil
	}

	if err != nil {
		return Config{}, err
	}

	return parseConfig(data)
}

func writeJSON(path string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".ocf-")
	if err != nil {
		return err
	}

	defer os.Remove(file.Name())
	file.Chmod(0600)
	_, err = file.Write(encoded)
	closeErr := file.Close()
	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	return os.Rename(file.Name(), path)
}

func nativeConfig(config Config, registration Registration) (map[string]any, map[string]string, error) {
	if len(registration.Networks) == 0 {
		return nil, nil, errors.New("native resolver networks unavailable")
	}

	backend := registration.Networks[0].Address
	hosts := []string{"127.0.0.1"}
	clients := []map[string]any{}
	for _, network := range registration.Networks {
		if network.Address != "127.0.0.1" {
			hosts = append(hosts, network.Address)
		}
		if network.Bridge == "br0" {
			backend = network.Address
		}

		clients = append(clients, map[string]any{
			"name":                network.Bridge,
			"ids":                 []string{network.CIDR},
			"upstreams":           []string{network.Address},
			"use_global_settings": true,
		})
	}

	upstreams := []string{backend}
	overrides := map[string]string{}
	if len(config.Upstreams) > 0 {
		upstreams = nil
		clients = nil
		for _, upstream := range config.Upstreams {
			upstreams = append(upstreams, upstream.URL)
			if upstream.Address != "" {
				parsed, _ := url.Parse(upstream.URL)
				overrides[parsed.Hostname()] = upstream.Address
			}
		}
	}

	for i, client := range config.Clients {
		clientUpstreams := upstreams
		if len(config.Upstreams) == 0 {
			address := net.ParseIP(strings.Split(client.Address, "/")[0])
			_, clientSubnet, _ := net.ParseCIDR(client.Address)
			matched := address != nil && address.IsLoopback()
			for _, network := range registration.Networks {
				_, subnet, _ := net.ParseCIDR(network.CIDR)
				if subnet != nil && subnet.Contains(address) {
					if clientSubnet != nil {
						clientBits, _ := clientSubnet.Mask.Size()
						networkBits, _ := subnet.Mask.Size()
						if clientBits < networkBits {
							return nil, nil, errors.New("client subnet crosses native DNS networks")
						}
					}
					matched = true
					clientUpstreams = []string{network.Address}
					break
				}
			}
			if !matched {
				return nil, nil, errors.New("client address is outside native DNS networks")
			}
		}

		clients = append(clients, map[string]any{
			"name":                "client-" + strconv.Itoa(i+1),
			"ids":                 []string{client.Address},
			"upstreams":           clientUpstreams,
			"use_global_settings": false,
			"filtering_enabled":   client.Filtering,
		})
	}

	filters := []map[string]any{}
	for _, address := range config.Filters {
		name := "Custom list"
		for _, source := range filterCatalog() {
			if source.URL == address {
				name = source.Name
			}
		}

		filters = append(filters, map[string]any{"enabled": true, "url": address, "name": name, "id": filterID(address)})
	}

	result := map[string]any{
		"schema_version": 34,
		"http":           map[string]any{"address": "127.0.0.1:3000", "pprof": map[string]any{"enabled": false}},
		"users":          []any{},
		"dns": map[string]any{
			"bind_hosts":                hosts,
			"port":                      dnsPort,
			"upstream_dns":              upstreams,
			"bootstrap_dns":             []string{backend},
			"fallback_dns":              []string{},
			"cache_enabled":             true,
			"cache_size":                4194304,
			"use_private_ptr_resolvers": false,
			"use_http3_upstreams":       false,
		},
		"tls":        map[string]any{"enabled": false},
		"dhcp":       map[string]any{"enabled": false},
		"querylog":   map[string]any{"enabled": config.QueryLog, "file_enabled": false, "size_memory": 1000},
		"statistics": map[string]any{"enabled": true, "interval": "24h"},
		"filtering":  map[string]any{"protection_enabled": true, "filtering_enabled": true, "blocking_mode": "nxdomain", "filters_update_interval": 24},
		"filters":    filters,
		"user_rules": config.Rules,
		"clients":    map[string]any{"persistent": clients},
	}

	return result, overrides, nil
}
