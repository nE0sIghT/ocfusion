// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"
)

const serviceSource = "https://github.com/itdoginfo/allow-domains"
const serviceRaw = "https://raw.githubusercontent.com/itdoginfo/allow-domains/"
const maxFeedBytes = 1 << 20

var serviceZonePattern = regexp.MustCompile(`^\.[a-z]{2,63}$`)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func serviceHTTP() *http.Client {
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 32768},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("list source redirect rejected")
		},
	}
}

func fetchServiceBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "OCFusion-service-lists")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("list source unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxFeedBytes+1))
	if err != nil || len(data) > maxFeedBytes {
		return nil, errors.New("list response exceeds limits")
	}
	return data, nil
}

func serviceCommit(ctx context.Context, client *http.Client) (string, error) {
	data, err := fetchServiceBytes(ctx, client, "https://api.github.com/repos/itdoginfo/allow-domains/git/ref/heads/main")
	if err != nil {
		return "", err
	}
	var reference struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if json.Unmarshal(data, &reference) != nil || !commitPattern.MatchString(reference.Object.SHA) {
		return "", errors.New("invalid list source revision")
	}
	return reference.Object.SHA, nil
}

func parseServiceFeed(data []byte, networks bool) ([]string, error) {
	if len(data) > maxFeedBytes {
		return nil, errors.New("list exceeds limits")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		value := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if value == "" {
			continue
		}
		if networks {
			if address, err := netip.ParseAddr(value); err == nil {
				value = netip.PrefixFrom(address, address.BitLen()).String()
			}
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsPrivate() {
				return nil, errors.New("invalid service subnet")
			}
			value = prefix.Masked().String()
		} else {
			value = strings.ToLower(strings.TrimSuffix(value, "."))
			if len(value) > 253 || (!domainPattern.MatchString(value) && !serviceZonePattern.MatchString(value)) || net.ParseIP(value) != nil {
				return nil, errors.New("invalid service domain")
			}
		}
		seen[value] = true
		if len(seen) > 50000 {
			return nil, errors.New("too many list entries")
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("empty list rejected")
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values, nil
}

func uniqueEntries(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func fetchService(ctx context.Context, client *http.Client, service ServiceList, commit string) (ServiceSnapshot, error) {
	snapshot := ServiceSnapshot{ID: service.ID, Commit: commit}
	if !commitPattern.MatchString(commit) {
		return snapshot, errors.New("invalid source revision")
	}
	hash := sha256.New()
	for _, path := range service.Paths {
		data, err := fetchServiceBytes(ctx, client, serviceRaw+commit+"/"+path)
		if err != nil {
			return snapshot, err
		}
		values, err := parseServiceFeed(data, strings.HasPrefix(path, "Subnets/"))
		if err != nil {
			return snapshot, err
		}
		hash.Write([]byte(path + "\x00"))
		hash.Write(data)
		if strings.HasPrefix(path, "Subnets/") {
			snapshot.Networks = append(snapshot.Networks, values...)
		} else {
			snapshot.Domains = append(snapshot.Domains, values...)
		}
	}
	snapshot.Domains = uniqueEntries(snapshot.Domains)
	snapshot.Networks = uniqueEntries(snapshot.Networks)
	snapshot.Digest = hex.EncodeToString(hash.Sum(nil))
	snapshot.Updated = time.Now().Unix()
	snapshot.Checksum = serviceChecksum(snapshot)
	return snapshot, nil
}
