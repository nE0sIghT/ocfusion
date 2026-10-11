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
