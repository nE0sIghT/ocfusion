#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Exercise the patched client's structured OTP pipe natively."""

import argparse
import os
import subprocess
import tempfile
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    arguments = parser.parse_args()
    text = arguments.source.read_text()
    start = text.index("static char *ocf_manual_otp(void)")
    end = text.index("\n#endif", start)
    function = text[start:end]
    harness = (
        """
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <poll.h>
#include <fcntl.h>
#include <stdio.h>
"""
        + function
        + """
int main(int argc, char **argv)
{
    int output = fcntl(atoi(argv[1]), F_DUPFD, 10);
    int input = fcntl(atoi(argv[2]), F_DUPFD, 10);
    dup2(output, 3);
    dup2(input, 4);

    char *code = ocf_manual_otp();
    if (!code) {
        return 1;
    }

    puts(code);
    free(code);
    return 0;
}
"""
    )
    with tempfile.TemporaryDirectory() as directory:
        source = Path(directory) / "otp.c"
        binary = Path(directory) / "otp"
        source.write_text(harness)
        subprocess.run(["cc", str(source), "-o", str(binary)], check=True)
        for code in ("123456", "12345678", "", "abc123", "123456789"):
            notice_read, notice_write = os.pipe()
            reply_read, reply_write = os.pipe()
            try:
                process = subprocess.Popen(
                    [str(binary), str(notice_write), str(reply_read)],
                    pass_fds=(notice_write, reply_read),
                    stdout=subprocess.PIPE,
                )
                assert os.read(notice_read, 4) == b"otp\n"
                os.write(reply_write, (code + "\n").encode())
                output, _ = process.communicate(timeout=5)
                valid = code in ("123456", "12345678")
                assert (process.returncode == 0) == valid
                assert output == ((code + "\n").encode() if valid else b"")
            finally:
                for descriptor in (notice_read, notice_write, reply_read, reply_write):
                    os.close(descriptor)
    print(
        "PASS: structured OTP accepts bounded codes "
        "and rejects cancellation/invalid input"
    )


if __name__ == "__main__":
    main()
