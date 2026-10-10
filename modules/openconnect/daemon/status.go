// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// The daemon lock distinguishes live state from snapshots left after an exit.
// PID files alone can be stale or refer to a reused PID.
func readStatus() ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(runDir, "status.json"))
	if err != nil {
		return nil, err
	}

	lock, err := os.OpenFile(filepath.Join(runDir, "daemon.lock"), os.O_RDWR, 0)
	if err == nil {
		defer lock.Close()

		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return raw, nil
		}

		if err != nil {
			return nil, err
		}

		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}

	st.State, st.Message = "STOPPED", ""
	st.RequestData = nil

	return json.MarshalIndent(st, "", "  ")
}
