#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Exercise host preparation and the ASUS Apps installation bootstrap."""

import hashlib
import shutil
import subprocess
import tempfile
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]


class USBFixture:
    def __init__(self, directory):
        self.root = directory
        self.bundle = directory / "bundle"
        self.usb = directory / "usb"
        self.apps = self.usb / "asusware.arm"
        self.runner = directory / "runner"
        self.usb.mkdir()
        shutil.copytree(PROJECT_ROOT / "packaging", self.bundle)

        # Only filesystem type is simulated. Production requires actual ext4.
        preparation = self.bundle / "scripts/prepare-usb.sh"
        script = preparation.read_text().replace(
            "set -eu",
            "set -eu\nstat() { echo ext2/ext3; }",
            1,
        )
        preparation.write_text(script)
        checksums = []
        for path in sorted(self.bundle.rglob("*")):
            if path.is_file():
                digest = hashlib.sha256(path.read_bytes()).hexdigest()
                checksums.append(f"{digest}  {path.relative_to(self.bundle)}\n")
        (self.bundle / "SHA256SUMS").write_text("".join(checksums))

    def prepare(self, expect_success=True):
        result = subprocess.run(
            [
                "/bin/sh",
                str(self.bundle / "install.sh"),
                "--prepare-usb",
                str(self.usb),
            ],
            capture_output=True,
            text=True,
        )
        assert (result.returncode == 0) == expect_success, result.stdout + result.stderr

    def bootstrap(self):
        return subprocess.run(["/bin/sh", str(self.runner), "start"]).returncode

    def create_bootstrap_runner(self):
        # Model /tmp/opt while /opt remains a separate stock directory.
        bootstrap = (PROJECT_ROOT / "packaging/init/S94ocfusionprep").read_text()
        bootstrap = bootstrap.replace("/tmp/opt/", str(self.apps) + "/")
        bootstrap = bootstrap.replace(
            "/tmp/ocfusion-install.log", str(self.root / "install.log")
        )
        self.runner.write_text(bootstrap)


def test_preparation_and_bootstrap(usb):
    usb.prepare()
    usb.prepare()
    control = usb.apps / "lib/ipkg/info/ocfusionprep.control"
    assert control.read_text().endswith("Enabled: yes\n")

    usb.create_bootstrap_runner()
    installer = usb.apps / "ocfusion-installer/install.sh"
    entry = usb.apps / "etc/init.d/S94ocfusionprep"
    installer.write_text("exit 1\n")
    assert usb.bootstrap() == 1
    assert entry.exists()

    installer.write_text("echo INSTALL_OK\n")
    assert usb.bootstrap() == 0
    assert not entry.exists()
    usb.prepare()


def test_migration_from_ssh_installation(usb):
    marker = usb.apps / ".asusrouter"
    marker.write_text("stock marker fixture\n")
    (usb.apps / "lib/ipkg/info/ocfusion.control").write_text(
        "Package: ocfusion\nEnabled: yes\n"
    )
    (usb.apps / "etc/init.d/S95ocfusion").write_text("# ASUS app entrypoint. fixture\n")
    binary_directory = usb.apps / "ocfusion/bin"
    binary_directory.mkdir(parents=True)
    (binary_directory / "extd").write_text("fixture")

    usb.prepare()
    assert marker.read_text() == "stock marker fixture\n"


def test_symlink_destination_refused(usb):
    (usb.apps / "etc/init.d/S95ocfusion").unlink()
    (usb.apps / "etc/init.d/S94ocfusionprep").unlink()
    init_directory = usb.apps / "etc/init.d"
    init_directory.rmdir()
    init_directory.symlink_to(usb.root)
    usb.prepare(expect_success=False)


def main():
    with tempfile.TemporaryDirectory() as temporary:
        usb = USBFixture(Path(temporary))
        test_preparation_and_bootstrap(usb)
        test_migration_from_ssh_installation(usb)
        test_symlink_destination_refused(usb)
    print(
        "PASS: host USB preparation, repeat/update scheduling, "
        "bootstrap retry/success, symlink refusal"
    )


if __name__ == "__main__":
    main()
