#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Download and verify locked build inputs before extracting any source."""

import argparse
import hashlib
import json
import os
from pathlib import Path
from urllib.request import urlopen

PROJECT_ROOT = Path(__file__).resolve().parents[1]
SOURCE_LOCK = PROJECT_ROOT / "third_party/sources.lock.json"
CHUNK_SIZE = 1024 * 1024


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(CHUNK_SIZE), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_download(path, expected_digest):
    if sha256_file(path) != expected_digest:
        raise SystemExit(f"Checksum mismatch: {path.name}")


def fetch_source(source, target):
    target.parent.mkdir(parents=True, exist_ok=True)
    if not target.exists():
        print("Fetch", target.name, flush=True)
        partial = target.with_suffix(target.suffix + ".part")
        with (
            urlopen(source["url"], timeout=90) as response,
            partial.open("wb") as destination,
        ):
            while chunk := response.read(CHUNK_SIZE):
                destination.write(chunk)

        if sha256_file(partial) != source["sha256"]:
            partial.unlink()
            raise SystemExit(f"Checksum mismatch: {target.name}")
        os.replace(partial, target)

    verify_download(target, source["sha256"])
    print("Verified", target.name, flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", type=Path, required=True)
    arguments = parser.parse_args()
    sources = json.loads(SOURCE_LOCK.read_text())["sources"]
    source = next(item for item in sources if item["name"] == arguments.source)
    fetch_source(source, arguments.output)


if __name__ == "__main__":
    main()
