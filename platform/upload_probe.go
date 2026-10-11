// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const uploadProbeLimit = 65536
const uploadProbePattern = "OCF0123456789abcdef"

var splashDirectory = "/jffs/customized_splash"

type uploadProbe struct {
	Resource string `json:"resource"`
	Target   string `json:"target"`
	Instance string `json:"instance"`
}

type uploadProbeRequest struct {
	Protocol string `json:"protocol"`
	Instance string `json:"instance"`
	ID       string `json:"id"`
	Size     int    `json:"size"`
	Payload  string `json:"payload"`
}

func uploadProbePayload(size int) string {
	return strings.Repeat(uploadProbePattern, size/len(uploadProbePattern)+1)[:size]
}

func (p uploadProbe) publish(state string) error {
	data, _ := json.Marshal(map[string]any{
		"state": state, "instance": p.Instance, "resource": p.Resource,
		"max_payload_bytes": uploadProbeLimit, "updated_at": time.Now().UnixMilli(),
	})

	return atomicWrite(filepath.Join(webRoot, "extensions-upload-probe.json"), data, 0644)
}

func (p uploadProbe) removeLink() {
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

func prepareUploadProbe() (uploadProbe, int, error) {
	p := uploadProbe{Target: filepath.Join(ram, "upload-probe", "inbox")}
	ownerPath := filepath.Join(base, "upload-probe.json")
	var previous uploadProbe
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

	os.Remove(filepath.Join(webRoot, "extensions-upload-reply.json"))
	if err = p.publish("READY"); err != nil {
		syscall.Close(fd)
		p.removeLink()

		return p, -1, err
	}

	return p, fd, nil
}

// Only a fixed synthetic pattern is accepted. This diagnostic cannot dispatch
// module operations, echo credentials or turn the stock uploader into a shell.
func (p uploadProbe) consume() {
	info, err := os.Lstat(p.Target)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return
	}

	file, err := os.Open(p.Target)
	if err != nil {
		return
	}

	raw, err := io.ReadAll(io.LimitReader(file, uploadProbeLimit+1025))
	file.Close()
	os.Truncate(p.Target, 0)

	var request uploadProbeRequest
	if err != nil || len(raw) > uploadProbeLimit+1024 || json.Unmarshal(raw, &request) != nil {
		return
	}

	if request.Protocol != "ocfusion-upload-probe-v1" || request.Instance != p.Instance || !validNonce(request.ID) || request.Size < 1 || request.Size > uploadProbeLimit {
		return
	}

	if request.Payload != uploadProbePayload(request.Size) {
		return
	}

	digest := sha256.Sum256([]byte(request.Payload))
	data, _ := json.Marshal(map[string]any{
		"instance": p.Instance, "id": request.ID, "result": "PASS",
		"received_bytes": request.Size, "request_bytes": len(raw),
		"payload": request.Payload, "sha256": hex.EncodeToString(digest[:]),
		"response_storage": "RAM", "updated_at": time.Now().UnixMilli(),
	})
	atomicWrite(filepath.Join(webRoot, "extensions-upload-reply.json"), data, 0644)
}

func startUploadProbe() func() {
	p, fd, err := prepareUploadProbe()
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
		os.Remove(filepath.Join(webRoot, "extensions-upload-reply.json"))
	}
}
