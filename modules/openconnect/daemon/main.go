// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func die(e error) {
	if e != nil {
		fmt.Fprintln(os.Stderr, "ocfusion-openconnect:", e)
		os.Exit(1)
	}
}

func main() {
	os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	if len(os.Args) < 2 {
		fmt.Println("ocfusion-openconnect", version, "commands: doctor setup import start stop enable disable status event web uninstall")

		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "claim":
		_, e := vpnCall(vpnRequest{Operation: "claim"})
		die(e)
	case "version":
		fmt.Println("ocfusion-openconnect", version)
	case "module-validate", "module-import", "module-patch", "module-settings":
		data, err := moduleOperation(cmd, os.Stdin)
		die(err)
		fmt.Println(string(data))
	case "validate":
		if len(os.Args) != 3 {
			die(errors.New("validate FILE"))
		}

		b, e := os.ReadFile(os.Args[2])
		die(e)

		_, e = parseProfile(b)
		die(e)
		fmt.Println("Valid schema 1 profile")
	case "doctor":
		doctor()
	case "setup":
		if len(os.Args) != 3 {
			die(errors.New("setup BUNDLE_DIR"))
		}

		die(setup(os.Args[2]))
	case "prepare-kernel":
		_, e := vpnCall(vpnRequest{Operation: "prepare"})
		die(e)
	case "verify-upgrade":
		if len(os.Args) != 3 {
			die(errors.New("verify-upgrade BUNDLE_DIR"))
		}

		m, e := loadManifest()
		die(e)
		if _, e = os.Stat(filepath.Join(root, "profile.json")); e == nil {
			_, e = loadProfile()
			die(e)
		} else if !os.IsNotExist(e) {
			die(e)
		}

		die(verifyUpgrade(m, os.Args[2]))
	case "verify-bundle":
		m, e := loadManifest()
		die(e)
		die(verifyBundle(m))
	case "verify-installation":
		m, e := loadManifest()
		die(e)
		die(verifyIdentity(m))
		die(verifyBundle(m))
	case "check-install":
		die(checkInstall())
	case "fusion-rename":
		_, e := vpnCall(vpnRequest{Operation: "prepare"})
		die(e)
	case "refresh-bundle":
		if len(os.Args) != 3 {
			die(errors.New("refresh-bundle BUNDLE_DIR"))
		}

		m, e := loadManifest()
		die(e)
		die(verifyIdentity(m))

		m, e = refreshedManifest(m, os.Args[2])
		die(e)
		die(saveJSON(filepath.Join(root, "manifest.json"), m))
	case "event":
		die(writeEvent())
	case "daemon":
		die(daemon())
	case "status":
		b, e := readStatus()
		die(e)
		fmt.Print(string(b))
	case "import":
		if len(os.Args) != 3 {
			die(errors.New("import FILE"))
		}

		b, e := os.ReadFile(os.Args[2])
		die(e)

		p, e := parseProfile(b)
		die(e)
		die(saveJSON(filepath.Join(root, "profile.json"), p))
		fmt.Println("Profile saved; supervisor will reload. Wi-Fi/VLAN bindings preserved.")
	case "start":
		die(start())

	case "stop":
		die(stop())
	case "enable", "disable":
		p, e := loadProfile()
		die(e)

		p.Enabled = cmd == "enable"
		die(saveJSON(filepath.Join(root, "profile.json"), p))
		if p.Enabled {
			die(start())
		}
	case "web-reset":
		die(errors.New("web overlays are platform-owned"))
	case "web":
		die(errors.New("web publishing is platform-owned"))
	case "uninstall":
		die(uninstall())
	default:
		die(errors.New("unknown command"))
	}
}

func doctor() {
	fmt.Println("ocfusion-openconnect", version, "UID", os.Getuid(), "EUID", os.Geteuid(), "(account name is irrelevant)")

	for _, k := range []string{"productid", "firmver", "buildno", "extendno", "apps_install_folder", "apps_mounted_path", "apps_local_space", "stop_runapp"} {
		fmt.Println(k + "=" + nvget(k))
	}

	for _, x := range []string{"nvram", "ip", "iptables", "ip6tables", "cru", "get_apps_name"} {
		p, e := exec.LookPath(x)
		fmt.Println(x, p, e)
	}

	for _, x := range []string{"/dev/net/tun", "/jffs", "/opt", "/tmp/opt"} {
		st, e := os.Stat(x)
		if e == nil {
			fmt.Println(x, st.Mode())
		} else {
			fmt.Println(x, e)
		}
	}

	if m, e := loadManifest(); e == nil {
		fmt.Println("identity:", verifyIdentity(m))

		ns, e := networks(m)
		fmt.Println("SDN:", e)

		b, _ := json.MarshalIndent(ns, "", "  ")
		fmt.Println(string(b))
		fmt.Println("bundle:", verifyBundle(m))
	}
}

