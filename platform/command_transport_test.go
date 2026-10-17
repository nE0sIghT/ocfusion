// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func commandTransportFixture(t *testing.T) {
	t.Helper()

	oldBase, oldRAM, oldWeb, oldSplash := base, ram, webRoot, splashDirectory
	base, ram, webRoot, splashDirectory = t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Cleanup(func() {
		base, ram, webRoot, splashDirectory = oldBase, oldRAM, oldWeb, oldSplash
	})
}

func commandRequest(p commandTransport) commandEnvelope {
	payload := `{"id":"0123456789abcdef0123456789abcdef","action":"module.action","module":"example","operation":"settings"}`

	return commandEnvelope{
		Protocol: "ocfusion-command-v1", Instance: p.Instance,
		ID: "0123456789abcdef0123456789abcdef", Payload: payload,
		CRC32: payloadCRC(payload), Expires: time.Now().Add(30 * time.Second).UnixMilli(),
	}
}

func TestCommandTransportWaitsForCompletedWriteAndStops(t *testing.T) {
	commandTransportFixture(t)
	previous := runModuleCommand
	runModuleCommand = func(args ...string) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}
	t.Cleanup(func() {
		runModuleCommand = previous
	})

	stop := startCommandTransport()
	defer func() {
		if stop != nil {
			stop()
		}
	}()

	var config commandTransport
	data, err := os.ReadFile(filepath.Join(webRoot, "extensions-command.json"))
	if err != nil || json.Unmarshal(data, &config) != nil {
		t.Fatal("bootstrap missing", err)
	}

	link := filepath.Join(splashDirectory, config.Resource+".css")
	file, err := os.OpenFile(link, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}

	request := commandRequest(config)
	raw, _ := json.Marshal(request)
	file.Write(raw[:len(raw)/2])
	time.Sleep(120 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(webRoot, "extensions-command-reply.json")); !os.IsNotExist(err) {
		file.Close()
		t.Fatal("partial write acknowledged")
	}

	file.Write(raw[len(raw)/2:])
	file.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err = os.ReadFile(filepath.Join(webRoot, "extensions-command-reply.json"))
		if err == nil {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || !strings.Contains(string(data), request.ID) {
		t.Fatal("completed write not acknowledged", err)
	}

	stop()
	stop = nil
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("owned resource remained after stop")
	}
	data, _ = os.ReadFile(filepath.Join(webRoot, "extensions-command.json"))
	if !strings.Contains(string(data), "STOPPED") {
		t.Fatal("probe still ready after stop")
	}
}

func TestCommandTransportOwnershipAndRestart(t *testing.T) {
	commandTransportFixture(t)
	p, fd, err := prepareCommandTransport()
	if err != nil {
		t.Fatal(err)
	}
	syscall.Close(fd)

	other, otherFD, err := prepareCommandTransport()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(otherFD)
	defer other.removeLink()

	if _, err := os.Lstat(filepath.Join(splashDirectory, p.Resource+".css")); !os.IsNotExist(err) {
		t.Fatal("old owned resource not removed")
	}
	link := filepath.Join(splashDirectory, other.Resource+".css")
	os.Remove(link)
	os.Symlink("/foreign/resource", link)
	other.removeLink()

	if target, err := os.Readlink(link); err != nil || target != "/foreign/resource" {
		t.Fatal("foreign replacement removed")
	}
}

func TestCommandCRCExpiryAndReplies(t *testing.T) {
	commandTransportFixture(t)
	previous := runModuleCommand
	t.Cleanup(func() {
		runModuleCommand = previous
	})

	calls := 0
	runModuleCommand = func(args ...string) (json.RawMessage, error) {
		calls++

		return json.RawMessage(`{"label":"Привет","rules":"` + strings.Repeat("<", 15000) + `"}`), nil
	}

	p, fd, err := prepareCommandTransport()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	defer p.removeLink()

	payload := `{"id":"0123456789abcdef0123456789abcdef","action":"module.action","module":"example","operation":"settings"}`
	request := commandEnvelope{
		Protocol: "ocfusion-command-v1", Instance: p.Instance,
		ID: "0123456789abcdef0123456789abcdef", Payload: payload,
		CRC32: payloadCRC(payload), Expires: time.Now().Add(30 * time.Second).UnixMilli(),
	}
	path := filepath.Join(webRoot, "extensions-command-reply.json")
	send := func() {
		raw, _ := json.Marshal(request)
		os.WriteFile(p.Target, raw, 0600)
		p.consume()
	}

	request.CRC32 = "00000000"
	send()
	if calls != 0 {
		t.Fatal("corrupt request dispatched")
	}

	request.CRC32 = payloadCRC(payload)
	request.Expires = time.Now().Add(-time.Minute).UnixMilli()
	send()
	if calls != 0 {
		t.Fatal("expired request dispatched")
	}

	request.Expires = time.Now().Add(30 * time.Second).UnixMilli()
	send()
	var reply struct {
		ID      string `json:"id"`
		Payload string `json:"payload"`
		CRC32   string `json:"crc32"`
	}
	raw, err := os.ReadFile(path)
	if len(raw) <= 16384 || len(raw) > 128*1024 {
		t.Fatal("escaped large response has unexpected wire size", len(raw))
	}

	if err != nil || json.Unmarshal(raw, &reply) != nil || calls != 1 || reply.ID != request.ID || reply.CRC32 != payloadCRC(reply.Payload) || !strings.Contains(reply.Payload, "Привет") {
		t.Fatal("invalid checked reply", string(raw), calls, err)
	}

	send()
	if calls != 1 {
		t.Fatal("immediate duplicate executed twice")
	}

	if payloadCRC("123456789") != "cbf43926" || payloadCRC("Привет") != "a8eab199" {
		t.Fatal("CRC32 IEEE/UTF-8 mismatch")
	}
}
