#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Exercise released 0.3.1 state and rollback with native managers and VPN hooks."""

import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]


def build(source, output, variables):
    flags = " ".join(f"-X main.{key}={value}" for key, value in variables.items())
    subprocess.run(
        ["go", "build", "-trimpath", f"-ldflags={flags}", "-o", str(output), "."],
        cwd=source,
        check=True,
    )


def adapt_manager(source):
    # Relocate only host privilege/PATH checks; transaction code stays unchanged.
    cli = source / "cli.go"
    cli.write_text(
        cli.read_text()
        .replace("if os.Geteuid() != 0 {", "if false {")
        .replace(
            'os.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")',
            'os.Setenv("PATH", os.Getenv("OCF_TEST_PATH"))',
        )
    )


def package(directory, manifest, executable):
    (directory / "bin").mkdir(parents=True)
    (directory / "module.json").write_text(json.dumps(manifest))
    shutil.copy2(executable, directory / "bin/module")


def main():
    baseline = subprocess.run(
        ["git", "rev-parse", "--verify", "v0.3.1^{commit}"],
        cwd=PROJECT_ROOT,
        capture_output=True,
        text=True,
    )
    if baseline.returncode:
        print("SKIP: released v0.3.1 source is unavailable in this checkout")
        return

    with tempfile.TemporaryDirectory(prefix="ocf-update-") as temporary:
        root = Path(temporary)
        jffs = root / "jffs"
        platform = jffs / "ocfusion-platform"
        vpn = jffs / "ocfusion-vpn"
        client = jffs / "ocfusion"
        for directory in (platform, vpn, client):
            directory.mkdir(parents=True)

        old_source = root / "baseline"
        old_source.mkdir()
        files = subprocess.check_output(
            [
                "git",
                "ls-tree",
                "-r",
                "--name-only",
                baseline.stdout.strip(),
                "platform",
            ],
            cwd=PROJECT_ROOT,
            text=True,
        ).splitlines()
        for name in files:
            if name.endswith("_test.go") or Path(name).suffix not in {
                ".go",
                ".mod",
                ".js",
            }:
                continue
            data = subprocess.check_output(
                ["git", "show", f"{baseline.stdout.strip()}:{name}"], cwd=PROJECT_ROOT
            )
            (old_source / Path(name).name).write_bytes(data)

        variables = {"base": platform, "stateRoot": jffs, "ram": root / "ram"}
        old_manager = platform / "extd"
        adapt_manager(old_source)
        build(old_source, old_manager, variables)
        new_source = root / "current"
        new_source.mkdir()
        for source in (PROJECT_ROOT / "platform").iterdir():
            if source.suffix in {".go", ".mod", ".js"}:
                shutil.copy2(source, new_source / source.name)
        adapt_manager(new_source)
        manager = root / "new-extd"
        build(new_source, manager, variables)

        provider_source = root / "provider"
        provider_source.mkdir()
        for source in (PROJECT_ROOT / "modules/vpn/daemon").iterdir():
            if source.suffix == ".go" or source.name == "go.mod":
                shutil.copy2(source, provider_source / source.name)
        entrypoint = provider_source / "main.go"
        entrypoint.write_text(
            entrypoint.read_text().replace(
                'os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")',
                'os.Setenv("PATH", os.Getenv("OCF_TEST_PATH"))',
            )
        )
        provider = root / "provider-bin"
        build(provider_source, provider, {"root": vpn, "runDir": root / "vpn-ram"})

        mock = root / "mock"
        mock.mkdir()
        nvram = root / "nvram"
        nvram.mkdir()
        stub = mock / "nvram"
        stub.write_text(
            """#!/bin/sh
set -eu
case "$1" in
    get)
        if [ -f "$OCF_TEST_NVRAM/$2" ]; then
            cat "$OCF_TEST_NVRAM/$2"
        fi
        ;;
    show)
        for file in "$OCF_TEST_NVRAM"/*; do
            printf '%s=' "${file##*/}"
            cat "$file"
            printf '\\n'
        done
        ;;
    set)
        pair=$2
        printf '%s' "${pair#*=}" > "$OCF_TEST_NVRAM/${pair%%=*}"
        ;;
    unset)
        rm -f "$OCF_TEST_NVRAM/$2"
        ;;
    commit)
        [ "${OCF_TEST_COMMIT_FAIL:-0}" = 0 ]
        ;;
esac
"""
        )
        stub.chmod(0o755)
        path = str(mock) + os.pathsep + os.environ["PATH"]
        environment = dict(
            os.environ, PATH=path, OCF_TEST_PATH=path, OCF_TEST_NVRAM=str(nvram)
        )
        native = "OCFusion (managed)>OpenVPN>4>>>0>7>>>0>0>Web"
        custom = "setenv OCFUSION_PROVIDER connection-7\nproto udp"
        for name, value in {
            "vpnc_clientlist": native,
            "vpn_client4_custom": custom,
            "sdn_rl": "unchanged native network assignment",
            "unrelated_password": "fixture-secret",
        }.items():
            (nvram / name).write_text(value)

        profile = client / "profile.json"
        profile.write_text('{"server":"https://vpn.example.org/","password":"fixture"}')
        profile_bytes = profile.read_bytes()
        record = {
            "id": "connection-7",
            "client": "openconnect",
            "profile": "default",
            "interface": "ocvpn0",
            "name": "OCFusion (managed)",
            "schema": 1,
            "unit": 4,
            "vpnc_idx": 7,
            "table": 1007,
            "priority": 107,
            "firmware": "fixture",
        }
        registry = vpn / "connections.json"
        registry.write_text(
            json.dumps({"schema": 1, "connections": {"connection-7": record}})
        )
        registry_bytes = registry.read_bytes()
        helper = root / "module"
        helper.write_text("#!/bin/sh\nprintf '%s\\n' '{}'\n")
        helper.chmod(0o755)
        common = {
            "schema": 1,
            "name": "Fixture",
            "module_api": 1,
            "entrypoint": "bin/module",
        }
        old_packages = root / "old-packages"
        package(
            old_packages / "vpn",
            {**common, "id": "vpn", "version": "0.3.1", "state_paths": [str(vpn)]},
            helper,
        )
        package(
            old_packages / "openconnect",
            {
                **common,
                "id": "openconnect",
                "version": "0.3.1",
                "state_paths": [str(client)],
            },
            helper,
        )
        legacy_backup = jffs / "ocfusion-update-backup"
        subprocess.run(
            [str(old_manager), "backup-state", str(old_packages), str(legacy_backup)],
            env=environment,
            check=True,
        )
        (nvram / "vpnc_clientlist").write_text("changed")
        profile.write_text("changed")
        subprocess.run(
            [str(manager), "restore-state", str(legacy_backup)],
            env=environment,
            check=True,
        )
        assert (nvram / "vpnc_clientlist").read_text() == native
        assert profile.read_bytes() == profile_bytes
        shutil.rmtree(legacy_backup)

        incoming = root / "incoming"
        package(
            incoming / "vpn",
            {
                **common,
                "id": "vpn",
                "version": "0.4",
                "provides_api": 1,
                "install": True,
                "transaction_hooks": True,
                "state_paths": [str(vpn)],
            },
            provider,
        )
        package(
            incoming / "openconnect",
            {
                **common,
                "id": "openconnect",
                "version": "0.4",
                "state_paths": [str(client)],
            },
            helper,
        )
        backup = jffs / "ocfusion-update-backup"
        subprocess.run(
            [str(manager), "backup-state", str(incoming), str(backup)],
            env=environment,
            check=True,
        )
        subprocess.run(
            [str(manager), "prepare-bundled", str(incoming)],
            env=environment,
            check=True,
        )
        assert profile.read_bytes() == profile_bytes
        assert (nvram / "sdn_rl").read_text() == "unchanged native network assignment"
        shutil.rmtree(incoming)
        (nvram / "vpnc_clientlist").write_text("changed")
        (nvram / "vpn_client4_custom").write_text("changed")
        profile.write_text("changed")
        registry.write_text("changed")
        failed = subprocess.run(
            [str(manager), "restore-state", str(backup)],
            env={**environment, "OCF_TEST_COMMIT_FAIL": "1"},
            capture_output=True,
        )
        assert failed.returncode != 0 and (backup / "state.json").exists()
        for _ in range(2):
            subprocess.run(
                [str(manager), "restore-state", str(backup)],
                env=environment,
                check=True,
            )
            assert profile.read_bytes() == profile_bytes
            assert registry.read_bytes() == registry_bytes
            assert (nvram / "vpnc_clientlist").read_text() == native
            assert (nvram / "vpn_client4_custom").read_text() == custom
            assert (nvram / "unrelated_password").read_text() == "fixture-secret"
            assert (
                nvram / "sdn_rl"
            ).read_text() == "unchanged native network assignment"

    print(
        "PASS: v0.3.1 native state, released-backup recovery, hook rollback and retry"
    )


if __name__ == "__main__":
    main()
