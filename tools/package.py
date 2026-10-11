#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Build deterministic runtime and source archives from explicit inputs."""

import argparse
import gzip
import hashlib
import io
import os
import tarfile
from pathlib import Path, PurePosixPath

from version import resolve_revision

PROJECT_ROOT = Path(__file__).resolve().parents[1]
SOURCE_DIRECTORIES = (
    "modules",
    "platform",
    "web",
    "packaging",
    "third_party",
    "tools",
    "tests",
    "docs",
    "build-env",
    ".github",
)
SOURCE_FILES = (
    "AGENTS.md",
    "README.md",
    "INSTALLATION.md",
    "LICENSE",
    "meson.build",
    "meson.options",
    "ruff.toml",
    ".gitignore",
    ".gitattributes",
    ".dockerignore",
    "package.json",
    "package-lock.json",
)
EXECUTABLES = (
    "modules/openconnect/bin/openconnect",
    "modules/openconnect/bin/ocfusion-openconnect",
    "modules/vpn/bin/ocfusion-vpn",
    "bin/extd",
)


def sha256_file(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_checksums(target, files, relative_to):
    lines = [
        f"{sha256_file(path)}  {path.relative_to(relative_to)}\n" for path in files
    ]
    target.write_text("".join(lines))


def write_archive(target, entries, timestamp, source_version="", source_revision=""):
    with (
        target.open("wb") as output,
        gzip.GzipFile(
            filename="", mode="wb", fileobj=output, mtime=timestamp
        ) as compressed,
        tarfile.open(fileobj=compressed, mode="w") as archive,
    ):
        for name, path in sorted(entries):
            if not path.is_file():
                continue
            data = path.read_bytes()
            entry = tarfile.TarInfo(name)
            entry.size = len(data)
            entry.mtime = timestamp
            entry.uid = entry.gid = 0
            entry.uname = entry.gname = ""
            entry.mode = 0o755 if path.stat().st_mode & 0o111 else 0o644
            archive.addfile(entry, io.BytesIO(data))

        if source_revision:
            root = f"ocfusion-{source_version}"
            data = (source_revision + "\n").encode()
            entry = tarfile.TarInfo(root + "/.git-revision")
            entry.size = len(data)
            entry.mtime = timestamp
            entry.mode = 0o644
            archive.addfile(entry, io.BytesIO(data))


def runtime_entries(runtime_tree):
    for path in runtime_tree.rglob("*"):
        if path.is_file():
            name = PurePosixPath("ocfusion") / path.relative_to(runtime_tree)
            yield str(name), path


def source_entries(version):
    archive_root = PurePosixPath(f"ocfusion-{version}")
    # Never traverse the whole workspace: downloads, firmware and profiles stay out.
    for directory in SOURCE_DIRECTORIES:
        for path in (PROJECT_ROOT / directory).rglob("*"):
            if (
                not path.is_file()
                or "__pycache__" in path.parts
                or path.suffix == ".pyc"
            ):
                continue
            yield str(archive_root / path.relative_to(PROJECT_ROOT)), path
    for name in SOURCE_FILES:
        yield str(archive_root / name), PROJECT_ROOT / name


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-dir", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--version", required=True)
    arguments = parser.parse_args()
    runtime_tree = arguments.build_dir.resolve() / "runtime/ocfusion"
    output_directory = arguments.output_dir.resolve()
    version = arguments.version
    timestamp = int(os.environ.get("SOURCE_DATE_EPOCH", "0"))
    assert all((runtime_tree / name).is_file() for name in EXECUTABLES), (
        "run meson compile before packaging"
    )
    output_directory.mkdir(exist_ok=True)

    runtime_files = sorted(
        path
        for path in runtime_tree.rglob("*")
        if path.is_file() and path.name != "SHA256SUMS"
    )
    write_checksums(runtime_tree / "SHA256SUMS", runtime_files, runtime_tree)

    runtime_archive = output_directory / f"ocfusion-{version}-aarch64.tar.gz"
    source_archive = output_directory / f"ocfusion-{version}-source.tar.gz"
    write_archive(runtime_archive, runtime_entries(runtime_tree), timestamp)
    write_archive(
        source_archive, source_entries(version), timestamp, version, resolve_revision()
    )
    write_checksums(
        output_directory / "SHA256SUMS",
        [runtime_archive, source_archive],
        output_directory,
    )

    for archive in (runtime_archive, source_archive):
        print("Created", archive.name, archive.stat().st_size, "bytes")


if __name__ == "__main__":
    main()
