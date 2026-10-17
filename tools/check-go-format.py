#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Enforce standard Go formatting without modifying source files."""

import subprocess
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]


def main():
    sources = sorted(
        path
        for module in (
            "modules/openconnect/daemon",
            "modules/vpn/daemon",
            "modules/adguard-home/daemon",
            "modules/radio-settings/daemon",
            "platform",
        )
        for path in (PROJECT_ROOT / module).glob("*.go")
    )
    result = subprocess.check_output(["gofmt", "-l", *map(str, sources)], text=True)
    if result.strip():
        raise SystemExit("Run gofmt on:\n" + result)
    print("PASS: gofmt")


if __name__ == "__main__":
    main()
