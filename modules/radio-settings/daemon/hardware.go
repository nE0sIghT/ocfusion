// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Value struct {
	Present bool   `json:"present"`
	Text    string `json:"text"`
}

type Radio struct {
	Interface string `json:"interface"`
	Band      string `json:"band"`
	Country   string `json:"country"`
	Current   string `json:"current"`
	Channels  string `json:"channels"`
	BaseMask  string `json:"base_mask"`
	UserMask  string `json:"user_mask"`
}

type Hardware interface {
	Read() (map[string]Value, error)
	Write(string, Value) error
	Commit() error
	Radios(map[string]Value) []Radio
	SupportsProfile(map[string]Value, Profile) bool
	BootID() (string, error)
}

type stockHardware struct{}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("command output too large")
	}

	return b.Buffer.Write(p)
}

func stockCommand(limit int, name string, args ...string) (string, error) {
	timeout := 2 * time.Second
	if name == "/bin/nvram" && len(args) == 1 && args[0] == "commit" {
		timeout = 8 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output := &boundedOutput{limit: limit}
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = output
	if err := command.Run(); err != nil {
		return "", errors.New("stock command failed")
	}

	return strings.TrimSpace(output.String()), nil
}

func managedKeys() []string {
	keys := []string{"location_code", "ui_location_code"}
	for unit := 0; unit < 16; unit++ {
		keys = append(keys, fmt.Sprintf("wl%d_country_abbrev_override", unit))
	}

	return keys
}

func diagnosticKeys() map[string]bool {
	keys := map[string]bool{}
	for _, key := range managedKeys() {
		keys[key] = true
	}

	for unit := 0; unit < 16; unit++ {
		for _, suffix := range []string{"ifname", "nband"} {
			keys[fmt.Sprintf("wl%d_%s", unit, suffix)] = true
		}
	}

	return keys
}

func parseNVRAM(raw string) map[string]Value {
	wanted := diagnosticKeys()
	values := map[string]Value{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && wanted[key] {
			values[key] = Value{Present: true, Text: value}
		}
	}

	return values
}

func (stockHardware) Read() (map[string]Value, error) {
	// Inspect presence as well as value. Retain only the fixed whitelist;
	// never save or expose the full NVRAM output.
	raw, err := stockCommand(2<<20, "/bin/nvram", "show")
	if err != nil {
		return nil, err
	}

	return parseNVRAM(raw), nil
}

func (stockHardware) Write(key string, value Value) error {
	allowed := false
	for _, candidate := range managedKeys() {
		allowed = allowed || candidate == key
	}

	if !allowed || strings.ContainsAny(value.Text, "\r\n\x00") {
		return errors.New("invalid setting")
	}

	if value.Present {
		_, err := stockCommand(1024, "/bin/nvram", "set", key+"="+value.Text)
		return err
	}

	_, err := stockCommand(1024, "/bin/nvram", "unset", key)
	return err
}

func (stockHardware) Commit() error {
	_, err := stockCommand(1024, "/bin/nvram", "commit")
	return err
}

var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,15}$`)

func radioUnits(values map[string]Value) []int {
	var units []int
	for unit := 0; unit < 16; unit++ {
		name := values[fmt.Sprintf("wl%d_ifname", unit)].Text
		if interfaceName.MatchString(name) {
			units = append(units, unit)
		}
	}

	return units
}

func wlRead(iface, query string) string {
	value, err := stockCommand(2048, "/usr/sbin/wl", "-i", iface, query)
	if err != nil || len(value) > 768 {
		return "UNAVAILABLE"
	}

	return value
}

func (stockHardware) Radios(values map[string]Value) []Radio {
	units := radioUnits(values)
	radios := make([]Radio, len(units))
	var workers sync.WaitGroup

	for index, unit := range units {
		workers.Add(1)
		go func() {
			defer workers.Done()
			prefix := fmt.Sprintf("wl%d_", unit)
			iface := values[prefix+"ifname"].Text
			band := map[string]string{"1": "5 GHz", "2": "2.4 GHz", "4": "6 GHz"}[values[prefix+"nband"].Text]
			radios[index] = Radio{
				Interface: iface,
				Band:      band,
				Country:   wlRead(iface, "country"),
				Current:   wlRead(iface, "chanspec"),
				Channels:  wlRead(iface, "channels"),
				BaseMask:  wlRead(iface, "dis_ch_grp_conf"),
				UserMask:  wlRead(iface, "dis_ch_grp_user"),
			}
		}()
	}

	workers.Wait()
	return radios
}

func (stockHardware) SupportsProfile(values map[string]Value, profile Profile) bool {
	units := radioUnits(values)
	if len(units) == 0 {
		return false
	}

	for _, unit := range units {
		iface := values[fmt.Sprintf("wl%d_ifname", unit)].Text
		output, err := stockCommand(4096, "/usr/sbin/wl", "-i", iface, "country_rev", profile.Country)
		if err != nil || !strings.Contains(" "+strings.Join(strings.Fields(output), " ")+" ", " "+profile.Revision+" ") {
			return false
		}
	}

	return true
}

func (stockHardware) BootID() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || len(strings.TrimSpace(string(data))) != 36 {
		return "", errors.New("boot identity unavailable")
	}

	return strings.TrimSpace(string(data)), nil
}
