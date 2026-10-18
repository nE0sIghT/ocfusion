#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Check generic module staging, selected dependencies and archive contents."""

import hashlib
import importlib.util
import json
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

PROJECT_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(PROJECT_ROOT / "tools"))


def load_tool(name, filename):
    spec = importlib.util.spec_from_file_location(
        name, PROJECT_ROOT / "tools" / filename
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


stage = load_tool("stage", "stage.py")
module_spec = load_tool("module_spec", "module-spec.py")
package_tool = load_tool("package_tool", "package.py")


def specification(root, module_id):
    source = root / (module_id + "-source")
    source.mkdir()
    (source / "module.json.in").write_text(
        json.dumps(
            {
                "id": module_id,
                "version": "@VERSION@",
                "entrypoint": "bin/worker",
                "ui": "ui/index.html",
            }
        )
    )
    (source / "worker").write_text("fixture")
    (source / "worker").chmod(0o755)
    (source / "index.html").write_text("fixture UI")
    return {
        "id": module_id,
        "files": [
            [str(source / name), destination]
            for name, destination in (
                ("module.json.in", "module.json.in"),
                ("worker", "bin/worker"),
                ("index.html", "ui/index.html"),
            )
        ],
        "verified_files": [],
        "executables": ["bin/worker"],
        "notices": [],
    }


class PackagingTests(unittest.TestCase):
    def test_selected_staging_removes_previous_optional_module(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manager = root / "manager"
            manager.write_bytes(b"fixture")
            arguments = [
                "stage.py",
                "--build-dir",
                str(root),
                "--manager",
                str(manager),
                "--version",
                "0.4.1",
                "--output",
                str(root / "stamp"),
            ]
            specs = []
            for module_id in ("vpn", "openconnect", "adguard-home", "radio-settings"):
                path = root / (module_id + ".json")
                path.write_text(json.dumps(specification(root, module_id)))
                specs += ["--spec", str(path)]
            with patch.object(sys, "argv", arguments + specs):
                stage.main()
            runtime = root / "runtime/ocfusion"
            self.assertTrue((runtime / "modules/radio-settings/module.json").is_file())
            with patch.object(sys, "argv", arguments + specs[:-2]):
                stage.main()
            self.assertEqual(
                {path.name for path in (runtime / "modules").iterdir()},
                {"vpn", "openconnect", "adguard-home"},
            )
            with patch.object(sys, "argv", arguments):
                stage.main()
            self.assertEqual(list((runtime / "modules").iterdir()), [])

    def test_unknown_module_uses_same_packaging_contract(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            description = specification(root, "independent-example")
            package = root / "package"
            stage.stage_module(package, description, "0.4.1")
            stage.combine_notices(package, [description])
            manifest = json.loads((package / "module.json").read_text())
            self.assertEqual(manifest["id"], "independent-example")
            self.assertEqual(manifest["version"], "0.4.1")
            self.assertTrue((package / manifest["entrypoint"]).stat().st_mode & 0o111)
            self.assertTrue((package / manifest["ui"]).is_file())
            self.assertFalse((package / "modules").exists())
            self.assertFalse((package / "module.json.in").exists())

    def test_verified_data_rejects_changed_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root / "data.pem"
            data.write_bytes(b"expected")
            lock = root / "third_party/sources.lock.json"
            lock.parent.mkdir()
            lock.write_text(
                json.dumps(
                    {
                        "sources": [
                            {
                                "name": data.name,
                                "sha256": hashlib.sha256(data.read_bytes()).hexdigest(),
                            }
                        ]
                    }
                )
            )
            description = specification(root, "example")
            description["verified_files"] = [str(data)]
            description["files"].append([str(data), "ca-bundle.crt"])
            with patch.object(stage, "PROJECT_ROOT", root):
                stage.stage_module(root / "good", description, "0.4.1")
                data.write_bytes(b"changed")
                with self.assertRaises(SystemExit):
                    stage.stage_module(root / "bad", description, "0.4.1")
            self.assertFalse((root / "bad").exists())

    def test_selection_checks_manifest_dependencies_and_api(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            provider = specification(root, "provider")
            consumer = specification(root, "consumer")
            consumer_manifest = Path(consumer["files"][0][0])
            manifest = json.loads(consumer_manifest.read_text())
            manifest["dependencies"] = [{"id": "provider", "api": 2}]
            consumer_manifest.write_text(json.dumps(manifest))
            with self.assertRaisesRegex(ValueError, "requires provider API 2"):
                module_spec.check_selection([consumer])
            with self.assertRaises(ValueError):
                module_spec.check_selection([provider, consumer])
            provider_manifest = Path(provider["files"][0][0])
            manifest = json.loads(provider_manifest.read_text())
            manifest["provides_api"] = 2
            provider_manifest.write_text(json.dumps(manifest))
            module_spec.check_selection([provider, consumer])
            module_spec.check_selection([])

    def test_platform_only_archive_preserves_empty_modules_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            runtime = root / "runtime"
            (runtime / "modules").mkdir(parents=True)
            output = root / "runtime.tar.gz"
            package_tool.write_archive(
                output, package_tool.runtime_entries(runtime), 123
            )
            with tarfile.open(output) as archive:
                directory = archive.getmember("ocfusion/modules")
                self.assertTrue(directory.isdir())
                self.assertEqual(directory.mtime, 123)
                self.assertEqual(directory.uid, 0)


if __name__ == "__main__":
    unittest.main()
