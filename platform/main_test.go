// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, dir, version, prepare string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "bin"), 0700)

	b, _ := json.Marshal(Module{Schema: 1, ID: "example", Name: "Example", Version: version, API: 1, Entry: "bin/module"})
	os.WriteFile(filepath.Join(dir, "module.json"), b, 0600)

	script := "#!/bin/sh\ncase \"$1\" in\nvalidate|reload|diagnose|remove) echo '{}' ;;\nprepare) " + prepare + " ;;\nrun) trap 'exit 0' TERM; while :; do sleep 0.1; done ;;\nesac\n"
	os.WriteFile(filepath.Join(dir, "bin/module"), []byte(script), 0700)
}

func archive(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "module.tar.gz")
	f, _ := os.Create(path)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}

		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		tw.WriteHeader(&tar.Header{Name: rel, Mode: 0700, Size: int64(len(b))})
		tw.Write(b)

		return nil
	})
	tw.Close()
	gz.Close()
	f.Close()

	return path
}

func TestModuleLifecycleAndFailedUpdate(t *testing.T) {
	oldBase, oldRam, oldPackages, oldWeb := base, ram, packageRoot, webRoot
	defer func() { base, ram, packageRoot, webRoot = oldBase, oldRam, oldPackages, oldWeb }()

	base = t.TempDir()
	ram = t.TempDir()
	packageRoot = t.TempDir()
	webRoot = t.TempDir()
	m := &Manager{registry: Registry{Schema: 1, Modules: map[string]Installed{}}, workers: map[string]*Worker{}}
	defer m.stop("example")

	one := t.TempDir()
	fixture(t, one, "1.0", "echo '{}'")
	if _, e := m.handle(Request{Op: "install", Path: archive(t, one)}); e != nil {
		t.Fatal(e)
	}

	if m.workers["example"].cmd == nil {
		t.Fatal("backend not running")
	}

	pid := m.workers["example"].cmd.Process.Pid
	if _, e := m.handle(Request{Op: "install", Path: archive(t, one)}); e != nil {
		t.Fatal("repeat install", e)
	}

	if m.workers["example"].cmd.Process.Pid != pid {
		t.Fatal("repeat install restarted module")
	}

	two := t.TempDir()
	fixture(t, two, "2.0", "exit 1")
	if _, e := m.handle(Request{Op: "install", Path: archive(t, two)}); e == nil {
		t.Fatal("failed prepare accepted")
	}

	if m.registry.Modules["example"].Manifest.Version != "1.0" || m.workers["example"].cmd.Process.Pid != pid {
		t.Fatal("failed update disrupted old module")
	}

	if _, e := m.handle(Request{Op: "disable", ID: "example"}); e != nil {
		t.Fatal(e)
	}

	if m.workers["example"].cmd != nil {
		t.Fatal("disable left backend running")
	}

	if _, e := m.handle(Request{Op: "enable", ID: "example"}); e != nil {
		t.Fatal(e)
	}

	if _, e := m.handle(Request{Op: "config", ID: "example", Config: json.RawMessage(`{"test":true}`)}); e != nil {
		t.Fatal(e)
	}

	if _, e := m.handle(Request{Op: "anything", ID: "example"}); e == nil {
		t.Fatal("arbitrary operation allowed")
	}

	if _, e := m.handle(Request{Op: "remove", ID: "example"}); e != nil {
		t.Fatal(e)
	}

	if len(m.registry.Modules) != 0 {
		t.Fatal("registry not cleaned")
	}
}

func TestPackageTraversalAndIntegrity(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "bin/../../escape"} {
		if safePath(name) {
			t.Fatal("accepted", name)
		}
	}

	path := filepath.Join(t.TempDir(), "bad.tar.gz")
	f, _ := os.Create(path)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0700, Size: 1})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	f.Close()
	if unpack(path, t.TempDir()) == nil {
		t.Fatal("archive escaped staging")
	}

	dir := t.TempDir()
	fixture(t, dir, "1.0", "echo '{}'")

	item, e := readPackage(dir)
	if e != nil {
		t.Fatal(e)
	}

	os.WriteFile(filepath.Join(dir, "bin/module"), []byte("changed"), 0700)
	if verify(item) == nil {
		t.Fatal("payload mutation undetected")
	}

	os.Symlink("/etc/passwd", filepath.Join(dir, "link"))
	if _, e = readPackage(dir); e == nil {
		t.Fatal("symlink accepted")
	}

	m := Manager{registry: Registry{Modules: map[string]Installed{"other": {Manifest: Module{Resources: map[string]string{"tun": "ocvpn0"}}}}}}
	if m.resourceConflict(Installed{Manifest: Module{ID: "new", Resources: map[string]string{"tun": "ocvpn0"}}}) == nil {
		t.Fatal("resource collision accepted")
	}

	b := &limitedBuffer{limit: 10}
	b.Write([]byte(strings.Repeat("x", 100)))
	if len(b.b) != 10 {
		t.Fatal("unbounded diagnostics")
	}
}

