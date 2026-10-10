// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
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
	if firmware() != m.Firmware {
		return errors.New("firmware changed: rerun doctor and revalidate before activation")
	}

	for _, r := range strings.Split(nvget("vpnc_clientlist"), "<") {
		f := strings.Split(r, ">")
		if len(f) > 6 && f[6] == strconv.Itoa(m.Index) {
			if (f[0] != "OpenConnect (SSH)" && f[0] != fusionName) || f[1] != "OpenVPN" || f[2] != strconv.Itoa(m.Unit) {
				return errors.New("reserved Fusion profile changed")
			}

			if f[5] != "0" {
				return errors.New("Fusion dummy must remain OFF; OpenConnect is controlled by ocfusion")
			}

			return nil
		}
	}

	return errors.New("reserved Fusion profile missing")
}

func networks(m Manifest) ([]Network, error) {
	return discover(nvget("sdn_rl"), nvget("subnet_rl"), m.Index)
}

// Stock SDN serializes kill_switch but the inspected JS exposes no obvious
// editor for it. Set only that field on networks explicitly assigned to us.
func protectiveSDN(raw string, idx int) (string, []string, error) {
	records := strings.Split(raw, "<")
	var changed []string

	for i, r := range records {
		f := strings.Split(r, ">")
		if len(f) < 16 || f[2] != "1" || f[6] != strconv.Itoa(idx) {
			continue
		}

		n, e := strconv.Atoi(f[0])
		if e != nil || n < 1 || n > 64 {
			return "", nil, errors.New("invalid SDN index")
		}

		if f[14] != "1" {
			f[14] = "1"
			records[i] = strings.Join(f, ">")
			changed = append(changed, f[0])
		}
	}

	return strings.Join(records, "<"), changed, nil
}

func ensureNativePolicy(m Manifest) error {
	raw := nvget("sdn_rl")
	updated, changed, e := protectiveSDN(raw, m.Index)
	if e != nil || len(changed) == 0 {
		return e
	}

	if _, e = discover(updated, nvget("subnet_rl"), m.Index); e != nil {
		return e
	}

	if nvget("sdn_rl") != raw {
		return errors.New("SDN web configuration still changing")
	}

	if e = nvset("sdn_rl", updated); e != nil {
		return e
	}

	if _, e = runner("nvram", "commit"); e != nil {
		return e
	}

	for _, idx := range changed {
		if _, e = runner("service", "restart_sdn "+idx); e != nil {
			return e
		}
	}

	return nil
}
