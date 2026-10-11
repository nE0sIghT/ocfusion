// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Connections struct {
	Schema int                 `json:"schema"`
	Items  map[string]Manifest `json:"connections"`
}

func readConnections() (Connections, error) {
	registry := Connections{Schema: 1, Items: map[string]Manifest{}}
	data, err := os.ReadFile(filepath.Join(root, "connections.json"))
	if err == nil {
		err = json.Unmarshal(data, &registry)
		if err == nil && (registry.Schema != 1 || registry.Items == nil) {
			err = errors.New("invalid connection registry")
		}

		if err == nil {
			used := map[string]bool{}
			for id, item := range registry.Items {
				if id != item.ID || !identifier.MatchString(id) || !identifier.MatchString(item.Client) || !identifier.MatchString(item.Profile) || !ifaceRE.MatchString(item.Interface) || item.Unit < 1 || item.Unit > 5 || item.Index < 5 || item.Index > 20 || item.Table < 1000 || item.Priority < 50 {
					return registry, errors.New("invalid connection record")
				}
				keys := []string{fmt.Sprintf("unit:%d", item.Unit), fmt.Sprintf("index:%d", item.Index), fmt.Sprintf("table:%d", item.Table), fmt.Sprintf("priority:%d", item.Priority), "interface:" + item.Interface, "owner:" + item.Client + "/" + item.Profile}
				for _, key := range keys {
					if used[key] {
						return registry, errors.New("duplicate connection resource")
					}
					used[key] = true
				}
			}
		}

		return registry, err
	}
	if !os.IsNotExist(err) {
		return registry, err
	}

	return registry, nil
}

func writeConnections(registry Connections) error {
	return saveJSON(filepath.Join(root, "connections.json"), registry)
}

func occupiedNative(raw string) (map[int]bool, map[int]bool) {
	units := map[int]bool{}
	indices := map[int]bool{}
	for _, row := range strings.Split(raw, "<") {
		fields := strings.Split(row, ">")
		if len(fields) < 7 {
			continue
		}

		index, _ := strconv.Atoi(fields[6])
		indices[index] = true
		if fields[1] == "OpenVPN" {
			unit, _ := strconv.Atoi(fields[2])
			units[unit] = true
		}
	}

	return units, indices
}

func allocateConnection(registry Connections, client, profile string) (Manifest, error) {
	manifest := Manifest{Schema: 1, Client: client, Profile: profile, Firmware: firmware()}
	for _, item := range registry.Items {
		if item.Client == client && item.Profile == profile {
			return item, nil
		}
	}

	raw := nvget("vpnc_clientlist")
	units, indices := occupiedNative(raw)
	for unit := 1; unit <= 5; unit++ {
		if !units[unit] && nvget(fmt.Sprintf("vpn_client%d_addr", unit)) == "" && nvget(fmt.Sprintf("vpn_client%d_custom", unit)) == "" && !strings.Contains(","+nvget("vpn_clientx_eas")+",", ","+strconv.Itoa(unit)+",") {
			manifest.Unit = unit
			break
		}
	}

	for index := 5; index <= 20; index++ {
		if !indices[index] {
			manifest.Index = index
			break
		}
	}

	if manifest.Unit == 0 || manifest.Index == 0 {
		return manifest, errors.New("no free native VPN profile slot")
	}

	rules, err := runner("ip", "rule", "show")
	if err != nil {
		return manifest, err
	}
	for slot := 0; slot < 32; slot++ {
		candidate := Manifest{Table: 51998 + slot, Priority: 90 + slot, Interface: fmt.Sprintf("ocvpn%d", slot)}
		occupied := strings.Contains(rules, "lookup "+strconv.Itoa(candidate.Table))
		for _, line := range strings.Split(rules, "\n") {
			occupied = occupied || strings.HasPrefix(strings.TrimSpace(line), strconv.Itoa(candidate.Priority)+":")
		}
		for _, item := range registry.Items {
			occupied = occupied || item.Table == candidate.Table || item.Priority == candidate.Priority || item.Interface == candidate.Interface
		}
		routes, routeErr := runner("ip", "route", "show", "table", strconv.Itoa(candidate.Table))
		if routeErr != nil || routes != "" {
			continue
		}
		if _, linkErr := runner("ip", "link", "show", "dev", candidate.Interface); linkErr == nil {
			occupied = true
		}
		if occupied {
			continue
		}

		manifest.Table = candidate.Table
		manifest.Priority = candidate.Priority
		manifest.Interface = candidate.Interface
		break
	}
	if manifest.Interface == "" {
		return manifest, errors.New("no free VPN network resources")
	}

	manifest.ID = fmt.Sprintf("connection-%d", manifest.Index)
	if nvget("vpnc_clientlist") != raw {
		return manifest, errors.New("native VPN profiles changed during allocation")
	}

	manifest.Name = "OCFusion " + client + "/" + profile
	row := fmt.Sprintf("%s>OpenVPN>%d>>>0>%d>>>0>0>Web", manifest.Name, manifest.Unit, manifest.Index)
	updated := row
	if raw != "" {
		updated = raw + "<" + row
	}
	if err := nvset("vpnc_clientlist", updated); err != nil {
		return manifest, err
	}
	if _, err := runner("nvram", "commit"); err != nil {
		nvset("vpnc_clientlist", raw)
		runner("nvram", "commit")
		return manifest, err
	}

	return manifest, nil
}

func removeNative(manifest Manifest) error {
	if err := verifyIdentity(manifest); err != nil {
		return err
	}

	raw := nvget("vpnc_clientlist")
	rows := []string{}
	for _, row := range strings.Split(raw, "<") {
		fields := strings.Split(row, ">")
		if len(fields) > 6 && fields[6] == strconv.Itoa(manifest.Index) {
			continue
		}
		rows = append(rows, row)
	}
	if err := nvset("vpnc_clientlist", strings.Join(rows, "<")); err != nil {
		return err
	}
	if _, err := runner("nvram", "commit"); err != nil {
		nvset("vpnc_clientlist", raw)
		runner("nvram", "commit")
		return err
	}

	return nil
}

func connectionIDs(registry Connections) []string {
	ids := make([]string, 0, len(registry.Items))
	for id := range registry.Items {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	return ids
}
