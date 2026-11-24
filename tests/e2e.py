"""Black-box tests for the built Pipkin executable on a Unix host."""

import errno
import fcntl
import http.client
import io
import json
import os
from pathlib import Path
import pty
import re
import select
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time
import unittest
from email import policy
from email.parser import BytesParser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlencode, urlsplit
from zipfile import ZipFile


TIMEOUT = 10
PROMPT = "Update the selected files"
ORIGINAL = {"alpha.txt": "Old alpha\n", "beta.txt": "Old beta\n"}
PROPOSED = {"alpha.txt": "New alpha\n", "beta.txt": "New beta\n"}
PIPKIN = Path(sys.argv.pop(1)).resolve()


def stop_process(process):
    if process.poll() is None:
        process.kill()
        process.wait(timeout=TIMEOUT)


def wait_readable(fd, deadline):
    if not select.select([fd], [], [], max(0, deadline - time.monotonic()))[0]:
        raise TimeoutError("Timed out waiting for Pipkin output")


class Terminal:
    def __init__(self, start):
        self.master, slave = pty.openpty()
        self.pending = b""
        try:
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 34, 120, 0, 0))
            self.process = start(stdin=slave, stdout=slave, stderr=slave)
        except Exception:
            os.close(self.master)
            raise
        finally:
            os.close(slave)

    def close(self):
        stop_process(self.process)
        os.close(self.master)

    def expect(self, text):
        marker = text.encode()
        deadline = time.monotonic() + TIMEOUT
        while marker not in self.pending:
            try:
                wait_readable(self.master, deadline)
                chunk = os.read(self.master, 65536)
            except (OSError, TimeoutError) as error:
                if isinstance(error, OSError) and error.errno not in (None, errno.EIO):
                    raise
                raise AssertionError(
                    f"Waiting for {text!r}. Terminal output: {self.pending[-6000:]!r}"
                ) from error
            if not chunk:
                raise AssertionError(f"Terminal closed before {text!r}: {self.pending!r}")
            self.pending += chunk
        _, _, self.pending = self.pending.partition(marker)

    def send(self, keys):
        self.pending = b""
        os.write(self.master, keys.encode())


