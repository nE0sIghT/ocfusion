#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Model platform CLI filesystem effects for stock installer shell fixtures."""

import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path("FIXTURE_ROOT")
JFFS = ROOT / "jffs"
OPERATION = sys.argv[1]


def packages(directory):
    return sorted(Path(directory).iterdir())


def backup_state():
    directory = Path(sys.argv[3])
    directory.mkdir()
    paths = [JFFS / "ocfusion-platform"]
    for package in packages(sys.argv[2]):
        manifest = json.loads((package / "module.json").read_text())
        paths.extend(JFFS / Path(path).name for path in manifest.get("state_paths", []))

    snapshot = {}
    for path in paths:
        existed = path.exists()
        snapshot[str(path)] = existed
        if existed:
            shutil.copytree(path, directory / path.name, symlinks=True)

    (directory / "state.json").write_text(json.dumps(snapshot))


def restore_state():
    directory = Path(sys.argv[2])
    for name, existed in json.loads((directory / "state.json").read_text()).items():
        path = Path(name)
        shutil.rmtree(path, ignore_errors=True)
        if existed:
            shutil.copytree(directory / path.name, path, symlinks=True)


def prepare_bundled():
    for package in packages(sys.argv[2]):
        manifest = json.loads((package / "module.json").read_text())
        if manifest.get("id") == "openconnect":
            adapter = (package / "scripts/module.sh").read_text()
            adapter = adapter.replace("/jffs", str(JFFS))
            temporary = ROOT / "module-adapter"
            temporary.write_text(adapter)
            environment = dict(
                os.environ, EXT_PAYLOAD_PATH=str(package), EXT_PACKAGE_PATH=str(package)
            )
            subprocess.run(
                ["/bin/sh", str(temporary), "install"], env=environment, check=True
            )
            subprocess.run(
                ["/bin/sh", str(temporary), "prepare"], env=environment, check=True
            )


def main():
    if OPERATION == "backup-state":
        backup_state()
    elif OPERATION == "restore-state":
        restore_state()
    elif OPERATION == "prepare-platform":
        (JFFS / "ocfusion-platform/transport.json").write_text("{}")
    elif OPERATION == "prepare-bundled":
        prepare_bundled()
    elif OPERATION == "register-set":
        with (ROOT / "registered-modules").open("a") as output:
            for package in packages(sys.argv[2]):
                output.write(str(package) + "\n")
    elif OPERATION == "remove-bundled":
        manifest = JFFS / "ocfusion/manifest.json"
        if manifest.exists():
            manifest.rename(manifest.with_name("manifest.uninstalled.json"))
        for name in ("ocfusion", "vpnc-script", "web-overlay.enabled"):
            (JFFS / "ocfusion" / name).unlink(missing_ok=True)
    elif OPERATION == "remove-transport":
        path = JFFS / "ocfusion-platform/transport.json"
        path.rename(path.with_name("transport.removed.json"))
    elif OPERATION == "prune-state":
        for package in packages(sys.argv[2]):
            manifest = json.loads((package / "module.json").read_text())
            for path in manifest.get("state_paths", []):
                shutil.rmtree(JFFS / Path(path).name, ignore_errors=True)
        shutil.rmtree(JFFS / "ocfusion-platform", ignore_errors=True)
    elif OPERATION == "check-bundled" and os.environ.get("TEST_FAIL") == "1":
        sys.exit(1)

    print("CHECK fixture=" + OPERATION)


main()
