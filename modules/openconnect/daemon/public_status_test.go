// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPublicStatusProjectionRedactsPrivateFields(t *testing.T) {
	private := Status{
		State:          "WAIT_NETWORK",
		Message:        "secret error https://private.example/?token=secret",
		RequestData:    json.RawMessage(`{"private_key":"secret","password":"secret"}`),
		RequestMessage: "secret",
		ImportID:       "secret",
		FusionName:     "OCFusion (managed)",
		Networks:       []Network{{Bridge: "br55", CIDR: "192.168.52.0/24"}},
	}
	public := publicStatusProjection("default", private)
	raw, _ := json.Marshal(public)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "private.example") || len(public.Networks) != 1 || public.State != "WAIT_NETWORK" {
		t.Fatal("unsafe/incomplete public projection", string(raw))
	}

	private.State = "secret"
	public = publicStatusProjection("default", private)
	if public.State != "UNKNOWN" || public.FusionName != "" {
		t.Fatal("unknown state was published", public)
	}
}

func TestPublicStatusDoesNotRefreshStaleProfile(t *testing.T) {
	directory := t.TempDir()
	if err := saveJSON(directory+"/status.json", Status{State: "UP"}); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-time.Minute)
	os.Chtimes(directory+"/status.json", old, old)
	status := publicProfileStatusAt("default", directory)
	if status.State != "UNKNOWN" {
		t.Fatal("stale UP republished", status)
	}
}
