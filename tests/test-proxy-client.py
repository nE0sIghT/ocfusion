#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Exercise source-built 3proxy HTTP, CONNECT, ACL and explicit DNS behavior."""

import argparse
import contextlib
import http.client
import http.server
import socket
import socketserver
import struct
import subprocess
import threading
import time
from pathlib import Path


class Origin(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Length", "12")
        self.end_headers()
        self.wfile.write(b"proxy-origin")

    def log_message(self, *_args):
        pass


class DNS(socketserver.BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        self.server.queries += 1
        end = 12
        while data[end] != 0:
            end += data[end] + 1

        question = data[12 : end + 5]
        answer = b"\xc0\x0c" + struct.pack("!HHIH", 1, 1, 1, 4)
        answer += socket.inet_aton("127.0.0.1")
        header = data[:2] + struct.pack("!HHHHH", 0x8180, 1, 1, 0, 0)
        sock.sendto(header + question + answer, self.client_address)


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


@contextlib.contextmanager
def proxy(binary, port, resolver, allow):
    config = (
        f"nserver 127.0.0.1:{resolver}\nnscache 1024\nauth iponly\n"
        f"allow * {allow}\ndeny *\nproxy -4 -i127.0.0.1 -p{port}\n"
    )
    worker = subprocess.Popen(
        [str(binary), "/dev/stdin"],
        stdin=subprocess.PIPE,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    worker.stdin.write(config.encode())
    worker.stdin.close()
    try:
        deadline = time.monotonic() + 5
        while True:
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                    break
            except OSError:
                assert worker.poll() is None, "proxy exited before listening"
                assert time.monotonic() < deadline, "proxy did not start"
                time.sleep(0.02)

        yield
    finally:
        worker.kill()
        worker.wait(timeout=3)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    args = parser.parse_args()
    origin = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Origin)
    dns = socketserver.UDPServer(("127.0.0.1", 0), DNS)
    dns.queries = 0
    threads = [threading.Thread(target=s.serve_forever) for s in (origin, dns)]
    for thread in threads:
        thread.start()

    try:
        port = free_port()
        origin_port = origin.server_address[1]
        with proxy(args.binary, port, dns.server_address[1], "127.0.0.1"):
            connection = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
            connection.request("GET", f"http://proxy-fixture.invalid:{origin_port}/")
            response = connection.getresponse()
            assert response.status == 200
            assert response.read() == b"proxy-origin"
            connection.close()
            assert dns.queries > 0, "explicit proxy DNS was bypassed"

            connection = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
            connection.set_tunnel("127.0.0.1", origin_port)
            connection.request("GET", "/")
            response = connection.getresponse()
            assert response.status == 200
            assert response.read() == b"proxy-origin"
            connection.close()

        with proxy(args.binary, port, dns.server_address[1], "192.0.2.1"):
            connection = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
            connection.request("GET", f"http://127.0.0.1:{origin_port}/")
            response = connection.getresponse()
            assert response.status in (403, 407), response.status
            response.read()
            connection.close()

        print("PASS: upstream HTTP, CONNECT, explicit DNS and client ACL")
    finally:
        for server in (origin, dns):
            server.shutdown()
            server.server_close()
        for thread in threads:
            thread.join(timeout=2)


if __name__ == "__main__":
    main()
