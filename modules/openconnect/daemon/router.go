// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var runner = realRun

func realRun(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, name, args...)
	b, e := c.CombinedOutput()
	if e != nil {
		return string(b), fmt.Errorf("%s failed: %w", name, e)
	}

	return strings.TrimSpace(string(b)), nil
}

func nvget(k string) string {
	s, _ := runner("nvram", "get", k)

	return s
}

func nvset(k, v string) error {
	_, e := runner("nvram", "set", k+"="+v)

	return e
}

func firmware() string {
	return nvget("firmver") + "_" + nvget("buildno") + "_" + nvget("extendno")
}

func profileKey(m Manifest) string {
	return fmt.Sprintf("vpn_client%d_custom", m.Unit)
}

func verifyArchitecture() error {
	architecture, err := runner("uname", "-m")
	if err != nil {
		return fmt.Errorf("detect architecture: %w", err)
	}

	if architecture != "aarch64" {
		return fmt.Errorf("AArch64 required; detected %s", architecture)
	}

	return nil
}

func verifyIdentity(m Manifest) error {
	response, err := vpnCall(vpnRequest{Operation: "state"})
	if err == nil && (m.Index != response.Manifest.Index || m.Unit != response.Manifest.Unit) {
		return errors.New("VPN connection identity changed")
	}

	return err
}

func verifyBundle(m Manifest) error {
	for rel, want := range m.BundleSHA {
		allowed := false

		for _, name := range append(append(append([]string{}, bundleFiles...), legacyBundleFiles...), previousBundleFiles...) {
			if rel == name {
				allowed = true
				break
			}
		}

		if !allowed {
			return errors.New("invalid bundle hash entry")
		}

		b, e := os.ReadFile(filepath.Join(m.Bundle, rel))
		if e != nil {
			return fmt.Errorf("bundle unavailable: %s", rel)
		}

		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != want {
			return fmt.Errorf("bundle integrity failed: %s", rel)
		}
	}

	return nil
}