func TestModuleActionDataAndUniformUI(t *testing.T) {
	oldBase, oldRam, oldWeb := base, ram, webRoot
	t.Cleanup(func() { base, ram, webRoot = oldBase, oldRam, oldWeb })

	base, ram, webRoot = t.TempDir(), t.TempDir(), t.TempDir()

	for _, id := range []string{"example", "openconnect"} {
		dir := t.TempDir()
		fixture(t, dir, "1.0", "echo '{}'")
		os.MkdirAll(filepath.Join(dir, "ui"), 0700)
		os.WriteFile(filepath.Join(dir, "ui/index.html"), []byte("module UI"), 0600)
		os.WriteFile(filepath.Join(dir, "bin/module"), []byte("#!/bin/sh\ncat\n"), 0700)

		manifest := Module{Schema: 1, ID: id, Name: id, Version: "1.0", API: 1, Entry: "bin/module", UI: "ui/index.html", Actions: []string{"settings"}}
		b, _ := json.Marshal(manifest)
		os.WriteFile(filepath.Join(dir, "module.json"), b, 0600)

		item, err := readPackage(dir)
		if err != nil {
			t.Fatal(err)
		}

		if err = publishUI(item, dir); err != nil {
			t.Fatal(err)
		}

		if _, err = os.Stat(filepath.Join(webRoot, "extensions-modules", id, "ui/index.html")); err != nil {
			t.Fatal(err)
		}

		m := &Manager{registry: Registry{Schema: 1, Modules: map[string]Installed{id: item}}, workers: map[string]*Worker{}}
		response, err := m.handle(Request{Op: "action", ID: id, Action: "settings", Config: json.RawMessage(`{"test":true}`)})
		if err != nil {
			t.Fatal(err)
		}

		var envelope struct {
			Config map[string]bool `json:"config"`
		}
		if err = json.Unmarshal(response.(json.RawMessage), &envelope); err != nil || !envelope.Config["test"] {
			t.Fatalf("action input missing: %s %v", response, err)
		}

		if _, err = m.handle(Request{Op: "action", ID: id, Action: "unregister"}); err == nil {
			t.Fatal("undeclared action accepted")
		}

		m.publish()

		metadata, _ := os.ReadFile(filepath.Join(webRoot, "extensions-modules.json"))
		if !strings.Contains(string(metadata), "/user/extensions-modules/"+id+"/ui/index.html") {
			t.Fatalf("special UI path: %s", metadata)
		}
	}
}

func TestPlatformRemovalPreservesAdditionalModules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := checkPlatformRemoval(path); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		body    string
		allowed bool
	}{
		{`{"modules":{"openconnect":{"path":"/tmp/opt/ocfusion"}}}`, true},
		{`{"modules":{"openconnect":{"path":"/tmp/opt/ocfusion/modules/openconnect"},"heartbeat":{"path":"/tmp/opt/ocfusion-modules/heartbeat/versions/1"}}}`, false},
		{`invalid`, false},
	} {
		os.WriteFile(path, []byte(tc.body), 0600)
		if err := checkPlatformRemoval(path); (err == nil) != tc.allowed {
			t.Fatalf("%s: %v", tc.body, err)
		}
	}
}

func TestPackageDoesNotRequireExactRouterModel(t *testing.T) {
	directory := t.TempDir()
	fixture(t, directory, "1.0", "echo '{}'")
	path := filepath.Join(directory, "module.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}

	manifest["requires"] = map[string]any{
		"architecture": "aarch64",
		"models":       []string{"A different router"},
		"platform_api": 1,
	}
	data, _ = json.Marshal(manifest)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := readPackage(directory); err != nil {
		t.Fatal("router model blocked compatible module", err)
	}
}
