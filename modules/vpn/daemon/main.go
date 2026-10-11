// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func install() error {
	registry, err := readConnections()
	if err != nil {
		return err
	}

	if err = writeConnections(registry); err != nil {
		return err
	}

	// The old shared profile becomes a normal VPN-owned binding. Clear only
	// recognizable mailbox data; preserve custom fields edited by an administrator.
	var previous struct {
		Unit     int    `json:"unit"`
		Key      string `json:"key"`
		Original string `json:"original_custom"`
	}
	data, err := os.ReadFile(transportPath)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil || json.Unmarshal(data, &previous) != nil {
		return errors.New("invalid previous transport ownership")
	}

	if previous.Unit == 0 {
		return nil
	}

	if previous.Unit < 1 || previous.Unit > 5 || previous.Key != fmt.Sprintf("vpn_client%d_custom", previous.Unit) {
		return errors.New("invalid previous transport binding")
	}

	value := nvget(previous.Key)
	if strings.HasPrefix(value, "# EXT-REQUEST1 ") || strings.HasPrefix(value, "# OCF-STATUS1 ") {
		if err := nvset(previous.Key, previous.Original); err != nil {
			return err
		}

		_, err = runner("nvram", "commit")
		return err
	}

	return nil
}

func remove() error {
	registry, err := readConnections()
	if err != nil {
		return err
	}
	for _, manifest := range registry.Items {
		assigned, err := networks(manifest)
		if err != nil || len(assigned) != 0 {
			return errors.New("reassign native networks before removing VPN provider")
		}
	}
	for _, manifest := range registry.Items {
		if err := closeTunnel(manifest); err != nil {
			return err
		}
		if err := removeNative(manifest); err != nil {
			return err
		}
	}

	return writeConnections(Connections{Schema: 1, Items: map[string]Manifest{}})
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
		var registry Connections
		registry, err = readConnections()
		if err == nil {
			for _, manifest := range registry.Items {
				if err = verifyIdentity(manifest); err != nil {
					break
				}
			}
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
