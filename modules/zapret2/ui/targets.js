// SPDX-License-Identifier: GPL-3.0-or-later
"use strict";

// Public endpoint suggestions, not hostlists or claims of service coverage.
// Reference: avatarDD/zapret-gui core/targets.py and core/scan_targets.py,
// commit 9d1c39abf6ec7fa80dcceed5ab9c28e329b5889c.
const targetSuggestions = {
  "ChatGPT": ["chatgpt.com"],
  "Claude": ["claude.ai"],
  "Cloudflare": ["www.cloudflare.com"],
  "Discord": ["discord.com", "cdn.discordapp.com", "gateway.discord.gg"],
  "Facebook": ["www.facebook.com"],
  "Google": ["www.google.com"],
  "Instagram": ["instagram.com", "i.instagram.com"],
  "LinkedIn": ["www.linkedin.com"],
  "RuTracker": ["rutracker.org"],
  "Spotify": ["www.spotify.com", "open.spotify.com"],
  "Telegram": ["t.me", "web.telegram.org", "core.telegram.org"],
  "X / Twitter": ["x.com", "twitter.com"],
  "YouTube": ["youtube.com", "www.youtube.com", "i.ytimg.com", "yt3.ggpht.com"],
};

const targetSelect = document.getElementById("scan-target");

for (const [service, hosts] of Object.entries(targetSuggestions)) {
  const group = document.createElement("optgroup");
  group.label = service;

  for (const host of hosts) {
    const option = document.createElement("option");
    option.value = host;
    option.textContent = host;
    group.appendChild(option);
  }

  targetSelect.appendChild(group);
}

targetSelect.addEventListener("change", () => {
  if (targetSelect.value) {
    document.getElementById("scan-host").value = targetSelect.value;
    document.getElementById("scan-port").value = "443";
  }
});

document.getElementById("scan-host").addEventListener("input", () => {
  targetSelect.value = "";
});
