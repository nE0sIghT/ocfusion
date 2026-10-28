// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testModuleProcess(t *testing.T, script string) (*exec.Cmd, chan error, int) {
	t.Helper()
	directory := t.TempDir()
	pidfile := filepath.Join(directory, "child")
	cmd := exec.Command("/bin/sh", "-c", script, "module", pidfile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})

	done := make(chan error, 1)
	go waitModule(cmd, done)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidfile)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				return cmd, done, pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("module did not start child")
	return nil, nil, 0
}

func assertChildStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if os.IsNotExist(err) || strings.Contains(string(data), ") Z ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("module completion left live descendant", pid)
}

func TestModuleExitReapsDescendantsHoldingOutputOpen(t *testing.T) {
	_, done, child := testModuleProcess(t, `
sleep 60 &
printf '%s\n' "$!" > "$1"
exit 0
`)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("orphan output pipe blocked module completion")
	}
	assertChildStopped(t, child)
}

func TestModuleStopLetsEntrypointCleanUpBeforeChildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ordered")
	t.Setenv("OCF_TEST_STOP_MARKER", marker)
	cmd, done, child := testModuleProcess(t, `
sleep 60 &
child=$!
trap '
    if kill -0 "$child"; then
        printf ordered > "$OCF_TEST_STOP_MARKER"
    fi
    kill "$child"
    wait "$child"
    exit 0
' TERM
printf '%s\n' "$child" > "$1"
while :; do
    sleep 0.05
done
`)
	manager := &Manager{workers: map[string]*Worker{"fixture": {cmd: cmd, done: done}}}
	manager.stop("fixture")
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "ordered" {
		t.Fatal("children stopped before entrypoint cleanup", string(data), err)
	}
	assertChildStopped(t, child)
}
