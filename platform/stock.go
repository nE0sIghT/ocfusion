// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var runner = realRun

func realRun(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, name, args...)
	b, e := c.CombinedOutput()
	if e != nil {
		return string(b), fmt.Errorf("%s failed: %w", name, e)
	}

	return strings.TrimSpace(string(b)), nil
}
func nvget(k string) string {
	s, _ := runner("nvram", "get", k)

	return s
}
func nvset(k, v string) error {
	_, e := runner("nvram", "set", k+"="+v)

	return e
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	f, e := os.CreateTemp(filepath.Dir(path), ".ocf-")
	if e != nil {
		return e
	}

	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}

	if e == nil {
		e = f.Sync()
	}

	ce := f.Close()
	if e == nil {
		e = ce
	}

	if e != nil {
		return e
	}

	return os.Rename(name, path)
}
func saveJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}

	return atomicWrite(path, append(b, '\n'), 0600)
}
