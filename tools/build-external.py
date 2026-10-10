#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Build one upstream C project; Meson owns all dependency ordering."""

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import tarfile
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]
BUILD_CONFIG = PROJECT_ROOT / "third_party/build.json"


def load_build_config():
    config = json.loads(BUILD_CONFIG.read_text())
    if config["schema"] != 1:
        raise ValueError("Unsupported third-party build configuration")

    return config


def expand(values, parameters):
    return [value.format_map(parameters) for value in values]


def run(command, directory, environment, log):
    with log.open("ab") as output:
        subprocess.run(
            command,
            cwd=directory,
            env=environment,
            stdout=output,
            stderr=subprocess.STDOUT,
            check=True,
        )


def compiler_environment(config, recipe, parameters):
    values = dict(config["environment"])
    if recipe["uses_musl"]:
        values.update(config["static_environment"])

    values.update(recipe.get("environment", {}))
    environment = dict(os.environ)
    environment.update(
        {name: value.format_map(parameters) for name, value in values.items()}
    )

    return environment


def apply_patches(recipe, source_directory):
    for relative_path in recipe.get("patches", []):
        with (PROJECT_ROOT / relative_path).open("rb") as patch:
            subprocess.run(
                ["patch", "--batch", "--forward", "-p1"],
                cwd=source_directory,
                stdin=patch,
                check=True,
            )


def build_component(arguments, config):
    recipe = config["components"][arguments.component]
    sources = json.loads((PROJECT_ROOT / "third_party/sources.lock.json").read_text())[
        "sources"
    ]
    source = next(item for item in sources if item["name"] == arguments.archive.name)
    if hashlib.sha256(arguments.archive.read_bytes()).hexdigest() != source["sha256"]:
        raise SystemExit(f"Source checksum mismatch: {arguments.archive.name}")

    build_root = arguments.build_root.resolve()
    prefix = build_root / "target"
    source_root = build_root / "src"
    log_root = build_root / "logs"
    license_root = build_root / "licenses"
    for directory in (prefix, source_root, log_root, license_root):
        directory.mkdir(parents=True, exist_ok=True)

    source_name = arguments.archive.name.split(".tar.", 1)[0]
    if not source_name.startswith(arguments.component + "-"):
        raise ValueError("Archive does not match the selected component")

    source_directory = source_root / source_name
    if source_directory.exists():
        shutil.rmtree(source_directory)
    with tarfile.open(arguments.archive) as archive:
        archive.extractall(source_root, filter="data")

    parameters = {
        "prefix": str(prefix),
        "c_flags": arguments.c_flags,
        "source_version": source_name.removeprefix(arguments.component + "-"),
    }
    environment = compiler_environment(config, recipe, parameters)
    configure_log = log_root / f"{arguments.component}-configure.log"
    make_log = log_root / f"{arguments.component}-make.log"
    configure_log.write_bytes(b"")
    make_log.write_bytes(b"")
    apply_patches(recipe, source_directory)

    try:
        run(
            expand(recipe["configure"], parameters),
            source_directory,
            environment,
            configure_log,
        )
        run(["make", "clean"], source_directory, environment, make_log)
        make_command = ["make", f"-j{arguments.jobs}"]
        for name in recipe.get("remove_before_make", []):
            (source_directory / name).unlink(missing_ok=True)

        make_command.extend(expand(recipe.get("make_arguments", []), parameters))
        run(make_command, source_directory, environment, make_log)

        if recipe["install_target"]:
            run(
                ["make", recipe["install_target"]],
                source_directory,
                environment,
                make_log,
            )
    except subprocess.CalledProcessError as error:
        raise SystemExit(
            f"{arguments.component} build failed; "
            f"inspect {configure_log} and {make_log}"
        ) from error

    for name in recipe.get("kernel_headers", []):
        shutil.copytree(
            Path("/usr/aarch64-linux-gnu/include") / name,
            prefix / "include" / name,
            dirs_exist_ok=True,
            symlinks=True,
        )

    shutil.copy(
        source_directory / recipe["license_file"],
        license_root / recipe["notice_file"],
    )

    output = arguments.output.resolve()
    if recipe.get("executable"):
        shutil.copy(source_directory / recipe["executable"], output)
        subprocess.run(["aarch64-linux-gnu-strip", str(output)], check=True)
    else:
        output.write_text(f"{source_name}\n")


def main():
    config = load_build_config()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--component", choices=config["components"], required=True)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--build-root", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--jobs", type=int, default=8)
    parser.add_argument("--c-flags", required=True)
    build_component(parser.parse_args(), config)


if __name__ == "__main__":
    main()
