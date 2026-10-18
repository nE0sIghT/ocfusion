#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Package one built-in module without building the platform distribution."""

import argparse
import importlib.util
import json
import os
import shutil
import tempfile
from pathlib import Path

from package import write_archive
from stage import (
    PROJECT_ROOT,
    combine_notices,
    stage_module,
)

spec = importlib.util.spec_from_file_location(
    "verify_runtime", PROJECT_ROOT / "tools/verify-runtime.py"
)
verification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verification)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--spec", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, required=True)
    arguments = parser.parse_args()

    with tempfile.TemporaryDirectory(prefix="ocfusion-module-") as directory:
        specification = json.loads(arguments.spec.read_text())
        package = Path(directory) / specification["id"]
        stage_module(package, specification, arguments.version)
        shutil.copy(PROJECT_ROOT / "LICENSE", package / "LICENSE")
        combine_notices(package, [specification])
        for executable in specification["executables"]:
            verification.verify_executable(package / executable)
        entries = [
            (str(path.relative_to(package)), path) for path in package.rglob("*")
        ]
        write_archive(
            arguments.output, entries, int(os.environ.get("SOURCE_DATE_EPOCH", "0"))
        )


if __name__ == "__main__":
    main()
