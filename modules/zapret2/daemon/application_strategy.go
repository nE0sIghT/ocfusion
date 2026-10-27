// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "strconv"

func applicationStrategy(c Config, profile ApplicationProfile, kind ApplicationKind) []string {
	args := []string{"--out-range=-d" + strconv.Itoa(profile.Packets), "--payload=" + kind.Payload}
	fake := "--lua-desync=fake:payload=all:blob=" + kind.Blob + fakeOptions(c, kind.Transport == "tcp") + ":repeats=" + strconv.Itoa(profile.Repeats)
	if profile.Method == "fake" || profile.Method == "fake-split" || profile.Method == "fake-disorder" || profile.Method == "fake-fragment" {
		args = append(args, fake)
	}
	switch profile.Method {
	case "split", "disorder", "fake-split", "fake-disorder":
		method := "multisplit"
		if profile.Method == "disorder" || profile.Method == "fake-disorder" {
			method = "multidisorder"
		}
		// Application payloads have no HTTP Host/TLS SNI position markers.
		args = append(args, "--lua-desync="+method+":payload=all:pos="+strconv.Itoa(profile.Position))
	case "fragment", "fake-fragment":
		field := "ipfrag_pos_udp"
		if kind.Transport == "tcp" {
			field = "ipfrag_pos_tcp"
		}
		args = append(args, "--lua-desync=ocf_fragment:payload=all:ipfrag:"+field+"="+strconv.Itoa(profile.Fragment))
	case "length":
		args = append(args, "--lua-desync=udplen:payload=all:increment="+strconv.Itoa(profile.Padding))
	}
	return args
}

func applicationDefaults(id, method string) ApplicationProfile {
	return ApplicationProfile{ID: id, Method: method, Ports: "*", Packets: 3, Position: 2, Repeats: 3, Fragment: 8, Padding: 2}
}

func broadApplicationProfiles(method string) []ApplicationProfile {
	profiles := []ApplicationProfile{}
	for _, kind := range applicationKinds() {
		if kind.ID == "unknown-tcp" {
			continue
		}
		selected := method
		if kind.Transport == "udp" {
			selected = "fake"
		}
		profiles = append(profiles, applicationDefaults(kind.ID, selected))
	}
	return profiles
}

type applicationBlob struct{ Name, File string }

func applicationBlobs() []applicationBlob {
	return []applicationBlob{
		{"ocf_dtls", "dtls_clienthello_w3_org.bin"},
		{"ocf_wireguard", "wireguard_initiation.bin"},
		{"ocf_stun", "stun.bin"},
		{"ocf_discord", "discord-ip-discovery-with-port.bin"},
	}
}

func applicationPresets() []Preset {
	return []Preset{
		{"apps-split", "Web and applications - split", "HTTP/TLS, QUIC, MTProto, DTLS, WireGuard, STUN and voice UDP on selected IPs. Application ports are unrestricted.", "combined", map[string]any{"tcp_strategy": "split", "split_positions": "1,midsld", "udp_strategy": "fake-quic", "applications": broadApplicationProfiles("split"), "auto_ttl": true}},
		{"apps-reorder", "Web and applications - reorder", "Combine fake/reordered web traffic with reordered MTProto and UDP fakes on selected IPs.", "combined", map[string]any{"tcp_strategy": "fake-disorder", "fake_fooling": "badseq", "split_positions": "1,midsld", "udp_strategy": "fake-quic", "applications": broadApplicationProfiles("disorder"), "auto_ttl": true}},
		{"mtproto-split", "Telegram MTProto - split", "Split native MTProto TCP traffic to selected IPs on all ports, including 80, 443 and 5222.", "application", map[string]any{"applications": []ApplicationProfile{applicationDefaults("mtproto", "split")}}},
		{"dtls-fake", "DTLS - fake packets", "Send DTLS-shaped fake handshakes to selected destination IPs. Includes Cisco-compatible DTLS recognition.", "application", map[string]any{"applications": []ApplicationProfile{applicationDefaults("dtls", "fake")}}},
		{"wireguard-fake", "WireGuard - fake packets", "Apply initiation-shaped fake packets to WireGuard on selected IPs, independently of the UDP port.", "application", map[string]any{"applications": []ApplicationProfile{applicationDefaults("wireguard", "fake")}}},
		{"voice-fake", "Calls and games - UDP fakes", "Process STUN, Discord discovery and unrecognized UDP to selected IPs. Only initial outgoing data packets are affected.", "application", map[string]any{"applications": []ApplicationProfile{applicationDefaults("stun", "fake"), applicationDefaults("discord", "fake"), applicationDefaults("unknown-udp", "fake")}}},
		{"udp-fragment", "UDP applications - fragmentation", "Fragment DTLS, WireGuard, STUN and unrecognized UDP packets without changing their application data.", "application", map[string]any{"applications": []ApplicationProfile{applicationDefaults("dtls", "fragment"), applicationDefaults("wireguard", "fragment"), applicationDefaults("stun", "fragment"), applicationDefaults("unknown-udp", "fragment")}}},
	}
}
