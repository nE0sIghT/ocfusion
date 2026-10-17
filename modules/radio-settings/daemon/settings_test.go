// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeHardware struct {
	values     map[string]Value
	radios     []Radio
	boot       string
	supported  bool
	writes     int
	commits    int
	failWrite  int
	failCommit bool
}

func (h *fakeHardware) Read() (map[string]Value, error) {
	return selected(h.values, append(managedKeys(), "wl1_ifname", "wl1_nband")), nil
}

func (h *fakeHardware) Write(key string, value Value) error {
	h.writes++
	if h.failWrite == h.writes {
		return errors.New("write failed")
	}

	if value.Present {
		h.values[key] = value
	} else {
		delete(h.values, key)
	}

	return nil
}

func (h *fakeHardware) Commit() error {
	h.commits++
	if h.failCommit {
		return errors.New("commit failed")
	}

	return nil
}

func (h *fakeHardware) Radios(map[string]Value) []Radio {
	return h.radios
}

func (h *fakeHardware) SupportsProfile(map[string]Value, Profile) bool {
	return h.supported
}

func (h *fakeHardware) BootID() (string, error) {
	return h.boot, nil
}

func fixture(t *testing.T) (Controller, *fakeHardware) {
	t.Helper()

	h := &fakeHardware{
		values: map[string]Value{
			"ui_location_code":            {Present: true},
			"wl1_ifname":                  {Present: true, Text: "wl1"},
			"wl1_nband":                   {Present: true, Text: "1"},
			"wl1_country_abbrev_override": {Present: true, Text: "DE"},
		},
		radios: []Radio{{Interface: "wl1", Country: "DE (E0/101) GERMANY", BaseMask: "23 (0x17)"}},
		boot:   "first", supported: true,
	}
	return Controller{Directory: t.TempDir(), Hardware: h}, h
}

func TestPassiveLifecycleNeverChangesNVRAM(t *testing.T) {
	c, h := fixture(t)
	for _, operation := range []string{"validate", "prepare", "reload", "settings", "diagnose", "remove"} {
		if _, err := execute(c, operation, nil); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
	}

	if h.writes != 0 || h.commits != 0 {
		t.Fatal("passive lifecycle changed native settings")
	}
}

func TestApplyRebootRestorePreservesPresenceAndOtherSettings(t *testing.T) {
	c, h := fixture(t)
	h.values["unrelated"] = Value{Present: true, Text: "keep"}
	before := selected(h.values, []string{"location_code", "ui_location_code", "wl1_country_abbrev_override", "unrelated"})

	result, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`))
	if err != nil {
		t.Fatal(err)
	}

	encoded, _ := json.Marshal(result)
	if len(encoded) > 2048 || strings.Contains(string(encoded), "baseline") || strings.Contains(string(encoded), "original") {
		t.Fatal("action response exposed internal transaction")
	}

	if h.values["location_code"].Text != "AA" || h.values["wl1_country_abbrev_override"].Text != "US" || h.commits != 1 {
		t.Fatal("native selectors not committed once")
	}

	assertPhase(t, c, "REBOOT_REQUIRED")
	h.boot = "second"
	assertPhase(t, c, "VERIFY_REQUIRED")

	h.radios = []Radio{{Interface: "wl1", Country: "US (US/102) UNITED STATES", BaseMask: "7 (0x7)"}}
	assertPhase(t, c, "APPLIED")

	if _, err := execute(c, "restore", json.RawMessage(`{"revision":1}`)); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(before, selected(h.values, []string{"location_code", "ui_location_code", "wl1_country_abbrev_override", "unrelated"})) {
		t.Fatal("restore lost missing/empty distinction or changed unrelated settings")
	}

	if _, exists := h.values["location_code"]; exists {
		t.Fatal("originally absent key was retained")
	}

	assertPhase(t, c, "REBOOT_REQUIRED")
	h.boot = "third"
	h.radios = []Radio{{Interface: "wl1", Country: "DE (E0/101) GERMANY", BaseMask: "23 (0x17)"}}
	assertPhase(t, c, "STOCK")

	settings, err := c.settings()
	if err != nil || !settings.CanApply || settings.Revision != 2 {
		t.Fatal("restored module cannot be configured again", err)
	}

	if _, err := execute(c, "apply", json.RawMessage(`{"revision":2,"profile":"AA"}`)); err != nil {
		t.Fatal(err)
	}
}

func assertPhase(t *testing.T, c Controller, wanted string) {
	t.Helper()

	status, err := c.status()
	if err != nil || status["state"] != wanted {
		t.Fatalf("want %s, got %v, error %v", wanted, status, err)
	}
}

func TestStaleRevisionAndExternalChangeAreRejected(t *testing.T) {
	c, h := fixture(t)
	if _, err := execute(c, "apply", json.RawMessage(`{"revision":1,"profile":"AA"}`)); err == nil || h.writes != 0 {
		t.Fatal("stale revision applied")
	}

	if _, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`)); err != nil {
		t.Fatal(err)
	}

	writes := h.writes
	h.values["location_code"] = Value{Present: true, Text: "JP"}
	if _, err := execute(c, "restore", json.RawMessage(`{"revision":1}`)); err == nil || h.writes != writes {
		t.Fatal("restore overwrote an external change")
	}
}

