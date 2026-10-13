// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

//go:embed header.js
var headerScript string

func stateOverlay(original string) string {
	return original + "\n" + headerScript
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
	}

	js := fmt.Sprintf("window.OCF_SETTINGS={version:%q,revision:%q,language:%q,model:%q};\n", version, revision, nvget("preferred_lang"), nvget("productid"))
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

	replacement := stateOverlay(string(original))

	overlay := filepath.Join(ram, "state.js")
	if e = atomicWrite(overlay, []byte(replacement), 0644); e != nil {
		return e
	}

	if e = syscall.Mount(overlay, "/www/state.js", "", syscall.MS_BIND, ""); e != nil {
		return fmt.Errorf("header bind mount: %w; /user/extensions.html remains available", e)
	}

	return nil
}
