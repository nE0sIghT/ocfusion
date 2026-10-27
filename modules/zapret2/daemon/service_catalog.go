// SPDX-License-Identifier: GPL-3.0-or-later
package main

// Runtime data is fetched directly from its publisher, never bundled or run.
// Catalog paths inspected at itdoginfo/allow-domains commit
// 820ba130c1b5bf94846d4c3b060cdecd5eae89c7. Each update resolves a new immutable
// upstream commit before downloading all parts of a service snapshot.
type ServiceList struct {
	Notice string   `json:"notice,omitempty"`
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Paths  []string `json:"paths"`
}

func serviceCatalog() []ServiceList {
	return []ServiceList{
		{ID: "discord", Name: "Discord", Paths: []string{"Services/discord.lst", "Subnets/IPv4/discord.lst", "Subnets/IPv6/discord.lst"}},
		{ID: "google-ai", Name: "Google AI", Paths: []string{"Services/google_ai.lst"}},
		{ID: "google-meet", Name: "Google Meet", Paths: []string{"Services/google_meet.lst", "Subnets/IPv4/google_meet.lst", "Subnets/IPv6/google_meet.lst"}},
		{ID: "google-play", Name: "Google Play", Paths: []string{"Services/google_play.lst"}},
		{ID: "hdrezka", Name: "HDrezka", Paths: []string{"Services/hdrezka.lst"}},
		{ID: "meta", Name: "Meta (Instagram, Facebook, WhatsApp)", Paths: []string{"Services/meta.lst", "Subnets/IPv4/meta.lst", "Subnets/IPv6/meta.lst"}},
		{ID: "roblox", Name: "Roblox", Paths: []string{"Services/roblox.lst", "Subnets/IPv4/roblox.lst"}},
		{ID: "telegram", Name: "Telegram", Paths: []string{"Services/telegram.lst", "Subnets/IPv4/telegram.lst", "Subnets/IPv6/telegram.lst"}},
		{ID: "tiktok", Name: "TikTok", Paths: []string{"Services/tiktok.lst"}},
		{ID: "twitter", Name: "X / Twitter", Paths: []string{"Services/twitter.lst", "Subnets/IPv4/twitter.lst", "Subnets/IPv6/twitter.lst"}},
		{ID: "youtube", Name: "YouTube", Paths: []string{"Services/youtube.lst"}},
		{ID: "russia", Name: "Russia - general list", Paths: []string{"Russia/inside-raw.lst"}, Notice: "General list includes entire domain zones, not only individual services."},
	}
}

func knownService(id string) (ServiceList, bool) {
	for _, entry := range serviceCatalog() {
		if entry.ID == id {
			return entry, true
		}
	}
	return ServiceList{}, false
}
