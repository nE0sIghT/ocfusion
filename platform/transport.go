// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Transport struct {
	Bundle string            `json:"bundle"`
	Hashes map[string]string `json:"hashes"`
}

var platformAssets = []string{"web/import.html", "web/panel.js", "web/panel.css", "web/logo.png", "web/icon.png", "web/i18n.js"}

func loadTransport() (Transport, error) {
	var transport Transport
	data, err := os.ReadFile(filepath.Join(base, "transport.json"))
	if err == nil {
		err = json.Unmarshal(data, &transport)
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
