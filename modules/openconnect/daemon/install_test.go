// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRefreshPreservesInstallation(t *testing.T) {
	d := t.TempDir()

	for _, r := range bundleFiles {
		p := filepath.Join(d, r)
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, []byte(r), 0600)
	}

	old := Manifest{Schema: 1, Unit: 4, Index: 7, Table: 51998, Priority: 90, Firmware: "firmware", Bundle: "old"}
	got, e := refreshedManifest(old, d)
	if e != nil {
		t.Fatal(e)
	}

	if e = verifyBundle(got); e != nil {
		t.Fatalf("refreshed bundle rejected: %v", e)
	}

	if len(got.BundleSHA) != len(bundleFiles) || got.Bundle != d {
		t.Fatal(got)
	}

	got.Bundle = old.Bundle
	got.BundleSHA = old.BundleSHA
	if !reflect.DeepEqual(got, old) {
		t.Fatal("refresh changed persistent settings")
	}

	os.Remove(filepath.Join(d, "ui/index.html"))
	if _, e = refreshedManifest(old, d); e == nil {
		t.Fatal("accepted incomplete bundle")
	}
}

func TestAdditionalCADoesNotReplacePublicRoots(t *testing.T) {
	public := []byte("PUBLIC_ROOTS")
	got := mergedCABundle(public, "PROFILE_ROOTS")
	if string(got) != "PUBLIC_ROOTS\nPROFILE_ROOTS\n" {
		t.Fatal("public roots replaced")
	}

	if string(mergedCABundle(public, "")) != string(public) {
		t.Fatal("empty optional CA changed trust")
	}

	got[0] = 'X'
	if string(public) != "PUBLIC_ROOTS" {
		t.Fatal("source bundle mutated")
	}
}

func TestBundleRejectsUnknownAndModifiedAssets(t *testing.T) {
	d := t.TempDir()

	for _, name := range bundleFiles {
		p := filepath.Join(d, name)
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, []byte(name), 0600)
	}

	m, e := refreshedManifest(Manifest{}, d)
	if e != nil {
		t.Fatal(e)
	}

	m.BundleSHA["web/unknown.png"] = strings.Repeat("0", 64)
	if verifyBundle(m) == nil {
		t.Fatal("accepted unknown asset")
	}

	delete(m.BundleSHA, "web/unknown.png")
	os.WriteFile(filepath.Join(d, "ui/app.js"), []byte("modified"), 0600)
	if verifyBundle(m) == nil {
		t.Fatal("accepted modified icon")
	}
}

func TestArchitectureGuard(t *testing.T) {
	previous := runner
	t.Cleanup(func() {
		runner = previous
	})

	for _, architecture := range []string{"aarch64", "x86_64", "armv7l", ""} {
		t.Run(architecture, func(t *testing.T) {
			runner = func(name string, args ...string) (string, error) {
				if name != "uname" || !reflect.DeepEqual(args, []string{"-m"}) {
					t.Fatalf("unexpected architecture probe: %s %v", name, args)
				}

				return architecture, nil
			}
			if err := verifyArchitecture(); (err == nil) != (architecture == "aarch64") {
				t.Fatalf("architecture %q: %v", architecture, err)
			}
		})
	}
}

func TestUpgradeWithReplacementDrive(t *testing.T) {
	replacement := t.TempDir()
	for _, name := range bundleFiles {
		path := filepath.Join(replacement, name)
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(name), 0600)
	}

	oldDirectory := filepath.Join(t.TempDir(), "removed-bundle")
	old := Manifest{Bundle: oldDirectory, BundleSHA: map[string]string{"ui/index.html": "old-hash"}}
	if err := verifyUpgrade(old, replacement); err != nil {
		t.Fatalf("replacement drive rejected: %v", err)
	}

	os.MkdirAll(oldDirectory, 0700)
	if err := verifyUpgrade(old, replacement); err == nil {
		t.Fatal("incomplete existing bundle accepted")
	}

	os.Remove(filepath.Join(replacement, "bin/openconnect"))
	os.RemoveAll(oldDirectory)
	if err := verifyUpgrade(old, replacement); err == nil {
		t.Fatal("incomplete replacement accepted")
	}
}
