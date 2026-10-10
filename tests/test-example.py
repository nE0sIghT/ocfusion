#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Exercise the documented module's packaging, IPC and foreground lifecycle."""

import json
import os
import signal
import subprocess
import tarfile
import tempfile
import time
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]
EXAMPLE_MODULE = PROJECT_ROOT / "docs/examples/heartbeat"
OPERATIONS = ("validate", "prepare", "reload", "remove", "diagnose")


def package_example(target):
    with tarfile.open(target, "w:gz") as archive:
        for name in ("module.json", "bin", "ui"):
            archive.add(EXAMPLE_MODULE / name, arcname=name)


def test_ipc_operations():
    for operation in OPERATIONS:
        response = subprocess.run(
            [str(EXAMPLE_MODULE / "bin/module"), operation],
            input='{"api":1,"config":null}',
            text=True,
            capture_output=True,
            check=True,
        )
        assert json.loads(response.stdout)["ok"]


def test_foreground_lifecycle():
    process = subprocess.Popen(
        [str(EXAMPLE_MODULE / "bin/module"), "run"],
        start_new_session=True,
    )
    try:
        time.sleep(0.1)
        assert process.poll() is None
    finally:
        os.killpg(process.pid, signal.SIGTERM)
        process.wait(timeout=3)


def main():
    # The platform suite also installs/runs/removes this exact documented example.
    subprocess.run(
        ["go", "test", "-run", "Test", "./..."],
        cwd=PROJECT_ROOT / "platform",
        check=True,
    )
    with tempfile.TemporaryDirectory() as temporary:
        package_example(Path(temporary) / "heartbeat.tar.gz")
        test_ipc_operations()
        test_foreground_lifecycle()
    print("PASS: documented example packaging, IPC and foreground lifecycle")


if __name__ == "__main__":
    main()
