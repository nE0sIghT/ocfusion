// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type ScanRequest struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

type ProbeResult struct {
	Preset       string `json:"preset"`
	Success      int    `json:"success"`
	Attempts     int    `json:"attempts"`
	Milliseconds int64  `json:"milliseconds"`
	Error        string `json:"error,omitempty"`
	Status       int    `json:"http_status,omitempty"`
}

type ScanState struct {
	ScanRequest
	State   string        `json:"state"`
	Results []ProbeResult `json:"results"`
	Current string        `json:"current,omitempty"`
	Error   string        `json:"error,omitempty"`
}

func scanPath(name string) string {
	return filepath.Join(runDir, "scan-"+name)
}

func scanResources() string {
	return scanPath("resources.json")
}

func scanStatus() ScanState {
	var state ScanState
	data, err := os.ReadFile(scanPath("status.json"))
	if err != nil || len(data) > 8192 || json.Unmarshal(data, &state) != nil {
		state.State = "IDLE"
	}
	return state
}

func resetScan() {
	state := scanStatus()
	if state.State == "RUNNING" || state.State == "QUEUED" {
		state.State = "CANCELLED"
		writeJSON(scanPath("status.json"), state)
	}
	os.Remove(scanPath("request.json"))
	os.Remove(scanPath("cancel"))
	os.Remove(scanPath("lock"))
}

func decodeScan(data []byte, result any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 1024 || decoder.Decode(result) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid scan request")
	}
	return nil
}

func requestScan(data []byte) (ScanState, error) {
	var request ScanRequest
	if err := decodeScan(data, &request); err != nil {
		return ScanState{}, err
	}
	c, err := loadConfig()
	request.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(request.Host), "."))
	if err != nil || request.Revision != c.Revision || request.ID != "" || len(request.Host) > 253 || !domainPattern.MatchString(request.Host) || net.ParseIP(request.Host) != nil || !containsPort(c.TCPPorts, request.Port) {
		return ScanState{}, errors.New("choose a domain and TCP port from the saved destinations")
	}
	// A live daemon owns the scan and all of its child processes.
	status, err := os.Stat(scanPath("service.json"))
	if err != nil || time.Since(status.ModTime()) > 15*time.Second {
		return ScanState{}, errors.New("module service unavailable")
	}
	if _, recoveryErr := os.Stat(scanResources()); recoveryErr == nil {
		return ScanState{}, errors.New("temporary resources require cleanup")
	}
	if err = os.Mkdir(scanPath("lock"), 0700); err != nil {
		return ScanState{}, errors.New("a scan is already running")
	}
	token := make([]byte, 12)
	if _, err = rand.Read(token); err != nil {
		os.Remove(scanPath("lock"))
		return ScanState{}, err
	}
	request.ID = hex.EncodeToString(token)
	state := ScanState{ScanRequest: request, State: "QUEUED", Results: []ProbeResult{}}
	os.Remove(scanPath("cancel"))
	if err = writeJSON(scanPath("status.json"), state); err == nil {
		err = writeJSON(scanPath("request.json"), request)
	}
	if err != nil {
		os.Remove(scanPath("lock"))
	}
	return state, err
}

func containsPort(ports []int, wanted int) bool {
	for _, port := range ports {
		if port == wanted {
			return true
		}
	}
	return false
}

