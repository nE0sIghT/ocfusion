#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Check tag-based versioning and explicit source-archive build overrides."""

import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

PROJECT_ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location(
    "build_version", PROJECT_ROOT / "tools/version.py"
)
version = importlib.util.module_from_spec(spec)
spec.loader.exec_module(version)


class VersionTests(unittest.TestCase):
    def test_explicit_override_does_not_require_git(self):
        with patch.object(
            version, "git_output", side_effect=AssertionError("Unexpected Git call")
        ):
            self.assertEqual(version.resolve_version("0.2.9"), "0.2.9")

    def test_invalid_override_is_rejected(self):
        with self.assertRaises(SystemExit):
            version.resolve_version("../../invalid")

    def test_exact_release_tag(self):
        with patch.object(version, "git_output", return_value="v0.2.9"):
            self.assertEqual(version.resolve_version(), "0.2.9")

    def test_untagged_commit_has_development_suffix(self):
        with patch.object(version, "git_output", side_effect=["", "v0.2.9-3-gabcdef0"]):
            self.assertEqual(version.resolve_version(), "0.2.9-dev.3.gabcdef0")

    def test_source_archive_has_safe_fallback(self):
        with (
            tempfile.TemporaryDirectory() as directory,
            patch.object(version, "PROJECT_ROOT", Path(directory)),
        ):
            self.assertEqual(version.resolve_version(), "0.0.0-dev")


if __name__ == "__main__":
    unittest.main()
