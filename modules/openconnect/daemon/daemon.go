// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Status struct {
	Version        string          `json:"version"`
	State          string          `json:"state"`
	Message        string          `json:"message"`
	Index          int             `json:"vpnc_idx"`
	Networks       []Network       `json:"networks"`
	ImportID       string          `json:"import_id,omitempty"`
	ImportOK       bool            `json:"import_ok"`
	RequestID      string          `json:"request_id,omitempty"`
	RequestOK      bool            `json:"request_ok"`
	RequestMessage string          `json:"request_message,omitempty"`
	RequestData    json.RawMessage `json:"request_data,omitempty"`
}
type Child struct {
	cmd        *exec.Cmd
	done       chan error
	generation string
	authError  chan bool
	stage      chan string
}

func launch(p Profile, m Manifest) (*Child, error) {
	if e := verifyBundle(m); e != nil {
		return nil, e
	}

	if _, err := os.Stat("/dev/net/tun"); err != nil {
		return nil, errors.New("VPN service has not prepared TUN")
	}

	for _, f := range []struct{ rel, target string }{{"bin/openconnect", "openconnect"}, {"ca-bundle.crt", "ca-bundle.crt"}} {
		b, e := os.ReadFile(filepath.Join(m.Bundle, bundleRelative(m, f.rel)))
		if e != nil {
			return nil, e
		}

		mode := os.FileMode(0600)
		if f.target == "openconnect" {
			mode = 0700
		}

		if e = atomicWrite(filepath.Join(runDir, f.target), b, mode); e != nil {
			return nil, e
		}
	}

	baseCA, e := os.ReadFile(filepath.Join(runDir, "ca-bundle.crt"))
	if e != nil {
		return nil, e
	}
	// Explicitly use packaged public roots plus profile roots; the router/system
	// OpenSSL paths are not our trust store. ca_pem is additive, as in nm-openconnect.

	if e := atomicWrite(filepath.Join(runDir, "effective-ca.pem"), mergedCABundle(baseCA, p.CA), 0600); e != nil {
		return nil, e
	}

	args := []string{"--protocol", p.Protocol, "--interface", "ocvpn0", "--script", filepath.Join(root, "vpnc-script"), "--disable-ipv6", "--non-inter", "--reconnect-timeout", "60", "--cafile", filepath.Join(runDir, "effective-ca.pem"), "--no-system-trust"}
	if p.PrivateKey != "" {
		for name, value := range map[string]string{"client.pem": p.Certificate, "key.pem": p.PrivateKey} {
			if e := atomicWrite(filepath.Join(runDir, name), []byte(value), 0600); e != nil {
				return nil, e
			}
		}

		args = append(args, "--certificate", filepath.Join(runDir, "client.pem"), "--sslkey", filepath.Join(runDir, "key.pem"))
	}

	if p.Username != "" {
		args = append(args, "--user", p.Username)
	}

	if p.Authgroup != "" {
		args = append(args, "--authgroup", p.Authgroup)
	}

	if p.ServerCert != "" {
		args = append(args, "--servercert", p.ServerCert)
	}

	if p.NoDTLS {
		args = append(args, "--no-dtls")
	}

	if p.MTU != 0 {
		args = append(args, "--mtu", strconv.Itoa(p.MTU))
	}

	if p.Password != "" {
		args = append(args, "--passwd-on-stdin")
	}

	args = append(args, p.Server)
	generation := strconv.FormatInt(time.Now().UnixNano(), 10)
	c := exec.Command(filepath.Join(runDir, "openconnect"), args...)
	c.Env = append(os.Environ(), "OCF_GENERATION="+generation, "OCF_MANAGED_ROUTES=1")
	c.Stdin = strings.NewReader(p.Password + "\n")
	pipe, e := c.StdoutPipe()
	if e != nil {
		return nil, e
	}

	c.Stderr = c.Stdout
	ch := &Child{cmd: c, done: make(chan error, 1), generation: generation, authError: make(chan bool, 1), stage: make(chan string, 32)}
	if e = c.Start(); e != nil {
		return nil, e
	}

	go func() {
		readClientLines(pipe, func(text string) {
			line := strings.ToLower(text)
			if stage := clientStage(line); stage != "" {
				select {
				case ch.stage <- stage:
				default:
				}
			}

			if strings.Contains(line, "authentication failed") || strings.Contains(line, "login failed") || strings.Contains(line, "certificate verify failed") || strings.Contains(line, "user input") || strings.Contains(line, "non-interactive") {
				select {
				case ch.authError <- true:
				default:
				}
			}
		})
		ch.done <- c.Wait()
	}()

	return ch, nil
}

func halt(c *Child) {
	if c == nil {
		return
	}

	c.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-c.done:
	case <-time.After(4 * time.Second):
		c.cmd.Process.Kill()

		select {
		case <-c.done:
		case <-time.After(time.Second):
		}
	}
}

