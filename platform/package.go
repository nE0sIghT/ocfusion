// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func safePath(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && !strings.Contains(p, "\\")
}

func readPackage(dir string) (Installed, error) {
	var item Installed
	var pathErr error
	dir, pathErr = filepath.Abs(dir)
	if pathErr != nil {
		return item, pathErr
	}

	item.Path = dir
	item.Enabled = true
	b, e := os.ReadFile(filepath.Join(dir, "module.json"))
	if e != nil {
		return item, e
	}

	if len(b) > 16384 {
		return item, errors.New("manifest too large")
	}

	if e = json.Unmarshal(b, &item.Manifest); e != nil {
		return item, e
	}

	m := item.Manifest
	if m.Schema != 1 || m.API != 1 || !idRE.MatchString(m.ID) || !versionRE.MatchString(m.Version) || !safePath(m.Entry) || m.UI != "" && !safePath(m.UI) {
		return item, errors.New("unsupported module manifest")
	}

	if m.Requires.Architecture != "" && m.Requires.Architecture != "aarch64" || m.Requires.PlatformAPI > 1 {
		return item, errors.New("incompatible module requirements")
	}

	for _, dependency := range m.Dependencies {
		if !idRE.MatchString(dependency.ID) || dependency.ID == m.ID || dependency.API < 1 {
			return item, errors.New("invalid module dependency")
		}
	}

	for _, contribution := range m.StockPages {
		if !supportedStockPage(contribution.Path) || !safePath(contribution.Script) {
			return item, errors.New("unsupported stock-page contribution")
		}
	}

	for _, path := range m.StatePaths {
		if !ownedStatePath(path) || path == base || path == "/jffs/ocfusion-update-backup" {
			return item, errors.New("invalid module state path")
		}

		if item.Manifest.Resources == nil {
			item.Manifest.Resources = map[string]string{}
		}

		item.Manifest.Resources["state:"+path] = "owned"
	}

	item.Hashes = map[string]string{}
	e = filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}

		if d.IsDir() {
			return nil
		}

		info, e := d.Info()
		if e != nil {
			return e
		}

		if !info.Mode().IsRegular() {
			return errors.New("only regular files/directories allowed")
		}

		if info.Size() > 64<<20 {
			return errors.New("module file too large")
		}

		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}

		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(dir, path)
		item.Hashes[rel] = hex.EncodeToString(sum[:])

		return nil
	})
	if e != nil {
		return item, e
	}

	if _, ok := item.Hashes[m.Entry]; !ok {
		return item, errors.New("missing entrypoint")
	}

	for _, contribution := range m.StockPages {
		if _, exists := item.Hashes[contribution.Script]; !exists {
			return item, errors.New("missing stock-page script")
		}
	}

	return item, nil
}

func verify(item Installed) error {
	for rel, want := range item.Hashes {
		b, e := os.ReadFile(filepath.Join(item.Path, rel))
		if e != nil {
			return e
		}

		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			return errors.New("module integrity mismatch")
		}
	}

	return nil
}

// Extract into a newly-created staging directory. No links, devices, path traversal.
func unpack(archive, dir string) error {
	f, e := os.Open(archive)
	if e != nil {
		return e
	}

	defer f.Close()

	gz, e := gzip.NewReader(io.LimitReader(f, 64<<20))
	if e != nil {
		return e
	}

	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64
	count := 0

	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}

		if e != nil {
			return e
		}

		count++
		name := strings.TrimSuffix(h.Name, "/")
		if count > 10000 || !safePath(name) {
			return errors.New("invalid archive path/count")
		}

		path := filepath.Join(dir, name)

		switch h.Typeflag {
		case tar.TypeDir:
			if e = os.MkdirAll(path, 0700); e != nil {
				return e
			}
		case tar.TypeReg:
			total += h.Size
			if h.Size < 0 || total > 128<<20 {
				return errors.New("module archive exceeds size limit")
			}

			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				return e
			}

			o, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(h.Mode)&0755)
			if e != nil {
				return e
			}

			_, e = io.CopyN(o, tr, h.Size)
			ce := o.Close()
			if e != nil {
				return e
			}

			if ce != nil {
				return ce
			}
		default:
			return errors.New("archive links/devices not allowed")
		}
	}
}

func snapshot(item Installed) (string, error) {
	dir := filepath.Join(ram, "modules", item.Manifest.ID, "payload")
	next := dir + ".next"
	os.RemoveAll(next)
	if e := os.MkdirAll(next, 0700); e != nil {
		return "", e
	}

	for rel, want := range item.Hashes {
		b, e := os.ReadFile(filepath.Join(item.Path, rel))
		if e != nil {
			return "", e
		}

		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			return "", errors.New("module integrity mismatch")
		}

		path := filepath.Join(next, rel)
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return "", e
		}

		if e = os.WriteFile(path, b, 0700); e != nil {
			return "", e
		}
	}

	os.RemoveAll(dir)
	if e := os.Rename(next, dir); e != nil {
		return "", e
	}

	return dir, nil
}

func supportedStockPage(path string) bool {
	switch path {
	case "/VPN/vpnc.html", "/Advanced_Wireless_Content.asp":
		return true
	default:
		return false
	}
}
