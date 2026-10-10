// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func install() error {
	if _, err := loadManifest(); err == nil {
		return nil
	}

	var manifest Manifest
	data, err := os.ReadFile("/jffs/ocfusion/manifest.json")
	if err == nil {
		err = json.Unmarshal(data, &manifest)
	} else if os.IsNotExist(err) {
		var transport struct {
			Unit  int `json:"unit"`
			Index int `json:"vpnc_idx"`
		}
		data, err = os.ReadFile("/jffs/ocfusion-platform/transport.json")
		if err == nil {
			err = json.Unmarshal(data, &transport)
		}

		manifest = Manifest{Schema: 1, Unit: transport.Unit, Index: transport.Index, Table: 51998, Priority: 90, Firmware: firmware()}
		rules, ruleErr := runner("ip", "rule", "show")
		if ruleErr != nil {
			return ruleErr
		}

		for _, line := range strings.Split(rules, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "90:") || strings.Contains(line, "lookup 51998") {
				return errors.New("VPN policy routing resources occupied")
			}
		}

		routes, _ := runner("ip", "route", "show", "table", strconv.Itoa(manifest.Table))
		if routes != "" {
			return errors.New("VPN route table occupied")
		}
	}

	if err != nil {
		return err
	}

	if err = verifyIdentity(manifest); err != nil {
		return err
	}

	if err = saveJSON(filepath.Join(root, "connection.json"), manifest); err != nil {
		return err
	}

	return atomicWrite(filepath.Join(root, "owner"), []byte(""), 0600)
}

func remove() error {
	manifest, err := loadManifest()
	if err != nil {
		return err
	}

	assigned, err := networks(manifest)
	if err != nil {
		return err
	}

	if len(assigned) != 0 {
		return errors.New("reassign native Wi-Fi/VLAN networks before removing VPN connection")
	}

	if err = closeTunnel(manifest); err != nil {
		return err
	}

	runner("ip", "route", "del", "unreachable", "default", "metric", "32760", "table", strconv.Itoa(manifest.Table))
	// The stock mailbox reservation belongs to the platform and stays available.
	return os.Rename(filepath.Join(root, "connection.json"), filepath.Join(root, "connection.removed.json"))
}

func main() {
	os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: ocfusion-vpn OPERATION")
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "version":
		fmt.Println("ocfusion-vpn", version)
		return
	case "validate", "reload":
		var envelope struct {
			Config json.RawMessage `json:"config"`
		}
		err = json.NewDecoder(io.LimitReader(os.Stdin, 20000)).Decode(&envelope)
		if len(envelope.Config) != 0 && string(envelope.Config) != "null" && string(envelope.Config) != "{}" {
			err = errors.New("VPN connection is managed through API 1")
		}
	case "preflight":
		manifest, readErr := loadManifest()
		if os.IsNotExist(readErr) {
			data, legacyErr := os.ReadFile("/jffs/ocfusion/manifest.json")
			if legacyErr == nil {
				readErr = json.Unmarshal(data, &manifest)
			}
		}

		if readErr == nil {
			err = verifyIdentity(manifest)
		}
	case "install":
		err = install()
	case "prepare":
		err = prepareKernel()
	case "run":
		err = serve()
	case "diagnose":
		data, readErr := os.ReadFile(filepath.Join(runDir, "status.json"))
		err = readErr
		if err == nil {
			fmt.Println(string(data))
			return
		}
	case "remove":
		err = remove()
	default:
		err = errors.New("unsupported operation")
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "ocfusion-vpn:", err)
		os.Exit(1)
	}

	fmt.Println(`{"ok":true}`)
}
