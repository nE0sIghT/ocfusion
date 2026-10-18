#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Check incremental Ninja plans after a complete Meson runtime build."""

import argparse
import json
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
    targets = json.loads(
        (arguments.build_dir / "meson-info/intro-targets.json").read_text()
    )
    modules = [
        target["name"].removeprefix("module-")
        for target in targets
        if target["name"].startswith("module-")
    ]
    for module in modules:
        target = next(
            target for target in targets if target["name"] == "module-" + module
        )
        output = Path(target["filename"][0]).relative_to(arguments.build_dir.resolve())
        commands = subprocess.check_output(
            ["ninja", "-C", str(arguments.build_dir), "-t", "commands", str(output)],
            text=True,
        )
        assert str(PROJECT_ROOT / "platform") not in commands, commands
        for other in modules:
            if other != module:
                assert (
                    str(PROJECT_ROOT / "modules" / other / "daemon") not in commands
                ), commands

    options = json.loads(
        (arguments.build_dir / "meson-info/intro-buildoptions.json").read_text()
    )
    selected = next(
        option["value"] for option in options if option["name"] == "modules"
    )
    if selected == ["all"]:
        selected = modules

    changes = [
        (
            "openconnect",
            "modules/openconnect/daemon/main.go",
            ["ocfusion-openconnect"],
            [*C_TARGETS, "extd"],
        ),
        (
            "vpn",
            "modules/vpn/daemon/service.go",
            ["ocfusion-vpn"],
            [*C_TARGETS, "ocfusion-openconnect", "extd"],
        ),
        (
            "adguard-home",
            "modules/adguard-home/daemon/config.go",
            ["ocfusion-adguard-home"],
            [*C_TARGETS, "AdGuardHome", "extd"],
        ),
        (
            "adguard-home",
            "modules/adguard-home/patches/0002-dnsproxy-marked-dialers.patch",
            ["AdGuardHome"],
            [*C_TARGETS, "ocfusion-adguard-home", "extd"],
        ),
        (
            "radio-settings",
            "modules/radio-settings/daemon/settings.go",
            ["ocfusion-radio-settings"],
            [*C_TARGETS, "AdGuardHome", "ocfusion-adguard-home", "extd"],
        ),
        (
            "openconnect",
            "modules/openconnect/patches/0001-managed-hook-environment.patch",
            ["openconnect"],
            ["musl", "zlib", "openssl", "libxml2", "ocfusion-openconnect", "extd"],
        ),
    ]
    for module, source, expected, forbidden in changes:
        expected = expected + ["runtime-stage", "runtime"]
        if module not in selected:
            forbidden += expected
            expected = []
        with changed_mtime(PROJECT_ROOT / source):
            assert_targets(build_plan(arguments.build_dir), expected, forbidden)

    with changed_mtime(PROJECT_ROOT / "platform/header.js"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["extd", "runtime-stage", "runtime"],
            [*C_TARGETS, "ocfusion-openconnect", "ocfusion-vpn"],
        )

    with changed_mtime(PROJECT_ROOT / "platform/manager.go"):
        assert_targets(
            build_plan(arguments.build_dir),
            ["extd", "runtime-stage", "runtime"],
            [*C_TARGETS, "ocfusion-openconnect"],
        )
    with changed_mtime(PROJECT_ROOT / "third_party/build.json"):
        expected = (
            [*C_TARGETS, "runtime-stage", "runtime"]
            if "openconnect" in selected
            else []
        )
        forbidden = ["ocfusion-openconnect", "extd"]
        if not expected:
            forbidden += [*C_TARGETS, "runtime-stage", "runtime"]
        assert_targets(build_plan(arguments.build_dir), expected, forbidden)

    ca = arguments.build_dir / "third_party/cacert-2026-09-25.pem"
    if ca.exists():
        with changed_mtime(ca):
            expected = (
                ["runtime-stage", "runtime"]
                if {"openconnect", "adguard-home"}.intersection(selected)
                else []
            )
            forbidden = [*C_TARGETS, "ocfusion-openconnect", "extd"]
            if not expected:
                forbidden += ["runtime-stage", "runtime"]
            assert_targets(build_plan(arguments.build_dir), expected, forbidden)
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
