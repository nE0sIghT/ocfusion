// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func TestDiscoveryTargetsAndRefinementBounds(t *testing.T) {
	for _, target := range []ScanTarget{
		{Host: "example.com", Port: 443, Protocol: "http3", Path: "/file?test=1"},
		{Host: "stun.example.com", Port: 3478, Protocol: "stun"},
	} {
		request := ScanRequest{Targets: []ScanTarget{target}}
		if err := normalizeScanTargets(&request); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []ScanTarget{
		{Host: "127.0.0.1", Port: 443, Protocol: "https"},
		{Host: "example.com", Port: 443, Protocol: "ftp"},
		{Host: "example.com", Port: 443, Protocol: "https", Path: "//other.example/path"},
		{Host: "example.com", Port: 443, Protocol: "https", Path: "https://other.example/"},
		{Host: "example.com", Port: 3478, Protocol: "stun", Path: "/path"},
	} {
		request := ScanRequest{Targets: []ScanTarget{target}}
		if normalizeScanTargets(&request) == nil {
			t.Fatal(target)
		}
	}
	state := ScanState{ScanRequest: ScanRequest{Targets: make([]ScanTarget, 4)}}
	for i := 0; i < scanResultLimit; i++ {
		result := ProbeResult{Preset: strings.Repeat("a", 32), Name: strings.Repeat("b", 70), Protocol: "tcp", Details: make([]TargetProbe, 4), Error: "Some targets failed"}
		for j := range result.Details {
			result.Details[j] = TargetProbe{Target: j, Stage: "short-body", Bytes: 65536, Milliseconds: 9000, Status: 206}
		}
		state.Results = append(state.Results, result)
	}
	encoded, _ := json.Marshal(state)
	if len(encoded) > 14500 {
		t.Fatal("public status exceeds budget", len(encoded))
	}
	for _, protocol := range []string{"tcp", "udp", "stun"} {
		for _, seed := range scanCandidates(protocol)[1:] {
			for _, mode := range []string{"refine", "lighten"} {
				candidates := refinedCandidates(seed, mode)
				if len(candidates) > 18 || len(candidates) < 2 {
					t.Fatal(len(candidates))
				}
				for _, candidate := range candidates[1:] {
					c := presetConfig(defaultConfig(), candidate)
					data, _ := json.Marshal(c)
					if _, err := parseConfig(data); err != nil {
						t.Fatalf("%s %s: %v", seed.ID, candidate.ID, err)
					}
				}
			}
		}
	}
}

func TestSTUNRejectsUnrelatedOrTruncatedDatagrams(t *testing.T) {
	request, err := stunBindingRequest()
	if err != nil {
		t.Fatal(err)
	}
	response := append([]byte{}, request...)
	binary.BigEndian.PutUint16(response, 0x0101)
	binary.BigEndian.PutUint16(response[2:], 12)
	response = append(response, 0, 0x20, 0, 8, 0, 1, 0x12, 0x34, 1, 2, 3, 4)
	if !validSTUNResponse(request, response) {
		t.Fatal("valid binding rejected")
	}
	response[8] ^= 1
	if validSTUNResponse(request, response) {
		t.Fatal("wrong transaction accepted")
	}
	response[8] ^= 1
	if validSTUNResponse(request, response[:len(response)-1]) {
		t.Fatal("truncated packet accepted")
	}
}

func configureProbeFixture(t *testing.T, certificate tls.Certificate) {
	t.Helper()
	previousRun, previousLaunch, previousBind, previousTLS, previousUDP := runDir, launchProbeWorker, bindScanSocket, probeTLSConfig, openProbePacket
	runDir = t.TempDir()
	t.Cleanup(func() {
		runDir, launchProbeWorker, bindScanSocket, probeTLSConfig, openProbePacket = previousRun, previousLaunch, previousBind, previousTLS, previousUDP
	})
	launchProbeWorker = func(context.Context, ScanTarget, Config, Preset, string, string, int, string) (*exec.Cmd, chan error, error) {
		return nil, nil, nil
	}
	bindScanSocket = func(fd int, _, _, _ string) (int, error) {
		if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
			return 0, err
		}
		local, err := syscall.Getsockname(fd)
		if err != nil {
			return 0, err
		}
		return local.(*syscall.SockaddrInet4).Port, nil
	}
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(parsed)
	probeTLSConfig = func(string) *tls.Config {
		return &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12}
	}
	openProbePacket = func(context.Context, resolvedScanTarget, string) (net.PacketConn, error) {
		return net.ListenPacket("udp4", "127.0.0.1:0")
	}
}

