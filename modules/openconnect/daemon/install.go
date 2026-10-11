// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var bundleFiles = []string{
	"bin/openconnect", "ca-bundle.crt", "ui/index.html", "ui/app.js", "ui/settings.js",
	"ui/connection.js", "ui/translations.js", "scripts/vpnc-script",
}

// A prepared replacement drive may contain no previous runtime payload.
// Existing payloads must still pass integrity verification.
func verifyUpgrade(m Manifest, replacement string) error {
	if _, err := refreshedManifest(m, replacement); err != nil {
		return fmt.Errorf("replacement bundle unavailable: %w", err)
	}

	_, err := os.Stat(m.Bundle)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	return verifyBundle(m)
}

// Refresh only payload identity. User configuration and bindings are not rewritten.
func refreshedManifest(m Manifest, bundle string) (Manifest, error) {
	hashes := map[string]string{}

	for _, rel := range bundleFiles {
		b, e := os.ReadFile(filepath.Join(bundle, rel))
		if e != nil {
			return m, e
		}

		sum := sha256.Sum256(b)
		hashes[rel] = hex.EncodeToString(sum[:])
	}

	m.Bundle = bundle
	m.BundleSHA = hashes

	return m, nil
}

func checkInstall() error {
	m, e := loadManifest()
	if e != nil {
		return e
	}

	if e = verifyIdentity(m); e != nil {
		return e
	}

	if e = verifyBundle(m); e != nil {
		return e
	}

	for i := 0; i < 15 && !ours(pid()); i++ {
		time.Sleep(time.Second)
	}

	if !ours(pid()) {
		return errors.New("supervisor is not running")
	}

	var st Status

	for i := 0; i < 10; i++ {
		b, err := os.ReadFile(filepath.Join(runDir, "status.json"))
		if err == nil && json.Unmarshal(b, &st) == nil {
			break
		}

		time.Sleep(time.Second)
	}

	if st.State == "" {
		return errors.New("supervisor did not publish status")
	}

	fmt.Printf("CHECK identity=OK bundle=OK supervisor=OK fusion_unit=%d fusion_index=%d\n", m.Unit, m.Index)
	fmt.Printf("CHECK state=%s networks=%d\n", st.State, len(st.Networks))
	fmt.Printf("CHECK message=%s\n", st.Message)
	if _, e = os.Stat("/dev/net/tun"); e != nil {
		return errors.New("TUN device unavailable")
	}

	fmt.Println("CHECK tun=OK")

	if _, e = os.Stat("/tmp/var/wwwext/ocfusion.html"); e != nil {
		return errors.New("import page unavailable")
	}

	fmt.Println("CHECK panel=/user/extensions.html direct_page=/user/ocfusion.html")

	return nil
}
