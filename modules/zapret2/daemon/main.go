// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func privateUID() (int, error) {
	used := map[int]bool{}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 3 {
			uid, _ := strconv.Atoi(fields[2])
			gid, _ := strconv.Atoi(fields[3])
			used[uid], used[gid] = true, true
		}
	}
	statuses, _ := filepath.Glob("/proc/[0-9]*/status")
	for _, path := range statuses {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "Uid:") || strings.HasPrefix(line, "Gid:") {
				for _, field := range strings.Fields(line)[1:] {
					id, _ := strconv.Atoi(field)
					used[id] = true
				}
			}
		}
	}
	for uid := 62000; uid < 62100; uid++ {
		if !used[uid] {
			return uid, nil
		}
	}
	return 0, errors.New("no free worker identity")
}

func prepareWorker(c Config, uid int) error {
	return prepareWorkerAt(c, uid, filepath.Join(runDir, "worker"))
}

func prepareWorkerAt(c Config, uid int, directory string) error {
	if err := os.MkdirAll(directory, 0750); err != nil {
		return err
	}
	// Separate normal/test identities can traverse only to their own protected
	// worker directory; status, journals and requests remain root-only.
	if err := os.Chmod(runDir, 0711); err != nil {
		return err
	}
	if err := os.Chown(directory, 0, uid); err != nil {
		return err
	}

	for _, name := range []string{"zapret-lib.lua", "zapret-antidpi.lua"} {
		data, err := os.ReadFile(filepath.Join(os.Getenv("EXT_PAYLOAD_PATH"), "lua", name))
		if err != nil {
			return err
		}
		path := filepath.Join(directory, name)
		if err = os.WriteFile(path, data, 0640); err != nil {
			return err
		}
		if err = os.Chown(path, 0, uid); err != nil {
			return err
		}
	}
	for name, entries := range map[string][]string{"domains.txt": c.Domains, "networks.txt": c.Networks} {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(strings.Join(entries, "\n")+"\n"), 0640); err != nil {
			return err
		}
		if err := os.Chown(path, 0, uid); err != nil {
			return err
		}
	}

	return nil
}

func publish(state string) {
	writeJSON(scanPath("service.json"), map[string]string{"state": state})
	if path := os.Getenv("EXT_PUBLIC_STATUS_PATH"); path != "" {
		writeJSON(path, map[string]any{"version": version, "state": state, "upstream": "1.0.6", "scan": scanStatus()})
	}
}

func queueReady(queue int) bool {
	data, _ := os.ReadFile("/proc/net/netfilter/nfnetlink_queue")
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == strconv.Itoa(queue) {
			return true
		}
	}
	return false
}

