// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type serviceTransport func(*http.Request) (*http.Response, error)

func (transport serviceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestServiceFeedsAreDataOnlyAndNonempty(t *testing.T) {
	for _, data := range []string{"", "# comment", "https://example.com/", "*.example.com", "example.com;echo bad", "127.0.0.1", "<html>error</html>"} {
		if _, err := parseServiceFeed([]byte(data), false); err == nil {
			t.Fatal("invalid domain data accepted", data)
		}
	}
	for _, data := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/8", "169.254.0.0/16", "invalid", "192.0.2.1\nbroken"} {
		if _, err := parseServiceFeed([]byte(data), true); err == nil {
			t.Fatal("invalid subnet data accepted", data)
		}
	}
	zones, zoneErr := parseServiceFeed([]byte(".ua"), false)
	if zoneErr != nil || string(workerListData(zones, true)) != "ua\n" {
		t.Fatal("domain zone normalization", zones, zoneErr)
	}
	values, err := parseServiceFeed([]byte("# heading\nEXAMPLE.com.\nexample.com # same\na.example.com\n"), false)
	if err != nil || strings.Join(values, ",") != "a.example.com,example.com" {
		t.Fatal(values, err)
	}
	values, err = parseServiceFeed([]byte("203.0.113.1/24\n2001:db8::/32"), true)
	if err != nil || strings.Join(values, ",") != "2001:db8::/32,203.0.113.0/24" {
		t.Fatal(values, err)
	}
	if _, err = parseServiceFeed([]byte(strings.Repeat("x", maxFeedBytes+1)), false); err == nil {
		t.Fatal("unbounded feed")
	}
}

func TestServiceFetchUsesOnePinnedCommitAndRejectsPartial(t *testing.T) {
	service, _ := knownService("telegram")
	commit := strings.Repeat("a", 40)
	calls := 0
	fail := false
	client := &http.Client{Transport: serviceTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "raw.githubusercontent.com" || !strings.Contains(request.URL.Path, "/"+commit+"/") {
			t.Fatal(request.URL)
		}
		calls++
		content := "telegram.org\nt.me\n"
		if strings.Contains(request.URL.Path, "/IPv4/") {
			content = "149.154.160.0/20\n"
		}
		if strings.Contains(request.URL.Path, "/IPv6/") {
			content = "2001:b28:f23d::/48\n"
			if fail {
				return nil, errors.New("interrupted")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(content)), Header: http.Header{}}, nil
	})}
	snapshot, err := fetchService(context.Background(), client, service, commit)
	if err != nil || calls != 3 || len(snapshot.Domains) != 2 || len(snapshot.Networks) != 2 || snapshot.Commit != commit || snapshot.Checksum != serviceChecksum(snapshot) {
		t.Fatal(snapshot, err, calls)
	}
	fail = true
	if _, err = fetchService(context.Background(), client, service, commit); err == nil {
		t.Fatal("partial service snapshot accepted")
	}
}

func TestServiceSelectionPreservesManualDataAndCache(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("EXT_CONFIG_PATH", filepath.Join(directory, "config.json"))
	store := newServiceStore()
	snapshot := ServiceSnapshot{ID: "telegram", Commit: strings.Repeat("a", 40), Digest: strings.Repeat("b", 64), Updated: time.Now().Unix(), Domains: []string{"telegram.org"}, Networks: []string{"149.154.160.0/20"}}
	snapshot.Checksum = serviceChecksum(snapshot)
	if err := os.MkdirAll(store.directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.directory, "telegram.json")
	if err := writeJSON(path, snapshot); err != nil {
		t.Fatal(err)
	}
	store = newServiceStore()
	c := defaultConfig()
	c.Lists = []string{"telegram"}
	c.Domains = []string{"example.com"}
	c.Networks = []string{"203.0.113.1/32"}
	effective := store.effective(c)
	if len(effective.Domains) != 2 || len(effective.Networks) != 2 || !effective.ManagedSets {
		t.Fatal(effective)
	}
	if len(c.Domains) != 1 || c.Domains[0] != "example.com" || len(c.Networks) != 1 {
		t.Fatal("manual configuration mutated", c)
	}
	c.Lists = nil
	if result := store.effective(c); len(result.Domains) != 1 || result.ManagedSets {
		t.Fatal("deselected list retained", result)
	}
	// A failed download leaves both runtime entries and the retained cache intact.
	c.Lists = []string{"telegram"}
	store.client = &http.Client{Transport: serviceTransport(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/git/ref/") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"object":{"sha":"` + strings.Repeat("a", 40) + `"}}`)), Header: http.Header{}}, nil
		}
		return nil, errors.New("source unavailable")
	})}
	store.refresh(context.Background(), []string{"telegram"}, true)
	store.wait()
	state := ""
	for _, status := range store.statuses() {
		if status.ID == "telegram" {
			state = status.State
			break
		}
	}
	if state != "ERROR" || len(store.effective(c).Domains) != 2 {
		t.Fatal("failed update state")
	}
	if _, err := loadServiceSnapshot(path, "telegram"); err != nil {
		t.Fatal(err)
	}
	snapshot.Domains = []string{"changed.example"}
	if err := writeJSON(path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServiceSnapshot(path, "telegram"); err == nil {
		t.Fatal("corrupted cache accepted")
	}
}

