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
	"syscall"
	"time"
)

type Worker struct {
	command *exec.Cmd
	done    chan error
}

func startWorker(config Config, registration Registration) (*Worker, error) {
	native, hosts, err := nativeConfig(config, registration)
	if err != nil {
		return nil, err
	}

	if err := writeJSON(filepath.Join(runDir, "AdGuardHome.yaml"), native); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Join(runDir, "work"), 0700); err != nil {
		return nil, err
	}

	executable := filepath.Join(os.Getenv("EXT_PAYLOAD_PATH"), "bin/AdGuardHome")
	command := exec.Command(
		executable,
		"--config", filepath.Join(runDir, "AdGuardHome.yaml"),
		"--work-dir", filepath.Join(runDir, "work"),
		"--no-check-update",
	)
	overrides, _ := json.Marshal(hosts)
	log := &boundedLog{path: filepath.Join(runDir, "worker.log")}
	command.Stderr = log
	command.Stdout = log

	command.Env = append(
		os.Environ(),
		"SSL_CERT_FILE="+filepath.Join(os.Getenv("EXT_PAYLOAD_PATH"), "ca-bundle.crt"),
		"OCF_CONTROL_SOCKET="+filepath.Join(runDir, "control.sock"),
		"OCF_DNS_MARK="+registration.Mark,
		"OCF_DNS_HOSTS="+string(overrides),
		fmt.Sprintf("OCF_DNS_VPN=%t", registration.VPN),
	)
	if err := command.Start(); err != nil {
		return nil, errors.New("AdGuard launch failed")
	}

	worker := &Worker{command: command, done: make(chan error, 1)}
	go func() {
		worker.done <- command.Wait()
	}()

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := privateAPI("/control/status"); err == nil {
			return worker, nil
		}

		select {
		case <-worker.done:
			return nil, errors.New("AdGuard startup failed")
		case <-time.After(200 * time.Millisecond):
		}
	}

	stopWorker(worker)
	return nil, errors.New("AdGuard startup timed out")
}

func stopWorker(worker *Worker) {
	if worker == nil {
		return
	}

	worker.command.Process.Signal(syscall.SIGTERM)
	select {
	case <-worker.done:
	case <-time.After(3 * time.Second):
		worker.command.Process.Kill()
		<-worker.done
	}
}

func run() error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var worker *Worker
	var previous Config
	var previousRegistration Registration
	previousPath := ""
	var refreshDone chan struct{}
	defer func() {
		provider("resolver-release")
		stopWorker(worker)
	}()

	for {
		config, err := loadConfig()
		state := "DISABLED"
		dns, dnsErr := provider("dns-state")
		dnsPath := dns.DNSPath()
		if err != nil {
			state = "CONFIG_ERROR"
		}

		if config.Enabled && err == nil {
			info, infoErr := provider("resolver-info")
			if infoErr != nil || dnsErr != nil {
				state = "WAIT_PROVIDER"
				provider("resolver-release")
				stopWorker(worker)
				worker = nil
			} else {
				info.Resolver.VPN = dnsPath == "VPN"
				changed := !reflect.DeepEqual(config, previous) || !reflect.DeepEqual(info.Resolver, previousRegistration) || (len(config.Upstreams) > 0 && dnsPath != previousPath)
				if worker != nil && changed {
					provider("resolver-release")
					stopWorker(worker)
					worker = nil
				}

				if worker != nil {
					select {
					case <-worker.done:
						worker = nil
						provider("resolver-release")
					default:
					}
				}

				if worker == nil {
					worker, err = startWorker(config, info.Resolver)
					previous = config
					previousRegistration = info.Resolver
					previousPath = dnsPath
				}

				state = "ERROR"
				if worker != nil {
					if _, err := provider("resolver-register"); err == nil {
						state = "RUNNING"
					} else {
						provider("resolver-release")
						stopWorker(worker)
						worker = nil
						state = "WAIT_PROVIDER"
					}
				}
			}
		} else if worker != nil {
			provider("resolver-release")
			stopWorker(worker)
			worker = nil
		}

		if refreshDone != nil {
			select {
			case <-refreshDone:
				refreshDone = nil
			default:
			}
		}
		if worker != nil && refreshDone == nil {
			if _, err := os.Stat(filepath.Join(runDir, "refresh")); err == nil {
				refreshDone = make(chan struct{})
				go func(done chan struct{}) {
					privateAPI("/control/filtering/refresh")
					os.Remove(filepath.Join(runDir, "refresh"))
					close(done)
				}(refreshDone)
			}
		}

		status := map[string]any{"state": state, "dns_path": dnsPath, "version": version}
		if worker != nil {
			if raw, err := privateAPI("/control/filtering/status"); err == nil {
				ready, failed := true, false
				items, _ := raw["filters"].([]any)
				for _, item := range items {
					filter, _ := item.(map[string]any)
					if date, _ := filter["last_updated"].(string); date == "" {
						ready = false
					}
					if filter["update_failed"] == true {
						failed = true
					}
				}
				status["lists_ready"] = ready
				status["list_update_failed"] = failed
			}
		}

		if path := os.Getenv("EXT_PUBLIC_STATUS_PATH"); path != "" {
			writeJSON(path, status)
		}

		select {
		case <-signals:
			return nil
		case <-ticker.C:
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: ocfusion-adguard-home OPERATION")
		os.Exit(1)
	}

	operation := os.Args[1]
	if operation == "version" {
		commit := revision
		if len(commit) > 7 {
			commit = commit[:7]
		}

		fmt.Println("ocfusion-adguard-home", version, "("+commit+")")
		return
	}

	var envelope struct {
		Config     json.RawMessage `json:"config"`
		ConfigPath string          `json:"config_path"`
	}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 20000)).Decode(&envelope); err != nil {
		fmt.Fprintln(os.Stderr, "ocfusion-adguard-home: invalid module envelope")
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
		err = os.MkdirAll(runDir, 0700)
	case "run":
		err = run()
	case "remove":
		_, err = provider("resolver-release")
	case "settings":
		result, err = loadConfig()
	case "catalog":
		result = filterCatalog()
	case "filter-status":
		result, err = filterStatus()
	case "refresh-filters":
		err = queueFilterRefresh()
	case "statistics":
		result, err = statistics()
	case "diagnose":
		result = map[string]any{"version": version}
		_, err = privateAPI("/control/status")
		if err != nil {
			result = map[string]string{"state": "UNAVAILABLE"}
			err = nil
		}
	default:
		err = errors.New("unsupported operation")
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "ocfusion-adguard-home:", err)
		os.Exit(1)
	}

	json.NewEncoder(os.Stdout).Encode(result)
}
