// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStatusAfterDaemonExit(t *testing.T) {
	previous := runDir
	runDir = t.TempDir()
	defer func() {
		runDir = previous
	}()

	original := Status{Version: "test", State: "STOPPING", Index: 7, Networks: []Network{{Bridge: "br55"}}}
	if err := saveJSON(filepath.Join(runDir, "status.json"), original); err != nil {
		t.Fatal(err)
	}

	lock, err := os.OpenFile(filepath.Join(runDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}

	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	check := func(want string) {
		t.Helper()

		raw, err := readStatus()
		if err != nil {
			t.Fatal(err)
		}

		var st Status
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}

		if st.State != want || st.Index != 7 || len(st.Networks) != 1 {
			t.Fatalf("unexpected status: %+v", st)
		}
	}
	check("STOPPING")
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}

	check("STOPPED")
	if err := os.Remove(filepath.Join(runDir, "daemon.lock")); err != nil {
		t.Fatal(err)
	}

	check("STOPPED")

	original.State = "UP"
	if err := saveJSON(filepath.Join(runDir, "status.json"), original); err != nil {
		t.Fatal(err)
	}

	check("STOPPED")
}
