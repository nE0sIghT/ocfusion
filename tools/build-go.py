#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Build one static Go executable for the router."""

import argparse
import os
import subprocess
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", required=True)
    parser.add_argument("--module", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    arguments = parser.parse_args()
    environment = dict(
        os.environ, CGO_ENABLED="0", GOOS="linux", GOARCH="arm64", GOTOOLCHAIN="local"
    )
    subprocess.run(
        [
            arguments.go,
            "build",
            "-buildvcs=false",
            "-trimpath",
            f"-ldflags=-s -w -buildid= -X main.version={arguments.version}",
            "-o",
            str(arguments.output.resolve()),
            ".",
        ],
        cwd=arguments.module,
        env=environment,
        check=True,
    )


if __name__ == "__main__":
    main()
