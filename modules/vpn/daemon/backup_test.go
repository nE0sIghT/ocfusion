// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeBackupPreservesReleasedVPNState(t *testing.T) {
	oldRunner := runner
	t.Cleanup(func() { runner = oldRunner })
	original := map[string]string{
		"vpnc_clientlist":    "OCFusion (managed)>OpenVPN>4>>>0>7>>>0>0>Web",
		"vpn_client4_custom": "setenv OCFUSION_PROVIDER connection-7\nremote vpn.example.org\nproto udp",
		"vpn_client2_custom": "",
	}
	values := map[string]string{}
	for key, value := range original {
		values[key] = value
	}
	values["unrelated_password"] = "private and unrelated"
	failCommit := false
	failRead := false
	runner = func(tool string, args ...string) (string, error) {
		if tool != "nvram" {
			t.Fatal("unexpected tool", tool)
		}

		switch args[0] {
		case "show":
			if failRead {
				return "", errors.New("fixture read failure")
			}

			var output []string
			for key, value := range values {
				output = append(output, key+"="+value)
			}
			return strings.Join(output, "\n"), nil
		case "get":
			return values[args[1]], nil
		case "set":
			key, value, _ := strings.Cut(args[1], "=")
			values[key] = value
		case "unset":
			delete(values, args[1])
		case "commit":
			if failCommit {
				return "", errors.New("fixture commit failure")
			}
		default:
			t.Fatal("unexpected operation", args)
		}

		return "", nil
	}

	directory := t.TempDir()
	if err := backupNative(directory); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(directory, "native.json")
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if strings.Contains(string(data), "unrelated_password") || info.Mode().Perm() != 0600 {
		t.Fatal("backup leaked unrelated settings or permissions")
	}

	values["vpnc_clientlist"] = "changed"
	values["vpn_client1_custom"] = "new connection"
	values["vpn_client4_custom"] = "changed"
	failCommit = true
	if restoreNative(directory) == nil {
		t.Fatal("commit failure ignored")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatal("failed restoration lost backup")
	}

	failCommit = false
	for attempt := 0; attempt < 2; attempt++ {
		if err := restoreNative(directory); err != nil {
			t.Fatal(err)
		}

		got := map[string]string{}
		for key, value := range values {
			if key != "unrelated_password" {
				got[key] = value
			}
		}
		if !reflect.DeepEqual(got, original) || values["unrelated_password"] != "private and unrelated" {
			t.Fatal("released VPN state or unrelated values changed")
		}
	}

	failRead = true
	if backupNative(t.TempDir()) == nil {
		t.Fatal("read failure produced a backup")
	}
}

func TestInvalidNativeBackupMakesNoChanges(t *testing.T) {
	oldRunner := runner
	t.Cleanup(func() { runner = oldRunner })
	calls := 0
	runner = func(string, ...string) (string, error) {
		calls++
		return "", nil
	}

	for _, data := range []string{`{}`, `{"schema":1,"values":{"unknown":{"present":true}}}`, `{"schema":1,"values":{}} {}`} {
		directory := t.TempDir()
		os.WriteFile(filepath.Join(directory, "native.json"), []byte(data), 0600)
		if restoreNative(directory) == nil || calls != 0 {
			t.Fatal("invalid backup reached NVRAM")
		}
	}
}
