#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Check source-only inputs, license headers and public-text conventions."""

import json
import re
import subprocess
from pathlib import Path

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
ROOT_FILES = (
    "AGENTS.md",
    "README.md",
    "meson.build",
    "meson.options",
    "ruff.toml",
    ".gitignore",
    ".gitattributes",
    ".dockerignore",
    "package.json",
    "package-lock.json",
)
LICENSE_HEADER = "SPDX-License-Identifier: GPL-3.0-or-later"
# Only programming-language source and executable shell scripts carry our header.
CODE_SUFFIXES = {".go", ".py", ".js", ".cjs", ".sh"}
FORBIDDEN_TRACKED_PREFIXES = ("contrib/", "build/", "dist/", "downloads/")


def source_files():
    for directory in SOURCE_DIRECTORIES:
        for path in (PROJECT_ROOT / directory).rglob("*"):
            if (
                path.is_file()
                and "__pycache__" not in path.parts
                and path.suffix != ".pyc"
            ):
                yield path
    for name in ROOT_FILES:
        yield PROJECT_ROOT / name


def check_file(path):
    relative = path.relative_to(PROJECT_ROOT)
    if "notices" in relative.parts:
        return  # Preserve unmodified third-party notices.
    assert path.suffix != ".license", f"unexpected license sidecar: {relative}"

    data = path.read_bytes()
    assert not data.startswith(b"\x7fELF"), relative
    if path.suffix == ".png":
        return

    text = data.decode("utf-8")
    assert ("SPDX-" + "FileCopyrightText:") not in text, relative
    assert chr(0x2014) not in text and chr(0x2013) not in text, relative
    assert ("Over" + "clock") not in text, relative
    is_readme = path.name == "README.md"
    is_document = relative.parts[0] in {"docs", "build-env"} and path.suffix == ".md"
    if is_document or (is_readme and "examples" not in relative.parts):
        assert not re.search("[\u0400-\u04ff]", text), relative

    is_owned_code = relative.parts[0] != "third_party" and (
        path.suffix in CODE_SUFFIXES or data.startswith(b"#!/bin/sh")
    )
    if is_owned_code:
        assert LICENSE_HEADER in text[:600], f"missing SPDX header: {relative}"
    else:
        assert "SPDX-" not in text, f"SPDX outside owned code: {relative}"


def check_dependency_lock():
    lock = json.loads((PROJECT_ROOT / "third_party/sources.lock.json").read_text())
    for source in lock["sources"]:
        assert re.fullmatch("[0-9a-f]{64}", source["sha256"]), source["name"]
        assert source["url"].startswith("https://"), source["name"]


def check_tracked_paths():
    if not (PROJECT_ROOT / ".git").exists():
        return
    output = subprocess.check_output(["git", "ls-files", "-z"], cwd=PROJECT_ROOT)
    for name in output.decode().split("\0"):
        assert not name.startswith(FORBIDDEN_TRACKED_PREFIXES) and name != "PLAN.md", (
            name
        )


def main():
    for path in source_files():
        check_file(path)
    check_dependency_lock()
    check_tracked_paths()
    print(
        "PASS: source-only tree, SPDX, English docs, project name, "
        "ASCII dashes, dependency hashes"
    )


if __name__ == "__main__":
    main()
