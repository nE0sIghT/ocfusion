// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTOTPValidation(t *testing.T) {
	for _, secret := range []string{"JBSWY3DPEHPK3PXP", "otpauth://totp/router?secret=JBSWY3DPEHPK3PXP&algorithm=SHA256&period=30&digits=6"} {
		if err := validateOTP(Profile{Protocol: "anyconnect", OTPMode: "totp", OTPSecret: secret}); err != nil {
			t.Fatal(err)
		}
	}

	for _, secret := range []string{"invalid", "otpauth://totp/router?secret=JBSWY3DPEHPK3PXP&digits=8", "otpauth://hotp/router?secret=JBSWY3DPEHPK3PXP", "otpauth://totp/router?secret=JBSWY3DPEHPK3PXP&period=0"} {
		if validateOTP(Profile{Protocol: "anyconnect", OTPMode: "totp", OTPSecret: secret}) == nil {
			t.Fatal("accepted unsupported secret format")
		}
	}

	if nativeTokenSecret("jbswy3dpehpk3pxp") != "base32:JBSWY3DPEHPK3PXP" {
		t.Fatal("native encoding")
	}
}

func TestOTPSubmissionIsBoundedAndOneUse(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	defer reader.Close()
	defer writer.Close()
	control := OTPControl{writer: writer, challenge: &OTPChallenge{ID: "current", Expires: time.Now().Add(time.Minute).Unix()}}
	if control.submit(OTPSubmission{Challenge: "stale", Code: "123456"}) == nil {
		t.Fatal("stale challenge accepted")
	}

	if control.submit(OTPSubmission{Challenge: "current", Code: "123456\n"}) == nil {
		t.Fatal("unbounded code accepted")
	}

	if err := control.submit(OTPSubmission{Challenge: "current", Code: "123456"}); err != nil {
		t.Fatal(err)
	}

	data := make([]byte, 7)
	reader.Read(data)
	if string(data) != "123456\n" || control.submit(OTPSubmission{Challenge: "current", Code: "123456"}) == nil {
		t.Fatal("one-use delivery failed")
	}
}

func TestOTPPublicProjectionContainsNoCredentials(t *testing.T) {
	projected := publicStatusProjection("default", Status{State: "WAIT_OTP", OTP: &OTPChallenge{ID: "safe", Expires: 100}, RequestData: json.RawMessage(`{"code":"123456"}`)})
	data, _ := json.Marshal(projected)
	if !strings.Contains(string(data), "safe") || strings.Contains(string(data), "123456") {
		t.Fatal(string(data))
	}
}

func TestTokenSecretLifetimeAndClientOutput(t *testing.T) {
	previousRun, previousTun := runDir, tunDevice
	runDir = t.TempDir()
	tunDevice = filepath.Join(runDir, "tun")
	os.WriteFile(tunDevice, nil, 0600)
	t.Cleanup(func() {
		runDir, tunDevice = previousRun, previousTun
	})
	bundle := t.TempDir()
	os.Mkdir(filepath.Join(bundle, "bin"), 0700)
	worker := []byte(`#!/bin/sh
while [ $# -gt 0 ]; do
    if [ "$1" = --token-secret ]; then
        shift
        test -s "${1#@}" || exit 2
    fi

    shift
done

echo 'Got CONNECT response'
`)
	os.WriteFile(filepath.Join(bundle, "bin/openconnect"), worker, 0700)
	os.WriteFile(filepath.Join(bundle, "ca-bundle.crt"), []byte("fixture"), 0600)
	hashes := map[string]string{}
	for _, name := range []string{"bin/openconnect", "ca-bundle.crt"} {
		data, _ := os.ReadFile(filepath.Join(bundle, name))
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	child, err := launch(Profile{Protocol: "anyconnect", Server: "https://vpn.example", OTPMode: "totp", OTPSecret: "JBSWY3DPEHPK3PXP"}, Manifest{Bundle: bundle, BundleSHA: hashes})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-child.done:
		if err != nil {
			t.Fatal("private secret was removed before the client read it", err)
		}
	case <-time.After(3 * time.Second):
		halt(child)
		t.Fatal("worker did not finish")
	}
	if _, err := os.Stat(filepath.Join(runDir, "token.secret")); !os.IsNotExist(err) {
		t.Fatal("token secret retained after child exit")
	}
	select {
	case <-child.stage:
	default:
		t.Fatal("client output pipe closed during launch")
	}
	for _, argument := range child.cmd.Args {
		if strings.Contains(argument, "JBSWY3DPEHPK3PXP") {
			t.Fatal("secret exposed in argv")
		}
	}
}
