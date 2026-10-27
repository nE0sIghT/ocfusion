// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

var launchProbeWorker = startProbeWorker
var bindScanSocket = bindProbeSocket
var probeTLSConfig = func(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

type probeSession struct {
	Worker  *exec.Cmd
	Done    chan error
	Started time.Time
}

func (session *probeSession) close() {
	cleanupAt(scanResources(), "ocfusion:zapret2-scan")
	if session.Worker != nil {
		session.Worker.Process.Kill()
		<-session.Done
	}
}

func (session *probeSession) start(ctx context.Context, target ScanTarget, base Config, preset Preset, destination resolvedScanTarget, port int, bypass string) error {
	var err error
	if session.Worker != nil {
		return errScanSetup
	}
	session.Worker, session.Done, err = launchProbeWorker(ctx, target, base, preset, destination.Address, destination.Interface, port, bypass)
	session.Started = time.Now()
	if err != nil {
		return errors.Join(errScanSetup, err)
	}
	return nil
}

func (session *probeSession) alive() bool {
	if session.Worker == nil {
		return true
	}
	select {
	case err := <-session.Done:
		session.Done <- err
		return false
	default:
		return true
	}
}

func probeDialer(ctx context.Context, session *probeSession, target ScanTarget, base Config, preset Preset, destination resolvedScanTarget, bypass string) *net.Dialer {
	return &net.Dialer{Control: func(network, address string, raw syscall.RawConn) error {
		var setupErr error
		err := raw.Control(func(fd uintptr) {
			port, bindErr := bindScanSocket(int(fd), destination.Interface, destination.Source, bypass)
			setupErr = bindErr
			if setupErr == nil {
				setupErr = session.start(ctx, target, base, preset, destination, port, bypass)
			}
		})
		if err != nil || setupErr != nil {
			return errors.Join(errScanSetup, err, setupErr)
		}
		return nil
	}}
}

func readProbeResponse(response *http.Response, result *TargetProbe) error {
	defer response.Body.Close()
	result.Status = response.StatusCode
	result.Stage = "body"
	// Read past common 16-20 KiB cutoffs, while bounding data per attempt.
	count, err := io.Copy(io.Discard, io.LimitReader(response.Body, scanBodyLimit))
	result.Bytes = count
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		result.Stage = "http-status"
		return errors.New("server rejected probe")
	}
	if count < scanBodyLimit {
		result.Stage = "short-body"
	} else {
		result.Stage = "complete"
	}
	return nil
}

func probeURL(target ScanTarget) string {
	scheme := "https"
	if target.Protocol == "http" {
		scheme = "http"
	}
	return scheme + "://" + net.JoinHostPort(target.Host, strconv.Itoa(target.Port)) + target.Path
}

func probeRequest(ctx context.Context, target ScanTarget, stage *atomic.Value) (*http.Request, error) {
	trace := &httptrace.ClientTrace{
		ConnectStart:      func(string, string) { stage.Store("connect") },
		ConnectDone:       func(string, string, error) { stage.Store("tls") },
		TLSHandshakeStart: func() { stage.Store("tls") },
		GotConn:           func(httptrace.GotConnInfo) { stage.Store("headers") },
	}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, probeURL(target), nil)
	if err == nil {
		request.Header.Set("User-Agent", "OCFusion-connectivity-test")
		request.Header.Set("Accept-Encoding", "identity")
		request.Header.Set("Range", "bytes=0-65535")
	}
	return request, err
}

func noProbeRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func performProtocolProbe(ctx context.Context, target ScanTarget, base Config, preset Preset, destination resolvedScanTarget, bypass string) (result TargetProbe, failure error) {
	session := &probeSession{Started: time.Now()}
	defer session.close()
	defer func() {
		result.Milliseconds = time.Since(session.Started).Milliseconds()
		if !session.alive() {
			failure = errScanSetup
		}
	}()
	if target.Protocol == "stun" {
		return probeSTUN(ctx, session, target, base, preset, destination, bypass)
	}
	if target.Protocol == "http3" {
		return probeHTTP3(ctx, session, target, base, preset, destination, bypass)
	}
	result.Stage = "connect"
	tlsConfig := probeTLSConfig(target.Host)
	if target.Protocol == "https12" {
		tlsConfig.MaxVersion = tls.VersionTLS12
	}
	transport := &http.Transport{
		Proxy:                  nil,
		ForceAttemptHTTP2:      true,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 32768,
		TLSClientConfig:        tlsConfig,
	}
	defer transport.CloseIdleConnections()
	transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		return probeDialer(ctx, session, target, base, preset, destination, bypass).DialContext(dialCtx, "tcp4", net.JoinHostPort(destination.Address, strconv.Itoa(target.Port)))
	}
	var phase atomic.Value
	phase.Store("connect")
	request, err := probeRequest(ctx, target, &phase)
	if err != nil {
		return result, err
	}
	response, err := (&http.Client{Transport: transport, CheckRedirect: noProbeRedirect}).Do(request)
	result.Stage = phase.Load().(string)
	if err != nil {
		return result, err
	}
	err = readProbeResponse(response, &result)
	return result, err
}
