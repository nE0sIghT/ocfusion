// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const publicStatusLimit = 16384

// Modules opt in by writing a redacted JSON snapshot to EXT_PUBLIC_STATUS_PATH.
// Publishing is generic; protocol-specific redaction belongs to the module.
func (m *Manager) publishModuleStatus(id, state string) {
	var data json.RawMessage
	sourceTime := int64(0)
	if state == "RUNNING" && !m.stopping {
		path := filepath.Join(ram, "modules", id, "public-status.json")
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Size() <= publicStatusLimit && time.Since(info.ModTime()) < 15*time.Second {
			file, err := os.Open(path)
			if err == nil {
				raw, readErr := io.ReadAll(io.LimitReader(file, publicStatusLimit+1))
				file.Close()
				if readErr == nil && len(raw) <= publicStatusLimit && json.Valid(raw) {
					data = raw
					sourceTime = info.ModTime().UnixMilli()
				}
			}
		}
	}

	raw, _ := json.Marshal(map[string]any{
		"module":          id,
		"state":           state,
		"updated_at":      time.Now().UnixMilli(),
		"data_updated_at": sourceTime,
		"data":            data,
	})
	destination := filepath.Join(webRoot, "extensions-status", id+".json")
	os.MkdirAll(filepath.Dir(destination), 0755)
	atomicWrite(destination, raw, 0644)
}

// Fixed, input-free probes identify stock handler behavior. They neither expose
// private files nor install a listener or accept commands from the browser.
func publishTransportProbes() error {
	probes := map[string]string{
		"ocfusion-probe.cgi": "#!/bin/sh\n# SPDX-License-Identifier: GPL-3.0-or-later\nprintf 'Content-Type: application/json\\r\\n\\r\\n{\"ocfusion_cgi_executed\":true}\\n'\n",
		"ocfusion-probe.asp": `{"ocfusion_template":"<% nvram_get("preferred_lang"); %>"}`,
	}

	for name, contents := range probes {
		mode := os.FileMode(0644)
		if filepath.Ext(name) == ".cgi" {
			mode = 0755
		}
		if err := atomicWrite(filepath.Join(webRoot, name), []byte(contents), mode); err != nil {
			return err
		}
	}

	return nil
}
