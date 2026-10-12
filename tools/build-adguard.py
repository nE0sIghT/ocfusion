#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Build pinned AdGuard Home with private control and marked DNS dialers."""

import argparse
import json
import os
import shutil
import subprocess
import tarfile
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]


def run(arguments, directory, environment):
    return subprocess.check_output(arguments, cwd=directory, env=environment, text=True)


def patch_source(directory, name):
    patch = PROJECT_ROOT / "modules/adguard-home/patches" / name
    with patch.open("rb") as content:
        subprocess.run(
            ["patch", "--batch", "--forward", "-p1"],
            cwd=directory,
            stdin=content,
            check=True,
        )


def collect_notices(source, modules, directory):
    sections = ["AdGuard Home and Go dependencies\n"]
    for name, root in [("AdGuard Home", source), *modules]:
        notices = sorted(
            file
            for file in root.iterdir()
            if file.is_file()
            and file.name.lower().startswith(("license", "copying", "notice"))
        )
        if not notices:
            raise SystemExit(f"Dependency license missing: {name}")
        for file in notices:
            sections.append(f"\n{name} - {file.name}\n\n{file.read_text()}\n")
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "AdGuardHome-and-dependencies.txt").write_text("".join(sections))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--go", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--build-root", type=Path, required=True)
    parser.add_argument("--architecture", default="arm64", choices=["arm64", "amd64"])
    arguments = parser.parse_args()
    build_root = arguments.build_root.resolve()
    source = build_root / "src/adguard-home"
    if source.exists():
        shutil.rmtree(source)
    source.mkdir(parents=True)
    with tarfile.open(arguments.archive) as archive:
        for member in archive.getmembers():
            member.name = member.name.partition("/")[2]
            if member.name:
                archive.extract(member, source, filter="data")
    environment = dict(os.environ, GOTOOLCHAIN="local", CGO_ENABLED="0")
    proxy = json.loads(
        run(
            [
                arguments.go,
                "mod",
                "download",
                "-json",
                "github.com/AdguardTeam/dnsproxy",
            ],
            source,
            environment,
        )
    )
    proxy_source = build_root / "src/adguard-dnsproxy"
    if proxy_source.exists():
        shutil.rmtree(proxy_source)
    shutil.copytree(proxy["Dir"], proxy_source)
    proxy_source.chmod(proxy_source.stat().st_mode | 0o200)
    for file in proxy_source.rglob("*"):
        file.chmod(file.stat().st_mode | 0o200)
    patch_source(source, "0001-private-headless-control.patch")
    patch_source(source, "0003-filter-update-status.patch")
    patch_source(proxy_source, "0002-dnsproxy-marked-dialers.patch")
    run(
        [
            arguments.go,
            "mod",
            "edit",
            "-replace",
            f"github.com/AdguardTeam/dnsproxy={proxy_source}",
        ],
        source,
        environment,
    )
    environment.update(GOOS="linux", GOARCH=arguments.architecture)
    listing = run(
        [
            arguments.go,
            "list",
            "-mod=readonly",
            "-tags=ocfusion",
            "-deps",
            "-json",
            ".",
        ],
        source,
        environment,
    )
    decoder = json.JSONDecoder()
    modules = []
    seen = set()
    while listing.strip():
        package, length = decoder.raw_decode(listing.lstrip())
        listing = listing.lstrip()[length:]
        module = package.get("Module")
        if not module or module.get("Main") or module["Path"] in seen:
            continue
        seen.add(module["Path"])
        location = module.get("Replace", module).get("Dir")
        if location:
            modules.append(
                (module["Path"] + " " + module.get("Version", ""), Path(location))
            )
    collect_notices(source, modules, build_root / "licenses")
    environment.update(GOOS="linux", GOARCH=arguments.architecture)
    arguments.output.parent.mkdir(parents=True, exist_ok=True)
    upstream_version = arguments.archive.name.removeprefix("AdGuardHome-").removesuffix(
        ".tar.gz"
    )
    version_package = "github.com/AdguardTeam/AdGuardHome/internal/version"
    linker_flags = (
        f"-s -w -buildid= -X {version_package}.version={upstream_version}-ocfusion "
        f"-X {version_package}.channel=release"
    )
    run(
        [
            arguments.go,
            "build",
            "-mod=readonly",
            "-buildvcs=false",
            "-trimpath",
            "-tags=ocfusion",
            f"-ldflags={linker_flags}",
            "-o",
            str(arguments.output.resolve()),
            ".",
        ],
        source,
        environment,
    )


if __name__ == "__main__":
    main()
