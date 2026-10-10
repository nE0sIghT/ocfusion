// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
)

func TestOversizedClientOutputKeepsDraining(t *testing.T) {
	count := 0
	last := ""
	readClientLines(strings.NewReader(strings.Repeat("route-data", 300000)+"\nScript '/secret/path' failed\n"), func(line string) {
		count++
		if len(line) > 8192 {
			t.Fatal("unbounded memory")
		}

		last = clientStage(line)
	})
	if count != 2 || last != "OpenConnect network script failed" {
		t.Fatalf("output parsing stopped: %d %s", count, last)
	}
}

func TestClientStagesDoNotExposeSecrets(t *testing.T) {
	got := clientStage("Connected to HTTPS on secret.example/?private with ciphersuite TLS")
	if got == "" || strings.Contains(got, "secret") || strings.Contains(got, "private") {
		t.Fatal(got)
	}

	if clientStage("Cookie: private-value") != "" {
		t.Fatal("reported auth data")
	}
}
