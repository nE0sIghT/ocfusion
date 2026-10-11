#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Resolve a build version from an explicit override or Git release tags."""

import argparse
import re
import subprocess
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]
RELEASE_VERSION = re.compile(r"[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[A-Za-z0-9.-]+)?")


def git_output(*arguments):
    result = subprocess.run(
        ["git", "-C", str(PROJECT_ROOT), *arguments],
        capture_output=True,
        text=True,
    )
    return result.stdout.strip() if result.returncode == 0 else ""


def resolve_version(override=""):
    if override:
        if not RELEASE_VERSION.fullmatch(override):
            raise SystemExit(
                "Invalid release_version: use MAJOR.MINOR[.PATCH][-suffix]"
            )
        return override
    exact_tag = git_output("describe", "--tags", "--exact-match", "--match", "v[0-9]*")
    if exact_tag and RELEASE_VERSION.fullmatch(exact_tag[1:]):
        return exact_tag[1:]
    description = git_output("describe", "--tags", "--match", "v[0-9]*", "--long")
    match = re.fullmatch(r"v(.+)-(\d+)-g([0-9a-f]+)", description)
    if match and RELEASE_VERSION.fullmatch(match[1]):
        return f"{match[1]}-dev.{match[2]}.g{match[3]}"
    revision = git_output("rev-parse", "--short", "HEAD")
    return "0.0.0-dev" + (".g" + revision if revision else "")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--override", default="")
    print(resolve_version(parser.parse_args().override))


if __name__ == "__main__":
    main()
