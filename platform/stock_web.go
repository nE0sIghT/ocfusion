// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const statePageSHA = "272c6e692bf9f058f52dce11da9940ec7a8b95fa1836abce1d3041335a8366f5"
const extensionMarker = "/* ASUS-EXTENSIONS-V1 */"
const statusAnchor = "banner_code += `<div id=\"status_block\" style=\"display: flex; align-items: center;\">`;"
const extensionIcon = `<a id="asus_extensions_link" href="/user/extensions.html" title="OCFusion" aria-label="OCFusion" style="display:flex;align-items:center;justify-content:center;width:30px;height:30px;margin-right:16px;flex-shrink:0"><img src="/user/ocfusion-icon.png" width="28" height="28" alt="" style="object-fit:contain"></a>`

func stateOverlay(original string) (string, error) {
	if strings.Contains(original, extensionMarker) {
		return "", errors.New("expected unmounted stock state.js")
	}

	sum := sha256.Sum256([]byte(original))
	if hex.EncodeToString(sum[:]) != statePageSHA {
		return "", errors.New("stock state.js mismatch; panel remains available by direct URL")
	}

	if strings.Count(original, statusAnchor) != 1 {
		return "", errors.New("stock header anchor missing or ambiguous")
	}

	patched := strings.Replace(original, statusAnchor, statusAnchor+"\n"+extensionMarker+"\nbanner_code += `"+extensionIcon+"`;", 1)
	// vpnc.html is an iframe which loads state.js but does not call show_banner.
	patched += `
if (typeof location !== "undefined") {
    var ocfScript = document.createElement('script');
    ocfScript.src = '/user/ocfusion-contributions.js';
    document.head.appendChild(ocfScript);
}
`

	return patched, nil
}

func ownWebMount(info, target, source string) bool {
	for _, line := range strings.Split(info, "\n") {
		f := strings.Fields(line)
		if len(f) > 5 && f[4] == target && (f[3] == source || f[3] == source+"//deleted") {
			return true
		}
	}

	return false
}

// Never remove somebody else's overlay. Read the firmware original only after
// releasing our own pinned bind inode, so every boot/update starts from stock.
func removeOwnedWebMount(target, source string) error {
	info, e := os.ReadFile("/proc/self/mountinfo")
	if e != nil {
		return e
	}

	for _, line := range strings.Split(string(info), "\n") {
		f := strings.Fields(line)
		if len(f) > 5 && f[4] == target {
			if !ownWebMount(string(info), target, source) {
				return fmt.Errorf("%s already mounted by another owner", target)
			}

			return syscall.Unmount(target, 0)
		}
	}

	return nil
}

func removeWebOverlays() error {
	return removeOwnedWebMount("/www/state.js", "/ocfusion-platform/state.js")
}

func publishWeb() error {
	m, e := loadTransport()
	if e != nil {
		return e
	}

	if e = verifyPlatformAssets(m.Bundle); e != nil {
		return e
	}

	if e = os.MkdirAll(ram, 0700); e != nil {
		return e
	}

	lock, e := os.OpenFile(filepath.Join(ram, "web.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}

	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}

	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	dir := webRoot
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}

	for src, dst := range map[string]string{"web/import.html": "extensions.html", "web/panel.js": "extensions-panel.js", "web/panel.css": "extensions-panel.css", "web/logo.png": "ocfusion-logo.png", "web/icon.png": "ocfusion-icon.png", "web/i18n.js": "ocfusion-i18n.js"} {
		b, err := os.ReadFile(filepath.Join(m.Bundle, src))
		if err != nil {
			return err
		}

		if err = atomicWrite(filepath.Join(dir, dst), b, 0644); err != nil {
			return err
		}
		// Existing bookmarked URL keeps working across the upgrade.

		if dst == "extensions.html" {
			if err = atomicWrite(filepath.Join(dir, "ocfusion.html"), b, 0644); err != nil {
				return err
			}
		}
	}

	js := fmt.Sprintf("window.OCF_SETTINGS={unit:%d,index:%d,key:%q,version:%q,language:%q,model:%q};\n", m.Unit, m.Index, m.Key, version, nvget("preferred_lang"), nvget("productid"))
	if e = atomicWrite(filepath.Join(dir, "ocfusion.js"), []byte(js), 0644); e != nil {
		return e
	}

	if _, e = os.Stat(filepath.Join(base, "web-overlay.enabled")); e != nil {
		return nil
	}

	if e = removeWebOverlays(); e != nil {
		return e
	}

	original, e := os.ReadFile("/www/state.js")
	if e != nil {
		return e
	}

	replacement, e := stateOverlay(string(original))
	if e != nil {
		return e
	}

	overlay := filepath.Join(ram, "state.js")
	if e = atomicWrite(overlay, []byte(replacement), 0644); e != nil {
		return e
	}

	if e = syscall.Mount(overlay, "/www/state.js", "", syscall.MS_BIND, ""); e != nil {
		return fmt.Errorf("header bind mount: %w; /user/extensions.html remains available", e)
	}

	return nil
}
