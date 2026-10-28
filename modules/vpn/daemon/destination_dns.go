// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type DestinationAnswer struct {
	Missing   []int     `json:"-"`
	Retained  bool      `json:"retained,omitempty"`
	Addresses []string  `json:"addresses"`
	Next      time.Time `json:"next"`
	Updated   time.Time `json:"updated"`
	Failed    bool      `json:"failed,omitempty"`
}

type DestinationDNS struct {
	mu      sync.Mutex
	answers map[string]DestinationAnswer
	busy    bool
	cancel  context.CancelFunc
	done    chan struct{}
}

var lookupDestination = resolveDestination

var destinationNameservers = func() []string {
	// Use the router resolver, including AdGuard capture and stock DoT. Never
	// silently replace an administrator's resolver with a public DNS service.
	return []string{"127.0.0.1:53"}
}

func dnsExchange(ctx context.Context, server, host string, kind dnsmessage.Type) (dnsmessage.Message, error) {
	var result dnsmessage.Message
	name, err := dnsmessage.NewName(host + ".")
	if err != nil {
		return result, err
	}
	var random [2]byte
	if _, err = rand.Read(random[:]); err != nil {
		return result, err
	}
	query := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: binary.BigEndian.Uint16(random[:]), RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: kind, Class: dnsmessage.ClassINET}},
	}
	wire, err := query.Pack()
	if err != nil {
		return result, err
	}
	for _, transport := range []string{"udp", "tcp"} {
		conn, err := (&net.Dialer{}).DialContext(ctx, transport, server)
		if err != nil {
			return result, err
		}
		deadline, _ := ctx.Deadline()
		conn.SetDeadline(deadline)
		if transport == "tcp" {
			wire = append([]byte{byte(len(wire) >> 8), byte(len(wire))}, wire...)
		}
		_, err = conn.Write(wire)
		buffer := make([]byte, 65535)
		n := 0
		if err == nil && transport == "tcp" {
			var size [2]byte
			_, err = io.ReadFull(conn, size[:])
			if err == nil {
				n = int(binary.BigEndian.Uint16(size[:]))
				_, err = io.ReadFull(conn, buffer[:n])
			}
		} else if err == nil {
			n, err = conn.Read(buffer)
		}
		conn.Close()
		if err != nil {
			return result, err
		}
		if err = result.Unpack(buffer[:n]); err != nil {
			return result, err
		}
		if result.ID != query.ID || !result.Response || result.OpCode != 0 || len(result.Questions) != 1 || result.Questions[0] != query.Questions[0] {
			return result, errors.New("invalid DNS reply")
		}
		if !result.Truncated {
			if result.RCode != dnsmessage.RCodeSuccess {
				return result, errors.New("DNS lookup failed")
			}
			return result, nil
		}
	}
	return result, errors.New("truncated DNS reply")
}

func resolveDestination(ctx context.Context, host string) (DestinationAnswer, error) {
	answer := DestinationAnswer{Updated: time.Now()}
	ttl := uint32(86400)
	seen := map[string]bool{}
	var failures error
	for _, kind := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		queryContext, cancel := context.WithTimeout(ctx, 3*time.Second)
		message, err := dnsExchange(queryContext, destinationNameservers()[0], host, kind)
		cancel()
		if err != nil {
			failures = errors.Join(failures, err)
			family := 4
			if kind == dnsmessage.TypeAAAA {
				family = 6
			}
			answer.Missing = append(answer.Missing, family)
			continue
		}
		for _, resource := range message.Answers {
			var ip netip.Addr
			switch body := resource.Body.(type) {
			case *dnsmessage.AResource:
				ip = netip.AddrFrom4(body.A)
			case *dnsmessage.AAAAResource:
				ip = netip.AddrFrom16(body.AAAA)
			case *dnsmessage.CNAMEResource:
				if resource.Header.TTL < ttl {
					ttl = resource.Header.TTL
				}
			}
			if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.Is4In6() || seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			answer.Addresses = append(answer.Addresses, netip.PrefixFrom(ip, ip.BitLen()).String())
			if resource.Header.TTL < ttl {
				ttl = resource.Header.TTL
			}
		}
	}
	// Retain the failed family independently: an AAAA failure must not discard
	// working A records, nor forget previously protected IPv6 destinations.
	if len(answer.Addresses) == 0 || len(answer.Addresses) > 64 {
		return answer, errors.New("destination lookup unavailable")
	}
	if failures != nil {
		answer.Failed = true
		if ttl > 60 {
			ttl = 60
		}
	}
	if ttl < 30 {
		ttl = 30
	}
	sort.Strings(answer.Addresses)
	answer.Next = answer.Updated.Add(time.Duration(ttl) * time.Second)
	return answer, nil
}

