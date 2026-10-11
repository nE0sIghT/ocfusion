// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileActionsKeepCredentialsAndIsolation(t *testing.T) {
	oldRoot, oldClientRoot, oldRun, oldID := root, clientRoot, runDir, profileID
	clientRoot = t.TempDir()
	t.Cleanup(func() {
		root, clientRoot, runDir, profileID = oldRoot, oldClientRoot, oldRun, oldID
	})
	selectProfile("default")
	original := `{"schema":1,"server":"https://default.example/","username":"user","password":"secret-default","enabled":false}`
	os.WriteFile(filepath.Join(root, "profile.json"), []byte(original), 0600)
	other := filepath.Join(clientRoot, "profiles/office")
	os.MkdirAll(other, 0700)
	os.WriteFile(filepath.Join(other, "manifest.json"), []byte(`{"schema":1}`), 0600)
	office := `{"schema":1,"server":"https://office.example/","username":"user","password":"secret-office","enabled":true}`
	os.WriteFile(filepath.Join(other, "profile.json"), []byte(office), 0600)

	settings, err := profileAction("settings", strings.NewReader(`{"config":{"profile":"office"}}`))
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(settings)
	if strings.Contains(string(public), "secret-office") || !strings.Contains(string(public), "office.example") {
		t.Fatal("settings leaked or selected wrong profile", string(public))
	}
	if _, err := profileAction("disconnect", strings.NewReader(`{"config":{"profile":"office"}}`)); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(filepath.Join(clientRoot, "profile.json"))
	if string(saved) != original {
		t.Fatal("default credentials modified")
	}
	selected, _ := os.ReadFile(filepath.Join(other, "profile.json"))
	if !strings.Contains(string(selected), "secret-office") || !strings.Contains(string(selected), `"enabled": false`) {
		t.Fatal("selected profile credentials or enabled state lost")
	}
	if _, err := profileAction("settings", strings.NewReader(`{"config":{"profile":"../default"}}`)); err == nil {
		t.Fatal("path traversal accepted")
	}
	permissions, _ := os.Stat(filepath.Join(other, "profile.json"))
	if permissions.Mode().Perm() != 0600 {
		t.Fatal("private permissions changed")
	}
}
