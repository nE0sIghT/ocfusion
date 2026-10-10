// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type StateBackup struct {
	Native     bool              `json:"native,omitempty"`
	Paths      map[string]bool   `json:"paths"`
	ClientList string            `json:"client_list"`
	Custom     map[string]string `json:"custom"`
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

	os.MkdirAll(backup, 0700)
	snapshot := StateBackup{Native: true, Paths: paths, ClientList: nvget("vpnc_clientlist"), Custom: map[string]string{}}
	for unit := 1; unit <= 5; unit++ {
		key := fmt.Sprintf("vpn_client%d_custom", unit)
		snapshot.Custom[key] = nvget(key)
	}

	for path := range paths {
		if path == base {
			if _, err := os.Stat(filepath.Join(base, ".legacy-migration")); err == nil {
				continue
			}
		}
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

	if transport, err := loadTransport(); err == nil && snapshot.Native {
		nvset(transport.Key, snapshot.Custom[transport.Key])
	}

	for path, existed := range snapshot.Paths {
		if !ownedStatePath(path) && path != base {
			return errors.New("invalid backup state path")
		}

		os.RemoveAll(path)
		if existed {
			source := filepath.Join(backup, filepath.Base(path))
			if err := exec.Command("/bin/cp", "-Rp", source, path).Run(); err != nil {
				return err
			}
		}
	}

	if snapshot.Native {
		if err := nvset("vpnc_clientlist", snapshot.ClientList); err != nil {
			return err
		}

		_, err = runner("nvram", "commit")
		return err
	}

	return nil
}

func removePlatformTransport() error {
	transport, err := loadTransport()
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	records := []string{}
	for _, record := range strings.Split(nvget("vpnc_clientlist"), "<") {
		fields := strings.Split(record, ">")
		if len(fields) > 6 && fields[1] == "OpenVPN" && fields[2] == strconv.Itoa(transport.Unit) && fields[6] == strconv.Itoa(transport.Index) {
			continue
		}

		records = append(records, record)
	}

	if err := nvset("vpnc_clientlist", strings.Join(records, "<")); err != nil {
		return err
	}

	if err := nvset(transport.Key, transport.OriginalCustom); err != nil {
		return err
	}

	_, err = runner("nvram", "commit")
	if err != nil {
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
	backup, err := os.MkdirTemp(ram, "install-state-")
	if err != nil {
		return "", err
	}

	snapshot := StateBackup{Paths: map[string]bool{}}
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

	if err := atomicJSON(filepath.Join(backup, "state.json"), snapshot); err != nil {
		os.RemoveAll(backup)
		return "", err
	}

	return backup, nil
}
