#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Verify static AArch64 runtime executables."""

import argparse
import json
import subprocess
from pathlib import Path


def command_output(*arguments):
    return subprocess.check_output(arguments, text=True).strip()


def verify_executable(path):
    header = command_output("aarch64-linux-gnu-readelf", "-h", str(path))
    sections = command_output("aarch64-linux-gnu-readelf", "-S", str(path))
    segments = command_output("aarch64-linux-gnu-readelf", "-l", str(path))
    assert "AArch64" in header and "INTERP" not in segments, path.name
    assert ".symtab" not in sections, path.name


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--spec", type=Path, action="append", default=[])
    arguments = parser.parse_args()
    runtime_tree = arguments.build_dir.resolve() / "runtime/ocfusion"
    verify_executable(runtime_tree / "bin/extd")
    for path in arguments.spec:
        specification = json.loads(path.read_text())
        for executable in specification["executables"]:
            verify_executable(
                runtime_tree / "modules" / specification["id"] / executable
            )
    arguments.output.write_text(arguments.version + "\n")


if __name__ == "__main__":
    main()
