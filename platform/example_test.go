// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentedHeartbeat(t *testing.T) {
	oldBase, oldRam, oldPackages, oldWeb := base, ram, packageRoot, webRoot
	defer func() { base, ram, packageRoot, webRoot = oldBase, oldRam, oldPackages, oldWeb }()

	base = t.TempDir()
	ram = t.TempDir()
	packageRoot = t.TempDir()
	webRoot = t.TempDir()
	m := &Manager{registry: Registry{Schema: 1, Modules: map[string]Installed{}}, workers: map[string]*Worker{}}
	defer m.stop("heartbeat")

	example := filepath.Join("..", "docs", "examples", "heartbeat")
	if _, e := m.handle(Request{Op: "install", Path: archive(t, example)}); e != nil {
		t.Fatal(e)
	}

	if m.workers["heartbeat"].cmd == nil {
		t.Fatal("example not running")
	}

	if _, e := m.handle(Request{Op: "diagnose", ID: "heartbeat"}); e != nil {
		t.Fatal(e)
	}

	if _, e := m.handle(Request{Op: "disable", ID: "heartbeat"}); e != nil {
		t.Fatal(e)
	}

	if _, e := m.handle(Request{Op: "enable", ID: "heartbeat"}); e != nil {
		t.Fatal(e)
	}

	if _, e := os.Stat(filepath.Join(webRoot, "extensions-modules", "heartbeat", "ui", "index.html")); e != nil {
		t.Fatal("example UI not published", e)
	}

	if _, e := m.handle(Request{Op: "remove", ID: "heartbeat"}); e != nil {
		t.Fatal(e)
	}
}
