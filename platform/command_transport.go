// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const commandWireLimit = 65536

var splashDirectory = "/jffs/customized_splash"

type commandTransport struct {
	Resource string `json:"resource"`
	Target   string `json:"target"`
	Instance string `json:"instance"`
	mailbox  *webMailbox
}

type commandEnvelope struct {
	Protocol string `json:"protocol"`
	Instance string `json:"instance"`
	ID       string `json:"id"`
	Payload  string `json:"payload"`
	CRC32    string `json:"crc32"`
	Expires  int64  `json:"expires_at"`
}

func (p commandTransport) publish(state string) error {
	data, _ := json.Marshal(map[string]any{
		"state": state, "instance": p.Instance, "resource": p.Resource,
		"max_payload_bytes": commandWireLimit, "updated_at": time.Now().UnixMilli(),
	})

	return atomicWrite(filepath.Join(webRoot, "extensions-command.json"), data, 0644)
}

func (p commandTransport) removeLink() {
	if !strings.HasPrefix(p.Resource, "ocf_probe_") || len(p.Resource) != 26 {
		return
	}

	if _, err := hex.DecodeString(strings.TrimPrefix(p.Resource, "ocf_probe_")); err != nil {
		return
	}

	link := filepath.Join(splashDirectory, p.Resource+".css")
	if target, err := os.Readlink(link); err == nil && target == filepath.Join(ram, "upload-probe", "inbox") && target == p.Target {
		os.Remove(link)
	}
}

func prepareCommandTransport() (commandTransport, int, error) {
	p := commandTransport{Target: filepath.Join(ram, "upload-probe", "inbox"), mailbox: &webMailbox{}}
	ownerPath := filepath.Join(base, "upload-probe.json")
	var previous commandTransport
	if data, err := os.ReadFile(ownerPath); err == nil && json.Unmarshal(data, &previous) == nil {
		previous.removeLink()
	}

	if err := os.MkdirAll(splashDirectory, 0755); err != nil {
		return p, -1, err
	}

	info, err := os.Lstat(splashDirectory)
	if err != nil || !info.IsDir() {
		return p, -1, errors.New("invalid splash resource directory")
	}

	if err = os.MkdirAll(filepath.Dir(p.Target), 0700); err != nil {
		return p, -1, err
	}

	// Replacing a previous inbox removes unknown inode/symlink state before use.
	if err = atomicWrite(p.Target, nil, 0600); err != nil {
		return p, -1, err
	}

	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return p, -1, err
	}

	p.Instance = hex.EncodeToString(nonce[:])
	p.Resource = "ocf_probe_" + p.Instance[:16]
	link := filepath.Join(splashDirectory, p.Resource+".css")
	if err = os.Symlink(p.Target, link); err != nil {
		return p, -1, err
	}

	if err = saveJSON(ownerPath, p); err != nil {
		p.removeLink()

		return p, -1, err
	}

	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err == nil {
		_, err = syscall.InotifyAddWatch(fd, p.Target, syscall.IN_CLOSE_WRITE)
	}

	if err != nil {
		if fd >= 0 {
			syscall.Close(fd)
		}
		p.removeLink()

		return p, -1, err
	}

	os.Remove(filepath.Join(webRoot, "extensions-command-reply.json"))
	if err = p.publish("READY"); err != nil {
		syscall.Close(fd)
		p.removeLink()

		return p, -1, err
	}

	return p, fd, nil
}

func payloadCRC(payload string) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(payload)))
}

func (p commandTransport) command(request commandEnvelope) {
	if len(request.Payload) > 24576 || request.CRC32 != payloadCRC(request.Payload) {
		return
	}

	now := time.Now()
	if request.Expires < now.UnixMilli() || request.Expires > now.Add(time.Minute).UnixMilli() {
		return
	}

	var command webRequest
	if json.Unmarshal([]byte(request.Payload), &command) != nil || command.ID != request.ID {
		return
	}

	if p.mailbox == nil {
		return
	}

	p.mailbox.apply([]byte(request.Payload), now)
	payload, _ := json.Marshal(map[string]any{
		"request_id": p.mailbox.LastID, "request_ok": p.mailbox.LastOK,
		"request_message": p.mailbox.LastMessage, "request_data": p.mailbox.LastData,
	})
	response, _ := json.Marshal(map[string]any{
		"instance": p.Instance, "id": request.ID, "payload": string(payload),
		"crc32": payloadCRC(string(payload)),
	})
	atomicWrite(filepath.Join(webRoot, "extensions-command-reply.json"), response, 0644)
}

func (p commandTransport) consume() {
	info, err := os.Lstat(p.Target)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return
	}

	file, err := os.Open(p.Target)
	if err != nil {
		return
	}

	raw, err := io.ReadAll(io.LimitReader(file, commandWireLimit+1025))
	file.Close()
	os.Truncate(p.Target, 0)

	var request commandEnvelope
	if err != nil || len(raw) > commandWireLimit+1024 || json.Unmarshal(raw, &request) != nil {
		return
	}

	if request.Instance != p.Instance || !validNonce(request.ID) {
		return
	}

	if request.Protocol != "ocfusion-command-v1" {
		return
	}

	p.command(request)
}

func startCommandTransport() func() {
	p, fd, err := prepareCommandTransport()
	if err != nil {
		p.publish("UNAVAILABLE")

		return func() {}
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		defer syscall.Close(fd)

		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		buffer := make([]byte, 4096)

		for {
			select {
			case <-done:
				return
			case <-tick.C:
				n, err := syscall.Read(fd, buffer)
				if err != nil && err != syscall.EAGAIN {
					p.publish("UNAVAILABLE")

					return
				}

				for offset := 0; offset+16 <= n; {
					mask := binary.LittleEndian.Uint32(buffer[offset+4:])
					length := int(binary.LittleEndian.Uint32(buffer[offset+12:]))
					if mask&(syscall.IN_CLOSE_WRITE|syscall.IN_Q_OVERFLOW) != 0 {
						p.consume()
					}

					offset += 16 + length
				}
			}
		}
	}()

	return func() {
		close(done)
		<-stopped

		p.removeLink()
		os.Truncate(p.Target, 0)
		p.publish("STOPPED")
		os.Remove(filepath.Join(webRoot, "extensions-command-reply.json"))
	}
}
