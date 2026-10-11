// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func fatal(e error) {
	if e != nil {
		fmt.Fprintln(os.Stderr, "OCFusion:", e)
		os.Exit(1)
	}
}

func runCLI() {
	os.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	if os.Geteuid() != 0 {
		fatal(errors.New("UID 0 required"))
	}

	if len(os.Args) < 2 {
		fmt.Println("extctl", version, "("+versionCommit()+")", "start stop list status MODULE register DIR install FILE config MODULE FILE call MODULE ACTION [FILE] enable|disable|diagnose|remove MODULE")

		return
	}

	op := os.Args[1]

	switch op {
	case "prune-state":
		if len(os.Args) != 3 {
			fatal(errors.New("module directory required"))
		}
		fatal(pruneState(os.Args[2]))
		return

	case "check-bundled", "remove-bundled":
		if len(os.Args) != 3 {
			fatal(errors.New("module directory required"))
		}
		if op == "check-bundled" {
			fatal(checkBundled(os.Args[2]))
		} else {
			fatal(removeBundled(os.Args[2]))
		}
		return

	case "backup-state":
		if len(os.Args) != 4 {
			fatal(errors.New("module directory and backup path required"))
		}
		fatal(backupState(os.Args[2], os.Args[3]))
		return
	case "restore-state":
		if len(os.Args) != 3 {
			fatal(errors.New("backup path required"))
		}
		fatal(restoreState(os.Args[2]))
		return
	case "remove-transport":
		fatal(removePlatformTransport())
		return

	case "prepare-platform":
		if len(os.Args) != 3 {
			fatal(errors.New("distribution path required"))
		}
		fatal(preparePlatform(os.Args[2]))
		return
	case "prepare-bundled":
		if len(os.Args) != 3 {
			fatal(errors.New("module directory required"))
		}
		fatal(prepareBundled(os.Args[2]))
		return
	case "web":
		fatal(publishWeb())
		return
	case "web-reset":
		fatal(removeWebOverlays())
		return

	case "check-removal":
		path := base + "/registry.json"
		if len(os.Args) == 3 {
			path = os.Args[2]
		}

		fatal(checkPlatformRemoval(path))

		return
	case "version":
		fmt.Println("OCFusion", version, "("+versionCommit()+")")

		return
	case "daemon":
		fatal(daemon())

		return
	case "start":
		fatal(startDaemon())

		return
	case "stop":
		b, e := os.ReadFile(ram + "/pid")
		if os.IsNotExist(e) {
			return
		}

		fatal(e)

		var pid int
		_, e = fmt.Sscan(string(b), &pid)
		fatal(e)

		exe, e := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if e != nil {
			return
		}

		if exe != base+"/extd" && exe != base+"/extd (deleted)" {
			fatal(errors.New("PID does not belong to extd"))
		}

		fatal(syscall.Kill(pid, syscall.SIGTERM))

		for i := 0; i < 40; i++ {
			if _, e = os.Stat(fmt.Sprintf("/proc/%d", pid)); os.IsNotExist(e) {
				return
			}

			time.Sleep(250 * time.Millisecond)
		}

		fatal(errors.New("extension manager stop timed out"))

		return
	}

	q := Request{Op: op}
	if op == "register" || op == "install" || op == "register-set" {
		if len(os.Args) != 3 {
			fatal(errors.New("package path required"))
		}

		q.Path = os.Args[2]
	} else if op != "list" {
		if len(os.Args) < 3 {
			fatal(errors.New("module ID required"))
		}

		q.ID = os.Args[2]
	}

	if op == "call" {
		if len(os.Args) != 4 && len(os.Args) != 5 {
			fatal(errors.New("call MODULE ACTION [FILE]"))
		}

		q.Op = "action"
		q.Action = os.Args[3]
		if !idRE.MatchString(q.Action) {
			fatal(errors.New("invalid module action"))
		}

		if len(os.Args) == 5 {
			b, e := os.ReadFile(os.Args[4])
			fatal(e)
			if len(b) > 16384 || !json.Valid(b) {
				fatal(errors.New("invalid/oversized action data"))
			}

			q.Config = b
		}
	}

	if op == "config" {
		if len(os.Args) != 4 {
			fatal(errors.New("config MODULE FILE"))
		}

		b, e := os.ReadFile(os.Args[3])
		fatal(e)
		if !json.Valid(b) {
			fatal(errors.New("invalid JSON"))
		}

		q.Config = b
	}

	r, e := rpc(q)
	fatal(e)

	b, e := json.MarshalIndent(r.Data, "", "  ")
	fatal(e)
	fmt.Println(string(b))
}
