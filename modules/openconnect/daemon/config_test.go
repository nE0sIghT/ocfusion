// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileValidation(t *testing.T) {
	p, e := parseProfile(validProfile())
	if e != nil || p.Protocol != "anyconnect" {
		t.Fatalf("default protocol: %+v %v", p, e)
	}

	for name, b := range map[string]string{
		"executable directive": `{"schema":1,"server":"https://x","password":"x","script":"/tmp/evil"}`,
		"embedded credentials": `{"schema":1,"server":"https://user:secret@x","password":"x"}`,
		"plaintext URL":        `{"schema":1,"server":"http://x","password":"x"}`,
		"interactive login":    `{"schema":1,"server":"https://x","username":"x"}`,
		"invalid DNS":          `{"schema":1,"server":"https://x","password":"x","dns":["1.2.3.4;reboot"]}`,
		"credential newline":   `{"schema":1,"server":"https://x","password":"x\nreboot"}`,
		"unsupported schema":   `{"schema":2,"server":"https://x","password":"x"}`,
		"trailing object":      string(validProfile()) + ` {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := parseProfile([]byte(b)); e == nil {
				t.Fatal("unsafe/unsupported profile accepted")
			}
		})
	}
}

func TestAtomicSecretPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	var p Profile
	json.Unmarshal(validProfile(), &p)
	if e := saveJSON(path, p); e != nil {
		t.Fatal(e)
	}

	st, e := os.Stat(path)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, e)
	}

	b, _ := os.ReadFile(path)
	if _, e = parseProfile(b); e != nil {
		t.Fatal(e)
	}
}

func validProfile() []byte {
	return []byte(`{"schema":1,"server":"https://vpn.example.org/group","username":"admin","password":"test-only","enabled":true}`)
}
