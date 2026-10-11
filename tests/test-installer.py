#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Exercise installation, updates and rollback in a fake router filesystem."""

import os
import shutil
import subprocess
import tempfile
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]
STOCK_PATH = "export PATH=/usr/bin:/bin:/usr/sbin:/sbin"


def write_executable(path, body):
    path.write_text("#!/bin/sh\n" + body)
    path.chmod(0o755)


class RouterFixture:
    def __init__(self, directory):
        self.root = directory
        self.usb = directory / "tmp/mnt/sda"
        self.jffs = directory / "jffs"
        self.mock = directory / "mock"
        self.bundle = self.usb / "ocfusion"
        self.apps = self.usb / "asusware.arm"
        self.manifest = self.jffs / "ocfusion/manifest.json"
        self.profile = self.jffs / "ocfusion/profile.json"
        self.supervisor = self.jffs / "ocfusion/ocfusion"
        self.manager = self.jffs / "ocfusion-platform/extd"

        self.create_filesystem()
        self.create_bundle()
        self.create_mock_commands()
        self.adapt_router_scripts()

    def create_filesystem(self):
        directories = (
            self.usb,
            self.jffs,
            self.mock,
            self.root / "rom",
            self.root / "dev/net",
            self.root / "www/VPN",
            self.root / "tmp/ocfusion",
            self.root / "usr/sbin",
        )
        for directory in directories:
            directory.mkdir(parents=True, exist_ok=True)
        (self.root / "dev/net/tun").symlink_to("/dev/null")
        (self.root / "opt").symlink_to(self.root / "tmp/opt")
        (self.root / "rom/.asusrouter").write_text("#!/bin/sh\n")
        (self.root / "www/VPN/vpnc.html").write_text("original")

    def create_bundle(self):
        shutil.copytree(PROJECT_ROOT / "packaging", self.bundle)
        shutil.copytree(PROJECT_ROOT / "web", self.bundle / "web")
        for module_id in ("vpn", "openconnect"):
            module = self.bundle / "modules" / module_id
            shutil.copytree(PROJECT_ROOT / "modules" / module_id / "packaging", module)
            shutil.copytree(PROJECT_ROOT / "modules" / module_id / "ui", module / "ui")
            (module / "bin").mkdir()
            template = module / "module.json.in"
            (module / "module.json").write_text(template.read_text())
            template.unlink()

        (self.bundle / "bin").mkdir()
        (self.bundle / "modules/openconnect/ca-bundle.crt").write_text("fixture")

    def create_mock_commands(self):
        write_executable(self.mock / "uname", 'echo "${TEST_ARCH:-aarch64}"\n')
        write_executable(
            self.mock / "nvram",
            """case "$2" in
 productid) echo GT-BE98;;
 firmver) echo 3.0.0.6;;
 buildno) echo 102;;
 extendno) echo 39393;;
 apps_local_space) echo /rom;;
 apps_install_folder) echo asusware.arm;;
esac
""",
        )
        write_executable(
            self.mock / "mount", f'echo "/dev/sda on {self.usb} type ext4 (rw)"\n'
        )
        write_executable(
            self.mock / "df",
            f"""echo "Filesystem 1024-blocks Used Available Capacity Mounted on"
echo "fake 100000 1000 99000 1% {self.usb}"
""",
        )
        write_executable(self.mock / "get_apps_name", "echo ocfusion\n")
        write_executable(self.root / "usr/sbin/app_get_field.sh", "echo yes\n")
        for name in ("ip", "iptables", "ip6tables", "cru", "service"):
            write_executable(self.mock / name, "exit 0\n")
        write_executable(
            self.bundle / "modules/openconnect/bin/openconnect",
            'echo "mock OpenConnect"\n',
        )
        self.supervisor_script = f"""case "$1" in
 setup) echo '{{"index":7,"unit":4}}' > {self.manifest};;
 verify-installation|refresh-bundle) test -f {self.manifest};;
 check-install)
  [ "${{TEST_FAIL:-0}}" != 1 ] || exit 1
  echo 'CHECK mock=OK';;
 uninstall) mv {self.manifest} {self.manifest.with_name("manifest.uninstalled.json")};;
 *) exit 0;;
esac
"""
        write_executable(
            self.bundle / "modules/openconnect/bin/ocfusion-openconnect",
            self.supervisor_script,
        )
        write_executable(
            self.bundle / "modules/vpn/bin/ocfusion-vpn", "echo '{\"ok\":true}'\n"
        )
        self.write_core_mock()

    def write_core_mock(self):
        script = (PROJECT_ROOT / "tests/fixtures/installer-core.py").read_text()
        script = script.replace('"FIXTURE_ROOT"', repr(str(self.root)))
        (self.bundle / "bin/extd").write_text(script)
        (self.bundle / "bin/extd").chmod(0o755)

    def adapt_router_scripts(self, source=None):
        installer = (source or PROJECT_ROOT / "packaging/install.sh").read_text()
        installer = installer.replace(
            STOCK_PATH, f"export PATH={self.mock}:/usr/bin:/bin:/usr/sbin:/sbin"
        )
        installer = installer.replace(
            "$(awk '$1 == \"Uid:\" {print $3}' /proc/$$/status)", "0"
        )
        installer = installer.replace(
            "for dir in /usr/sbin /usr/bin /sbin /bin;",
            f"for dir in {self.mock} /usr/bin /bin;",
        )

        # Rewrite fixture paths but retain the real /bin/sh interpreter.
        router_paths = (
            "/usr/sbin/app_get_field.sh",
            "/dev/net/tun",
            "/www/VPN/vpnc.html",
            "/rom/.asusrouter",
            "/tmp/ocfusion-checksums.log",
            "/tmp/ocfusion-install.lock",
            "/tmp/ocfusion/status.json",
            "/tmp/opt",
            "/opt",
            "/jffs",
        )
        for router_path in router_paths:
            installer = installer.replace(router_path, str(self.root) + router_path)
        installer = installer.replace(
            "/tmp/mnt/*)",
            f"{self.root}/tmp/mnt/*)",
        )
        # Replacing /opt also touches the previously rewritten /tmp/opt path.
        installer = installer.replace(
            str(self.root) + "/tmp" + str(self.root) + "/opt",
            str(self.root) + "/tmp/opt",
        )
        (self.bundle / "install.sh").write_text(installer)

        boot_source = (
            source.parent / "S95ocfusion"
            if source
            else PROJECT_ROOT / "packaging/init/S95ocfusion"
        )
        boot_script = boot_source.read_text()
        boot_script = boot_script.replace("/jffs", str(self.jffs))
        boot_script = boot_script.replace(
            STOCK_PATH, f"export PATH={self.mock}:/usr/bin:/bin:/usr/sbin:/sbin"
        )
        boot_target = self.bundle / ("S95ocfusion" if source else "init/S95ocfusion")
        boot_target.write_text(boot_script)
        (self.bundle / "SHA256SUMS").unlink(missing_ok=True)

    def install(self, fail=False, architecture="aarch64", arguments=()):
        environment = dict(
            os.environ, TEST_FAIL="1" if fail else "0", TEST_ARCH=architecture
        )
        return subprocess.run(
            ["/bin/sh", str(self.bundle / "install.sh"), *arguments],
            env=environment,
            text=True,
            capture_output=True,
        )


