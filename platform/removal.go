// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type RemovalResult struct {
	RebootRequired bool `json:"reboot_required"`
}

// The RAM marker survives manager restarts and partial removal, but not reboot.
// Record it before removing the registry entry so retries retain the notice.
func recordRemovalResult(data json.RawMessage) (RemovalResult, error) {
	var result RemovalResult
	if err := json.Unmarshal(data, &result); err != nil {
		return result, errors.New("invalid module removal result")
	}

	if result.RebootRequired {
		if err := os.MkdirAll(ram, 0700); err != nil {
			return result, err
		}

		if err := os.WriteFile(filepath.Join(ram, "removal-reboot-required"), nil, 0600); err != nil {
			return result, err
		}
	}

	return result, nil
}
