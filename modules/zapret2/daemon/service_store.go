// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

type ServiceSnapshot struct {
	Checksum string   `json:"checksum"`
	ID       string   `json:"id"`
	Commit   string   `json:"commit"`
	Digest   string   `json:"digest"`
	Updated  int64    `json:"updated"`
	Domains  []string `json:"domains"`
	Networks []string `json:"networks"`
}

type ServiceStatus struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Updated  int64  `json:"updated"`
	Domains  int    `json:"domains"`
	Networks int    `json:"networks"`
}

type serviceEntry struct {
	snapshot ServiceSnapshot
	state    string
	next     time.Time
}

type ServiceStore struct {
	client    *http.Client
	mu        sync.Mutex
	entries   map[string]*serviceEntry
	busy      bool
	directory string
	done      chan struct{}
}

func serviceDataDir() string {
	if path := os.Getenv("EXT_CONFIG_PATH"); path != "" {
		return filepath.Join(filepath.Dir(path), "data", "lists")
	}
	return filepath.Join(runDir, "list-cache")
}

func readServiceCache(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 4*maxFeedBytes+1))
	if err != nil || len(data) > 4*maxFeedBytes {
		return nil, errors.New("service cache exceeds limits")
	}
	return data, nil
}

func serviceChecksum(snapshot ServiceSnapshot) string {
	snapshot.Checksum = ""
	data, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func loadServiceSnapshot(path, id string) (ServiceSnapshot, error) {
	var snapshot ServiceSnapshot
	file, err := os.Open(path)
	if err != nil {
		return snapshot, err
	}
	defer file.Close()
	// Decode from a bounded read; never trust sizes in a retained cache file.
	data, err := readServiceCache(file)
	if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot.ID != id || !commitPattern.MatchString(snapshot.Commit) || len(snapshot.Digest) != 64 || snapshot.Updated <= 0 || snapshot.Checksum != serviceChecksum(snapshot) {
		return snapshot, errors.New("invalid service cache")
	}
	domains, err := parseServiceFeed([]byte(strings.Join(snapshot.Domains, "\n")), false)
	if err != nil || !reflect.DeepEqual(domains, snapshot.Domains) {
		return snapshot, errors.New("invalid cached domains")
	}
	if len(snapshot.Networks) > 0 {
		networks, err := parseServiceFeed([]byte(strings.Join(snapshot.Networks, "\n")), true)
		if err != nil || !reflect.DeepEqual(networks, snapshot.Networks) {
			return snapshot, errors.New("invalid cached subnets")
		}
	}
	return snapshot, nil
}

func newServiceStore() *ServiceStore {
	store := &ServiceStore{client: serviceHTTP(), entries: map[string]*serviceEntry{}, directory: serviceDataDir()}
	for _, service := range serviceCatalog() {
		entry := &serviceEntry{state: "NOT_LOADED"}
		snapshot, err := loadServiceSnapshot(filepath.Join(store.directory, service.ID+".json"), service.ID)
		if err == nil {
			entry.snapshot = snapshot
			entry.state = "READY"
			entry.next = time.Unix(snapshot.Updated, 0).Add(24 * time.Hour)
			if time.Unix(snapshot.Updated, 0).After(time.Now()) {
				entry.next = time.Time{}
			}
		}
		store.entries[service.ID] = entry
	}
	return store
}

func (store *ServiceStore) statuses() []ServiceStatus {
	store.mu.Lock()
	defer store.mu.Unlock()
	statuses := []ServiceStatus{}
	for _, service := range serviceCatalog() {
		entry := store.entries[service.ID]
		snapshot := entry.snapshot
		statuses = append(statuses, ServiceStatus{ID: service.ID, State: entry.state, Updated: snapshot.Updated, Domains: len(snapshot.Domains), Networks: len(snapshot.Networks)})
	}
	return statuses
}

func (store *ServiceStore) effective(c Config) Config {
	store.mu.Lock()
	defer store.mu.Unlock()
	expand := func(domains, networks, ids []string) ([]string, []string) {
		domains = append([]string{}, domains...)
		networks = append([]string{}, networks...)
		for _, id := range ids {
			if entry := store.entries[id]; entry != nil {
				domains = append(domains, entry.snapshot.Domains...)
				networks = append(networks, entry.snapshot.Networks...)
			}
		}
		return uniqueEntries(domains), uniqueEntries(networks)
	}
	c.Domains, c.Networks = expand(c.Domains, c.Networks, c.Lists)
	c.ManagedSets = len(c.Lists) > 0
	c.Rules = append([]DestinationRule{}, c.Rules...)
	for i := range c.Rules {
		rule := &c.Rules[i]
		if rule.Enabled {
			rule.Domains, rule.Networks = expand(rule.Domains, rule.Networks, rule.Lists)
		}
	}
	return c
}

func (store *ServiceStore) refresh(ctx context.Context, ids []string, force bool) {
	store.mu.Lock()
	if store.busy {
		store.mu.Unlock()
		return
	}
	pending := []ServiceList{}
	for _, id := range ids {
		service, known := knownService(id)
		entry := store.entries[id]
		if known && entry != nil && (force || !time.Now().Before(entry.next)) {
			entry.state = "UPDATING"
			pending = append(pending, service)
		}
	}
	if len(pending) == 0 {
		store.mu.Unlock()
		return
	}
	store.busy = true
	store.done = make(chan struct{})
	store.mu.Unlock()
	go store.update(ctx, pending)
}

func (store *ServiceStore) update(ctx context.Context, services []ServiceList) {
	defer func() {
		store.mu.Lock()
		store.busy = false
		close(store.done)
		store.mu.Unlock()
	}()
	client := store.client
	defer client.CloseIdleConnections()
	commit, commitErr := serviceCommit(ctx, client)
	for _, service := range services {
		var snapshot ServiceSnapshot
		err := commitErr
		if err == nil {
			snapshot, err = fetchService(ctx, client, service, commit)
		}
		if err == nil {
			err = os.MkdirAll(store.directory, 0700)
		}
		if err == nil {
			// Publish all parts atomically; failures never replace a previous snapshot.
			err = writeJSON(filepath.Join(store.directory, service.ID+".json"), snapshot)
		}
		store.mu.Lock()
		entry := store.entries[service.ID]
		entry.state = "ERROR"
		entry.next = time.Now().Add(15 * time.Minute)
		if err == nil {
			entry.snapshot = snapshot
			entry.state = "READY"
			entry.next = time.Now().Add(24 * time.Hour)
		}
		store.mu.Unlock()
	}
}

func (store *ServiceStore) wait() {
	store.mu.Lock()
	done := store.done
	store.mu.Unlock()
	if done != nil {
		<-done
	}
}

func requestServiceRefresh(data []byte) error {
	var request struct {
		Revision uint64 `json:"revision"`
	}
	if decodeScan(data, &request) != nil {
		return errors.New("invalid list refresh request")
	}
	c, err := loadConfig()
	if err != nil || c.Revision != request.Revision || len(selectedServiceIDs(c)) == 0 {
		return errors.New("save service list selection before updating")
	}
	status, err := os.Stat(scanPath("service.json"))
	if err != nil || time.Since(status.ModTime()) > 15*time.Second {
		return errors.New("module service unavailable")
	}
	return writeJSON(filepath.Join(runDir, "refresh-lists.json"), request)
}

func serviceRefreshRequested(c Config) bool {
	path := filepath.Join(runDir, "refresh-lists.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	os.Remove(path)
	var request struct {
		Revision uint64 `json:"revision"`
	}
	return decodeScan(data, &request) == nil && request.Revision == c.Revision
}

func serviceStatuses() []ServiceStatus {
	var statuses []ServiceStatus
	data, err := os.ReadFile(filepath.Join(runDir, "list-status.json"))
	if err == nil && len(data) < 8192 {
		_ = json.Unmarshal(data, &statuses)
	}
	return statuses
}

func workerListData(entries []string, domains bool) []byte {
	values := make([]string, len(entries))
	for i, value := range entries {
		values[i] = value
		if domains {
			// Publisher .tld entries are suffixes; nfqws2 checks the label after a dot.
			values[i] = strings.TrimPrefix(value, ".")
		}
	}
	return []byte(strings.Join(values, "\n") + "\n")
}
