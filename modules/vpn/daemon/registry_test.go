// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixtureBroker(t *testing.T) (*Broker, map[string]string) {
	t.Helper()
	oldRoot, oldTransport, oldRunner := root, transportPath, runner
	root = t.TempDir()
	transportPath = filepath.Join(t.TempDir(), "transport.json")
	os.WriteFile(transportPath, []byte(`{"unit":4,"vpnc_idx":7,"key":"vpn_client4_custom"}`), 0600)
	t.Cleanup(func() {
		root, transportPath, runner = oldRoot, oldTransport, oldRunner
	})

	values := map[string]string{
		"firmver": "test", "buildno": "102", "extendno": "build",
		"vpnc_clientlist": "OCFusion (managed)>OpenVPN>4>>>0>7>>>0>0>Web",
	}
	runner = func(name string, args ...string) (string, error) {
		if name == "nvram" && args[0] == "get" {
			return values[args[1]], nil
		}
		if name == "nvram" && args[0] == "set" {
			pair := strings.SplitN(args[1], "=", 2)
			values[pair[0]] = pair[1]
		}
		if name == "ip" && len(args) > 1 && args[0] == "link" {
			return "", errors.New("interface absent")
		}

		return "", nil
	}

	return newBroker(Connections{Schema: 1, Items: map[string]Manifest{}}), values
}