func domainSelected(c Config, host string) bool {
	for _, domain := range c.Domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func addressSelected(c Config, address netip.Addr) bool {
	for _, value := range c.Networks {
		prefix, err := netip.ParsePrefix(value)
		if err == nil && prefix.Contains(address) {
			return true
		}
	}
	return false
}

func pendingScan() (ScanRequest, bool) {
	var request ScanRequest
	data, err := os.ReadFile(scanPath("request.json"))
	if err != nil || decodeScan(data, &request) != nil {
		return request, false
	}
	os.Remove(scanPath("request.json"))
	return request, true
}

func cancelRequestedScan(data []byte) error {
	var request struct {
		ID string `json:"id"`
	}
	if decodeScan(data, &request) != nil || request.ID == "" || request.ID != scanStatus().ID {
		return errors.New("scan changed; refresh before cancelling")
	}
	return os.WriteFile(scanPath("cancel"), []byte(request.ID), 0600)
}

func scanCancelled(c Config) bool {
	state := scanStatus()
	data, _ := os.ReadFile(scanPath("cancel"))
	return state.Revision != c.Revision || string(data) == state.ID
}

func scanRules(r Resources, iface, address string, port, source int, bypass string) []Rule {
	mark := []string{"-m", "mark", "!", "--mark", r.Mark + "/" + r.Mark}
	queue := append(mark, "-m", "comment", "--comment", "ocfusion:zapret2-scan", "-j", "NFQUEUE", "--queue-num", strconv.Itoa(r.Queue), "--queue-bypass")
	outgoing := []string{"-o", iface, "-p", "tcp", "-d", address, "--dport", strconv.Itoa(port), "--sport", strconv.Itoa(source)}
	incoming := []string{"-i", iface, "-p", "tcp", "-s", address, "--sport", strconv.Itoa(port), "--dport", strconv.Itoa(source)}
	rules := []Rule{{Tool: "iptables", Chain: "OUTPUT", Args: append(outgoing, queue...)}}
	if bypass != "" {
		// Mark only this reply tuple before the regular module's PREROUTING rules.
		args := append(append([]string{}, incoming...), "-m", "comment", "--comment", "ocfusion:zapret2-scan", "-j", "MARK", "--set-xmark", bypass+"/"+bypass)
		rules = append(rules, Rule{Tool: "iptables", Chain: "PREROUTING", Args: args})
	}
	rules = append(rules, Rule{Tool: "iptables", Chain: "PREROUTING", Args: append(incoming, queue...)})
	return rules
}

func routeForScan(address, iface string) (string, error) {
	output, err := execute("ip", "-4", "route", "get", address, "oif", iface)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	device, source := "", ""
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "dev" {
			device = fields[i+1]
		}
		if fields[i] == "src" {
			source = fields[i+1]
		}
	}
	if device != iface || net.ParseIP(source).To4() == nil {
		return "", errors.New("direct WAN route unavailable")
	}
	return source, nil
}