func daemon() error {
	m, e := loadManifest()
	if e != nil {
		return e
	}

	if e = os.MkdirAll(runDir, 0700); e != nil {
		return e
	}

	lock, e := os.OpenFile(filepath.Join(runDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}

	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return nil
	}

	if e = atomicWrite(filepath.Join(runDir, "pid"), []byte(strconv.Itoa(os.Getpid())), 0600); e != nil {
		return e
	}

	defer os.Remove(filepath.Join(runDir, "pid"))

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)

	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	var child *Child
	var current Profile
	var ns []Network
	var lastEvent []byte
	var next time.Time
	backoff := 5 * time.Second
	state, message := "STARTING", ""
	authBlocked := false
	bundleNext := time.Time{}
	var connected *Event
	var importID string
	var importOK bool
	publish := func() {
		st := Status{Version: version, State: state, Message: message, Index: m.Index, Networks: ns, ImportID: importID, ImportOK: importOK}
		saveJSON(filepath.Join(runDir, "status.json"), st)
	}
	defer func() {
		closeTunnel(m)
		halt(child)

		state, message = "STOPPED", ""
		publish()
	}()

	invalidate := func() {
		closeTunnel(m)
		halt(child)

		child = nil
		connected = nil
		lastEvent = nil

	}

	for {

		if fresh, err := loadManifest(); err == nil {
			m = fresh
		}

		vpnState, netErr := vpnCall(vpnRequest{Operation: "prepare"})
		identErr := netErr
		freshNS := vpnState.Networks

		if identErr != nil || netErr != nil {
			invalidate()

			state = "CONFIG_CONFLICT"
			message = "Fusion/SDN configuration invalid; run doctor"
			// Do not remove guards for last-known networks on a malformed/transitional snapshot.
		} else {
			if !reflect.DeepEqual(freshNS, ns) {
				invalidate()

				ns = freshNS
				state = "NETWORK_CHANGED"
				message = "Rebinding current ASUS web configuration"
			}

			prepErr := netErr
			p, profileErr := loadProfile()
			if profileErr != nil {
				invalidate()

				state = "NO_PROFILE"
				message = "Import a valid OpenConnect profile"
			} else {
				if !reflect.DeepEqual(p, current) {
					invalidate()

					current = p
					authBlocked = false
					next = time.Time{}
					backoff = 5 * time.Second
				}

				switch {
				case !p.Enabled:
					invalidate()

					state = "DISABLED"
					message = "Profile disabled; bound networks remain blocked"
				case len(ns) == 0:
					invalidate()

					state = "WAIT_NETWORK"
					message = "Assign OCFusion (managed) to a dedicated SDN Wi-Fi/VLAN in ASUS web"
				case prepErr != nil:
					invalidate()

					state = "WAIT_NETWORK"
					message = "Bridge/firewall preparation failed: " + prepErr.Error()
				case authBlocked:
					invalidate()

					state = "AUTH_REQUIRED"
					message = "Authentication/TLS failed; correct profile and import again"
				case time.Now().Year() < 2025:
					invalidate()

					state = "WAIT_TIME"
					message = "Wait for router NTP before TLS"
				default:
					if child != nil {
						for len(child.stage) > 0 {
							stage := <-child.stage
							if connected == nil {
								message = stage
							}
						}

						select {
						case <-child.authError:
							authBlocked = true
						default:
						}

						select {
						case <-child.done:
							child = nil
							closeTunnel(m)

							connected = nil
							state = "RECONNECTING"
							message = "VPN exited; retry scheduled"
							next = time.Now().Add(backoff)
							if backoff < 60*time.Second {
								backoff *= 2
								if backoff > 60*time.Second {
									backoff = 60 * time.Second
								}
							}
						default:
						}
					}

					if time.Now().After(bundleNext) {
						bundleNext = time.Now().Add(30 * time.Second)
						if err := verifyBundle(m); err != nil {
							invalidate()

							state = "WAIT_STORAGE"
							message = "USB bundle missing/changed"
							next = time.Now().Add(30 * time.Second)
						}
					}

					if child == nil && !authBlocked && time.Now().After(next) {
						if _, err := runner("ip", "route", "get", "1.1.1.1"); err != nil {
							state = "WAIT_NETWORK"
							message = "No WAN route"
							next = time.Now().Add(10 * time.Second)
						} else {
							var err error
							child, err = launch(p, m)
							if err != nil {
								state = "ERROR"
								message = "Client launch failed; run doctor"
								next = time.Now().Add(30 * time.Second)
							} else {
								state = "CONNECTING"
								message = ""
							}
						}
					}

					if child != nil {
						raw, err := os.ReadFile(filepath.Join(runDir, "event.json"))
						if err == nil && !bytes.Equal(raw, lastEvent) {
							lastEvent = raw
							var ev Event
							if json.Unmarshal(raw, &ev) == nil && ev.Generation == child.generation {
								switch ev.Reason {
								case "connect", "reconnect":
									_, err = applyConnect(m, p, ns, ev)
									if err != nil {
										invalidate()

										state = "ERROR"
										message = "Tunnel network application failed; check DNS, conflicts and doctor"
										next = time.Now().Add(30 * time.Second)
									} else {

										connected = &ev
										state = "UP"
										message = "IPv4 full tunnel; Wi-Fi/VLAN bindings active"
										backoff = 5 * time.Second
									}
								case "disconnect", "attempt-reconnect":
									closeTunnel(m)

									connected = nil
									state = "RECONNECTING"
									message = "Tunnel down; WAN fallback blocked"
								}
							}
						}

						if connected != nil {
							if _, err = applyConnect(m, p, ns, *connected); err != nil {
								invalidate()

								state = "ERROR"
								message = "Network invariant lost; tunnel closed"
								next = time.Now().Add(10 * time.Second)
							}
						}
					}
				}
			}
		}

		publish()

		select {
		case <-signals:
			state = "STOPPING"
			message = ""
			publish()

			return nil
		case <-tick.C:
		}
	}
}

func mergedCABundle(public []byte, additional string) []byte {
	result := append([]byte(nil), public...)
	if additional != "" {
		result = append(result, '\n')
		result = append(result, []byte(additional)...)
		result = append(result, '\n')
	}

	return result
}
