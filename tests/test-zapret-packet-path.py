#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Exercise patched engine packet paths without root, NFQUEUE or live traffic."""

import argparse
import resource
import signal
import subprocess
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--prefix", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--check-regression", action="store_true")
    args = parser.parse_args()
    source = args.source.resolve() / "nfq2"
    prefix = args.prefix.resolve()
    sources = sorted(path for path in source.glob("*.c") if path.name != "nfqws.c")
    sources += sorted((source / "crypto").glob("*.c"))
    subprocess.run(
        [
            "gcc",
            "-O1",
            "-std=gnu99",
            "-ffunction-sections",
            "-fdata-sections",
            "-I" + str(source),
            "-I" + str(prefix / "include"),
            str(Path(__file__).with_name("zapret-packet-path.c")),
            *map(str, sources),
            "-L" + str(prefix / "lib"),
            "-Wl,--gc-sections,--wrap=rawsend,--wrap=rawsend_rp",
            "-llua",
            "-lnetfilter_queue",
            "-lnfnetlink",
            "-lmnl",
            "-lz",
            "-lm",
            "-ldl",
            "-o",
            str(args.output.resolve()),
        ],
        check=True,
    )
    subprocess.run([str(args.output.resolve())], check=True)
    if args.check_regression:
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
        baseline = subprocess.run(
            [str(args.output.resolve()), "--legacy-quic"],
            capture_output=True,
            text=True,
            check=False,
        )
        assert baseline.returncode == -signal.SIGABRT, baseline.stderr
        assert "feed_udp(ipv6, wire) == VERDICT_PASS" in baseline.stderr
        print("PASS: legacy QUIC buffering fails the same original-packet assertion")


if __name__ == "__main__":
    main()