func (d *DestinationDNS) refresh(domains []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.answers == nil {
		d.answers = map[string]DestinationAnswer{}
		data, _ := os.ReadFile(runDir + "/destination-dns.json")
		if len(data) < 16<<20 {
			var cached map[string]DestinationAnswer
			if json.Unmarshal(data, &cached) == nil && len(cached) <= 4096 {
				for host, answer := range cached {
					valid := validDestinationDomain(host) && len(answer.Addresses) <= 64
					for _, address := range answer.Addresses {
						prefix, err := netip.ParsePrefix(address)
						valid = valid && err == nil && prefix.Bits() == prefix.Addr().BitLen() && prefix.Addr().IsGlobalUnicast()
					}
					if valid {
						d.answers[host] = answer
					}
				}
			}
		}
	}
	wanted := map[string]bool{}
	for _, domain := range domains {
		wanted[domain] = true
	}
	for domain := range d.answers {
		if !wanted[domain] {
			delete(d.answers, domain)
		}
	}
	if d.busy {
		return
	}
	pending := []string{}
	for _, domain := range domains {
		if !time.Now().Before(d.answers[domain].Next) {
			pending = append(pending, domain)
		}
	}
	if len(pending) == 0 {
		return
	}
	if len(pending) > 16 {
		pending = pending[:16]
	}
	d.busy = true
	d.done = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	go func() {
		defer close(d.done)
		defer cancel()
		limit := make(chan struct{}, 4)
		var workers sync.WaitGroup
		for _, host := range pending {
			limit <- struct{}{}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-limit }()
				answer, err := lookupDestination(ctx, host)
				d.mu.Lock()
				defer d.mu.Unlock()
				if err == nil && len(answer.Missing) > 0 {
					for _, address := range d.answers[host].Addresses {
						prefix, parseErr := netip.ParsePrefix(address)
						if parseErr != nil {
							continue
						}
						for _, family := range answer.Missing {
							if prefix.Addr().Is4() == (family == 4) {
								answer.Addresses = append(answer.Addresses, address)
								answer.Retained = true
							}
						}
					}
					if len(answer.Addresses) > 64 {
						err = errors.New("destination DNS answer exceeds limits")
					}
					sort.Strings(answer.Addresses)
				}
				if err != nil {
					answer = d.answers[host]
					answer.Retained = len(answer.Addresses) > 0
					answer.Next = time.Now().Add(time.Minute)
					answer.Failed = true
				}
				if wanted[host] {
					d.answers[host] = answer
				}
			}()
		}
		workers.Wait()
		d.mu.Lock()
		defer d.mu.Unlock()
		saveJSON(runDir+"/destination-dns.json", d.answers)
		d.busy = false
	}()
}

func (d *DestinationDNS) addresses(host string) DestinationAnswer {
	d.mu.Lock()
	defer d.mu.Unlock()
	answer := d.answers[host]
	answer.Addresses = append([]string{}, answer.Addresses...)
	return answer
}

func (d *DestinationDNS) stop() {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func validDestinationDomain(host string) bool {
	if len(host) > 253 || host != strings.ToLower(host) || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
