// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var markerPattern = regexp.MustCompile(`^(?:-?[1-9][0-9]{0,4}|(?:method|host|endhost|sld|endsld|midsld|sniext|extlen)(?:[+-][0-9]{1,4})?)$`)

func validateStrategy(c Config) error {
	switch c.TCP {
	case "off", "split", "disorder", "fake", "fake-split", "fake-disorder", "fakedsplit", "fakeddisorder", "hostfakesplit":
	default:
		return errors.New("invalid TCP strategy")
	}
	switch c.UDP {
	case "off", "fake-quic", "fragment-quic", "fake-fragment-quic", "hopbyhop-quic", "destopt-quic", "hopbyhop-destopt-quic":
	default:
		return errors.New("invalid UDP strategy")
	}
	if c.UDPFragment < 8 || c.UDPFragment > 1200 || c.UDPFragment%8 != 0 {
		return errors.New("invalid QUIC fragment position")
	}
	if c.UDPProtection != "none" && c.UDPProtection != "ttl" {
		return errors.New("invalid QUIC fake protection")
	}
	if c.Fooling != "ttl" && c.Fooling != "badseq" && c.Fooling != "md5" && c.Fooling != "badsum" {
		return errors.New("invalid fake packet protection")
	}
	if c.Overlap < 0 || c.Overlap > 4096 || c.AutoDelta < -20 || c.AutoDelta > 20 || c.AutoMin < 1 || c.AutoMax > 255 || c.AutoMax < c.AutoMin || c.PacketLimit < 4 || c.PacketLimit > 64 {
		return errors.New("strategy settings exceed limits")
	}
	if c.FakeSNI != "" && (len(c.FakeSNI) > 253 || !domainPattern.MatchString(c.FakeSNI)) {
		return errors.New("invalid fake TLS server name")
	}
	if c.Positions != "" {
		markers := strings.Split(c.Positions, ",")
		if len(markers) > 8 {
			return errors.New("at most eight split positions")
		}
		for _, marker := range markers {
			if !markerPattern.MatchString(marker) {
				return errors.New("invalid split position")
			}
		}
		if (c.TCP == "fakedsplit" || c.TCP == "fakeddisorder") && len(markers) != 1 {
			return errors.New("this strategy requires one split position")
		}
	}

	return nil
}

func splitPositions(c Config) string {
	if c.Positions != "" {
		return c.Positions
	}

	return strconv.Itoa(c.Split)
}

func fakeOptions(c Config, tcp bool) string {
	options := ":ip_ttl=" + strconv.Itoa(c.TTL) + ":ip6_ttl=" + strconv.Itoa(c.TTL)
	if c.AutoTTL {
		value := strconv.Itoa(c.AutoDelta) + "," + strconv.Itoa(c.AutoMin) + "-" + strconv.Itoa(c.AutoMax)
		options += ":ip_autottl=" + value + ":ip6_autottl=" + value
	}
	if tcp {
		switch c.Fooling {
		case "badseq":
			options += ":tcp_seq=-10000"
		case "md5":
			options += ":tcp_md5"
		case "badsum":
			options += ":badsum"
		}
	}

	return options
}

func tcpStrategy(c Config) []string {
	c = protocolConfig(c, "tcp")
	args := []string{}
	if c.TCP == "fake" || c.TCP == "fake-split" || c.TCP == "fake-disorder" {
		repeats := ":repeats=" + strconv.Itoa(c.Repeats)
		fakeTLS := "--lua-desync=fake:blob=fake_default_tls" + fakeOptions(c, true) + repeats
		if c.FakeSNI != "" {
			fakeTLS += ":tls_mod=rnd,dupsid,sni=" + c.FakeSNI
		}
		args = append(args,
			"--payload=http_req",
			"--lua-desync=fake:blob=fake_default_http"+fakeOptions(c, true)+repeats,
			"--payload=tls_client_hello", fakeTLS,
		)
	}
	args = append(args, "--payload=http_req,tls_client_hello")

	switch c.TCP {
	case "split", "fake-split", "disorder", "fake-disorder":
		function := "multisplit"
		if c.TCP == "disorder" || c.TCP == "fake-disorder" {
			function = "multidisorder"
		}
		value := "--lua-desync=" + function + ":pos=" + splitPositions(c)
		if c.Overlap > 0 {
			value += ":seqovl=" + strconv.Itoa(c.Overlap)
		}
		args = append(args, value)
	case "fakedsplit", "fakeddisorder":
		value := "--lua-desync=" + c.TCP + ":pos=" + splitPositions(c) + fakeOptions(c, true)
		if c.Overlap > 0 {
			value += ":seqovl=" + strconv.Itoa(c.Overlap)
		}
		args = append(args, value)
	case "hostfakesplit":
		host := c.FakeSNI
		if host == "" {
			host = "example.com"
		}
		args = append(args, "--lua-desync=hostfakesplit:host="+host+fakeOptions(c, true))
	}

	return args
}

