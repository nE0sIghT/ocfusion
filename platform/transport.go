// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Transport struct {
	Unit           int               `json:"unit"`
	Index          int               `json:"vpnc_idx"`
	Key            string            `json:"key"`
	Bundle         string            `json:"bundle"`
	OriginalCustom string            `json:"original_custom"`
	Hashes         map[string]string `json:"hashes"`
}

var platformAssets = []string{"web/import.html", "web/panel.js", "web/panel.css", "web/logo.png", "web/icon.png", "web/i18n.js", "web/diagnostics.js"}

func loadTransport() (Transport, error) {
	var transport Transport
	data, err := os.ReadFile(filepath.Join(base, "transport.json"))
	if err == nil {
		err = json.Unmarshal(data, &transport)
	}

	if err == nil && ((transport.Unit != 0 || transport.Index != 0 || transport.Key != "") && (transport.Unit < 1 || transport.Unit > 5 || transport.Index < 5 || transport.Index > 20 || transport.Key != fmt.Sprintf("vpn_client%d_custom", transport.Unit))) {
		err = errors.New("invalid platform transport")
	}

	return transport, err
}

func preparePlatform(bundle string) error {
	transport := Transport{}
	if previous, err := loadTransport(); err == nil {
		transport = previous
	} else if !os.IsNotExist(err) {
		return err
	}

	transport.Bundle = bundle
	transport.Hashes = map[string]string{}
	for _, relative := range platformAssets {
		data, err := os.ReadFile(filepath.Join(bundle, relative))
		if err != nil {
			return err
		}

		digest := sha256.Sum256(data)
		transport.Hashes[relative] = hex.EncodeToString(digest[:])
	}

	if err := saveJSON(filepath.Join(base, "transport.json"), transport); err != nil {
		return err
	}

	return atomicWrite(filepath.Join(base, "web-overlay.enabled"), []byte("enabled\n"), 0600)
}

func verifyPlatformAssets(bundle string) error {
	transport, err := loadTransport()
	if err != nil {
		return err
	}

	for _, relative := range platformAssets {
		data, err := os.ReadFile(filepath.Join(bundle, relative))
		if err != nil {
			return err
		}

		digest := sha256.Sum256(data)
		if transport.Hashes[relative] != hex.EncodeToString(digest[:]) {
			return errors.New("platform asset checksum mismatch")
		}
	}

	return nil
}
