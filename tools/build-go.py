#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Build one static Go executable for the router."""

import argparse
import json
import os
import subprocess
from pathlib import Path


def collect_notices(go, module, environment, destination):
    listing = subprocess.check_output(
        [go, "list", "-mod=readonly", "-deps", "-json", "."],
        cwd=module,
        env=environment,
        text=True,
    )
    decoder = json.JSONDecoder()
    modules = {}
    while listing.strip():
        package, length = decoder.raw_decode(listing.lstrip())
        listing = listing.lstrip()[length:]
        dependency = package.get("Module", {})
        if dependency and not dependency.get("Main"):
            modules[dependency["Path"]] = dependency

    sections = ["Go dependency licenses and notices\n"]
    for name, dependency in sorted(modules.items()):
        root = Path(dependency["Dir"])
        notices = sorted(
            path
            for path in root.iterdir()
            if path.is_file()
            and path.name.lower().startswith(
                ("license", "copying", "notice", "patents")
            )
        )
        if not notices:
            raise SystemExit(f"Dependency license missing: {name}")
        for path in notices:
            sections.append(
                f"\n{name} {dependency['Version']} - {path.name}\n\n"
                f"{path.read_text()}\n"
            )
    destination.write_text("".join(sections))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", required=True)
    parser.add_argument("--module", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--notices", type=Path)
    arguments = parser.parse_args()
    environment = dict(
        os.environ, CGO_ENABLED="0", GOOS="linux", GOARCH="arm64", GOTOOLCHAIN="local"
    )
    if arguments.notices:
        collect_notices(arguments.go, arguments.module, environment, arguments.notices)

    subprocess.run(
        [
            arguments.go,
            "build",
            "-mod=readonly",
            "-buildvcs=false",
            "-trimpath",
            (
                f"-ldflags=-s -w -buildid= -X main.version={arguments.version} "
                f"-X main.revision={arguments.revision}"
            ),
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
