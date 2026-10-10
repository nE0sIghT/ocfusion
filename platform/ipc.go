// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"net"
	"time"
)

type Request struct {
	Op     string          `json:"operation"`
	Action string          `json:"action,omitempty"`
	ID     string          `json:"module,omitempty"`
	Path   string          `json:"path,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

func rpc(q Request) (Response, error) {
	var r Response
	c, e := net.DialTimeout("unix", socket, 3*time.Second)
	if e != nil {
		return r, e
	}

	defer c.Close()
	c.SetDeadline(time.Now().Add(90 * time.Second))
	if e = json.NewEncoder(c).Encode(q); e != nil {
		return r, e
	}

	e = json.NewDecoder(c).Decode(&r)
	if e == nil && !r.OK {
		e = errors.New(r.Error)
	}

	return r, e
}