func performScan(ctx context.Context, request ScanRequest, c Config, bypass string) (failure error) {
	state := ScanState{ScanRequest: request, State: "RUNNING", Results: []ProbeResult{}}
	defer func() {
		cleanupErr := cleanupAt(scanResources(), "ocfusion:zapret2-scan")
		if cleanupErr != nil {
			failure = errors.Join(failure, cleanupErr)
		}
		state.Current = ""
		state.State = "COMPLETE"
		if failure != nil {
			state.State = "ERROR"
			if state.Error == "" {
				state.Error = "Test could not be completed"
			}
		}
		if ctx.Err() != nil {
			state.State, state.Error = "CANCELLED", ""
		}
		if cleanupErr != nil {
			state.State, state.Error = "ERROR", "Temporary rule cleanup failed"
		}
		writeJSON(scanPath("status.json"), state)
		os.Remove(scanPath("lock"))
	}()
	writeJSON(scanPath("status.json"), state)
	if request.Revision != c.Revision || !containsPort(c.TCPPorts, request.Port) {
		return errors.New("settings changed")
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	addresses, err := net.DefaultResolver.LookupNetIP(resolveCtx, "ip4", request.Host)
	cancel()
	if err != nil {
		state.Error = "DNS lookup failed"
		return err
	}
	address := ""
	for _, value := range addresses {
		value = value.Unmap()
		// Never use a hostname test to access LAN, loopback or link-local services.
		if value.Is4() && value.IsGlobalUnicast() && !value.IsPrivate() && (domainSelected(c, request.Host) || addressSelected(c, value)) {
			address = value.String()
			break
		}
	}
	if address == "" {
		state.Error = "No allowed public IPv4 address"
		return errors.New("no public IPv4 destination in the saved selection")
	}
	iface, err := wan("-4")
	if err != nil {
		state.Error = "Direct WAN route unavailable"
		return err
	}
	source, err := routeForScan(address, iface)
	if err != nil {
		state.Error = "Direct WAN route unavailable"
		return err
	}
	candidates := append([]Preset{{ID: "baseline", Name: "Without processing", Protocol: "tcp"}}, presets()...)
	for _, preset := range candidates {
		if preset.Protocol != "tcp" {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		state.Current = preset.ID
		writeJSON(scanPath("status.json"), state)
		result := ProbeResult{Preset: preset.ID}
		for attempt := 0; attempt < 2; attempt++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			trialCtx, trialCancel := context.WithTimeout(ctx, 7*time.Second)
			elapsed, status, probeErr := probeHTTPS(trialCtx, request, c, preset, address, iface, source, bypass)
			trialCancel()
			if errors.Is(probeErr, errScanSetup) {
				state.Error = "Test packet processing unavailable"
				return probeErr
			}
			result.Attempts++
			if probeErr == nil {
				result.Success++
				result.Milliseconds += elapsed.Milliseconds()
				result.Status = status
			} else {
				result.Error = "Connection or HTTPS response failed"
			}
			// A failed cleanup must stop enumeration, not carry stale rules forward.
			if _, statErr := os.Stat(scanResources()); statErr == nil {
				return errors.New("temporary resources remain")
			}
		}
		if result.Success > 0 {
			result.Milliseconds /= int64(result.Success)
		}
		state.Results = append(state.Results, result)
		writeJSON(scanPath("status.json"), state)
	}
	return nil
}

func probeHTTPS(ctx context.Context, request ScanRequest, base Config, preset Preset, address, iface, source, bypass string) (time.Duration, int, error) {
	var worker *exec.Cmd
	var workerDone chan error
	defer func() {
		// Remove packet interception before terminating the queue consumer.
		cleanupAt(scanResources(), "ocfusion:zapret2-scan")
		if worker != nil {
			worker.Process.Kill()
			<-workerDone
		}
	}()
	started := time.Now()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{ServerName: request.Host, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	transport.DialContext = func(dialCtx context.Context, network, ignored string) (net.Conn, error) {
		dialer := net.Dialer{Control: func(network, destination string, raw syscall.RawConn) error {
			var setupErr error
			controlErr := raw.Control(func(fd uintptr) {
				var port int
				port, setupErr = bindProbeSocket(int(fd), iface, source, bypass)
				if setupErr == nil {
					worker, workerDone, setupErr = startProbeWorker(ctx, request, base, preset, address, iface, port, bypass)
				}
			})
			started = time.Now()
			if controlErr != nil || setupErr != nil {
				return errors.Join(errScanSetup, controlErr, setupErr)
			}
			return nil
		}}
		return dialer.DialContext(dialCtx, "tcp4", net.JoinHostPort(address, strconv.Itoa(request.Port)))
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	url := "https://" + net.JoinHostPort(request.Host, strconv.Itoa(request.Port)) + "/"
	query, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	query.Header.Set("User-Agent", "OCFusion-connectivity-test")
	response, err := client.Do(query)
	if err != nil {
		return 0, 0, err
	}
	defer response.Body.Close()
	if worker != nil {
		select {
		case workerErr := <-workerDone:
			workerDone <- workerErr
			return 0, 0, errScanSetup
		default:
		}
	}
	// No response body is downloaded; a verified TLS handshake plus HTTP headers is the
	// result, not a claim about video throughput, HTTP/3 or all site resources.
	return time.Since(started), response.StatusCode, nil
}