func TestInterruptedTransactionCanBeRestored(t *testing.T) {
	for _, commitFailure := range []bool{false, true} {
		name := "write failure"
		if commitFailure {
			name = "commit failure"
		}

		t.Run(name, func(t *testing.T) {
			c, h := fixture(t)
			h.failCommit = commitFailure
			if !commitFailure {
				h.failWrite = 2
			}

			if _, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`)); err == nil {
				t.Fatal("failure was not reported")
			}

			assertPhase(t, c, "RECOVERY_REQUIRED")
			h.failWrite = 0
			h.failCommit = false
			if _, err := execute(c, "restore", json.RawMessage(`{"revision":1}`)); err != nil {
				t.Fatal(err)
			}

			if _, present := h.values["location_code"]; present {
				t.Fatal("interrupted write was not restored")
			}
		})
	}
}

func TestUnsupportedAndOverlappingActionsDoNotWrite(t *testing.T) {
	c, h := fixture(t)
	h.supported = false
	if _, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`)); err == nil || h.writes != 0 {
		t.Fatal("unsupported profile applied")
	}

	lock, err := c.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	if _, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`)); err == nil {
		t.Fatal("overlapping action accepted")
	}
}

func TestPrivateStateAndNVRAMWhitelist(t *testing.T) {
	values := parseNVRAM("password=SECRET\nlocation_code=\nwl1_ifname=wl1\nwl1_country_abbrev_override=DE\n")
	if _, present := values["password"]; present {
		t.Fatal("secret retained")
	}

	if !values["location_code"].Present || values["ui_location_code"].Present {
		t.Fatal("presence was lost")
	}

	c, _ := fixture(t)
	if _, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`)); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(c.Directory, "state.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private snapshot permissions", err)
	}
}

func TestRemoveRestoresSelectorsWithoutRestart(t *testing.T) {
	c, h := fixture(t)
	if _, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`)); err != nil {
		t.Fatal(err)
	}

	if _, err := execute(c, "remove", nil); err != nil {
		t.Fatal(err)
	}

	if h.values["wl1_country_abbrev_override"].Text != "DE" || h.commits != 2 {
		t.Fatal("remove did not restore selectors exactly once")
	}

	assertPhase(t, c, "REBOOT_REQUIRED")
}

func TestCountryChangeWithoutRebootDoesNotConfirmProfile(t *testing.T) {
	c, h := fixture(t)
	if _, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`)); err != nil {
		t.Fatal(err)
	}

	h.radios = []Radio{{Interface: "wl1", Country: "US (US/102) UNITED STATES"}}
	assertPhase(t, c, "REBOOT_REQUIRED")
}

func TestRestoreAcceptsNativeLocationNormalization(t *testing.T) {
	c, h := fixture(t)
	if _, err := execute(c, "apply", json.RawMessage(`{"revision":0,"profile":"AA"}`)); err != nil {
		t.Fatal(err)
	}

	if _, err := execute(c, "restore", json.RawMessage(`{"revision":1}`)); err != nil {
		t.Fatal(err)
	}

	h.boot = "second"
	h.values["location_code"] = Value{Present: true, Text: "EU"}
	assertPhase(t, c, "STOCK")
}

