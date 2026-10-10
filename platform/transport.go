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
	"strconv"
	"strings"
)

type Transport struct {
	Unit           int               `json:"unit"`
	Index          int               `json:"vpnc_idx"`
	Key            string            `json:"key"`
	Bundle         string            `json:"bundle"`
	OriginalCustom string            `json:"original_custom"`
	Hashes         map[string]string `json:"hashes"`
}

var platformAssets = []string{"web/import.html", "web/panel.js", "web/panel.css", "web/logo.png", "web/icon.png", "web/i18n.js"}

func loadTransport() (Transport, error) {
	var transport Transport
	data, err := os.ReadFile(filepath.Join(base, "transport.json"))
	if err == nil {
		err = json.Unmarshal(data, &transport)
	}

	if err == nil && (transport.Unit < 1 || transport.Unit > 5 || transport.Index < 5 || transport.Index > 20 || transport.Key != fmt.Sprintf("vpn_client%d_custom", transport.Unit)) {
		err = errors.New("invalid platform transport")
	}

	return transport, err
}

func preparePlatform(bundle string) error {
	transport, err := loadTransport()
	if os.IsNotExist(err) {
		// Adopt the existing reservation during an MVP upgrade; do not allocate a new index.
		data, legacyErr := os.ReadFile("/jffs/ocfusion/manifest.json")
		if legacyErr == nil {
			legacyErr = json.Unmarshal(data, &transport)
		}

		if legacyErr != nil {
			transport, err = reserveTransport()
			if err != nil {
				return err
			}
		}
	} else if err != nil {
		return err
	}

	transport.Key = fmt.Sprintf("vpn_client%d_custom", transport.Unit)
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

	if err = saveJSON(filepath.Join(base, "transport.json"), transport); err != nil {
		return err
	}

	return atomicWrite(filepath.Join(base, "web-overlay.enabled"), []byte("enabled\n"), 0600)
}

func reserveTransport() (Transport, error) {
	usedUnits := map[int]bool{}
	usedIndices := map[int]bool{}
	list := nvget("vpnc_clientlist")
	for _, record := range strings.Split(list, "<") {
		fields := strings.Split(record, ">")
		if len(fields) > 6 {
			index, _ := strconv.Atoi(fields[6])
			usedIndices[index] = true
			if fields[1] == "OpenVPN" {
				unit, _ := strconv.Atoi(fields[2])
				usedUnits[unit] = true
			}
		}
	}

	transport := Transport{}
	for unit := 5; unit >= 1; unit-- {
		if !usedUnits[unit] && nvget(fmt.Sprintf("vpn_client%d_addr", unit)) == "" && nvget(fmt.Sprintf("vpn_client%d_custom", unit)) == "" && !strings.Contains(","+nvget("vpn_clientx_eas")+",", ","+strconv.Itoa(unit)+",") {
			transport.Unit = unit
			break
		}
	}

	for index := 5; index <= 20; index++ {
		if !usedIndices[index] {
			transport.Index = index
			break
		}
	}

	if transport.Unit == 0 || transport.Index == 0 {
		return transport, errors.New("no free stock slot for authenticated platform transport")
	}

	record := fmt.Sprintf("OCFusion (managed)>OpenVPN>%d>>>0>%d>>>0>0>Web", transport.Unit, transport.Index)
	if list != "" {
		list += "<"
	}

	if err := nvset("vpnc_clientlist", list+record); err != nil {
		return transport, err
	}

	_, err := runner("nvram", "commit")

	return transport, err
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
