#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Install verified upstream compatibility headers into the static sysroot."""

import argparse
import hashlib
import json
import tarfile
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]


def verified(path):
    lock = json.loads((PROJECT_ROOT / "third_party/sources.lock.json").read_text())[
        "sources"
    ]
    source = next(item for item in lock if item["name"] == path.name)
    if hashlib.sha256(path.read_bytes()).hexdigest() != source["sha256"]:
        raise SystemExit("Header source checksum mismatch")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--queue", type=Path, required=True)
    parser.add_argument("--capability", type=Path, required=True)
    parser.add_argument("--build-root", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    verified(args.queue)
    verified(args.capability)

    destination = args.build_root / "target/include/sys"
    destination.mkdir(parents=True, exist_ok=True)
    queue = args.queue.read_bytes()
    (destination / "queue.h").write_bytes(queue)
    with tarfile.open(args.capability) as archive:
        root = archive.getmembers()[0].name.split("/")[0]
        capability = archive.extractfile(
            root + "/libcap/include/sys/capability.h"
        ).read()
        license_text = archive.extractfile(root + "/License").read()

    (destination / "capability.h").write_bytes(capability)
    notices = args.build_root / "licenses"
    notices.mkdir(exist_ok=True)
    (notices / "zapret2-compatibility-headers.txt").write_bytes(
        queue + b"\nlibcap headers\n" + capability + b"\n" + license_text
    )
    args.output.write_text("Compatibility headers installed\n")


if __name__ == "__main__":
    main()
