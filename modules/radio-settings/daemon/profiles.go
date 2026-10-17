// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"sync"
)

type Profile struct {
	ID        string `json:"id"`
	Country   string `json:"country"`
	Revision  string `json:"country_revision"`
	Available bool   `json:"available"`
}

// Native location selectors and their CLM pairs. Driver support is checked
// before offering a profile; live state confirms the firmware mapping on boot.
var nativeProfiles = []Profile{
	{ID: "EU", Country: "E0", Revision: "101"},
	{ID: "AA", Country: "US", Revision: "102"},
	{ID: "AU", Country: "AU", Revision: "100"},
	{ID: "JP", Country: "JP", Revision: "101"},
	{ID: "KR", Country: "KR", Revision: "100"},
	{ID: "XX", Country: "US", Revision: "104"},
}

func findProfile(id string) (Profile, bool) {
	for _, profile := range nativeProfiles {
		if profile.ID == id {
			return profile, true
		}
	}

	return Profile{}, false
}

func availableProfiles(h Hardware, values map[string]Value) []Profile {
	profiles := append([]Profile(nil), nativeProfiles...)
	var workers sync.WaitGroup

	for index := range profiles {
		workers.Add(1)
		go func() {
			defer workers.Done()
			profiles[index].Available = h.SupportsProfile(values, profiles[index])
		}()
	}

	workers.Wait()
	return profiles
}

func countryMatches(country string, profile Profile) bool {
	pair := profile.Country + "/" + profile.Revision
	for _, field := range strings.FieldsFunc(country, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')'
	}) {
		if field == pair {
			return true
		}
	}

	return false
}