func workerArgs(c Config, r Resources, directory string, uid int) []string {
	args := []string{
		"--qnum=" + strconv.Itoa(r.Queue),
		"--fwmark=" + r.Mark,
		"--uid=" + strconv.Itoa(uid) + ":" + strconv.Itoa(uid),
		"--bind-fix4", "--bind-fix6",
		"--quic-initial-passthrough",
		"--lua-init=@" + filepath.Join(directory, "zapret-lib.lua"),
		"--lua-init=@" + filepath.Join(directory, "zapret-antidpi.lua"),
		"--lua-init=@" + filepath.Join(directory, "ocfusion-strategies.lua"),
	}
	for _, blob := range applicationBlobs() {
		args = append(args, "--blob="+blob.Name+":@"+filepath.Join(directory, blob.File))
	}
	for _, selected := range processingConfigs(c) {
		profiles := workerProfiles(selected, directory)
		if len(profiles) > 0 {
			if len(args) > 0 && hasWorkerProfiles(args) {
				args = append(args, "--new")
			}
			args = append(args, profiles...)
		}
		if selected.SelectorPrefix != "" {
			args = appendPassProfiles(args, selected, directory)
		}
	}
	return args
}

func hasWorkerProfiles(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--filter-") {
			return true
		}
	}
	return false
}

func appendPassProfiles(args []string, c Config, directory string) []string {
	for _, selector := range []string{"networks", "domains"} {
		if selector == "networks" && len(c.Networks) == 0 || selector == "domains" && len(c.Domains) == 0 {
			continue
		}
		for _, transport := range []string{"tcp", "udp"} {
			if hasWorkerProfiles(args) {
				args = append(args, "--new")
			}
			args = append(args, "--filter-"+transport+"=*")
			flag := "--ipset="
			if selector == "domains" {
				flag = "--hostlist="
			}
			args = append(args, flag+filepath.Join(directory, c.SelectorPrefix+selector+".txt"))
		}
	}
	return args
}

func workerProfiles(c Config, directory string) []string {
	args := []string{}
	profiles := 0
	for _, selector := range []string{"networks", "domains"} {
		if (selector == "networks" && len(c.Networks) == 0) || (selector == "domains" && len(c.Domains) == 0) {
			continue
		}
		for _, protocol := range []string{"tcp", "udp"} {
			ports, strategy := c.TCPPorts, c.TCP
			if protocol == "udp" {
				ports, strategy = c.UDPPorts, c.UDP
			}
			if len(ports) == 0 || strategy == "off" {
				continue
			}
			if profiles > 0 {
				args = append(args, "--new")
			}
			profiles++
			args = append(args, "--filter-"+protocol+"="+portList(ports))
			if protocol == "tcp" {
				args = append(args, "--filter-l7=http,tls")
			} else {
				args = append(args, "--filter-l7=quic")
			}
			if selector == "networks" {
				args = append(args, "--ipset="+filepath.Join(directory, c.SelectorPrefix+"networks.txt"))
			} else {
				args = append(args, "--hostlist="+filepath.Join(directory, c.SelectorPrefix+"domains.txt"))
			}
			args = append(args, "--out-range=-n"+strconv.Itoa(protocolConfig(c, protocol).PacketLimit))
			if protocol == "tcp" {
				args = append(args, tcpStrategy(c)...)
			} else {
				args = append(args, quicStrategy(c)...)
			}
		}
	}

	if len(c.Networks) > 0 {
		for _, profile := range c.Applications {
			kind, _ := applicationKind(profile.ID)
			if profiles > 0 {
				args = append(args, "--new")
			}
			profiles++
			args = append(args, "--filter-"+kind.Transport+"="+profile.Ports, "--filter-l7="+kind.L7, "--ipset="+filepath.Join(directory, c.SelectorPrefix+"networks.txt"))
			args = append(args, applicationStrategy(c, profile, kind)...)
		}
	}

	return args
}
