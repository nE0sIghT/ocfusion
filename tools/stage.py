#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Assemble the runtime package from declared Meson inputs."""

import argparse
import hashlib
import json
import shutil
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]
DOCUMENTS = ("README.md", "INSTALLATION.md")


def render_module_version(package, version):
    template = package / "module.json.in"
    manifest = json.loads(template.read_text())
    manifest["version"] = version
    (package / "module.json").write_text(json.dumps(manifest, indent=2) + "\n")
    template.unlink()


def render_version(runtime_tree, version):
    installer = runtime_tree / "install.sh"
    installer.write_text(
        installer.read_text().replace("Version: @VERSION@", "Version: " + version)
    )


def copy_documentation(runtime_tree):
    shutil.copy(PROJECT_ROOT / "LICENSE", runtime_tree / "LICENSE")
    for name in DOCUMENTS:
        document = (PROJECT_ROOT / name).read_text()
        for topic in ("building", "modules", "architecture"):
            document = document.replace(
                f"(docs/{topic}.md)",
                f"(https://github.com/nE0sIghT/ocfusion/blob/main/docs/{topic}.md)",
            )
        document = document.replace(
            'src="docs/screenshots/',
            'src="https://raw.githubusercontent.com/nE0sIghT/ocfusion/main/docs/screenshots/',
        )
        (runtime_tree / name).write_text(document)


def combine_notices(runtime_tree, specifications):
    sections = ["# Third-party licenses and notices\n"]
    notices = sorted((PROJECT_ROOT / "third_party/notices").iterdir())
    for specification in specifications:
        notices.extend(Path(name) for name in specification["notices"])

    for notice in notices:
        sections.append(f"\n## {notice.name}\n\n{notice.read_text()}\n")

    (runtime_tree / "THIRD-PARTY.md").write_text("".join(sections))


def stage_module(package, specification, version):
    for source in specification["verified_files"]:
        verify_data_source(Path(source))

    for source, destination in specification["files"]:
        target = package / destination
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(source, target)
    render_module_version(package, version)


def verify_data_source(path):
    sources = json.loads((PROJECT_ROOT / "third_party/sources.lock.json").read_text())[
        "sources"
    ]
    source = next(item for item in sources if item["name"] == path.name)
    if hashlib.sha256(path.read_bytes()).hexdigest() != source["sha256"]:
        raise SystemExit("Data source checksum mismatch")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-dir", type=Path, required=True)
    parser.add_argument("--manager", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--spec", type=Path, action="append", default=[])
    arguments = parser.parse_args()
    specifications = [json.loads(path.read_text()) for path in arguments.spec]
    runtime_tree = arguments.build_dir.resolve() / "runtime/ocfusion"
    if runtime_tree.exists():
        shutil.rmtree(runtime_tree)
    runtime_tree.mkdir(parents=True)
    shutil.copy(PROJECT_ROOT / "packaging/install.sh", runtime_tree)
    shutil.copytree(PROJECT_ROOT / "packaging/scripts", runtime_tree / "scripts")
    shutil.copytree(PROJECT_ROOT / "packaging/init", runtime_tree / "init")
    shutil.copytree(PROJECT_ROOT / "web", runtime_tree / "web")
    (runtime_tree / "modules").mkdir()
    for specification in specifications:
        stage_module(
            runtime_tree / "modules" / specification["id"],
            specification,
            arguments.version,
        )
    (runtime_tree / "bin").mkdir()

    version = arguments.version
    render_version(runtime_tree, version)
    copy_documentation(runtime_tree)
    shutil.copy(arguments.manager, runtime_tree / "bin/extd")
    combine_notices(runtime_tree, specifications)
    arguments.output.write_text(version + "\n")


if __name__ == "__main__":
    main()
