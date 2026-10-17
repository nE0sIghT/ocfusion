// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type StateBackup struct {
	Module   string          `json:"module,omitempty"`
	Restored bool            `json:"restored,omitempty"`
	Schema   int             `json:"schema"`
	Paths    map[string]bool `json:"paths"`
	Hooks    []Installed     `json:"hooks,omitempty"`
}

var stateRoot = "/jffs"

func ownedStatePath(path string) bool {
	return filepath.Clean(path) == path && strings.HasPrefix(path, filepath.Join(stateRoot, "ocfusion")) && !strings.Contains(strings.TrimPrefix(path, stateRoot+"/"), "/")
}

func backupState(directory, backup string) error {
	packages, err := bundledPackages(directory)
	if err != nil {
		return err
	}

	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		return errors.New("installation backup already exists")
	}

	order, err := dependencyOrder(packages)
	if err != nil {
		return err
	}

	for _, id := range order {
		item := packages[id]
		if err := pendingModuleTransaction(id); err != nil {
			return err
		}

		if item.Manifest.Install {
			if _, err := invoke(item, item.Path, "preflight", nil); err != nil {
				return err
			}
		}
	}

	paths := map[string]bool{base: false}
	for _, item := range packages {
		for _, path := range item.Manifest.StatePaths {
			if !ownedStatePath(path) || path == base || path == backup {
				return errors.New("invalid module state path")
			}

			paths[path] = false
		}
	}

	if err := os.MkdirAll(backup, 0700); err != nil {
		return err
	}

	snapshot := StateBackup{Schema: 1, Paths: paths}

	for path := range paths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}

		destination := filepath.Join(backup, filepath.Base(path))
		if err := exec.Command("/bin/cp", "-Rp", path, destination).Run(); err != nil {
			return err
		}

		snapshot.Paths[path] = true
	}

	for _, id := range order {
		if err := backupTransactionHook(packages[id], backup, &snapshot); err != nil {
			return err
		}
	}

	return atomicJSON(filepath.Join(backup, "state.json"), snapshot)
}

func restoreState(backup string) error {
	var snapshot StateBackup
	data, err := os.ReadFile(filepath.Join(backup, "state.json"))
	if err != nil {
		return err
	}

	if err = json.Unmarshal(data, &snapshot); err != nil {
		return err
	}

	if snapshot.Schema == 0 {
		// Released installers saved their restoration tool with platform state.
		// Let that tool interpret its own private transaction format.
		tool := filepath.Join(backup, filepath.Base(base), "extd")
		if err := exec.Command(tool, "restore-state", backup).Run(); err != nil {
			return errors.New("previous installer could not restore its backup; backup retained")
		}

		return nil
	}

	if snapshot.Schema != 1 {
		return errors.New("unsupported installation backup")
	}

	for path := range snapshot.Paths {
		if !ownedStatePath(path) && path != base {
			return errors.New("invalid backup state path")
		}
	}

	for _, item := range snapshot.Hooks {
		if _, _, err := backupHookItem(item, backup); err != nil {
			return err
		}
	}

	for path, existed := range snapshot.Paths {
		if path == base {
			continue
		}

		if err := restoreBackupPath(backup, path, existed); err != nil {
			return err
		}
	}

	for index := len(snapshot.Hooks) - 1; index >= 0; index-- {
		if err := restoreTransactionHook(snapshot.Hooks[index], backup); err != nil {
			return err
		}
	}

	if existed, included := snapshot.Paths[base]; included {
		if err := restoreBackupPath(backup, base, existed); err != nil {
			return err
		}
	}

	snapshot.Restored = true
	return atomicJSON(filepath.Join(backup, "state.json"), snapshot)
}

func restoreBackupPath(backup, path string, existed bool) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}

	if existed {
		source := filepath.Join(backup, filepath.Base(path))
		return exec.Command("/bin/cp", "-Rp", source, path).Run()
	}

	return nil
}

func removePlatformTransport() error {
	if _, err := loadTransport(); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	return os.Rename(filepath.Join(base, "transport.json"), filepath.Join(base, "transport.removed.json"))
}

func pruneState(directory string) error {
	packages, err := bundledPackages(directory)
	if err != nil {
		return err
	}

	for _, item := range packages {
		for _, path := range item.Manifest.StatePaths {
			if !ownedStatePath(path) || path == base {
				return errors.New("invalid module state path")
			}

			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
	}

	return os.RemoveAll(base)
}

func backupModuleState(item Installed) (string, error) {
	directory := filepath.Join(base, "module-backups")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}

	backup, err := os.MkdirTemp(directory, item.Manifest.ID+"-")
	if err != nil {
		return "", err
	}

	snapshot := StateBackup{Schema: 1, Module: item.Manifest.ID, Paths: map[string]bool{}}
	for _, path := range item.Manifest.StatePaths {
		if !ownedStatePath(path) || path == base {
			os.RemoveAll(backup)
			return "", errors.New("invalid module state path")
		}

		snapshot.Paths[path] = false
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			os.RemoveAll(backup)
			return "", err
		}

		if err := exec.Command("/bin/cp", "-Rp", path, filepath.Join(backup, filepath.Base(path))).Run(); err != nil {
			os.RemoveAll(backup)
			return "", err
		}

		snapshot.Paths[path] = true
	}

	if err := backupTransactionHook(item, backup, &snapshot); err != nil {
		os.RemoveAll(backup)
		return "", err
	}

	if err := atomicJSON(filepath.Join(backup, "state.json"), snapshot); err != nil {
		os.RemoveAll(backup)
		return "", err
	}

	return backup, nil
}
