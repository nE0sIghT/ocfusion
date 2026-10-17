// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// Fixed declared actions only; no untrusted paths or shell commands.
const moduleResponseLimit = 16 * 1024

var moduleIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type webRequest struct {
	ID        string          `json:"id"`
	Action    string          `json:"action"`
	Module    string          `json:"module,omitempty"`
	Operation string          `json:"operation,omitempty"`
	Config    json.RawMessage `json:"config,omitempty"`
}

type webMailbox struct {
	LastID      string
	LastOK      bool
	LastMessage string
	LastData    json.RawMessage
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
	if len(raw) > 24576 || json.Unmarshal(raw, &q) != nil || !validNonce(q.ID) {
		return "", false, "Invalid request"
	}

	if q.ID == w.LastID {
		return q.ID, w.LastOK, w.LastMessage
	}

	w.LastData = nil
	e := errors.New("Unsupported action")
	message := "Request rejected"

	switch q.Action {
	case "module.configure":
		if moduleIDRE.MatchString(q.Module) && len(q.Config) > 0 && len(q.Config) <= 16384 && json.Valid(q.Config) {
			path := ram + "/web-config.json"
			e = atomicWrite(path, q.Config, 0600)
			if e == nil {
				w.LastData, e = runModuleCommand("config", q.Module, path)
				os.Remove(path)
			}

			if e == nil {
				message = "Settings saved"
			}
		}

	case "module.call":
		if moduleIDRE.MatchString(q.Module) && moduleIDRE.MatchString(q.Operation) && len(q.Config) > 0 && len(q.Config) <= 16384 && json.Valid(q.Config) {
			path := ram + "/web-config.json"
			e = atomicWrite(path, q.Config, 0600)
			if e == nil {
				w.LastData, e = runModuleCommand("call", q.Module, q.Operation, path)
				os.Remove(path)
			}
			if e == nil {
				message = "Action accepted"
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

// Commands are fixed argv, never shell input. Bound returned JSON for the RAM transport.
func moduleCommand(args ...string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, "/jffs/ocfusion-platform/extctl", args...).Output()
	if err != nil {
		return nil, err
	}

	return compactModuleResponse(output)
}

func compactModuleResponse(output []byte) (json.RawMessage, error) {
	var compact bytes.Buffer
	if json.Compact(&compact, output) != nil || compact.Len() > moduleResponseLimit {
		return nil, errors.New("invalid/oversized module response")
	}

	return json.RawMessage(compact.Bytes()), nil
}
