#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Build the locked upstream proxy for native protocol integration tests."""

import argparse
import hashlib
import json
import shutil
import subprocess
import tarfile
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    sources = json.loads((ROOT / "third_party/sources.lock.json").read_text())[
        "sources"
    ]
    source = next(item for item in sources if item["name"] == args.archive.name)
    assert hashlib.sha256(args.archive.read_bytes()).hexdigest() == source["sha256"]
    output = args.output.resolve()

    with tempfile.TemporaryDirectory(prefix="proxy-fixture-", dir=output.parent) as tmp:
        with tarfile.open(args.archive) as archive:
            archive.extractall(tmp, filter="data")

        directory = Path(tmp) / args.archive.name.removesuffix(".tar.gz")
        with (output.parent / "proxy-native-build.log").open("wb") as log:
            subprocess.run(
                [
                    "make",
                    "-f",
                    "Makefile.Linux",
                    "-j4",
                    "CC=gcc",
                    "LN=gcc",
                    "STATIC=true",
                    "OPENSSL_CHECK=false",
                    "WOLFSSL_CHECK=false",
                    "PAM_CHECK=false",
                ],
                cwd=directory,
                stdout=log,
                stderr=subprocess.STDOUT,
                check=True,
            )

        shutil.copyfile(directory / "bin/3proxy", output)
        output.chmod(0o755)


if __name__ == "__main__":
    main()
