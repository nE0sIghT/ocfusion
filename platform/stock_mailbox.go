// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

func serveMailbox(done <-chan struct{}) {
	transport, err := loadTransport()
	if err != nil {
		return
	}

	mailbox := &webMailbox{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			value := nvget(transport.Key)
			if !strings.HasPrefix(value, "# EXT-REQUEST1 ") {
				continue
			}

			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "# EXT-REQUEST1 "))
			if err == nil {
				mailbox.apply(raw, time.Now())
			}

			response := map[string]any{"request_id": mailbox.LastID, "request_ok": mailbox.LastOK, "request_message": mailbox.LastMessage, "request_data": mailbox.LastData}
			data, _ := json.Marshal(response)
			if len(data) > 2800 {
				response["request_data"] = nil
				response["request_ok"] = false
				response["request_message"] = "Module response exceeds ASUS mailbox"
				data, _ = json.Marshal(response)
			}

			replyMailbox(transport.Key, value, data)
		}
	}
}

// Replies are transient: persist module settings through their own lifecycle,
// never commit a transport acknowledgement to flash.
func replyMailbox(key, request string, data []byte) {
	if nvget(key) == request {
		nvset(key, "# OCF-STATUS1 "+base64.StdEncoding.EncodeToString(data))
	}
}
