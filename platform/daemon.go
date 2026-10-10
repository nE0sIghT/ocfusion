// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func daemon() error {
	os.MkdirAll(ram, 0700)

	lock, e := os.OpenFile(ram+"/lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}

	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return e
	}

	m := &Manager{registry: Registry{Schema: 1, Modules: map[string]Installed{}}, workers: map[string]*Worker{}}
	b, e := os.ReadFile(base + "/registry.json")
	if e == nil {
		if e = json.Unmarshal(b, &m.registry); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}

	if m.registry.Schema != 1 || m.registry.Modules == nil {
		return errors.New("invalid registry")
	}

	for id, item := range m.registry.Modules {
		if !idRE.MatchString(id) || id != item.Manifest.ID || !safePath(item.Manifest.Entry) {
			return errors.New("invalid registered module")
		}

		for rel := range item.Hashes {
			if !safePath(rel) {
				return errors.New("invalid registered payload path")
			}
		}
	}

	if migrateRegistryPaths(&m.registry) {
		if e = m.save(); e != nil {
			return e
		}
	}

	if _, err := loadTransport(); err == nil {
		publishWeb()
	}

	mailboxDone := make(chan struct{})
	defer close(mailboxDone)
	go serveMailbox(mailboxDone)

	os.WriteFile(ram+"/pid", []byte(fmt.Sprint(os.Getpid())), 0600)
	defer os.Remove(ram + "/pid")
	os.Remove(socket)

	l, e := net.Listen("unix", socket)
	if e != nil {
		return e
	}

	defer l.Close()
	defer os.Remove(socket)
	os.Chmod(socket, 0600)
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}

			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(90 * time.Second))

				var q Request
				if e := json.NewDecoder(io.LimitReader(c, 65536)).Decode(&q); e != nil {
					return
				}

				data, e := m.handle(q)
				r := Response{OK: e == nil, Data: data}
				if e != nil {
					r.Error = e.Error()
				}

				json.NewEncoder(c).Encode(r)
			}()
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-sig:
			m.mu.Lock()

			order, _ := dependencyOrder(m.registry.Modules)
			for i := len(order) - 1; i >= 0; i-- {
				m.stop(order[i])
			}
			for id := range m.workers {
				m.stop(id)
			}

			m.mu.Unlock()

			return nil
		case <-tick.C:
			m.mu.Lock()

			for id, item := range m.registry.Modules {
				if item.Manifest.UI != "" || len(item.Manifest.StockPages) != 0 {
					asset := item.Manifest.UI
					if asset == "" {
						asset = item.Manifest.StockPages[0].Script
					}
					path := filepath.Join(webRoot, "extensions-modules", id, asset)
					if _, err := os.Stat(path); os.IsNotExist(err) {
						if payload, err := snapshot(item); err == nil {
							publishUI(item, payload)
						}
					}
				}

				w := m.workers[id]
				if w != nil && w.cmd != nil {
					select {
					case <-w.done:
						w.cmd = nil
						m.stopDependents(id)
						w.state = "RESTARTING"
						w.next = time.Now().Add(5 * time.Second)
					default:
					}
				}

				if item.Enabled && (w == nil || w.cmd == nil && !time.Now().Before(w.next)) {
					if e = m.start(id); e != nil {
						w = m.workers[id]
						if w == nil {
							w = &Worker{}
							m.workers[id] = w
						}

						w.state = "WAIT_STORAGE_OR_DEPENDENCY"
						w.next = time.Now().Add(15 * time.Second)
					}
				}
			}

			m.publish()
			m.mu.Unlock()
		}
	}
}

func startDaemon() error {
	if _, e := rpc(Request{Op: "list"}); e == nil {
		return nil
	}

	cmd := exec.Command(base+"/extd", "daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e := cmd.Start(); e != nil {
		return e
	}

	go cmd.Wait()

	for i := 0; i < 40; i++ {
		if _, e := rpc(Request{Op: "list"}); e == nil {
			return nil
		}

		time.Sleep(250 * time.Millisecond)
	}

	return errors.New("extension manager did not start")
}
