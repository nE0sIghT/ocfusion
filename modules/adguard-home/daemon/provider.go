// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

type ProviderResponse struct {
	OK       bool         `json:"ok"`
	Resolver Registration `json:"resolver"`
	DNS      struct {
		Path string `json:"path"`
	} `json:"dns_status"`
}

func (response ProviderResponse) DNSPath() string {
	if response.OK && (response.DNS.Path == "VPN" || response.DNS.Path == "WAN") {
		return response.DNS.Path
	}

	return "UNKNOWN"
}

func provider(operation string) (ProviderResponse, error) {
	var response ProviderResponse
	connection, err := net.DialTimeout("unix", "/tmp/ocfusion-vpn/control.sock", time.Second)
	if err != nil {
		return response, errors.New("VPN provider unavailable")
	}

	defer connection.Close()
	connection.SetDeadline(time.Now().Add(20 * time.Second))
	request := map[string]any{"api": 1, "client": "adguard-home", "operation": operation, "resolver": map[string]int{"port": dnsPort}}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return response, err
	}

	if json.NewDecoder(connection).Decode(&response) != nil || !response.OK {
		return ProviderResponse{}, errors.New("DNS provider rejected request")
	}

	return response, nil
}

func privateAPI(path string) (map[string]any, error) {
	if path != "/control/status" && path != "/control/stats" && path != "/control/filtering/status" && path != "/control/filtering/refresh" {
		return nil, errors.New("unsupported private action")
	}

	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(runDir, "control.sock"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	var response *http.Response
	var err error
	if path == "/control/filtering/refresh" {
		client.Timeout = 5 * time.Minute
		response, err = client.Post("http://private"+path, "application/json", bytes.NewBufferString(`{"whitelist":false}`))
	} else {
		response, err = client.Get("http://private" + path)
	}
	if err != nil {
		return nil, errors.New("AdGuard control unavailable")
	}

	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("AdGuard control request failed")
	}

	var result map[string]any
	err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&result)
	return result, err
}

func statistics() (map[string]any, error) {
	raw, err := privateAPI("/control/stats")
	if err != nil {
		return nil, err
	}

	safe := map[string]any{}
	for _, key := range []string{"num_dns_queries", "num_blocked_filtering", "avg_processing_time"} {
		if value, valid := raw[key].(float64); valid {
			safe[key] = value
		}
	}

	return safe, nil
}