def assert_success(result):
    assert result.returncode == 0, (result.stdout, result.stderr)


def test_fresh_and_repeated_install(router):
    second = router.bundle / "modules/example"
    second.mkdir()
    (second / "module.json").write_text('{"id":"example"}\n')

    assert_success(router.install())
    registered = (router.root / "registered-modules").read_text().splitlines()
    expected = router.root / "tmp/opt/ocfusion/modules"
    assert str(expected / "openconnect") in registered
    assert str(expected / "example") in registered
    assert (router.apps / "ocfusion/modules/example/module.json").exists()
    assert not (router.apps / "ocfusion/module.json").exists()
    original_manifest = router.manifest.read_bytes()
    router.profile.write_text("secret-config")

    assert_success(router.install())
    assert router.manifest.read_bytes() == original_manifest
    assert router.profile.read_text() == "secret-config"
    assert (
        router.apps / "ocfusion/modules/openconnect/ui/index.html"
    ).read_bytes() == (router.bundle / "modules/openconnect/ui/index.html").read_bytes()


def test_failed_update_and_retry(router):
    original_supervisor = router.supervisor.read_bytes()
    original_manager = router.manager.read_bytes()
    original_manifest = router.manifest.read_bytes()
    router.write_core_mock()
    write_executable(
        router.bundle / "modules/openconnect/bin/ocfusion-openconnect",
        router.supervisor_script + "# updated payload\n",
    )

    failed = router.install(fail=True)
    assert failed.returncode != 0
    assert router.supervisor.read_bytes() == original_supervisor
    assert router.manager.read_bytes() == original_manager
    assert router.manifest.read_bytes() == original_manifest

    assert_success(router.install())
    assert b"updated payload" in router.supervisor.read_bytes()
    assert router.profile.read_text() == "secret-config"


def test_interrupted_update_recovery(router):
    backup = router.jffs / "ocfusion-update-backup"
    subprocess.run(
        [
            str(router.bundle / "bin/extd"),
            "backup-state",
            str(router.bundle / "modules"),
            str(backup),
        ],
        check=True,
    )
    (router.apps / "ocfusion").rename(router.apps / "ocfusion.previous")
    (router.apps / "ocfusion").mkdir()
    assert_success(router.install())
    assert not backup.exists()


def main():
    with tempfile.TemporaryDirectory() as temporary:
        router = RouterFixture(Path(temporary))
        wrong_architecture = router.install(architecture="x86_64")
        assert wrong_architecture.returncode != 0
        assert "requires AArch64" in wrong_architecture.stderr
        assert not router.manifest.exists()
        marker = router.root / "rom/.asusrouter"
        marker.unlink()
        non_asuswrt = router.install()
        assert non_asuswrt.returncode != 0
        assert "ASUSWRT required" in non_asuswrt.stderr
        assert not router.apps.exists()
        assert not router.manifest.exists()
        marker.write_text("#!/bin/sh\n")
        test_fresh_and_repeated_install(router)
        test_failed_update_and_retry(router)
        test_interrupted_update_recovery(router)
        unrelated = router.apps / "other-app"
        unrelated.mkdir()
        assert_success(router.install(arguments=("--uninstall",)))
        assert router.profile.read_text() == "secret-config"
        assert not router.supervisor.exists()
        assert not router.manager.exists()
        assert not (router.apps / "ocfusion").exists()
        assert unrelated.exists()
        assert_success(router.install(arguments=("--uninstall", "--prune")))
        assert not router.profile.exists()
        assert unrelated.exists()
    print(
        "PASS: fresh install, repeated install, preserved config/index/snapshot, "
        "failed-update rollback, successful retry"
    )


if __name__ == "__main__":
    main()
