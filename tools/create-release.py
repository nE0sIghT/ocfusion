#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Create a draft GitHub release and upload checked, source-built artifacts."""

import json
import os
import re
from pathlib import Path
from urllib.parse import quote
from urllib.request import Request, urlopen

PROJECT_ROOT = Path(__file__).resolve().parents[1]


def post(url, data, headers, content_type="application/json"):
    request = Request(
        url,
        data=data,
        headers={**headers, "Content-Type": content_type},
        method="POST",
    )
    with urlopen(request, timeout=120) as response:
        return json.load(response)


def main():
    tag = os.environ["RELEASE_TAG"]
    repository = os.environ["RELEASE_REPOSITORY"]
    assert re.fullmatch(r"v[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[A-Za-z0-9.-]+)?", tag), (
        "invalid release tag"
    )
    version = tag[1:]
    artifact_names = [
        f"ocfusion-{version}-aarch64.tar.gz",
        f"ocfusion-{version}-source.tar.gz",
        "SHA256SUMS",
    ]
    for name in artifact_names:
        assert (PROJECT_ROOT / "dist" / name).is_file(), "missing tagged artifact"

    headers = {
        "Authorization": "Bearer " + os.environ["GH_TOKEN"],
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    metadata = {
        "tag_name": tag,
        "name": "OCFusion " + version,
        "draft": True,
        "prerelease": True,
        "body": (
            f"See the [README](https://github.com/{repository}/tree/{tag}#quick-start) "
            "for installation and module documentation."
        ),
    }
    release = post(
        f"https://api.github.com/repos/{repository}/releases",
        json.dumps(metadata).encode(),
        headers,
    )
    upload_url = release["upload_url"].split("{")[0]
    for name in artifact_names:
        artifact = PROJECT_ROOT / "dist" / name
        post(
            upload_url + "?name=" + quote(artifact.name),
            artifact.read_bytes(),
            headers,
            "application/octet-stream",
        )
    print("Draft release:", release["html_url"])


if __name__ == "__main__":
    main()
