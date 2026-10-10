"""Local HTTP fixture for container configuration failure/cancellation tests."""

import http.server
import pathlib
import sys
import threading


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/blocked":
            (fixture / "request-ready").write_text("ready", encoding="utf-8")
            # No response: the application must leave via SIGTERM/context,
            # before its normal 30-second configuration download deadline.
            threading.Event().wait(60)
            return
        self.send_response(503)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def log_message(self, *_):
        pass


if __name__ == "__main__":
    fixture = pathlib.Path(sys.argv[1])
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    (fixture / "port").write_text(str(server.server_port), encoding="utf-8")
    server.serve_forever()
