#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Check incremental Ninja plans after a complete Meson runtime build."""

import argparse
import os
import re
import subprocess
import time
from contextlib import contextmanager
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]
C_TARGETS = ("musl", "zlib", "openssl", "libxml2", "openconnect")


def build_plan(build_directory):
    result = subprocess.run(
        ["ninja", "-C", str(build_directory), "-n"],
        capture_output=True,
        text=True,
        check=True,
    )
    return result.stdout


@contextmanager
def changed_mtime(path):
    original = path.stat()
    changed = max(time.time_ns(), original.st_mtime_ns + 1_000_000_000)
    os.utime(path, ns=(original.st_atime_ns, changed))
    try:
        yield
    finally:
        os.utime(path, ns=(original.st_atime_ns, original.st_mtime_ns))


def assert_targets(plan, expected, forbidden):
    for name in expected:
        pattern = rf"Generating (?:[^\s]+/)?{re.escape(name)} with a custom command"
        assert re.search(pattern, plan), plan
    for name in forbidden:
        pattern = rf"Generating (?:[^\s]+/)?{re.escape(name)} with a custom command"
        assert not re.search(pattern, plan), plan


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-dir", type=Path, required=True)
    arguments = parser.parse_args()
    assert "no work to do" in build_plan(arguments.build_dir), "Build runtime first."
    with changed_mtime(PROJECT_ROOT / "modules/openconnect/daemon/main.go"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["ocfusion-openconnect", "runtime-stage", "runtime"],
            [*C_TARGETS, "extd"],
        )
    with changed_mtime(PROJECT_ROOT / "modules/vpn/daemon/service.go"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["ocfusion-vpn", "runtime-stage", "runtime"],
            [*C_TARGETS, "ocfusion-openconnect", "extd"],
        )

    with changed_mtime(PROJECT_ROOT / "platform/header.js"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["extd", "runtime-stage", "runtime"],
            [*C_TARGETS, "ocfusion-openconnect", "ocfusion-vpn"],
        )

    with changed_mtime(PROJECT_ROOT / "modules/adguard-home/daemon/config.go"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["ocfusion-adguard-home", "runtime-stage", "runtime"],
            [*C_TARGETS, "AdGuardHome", "extd", "ocfusion-openconnect"],
        )
    with changed_mtime(
        PROJECT_ROOT / "modules/adguard-home/patches/0002-dnsproxy-marked-dialers.patch"
    ):
        assert_targets(
            build_plan(arguments.build_dir),
            ["AdGuardHome", "runtime-stage", "runtime"],
            [*C_TARGETS, "ocfusion-adguard-home", "extd"],
        )

    with changed_mtime(PROJECT_ROOT / "modules/radio-settings/daemon/settings.go"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["ocfusion-radio-settings", "runtime-stage", "runtime"],
            [*C_TARGETS, "AdGuardHome", "ocfusion-adguard-home", "extd"],
        )

    with changed_mtime(PROJECT_ROOT / "platform/manager.go"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["extd", "runtime-stage", "runtime"],
            [*C_TARGETS, "ocfusion-openconnect"],
        )
    with changed_mtime(
        PROJECT_ROOT / "modules/openconnect/patches/0001-managed-hook-environment.patch"
    ):
        assert_targets(
            build_plan(arguments.build_dir),
            ["openconnect", "runtime-stage", "runtime"],
            ["musl", "zlib", "openssl", "libxml2", "ocfusion-openconnect", "extd"],
        )
    with changed_mtime(PROJECT_ROOT / "third_party/build.json"):
        assert_targets(
            build_plan(arguments.build_dir),
            [*C_TARGETS, "runtime-stage", "runtime"],
            ["ocfusion-openconnect", "extd"],
        )
    with changed_mtime(arguments.build_dir / "third_party/cacert-2026-09-25.pem"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["runtime-stage", "runtime"],
            [*C_TARGETS, "ocfusion-openconnect", "extd"],
        )
    with changed_mtime(PROJECT_ROOT / "web/panel.js"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["runtime-stage", "runtime"],
            [*C_TARGETS, "ocfusion-openconnect", "extd"],
        )
    assert "no work to do" in build_plan(arguments.build_dir)
    print(
        "PASS: no-op rebuild; Go, recipe, patch and CA changes "
        "rebuild only their dependents"
    )


if __name__ == "__main__":
    main()
