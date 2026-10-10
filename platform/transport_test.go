// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformPublishesAndAllocatesTransportWithoutModules(t *testing.T) {
	previousBase := base
	previousRAM := ram
	previousWeb := webRoot
	previousRunner := runner
	base = filepath.Join(t.TempDir(), "platform")
	ram = t.TempDir()
	webRoot = t.TempDir()
	t.Cleanup(func() {
		base = previousBase
		ram = previousRAM
		webRoot = previousWeb
		runner = previousRunner
	})

	values := map[string]string{}
	runner = func(name string, args ...string) (string, error) {
		if name == "nvram" && args[0] == "get" {
			return values[args[1]], nil
		}

		if name == "nvram" && args[0] == "set" {
			parts := strings.SplitN(args[1], "=", 2)
			values[parts[0]] = parts[1]
		}

		return "", nil
	}

	bundle := t.TempDir()
	for _, asset := range platformAssets {
		path := filepath.Join(bundle, asset)
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte(asset), 0644)
	}

	if err := preparePlatform(bundle); err != nil {
		t.Fatal(err)
	}

	transport, err := loadTransport()
	if err != nil || transport.Unit == 0 || transport.Index == 0 {
		t.Fatal("transport depends on a module", transport, err)
	}

	if err := verifyPlatformAssets(bundle); err != nil {
		t.Fatal(err)
	}

	os.Remove(filepath.Join(base, "web-overlay.enabled"))
	if err := publishWeb(); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{registry: Registry{Modules: map[string]Installed{}}, workers: map[string]*Worker{}}
	manager.publish()
	for _, filename := range []string{"extensions.html", "extensions-panel.js", "ocfusion.js", "extensions-modules.json"} {
		if _, err := os.Stat(filepath.Join(webRoot, filename)); err != nil {
			t.Fatal("shared web missing without VPN", filename, err)
		}
	}
}
