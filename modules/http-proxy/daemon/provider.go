// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

type Reply struct {
	OK       bool          `json:"ok"`
	Egress   *Registration `json:"egress"`
	Targets  []Target      `json:"targets"`
	Networks []Network     `json:"networks"`
}

func provider(op string, c Config) (Reply, error) {
	var result Reply
	conn, err := net.DialTimeout("unix", "/tmp/ocfusion-vpn/control.sock", time.Second)
	if err != nil {
		return result, errors.New("VPN service unavailable")
	}

	defer conn.Close()
	conn.SetDeadline(time.Now().Add(18 * time.Second))
	request := map[string]any{"api": 1, "client": "http-proxy", "operation": op}
	if op == "egress-register" {
		request["egress"] = Registration{Target: c.Target, Bridge: c.Bridge, Port: c.Port, Allowed: c.Allowed}
	}

	if err = json.NewEncoder(conn).Encode(request); err != nil {
		return result, errors.New("VPN request failed")
	}

	if json.NewDecoder(io.LimitReader(conn, 32768)).Decode(&result) != nil || !result.OK {
		return result, errors.New("VPN route unavailable; check VPN and LAN settings")
	}

	return result, nil
}
