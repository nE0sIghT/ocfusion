// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Worker struct {
	cmd   *exec.Cmd
	done  chan error
	next  time.Time
	state string
}

func invoke(item Installed, payload, op string, config json.RawMessage) (json.RawMessage, error) {
	return invokeWithEnvironment(item, payload, op, config, nil)
}

func invokeWithEnvironment(item Installed, payload, op string, config json.RawMessage, environment []string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, filepath.Join(payload, item.Manifest.Entry), op)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second

	input, _ := json.Marshal(map[string]any{
		"api":          1,
		"operation":    op,
		"config":       config,
		"config_path":  filepath.Join(base, "modules", item.Manifest.ID, "config.json"),
		"package_path": item.Path,
	})
	cmd.Stdin = strings.NewReader(string(input))
	cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "EXT_MODULE_ID="+item.Manifest.ID, "EXT_PACKAGE_PATH="+item.Path, "EXT_PAYLOAD_PATH="+payload)
	cmd.Env = append(cmd.Env, environment...)

	out := &limitedBuffer{limit: 32768}
	cmd.Stdout = out
	cmd.Stderr = io.Discard

	if e := cmd.Run(); e != nil {
		return nil, errors.New("module operation failed")
	}

	if len(out.b) == 0 {
		return json.RawMessage(`{}`), nil
	}

	if !json.Valid(out.b) {
		return nil, errors.New("module returned invalid JSON")
	}

	return out.b, nil
}

type limitedBuffer struct {
	b     []byte
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.b) < b.limit {
		keep := b.limit - len(b.b)
		if len(p) < keep {
			keep = len(p)
		}

		b.b = append(b.b, p[:keep]...)
	}

	return n, nil
}

func (m *Manager) stop(id string) {
	if w := m.workers[id]; w != nil && w.cmd != nil {
		syscall.Kill(-w.cmd.Process.Pid, syscall.SIGTERM)

		select {
		case <-w.done:
		case <-time.After(5 * time.Second):
			syscall.Kill(-w.cmd.Process.Pid, syscall.SIGKILL)
			<-w.done
		}

		w.cmd = nil
		w.state = "STOPPED"
	}
}

func (m *Manager) start(id string) error {
	if m.transaction != id {
		if err := pendingModuleTransaction(id); err != nil {
			return err
		}
	}

	if _, err := dependencyOrder(m.registry.Modules); err != nil {
		return err
	}

	item := m.registry.Modules[id]
	if w := m.workers[id]; w != nil && w.cmd != nil {
		return nil
	}

	for _, dependency := range item.Manifest.Dependencies {
		if err := m.start(dependency.ID); err != nil {
			return err
		}
	}

	payload, e := snapshot(item)
	if e != nil {
		return e
	}

	if _, e = invoke(item, payload, "prepare", nil); e != nil {
		return e
	}

	cmd := exec.Command(filepath.Join(payload, item.Manifest.Entry), "run")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	statusPath := filepath.Join(ram, "modules", id, "public-status.json")
	os.Remove(statusPath)

	cmd.Env = append(os.Environ(),
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"EXT_MODULE_ID="+id,
		"EXT_PACKAGE_PATH="+item.Path,
		"EXT_PAYLOAD_PATH="+payload,
		"EXT_CONFIG_PATH="+filepath.Join(base, "modules", id, "config.json"),
		"EXT_PUBLIC_STATUS_PATH="+statusPath,
	)
	cmd.Stdin = strings.NewReader(`{"api":1,"operation":"run"}`)

	logdir := filepath.Join(ram, "modules", id)
	os.MkdirAll(logdir, 0700)

	log := &limitedBuffer{limit: 32768}
	cmd.Stdout = log
	cmd.Stderr = io.Discard

	if e = cmd.Start(); e != nil {
		return e
	}

	w := &Worker{cmd: cmd, done: make(chan error, 1), state: "RUNNING"}
	m.workers[id] = w
	go func() {
		w.done <- cmd.Wait()
	}()

	select {
	case <-w.done:
		w.cmd = nil
		w.state = "FAILED"

		return errors.New("module exited during startup")
	case <-time.After(300 * time.Millisecond):
	}

	if e = publishUI(item, payload); e != nil {
		m.stop(id)

		return e
	}

	return nil
}

func (m *Manager) stopDependents(id string) {
	for other, item := range m.registry.Modules {
		for _, dependency := range item.Manifest.Dependencies {
			if dependency.ID == id {
				m.stopDependents(other)
				m.stop(other)
			}
		}
	}
}
