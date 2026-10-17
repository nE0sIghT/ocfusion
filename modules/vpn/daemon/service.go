// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

type Tunnel struct {
	Interface string   `json:"interface"`
	Address   string   `json:"address"`
	DNS       []string `json:"dns"`
	MTU       int      `json:"mtu"`
}

type Request struct {
	Resolver   *ResolverRegistration `json:"resolver,omitempty"`
	PreferDNS  bool                  `json:"prefer_dns,omitempty"`
	API        int                   `json:"api"`
	Operation  string                `json:"operation"`
	Client     string                `json:"client"`
	Profile    string                `json:"profile"`
	Connection string                `json:"connection,omitempty"`
	Tunnel     *Tunnel               `json:"tunnel,omitempty"`
}

type Response struct {
	Resolver    *ResolverRegistration `json:"resolver,omitempty"`
	DNSStatus   *DNSStatus            `json:"dns_status,omitempty"`
	Connections []Manifest            `json:"connections,omitempty"`
	OK          bool                  `json:"ok"`
	Manifest    Manifest              `json:"manifest"`
	Networks    []Network             `json:"networks"`
	DNS         string                `json:"dns,omitempty"`
	Error       string                `json:"error,omitempty"`
}

type Service struct {
	dnsCleaned bool
	dnsOnly    bool
	manifest   Manifest
	owner      string
	networks   []Network
	event      *Tunnel
	dns        string
	heartbeat  time.Time
}

func (s *Service) down() {
	closeTunnel(s.manifest)
	for _, network := range s.networks {
		forwarding(s.manifest, network, "del")
	}

	s.event = nil
	s.dns = ""
}

func (s *Service) reconcile(refreshTunnel bool) error {
	if err := verifyIdentity(s.manifest); err != nil {
		s.down()

		return err
	}

	if !s.dnsCleaned {
		if err := removeDNSRedirects(s.manifest); err != nil {
			s.down()

			return err
		}

		s.dnsCleaned = true
	}

	if err := ensureNativePolicy(s.manifest); err != nil {
		s.down()

		return err
	}

	fresh, err := networks(s.manifest)
	if err != nil {
		s.down()

		return err
	}

	old, _ := json.Marshal(s.networks)
	current, _ := json.Marshal(fresh)
	if string(old) != string(current) {
		s.down()
		for _, network := range s.networks {
			unbind(s.manifest, network, "")
		}

		s.networks = fresh
	}

	if err = prepare(s.manifest, s.networks); err != nil {
		s.down()

		return err
	}

	if s.event != nil {
		if time.Since(s.heartbeat) > 12*time.Second {
			s.down()

			return errors.New("VPN client heartbeat expired")
		}

		if _, err = runner("ip", "link", "show", "dev", tunnelInterface(s.manifest)); err != nil {
			s.down()

			return errors.New("tunnel interface unavailable")
		}

		if !refreshTunnel {
			return nil
		}

		s.dns, err = applyConnection(s.manifest, s.networks, *s.event)
		if err != nil {
			s.down()
		}
	}

	return err
}

func (s *Service) handle(request Request) (Response, error) {
	response := Response{Manifest: s.manifest}
	if request.API != 1 || !regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`).MatchString(request.Client) {
		return response, errors.New("unsupported VPN request")
	}

	if request.Client != s.owner {
		return response, errors.New("VPN connection owned by another client")
	}

	if request.Operation == "state" {
		assigned, err := networks(s.manifest)
		if err != nil {
			return response, err
		}

		response.OK = true
		response.Networks = assigned
		return response, nil
	}

	if request.Operation == "down" {
		s.down()
	}

	if err := s.reconcile(false); err != nil {
		return response, err
	}

	switch request.Operation {
	case "state", "prepare", "down":
	case "apply":
		if request.Tunnel == nil || request.Tunnel.MTU < 0 || request.Tunnel.MTU > 1500 || len(request.Tunnel.DNS) > 8 {
			return response, errors.New("invalid tunnel parameters")
		}

		if len(s.networks) == 0 && !s.dnsOnly {
			return response, errors.New("no assigned networks")
		}

		dns, err := applyConnection(s.manifest, s.networks, *request.Tunnel)
		if err != nil {
			s.down()

			return response, err
		}

		s.event = request.Tunnel
		s.dns = dns
		s.heartbeat = time.Now()
	default:
		return response, errors.New("unsupported VPN operation")
	}

	response.OK = true
	response.Networks = s.networks
	response.DNS = s.dns

	return response, nil
}

func serve() error {
	return serveUntil(nil)
}

func serveUntil(stop <-chan struct{}) error {
	registry, err := readConnections()
	if err != nil {
		return err
	}

	service := newBroker(registry)
	os.MkdirAll(runDir, 0700)
	lock, err := os.OpenFile(filepath.Join(runDir, "service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("VPN provider already running")
	}

	recoverResolverCapture()
	recoverDNSRouting()

	path := filepath.Join(runDir, "control.sock")
	os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}

	defer listener.Close()
	defer os.Remove(path)
	defer service.down()
	os.Chmod(path, 0600)

	requests := make(chan net.Conn)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}

			select {
			case requests <- connection:
			case <-done:
				connection.Close()
				return
			}
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	for {
		select {
		case connection := <-requests:
			connection.SetDeadline(time.Now().Add(25 * time.Second))
			var request Request
			err := json.NewDecoder(io.LimitReader(connection, 16385)).Decode(&request)
			response := Response{}
			if err == nil {
				response, err = service.handle(request)
			}

			if err != nil {
				response.Error = err.Error()
				response.OK = false
			}

			json.NewEncoder(connection).Encode(response)
			connection.Close()
		case <-tick.C:
			service.reconcile()
		case <-signals:
			return nil
		case <-stop:
			return nil
		}
	}
}
