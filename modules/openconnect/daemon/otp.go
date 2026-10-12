// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OTPChallenge struct {
	ID      string `json:"id"`
	Expires int64  `json:"expires"`
}

type OTPControl struct {
	mu        sync.Mutex
	challenge *OTPChallenge
	writer    *os.File
	reader    *os.File
	listener  net.Listener
}

type OTPSubmission struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
	Cancel    bool   `json:"cancel,omitempty"`
}

func validateOTP(p Profile) error {
	if p.OTPMode == "" || p.OTPMode == "none" {
		return nil
	}

	if p.Protocol != "anyconnect" || (p.OTPMode != "manual" && p.OTPMode != "totp") {
		return errors.New("OTP requires AnyConnect and manual or totp mode")
	}

	if p.OTPField != "" && !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`).MatchString(p.OTPField) {
		return errors.New("invalid OTP field")
	}

	if p.OTPMode == "manual" {
		return nil
	}

	secret := p.OTPSecret
	if strings.HasPrefix(secret, "otpauth://") {
		parsed, err := url.Parse(secret)
		if err != nil || parsed.Host != "totp" || parsed.User != nil {
			return errors.New("invalid TOTP URI")
		}

		parameters := parsed.Query()
		for _, values := range parameters {
			if len(values) != 1 {
				return errors.New("duplicate TOTP parameter")
			}
		}
		for key := range parameters {
			switch key {
			case "secret", "issuer", "algorithm", "digits", "period":
			default:
				return errors.New("unsupported TOTP parameter")
			}
		}

		if digits := parameters.Get("digits"); digits != "" && digits != "6" {
			return errors.New("automatic TOTP requires six digits")
		}

		if period := parameters.Get("period"); period != "" {
			seconds, err := strconv.Atoi(period)
			if err != nil || seconds < 15 || seconds > 120 {
				return errors.New("TOTP period must be 15..120 seconds")
			}
		}

		if algorithm := parameters.Get("algorithm"); algorithm != "" && algorithm != "SHA1" && algorithm != "SHA256" && algorithm != "SHA512" {
			return errors.New("unsupported TOTP algorithm")
		}

		secret = parameters.Get("secret")
	}

	if strings.ContainsAny(p.OTPSecret, "\r\n\x00") || len(p.OTPSecret) > 2048 {
		return errors.New("invalid TOTP secret")
	}

	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(strings.ToUpper(secret), "="))
	if err != nil || len(decoded) < 10 || len(decoded) > 128 {
		return errors.New("TOTP secret must be base32 encoded")
	}

	return nil
}

func (o *OTPControl) snapshot() *OTPChallenge {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.challenge == nil || time.Now().Unix() >= o.challenge.Expires {
		return nil
	}

	copy := *o.challenge
	return &copy
}

func (o *OTPControl) submit(submission OTPSubmission) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.challenge == nil || submission.Challenge != o.challenge.ID || time.Now().Unix() >= o.challenge.Expires {
		return errors.New("OTP challenge expired or changed")
	}

	if !submission.Cancel && !regexp.MustCompile(`^(?:[0-9]{6}|[0-9]{8})$`).MatchString(submission.Code) {
		return errors.New("OTP must contain six or eight digits")
	}

	code := submission.Code
	if submission.Cancel {
		code = ""
	}

	o.challenge = nil
	_, err := io.WriteString(o.writer, code+"\n")
	return err
}

func (o *OTPControl) close() {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.challenge = nil
	o.listener.Close()
	o.reader.Close()
	o.writer.Close()
}

func newOTPControl(reader, writer *os.File) (*OTPControl, error) {
	path := filepath.Join(runDir, "otp.sock")
	os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	control := &OTPControl{reader: reader, writer: writer, listener: listener}
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64), 128)
		for scanner.Scan() {
			if scanner.Text() != "otp" {
				continue
			}

			var id [16]byte
			if _, err := rand.Read(id[:]); err != nil {
				return
			}

			control.mu.Lock()
			control.challenge = &OTPChallenge{ID: hex.EncodeToString(id[:]), Expires: time.Now().Add(120 * time.Second).Unix()}
			control.mu.Unlock()
		}
	}()

	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}

			connection.SetDeadline(time.Now().Add(2 * time.Second))
			var submission OTPSubmission
			decoder := json.NewDecoder(io.LimitReader(connection, 1024))
			decoder.DisallowUnknownFields()
			err = decoder.Decode(&submission)
			if err == nil {
				err = control.submit(submission)
			}

			response := map[string]any{"ok": err == nil}
			if err != nil {
				response["error"] = "OTP rejected: challenge expired, changed or invalid"
			}

			json.NewEncoder(connection).Encode(response)
			connection.Close()
		}
	}()

	return control, nil
}

func submitOTP(data json.RawMessage, cancel bool) (any, error) {
	var submission OTPSubmission
	if json.Unmarshal(data, &submission) != nil {
		return nil, errors.New("invalid OTP submission")
	}

	submission.Cancel = cancel
	connection, err := net.DialTimeout("unix", filepath.Join(runDir, "otp.sock"), time.Second)
	if err != nil {
		return nil, errors.New("no active OTP worker")
	}

	defer connection.Close()
	connection.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(connection).Encode(submission); err != nil {
		return nil, err
	}

	var response struct {
		OK bool `json:"ok"`
	}
	if json.NewDecoder(connection).Decode(&response) != nil || !response.OK {
		return nil, errors.New("OTP challenge expired, changed or invalid")
	}

	return map[string]bool{"ok": true}, nil
}

func nativeTokenSecret(secret string) string {
	if strings.HasPrefix(secret, "otpauth://") {
		return secret
	}

	return "base32:" + strings.TrimRight(strings.ToUpper(secret), "=")
}
