// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func uploadProbeFixture(t *testing.T) {
	t.Helper()

	oldBase, oldRAM, oldWeb, oldSplash := base, ram, webRoot, splashDirectory
	base, ram, webRoot, splashDirectory = t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Cleanup(func() {
		base, ram, webRoot, splashDirectory = oldBase, oldRAM, oldWeb, oldSplash
	})
}

func probeRequest(p uploadProbe, size int) uploadProbeRequest {
	return uploadProbeRequest{
		Protocol: "ocfusion-upload-probe-v1", Instance: p.Instance,
		ID: "0123456789abcdef0123456789abcdef", Size: size,
		Payload: uploadProbePayload(size),
	}
}

func TestUploadProbeRoundTripSizes(t *testing.T) {
	uploadProbeFixture(t)
	native := filepath.Join(splashDirectory, "existing.css")
	os.WriteFile(native, []byte("native asset"), 0600)

	p, fd, err := prepareUploadProbe()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	defer p.removeLink()

	for _, size := range []int{1024, 8192, 32768, 65536} {
		request := probeRequest(p, size)
		raw, _ := json.Marshal(request)
		link := filepath.Join(splashDirectory, p.Resource+".css")
		if err := os.WriteFile(link, append(raw, '\r', '\n'), 0600); err != nil {
			t.Fatal(err)
		}

		p.consume()
		var reply struct {
			ID            string `json:"id"`
			Instance      string `json:"instance"`
			Payload       string `json:"payload"`
			ReceivedBytes int    `json:"received_bytes"`
			SHA256        string `json:"sha256"`
		}
		data, err := os.ReadFile(filepath.Join(webRoot, "extensions-upload-reply.json"))
		if err != nil || json.Unmarshal(data, &reply) != nil {
			t.Fatal("missing reply", err)
		}

		digest := sha256.Sum256([]byte(request.Payload))
		if reply.ID != request.ID || reply.Instance != p.Instance || reply.Payload != request.Payload || reply.ReceivedBytes != size || reply.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatal("corrupt round trip", size)
		}

		info, _ := os.Stat(p.Target)
		if info.Size() != 0 {
			t.Fatal("ingress data not cleared")
		}
	}

	data, _ := os.ReadFile(native)
	if string(data) != "native asset" {
		t.Fatal("native resource modified")
	}
	if _, err := os.Stat(filepath.Join(splashDirectory, p.Resource+".json")); !os.IsNotExist(err) {
		t.Fatal("JSON resource created")
	}
}

func TestUploadProbeRejectsUntrustedAndPrivatePayloads(t *testing.T) {
	uploadProbeFixture(t)
	p, fd, err := prepareUploadProbe()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	defer p.removeLink()

	mutations := []func(*uploadProbeRequest){
		func(q *uploadProbeRequest) {
			q.Protocol = "module.call"
		},
		func(q *uploadProbeRequest) {
			q.Instance = "foreign"
		},
		func(q *uploadProbeRequest) {
			q.ID = "invalid"
		},
		func(q *uploadProbeRequest) {
			q.Size = 0
		},
		func(q *uploadProbeRequest) {
			q.Size = uploadProbeLimit + 1
		},
		func(q *uploadProbeRequest) {
			q.Payload = "-----BEGIN PRIVATE KEY-----"
		},
	}
	for _, mutate := range mutations {
		request := probeRequest(p, 1024)
		mutate(&request)
		raw, _ := json.Marshal(request)
		os.WriteFile(p.Target, raw, 0600)
		p.consume()

		if _, err := os.Stat(filepath.Join(webRoot, "extensions-upload-reply.json")); !os.IsNotExist(err) {
			t.Fatal("invalid request produced a reply")
		}
	}

	for _, raw := range []string{"{", strings.Repeat("A", uploadProbeLimit+1025)} {
		os.WriteFile(p.Target, []byte(raw), 0600)
		p.consume()

		if _, err := os.Stat(filepath.Join(webRoot, "extensions-upload-reply.json")); !os.IsNotExist(err) {
			t.Fatal("malformed/oversized request accepted")
		}
	}
}

func TestUploadProbeWaitsForCompletedWriteAndStops(t *testing.T) {
	uploadProbeFixture(t)
	stop := startUploadProbe()
	defer func() {
		if stop != nil {
			stop()
		}
	}()

	var config uploadProbe
	data, err := os.ReadFile(filepath.Join(webRoot, "extensions-upload-probe.json"))
	if err != nil || json.Unmarshal(data, &config) != nil {
		t.Fatal("bootstrap missing", err)
	}

	link := filepath.Join(splashDirectory, config.Resource+".css")
	file, err := os.OpenFile(link, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}

	request := probeRequest(config, 8192)
	raw, _ := json.Marshal(request)
	file.Write(raw[:len(raw)/2])
	time.Sleep(120 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(webRoot, "extensions-upload-reply.json")); !os.IsNotExist(err) {
		file.Close()
		t.Fatal("partial write acknowledged")
	}

	file.Write(raw[len(raw)/2:])
	file.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err = os.ReadFile(filepath.Join(webRoot, "extensions-upload-reply.json"))
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
	data, _ = os.ReadFile(filepath.Join(webRoot, "extensions-upload-probe.json"))
	if !strings.Contains(string(data), "STOPPED") {
		t.Fatal("probe still ready after stop")
	}
}

func TestUploadProbeOwnershipAndRestart(t *testing.T) {
	uploadProbeFixture(t)
	p, fd, err := prepareUploadProbe()
	if err != nil {
		t.Fatal(err)
	}
	syscall.Close(fd)

	other, otherFD, err := prepareUploadProbe()
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
	uploadProbeFixture(t)
	previous := runModuleCommand
	t.Cleanup(func() {
		runModuleCommand = previous
	})

	calls := 0
	runModuleCommand = func(args ...string) (json.RawMessage, error) {
		calls++

		return json.RawMessage(`{"label":"Привет"}`), nil
	}

	p, fd, err := prepareUploadProbe()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	defer p.removeLink()

	payload := `{"id":"0123456789abcdef0123456789abcdef","action":"module.action","module":"example","operation":"settings"}`
	request := uploadProbeRequest{
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
