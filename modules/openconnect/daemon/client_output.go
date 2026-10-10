// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bufio"
	"io"
	"strings"
)

// Drain every line, even very large route/header dumps. Never let output parsing
// stop draining the child pipe and deadlock the client. Keep only a bounded prefix.
func readClientLines(input io.Reader, emit func(string)) {
	r := bufio.NewReaderSize(input, 4096)
	prefix := make([]byte, 0, 8192)

	for {
		part, more, err := r.ReadLine()
		remaining := 8192 - len(prefix)
		if remaining > len(part) {
			remaining = len(part)
		}

		if remaining > 0 {
			prefix = append(prefix, part[:remaining]...)
		}

		if !more && len(prefix) > 0 {
			emit(string(prefix))

			prefix = prefix[:0]
		}

		if err != nil {
			return
		}
	}
}

// Return fixed messages only: URLs, certificate DNs and auth data never enter status.
func clientStage(line string) string {
	line = strings.ToLower(line)

	switch {
	case strings.Contains(line, "script") && (strings.Contains(line, "failed") || strings.Contains(line, "error") || strings.Contains(line, "exited")):
		return "OpenConnect network script failed"
	case strings.Contains(line, "argument list too long"):
		return "Client or network script environment exceeded exec limit"
	case strings.Contains(line, "got connect response"):
		return "CSTP response received; waiting for network script event"
	case strings.Contains(line, "established dtls"):
		return "DTLS established; waiting for network script event"
	case strings.Contains(line, "connected to https"):
		return "HTTPS TLS established; authenticating/creating CSTP tunnel"
	case strings.Contains(line, "ssl negotiation"):
		return "TLS negotiation in progress"
	case strings.Contains(line, "using client certificate"):
		return "Client certificate loaded"
	}

	return ""
}
