// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQueueStatusUsesOwnedKernelQueue(t *testing.T) {
	t.Setenv("EXT_RUN_DIR", t.TempDir())
	oldDir := runDir
	oldRead := readNFQueues
	runDir = t.TempDir()
	t.Cleanup(func() { runDir = oldDir; readNFQueues = oldRead })
	if err := os.WriteFile(filepath.Join(runDir, "resources.json"), []byte(`{"queue":201}`), 0600); err != nil {
		t.Fatal(err)
	}
	readNFQueues = func() ([]byte, error) {
		return []byte("200 100 99 2 65535 7 8 900 1\n201 101 3 2 65535 4 5 600 1\n"), nil
	}
	status := queueStatus().(map[string]any)
	if status["pending"] != uint64(3) || status["kernel_dropped"] != uint64(4) || status["userspace_dropped"] != uint64(5) || status["sequence"] != uint64(600) {
		t.Fatal(status)
	}
	readNFQueues = func() ([]byte, error) {
		return []byte("201 101 broken 2 65535 4 5 600 1"), nil
	}
	if queueStatus() != nil {
		t.Fatal("malformed queue snapshot accepted")
	}
}
