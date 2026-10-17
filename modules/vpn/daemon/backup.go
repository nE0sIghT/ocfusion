// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type NativeSetting struct {
	Present bool   `json:"present"`
	Value   string `json:"value"`
}

type NativeBackup struct {
	Schema int                      `json:"schema"`
	Values map[string]NativeSetting `json:"values"`
}

func nativeBackupKeys() []string {
	keys := []string{"vpnc_clientlist"}
	for unit := 1; unit <= 5; unit++ {
		keys = append(keys, fmt.Sprintf("vpn_client%d_custom", unit))
	}

	return keys
}

func nativeBackupPath(directory string) (string, error) {
	if !filepath.IsAbs(directory) {
		return "", errors.New("private backup directory required")
	}

	return filepath.Join(directory, "native.json"), nil
}

func backupNative(directory string) error {
	path, err := nativeBackupPath(directory)
	if err != nil {
		return err
	}

	output, err := runner("nvram", "show")
	if err != nil {
		return errors.New("cannot read native VPN settings")
	}

	state := NativeBackup{Schema: 1, Values: map[string]NativeSetting{}}
	for _, key := range nativeBackupKeys() {
		state.Values[key] = NativeSetting{}
	}

	for _, line := range strings.Split(output, "\n") {
		key, _, found := strings.Cut(line, "=")
		if _, owned := state.Values[key]; found && owned {
			state.Values[key] = NativeSetting{Present: true}
		}
	}

	for _, key := range nativeBackupKeys() {
		setting := state.Values[key]
		if !setting.Present {
			continue
		}

		// Read each value separately: custom OpenVPN fields may span lines.
		setting.Value, err = runner("nvram", "get", key)
		if err != nil {
			return errors.New("cannot read native VPN setting")
		}
		state.Values[key] = setting
	}

	return saveJSON(path, state)
}

func restoreNative(directory string) error {
	path, err := nativeBackupPath(directory)
	if err != nil {
		return err
	}

	file, err := os.Open(path)
	if err != nil {
		return errors.New("native VPN backup unavailable")
	}
	defer file.Close()

	var state NativeBackup
	decoder := json.NewDecoder(io.LimitReader(file, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || decoder.Decode(new(any)) != io.EOF || state.Schema != 1 || len(state.Values) != len(nativeBackupKeys()) {
		return errors.New("invalid native VPN backup")
	}

	for _, key := range nativeBackupKeys() {
		if _, exists := state.Values[key]; !exists {
			return errors.New("incomplete native VPN backup")
		}
	}

	for _, key := range nativeBackupKeys() {
		value := state.Values[key]
		if value.Present {
			err = nvset(key, value.Value)
		} else {
			_, err = runner("nvram", "unset", key)
		}

		if err != nil {
			return errors.New("native VPN settings restoration failed")
		}
	}

	if _, err := runner("nvram", "commit"); err != nil {
		return errors.New("native VPN settings commit failed")
	}

	return nil
}