func TestIndependentVPNConsumers(t *testing.T) {
	broker, values := fixtureBroker(t)
	first, err := broker.handle(Request{API: 1, Client: "openconnect", Profile: "default", Operation: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := broker.handle(Request{API: 1, Client: "other-vpn", Profile: "office", Operation: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := first.Manifest, second.Manifest
	if a.Index == b.Index || a.Unit == b.Unit || a.Interface == b.Interface || a.Table == b.Table || a.Priority == b.Priority {
		t.Fatal("resources overlap", a, b)
	}

	repeated, err := broker.handle(Request{API: 1, Client: "other-vpn", Profile: "office", Operation: "claim"})
	if err != nil || !reflect.DeepEqual(repeated.Manifest, b) || len(broker.registry.Items) != 2 {
		t.Fatal("claim not idempotent", err)
	}
	before := values["vpnc_clientlist"]
	if _, err := broker.handle(Request{API: 1, Client: "openconnect", Profile: "default", Connection: b.ID, Operation: "down"}); err == nil {
		t.Fatal("foreign operation accepted")
	}
	if values["vpnc_clientlist"] != before {
		t.Fatal("foreign operation changed native state")
	}
	if _, err := broker.handle(Request{API: 1, Client: "other-vpn", Profile: "office", Connection: b.ID, Operation: "release"}); err != nil {
		t.Fatal(err)
	}
	if len(broker.registry.Items) != 1 || !strings.Contains(values["vpnc_clientlist"], "OCFusion (managed)") || strings.Contains(values["vpnc_clientlist"], b.Name) {
		t.Fatal("removal damaged native transport", values)
	}
}

func TestNativeCapacity(t *testing.T) {
	broker, values := fixtureBroker(t)
	values["vpnc_clientlist"] = ""
	for _, profile := range []string{"one", "two", "three", "four", "five"} {
		if _, err := broker.handle(Request{API: 1, Client: "consumer", Profile: profile, Operation: "claim"}); err != nil {
			t.Fatal(err)
		}
	}
	native := values["vpnc_clientlist"]
	if _, err := broker.handle(Request{API: 1, Client: "consumer", Profile: "six", Operation: "claim"}); err == nil {
		t.Fatal("capacity ignored")
	}
	if values["vpnc_clientlist"] != native || len(broker.registry.Items) != 5 {
		t.Fatal("capacity failure changed state")
	}
}

func TestMigrationPreserves029Resources(t *testing.T) {
	fixtureBroker(t)
	old := Manifest{Schema: 1, Unit: 4, Index: 7, Table: 51998, Priority: 90, Firmware: "test_102_build"}
	saveJSON(filepath.Join(root, "connection.json"), old)
	registry, err := readConnections()
	if err != nil {
		t.Fatal(err)
	}
	migrated := registry.Items["openconnect-default"]
	if migrated.Unit != old.Unit || migrated.Index != old.Index || migrated.Table != old.Table || migrated.Priority != old.Priority || migrated.Interface != "ocvpn0" || migrated.Shared {
		t.Fatal("migration changed resources", migrated)
	}
	if err := writeConnections(registry); err != nil {
		t.Fatal(err)
	}
	again, err := readConnections()
	if err != nil || !reflect.DeepEqual(registry, again) {
		t.Fatal("migration not idempotent", err)
	}
}

func TestFailedAllocationRestoresNativeState(t *testing.T) {
	broker, values := fixtureBroker(t)
	if _, err := broker.handle(Request{API: 1, Client: "first-vpn", Profile: "default", Operation: "claim"}); err != nil {
		t.Fatal(err)
	}
	before := values["vpnc_clientlist"]
	os.RemoveAll(root)
	os.WriteFile(root, []byte("blocked state directory"), 0600)
	if _, err := broker.handle(Request{API: 1, Client: "other-vpn", Profile: "default", Operation: "claim"}); err == nil {
		t.Fatal("failed persistence accepted")
	}
	if values["vpnc_clientlist"] != before || len(broker.registry.Items) != 1 {
		t.Fatal("failed allocation was not rolled back")
	}
}

func TestDisconnectDoesNotTouchOtherTunnel(t *testing.T) {
	broker, _ := fixtureBroker(t)
	a, _ := broker.handle(Request{API: 1, Client: "first-vpn", Profile: "one", Operation: "claim"})
	b, _ := broker.handle(Request{API: 1, Client: "other-vpn", Profile: "two", Operation: "claim"})
	calls := []string{}
	original := runner
	runner = func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return original(name, args...)
	}
	broker.services[b.Manifest.ID].event = &Tunnel{Interface: b.Manifest.Interface}
	if _, err := broker.handle(Request{API: 1, Client: "first-vpn", Profile: "one", Connection: a.Manifest.ID, Operation: "down"}); err != nil {
		t.Fatal(err)
	}
	if broker.services[b.Manifest.ID].event == nil {
		t.Fatal("other client state cleared")
	}
	for _, call := range calls {
		if strings.Contains(call, "dev "+b.Manifest.Interface) {
			t.Fatal("other interface touched", call)
		}
	}
}

func TestExpiredConnectionDoesNotExpireOtherOwner(t *testing.T) {
	broker, _ := fixtureBroker(t)
	first, _ := broker.handle(Request{API: 1, Client: "first-vpn", Profile: "one", Operation: "claim"})
	second, _ := broker.handle(Request{API: 1, Client: "other-vpn", Profile: "two", Operation: "claim"})
	a := broker.services[first.Manifest.ID]
	b := broker.services[second.Manifest.ID]
	a.event = &Tunnel{Interface: a.manifest.Interface}
	a.heartbeat = time.Now().Add(-time.Minute)
	b.event = &Tunnel{Interface: b.manifest.Interface}
	b.heartbeat = time.Now()
	original := runner
	runner = func(name string, args ...string) (string, error) {
		if name == "ip" && len(args) > 1 && args[0] == "link" {
			return "exists", nil
		}
		return original(name, args...)
	}
	if err := a.reconcile(true); err == nil || a.event != nil {
		t.Fatal("expired tunnel retained")
	}
	if b.event == nil || time.Since(b.heartbeat) > time.Second {
		t.Fatal("other owner's heartbeat or event changed")
	}
}

func TestMalformedMigrationLeavesSourceUntouched(t *testing.T) {
	fixtureBroker(t)
	original := []byte(`{"schema":1,"unit":0,"vpnc_idx":7,"table":51998,"priority":90}`)
	path := filepath.Join(root, "connection.json")
	os.WriteFile(path, original, 0600)
	if _, err := readConnections(); err == nil {
		t.Fatal("malformed migration accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("malformed source rewritten")
	}
	if _, err := os.Stat(filepath.Join(root, "connections.json")); !os.IsNotExist(err) {
		t.Fatal("failed migration published registry")
	}
}

func TestSharedBindingMigrationPreservesNetworksAndClearsMailbox(t *testing.T) {
	_, values := fixtureBroker(t)
	old := Manifest{Schema: 1, Unit: 4, Index: 7, Table: 51998, Priority: 90, Firmware: "test_102_build"}
	saveJSON(filepath.Join(root, "connection.json"), old)
	values["vpn_client4_custom"] = "# OCF-STATUS1 previous"
	native := values["vpnc_clientlist"]
	values["sdn_rl"] = "unchanged network bindings"

	if err := install(); err != nil {
		t.Fatal(err)
	}

	registry, err := readConnections()
	if err != nil || registry.Items["openconnect-default"].Shared || registry.Items["openconnect-default"].Index != 7 || values["vpn_client4_custom"] != "" || values["vpnc_clientlist"] != native || values["sdn_rl"] != "unchanged network bindings" {
		t.Fatal("migration changed binding or kept mailbox", registry, values, err)
	}

	values["vpn_client4_custom"] = "administrator content"
	if err := install(); err != nil || values["vpn_client4_custom"] != "administrator content" {
		t.Fatal("repeat migration changed administrator content", err)
	}
}

func TestFreshAllocationDoesNotRequirePlatformTransport(t *testing.T) {
	broker, values := fixtureBroker(t)
	os.Remove(transportPath)
	values["vpnc_clientlist"] = ""

	response, err := broker.handle(Request{API: 1, Client: "other-vpn", Profile: "office", Operation: "claim"})
	if err != nil || response.Manifest.Shared || response.Manifest.Unit != 1 {
		t.Fatal("native binding depends on platform transport", response, err)
	}
}

func TestMigrationOfShared03Connection(t *testing.T) {
	broker, values := fixtureBroker(t)
	manifest := Manifest{
		Schema: 1, ID: "connection-7", Client: "openconnect", Profile: "default",
		Unit: 4, Index: 7, Table: 51998, Priority: 90, Interface: "ocvpn0",
		Name: fusionName, Shared: true, Firmware: "test_102_build",
	}
	registry := Connections{Schema: 1, Items: map[string]Manifest{manifest.ID: manifest}}
	if err := writeConnections(registry); err != nil {
		t.Fatal(err)
	}

	native := values["vpnc_clientlist"]
	if err := install(); err != nil {
		t.Fatal(err)
	}

	migrated, err := readConnections()
	if err != nil {
		t.Fatal(err)
	}

	expected := manifest
	expected.Shared = false
	if !reflect.DeepEqual(migrated.Items[manifest.ID], expected) || values["vpnc_clientlist"] != native {
		t.Fatal("existing connection resources changed", migrated, values)
	}

	broker = newBroker(migrated)
	response, err := broker.handle(Request{API: 1, Client: "openconnect", Profile: "default", Operation: "claim"})
	if err != nil || response.Manifest.Unit != 4 || response.Manifest.Index != 7 || values["vpnc_clientlist"] != native {
		t.Fatal("existing default allocated another binding", response, err)
	}

	if _, err := broker.handle(Request{API: 1, Client: "openconnect", Profile: "default", Connection: manifest.ID, Operation: "release"}); err != nil || strings.Contains(values["vpnc_clientlist"], ">7>") {
		t.Fatal("transferred binding still owned by platform", values, err)
	}
}
