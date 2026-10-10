// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	old := Manifest{Schema: 1, Unit: 4, Index: 7, Table: 51998, Priority: 90, Firmware: "firmware", Bundle: "old", OriginalCustom: "custom", OriginalEAS: "eas"}
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

func TestLegacyManifestRemovesObsoleteGates(t *testing.T) {
	var m Manifest
	if err := json.Unmarshal([]byte(`{"schema":1,"unit":4,"vpnc_idx":7,"table":51998,"priority":90,"boot_policy_verified":false,"autoconnect":false}`), &m); err != nil {
		t.Fatal(err)
	}

	if m.Unit != 4 || m.Index != 7 {
		t.Fatal("legacy installation lost")
	}

	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(b), "boot_policy") || strings.Contains(string(b), "autoconnect") {
		t.Fatal("obsolete gates retained")
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

func TestLegacyBundlePathsRemainVerifiable(t *testing.T) {
	directory := t.TempDir()
	manifest := Manifest{Bundle: directory, BundleSHA: map[string]string{}}

	for _, relative := range legacyBundleFiles {
		path := filepath.Join(directory, relative)
		os.MkdirAll(filepath.Dir(path), 0700)
		data := []byte(relative)
		os.WriteFile(path, data, 0600)
		digest := sha256.Sum256(data)
		manifest.BundleSHA[relative] = hex.EncodeToString(digest[:])
	}

	if err := verifyBundle(manifest); err != nil {
		t.Fatal(err)
	}

	for _, relative := range []string{"bin/openconnect", "ca-bundle.crt", "web/fusion.js"} {
		if bundleRelative(manifest, relative) != relative {
			t.Fatalf("legacy path changed: %s", relative)
		}

		current := "modules/openconnect/" + relative
		if relative == "web/fusion.js" {
			current = "modules/openconnect/ui/fusion.js"
		}

		manifest.BundleSHA[current] = "current"
		if bundleRelative(manifest, relative) != current {
			t.Fatalf("nested package path not selected: %s", relative)
		}
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
	old := Manifest{Bundle: oldDirectory, BundleSHA: map[string]string{"web/logo.png": "old-hash"}}
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
