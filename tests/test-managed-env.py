#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Run the exact patched hook-env function with native C; no QEMU is needed."""

import argparse
import subprocess
import tempfile
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]

C_FIXTURE = r"""
#include <stdlib.h>
#include <string.h>
#include <assert.h>
#include <errno.h>
#include <unistd.h>
#include <sys/wait.h>

struct oc_vpn_option {
    char *option;
    char *value;
    struct oc_vpn_option *next;
};

FUNCTION

int main(void)
{
    char *huge = malloc(500001);
    memset(huge, 'x', 500000);
    huge[500000] = 0;

    struct oc_vpn_option dns = {"INTERNAL_IP4_DNS", "192.0.2.53", NULL};
    struct oc_vpn_option tun = {"TUNDEV", "ocvpn0", &dns};
    struct oc_vpn_option routes = {"CISCO_CSTP_OPTIONS", huge, &tun};

    /* Default mode keeps the oversized options and exec fails with E2BIG. */
    unsetenv("OCF_MANAGED_ROUTES");
    apply_script_env(&routes);
    assert(strlen(getenv("CISCO_CSTP_OPTIONS")) == 500000);

    pid_t child = fork();
    assert(child >= 0);
    if (!child) {
        execl("/bin/true", "true", NULL);
        _exit(errno == E2BIG ? 0 : 1);
    }
    int status;
    assert(waitpid(child, &status, 0) == child);
    assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);

    /* Managed mode drops bulky options but retains the hook's required fields. */
    setenv("OCF_MANAGED_ROUTES", "1", 1);
    setenv("OCF_GENERATION", "test-session", 1);
    apply_script_env(&routes);
    assert(!getenv("CISCO_CSTP_OPTIONS"));
    assert(!strcmp(getenv("TUNDEV"), "ocvpn0"));
    assert(!strcmp(getenv("INTERNAL_IP4_DNS"), "192.0.2.53"));
    assert(!strcmp(getenv("OCF_GENERATION"), "test-session"));

    child = fork();
    assert(child >= 0);
    if (!child) {
        execl("/bin/true", "true", NULL);
        _exit(1);
    }
    assert(waitpid(child, &status, 0) == child);
    assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);
    free(huge);
    return 0;
}
"""


def extract_hook_function(path):
    source = path.read_text()
    start = source.index("int apply_script_env(struct oc_vpn_option *envs)")
    end = source.index("\nint script_config_tun(", start)
    return source[start:end]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    arguments = parser.parse_args()
    fixture = C_FIXTURE.replace("FUNCTION", extract_hook_function(arguments.source))
    with tempfile.TemporaryDirectory() as temporary:
        directory = Path(temporary)
        source = directory / "test.c"
        executable = directory / "test"
        source.write_text(fixture)
        subprocess.run(
            ["cc", "-O2", "-Wall", "-Wextra", str(source), "-o", str(executable)],
            check=True,
        )
        subprocess.run([str(executable)], check=True)
    print(
        "PASS native C: default hook environment retained; "
        "oversized options cause E2BIG; "
        "managed env drops options, retains required fields and permits exec"
    )


if __name__ == "__main__":
    main()
