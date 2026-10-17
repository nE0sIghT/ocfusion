// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
)

type Manager struct {
	transaction string
	mu          sync.Mutex
	stopping    bool
	stopped     bool
	registry    Registry
	workers     map[string]*Worker
}

func (m *Manager) handle(q Request) (result any, resultErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, exists := m.registry.Modules[q.ID]

	switch q.Op {
	case "register-set":
		return m.registerSet(q.Path)
	case "list":
		return m.registry, nil
	case "register":
		next, e := readPackage(q.Path)
		if e != nil {
			return nil, e
		}

		id := next.Manifest.ID
		if e := m.checkReplacement(next); e != nil {
			return nil, e
		}

		if e := m.resourceConflict(next); e != nil {
			return nil, e
		}

		if old, ok := m.registry.Modules[id]; ok {
			next.Enabled = old.Enabled
			m.stopDependents(id)
			m.stop(id)
		}

		delete(m.workers, id)

		m.registry.Modules[id] = next
		if e = m.save(); e != nil {
			return nil, e
		}

		return next.Manifest, nil
	case "install":
		var installCommitted *bool

		parent := packageRoot
		if e := os.MkdirAll(parent, 0700); e != nil {
			return nil, e
		}

		stage, e := os.MkdirTemp(parent, ".staging-")
		if e != nil {
			return nil, e
		}

		defer os.RemoveAll(stage)
		if e = unpack(q.Path, stage); e != nil {
			return nil, e
		}

		next, e := readPackage(stage)
		if e != nil {
			return nil, e
		}

		id := next.Manifest.ID
		if e := m.checkReplacement(next); e != nil {
			return nil, e
		}

		if e := m.resourceConflict(next); e != nil {
			return nil, e
		}

		if old, ok := m.registry.Modules[id]; ok && old.Manifest.Version == next.Manifest.Version {
			if reflect.DeepEqual(old.Hashes, next.Hashes) && verify(old) == nil {
				return old.Manifest, nil
			}

			return nil, errors.New("same version has different payload; use a new version")
		}

		payload, e := snapshot(next)
		if e != nil {
			return nil, e
		}

		if _, e = invoke(next, payload, "validate", nil); e != nil {
			return nil, e
		}

		for _, dependency := range next.Manifest.Dependencies {
			if e = m.start(dependency.ID); e != nil {
				return nil, e
			}
		}

		if next.Manifest.Install || next.Manifest.TransactionHooks {
			if err := pendingModuleTransaction(id); err != nil {
				return nil, err
			}

			backup, backupErr := backupModuleState(next)
			if backupErr != nil {
				return nil, backupErr
			}

			old, existed := m.registry.Modules[id]
			m.stopDependents(id)
			m.stop(id)
			committed := false
			m.transaction = id
			defer func() {
				defer func() { m.transaction = "" }()
				if !committed {
					m.stopDependents(id)
					m.stop(id)
					if err := restoreState(backup); err != nil {
						resultErr = errors.Join(resultErr, fmt.Errorf("module rollback failed; backup retained at %s: %w", backup, err))
						return
					}

					if existed && old.Enabled {
						m.registry.Modules[id] = old
						if err := m.start(id); err != nil {
							resultErr = errors.Join(resultErr, err)
						}
					}
				}

				os.RemoveAll(backup)
			}()

			installCommitted = &committed

			if next.Manifest.Install {
				if _, e = invoke(next, payload, "preflight", nil); e != nil {
					return nil, e
				}

				if _, e = invoke(next, payload, "install", nil); e != nil {
					return nil, e
				}
			}
		}

		if _, e = invoke(next, payload, "prepare", nil); e != nil {
			return nil, e
		}

		dest := filepath.Join(parent, id, "versions", next.Manifest.Version)
		if _, e = os.Stat(dest); e == nil {
			return nil, errors.New("version already installed")
		}

		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return nil, e
		}

		if e = os.Rename(stage, dest); e != nil {
			return nil, e
		}

		next.Path = dest
		old, ok := m.registry.Modules[id]
		if ok {
			next.Enabled = old.Enabled
			next.Previous = old.Path
			m.stopDependents(id)
			m.stop(id)
		}

		m.registry.Modules[id] = next
		if next.Enabled {
			if e = m.start(id); e != nil {
				if ok {
					m.registry.Modules[id] = old
					if installCommitted == nil {
						m.start(id)
					}
				} else {
					delete(m.registry.Modules, id)
				}

				os.RemoveAll(dest)

				return nil, e
			}
		}

		if e = m.save(); e != nil {
			m.stop(id)
			if ok {
				m.registry.Modules[id] = old
				if old.Enabled && installCommitted == nil {
					m.start(id)
				}
			} else {
				delete(m.registry.Modules, id)
			}

			return nil, e
		}

		if installCommitted != nil {
			*installCommitted = true
		}

		return next.Manifest, nil
	case "status":
		if !exists {
			return nil, errors.New("module not installed")
		}

		state := "STOPPED"
		if w := m.workers[q.ID]; w != nil {
			state = w.state
		}

		return map[string]any{"state": state, "enabled": item.Enabled}, nil
	case "enable", "disable":
		if !exists {
			return nil, errors.New("module not installed")
		}

		item.Enabled = q.Op == "enable"
		if e := m.checkReplacement(item); e != nil {
			return nil, e
		}
		m.registry.Modules[q.ID] = item
		if !item.Enabled {
			m.stop(q.ID)
		} else if e := m.start(q.ID); e != nil {
			return nil, e
		}

		return nil, m.save()
	case "config":
		if !exists {
			return nil, errors.New("module not installed")
		}

		if len(q.Config) > 16384 || !json.Valid(q.Config) {
			return nil, errors.New("invalid/oversized configuration")
		}

		payload, e := snapshot(item)
		if e != nil {
			return nil, e
		}

		if _, e = invoke(item, payload, "validate", q.Config); e != nil {
			return nil, e
		}

		configPath := filepath.Join(base, "modules", q.ID, "config.json")
		previous, oldErr := os.ReadFile(configPath)
		if oldErr != nil && !os.IsNotExist(oldErr) {
			return nil, oldErr
		}

		if e = atomicJSON(configPath, q.Config); e != nil {
			return nil, e
		}

		data, e := invoke(item, payload, "reload", q.Config)
		if e != nil {
			if oldErr == nil {
				os.WriteFile(configPath, previous, 0600)
				invoke(item, payload, "reload", json.RawMessage(previous))
			} else {
				os.Remove(configPath)
			}

			return nil, e
		}

		return data, nil
	case "unregister":
		if e := m.checkDependents(q.ID); e != nil {
			return nil, e
		}

		if !exists {
			return nil, errors.New("module not installed")
		}

		m.stop(q.ID)
		delete(m.registry.Modules, q.ID)
		os.Remove(filepath.Join(webRoot, "extensions-status", q.ID+".json"))

		return nil, m.save()
	case "remove":
		if e := m.checkDependents(q.ID); e != nil {
			return nil, e
		}

		if !exists {
			return nil, errors.New("module not installed")
		}

		m.stop(q.ID)

		payload, e := snapshot(item)
		if e != nil {
			return nil, e
		}

		result, e := invoke(item, payload, "remove", nil)
		if e != nil {
			return nil, e
		}

		removal, e := recordRemovalResult(result)
		if e != nil {
			return nil, e
		}

		delete(m.registry.Modules, q.ID)
		os.Remove(filepath.Join(webRoot, "extensions-status", q.ID+".json"))

		return removal, m.save()
	case "action":
		if !exists {
			return nil, errors.New("module not installed")
		}

		allowed := false

		for _, action := range item.Manifest.Actions {
			allowed = allowed || action == q.Action
		}

		if !allowed || !idRE.MatchString(q.Action) {
			return nil, errors.New("unsupported action")
		}

		if len(q.Config) > 16384 || len(q.Config) != 0 && !json.Valid(q.Config) {
			return nil, errors.New("invalid/oversized action data")
		}

		payload, err := snapshot(item)
		if err != nil {
			return nil, err
		}

		return invoke(item, payload, q.Action, q.Config)
	default:
		if !exists {
			return nil, errors.New("module not installed")
		}

		allowed := q.Op == "diagnose"

		for _, action := range item.Manifest.Actions {
			allowed = allowed || action == q.Op
		}

		if !allowed {
			return nil, errors.New("unsupported operation")
		}

		payload, e := snapshot(item)
		if e != nil {
			return nil, e
		}

		return invoke(item, payload, q.Op, q.Config)
	}
}
