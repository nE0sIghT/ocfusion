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
	API       int     `json:"api"`
	Operation string  `json:"operation"`
	Client    string  `json:"client"`
	Tunnel    *Tunnel `json:"tunnel,omitempty"`
}

type Response struct {
	OK       bool      `json:"ok"`
	Manifest Manifest  `json:"manifest"`
	Networks []Network `json:"networks"`
	DNS      string    `json:"dns,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type Service struct {
	manifest  Manifest
	owner     string
	networks  []Network
	event     *Tunnel
	dns       string
	heartbeat time.Time
}

func (s *Service) down() {
	closeTunnel(s.manifest)
	for _, network := range s.networks {
		if s.dns != "" {
			dnsRules(network, s.dns, "del")
		}

		forwarding(network, "del")
	}

	s.event = nil
	s.dns = ""
}

func (s *Service) reconcile(refreshTunnel bool) error {
	if err := verifyIdentity(s.manifest); err != nil {
		s.down()

		return err
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

		if _, err = runner("ip", "link", "show", "dev", "ocvpn0"); err != nil {
			s.down()

			return errors.New("tunnel interface unavailable")
		}

		if !refreshTunnel {
			return nil
		}

		s.dns, err = applyConnect(s.manifest, s.networks, *s.event)
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

	if request.Operation == "claim" && (s.owner == "" || s.owner == request.Client) {
		if err := atomicWrite(filepath.Join(root, "owner"), []byte(request.Client), 0600); err != nil {
			return response, err
		}

		s.owner = request.Client
		response.OK = true
		return response, nil
	}

	if request.Client != s.owner {
		return response, errors.New("VPN connection owned by another client")
	}

	if request.Operation == "release" {
		assigned, err := networks(s.manifest)
		if err != nil || len(assigned) != 0 {
			return response, errors.New("reassign native Wi-Fi/VLAN networks before releasing VPN connection")
		}

		s.down()
		if err := atomicWrite(filepath.Join(root, "owner"), []byte(""), 0600); err != nil {
			return response, err
		}

		s.owner = ""
		response.OK = true
		return response, nil
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

		if len(s.networks) == 0 {
			return response, errors.New("no assigned networks")
		}

		dns, err := applyConnect(s.manifest, s.networks, *request.Tunnel)
		if err != nil {
			s.down()

			return response, err
		}

		if s.dns != "" && s.dns != dns {
			for _, network := range s.networks {
				dnsRules(network, s.dns, "del")
			}
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
	manifest, err := loadManifest()
	if err != nil {
		return err
	}

	owner, err := os.ReadFile(filepath.Join(root, "owner"))
	if err != nil {
		return err
	}

	service := &Service{manifest: manifest, owner: string(owner)}
	os.MkdirAll(runDir, 0700)
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
			err := service.reconcile(true)
			state := "BLOCKED"
			if service.event != nil && err == nil {
				state = "UP"
			}

			saveJSON(filepath.Join(runDir, "status.json"), map[string]any{"state": state, "networks": service.networks})
		case <-signals:
			return nil
		}
	}
}