func setup(bundle string) error {
	if err := verifyArchitecture(); err != nil {
		return err
	}

	response, err := vpnCall(vpnRequest{Operation: "state"})
	if err != nil {
		return err
	}

	manifest := response.Manifest

	manifest, err = refreshedManifest(manifest, bundle)
	if err != nil {
		return err
	}

	return saveJSON(filepath.Join(root, "manifest.json"), manifest)
}

func pid() int {
	b, _ := os.ReadFile(filepath.Join(runDir, "pid"))
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))

	return n
}

func ours(n int) bool {
	if n < 2 {
		return false
	}

	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", n))

	return e == nil && strings.Contains(string(b), "ocfusion") && strings.Contains(string(b), "daemon")
}

func start() error {
	if ours(pid()) {
		return nil
	}

	if e := os.MkdirAll(runDir, 0700); e != nil {
		return e
	}

	exe, e := os.Executable()
	if e != nil {
		return e
	}

	log, e := os.OpenFile(filepath.Join(runDir, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}

	defer log.Close()

	c := exec.Command(exe, "daemon")
	c.Stdout = log
	c.Stderr = log
	c.Stdin = nil
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = c.Start(); e != nil {
		return e
	}

	return c.Process.Release()
}

func stop() error {
	n := pid()
	if !ours(n) {
		return nil
	}

	p, e := os.FindProcess(n)
	if e != nil {
		return e
	}

	if e = p.Signal(syscall.SIGTERM); e != nil {
		return e
	}

	for i := 0; i < 50; i++ {
		if !ours(n) {
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}
	// Never kill daemon abruptly: its child/cleanup needs to complete.

	return errors.New("stop timed out; inspect status/processes before retrying")
}

func writeEvent() error {
	v := map[string]string{}

	for _, k := range []string{"TUNDEV", "INTERNAL_IP4_ADDRESS", "INTERNAL_IP4_NETMASK", "INTERNAL_IP4_MTU", "INTERNAL_IP4_DNS"} {
		v[k] = os.Getenv(k)
	}

	generation := os.Getenv("OCF_GENERATION")
	if generation == "" {
		return errors.New("missing session generation")
	}

	reason := os.Getenv("reason")

	switch reason {
	case "pre-init", "connect", "reconnect", "disconnect", "attempt-reconnect":
	default:
		return nil
	}

	return saveJSON(filepath.Join(runDir, "event.json"), Event{Generation: generation, Reason: reason, Vars: v})
}

func uninstall() error {
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); os.IsNotExist(err) {
		return nil
	}

	if _, err := vpnCall(vpnRequest{Operation: "release"}); err != nil {
		return err
	}

	if err := stop(); err != nil {
		return err
	}

	if err := os.Rename(filepath.Join(root, "manifest.json"), filepath.Join(root, "manifest.uninstalled.json")); err != nil {
		return err
	}

	for _, name := range []string{"ocfusion", "vpnc-script", "web-overlay.enabled"} {
		os.Remove(filepath.Join(root, name))
	}

	return nil
}

// Module entrypoints use the same stdin envelope as every API 1 module.
func moduleOperation(operation string, input io.Reader) (json.RawMessage, error) {
	if operation == "module-settings" {
		return readSettings()
	}

	var envelope struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(io.LimitReader(input, 20000)).Decode(&envelope); err != nil {
		return nil, errors.New("invalid module input")
	}

	if operation == "module-patch" {
		if err := patchProfile(envelope.Config); err != nil {
			return nil, err
		}
	} else if string(envelope.Config) != "null" && len(envelope.Config) != 0 {
		profile, err := parseProfile(envelope.Config)
		if err != nil {
			return nil, err
		}

		if operation == "module-import" {
			if err = saveJSON(filepath.Join(root, "profile.json"), profile); err != nil {
				return nil, err
			}
		}
	}

	return json.RawMessage(`{"ok":true}`), nil
}
