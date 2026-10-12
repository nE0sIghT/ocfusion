// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Use an explicit public projection. Request replies, import metadata and raw
// daemon errors never belong in a web-readable snapshot.
type PublicProfileStatus struct {
	DNSPath    string        `json:"dns_path,omitempty"`
	DNSReason  string        `json:"dns_reason,omitempty"`
	OTP        *OTPChallenge `json:"otp,omitempty"`
	Profile    string        `json:"profile"`
	State      string        `json:"state"`
	Message    string        `json:"message"`
	FusionName string        `json:"fusion_name"`
	Index      int           `json:"vpnc_idx"`
	Networks   []Network     `json:"networks"`
}

func publicProfileStatus(id string) PublicProfileStatus {
	directory := "/tmp/ocfusion"
	if id != "default" {
		directory = filepath.Join(directory, "profiles", id)
	}

	return publicProfileStatusAt(id, directory)
}

func publicProfileStatusAt(id, directory string) PublicProfileStatus {
	result := PublicProfileStatus{Profile: id, State: "STARTING", Networks: []Network{}}
	info, err := os.Stat(filepath.Join(directory, "status.json"))
	if err != nil {
		return result
	}

	if time.Since(info.ModTime()) >= 15*time.Second {
		result.State = "UNKNOWN"
		result.Message = "Public status unavailable"
		return result
	}

	raw, err := readStatusAt(directory)
	if err != nil {
		return result
	}

	var st Status
	if json.Unmarshal(raw, &st) != nil {
		return result
	}

	return publicStatusProjection(id, st)
}

func publicStatusProjection(id string, st Status) PublicProfileStatus {
	result := PublicProfileStatus{Profile: id, Networks: []Network{}}

	messages := map[string]string{
		"UP":              "IPv4 full tunnel; Wi-Fi/VLAN bindings active",
		"NO_PROFILE":      "Import a valid OpenConnect profile",
		"WAIT_NETWORK":    "Check the assigned Wi-Fi/VLAN networks and SSH diagnostics.",
		"AUTH_REQUIRED":   "Authentication/TLS failed; correct profile and import again",
		"WAIT_OTP":        "Enter the one-time code",
		"WAIT_TIME":       "Wait for router NTP before TLS",
		"DISABLED":        "Profile disabled; bound networks remain blocked",
		"CONNECTING":      "Connecting to the VPN server.",
		"DOWN":            "Tunnel down; WAN fallback blocked",
		"ERROR":           "Check SSH diagnostics for details.",
		"CONFIG_CONFLICT": "Check SSH diagnostics for details.",
		"NETWORK_CHANGED": "Rebinding current ASUS web configuration",
		"WAIT_STORAGE":    "USB bundle missing/changed",
		"RECONNECTING":    "VPN exited; retry scheduled",
		"STOPPING":        "",
		"STOPPED":         "",
		"STARTING":        "",
	}
	message, known := messages[st.State]
	if !known {
		result.State = "UNKNOWN"
		result.Message = "Check SSH diagnostics for details."
		return result
	}

	if st.DNSPath == "VPN" || st.DNSPath == "WAN" || st.DNSPath == "ERROR" {
		result.DNSPath = st.DNSPath
		switch st.DNSReason {
		case "VPN unavailable", "native IPv6 DNS requires WAN":
			result.DNSReason = st.DNSReason
		case "":
		default:
			result.DNSReason = "Check DNS routing diagnostics"
		}
	}

	if st.State == "WAIT_OTP" {
		result.OTP = st.OTP
	}

	result.State = st.State
	result.Message = message
	result.FusionName = st.FusionName
	result.Index = st.Index
	if st.Networks != nil {
		result.Networks = st.Networks
	}

	return result
}

func publishPublicProfiles() error {
	path := os.Getenv("EXT_PUBLIC_STATUS_PATH")
	if path == "" {
		return nil
	}

	ids, err := profileIDs()
	if err != nil {
		return err
	}

	profiles := map[string]PublicProfileStatus{}
	for _, id := range ids {
		profiles[id] = publicProfileStatus(id)
	}

	return saveJSON(path, map[string]any{"profiles": profiles})
}
