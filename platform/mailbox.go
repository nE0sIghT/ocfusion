// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// A bounded, in-memory upload transaction. No untrusted paths or shell commands.
// Only one upload at a time; interruption/expiry leaves the old profile intact.
var moduleIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type webRequest struct {
	ID        string          `json:"id"`
	Action    string          `json:"action"`
	Module    string          `json:"module,omitempty"`
	Operation string          `json:"operation,omitempty"`
	Upload    string          `json:"upload,omitempty"`
	Size      int             `json:"size,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	SHA256    string          `json:"sha256,omitempty"`
	Offset    int             `json:"offset,omitempty"`
	Data      []byte          `json:"data,omitempty"`
	Config    json.RawMessage `json:"config,omitempty"`
}
type uploadSession struct {
	ID        string
	Module    string
	Kind      string
	Operation string
	Size      int
	Hash      string
	Data      []byte
	Expires   time.Time
}
type webMailbox struct {
	Upload      *uploadSession
	LastID      string
	LastOK      bool
	LastMessage string
	LastData    json.RawMessage
	DataUntil   time.Time
}

func validNonce(s string) bool {
	if len(s) != 32 {
		return false
	}

	_, e := hex.DecodeString(s)

	return e == nil
}

func (w *webMailbox) apply(raw []byte, now time.Time) (string, bool, string) {
	var q webRequest
	if len(raw) > 3072 || json.Unmarshal(raw, &q) != nil || !validNonce(q.ID) {
		return "", false, "Invalid request"
	}

	if q.ID == w.LastID {
		return q.ID, w.LastOK, w.LastMessage
	}

	if w.Upload != nil && !now.Before(w.Upload.Expires) {
		w.Upload = nil
	}

	w.LastData = nil
	e := errors.New("Unsupported action")
	message := "Request rejected"

	switch q.Action {
	case "upload.begin":
		if (q.Kind == "" || q.Kind == "module.call" && moduleIDRE.MatchString(q.Operation)) && moduleIDRE.MatchString(q.Module) && validNonce(q.Upload) && q.Size > 0 && q.Size <= 16384 && len(q.SHA256) == 64 {
			if _, err := hex.DecodeString(q.SHA256); err == nil {
				if w.Upload != nil && w.Upload.ID != q.Upload {
					e = errors.New("Upload already in progress")
				} else {
					w.Upload = &uploadSession{ID: q.Upload, Module: q.Module, Kind: q.Kind, Operation: q.Operation, Size: q.Size, Hash: q.SHA256, Data: make([]byte, 0, q.Size), Expires: now.Add(2 * time.Minute)}
					e = nil
					message = "Upload started"
				}
			}
		}
	case "upload.chunk":
		u := w.Upload
		if u != nil && u.ID == q.Upload && q.Offset == len(u.Data) && len(q.Data) > 0 && len(q.Data) <= 1536 && len(u.Data)+len(q.Data) <= u.Size {
			u.Data = append(u.Data, q.Data...)
			u.Expires = now.Add(2 * time.Minute)
			e = nil
			message = "Chunk received"
		}
	case "upload.finish":
		u := w.Upload
		if u != nil && u.ID == q.Upload && len(u.Data) == u.Size {
			sum := sha256.Sum256(u.Data)
			if hex.EncodeToString(sum[:]) == u.Hash {
				e = errors.New("Invalid module configuration")
				if json.Valid(u.Data) {
					path := ram + "/web-config.json"
					e = atomicWrite(path, u.Data, 0600)
					if e == nil {
						args := []string{"config", u.Module, path}
						if u.Kind == "module.call" {
							args = []string{"call", u.Module, u.Operation, path}
						}

						w.LastData, e = runModuleCommand(args...)
						if e == nil {
							w.DataUntil = now.Add(30 * time.Second)
						}
					}

					os.Remove(path)
				}

				if e == nil {
					message = "Settings saved"
				}
			}

			w.Upload = nil
		}
	case "module.call":
		if moduleIDRE.MatchString(q.Module) && moduleIDRE.MatchString(q.Operation) && len(q.Config) > 0 && json.Valid(q.Config) {
			path := ram + "/web-config.json"
			e = atomicWrite(path, q.Config, 0600)
			if e == nil {
				w.LastData, e = runModuleCommand("call", q.Module, q.Operation, path)
				os.Remove(path)
			}
			if e == nil {
				message = "Action accepted"
				w.DataUntil = now.Add(30 * time.Second)
			}
		}

	case "module.action":
		if moduleIDRE.MatchString(q.Module) && moduleIDRE.MatchString(q.Operation) {
			args := []string{"call", q.Module, q.Operation}
			if q.Operation == "enable" || q.Operation == "disable" || q.Operation == "diagnose" || q.Operation == "status" {
				args = []string{q.Operation, q.Module}
			}

			w.LastData, e = runModuleCommand(args...)
			if e == nil {
				message = "Action accepted"
				w.DataUntil = now.Add(30 * time.Second)
			}
		}

	}
	// Never expose raw parsing/errors: these can contain a URL or secret content.

	w.LastID = q.ID
	w.LastOK = e == nil
	w.LastMessage = message

	return q.ID, e == nil, message
}

var runModuleCommand = moduleCommand

// Commands are fixed argv, never shell input. Bound returned JSON for the stock mailbox.
func moduleCommand(args ...string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, "/jffs/ocfusion-platform/extctl", args...).Output()
	if err != nil {
		return nil, err
	}

	var compact bytes.Buffer
	if json.Compact(&compact, output) != nil || compact.Len() > 2048 {
		return nil, errors.New("invalid/oversized module response")
	}

	return json.RawMessage(compact.Bytes()), nil
}