func TestServiceIDsAndPresetsCannotWidenSelection(t *testing.T) {
	for _, ids := range [][]string{{"unknown"}, {"telegram", "telegram"}, {"../../etc/passwd"}} {
		data, _ := json.Marshal(map[string]any{"lists": ids})
		if _, err := parseConfig(data); err == nil {
			t.Fatal(ids)
		}
	}
	c := defaultConfig()
	c.Lists = []string{"telegram"}
	for _, preset := range presetCatalog() {
		if _, ok := preset.Settings["lists"]; ok {
			t.Fatal("preset overwrites list choices")
		}
		if next := presetConfig(c, preset); len(next.Lists) != 1 || next.Lists[0] != "telegram" {
			t.Fatal("preset dropped list choices")
		}
	}
}

func TestManagedSubnetsUseBoundedFirewallRules(t *testing.T) {
	old := execute
	defer func() { execute = old }()
	execute = func(tool string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "route show") {
			return "default dev ppp0", nil
		}
		return "", nil
	}
	c := defaultConfig()
	c.ManagedSets = true
	for i := 0; i < 2000; i++ {
		c.Networks = append(c.Networks, "203.0.113.0/24")
	}
	resources, err := allocate(c)
	if err != nil || len(resources.Rules) != 4 {
		t.Fatal("one rule per subnet", len(resources.Rules), err)
	}
	for _, rule := range resources.Rules {
		text := " " + strings.Join(rule.Args, " ") + " "
		if strings.Contains(text, " -s ") || strings.Contains(text, " -d ") {
			t.Fatal("managed addresses bypassed engine selection")
		}
	}
}

func TestLiveServiceSources(t *testing.T) {
	if os.Getenv("OCF_VERIFY_SERVICE_FEEDS") != "1" {
		t.Skip("explicit live-source check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := serviceHTTP()
	// The development environment may require an HTTP CONNECT proxy. This
	// explicit source-format check does not change the router's downloader.
	client.Transport.(*http.Transport).Proxy = http.ProxyFromEnvironment
	defer client.CloseIdleConnections()
	commit, err := serviceCommit(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range serviceCatalog() {
		snapshot, err := fetchService(ctx, client, service, commit)
		if err != nil {
			t.Fatalf("%s: %v", service.ID, err)
		}
		t.Logf("%s: %d domains, %d subnets; source %s", service.ID, len(snapshot.Domains), len(snapshot.Networks), commit)
	}
}

func TestServiceStoreUpdatesInBackgroundAndSchedules(t *testing.T) {
	t.Setenv("EXT_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	store := newServiceStore()
	calls := 0
	store.client = &http.Client{Transport: serviceTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		content := "youtube.com\ngooglevideo.com\n"
		if strings.Contains(request.URL.Path, "/git/ref/") {
			content = `{"object":{"sha":"` + strings.Repeat("a", 40) + `"}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(content)), Header: http.Header{}}, nil
	})}
	store.refresh(context.Background(), []string{"youtube"}, false)
	store.wait()
	if calls != 2 {
		t.Fatal("unexpected download count", calls)
	}
	snapshot, err := loadServiceSnapshot(filepath.Join(store.directory, "youtube.json"), "youtube")
	if err != nil || len(snapshot.Domains) != 2 {
		t.Fatal(snapshot, err)
	}
	store.refresh(context.Background(), []string{"youtube"}, false)
	store.wait()
	if calls != 2 {
		t.Fatal("download repeated before update interval")
	}
	store.refresh(context.Background(), []string{"youtube"}, true)
	store.wait()
	if calls != 4 {
		t.Fatal("manual refresh ignored")
	}
	c := defaultConfig()
	c.Lists = []string{"youtube"}
	if effective := store.effective(c); len(effective.Domains) != 2 || !hasSelection(effective) {
		t.Fatal("downloaded list not applied", effective)
	}
}
