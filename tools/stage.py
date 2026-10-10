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


def render_version(runtime_tree, version):
    for template in sorted((runtime_tree / "modules").glob("*/module.json.in")):
        manifest = json.loads(template.read_text())
        manifest["version"] = version
        (template.parent / "module.json").write_text(
            json.dumps(manifest, indent=2) + "\n"
        )
        template.unlink()

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
        (runtime_tree / name).write_text(document)


def combine_notices(runtime_tree, build_directory):
    sections = ["# Third-party licenses and notices\n"]
    notice_directories = (
        PROJECT_ROOT / "third_party/notices",
        build_directory / "licenses",
    )

    for directory in notice_directories:
        for notice in sorted(directory.iterdir()):
            sections.append(f"\n## {notice.name}\n\n{notice.read_text()}\n")

    (runtime_tree / "THIRD-PARTY.md").write_text("".join(sections))


def copy_module_assets(runtime_tree, module_id):
    module_root = PROJECT_ROOT / "modules" / module_id
    package = runtime_tree / "modules" / module_id
    package.mkdir(parents=True)
    shutil.copy(module_root / "packaging/module.json.in", package)
    for name in ("examples", "scripts"):
        if (module_root / "packaging" / name).is_dir():
            shutil.copytree(module_root / "packaging" / name, package / name)
    shutil.copytree(module_root / "ui", package / "ui")
    (package / "bin").mkdir()


def verify_ca_bundle(path):
    sources = json.loads((PROJECT_ROOT / "third_party/sources.lock.json").read_text())[
        "sources"
    ]
    source = next(item for item in sources if item["name"] == path.name)
    if hashlib.sha256(path.read_bytes()).hexdigest() != source["sha256"]:
        raise SystemExit("CA bundle checksum mismatch")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--build-dir", type=Path, required=True)
    parser.add_argument("--client", type=Path, required=True)
    parser.add_argument("--supervisor", type=Path, required=True)
    parser.add_argument("--vpn-service", type=Path, required=True)
    parser.add_argument("--manager", type=Path, required=True)
    parser.add_argument("--ca", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    arguments = parser.parse_args()
    verify_ca_bundle(arguments.ca)
    runtime_tree = arguments.build_dir.resolve() / "runtime/ocfusion"
    if runtime_tree.exists():
        shutil.rmtree(runtime_tree)
    runtime_tree.mkdir(parents=True)
    shutil.copy(PROJECT_ROOT / "packaging/install.sh", runtime_tree)
    shutil.copytree(PROJECT_ROOT / "packaging/scripts", runtime_tree / "scripts")
    shutil.copytree(PROJECT_ROOT / "packaging/init", runtime_tree / "init")
    shutil.copytree(PROJECT_ROOT / "web", runtime_tree / "web")
    for module_id in ("vpn", "openconnect"):
        copy_module_assets(runtime_tree, module_id)
    (runtime_tree / "bin").mkdir()

    version = arguments.version
    render_version(runtime_tree, version)
    copy_documentation(runtime_tree)
    for executable, name in [
        (arguments.client, "modules/openconnect/bin/openconnect"),
        (arguments.supervisor, "modules/openconnect/bin/ocfusion-openconnect"),
        (arguments.vpn_service, "modules/vpn/bin/ocfusion-vpn"),
        (arguments.manager, "bin/extd"),
    ]:
        shutil.copy(executable, runtime_tree / name)
    shutil.copy(arguments.ca, runtime_tree / "modules/openconnect/ca-bundle.crt")
    combine_notices(runtime_tree, arguments.build_dir)
    arguments.output.write_text(version + "\n")


if __name__ == "__main__":
    main()
