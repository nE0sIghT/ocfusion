// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var version = "dev"
var revision = "unknown"

func run(c Controller) error {
	path := os.Getenv("EXT_PUBLIC_STATUS_PATH")
	if path == "" {
		return errors.New("public status path unavailable")
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		status, err := c.status()
		if err != nil {
			status = map[string]any{"state": "UNAVAILABLE"}
		}

		if err := writeJSON(path, status); err != nil {
			return errors.New("status publication failed")
		}

		select {
		case <-signals:
			return nil
		case <-ticker.C:
		}
	}
}

func execute(c Controller, operation string, config json.RawMessage) (any, error) {
	if operation == "run" {
		return nil, run(c)
	}

	lock, err := c.lock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()

	switch operation {
	case "validate", "reload":
		if len(config) != 0 && string(config) != "null" && string(config) != "{}" {
			return nil, errors.New("use explicit apply or restore actions")
		}

		return map[string]bool{"ok": true}, nil
	case "prepare":
		_, err := c.load()
		return map[string]bool{"ok": err == nil}, err
	case "settings":
		return c.settings()
	case "diagnose":
		return c.status()
	case "remove":
		state, err := c.load()
		if err != nil {
			return nil, err
		}

		state, err = c.restore(state.Revision)
		if err != nil {
			return nil, err
		}

		rebootRequired := state.Phase == "REBOOT_REQUIRED"
		if rebootRequired {
			status, err := c.status()
			if err != nil {
				return nil, err
			}

			rebootRequired = status["state"] != "STOCK"
		}

		return map[string]bool{"reboot_required": rebootRequired}, nil
	case "apply", "restore":
		var request struct {
			Revision *uint64 `json:"revision"`
			Profile  string  `json:"profile"`
		}
		if json.Unmarshal(config, &request) != nil || request.Revision == nil {
			return nil, errors.New("settings revision required")
		}

		if operation == "restore" {
			state, err := c.restore(*request.Revision)
			return actionResult(state), err
		}

		if _, ok := findProfile(request.Profile); !ok {
			return nil, errors.New("unsupported native profile")
		}

		state, err := c.apply(*request.Revision, request.Profile)
		return actionResult(state), err
	default:
		return nil, errors.New("unsupported operation")
	}
}

func actionResult(state State) Settings {
	return Settings{Revision: state.Revision, Mode: state.Mode, Phase: state.Phase}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: ocfusion-radio-settings OPERATION")
		os.Exit(1)
	}

	if os.Args[1] == "version" {
		commit := revision
		if len(commit) > 7 {
			commit = commit[:7]
		}

		fmt.Println("OCFusion radio settings", version, "("+commit+")")
		return
	}

	var envelope struct {
		Config     json.RawMessage `json:"config"`
		ConfigPath string          `json:"config_path"`
	}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 20000)).Decode(&envelope); err != nil {
		fmt.Fprintln(os.Stderr, "OCFusion radio settings: invalid envelope")
		os.Exit(1)
	}

	configPath := os.Getenv("EXT_CONFIG_PATH")
	if envelope.ConfigPath != "" {
		configPath = envelope.ConfigPath
	}

	if configPath == "" {
		fmt.Fprintln(os.Stderr, "OCFusion radio settings: configuration path unavailable")
		os.Exit(1)
	}

	controller := Controller{
		Directory: filepath.Join(filepath.Dir(configPath), "data"),
		Hardware:  stockHardware{},
	}
	result, err := execute(controller, os.Args[1], envelope.Config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "OCFusion radio settings:", err)
		os.Exit(1)
	}

	json.NewEncoder(os.Stdout).Encode(result)
}
