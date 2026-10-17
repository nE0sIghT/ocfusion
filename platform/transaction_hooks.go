// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

func transactionDirectory(backup, id string) (string, error) {
	if !idRE.MatchString(id) {
		return "", errors.New("invalid backup module ID")
	}

	return filepath.Abs(filepath.Join(backup, "hooks", id))
}

func backupTransactionHook(item Installed, backup string, state *StateBackup) error {
	if !item.Manifest.TransactionHooks {
		return nil
	}

	directory, err := transactionDirectory(backup, item.Manifest.ID)
	if err != nil {
		return err
	}

	data := filepath.Join(directory, "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		return err
	}

	payload := filepath.Join(directory, "payload")
	if err := exec.Command("/bin/cp", "-Rp", item.Path, payload).Run(); err != nil {
		return err
	}

	item.Path = payload
	if err := verify(item); err != nil {
		return err
	}

	if _, err := invokeWithEnvironment(item, payload, "backup", nil, []string{"EXT_BACKUP_PATH=" + data}); err != nil {
		return err
	}

	state.Hooks = append(state.Hooks, item)
	return nil
}

func backupHookItem(item Installed, backup string) (Installed, string, error) {
	directory, err := transactionDirectory(backup, item.Manifest.ID)
	if err != nil {
		return item, "", err
	}

	if !item.Manifest.TransactionHooks || !safePath(item.Manifest.Entry) || len(item.Hashes) == 0 {
		return item, "", errors.New("invalid transaction hook backup")
	}

	for path := range item.Hashes {
		if !safePath(path) {
			return item, "", errors.New("invalid backup payload path")
		}
	}

	if _, exists := item.Hashes[item.Manifest.Entry]; !exists {
		return item, "", errors.New("missing backup entrypoint")
	}

	item.Path = filepath.Join(directory, "payload")
	if err := verify(item); err != nil {
		return item, "", err
	}

	return item, filepath.Join(directory, "data"), nil
}

func restoreTransactionHook(item Installed, backup string) error {
	item, data, err := backupHookItem(item, backup)
	if err != nil {
		return err
	}

	_, err = invokeWithEnvironment(item, item.Path, "restore", nil, []string{"EXT_BACKUP_PATH=" + data})
	return err
}

func pendingModuleTransaction(id string) error {
	if _, err := os.Stat(installMarker); os.IsNotExist(err) {
		path := filepath.Join(stateRoot, "ocfusion-update-backup", "state.json")
		data, readErr := os.ReadFile(path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}

		if readErr == nil {
			var state StateBackup
			if json.Unmarshal(data, &state) != nil || (state.Schema == 1 && !state.Restored) {
				return errors.New("distribution installation requires rollback; rerun the installer first")
			}
		}
	} else if err != nil {
		return err
	}

	paths, err := filepath.Glob(filepath.Join(base, "module-backups", id+"-*", "state.json"))
	if err != nil {
		return err
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var state StateBackup
		if json.Unmarshal(data, &state) != nil || state.Module != id || !state.Restored {
			return errors.New("module installation requires rollback; restore the retained backup first")
		}
	}

	return nil
}
