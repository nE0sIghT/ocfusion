// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

type State struct {
	Revision uint64           `json:"revision"`
	Mode     string           `json:"mode"`
	Phase    string           `json:"phase"`
	Boot     string           `json:"boot"`
	Original map[string]Value `json:"original"`
	Target   map[string]Value `json:"target"`
	Previous map[string]Value `json:"previous,omitempty"`
	Baseline []Radio          `json:"baseline"`
}

type Settings struct {
	Revision uint64    `json:"revision"`
	Mode     string    `json:"mode"`
	Phase    string    `json:"phase"`
	CanApply bool      `json:"can_apply"`
	Profiles []Profile `json:"profiles"`
}

type Controller struct {
	Directory string
	Hardware  Hardware
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".radio-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}

	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}

	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()

	return directory.Sync()
}

func (c Controller) lock() (*os.File, error) {
	if err := os.MkdirAll(c.Directory, 0700); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(filepath.Join(c.Directory, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("settings busy")
	}

	return file, nil
}

func (c Controller) load() (State, error) {
	data, err := os.ReadFile(filepath.Join(c.Directory, "state.json"))
	if os.IsNotExist(err) {
		return State{Mode: "stock", Phase: "STOCK"}, nil
	}

	var state State
	if err != nil || len(data) > 32768 || json.Unmarshal(data, &state) != nil {
		return state, errors.New("saved settings unavailable")
	}

	if _, ok := findProfile(state.Mode); state.Mode != "stock" && !ok {
		return state, errors.New("invalid saved profile")
	}

	return state, nil
}

func (c Controller) save(state State) error {
	return writeJSON(filepath.Join(c.Directory, "state.json"), state)
}

func (c Controller) settings() (Settings, error) {
	state, err := c.load()
	if err != nil {
		return Settings{}, err
	}

	status, err := c.status()
	if err != nil {
		return Settings{}, err
	}
	phase := status["state"].(string)

	values, err := c.Hardware.Read()
	if err != nil {
		return Settings{}, err
	}

	return Settings{
		Revision: state.Revision,
		Mode:     state.Mode,
		Phase:    phase,
		CanApply: phase == "STOCK" || phase == "APPLIED",
		Profiles: availableProfiles(c.Hardware, values),
	}, nil
}

func selected(values map[string]Value, keys []string) map[string]Value {
	result := map[string]Value{}
	for _, key := range keys {
		result[key] = values[key]
	}

	return result
}

func originalKeys(original map[string]Value) []string {
	var keys []string
	for _, key := range managedKeys() {
		if _, ok := original[key]; ok {
			keys = append(keys, key)
		}
	}

	return keys
}

func (c Controller) apply(revision uint64, profileID string) (State, error) {
	state, err := c.load()
	if err != nil {
		return state, err
	}

	status, statusErr := c.status()
	if statusErr != nil {
		return state, statusErr
	}

	if revision != state.Revision || (status["state"] != "STOCK" && status["state"] != "APPLIED") {
		return state, errors.New("reload settings before applying")
	}

	profile, ok := findProfile(profileID)
	if !ok {
		return state, errors.New("unsupported native profile")
	}

	values, err := c.Hardware.Read()
	if err != nil || !c.Hardware.SupportsProfile(values, profile) {
		return state, errors.New("regional profile unavailable on one or more radios")
	}

	if state.Mode != "stock" && !reflect.DeepEqual(selected(values, originalKeys(state.Original)), state.Target) {
		return state, errors.New("native settings changed outside this module")
	}

	boot, err := c.Hardware.BootID()
	if err != nil {
		return state, err
	}

	keys := []string{"location_code", "ui_location_code"}
	target := map[string]Value{
		"location_code":    {Present: true, Text: profile.ID},
		"ui_location_code": {Present: true, Text: profile.ID},
	}
	for _, unit := range radioUnits(values) {
		key := fmt.Sprintf("wl%d_country_abbrev_override", unit)
		keys = append(keys, key)
		target[key] = Value{Present: true, Text: profile.Country}
	}

	original := state.Original
	baseline := state.Baseline
	if state.Mode == "stock" {
		original = selected(values, keys)
		baseline = c.Hardware.Radios(values)
	} else if !reflect.DeepEqual(originalKeys(original), keys) {
		return state, errors.New("radio layout changed; restore original settings")
	}

	for _, value := range original {
		if len(value.Text) > 32 {
			return state, errors.New("unexpected native setting")
		}
	}

	state = State{
		Revision: state.Revision + 1,
		Mode:     profile.ID,
		Phase:    "APPLYING",
		Boot:     boot,
		Original: original,
		Target:   target,
		Previous: selected(values, keys),
		Baseline: baseline,
	}
	if err := c.save(state); err != nil {
		return state, err
	}

	return c.finish(state, target)
}

func (c Controller) finish(state State, target map[string]Value) (State, error) {
	for _, key := range originalKeys(state.Original) {
		if err := c.Hardware.Write(key, target[key]); err != nil {
			state.Phase = "RECOVERY_REQUIRED"
			c.save(state)
			return state, errors.New("settings write failed; restore required")
		}
	}

	if err := c.Hardware.Commit(); err != nil {
		state.Phase = "RECOVERY_REQUIRED"
		c.save(state)
		return state, errors.New("settings commit failed; restore required")
	}

	state.Phase = "REBOOT_REQUIRED"
	if err := c.save(state); err != nil {
		return state, errors.New("settings saved; recovery record update failed")
	}

	return state, nil
}

func (c Controller) restore(revision uint64) (State, error) {
	state, err := c.load()
	if err != nil {
		return state, err
	}

	if revision != state.Revision {
		return state, errors.New("reload settings before restoring")
	}

	if state.Mode == "stock" && (state.Phase == "STOCK" || state.Phase == "REBOOT_REQUIRED") {
		return state, nil
	}

	values, err := c.Hardware.Read()
	if err != nil {
		return state, err
	}

	keys := originalKeys(state.Original)
	if len(keys) < 2 {
		return state, errors.New("original settings unavailable")
	}

	for _, key := range keys {
		previous, hasPrevious := state.Previous[key]
		if values[key] != state.Target[key] && values[key] != state.Original[key] && (!hasPrevious || values[key] != previous) {
			return state, errors.New("native settings changed outside this module")
		}
	}

	boot, err := c.Hardware.BootID()
	if err != nil {
		return state, err
	}

	state.Mode = "stock"
	state.Phase = "RESTORING"
	state.Boot = boot
	state.Revision++
	if err := c.save(state); err != nil {
		return state, err
	}

	return c.finish(state, state.Original)
}

func (c Controller) status() (map[string]any, error) {
	state, err := c.load()
	if err != nil {
		return nil, err
	}

	values, err := c.Hardware.Read()
	if err != nil {
		return nil, err
	}

	radios := c.Hardware.Radios(values)
	phase := state.Phase
	if phase == "REBOOT_REQUIRED" {
		boot, err := c.Hardware.BootID()
		if err == nil && boot != state.Boot {
			phase = "VERIFY_REQUIRED"
			selectorsMatch := reflect.DeepEqual(selected(values, originalKeys(state.Original)), state.Target)
			// ASUS may populate an originally empty location during boot.
			// Confirm restoration from the original live country and masks.
			if state.Mode == "stock" {
				selectorsMatch = true
			}

			if selectorsMatch && profileMatches(state, radios) {
				phase = "APPLIED"
				if state.Mode == "stock" {
					phase = "STOCK"
				}
			}
		}
	}

	return map[string]any{
		"state": phase, "mode": state.Mode, "revision": state.Revision,
		"radios": radios, "version": version,
	}, nil
}

func profileMatches(state State, radios []Radio) bool {
	if len(radios) == 0 || len(radios) != len(state.Baseline) {
		return false
	}

	for index, radio := range radios {
		if radio.Interface != state.Baseline[index].Interface || radio.Country == "" || radio.Country == "UNAVAILABLE" {
			return false
		}

		if profile, ok := findProfile(state.Mode); ok {
			if !countryMatches(radio.Country, profile) {
				return false
			}
		} else if radio.Country != state.Baseline[index].Country || radio.BaseMask != state.Baseline[index].BaseMask {
			return false
		}
	}

	return true
}
