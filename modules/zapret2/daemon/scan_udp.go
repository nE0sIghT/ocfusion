// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func stunBindingRequest() ([]byte, error) {
	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request, 1)
	binary.BigEndian.PutUint32(request[4:], 0x2112a442)
	_, err := rand.Read(request[8:])
	return request, err
}

func validSTUNResponse(request, response []byte) bool {
	if len(request) != 20 || len(response) < 20 || binary.BigEndian.Uint16(response) != 0x0101 || !bytes.Equal(request[4:20], response[4:20]) {
		return false
	}
	size := int(binary.BigEndian.Uint16(response[2:4]))
	if size%4 != 0 || size+20 != len(response) {
		return false
	}
	mapped := false
	for offset := 20; offset < len(response); {
		if offset+4 > len(response) {
			return false
		}
		kind := binary.BigEndian.Uint16(response[offset:])
		length := int(binary.BigEndian.Uint16(response[offset+2:]))
		offset += 4
		if offset+length > len(response) {
			return false
		}
		if kind == 1 || kind == 0x0020 {
			value := response[offset : offset+length]
			if length != 8 && length != 20 {
				return false
			}
			if (value[1] == 1 && length == 8) || (value[1] == 2 && length == 20) {
				mapped = true
			}
		}
		offset += (length + 3) &^ 3
		if offset > len(response) {
			return false
		}
	}
	return mapped
}

func probeSTUN(ctx context.Context, session *probeSession, target ScanTarget, base Config, preset Preset, destination resolvedScanTarget, bypass string) (result TargetProbe, failure error) {
	result.Stage = "stun"
	conn, err := probeDialer(ctx, session, target, base, preset, destination, bypass).DialContext(ctx, "udp4", net.JoinHostPort(destination.Address, strconv.Itoa(target.Port)))
	if err != nil {
		return result, err
	}
	defer conn.Close()
	deadline, bounded := ctx.Deadline()
	if !bounded {
		deadline = time.Now().Add(8 * time.Second)
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	request, err := stunBindingRequest()
	if err != nil {
		return result, err
	}
	response := make([]byte, 2048)
	for attempt := 0; attempt < 3; attempt++ {
		if _, err = conn.Write(request); err != nil {
			return result, err
		}
		next := time.Now().Add(time.Duration(1<<attempt) * 500 * time.Millisecond)
		if deadline.Before(next) {
			next = deadline
		}
		conn.SetReadDeadline(next)
		for {
			count, readErr := conn.Read(response)
			if readErr != nil {
				break
			}
			if validSTUNResponse(request, response[:count]) {
				result.Bytes = int64(count)
				result.Stage = "complete"
				return result, nil
			}
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
	}
	return result, errors.New("no matching STUN binding response")
}

func listenProbeUDP(ctx context.Context, destination resolvedScanTarget, bypass string) (net.PacketConn, error) {
	config := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
		var setupErr error
		err := raw.Control(func(fd uintptr) {
			setupErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, destination.Interface)
			if setupErr == nil && bypass != "" {
				mark, err := strconv.ParseUint(bypass, 0, 32)
				if err != nil {
					setupErr = err
					return
				}
				setupErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(mark))
			}
		})
		return errors.Join(err, setupErr)
	}}
	return config.ListenPacket(ctx, "udp4", net.JoinHostPort(destination.Source, "0"))
}

var openProbePacket = listenProbeUDP

func probeHTTP3(ctx context.Context, session *probeSession, target ScanTarget, base Config, preset Preset, destination resolvedScanTarget, bypass string) (result TargetProbe, failure error) {
	result.Stage = "quic"
	socket, err := openProbePacket(ctx, destination, bypass)
	if err != nil {
		return result, errors.Join(errScanSetup, err)
	}
	defer socket.Close()
	if err = session.start(ctx, target, base, preset, destination, socket.LocalAddr().(*net.UDPAddr).Port, bypass); err != nil {
		return result, err
	}
	wire := &quic.Transport{Conn: socket}
	defer wire.Close()
	transport := &http3.Transport{
		TLSClientConfig: probeTLSConfig(target.Host),
		QUICConfig: &quic.Config{
			Versions:                       []quic.Version{quic.Version1},
			HandshakeIdleTimeout:           4 * time.Second,
			MaxIdleTimeout:                 5 * time.Second,
			DisablePathMTUDiscovery:        true,
			InitialPacketSize:              1200,
			InitialStreamReceiveWindow:     128 * 1024,
			MaxStreamReceiveWindow:         128 * 1024,
			InitialConnectionReceiveWindow: 256 * 1024,
			MaxConnectionReceiveWindow:     256 * 1024,
			MaxIncomingStreams:             -1,
			MaxIncomingUniStreams:          8,
		},
		MaxResponseHeaderBytes: 32768,
		DisableCompression:     true,
		Dial: func(dialCtx context.Context, _ string, tlsConfig *tls.Config, config *quic.Config) (*quic.Conn, error) {
			remote := &net.UDPAddr{IP: net.ParseIP(destination.Address), Port: target.Port}
			return wire.Dial(dialCtx, remote, tlsConfig, config)
		},
	}
	defer transport.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL(target), nil)
	if err != nil {
		return result, err
	}
	request.Header.Set("User-Agent", "OCFusion-connectivity-test")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Range", "bytes=0-65535")
	response, err := (&http.Client{Transport: transport, CheckRedirect: noProbeRedirect}).Do(request)
	if err != nil {
		return result, err
	}
	err = readProbeResponse(response, &result)
	return result, err
}
