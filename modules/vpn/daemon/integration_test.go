// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
)

func TestFusionFragmentAndLegacyOwnership(t *testing.T) {
	for _, original := range []string{"<div>fragment</div>", "<body>full</body>"} {
		got := fusionOverlay(original, "<script>link</script>")
		if !strings.Contains(got, "<script>link</script>") {
			t.Fatal("missing injection")
		}
	}

	if !ownFusionMount("41 22 0:30 /ocfusion/vpnc.html /www/VPN/vpnc.html rw - tmpfs tmpfs rw") {
		t.Fatal("missed owned legacy overlay")
	}

	if !ownFusionMount("27 11 0:19 /ocfusion/vpnc.html//deleted /www/VPN/vpnc.html rw,relatime - tmpfs tmpfs rw") {
		t.Fatal("missed pinned deleted inode")
	}

	if ownFusionMount("41 22 0:30 /ocfusion/vpnc.html/foreign /www/VPN/vpnc.html rw - tmpfs tmpfs rw") {
		t.Fatal("accepted arbitrary source suffix")
	}

	if ownFusionMount("41 22 0:30 /foreign/page /www/VPN/vpnc.html rw - tmpfs tmpfs rw") {
		t.Fatal("accepted foreign overlay")
	}
}

func TestFusionRenamePreservesBindings(t *testing.T) {
	raw := "Other>WireGuard>1>>>0>2<OpenConnect (SSH)>OpenVPN>4>>>0>7>>>0>0>Web"
	m := Manifest{Unit: 4, Index: 7}
	got, e := renamedFusion(raw, m)
	if e != nil || got != strings.Replace(raw, "OpenConnect (SSH)", fusionName, 1) {
		t.Fatal(got, e)
	}

	again, e := renamedFusion(got, m)
	if e != nil || again != got {
		t.Fatal("not idempotent")
	}

	m.Unit = 3
	if _, e = renamedFusion(raw, m); e == nil {
		t.Fatal("accepted wrong unit")
	}
}
