// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationPortScopes(t *testing.T) {
	c := defaultConfig()
	c.TCP, c.UDP = "off", "off"
	c.Applications = []ApplicationProfile{applicationDefaults("mtproto", "split")}
	if len(firewallPortGroups(c, "tcp")) != 0 {
		t.Fatal("application ports without destination IPs")
	}
	c.Networks = []string{"203.0.113.1/32"}
	if got := strings.Join(firewallPortGroups(c, "tcp"), ","); got != "*" {
		t.Fatal(got)
	}
	c.Applications[0].Ports = "5222,443,50000-65535,443"
	if err := validateApplications(&c); err != nil {
		t.Fatal(err)
	}
	if c.Applications[0].Ports != "443,5222,50000-65535" {
		t.Fatal(c.Applications)
	}
	c.Applications = append(c.Applications, c.Applications[0])
	if validateApplications(&c) == nil {
		t.Fatal("duplicate profile accepted")
	}
	for _, value := range []string{"0", "65536", "99-1", "* --new", "1:2"} {
		if _, err := normalizePortRanges(value); err == nil {
			t.Fatal(value)
		}
	}
}

func TestPinnedApplicationEngine(t *testing.T) {
	binary := os.Getenv("OCF_NFQWS2_TEST_BINARY")
	directory := os.Getenv("OCF_NFQWS2_TEST_LUA")
	if binary == "" || directory == "" {
		t.Skip("requires pinned native nfqws2")
	}
	worker := t.TempDir()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worker, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"networks.txt": "203.0.113.1/32\n", "domains.txt": "example.com\n"} {
		if err := os.WriteFile(filepath.Join(worker, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range applicationKinds() {
		for _, method := range kind.Methods {
			t.Run(kind.ID+"/"+method, func(t *testing.T) {
				c := defaultConfig()
				c.Networks = []string{"203.0.113.1/32"}
				c.Domains = []string{"example.com"}
				c.Applications = []ApplicationProfile{applicationDefaults(kind.ID, method)}
				args := workerArgs(c, Resources{Queue: 200, Mark: "0x40000000"}, worker, 62000)
				args = append(args[:2], args[3:]...)
				args = append(args, "--dry-run")
				if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, output)
				}
			})
		}
	}
}

func TestApplicationPresetCatalog(t *testing.T) {
	for _, preset := range presetCatalog() {
		c := defaultConfig()
		data, err := json.Marshal(preset.Settings)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatal(err)
		}
		if err := validateApplications(&c); err != nil {
			t.Fatalf("%s: %v", preset.ID, err)
		}
		if preset.Protocol == "combined" && (c.UDP == "off" || len(c.Applications) == 0) {
			t.Fatal(preset.ID)
		}
		if preset.Protocol == "combined" {
			if _, ok := preset.Settings["tcp_strategy"]; !ok {
				t.Fatal("combined preset lost TCP strategy")
			}
		}
	}
	data, err := json.Marshal(map[string]any{"presets": presetCatalog(), "lists": serviceCatalog(), "applications": applicationCatalog()})
	if err != nil || len(data) > 16384 {
		t.Fatalf("catalog size %d: %v", len(data), err)
	}
}
