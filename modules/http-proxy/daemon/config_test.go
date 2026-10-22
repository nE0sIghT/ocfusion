// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProxyConfigRejectsInjectionAndIPv6(t *testing.T) {
	base, _ := parseConfig(nil)
	for _, value := range []string{"1.1.1.1\nadmin -p80", "::1", "0.0.0.0", "224.0.0.1", "resolver.example"} {
		c := base
		c.DNS = []string{value}
		data, _ := json.Marshal(c)
		if _, err := parseConfig(data); err == nil {
			t.Fatalf("accepted DNS %q", value)
		}
	}

	for _, value := range []string{"all", "0.0.0.0/0 allow *", "::/0", "192.0.2.1\nsocks"} {
		c := base
		c.Allowed = []string{value}
		data, _ := json.Marshal(c)
		if _, err := parseConfig(data); err == nil {
			t.Fatalf("accepted ACL %q", value)
		}
	}
}

func TestProxyUsesOnlyConfiguredResolverAndLAN(t *testing.T) {
	c, _ := parseConfig(nil)
	if c.Enabled {
		t.Fatal("proxy enabled on first installation")
	}

	r := Registration{Address: "192.0.2.1", CIDR: "192.0.2.0/24"}
	config := proxyConfig(c, r)
	for _, required := range []string{"nserver 1.1.1.1\n", "nserver 9.9.9.9\n", "auth iponly\n", "allow * 192.0.2.0/24\ndeny *\n", "proxy -4 -p3128 -i192.0.2.1\n"} {
		if !strings.Contains(config, required) {
			t.Fatalf("missing %q in %s", required, config)
		}
	}

	for _, unsafe := range []string{"daemon", "admin ", "socks ", "-i0.0.0.0", "auth none"} {
		if strings.Contains(config, unsafe) {
			t.Fatal("unsafe configuration", config)
		}
	}
}

func TestRouterDNSCannotBypassProxyTunnel(t *testing.T) {
	c, _ := parseConfig(nil)
	r := Registration{UID: 60000, Address: "192.0.2.1", CIDR: "192.0.2.0/24", Local: []string{"192.0.2.1", "198.51.100.2"}}
	if !validRegistration(c, r) {
		t.Fatal("valid lease rejected")
	}

	for _, address := range r.Local {
		c.DNS = []string{address}
		if validRegistration(c, r) {
			t.Fatal("accepted router DNS", address)
		}
	}

	config := proxyConfig(c, r)
	if !strings.Contains(config, "deny * * 0.0.0.0/8,127.0.0.0/8") || !strings.Contains(config, "198.51.100.2") {
		t.Fatal("local destinations not blocked", config)
	}
}