func TestHTTPSProbeReadsPastCutoffAndDetectsTruncation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", "65536")
		if request.URL.Path == "/cut" {
			io.WriteString(writer, strings.Repeat("x", 17000))
			return
		}
		io.WriteString(writer, strings.Repeat("x", 65536))
	}))
	defer server.Close()
	configureProbeFixture(t, server.TLS.Certificates[0])
	_, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	for _, path := range []string{"/full", "/cut"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result, err := performProtocolProbe(ctx, ScanTarget{Host: "example.com", Port: port, Protocol: "https", Path: path}, defaultConfig(), Preset{ID: "baseline"}, resolvedScanTarget{Address: "127.0.0.1"}, "")
		cancel()
		if path == "/full" && (err != nil || result.Bytes != 65536 || result.Stage != "complete") {
			t.Fatal(result, err)
		}
		if path == "/cut" && (err == nil || result.Bytes != 17000 || result.Stage != "body") {
			t.Fatal(result, err)
		}
	}
}

func TestHTTP3ProbeUsesRealQUICAndReadsBody(t *testing.T) {
	certificateSource := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := certificateSource.TLS.Certificates[0]
	certificateSource.Close()
	configureProbeFixture(t, certificate)
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := http3.Server{TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}}, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, strings.Repeat("h", 65536)) })}
	done := make(chan error, 1)
	go func() { done <- server.Serve(packet) }()
	defer func() { server.Close(); <-done; packet.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := performProtocolProbe(ctx, ScanTarget{Host: "example.com", Port: packet.LocalAddr().(*net.UDPAddr).Port, Protocol: "http3", Path: "/"}, defaultConfig(), Preset{ID: "baseline"}, resolvedScanTarget{Address: "127.0.0.1"}, "")
	if err != nil || result.Bytes != 65536 || result.Stage != "complete" {
		t.Fatal(result, err)
	}
}