func run() error {
	if err := os.MkdirAll(runDir, 0700); err != nil {
		return err
	}
	if err := errors.Join(cleanup(), cleanupAt(scanResources(), "ocfusion:zapret2-scan")); err != nil {
		return err
	}
	resetScan()
	defer os.Remove(scanPath("service.json"))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	var worker *exec.Cmd
	var done chan error
	var active Config
	var resources Resources
	lastRepair := time.Time{}
	var scanning <-chan error
	var cancelScan context.CancelFunc

	terminate := func() error {
		err := cleanup()
		if worker != nil {
			worker.Process.Kill()
			<-done
			worker = nil
		}
		return err
	}
	defer func() {
		if cancelScan != nil {
			cancelScan()
			<-scanning
		}
		terminate()
	}()

	for {
		c, err := loadConfig()
		if scanning != nil {
			if worker != nil {
				select {
				case <-done:
					worker = nil
					cancelScan()
				default:
				}
			}
			if err != nil || scanCancelled(c) {
				cancelScan()
			}
			select {
			case <-scanning:
				scanning = nil
				cancelScan = nil
			default:
				state := "DISABLED"
				if worker != nil {
					state = "RUNNING"
				}
				publish(state)
				select {
				case <-stop:
					return nil
				case <-tick.C:
				}
				continue
			}
		}
		if request, ok := pendingScan(); ok && err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			cancelScan = cancel
			finished := make(chan error, 1)
			scanning = finished
			bypass := ""
			if worker != nil {
				bypass = resources.Mark
			}
			go func() {
				defer cancel()
				finished <- performScan(ctx, request, c, bypass)
			}()
			continue
		}
		state := "DISABLED"
		if err != nil || !c.Enabled || !hasSelection(c) {
			cleanupErr := terminate()
			if err != nil || cleanupErr != nil {
				state = "ERROR"
			} else if c.Enabled {
				state = "EMPTY"
			}
		} else {
			if worker != nil {
				select {
				case <-done:
					worker = nil
					cleanup()
				default:
				}
			}
			if worker != nil && !reflect.DeepEqual(active, c) {
				err = terminate()
			}
			if worker != nil {
				// Only a genuine WAN-interface change restarts packet processing.
				checked := map[string]bool{}
				for _, rule := range resources.Rules {
					if checked[rule.Tool] {
						continue
					}
					checked[rule.Tool] = true
					family := "-4"
					if rule.Tool == "ip6tables" {
						family = "-6"
					}
					iface, routeErr := wan(family)
					if routeErr != nil || len(rule.Args) < 2 || rule.Args[1] != iface {
						err = terminate()
						break
					}
				}
			}
			if worker == nil && err == nil {
				err = cleanup()
				if err == nil {
					resources, err = allocate(c)
				}
				uid := 0
				if err == nil {
					uid, err = privateUID()
				}
				if err == nil {
					err = prepareWorker(c, uid)
				}
				if err == nil {
					// connbytes only counts new connections after accounting is enabled.
					accounting := "/proc/sys/net/netfilter/nf_conntrack_acct"
					data, readErr := os.ReadFile(accounting)
					err = readErr
					if err == nil && strings.TrimSpace(string(data)) != "1" {
						err = os.WriteFile(accounting, []byte("1\n"), 0600)
						resources.Acct = true
					}
				}
				if err == nil {
					err = writeJSON(filepath.Join(runDir, "resources.json"), resources)
				}
				if err == nil {
					worker = exec.Command(filepath.Join(os.Getenv("EXT_PAYLOAD_PATH"), "bin", "nfqws2"), workerArgs(c, resources, filepath.Join(runDir, "worker"), uid)...)
					worker.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LUA_PATH=", "LUA_CPATH="}
					log := &boundedLog{path: filepath.Join(runDir, "worker.log")}
					worker.Stdout, worker.Stderr = log, log
					err = worker.Start()
					if err == nil {
						done = make(chan error, 1)
						started := worker
						go func() {
							done <- started.Wait()
						}()
						deadline := time.Now().Add(3 * time.Second)
						for !queueReady(resources.Queue) && time.Now().Before(deadline) {
							select {
							case <-done:
								worker = nil
								err = errors.New("packet processor exited")
							case <-time.After(50 * time.Millisecond):
							}
							if err != nil {
								break
							}
						}
						if err == nil && !queueReady(resources.Queue) {
							err = errors.New("packet processor did not open its queue")
						}
					} else {
						worker = nil
					}
					active = c
					lastRepair = time.Time{}
				}
			}
			if err == nil && worker != nil && time.Since(lastRepair) > 30*time.Second {
				for _, rule := range resources.Rules {
					if err = applyRule(rule, true); err != nil {
						break
					}
				}
				lastRepair = time.Now()
			}
			state = "RUNNING"
			if err != nil || worker == nil {
				terminate()
				state = "ERROR"
			}
		}

		publish(state)
		select {
		case <-stop:
			return nil
		case <-tick.C:
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(1)
	}
	op := os.Args[1]
	if op == "version" {
		commit := revision
		if len(commit) > 7 {
			commit = commit[:7]
		}
		fmt.Printf("ocfusion-zapret2 %s (%s)\n", version, commit)
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
	switch op {
	case "validate", "reload":
		var incoming Config
		incoming, err = parseConfig(envelope.Config)
		if err == nil && op == "validate" && len(envelope.Config) > 0 && string(envelope.Config) != "null" {
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
	case "settings":
		result, err = loadConfig()
	case "presets":
		result = presetCatalog()
	case "scan-start":
		result, err = requestScan(envelope.Config)
	case "scan-cancel":
		err = cancelRequestedScan(envelope.Config)
	case "remove":
		err = errors.Join(cleanup(), cleanupAt(scanResources(), "ocfusion:zapret2-scan"))
	case "diagnose":
		result = map[string]string{"version": version, "state": "UNAVAILABLE"}
		data, readErr := os.ReadFile(os.Getenv("EXT_PUBLIC_STATUS_PATH"))
		if readErr == nil && len(data) < 8192 {
			var status map[string]any
			if json.Unmarshal(data, &status) == nil {
				result = status
			}
		}
	default:
		err = errors.New("unsupported operation")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "zapret2:", err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(result)
}