func TestSwitchProfileRetainsOriginalSnapshot(t *testing.T) {
	c, h := fixture(t)
	before := selected(h.values, managedKeys())

	if _, err := c.apply(0, "AA"); err != nil {
		t.Fatal(err)
	}

	h.boot = "second-boot"
	h.radios[0].Country = "US (US/102) UNITED STATES"
	if _, err := c.apply(1, "AU"); err != nil {
		t.Fatal(err)
	}

	state, err := c.load()
	if err != nil || state.Mode != "AU" || state.Baseline[0].Country != "DE (E0/101) GERMANY" {
		t.Fatalf("lost original baseline: %+v, %v", state, err)
	}

	if h.values["wl1_country_abbrev_override"].Text != "AU" {
		t.Fatal("profile country not selected")
	}

	if _, err := c.restore(2); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(selected(h.values, managedKeys()), before) {
		t.Fatal("restore did not return to first original settings")
	}
}

func TestProfileCatalogAndCountryVerification(t *testing.T) {
	c, h := fixture(t)
	settings, err := c.settings()
	if err != nil || len(settings.Profiles) != 6 {
		t.Fatalf("catalog unavailable: %+v, %v", settings, err)
	}

	for _, profile := range settings.Profiles {
		if !profile.Available || !countryMatches("REGION ("+profile.Country+"/"+profile.Revision+")", profile) {
			t.Fatalf("invalid profile: %+v", profile)
		}

		if countryMatches(profile.Country+"/"+profile.Revision+"0", profile) {
			t.Fatal("partial revision accepted")
		}
	}

	if _, err := c.apply(0, "arbitrary"); err == nil || h.writes != 0 {
		t.Fatal("unknown profile changed settings")
	}
}

func TestSwitchRejectsExternalChanges(t *testing.T) {
	c, h := fixture(t)
	if _, err := c.apply(0, "AA"); err != nil {
		t.Fatal(err)
	}

	h.boot = "second-boot"
	h.radios[0].Country = "US (US/102) UNITED STATES"
	h.values["ui_location_code"] = Value{Present: true, Text: "JP"}
	writes := h.writes
	if _, err := c.apply(1, "AU"); err == nil || h.writes != writes {
		t.Fatal("profile switch overwrote external changes")
	}
}

func TestRestoreAfterInterruptedProfileSwitch(t *testing.T) {
	for _, failure := range []int{1, 2, 3} {
		t.Run(string(rune('0'+failure)), func(t *testing.T) {
			c, h := fixture(t)
			before := selected(h.values, managedKeys())
			if _, err := c.apply(0, "AA"); err != nil {
				t.Fatal(err)
			}

			h.boot = "second-boot"
			h.radios[0].Country = "US (US/102) UNITED STATES"
			h.failWrite = h.writes + failure
			if _, err := c.apply(1, "AU"); err == nil {
				t.Fatal("injected failure was ignored")
			}

			h.failWrite = 0
			if _, err := c.restore(2); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(selected(h.values, managedKeys()), before) {
				t.Fatal("interrupted switch lost original settings")
			}
		})
	}
}

func TestApplyEachProfile(t *testing.T) {
	for _, profile := range nativeProfiles {
		t.Run(profile.ID, func(t *testing.T) {
			c, h := fixture(t)
			if _, err := c.apply(0, profile.ID); err != nil {
				t.Fatal(err)
			}

			if h.values["location_code"].Text != profile.ID || h.values["wl1_country_abbrev_override"].Text != profile.Country {
				t.Fatal("incorrect regional selector or advertised country")
			}

			h.boot = "second-boot"
			h.radios[0].Country = profile.Country + "/" + profile.Revision
			status, err := c.status()
			if err != nil || status["state"] != "APPLIED" {
				t.Fatalf("profile not verified: %+v %v", status, err)
			}
		})
	}
}