func TestProbeDoesNotAcceptCertificateFailure(t *testing.T) {
	response := httptest.NewTLSServer(http.NotFoundHandler())
	defer response.Close()
	configureProbeFixture(t, response.TLS.Certificates[0])
	probeTLSConfig = func(string) *tls.Config {
		return &tls.Config{RootCAs: x509.NewCertPool(), ServerName: "example.com", MinVersion: tls.VersionTLS12}
	}
	_, portText, _ := net.SplitHostPort(response.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := performProtocolProbe(ctx, ScanTarget{Host: "example.com", Port: port, Protocol: "https", Path: "/"}, defaultConfig(), Preset{ID: "baseline"}, resolvedScanTarget{Address: "127.0.0.1"}, "")
	if err == nil || errors.Is(err, errScanSetup) || result.Stage != "tls" {
		t.Fatal(result, err)
	}
}

func TestSTUNProbeRoundTrip(t *testing.T) {
	certificateSource := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := certificateSource.TLS.Certificates[0]
	certificateSource.Close()
	configureProbeFixture(t, certificate)
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	done := make(chan error, 1)
	go func() {
		packet := make([]byte, 512)
		count, source, err := socket.ReadFrom(packet)
		if err != nil {
			done <- err
			return
		}
		if count != 20 {
			done <- errors.New("unexpected binding request")
			return
		}
		reply := append([]byte{}, packet[:count]...)
		binary.BigEndian.PutUint16(reply, 0x0101)
		binary.BigEndian.PutUint16(reply[2:], 12)
		reply = append(reply, 0, 0x20, 0, 8, 0, 1, 0x12, 0x34, 1, 2, 3, 4)
		_, err = socket.WriteTo(reply, source)
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := performProtocolProbe(ctx, ScanTarget{Host: "example.com", Port: socket.LocalAddr().(*net.UDPAddr).Port, Protocol: "stun"}, defaultConfig(), Preset{ID: "baseline"}, resolvedScanTarget{Address: "127.0.0.1"}, "")
	if err != nil || result.Stage != "complete" || result.Bytes != 32 {
		t.Fatal(result, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRefinementUsesPrivateResultAndKeepsOriginalTarget(t *testing.T) {
	previous := runDir
	runDir = t.TempDir()
	defer func() { runDir = previous }()
	c := defaultConfig()
	c.Revision = 3
	configPath := runDir + "/config.json"
	t.Setenv("EXT_CONFIG_PATH", configPath)
	if err := writeJSON(configPath, c); err != nil {
		t.Fatal(err)
	}
	seed := scanCandidates("tcp")[1]
	state := ScanState{ScanRequest: ScanRequest{ID: "prior", Revision: 3, Targets: []ScanTarget{{Host: "example.com", Port: 443, Protocol: "https", Path: "/large?test=1"}}}, State: "COMPLETE", Results: []ProbeResult{{Preset: "tcp:split"}}}
	for name, value := range map[string]any{"status.json": state, "candidates.json": map[string]Preset{"tcp:split": seed}, "service.json": map[string]string{"state": "DISABLED"}} {
		if err := writeJSON(scanPath(name), value); err != nil {
			t.Fatal(err)
		}
	}
	if publicScanStatus().Targets[0].Path != "" {
		t.Fatal("HTTP path in public polling status")
	}
	for _, request := range []string{
		`{"revision":3,"mode":"refine","parent":"stale","from":"tcp:split"}`,
		`{"revision":3,"mode":"refine","parent":"prior","from":"tcp:missing"}`,
		`{"revision":3,"mode":"refine","parent":"prior","from":"tcp:split","seed":{}}`,
	} {
		if _, err := requestScan([]byte(request)); err == nil {
			t.Fatal("invalid seed accepted", request)
		}
	}
	next, err := requestScan([]byte(`{"revision":3,"mode":"refine","parent":"prior","from":"tcp:split"}`))
	if err != nil || next.Targets[0].Path != "" {
		t.Fatal(next, err)
	}
	pending, ok := pendingScan()
	if !ok || pending.seed == nil || pending.seed.ID != seed.ID || pending.Targets[0].Path != "/large?test=1" {
		t.Fatal(pending, ok)
	}
}

func TestUDPDiscoveryRulesHaveExactTupleAndNoTCPMatch(t *testing.T) {
	for _, rule := range scanRulesForProtocol(Resources{Queue: 202, Mark: "0x20000000"}, "ppp0", "203.0.113.10", 3478, 45231, "0x40000000", "udp") {
		text := strings.Join(rule.Args, " ")
		if !strings.Contains(text, "-p udp") || !strings.Contains(text, "3478") || !strings.Contains(text, "45231") || strings.Contains(text, "-p tcp") {
			t.Fatal(text)
		}
	}
}

func TestPublishedDiscoveryStatusStaysWithinPlatformLimit(t *testing.T) {
	previous := runDir
	runDir = t.TempDir()
	defer func() { runDir = previous }()
	state := ScanState{ScanRequest: ScanRequest{ID: strings.Repeat("a", 24), Revision: 1}, State: "COMPLETE"}
	for i := 0; i < 4; i++ {
		state.Targets = append(state.Targets, ScanTarget{Host: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 57), Port: 65535, Protocol: "https", Path: "/" + strings.Repeat("x", 511)})
	}
	for i := 0; i < scanResultLimit; i++ {
		result := ProbeResult{Preset: strings.Repeat("a", 30), Name: strings.Repeat("b", 70), Protocol: "tcp", Error: "Some targets failed"}
		for j := 0; j < 4; j++ {
			result.Details = append(result.Details, TargetProbe{Target: j, Stage: "short-body", Bytes: 65536, Milliseconds: 9000, Status: 206})
		}
		state.Results = append(state.Results, result)
	}
	if err := writeJSON(scanPath("status.json"), state); err != nil {
		t.Fatal(err)
	}
	lists := []ServiceStatus{}
	for _, service := range serviceCatalog() {
		lists = append(lists, ServiceStatus{ID: service.ID, State: "READY", Updated: 1793000000, Domains: 50000, Networks: 50000})
	}
	if err := writeJSON(runDir+"/list-status.json", lists); err != nil {
		t.Fatal(err)
	}
	path := runDir + "/public.json"
	t.Setenv("EXT_PUBLIC_STATUS_PATH", path)
	publish("RUNNING")
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 16384 {
		t.Fatal("platform status too large", len(data), err)
	}
	if !bytes.Contains(data, []byte(`"results"`)) || bytes.Contains(data, []byte(strings.Repeat("x", 511))) {
		t.Fatal("discovery status lost or paths leaked")
	}
}

func TestHTTPSProbeCanSelectTLS12AndNegotiateHTTP2(t *testing.T) {
	seen := make(chan struct {
		Version  uint16
		Protocol int
	}, 2)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- struct {
			Version  uint16
			Protocol int
		}{r.TLS.Version, r.ProtoMajor}
		io.WriteString(w, strings.Repeat("h", 65536))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	configureProbeFixture(t, server.TLS.Certificates[0])
	_, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	for _, protocol := range []string{"https", "https12"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result, err := performProtocolProbe(ctx, ScanTarget{Host: "example.com", Port: port, Protocol: protocol, Path: "/"}, defaultConfig(), Preset{ID: "baseline"}, resolvedScanTarget{Address: "127.0.0.1"}, "")
		cancel()
		if err != nil || result.Bytes != 65536 {
			t.Fatal(result, err)
		}
		observed := <-seen
		if observed.Protocol != 2 {
			t.Fatal("HTTP/2 not negotiated", observed)
		}
		if protocol == "https12" && observed.Version != tls.VersionTLS12 {
			t.Fatal("TLS 1.2 not selected", observed)
		}
	}
}

func TestSelectedResultReproducesTestedArguments(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp", "stun"} {
		for _, preset := range scanCandidates(protocol)[1:] {
			applied := defaultConfig()
			applied.TTL, applied.Repeats, applied.AutoDelta, applied.AutoMin, applied.AutoMax = 19, 17, -7, 8, 30
			applied.Fooling, applied.Positions, applied.FakeSNI = "md5", "17", "example.net"
			data, err := json.Marshal(preset.Settings)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &applied); err != nil {
				t.Fatal(err)
			}
			applied.Networks, applied.Domains = []string{"203.0.113.1/32"}, nil
			applied.TCPPorts, applied.UDPPorts = []int{443}, []int{443}
			target := ScanTarget{Protocol: "https", Port: 443}
			if protocol == "tcp" {
				applied.UDP = "off"
				applied.Applications = nil
			}
			if protocol == "udp" {
				target.Protocol = "http3"
				applied.TCP = "off"
				applied.Applications = nil
			}
			if protocol == "stun" {
				target.Protocol = "stun"
				applied.TCP, applied.UDP = "off", "off"
				for i := range applied.Applications {
					applied.Applications[i].Ports = "443"
				}
			}
			expected := probeWorkerConfig(defaultConfig(), preset, target, "203.0.113.1")
			resource := Resources{Queue: 202, Mark: "0x40000000"}
			actualArgs := strings.Join(workerArgs(applied, resource, "/worker", 62000), " ")
			wantedArgs := strings.Join(workerArgs(expected, resource, "/worker", 62000), " ")
			if actualArgs != wantedArgs {
				t.Fatalf("%s selected settings differ from tested arguments\n%s\n%s", preset.ID, actualArgs, wantedArgs)
			}
		}
	}
}
