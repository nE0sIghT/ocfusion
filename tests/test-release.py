#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Check release tags and uploads without contacting GitHub."""

import importlib.util
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

PROJECT_ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location(
    "create_release", PROJECT_ROOT / "tools/create-release.py"
)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseTests(unittest.TestCase):
    def test_supported_tags_upload_matching_archives(self):
        for tag in ("v0.3", "v0.3.1", "v0.3-rc.1"):
            with self.subTest(tag=tag), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                artifacts = root / "dist"
                artifacts.mkdir()
                names = (
                    f"ocfusion-{tag[1:]}-aarch64.tar.gz",
                    f"ocfusion-{tag[1:]}-source.tar.gz",
                    "SHA256SUMS",
                )
                for name in names:
                    (artifacts / name).write_bytes(b"fixture")

                environment = {
                    "RELEASE_TAG": tag,
                    "RELEASE_REPOSITORY": "owner/project",
                    "GH_TOKEN": "test-token",
                }
                response = {
                    "upload_url": "https://example.invalid/upload{?name,label}",
                    "html_url": "https://example.invalid/release",
                }
                with (
                    patch.object(release, "PROJECT_ROOT", root),
                    patch.dict(os.environ, environment),
                    patch.object(release, "post", return_value=response) as post,
                    patch("builtins.print"),
                ):
                    release.main()

                metadata = json.loads(post.call_args_list[0].args[1])
                self.assertFalse(metadata["draft"])
                self.assertFalse(metadata["prerelease"])
                self.assertEqual(metadata["tag_name"], tag)
                self.assertEqual(post.call_count, 4)
                uploads = [call.args[0] for call in post.call_args_list[1:]]
                self.assertEqual(
                    uploads,
                    ["https://example.invalid/upload?name=" + name for name in names],
                )

    def test_invalid_tag_never_contacts_github(self):
        for tag in ("tmp", "v0", "v0.3/invalid", "v0.3.1.2"):
            with (
                self.subTest(tag=tag),
                patch.dict(
                    os.environ,
                    {"RELEASE_TAG": tag, "RELEASE_REPOSITORY": "owner/project"},
                ),
                patch.object(release, "post") as post,
                self.assertRaises(AssertionError),
            ):
                release.main()

            post.assert_not_called()


if __name__ == "__main__":
    unittest.main()
