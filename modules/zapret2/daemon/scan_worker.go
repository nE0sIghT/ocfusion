// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

var errScanSetup = errors.New("test packet processing unavailable")

func bindProbeSocket(fd int, iface, source, bypass string) (int, error) {
	if err := syscall.SetsockoptString(fd, syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface); err != nil {
		return 0, err
	}
	if bypass != "" {
		mark, err := strconv.ParseUint(bypass, 0, 32)
		if err != nil {
			return 0, err
		}
		if err = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_MARK, int(mark)); err != nil {
			return 0, err
		}
	}
	ip := net.ParseIP(source).To4()
	if ip == nil {
		return 0, errors.New("invalid WAN source address")
	}
	bound := &syscall.SockaddrInet4{}
	copy(bound.Addr[:], ip)
	if err := syscall.Bind(fd, bound); err != nil {
		return 0, err
	}
	socket, err := syscall.Getsockname(fd)
	if err != nil {
		return 0, err
	}
	local, ok := socket.(*syscall.SockaddrInet4)
	if !ok {
		return 0, errors.New("unexpected test socket")
	}
	return local.Port, nil
}

func installScanRules(resources Resources) error {
	if err := writeJSON(scanResources(), resources); err != nil {
		return err
	}
	// Insert backwards so reply marking precedes both the test queue and the
	// existing module's rules. No foreign rule or chain is removed or bypassed.
	for i := len(resources.Rules) - 1; i >= 0; i-- {
		if err := ensureRule(resources.Rules[i]); err != nil {
			return err
		}
	}
	return nil
}

func probeWorkerConfig(base Config, preset Preset, target ScanTarget, address string) Config {
	resolved := presetConfig(base, preset)
	c := resolved
	prefix := "/32"
	if net.ParseIP(address).To4() == nil {
		prefix = "/128"
	}
	c.Networks, c.Domains = []string{address + prefix}, nil
	c.TCP, c.UDP, c.Applications = "off", "off", nil
	if scanProtocol(target) == "tcp" {
		c.TCP = resolved.TCP
		c.TCPPorts = []int{target.Port}
	} else if target.Protocol == "http3" {
		c.UDP = resolved.UDP
		c.UDPPorts = []int{target.Port}
	} else {
		c.Applications = resolved.Applications
		for i := range c.Applications {
			c.Applications[i].Ports = strconv.Itoa(target.Port)
		}
	}
	return c
}

func startProbeWorker(ctx context.Context, target ScanTarget, base Config, preset Preset, address, iface string, port int, bypass string) (*exec.Cmd, chan error, error) {
	c := probeWorkerConfig(base, preset, target, address)
	allocation := c
	if preset.ID == "baseline" {
		// No engine is launched. Allocate only recovery/loop-protection identity,
		// independent of whether the selected protocol has any enabled strategy.
		allocation.TCP = "split"
		allocation.TCPPorts = []int{target.Port}
	}
	resources, err := allocate(allocation)
	if err != nil {
		return nil, nil, err
	}
	protocol := "udp"
	if scanProtocol(target) == "tcp" {
		protocol = "tcp"
	}
	rules := scanRulesForProtocol(resources, iface, address, target.Port, port, bypass, protocol)
	resources.Rules = rules
	if preset.ID == "baseline" {
		resources.Rules = nil
		if bypass != "" {
			resources.Rules = []Rule{rules[1]}
		}
		return nil, nil, installScanRules(resources)
	}
	uid, err := privateUID()
	if err != nil {
		return nil, nil, err
	}
	directory := scanPath("worker")
	if err = prepareWorkerAt(c, uid, directory); err != nil {
		return nil, nil, err
	}
	// Journal before launching; a forced exit still leaves recoverable ownership.
	if err = writeJSON(scanResources(), resources); err != nil {
		return nil, nil, err
	}
	worker := exec.CommandContext(ctx, filepath.Join(os.Getenv("EXT_PAYLOAD_PATH"), "bin", "nfqws2"), workerArgs(c, resources, directory, uid)...)
	worker.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LUA_PATH=", "LUA_CPATH="}
	worker.Stdout = &boundedLog{path: scanPath("worker.log")}
	worker.Stderr = worker.Stdout
	if err = worker.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- worker.Wait()
	}()
	deadline := time.Now().Add(time.Second)
	for !queueReady(resources.Queue) {
		if ctx.Err() != nil || time.Now().After(deadline) {
			return worker, done, errors.New("test packet processor unavailable")
		}
		select {
		case err := <-done:
			// Keep the completion available for the caller's single cleanup path.
			done <- err
			return worker, done, errors.New("test packet processor exited")
		case <-time.After(20 * time.Millisecond):
		}
	}
	return worker, done, installScanRules(resources)
}
