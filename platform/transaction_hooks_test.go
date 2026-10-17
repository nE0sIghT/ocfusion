// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func transactionFixture(t *testing.T, directory, version, external, failRestore, install string) {
	t.Helper()
	fixture(t, directory, version, "echo '{}'")
	manifest := Module{Schema: 1, ID: "example", Name: "Example", Version: version, API: 1, Entry: "bin/module", Install: true, TransactionHooks: true}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(directory, "module.json"), data, 0600)
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
    backup)
        cp %q "$EXT_BACKUP_PATH/native"
        ;;
    restore)
        if [ -f %q ]; then
            exit 1
        fi
        cp "$EXT_BACKUP_PATH/native" %q
        ;;
    install)
        %s
        ;;
    run)
        trap 'exit 0' TERM INT
        while :; do
            sleep 0.1
        done
        ;;
esac
printf '%%s\n' '{}'
`, external, failRestore, external, install)
	os.WriteFile(filepath.Join(directory, "bin/module"), []byte(script), 0700)
}

func TestDistributionHooksSurvivePayloadReplacementAndRestoreFailure(t *testing.T) {
	oldBase, oldRoot := base, stateRoot
	stateRoot = t.TempDir()
	base = filepath.Join(stateRoot, "ocfusion-platform")
	t.Cleanup(func() { base, stateRoot = oldBase, oldRoot })
	external := filepath.Join(t.TempDir(), "external")
	fail := filepath.Join(t.TempDir(), "fail")
	os.WriteFile(external, []byte("original"), 0600)
	directory := t.TempDir()
	packageDirectory := filepath.Join(directory, "example")
	transactionFixture(t, packageDirectory, "2", external, fail, ":")
	os.MkdirAll(base, 0700)
	managerPath := filepath.Join(base, "extd")
	os.WriteFile(managerPath, []byte("previous-manager"), 0700)
	backup := filepath.Join(t.TempDir(), "backup")
	if err := backupState(directory, backup); err != nil {
		t.Fatal(err)
	}

	os.RemoveAll(directory)
	os.WriteFile(external, []byte("changed"), 0600)
	os.WriteFile(fail, nil, 0600)
	os.WriteFile(managerPath, []byte("current-manager"), 0700)
	if err := restoreState(backup); err == nil {
		t.Fatal("restore hook failure ignored")
	}

	if _, err := os.Stat(filepath.Join(backup, "state.json")); err != nil {
		t.Fatal("restore failure lost backup", err)
	}
	managerBytes, _ := os.ReadFile(managerPath)
	if string(managerBytes) != "current-manager" {
		t.Fatal("failed hook replaced the recovery-aware manager")
	}

	os.Remove(fail)
	for attempt := 0; attempt < 2; attempt++ {
		if err := restoreState(backup); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(external)
		if string(data) != "original" {
			t.Fatal("restore did not use saved hook payload")
		}
	}
}

func TestModuleInstallRetainsBackupAfterRollbackFailure(t *testing.T) {
	oldBase, oldRAM, oldPackages, oldWeb := base, ram, packageRoot, webRoot
	base, ram, packageRoot, webRoot = t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Cleanup(func() { base, ram, packageRoot, webRoot = oldBase, oldRAM, oldPackages, oldWeb })
	manager := &Manager{registry: Registry{Schema: 1, Modules: map[string]Installed{}}, workers: map[string]*Worker{}}
	t.Cleanup(func() { manager.stop("example") })
	external := filepath.Join(t.TempDir(), "external")
	fail := filepath.Join(t.TempDir(), "fail")
	os.WriteFile(external, []byte("original"), 0600)
	one := t.TempDir()
	transactionFixture(t, one, "1", external, fail, ":")
	if _, err := manager.handle(Request{Op: "install", Path: archive(t, one)}); err != nil {
		t.Fatal(err)
	}

	two := t.TempDir()
	transactionFixture(t, two, "2", external, fail, fmt.Sprintf("printf changed > %q\n        exit 1", external))
	os.WriteFile(fail, nil, 0600)
	_, err := manager.handle(Request{Op: "install", Path: archive(t, two)})
	if err == nil || !strings.Contains(err.Error(), "backup retained at") {
		t.Fatal("rollback failure not reported", err)
	}

	backups, _ := filepath.Glob(filepath.Join(base, "module-backups", "example-*"))
	if len(backups) != 1 || manager.workers["example"].cmd != nil {
		t.Fatal("failed rollback removed backup or restarted worker", backups)
	}

	if err := manager.start("example"); err == nil {
		t.Fatal("failed rollback allowed automatic restart")
	}

	restarted := &Manager{registry: manager.registry, workers: map[string]*Worker{}}
	if err := restarted.start("example"); err == nil {
		t.Fatal("manager restart bypassed pending rollback")
	}

	os.Remove(fail)
	if err := restoreState(backups[0]); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(external)
	if string(data) != "original" {
		t.Fatal("retry did not restore native state")
	}

	os.RemoveAll(backups[0])
	if err := manager.start("example"); err != nil {
		t.Fatal(err)
	}

	three := t.TempDir()
	transactionFixture(t, three, "3", external, fail, fmt.Sprintf("printf updated > %q", external))
	if _, err := manager.handle(Request{Op: "install", Path: archive(t, three)}); err != nil {
		t.Fatal(err)
	}

	data, _ = os.ReadFile(external)
	backups, _ = filepath.Glob(filepath.Join(base, "module-backups", "example-*"))
	if string(data) != "updated" || len(backups) != 0 || manager.registry.Modules["example"].Manifest.Version != "3" {
		t.Fatal("successful update did not commit or clean backup")
	}
}

func TestTransactionHookPayloadIsVerifiedBeforeRestoringFiles(t *testing.T) {
	oldBase, oldRoot := base, stateRoot
	stateRoot = t.TempDir()
	base = filepath.Join(stateRoot, "ocfusion-platform")
	t.Cleanup(func() { base, stateRoot = oldBase, oldRoot })
	os.MkdirAll(base, 0700)
	file := filepath.Join(base, "config.json")
	os.WriteFile(file, []byte("original"), 0600)
	external := filepath.Join(t.TempDir(), "external")
	os.WriteFile(external, []byte("original"), 0600)
	directory := t.TempDir()
	transactionFixture(t, filepath.Join(directory, "example"), "1", external, filepath.Join(t.TempDir(), "fail"), ":")
	backup := filepath.Join(t.TempDir(), "backup")
	if err := backupState(directory, backup); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(file, []byte("changed"), 0600)
	os.WriteFile(filepath.Join(backup, "hooks/example/payload/bin/module"), []byte("#!/bin/sh\nexit 0\n"), 0700)
	if restoreState(backup) == nil {
		t.Fatal("tampered restore payload accepted")
	}

	data, _ := os.ReadFile(file)
	if string(data) != "changed" {
		t.Fatal("files restored before verifying all hook payloads")
	}
}

func TestReleasedBackupUsesItsSavedRestorationTool(t *testing.T) {
	oldBase := base
	base = filepath.Join(t.TempDir(), "ocfusion-platform")
	t.Cleanup(func() { base = oldBase })
	backup := t.TempDir()
	os.WriteFile(filepath.Join(backup, "state.json"), []byte(`{"paths":{},"native":true}`), 0600)
	tool := filepath.Join(backup, "ocfusion-platform/extd")
	os.MkdirAll(filepath.Dir(tool), 0700)
	marker := filepath.Join(backup, "restored")
	script := fmt.Sprintf("#!/bin/sh\nset -eu\n[ \"$1\" = restore-state ]\n[ \"$2\" = %q ]\nprintf restored > %q\n", backup, marker)
	os.WriteFile(tool, []byte(script), 0700)
	if err := restoreState(backup); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatal("released restoration tool not invoked")
	}

	os.WriteFile(tool, []byte("#!/bin/sh\nexit 1\n"), 0700)
	if restoreState(backup) == nil {
		t.Fatal("released restoration failure ignored")
	}
	if _, err := os.Stat(filepath.Join(backup, "state.json")); err != nil {
		t.Fatal("released backup was removed after failure")
	}
}

func TestTransactionHooksFollowDependencyOrder(t *testing.T) {
	oldBase, oldRoot := base, stateRoot
	stateRoot = t.TempDir()
	base = filepath.Join(stateRoot, "ocfusion-platform")
	t.Cleanup(func() { base, stateRoot = oldBase, oldRoot })
	directory := t.TempDir()
	log := filepath.Join(t.TempDir(), "order")
	for _, id := range []string{"consumer", "provider"} {
		path := filepath.Join(directory, id)
		os.MkdirAll(filepath.Join(path, "bin"), 0700)
		manifest := Module{Schema: 1, ID: id, Name: id, Version: "1", API: 1, Entry: "bin/module", TransactionHooks: true, ProvidesAPI: 1}
		if id == "consumer" {
			manifest.Dependencies = []Dependency{{ID: "provider", API: 1}}
		}

		data, _ := json.Marshal(manifest)
		os.WriteFile(filepath.Join(path, "module.json"), data, 0600)
		script := fmt.Sprintf("#!/bin/sh\nprintf '%%s:%%s\\n' \"$1\" \"$EXT_MODULE_ID\" >> %q\nprintf '%%s\\n' '{}'\n", log)
		os.WriteFile(filepath.Join(path, "bin/module"), []byte(script), 0700)
	}

	backup := filepath.Join(t.TempDir(), "backup")
	if err := backupState(directory, backup); err != nil {
		t.Fatal(err)
	}
	if err := restoreState(backup); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(log)
	if string(data) != "backup:provider\nbackup:consumer\nrestore:consumer\nrestore:provider\n" {
		t.Fatal("transaction hook order", string(data))
	}
}

func TestDistributionRollbackBlocksStartupUntilRestored(t *testing.T) {
	oldBase, oldRoot, oldMarker := base, stateRoot, installMarker
	stateRoot = t.TempDir()
	base = filepath.Join(stateRoot, "ocfusion-platform")
	installMarker = filepath.Join(t.TempDir(), "installing")
	t.Cleanup(func() { base, stateRoot, installMarker = oldBase, oldRoot, oldMarker })
	backup := filepath.Join(stateRoot, "ocfusion-update-backup")
	state := StateBackup{Schema: 1, Paths: map[string]bool{}}
	if err := atomicJSON(filepath.Join(backup, "state.json"), state); err != nil {
		t.Fatal(err)
	}

	if pendingModuleTransaction("example") == nil {
		t.Fatal("interrupted distribution allowed startup")
	}

	os.WriteFile(installMarker, nil, 0600)
	if err := pendingModuleTransaction("example"); err != nil {
		t.Fatal("active installer could not check its new modules", err)
	}

	os.Remove(installMarker)
	if err := restoreState(backup); err != nil {
		t.Fatal(err)
	}

	if err := pendingModuleTransaction("example"); err != nil {
		t.Fatal("restored distribution still blocked startup", err)
	}
}