class PipkinEndToEnd(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        directory = Path(temporary.name)
        self.project = directory / "project"
        self.project.mkdir()
        self.output = directory / "deliveries"
        for name, contents in ORIGINAL.items():
            (self.project / name).write_text(contents)
        (self.project / "unselected.txt").write_text("Leave this alone\n")
        env_file = directory / "test.env"
        env_file.touch()
        self.env = {
            "PATH": os.environ["PATH"],
            "HOME": str(directory),
            "PIPKIN_CONFIG": str(directory / "config.json"),
            "PIPKIN_ENV_FILE": str(env_file),
            "PIPKIN_OUTPUT_DIR": str(self.output),
            "PIPKIN_TEST_KEY": "dummy-key",
            "TERM": "xterm",
            "LANGFUSE_BASE_URL": "https://langfuse.example.test",
        }
        self.start_provider()
        Path(self.env["PIPKIN_CONFIG"]).write_text(json.dumps({
            "provider": "multipart",
            "endpoint": f"http://127.0.0.1:{self.provider.server_port}/deliver",
            "api_key_env": "PIPKIN_TEST_KEY",
            "model": "test-model",
        }))

    def start_provider(self, proposed=PROPOSED):
        fixture = io.BytesIO()
        with ZipFile(fixture, "w") as archive:
            for name, contents in proposed.items():
                archive.writestr(name, contents)
        result = fixture.getvalue()

        class DummyProvider(BaseHTTPRequestHandler):
            def do_POST(self):
                try:
                    if self.path != "/deliver":
                        raise ValueError("Wrong provider endpoint")
                    if self.headers.get("Authorization") != "Bearer dummy-key":
                        raise ValueError("Missing dummy API key")
                    body = self.rfile.read(int(self.headers["Content-Length"]))
                    mime_header = f"Content-Type: {self.headers['Content-Type']}\r\n\r\n"
                    message = BytesParser(policy=policy.default).parsebytes(
                        mime_header.encode() + body
                    )
                    fields = {
                        part.get_param("name", header="content-disposition"):
                        part.get_payload(decode=True)
                        for part in message.iter_parts()
                    }
                    if fields["prompt"] != PROMPT.encode() or fields["model"] != b"test-model":
                        raise ValueError("Wrong prompt or model")
                    with ZipFile(io.BytesIO(fields["file"])) as archive:
                        contents = {name: archive.read(name) for name in archive.namelist()}
                    if contents != {name: value.encode() for name, value in ORIGINAL.items()}:
                        raise ValueError(f"Wrong ZIP contents: {list(contents)}")
                except (KeyError, OSError, ValueError) as error:
                    self.send_error(400, str(error))
                    return
                self.send_response(200)
                self.send_header("Content-Type", "application/zip")
                self.send_header("Content-Length", str(len(result)))
                self.end_headers()
                self.wfile.write(result)

            def log_message(self, format, *args):
                pass

        self.provider = ThreadingHTTPServer(("127.0.0.1", 0), DummyProvider)
        thread = threading.Thread(target=self.provider.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.provider.server_close)
        self.addCleanup(thread.join, TIMEOUT)
        self.addCleanup(self.provider.shutdown)

    def start(self, *args, **streams):
        process = subprocess.Popen(
            [str(PIPKIN), *args], cwd=self.project, env=self.env, **streams
        )
        for pipe in (process.stdin, process.stdout, process.stderr):
            if pipe is not None:
                self.addCleanup(pipe.close)
        self.addCleanup(stop_process, process)
        return process

    def assert_workspace(self, alpha):
        self.assertEqual((self.project / "alpha.txt").read_text(), alpha)
        self.assertEqual((self.project / "beta.txt").read_text(), ORIGINAL["beta.txt"])
        self.assertEqual((self.project / "unselected.txt").read_text(), "Leave this alone\n")

    def test_terminal_select_submit_review_and_copy(self):
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("Parcel the otter")
        terminal.expect("\x1b]8;;https://langfuse.example.test\x1b\\ Langfuse: https://langfuse.example.test")
        # Drop the initial selection and select only the first two files.
        terminal.send("n j ")
        terminal.expect("2 files selected")
        terminal.send("p")
        terminal.expect("Prompt (Enter send, Esc cancel)")
        terminal.send(PROMPT + "\r")
        terminal.expect("Delivery saved")
        self.assert_workspace(ORIGINAL["alpha.txt"])
        terminal.send("r")
        terminal.expect("1 completed deliveries")
        terminal.send("\r")
        terminal.expect("Prompt: " + PROMPT)
        terminal.expect("Old alpha")
        terminal.expect("New alpha")
        terminal.send("oc")
        terminal.expect("Review the remaining 1 pending files")
        terminal.send("jxc")
        terminal.expect("Press Y to confirm or N to cancel")
        terminal.send("N")
        terminal.expect("Copy cancelled.")
        self.assert_workspace(ORIGINAL["alpha.txt"])
        terminal.send("cY")
        terminal.expect("Copied 1 files")
        terminal.send("bqq")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)
        self.assert_workspace(PROPOSED["alpha.txt"])

    def test_terminal_advertises_agentic_shortcut(self):
        config = Path(self.env["PIPKIN_CONFIG"])
        config.write_text(json.dumps({"provider": "vllm", "model": "local-model"}))
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("p LLM  P Agentic")
        terminal.send("P")
        terminal.expect("Agentic prompt (Enter send, Esc cancel)")
        terminal.send("\x07q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)

    def test_terminal_reports_unchanged_and_missing_trace_url(self):
        self.start_provider(ORIGINAL)
        config = Path(self.env["PIPKIN_CONFIG"])
        settings = json.loads(config.read_text())
        settings["endpoint"] = f"http://127.0.0.1:{self.provider.server_port}/deliver"
        config.write_text(json.dumps(settings))
        self.env.pop("LANGFUSE_BASE_URL")
        self.env["LANGFUSE_PUBLIC_KEY"] = "pk-test"
        self.env["LANGFUSE_SECRET_KEY"] = "sk-test"
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("Parcel the otter")
        terminal.send("n j p" + PROMPT + "\r")
        terminal.expect("Provider returned identical files")
        terminal.expect("Langfuse export failed: set LANGFUSE_BASE_URL or langfuse_url")
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)
        self.assert_workspace(ORIGINAL["alpha.txt"])

    def test_http_submit_review_and_copy(self):
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
        self.env["LISTEN_URL"] = f"http://127.0.0.1:{port}"
        log = tempfile.TemporaryFile()
        self.addCleanup(log.close)
        process = self.start("--web", stdout=log, stderr=log)

        def request(path, form=None):
            connection = http.client.HTTPConnection("127.0.0.1", port, timeout=2)
            try:
                if form is None:
                    connection.request("GET", path)
                else:
                    connection.request("POST", path, urlencode(form, doseq=True), {
                        "Content-Type": "application/x-www-form-urlencoded",
                    })
                response = connection.getresponse()
                return response.status, response.read().decode()
            finally:
                connection.close()

        deadline = time.monotonic() + TIMEOUT
        while True:
            try:
                status, landing = request("/")
                break
            except ConnectionRefusedError:
                if process.poll() is not None or time.monotonic() >= deadline:
                    log.seek(0)
                    self.fail(f"Web server failed to start: {log.read()!r}")
                time.sleep(0.05)
        self.assertEqual(status, 200)
        self.assertIn('value="alpha.txt"', landing)
        self.assertIn('href="https://langfuse.example.test"', landing)
        self.assertIn(f'href="http://127.0.0.1:{port}"', landing)
        token = re.search(r'name="csrf" value="([^"]+)"', landing)
        self.assertIsNotNone(token)
        form = {"csrf": token[1], "files": list(ORIGINAL), "prompt": PROMPT}
        self.assertEqual(request("/submit", dict(form, csrf="bad"))[0], 403)
        self.assertEqual(request("/submit", form)[0], 303)

        deadline = time.monotonic() + TIMEOUT
        while True:
            _, landing = request("/")
            link = re.search(r'href="(/review\?job=[^"]+)"', landing)
            if link:
                review_url = link[1]
                status, page = request(review_url)
                if "2 pending" in page:
                    break
            if time.monotonic() >= deadline:
                log.seek(0)
                self.fail(f"No completed web delivery: {log.read()!r}")
            time.sleep(0.05)

        self.assertEqual(status, 200)
        self.assertIn(PROMPT, page)
        self.assertIn("Old alpha", page)
        self.assertIn("New alpha", page)
        job = parse_qs(urlsplit(review_url).query)["job"][0]
        review = {"csrf": token[1], "job": job}
        self.assertEqual(request("/copy", dict(review, confirm="COPY"))[0], 409)
        self.assertEqual(request("/decision", dict(review, action="approve", path="alpha.txt"))[0], 303)
        self.assertEqual(request("/decision", dict(review, action="reject", path="beta.txt"))[0], 303)
        self.assertEqual(request("/copy", review)[0], 400)
        self.assert_workspace(ORIGINAL["alpha.txt"])
        self.assertEqual(request("/copy", dict(review, confirm="COPY"))[0], 303)
        self.assert_workspace(PROPOSED["alpha.txt"])

    def test_mcp_dummy_client_submit_and_review(self):
        log = tempfile.TemporaryFile()
        self.addCleanup(log.close)
        process = self.start("--mcp", stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=log)
        request_id = 0
        output = b""

        def send(method, params, notification=False):
            nonlocal output, request_id
            message = {"jsonrpc": "2.0", "method": method, "params": params}
            if not notification:
                request_id += 1
                message["id"] = request_id
            process.stdin.write(json.dumps(message).encode() + b"\n")
            process.stdin.flush()
            if notification:
                return None
            deadline = time.monotonic() + TIMEOUT
            while b"\n" not in output:
                wait_readable(process.stdout, deadline)
                chunk = os.read(process.stdout.fileno(), 65536)
                if not chunk:
                    log.seek(0)
                    self.fail(f"MCP stdout closed: {log.read()!r}")
                output += chunk
            line, _, output = output.partition(b"\n")
            response = json.loads(line)
            self.assertEqual(response.get("jsonrpc"), "2.0")
            self.assertEqual(response.get("id"), request_id)
            self.assertNotIn("error", response)
            return response["result"]

        def call(name, **arguments):
            return send("tools/call", {"name": name, "arguments": arguments})

        def result(call_result):
            self.assertFalse(call_result.get("isError", False), call_result)
            return json.loads(call_result["content"][0]["text"])

        self.assertEqual(send("initialize", {"protocolVersion": "2024-11-05"})["serverInfo"]["name"], "pipkin")
        send("notifications/initialized", {}, notification=True)
        tools = send("tools/list", {})["tools"]
        self.assertEqual({tool["name"] for tool in tools}, {
            "pipkin_pack", "pipkin_submit", "pipkin_jobs", "pipkin_review", "pipkin_apply",
        })

        selection = {"root": str(self.project), "paths": list(ORIGINAL)}
        packed = result(call("pipkin_pack", **selection))
        with ZipFile(packed["zip"]) as archive:
            self.assertCountEqual(archive.namelist(), ORIGINAL)
        self.assertEqual(packed["files"], len(ORIGINAL))
        submitted = result(call("pipkin_submit", **selection, prompt=PROMPT))
        self.assertTrue(Path(submitted["result"]).is_file())
        job = submitted["job"]
        self.assertEqual(result(call("pipkin_jobs", root=str(self.project))), [job])
        review = result(call("pipkin_review", job=job))
        self.assertCountEqual([change["path"] for change in review["files"]], ORIGINAL)
        disabled = call("pipkin_apply", job=job, file="alpha.txt")
        self.assertTrue(disabled["isError"])
        self.assertIn("apply disabled", disabled["content"][0]["text"])
        process.stdin.close()
        self.assertEqual(process.wait(timeout=TIMEOUT), 0)
        self.assertEqual(output + process.stdout.read(), b"")
        self.assert_workspace(ORIGINAL["alpha.txt"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
