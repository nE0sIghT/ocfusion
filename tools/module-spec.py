#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Describe module packages and check selected runtime dependencies for Meson."""

import argparse
import json
from pathlib import Path


def check_selection(specifications):
    manifests = {}
    for specification in specifications:
        source = next(
            source
            for source, destination in specification["files"]
            if destination == "module.json.in"
        )
        manifest = json.loads(Path(source).read_text())
        if manifest["id"] != specification["id"]:
            raise ValueError("Module catalog ID does not match its manifest")
        manifests[manifest["id"]] = manifest

    for module_id, manifest in manifests.items():
        for dependency in manifest.get("dependencies", []):
            provider = manifests.get(dependency["id"])
            if provider is None or provider.get("provides_api") != dependency["api"]:
                raise ValueError(
                    f"Module {module_id} requires {dependency['id']} "
                    f"API {dependency['api']} in the selected runtime"
                )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="operation", required=True)
    describe = commands.add_parser("describe")
    describe.add_argument("--id", required=True)
    describe.add_argument("--file", nargs=2, action="append", default=[])
    describe.add_argument("--verified-file", nargs=2, action="append", default=[])
    describe.add_argument("--executable", action="append", default=[])
    describe.add_argument("--notice", action="append", default=[])
    describe.add_argument("--output", type=Path, required=True)
    selection = commands.add_parser("check-selection")
    selection.add_argument("--spec", type=Path, action="append", default=[])
    arguments = parser.parse_args()

    if arguments.operation == "check-selection":
        try:
            check_selection([json.loads(path.read_text()) for path in arguments.spec])
        except ValueError as error:
            parser.error(str(error))
        return

    specification = {
        "id": arguments.id,
        "files": arguments.file + arguments.verified_file,
        "verified_files": [source for source, _ in arguments.verified_file],
        "executables": arguments.executable,
        "notices": arguments.notice,
    }
    arguments.output.write_text(json.dumps(specification, indent=2) + "\n")


if __name__ == "__main__":
    main()
