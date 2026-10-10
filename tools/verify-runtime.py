#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Verify static AArch64 runtime executables."""

import argparse
import subprocess
from pathlib import Path

EXECUTABLES = (
    "modules/openconnect/bin/openconnect",
    "modules/openconnect/bin/ocfusion-openconnect",
    "modules/vpn/bin/ocfusion-vpn",
    "bin/extd",
)


def command_output(*arguments):
    return subprocess.check_output(arguments, text=True).strip()


def verify_executable(path):
    header = command_output("aarch64-linux-gnu-readelf", "-h", str(path))
    segments = command_output("aarch64-linux-gnu-readelf", "-l", str(path))
    assert "AArch64" in header and "INTERP" not in segments, path.name


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    arguments = parser.parse_args()
    runtime_tree = arguments.build_dir.resolve() / "runtime/ocfusion"
    for name in EXECUTABLES:
        verify_executable(runtime_tree / name)
    arguments.output.write_text(arguments.version + "\n")


if __name__ == "__main__":
    main()
