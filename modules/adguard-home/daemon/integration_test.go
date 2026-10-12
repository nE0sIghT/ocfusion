// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceBuiltAdGuardFilteringAndPrivateControl(t *testing.T) {
	executable := os.Getenv("OCF_ADGUARD_BINARY")
	if executable == "" {
		t.Skip("source-built AdGuard executable not supplied")
	}

	backend, err := net.ListenPacket("udp", "127.0.0.1:53")
	if err != nil {
		t.Skip("native DNS fixture port unavailable")
	}

	defer backend.Close()
	var forwarded atomic.Int32
	go func() {
		packet := make([]byte, 4096)
		for {
			size, address, err := backend.ReadFrom(packet)
			if err != nil {
				return
			}

			response := append([]byte(nil), packet[:size]...)
			response[2] |= 0x80
			response[3] |= 0x80
			forwarded.Add(1)
			backend.WriteTo(response, address)
		}
	}()

	oldRunDir := runDir
	runDir = t.TempDir()
	t.Cleanup(func() {
		runDir = oldRunDir
	})
	payload := t.TempDir()
	os.MkdirAll(filepath.Join(payload, "bin"), 0700)
	if err := os.Symlink(executable, filepath.Join(payload, "bin", "AdGuardHome")); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EXT_PAYLOAD_PATH", payload)
	worker, err := startWorker(Config{Enabled: true, Rules: []string{"||blocked.example^"}}, Registration{Networks: []Network{{Bridge: "br0", Address: "127.0.0.1", CIDR: "127.0.0.0/8"}}})
	if err != nil {
		data, _ := os.ReadFile(filepath.Join(runDir, "worker.log"))
		t.Fatalf("%v: %s", err, data)
	}

	defer func() { stopWorker(worker) }()
	if info, err := os.Stat(filepath.Join(runDir, "control.sock")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private socket permissions", err)
	}

	query := func(domain string) []byte {
		t.Helper()
		packet := make([]byte, 12)
		packet[0] = 1
		packet[2] = 1
		packet[5] = 1
		start := 0
		for i := 0; i <= len(domain); i++ {
			if i == len(domain) || domain[i] == '.' {
				packet = append(packet, byte(i-start))
				packet = append(packet, domain[start:i]...)
				start = i + 1
			}
		}

		packet = append(packet, 0, 0, 1, 0, 1)
		connection, err := net.Dial("udp", "127.0.0.1:53535")
		if err != nil {
			t.Fatal(err)
		}

		defer connection.Close()
		connection.SetDeadline(time.Now().Add(3 * time.Second))
		connection.Write(packet)
		response := make([]byte, 4096)
		size, err := connection.Read(response)
		if err != nil {
			t.Fatal(err)
		}

		return response[:size]
	}

	allowed := query("local.example")
	if len(allowed) < 12 || allowed[3]&15 != 0 || binary.BigEndian.Uint16(allowed[4:6]) != 1 {
		t.Fatal("native backend response not preserved")
	}

	before := forwarded.Load()
	blocked := query("blocked.example")
	if len(blocked) < 12 || blocked[3]&15 != 3 || forwarded.Load() != before {
		t.Fatal("filter did not prevent backend query", blocked)
	}

	if _, err := statistics(); err != nil {
		t.Fatal("private statistics", err)
	}

	// Exercise a real TLS upstream, with an explicit IP and the original SNI.
	stopWorker(worker)
	worker = nil
	var httpsQueries atomic.Int32
	httpsDNS := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.TLS.ServerName != "example.com" {
			t.Error("upstream TLS hostname was not retained")
		}
		packet, err := io.ReadAll(io.LimitReader(request.Body, 4096))
		if request.Method == http.MethodGet {
			packet, err = base64.RawURLEncoding.DecodeString(request.URL.Query().Get("dns"))
		}
		if err != nil || len(packet) < 12 {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		packet[2] |= 0x80
		packet[3] |= 0x80
		httpsQueries.Add(1)
		response.Header().Set("Content-Type", "application/dns-message")
		response.Write(packet)
	}))
	defer httpsDNS.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: httpsDNS.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(payload, "ca-bundle.crt"), certificate, 0600); err != nil {
		t.Fatal(err)
	}
	port := strings.Split(httpsDNS.Listener.Addr().String(), ":")[1]
	worker, err = startWorker(Config{Enabled: true, Upstreams: []Upstream{{URL: "https://example.com:" + port + "/dns-query", Address: "127.0.0.1"}}}, Registration{Networks: []Network{{Bridge: "br0", Address: "127.0.0.1", CIDR: "127.0.0.0/8"}}})
	if err != nil {
		t.Fatal(err)
	}
	answer := query("doh.example")
	if answer[3]&15 != 0 || httpsQueries.Load() == 0 {
		data, _ := os.ReadFile(filepath.Join(runDir, "worker.log"))
		t.Fatalf("custom DoH did not reach verified TLS upstream: %v: %s", answer, data)
	}
}

