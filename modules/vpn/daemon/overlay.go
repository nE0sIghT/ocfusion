// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "strings"

// ASUS vpnc.html is an HTML fragment and has no closing body tag.
func fusionOverlay(original, injection string) string {
	if strings.Contains(original, "</body>") {
		return strings.Replace(original, "</body>", injection+"</body>", 1)
	}

	return original + "\n" + injection + "\n"
}

func ownFusionMount(info string) bool {
	for _, line := range strings.Split(info, "\n") {
		f := strings.Fields(line)
		if len(f) > 5 && (f[3] == "/ocfusion/vpnc.html" || f[3] == "/ocfusion/vpnc.html//deleted") && f[4] == "/www/VPN/vpnc.html" {
			return true
		}
	}

	return false
}
