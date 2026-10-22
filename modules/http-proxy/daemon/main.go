// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

func publish(state string, c Config, r *Registration) {
	status := map[string]any{"version": version, "state": state}
	if r != nil {
		status["address"] = r.Address
		status["port"] = c.Port
	}

	data, _ := json.Marshal(status)
	path := os.Getenv("EXT_PUBLIC_STATUS_PATH")
	if path == "" {
		return
	}

	temporary := path + ".new"
	if os.WriteFile(temporary, data, 0600) == nil {
		os.Rename(temporary, path)
	}
}

func run() error {
	// This directory contains only the immutable executable. Private settings
	// remain in the platform configuration and are passed through inherited stdin.
	if err := os.MkdirAll(runDir, 0711); err != nil {
		return err
	}

	os.Chmod(runDir, 0711)
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	binary, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "3proxy"))
	if err != nil {
		return err
	}

	workerPath := filepath.Join(runDir, "3proxy")
	if err = os.WriteFile(workerPath, binary, 0755); err != nil {
		return err
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stop)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var worker *exec.Cmd
	var done chan error
	var active Config
	var activeRegistration Registration
	terminate := func() {
		if worker != nil {
			worker.Process.Kill()
			<-done
			worker = nil
		}
	}

	defer func() {
		terminate()
		provider("egress-release", Config{})
	}()
	for {
		c, e := loadConfig()
		if e != nil {
			terminate()
			publish("UNAVAILABLE", c, nil)
		} else if !c.Enabled {
			terminate()
			provider("egress-release", c)
			publish("DISABLED", c, nil)
		} else {
			if !reflect.DeepEqual(c, active) {
				terminate()
				provider("egress-release", c)
				active = c
			}

			response, e := provider("egress-register", c)
			if e == nil && response.Egress != nil && response.Egress.Ready && !validRegistration(c, *response.Egress) {
				terminate()
				publish("CONFIG_ERROR", c, nil)
			} else if e != nil || response.Egress == nil || !response.Egress.Ready {
				terminate()
				state := "WAITING_VPN"
				if e != nil {
					provider("egress-release", c)
					state = "UNAVAILABLE"
				}

				publish(state, c, nil)
			} else {
				r := *response.Egress
				if worker != nil && !reflect.DeepEqual(activeRegistration, r) {
					terminate()
				}

				if worker == nil {
					worker = exec.Command(workerPath, "/dev/stdin")
					worker.Stdin = strings.NewReader(proxyConfig(c, r))
					worker.Stdout = io.Discard
					worker.Stderr = io.Discard
					worker.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(r.UID), Gid: uint32(r.UID), Groups: []uint32{}}}
					if e = worker.Start(); e != nil {
						worker = nil
						publish("UNAVAILABLE", c, nil)
					} else {
						done = make(chan error, 1)
						current := worker
						finished := done
						go func() {
							finished <- current.Wait()
						}()
						activeRegistration = r
					}
				}

				if worker != nil {
					select {
					case <-done:
						worker = nil
						publish("UNAVAILABLE", c, nil)
					default:
						publish("RUNNING", c, &r)
					}
				}
			}
		}

		select {
		case <-stop:
			return nil
		case <-ticker.C:
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(1)
	}

	operation := os.Args[1]
	if operation == "version" {
		commit := revision
		if len(commit) > 7 {
			commit = commit[:7]
		}

		fmt.Println("ocfusion-http-proxy", version, "("+commit+")")
		return
	}

	var envelope struct {
		Config     json.RawMessage `json:"config"`
		ConfigPath string          `json:"config_path"`
	}

	if json.NewDecoder(io.LimitReader(os.Stdin, 20000)).Decode(&envelope) != nil {
		os.Exit(1)
	}

	if envelope.ConfigPath != "" {
		os.Setenv("EXT_CONFIG_PATH", envelope.ConfigPath)
	}

	var result any = map[string]bool{"ok": true}
	var err error
	switch operation {
	case "validate", "reload":
		var incoming Config
		incoming, err = parseConfig(envelope.Config)
		if err == nil && operation == "validate" && len(envelope.Config) > 0 && string(envelope.Config) != "null" {
			var current Config
			current, err = loadConfig()
			if err == nil && !reflect.DeepEqual(current, incoming) && incoming.Revision != current.Revision+1 {
				err = errors.New("settings changed; reload before saving")
			}
		}
	case "prepare":
		err = os.MkdirAll(runDir, 0711)
	case "run":
		err = run()
	case "settings":
		result, err = loadConfig()
	case "targets":
		result, err = provider("egress-list", Config{})
	case "remove":
		_, err = provider("egress-release", Config{})
	case "diagnose":
		result = map[string]string{"version": version, "state": "UNAVAILABLE"}
		data, readErr := os.ReadFile(os.Getenv("EXT_PUBLIC_STATUS_PATH"))
		if readErr == nil && len(data) <= 8192 {
			var status map[string]any
			if json.Unmarshal(data, &status) == nil {
				result = status
			}
		}
	default:
		err = errors.New("unsupported operation")
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "http-proxy:", err)
		os.Exit(1)
	}

	json.NewEncoder(os.Stdout).Encode(result)
}
