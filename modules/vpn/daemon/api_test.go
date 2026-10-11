// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSocketAPIAndPersistentRestart(t *testing.T) {
	fixtureBroker(t)
	previousRun := runDir
	runDir = t.TempDir()
	t.Cleanup(func() { runDir = previousRun })

	start := func() (chan struct{}, chan error) {
		stop := make(chan struct{})
		result := make(chan error, 1)
		go func() { result <- serveUntil(stop) }()
		for attempt := 0; attempt < 100; attempt++ {
			connection, err := net.Dial("unix", filepath.Join(runDir, "control.sock"))
			if err == nil {
				connection.Close()
				return stop, result
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(stop)
		t.Fatal("provider socket did not start")

		return nil, nil
	}
	call := func(request Request) Response {
		t.Helper()
		connection, err := net.Dial("unix", filepath.Join(runDir, "control.sock"))
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(time.Second))
		if err = json.NewEncoder(connection).Encode(request); err != nil {
			t.Fatal(err)
		}
		var response Response
		if err = json.NewDecoder(connection).Decode(&response); err != nil {
			t.Fatal(err)
		}

		return response
	}

	stop, result := start()
	first := call(Request{API: 1, Client: "openconnect", Profile: "default", Operation: "claim"})
	second := call(Request{API: 1, Client: "other-vpn", Profile: "office", Operation: "claim"})
	if !first.OK || !second.OK || first.Manifest.ID == second.Manifest.ID {
		t.Fatal("independent socket claims failed", first, second)
	}
	foreign := call(Request{API: 1, Client: "openconnect", Profile: "default", Connection: second.Manifest.ID, Operation: "down"})
	if foreign.OK {
		t.Fatal("socket accepted foreign connection")
	}
	close(stop)
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	stop, result = start()
	state := call(Request{API: 1, Client: "other-vpn", Profile: "office", Connection: second.Manifest.ID, Operation: "state"})
	if !state.OK || state.Manifest.Interface != second.Manifest.Interface {
		t.Fatal("resources changed after provider restart", state)
	}
	list := call(Request{API: 1, Client: "other-vpn", Profile: "office", Operation: "list"})
	if !list.OK || len(list.Connections) != 1 || list.Connections[0].ID != second.Manifest.ID {
		t.Fatal("owner listing incorrect", list)
	}
	close(stop)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
