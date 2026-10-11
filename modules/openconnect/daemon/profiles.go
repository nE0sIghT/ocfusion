// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"
)

var profileID = "default"
var profileIdentifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

var clientRoot = "/jffs/ocfusion"

func selectProfile(id string) error {
	if !profileIdentifier.MatchString(id) {
		return errors.New("invalid profile ID")
	}
	profileID = id
	root = clientRoot
	runDir = "/tmp/ocfusion"
	if id != "default" {
		root = filepath.Join(clientRoot, "profiles", id)
		runDir = filepath.Join(runDir, "profiles", id)
	}

	return nil
}

func profileIDs() ([]string, error) {
	ids := []string{"default"}
	entries, err := os.ReadDir(filepath.Join(clientRoot, "profiles"))
	if os.IsNotExist(err) {
		return ids, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() && profileIdentifier.MatchString(entry.Name()) && entry.Name() != "default" {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)

	return ids, nil
}

func profileAction(action string, input io.Reader) (any, error) {
	var envelope struct {
		Config struct {
			Profile string          `json:"profile"`
			Data    json.RawMessage `json:"data"`
		} `json:"config"`
	}
	if err := json.NewDecoder(io.LimitReader(input, 20000)).Decode(&envelope); err != nil {
		return nil, err
	}
	id := envelope.Config.Profile
	if id == "" {
		id = "default"
	}
	if err := selectProfile(id); err != nil {
		return nil, err
	}
	if id != "default" && action != "add-profile" && action != "profiles" {
		if _, err := os.Stat(filepath.Join(root, "manifest.json")); err != nil {
			return nil, errors.New("profile not found")
		}
	}

	switch action {
	case "profiles":
		return profileIDs()
	case "add-profile":
		if id == "default" {
			return nil, errors.New("default profile already exists")
		}
		if _, err := os.Stat(root); err == nil {
			return nil, errors.New("profile already exists")
		}
		baseData, err := os.ReadFile(filepath.Join(clientRoot, "manifest.json"))
		if err != nil {
			return nil, err
		}
		var base Manifest
		if err = json.Unmarshal(baseData, &base); err != nil {
			return nil, err
		}
		if err = setup(base.Bundle); err != nil {
			os.RemoveAll(root)
			return nil, err
		}
	case "delete-profile":
		if id == "default" {
			return nil, errors.New("default profile cannot be deleted")
		}
		if _, err := vpnCall(vpnRequest{Operation: "release"}); err != nil {
			return nil, err
		}
		stop()
		if err := os.RemoveAll(root); err != nil {
			return nil, err
		}
	case "settings":
		return readSettings()
	case "update-settings":
		if err := patchProfile(envelope.Config.Data); err != nil {
			return nil, err
		}
	case "import-profile":
		profile, err := parseProfile(envelope.Config.Data)
		if err != nil {
			return nil, err
		}
		if err = saveJSON(filepath.Join(root, "profile.json"), profile); err != nil {
			return nil, err
		}
	case "diagnose":
		data, err := readStatus()
		if os.IsNotExist(err) {
			return map[string]string{"state": "STARTING"}, nil
		}
		return json.RawMessage(data), err
	case "connect", "disconnect", "reconnect":
		profile, err := loadProfile()
		if err != nil {
			return nil, err
		}
		profile.Enabled = action != "disconnect"
		if err = saveJSON(filepath.Join(root, "profile.json"), profile); err != nil {
			return nil, err
		}
		if action == "reconnect" {
			stop()
		}
	default:
		return nil, errors.New("unsupported profile action")
	}

	return map[string]bool{"ok": true}, nil
}

func superviseProfiles() error {
	processes := map[string]*exec.Cmd{}
	exits := make(chan string, 16)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	defer func() {
		for _, process := range processes {
			process.Process.Signal(syscall.SIGTERM)
		}
		deadline := time.After(5 * time.Second)
		for len(processes) > 0 {
			select {
			case id := <-exits:
				delete(processes, id)
			case <-deadline:
				for _, process := range processes {
					process.Process.Kill()
				}
				return
			}
		}
	}()
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	for {
		ids, err := profileIDs()
		if err != nil {
			return err
		}
		wanted := map[string]bool{}
		for _, id := range ids {
			wanted[id] = true
			if processes[id] != nil {
				continue
			}
			if err := selectProfile(id); err != nil {
				return err
			}
			manifest, err := loadManifest()
			if err != nil {
				continue
			}
			if err = setup(manifest.Bundle); err != nil {
				continue
			}

			command := exec.Command(filepath.Join(clientRoot, "ocfusion"), "daemon")
			command.Env = append(os.Environ(), "OCF_PROFILE="+id)
			if err = command.Start(); err != nil {
				continue
			}
			processes[id] = command
			go func(id string, command *exec.Cmd) {
				command.Wait()
				exits <- id
			}(id, command)
		}
		for id, process := range processes {
			if !wanted[id] {
				process.Process.Signal(syscall.SIGTERM)
			}
		}

		select {
		case id := <-exits:
			delete(processes, id)
			// Avoid tight restart loops on persistent profile errors.
			select {
			case <-tick.C:
			case <-signals:
				return nil
			}
		case <-tick.C:
		case <-signals:
			return nil
		}
	}
}