func TestSourceBuiltAdGuardFilterDownloadsKeepWorkingCopyAfterFailure(t *testing.T) {
	executable := os.Getenv("OCF_ADGUARD_BINARY")
	if executable == "" {
		t.Skip("source-built AdGuard executable not supplied")
	}

	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		io.WriteString(w, "||blocked-by-list.example^\n")
	}))
	defer server.Close()

	previous := runDir
	runDir = t.TempDir()
	t.Cleanup(func() { runDir = previous })
	payload := t.TempDir()
	os.MkdirAll(filepath.Join(payload, "bin"), 0700)
	if err := os.Symlink(executable, filepath.Join(payload, "bin", "AdGuardHome")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXT_PAYLOAD_PATH", payload)
	worker, err := startWorker(Config{Enabled: true, Filters: []string{server.URL}}, Registration{Networks: []Network{{Bridge: "br0", Address: "127.0.0.1", CIDR: "127.0.0.0/8"}}})
	if err != nil {
		log, _ := os.ReadFile(filepath.Join(runDir, "worker.log"))
		t.Fatalf("%v: %s", err, log)
	}
	defer stopWorker(worker)

	var filter map[string]any
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		raw, readErr := privateAPI("/control/filtering/status")
		if readErr == nil {
			items, _ := raw["filters"].([]any)
			if len(items) == 1 {
				filter, _ = items[0].(map[string]any)
				if count, _ := filter["rules_count"].(float64); count > 0 {
					break
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	if filter == nil || filter["rules_count"] != float64(1) {
		t.Fatalf("list did not load automatically: %v", filter)
	}
	assertBlocked := func(domain string) {
		t.Helper()
		connection, err := net.DialTimeout("udp", "127.0.0.1:53535", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(2 * time.Second))
		packet := make([]byte, 12)
		packet[0], packet[2], packet[5] = 1, 1, 1
		for _, label := range strings.Split(domain, ".") {
			packet = append(packet, byte(len(label)))
			packet = append(packet, label...)
		}
		packet = append(packet, 0, 0, 1, 0, 1)
		connection.Write(packet)
		answer := make([]byte, 512)
		n, err := connection.Read(answer)
		if err != nil || n < 12 || answer[3]&15 != 3 {
			t.Fatalf("list did not block %s: %v, %v", domain, answer[:n], err)
		}
	}
	assertBlocked("before.blocked-by-list.example")
	last := filter["last_updated"]
	fail.Store(true)
	if _, err := privateAPI("/control/filtering/refresh"); err != nil {
		t.Fatal(err)
	}
	raw, err := privateAPI("/control/filtering/status")
	if err != nil {
		t.Fatal(err)
	}
	filter = raw["filters"].([]any)[0].(map[string]any)
	if filter["update_failed"] != true || filter["rules_count"] != float64(1) || filter["last_updated"] != last {
		t.Fatalf("failed refresh lost list or success timestamp: %v", filter)
	}
	assertBlocked("after.blocked-by-list.example")
}
