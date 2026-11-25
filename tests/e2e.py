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
import shutil
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

    def start_provider(self, proposed=PROPOSED, release=None):
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
                if release is not None and not release.wait(timeout=TIMEOUT):
                    self.send_error(504)
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

    def test_terminal_edits_standard_instructions(self):
        editor = self.project.parent / "test-editor"
        editor.write_text('#!/bin/sh\ngrep -q "^name:" "$1" && exit 0\nprintf "\\nUse targeted tests.\\n" >> "$1"\n')
        editor.chmod(0o700)
        self.env["VISUAL"] = str(editor)
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("I Instructions")
        terminal.send("?")
        terminal.expect("PIPKIN / HELP")
        terminal.expect("/help#http")
        terminal.expect("/help#mcp")
        terminal.send("q")
        terminal.expect("I Instructions")
        terminal.send("I")
        terminal.expect("PIPKIN / INSTRUCTIONS")
        terminal.send("\r")
        terminal.expect("Saved AGENTS.md")
        self.assertIn("Use targeted tests.", (self.project / "AGENTS.md").read_text())
        terminal.send("nreview-migrations\r")
        terminal.expect("Saved .agents/skills/review-migrations/SKILL.md")
        skill = self.project / ".agents/skills/review-migrations/SKILL.md"
        self.assertIn("name: review-migrations", skill.read_text())
        terminal.send(" ")
        terminal.expect("(active)")
        terminal.send("qq")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)

    def test_terminal_mkdocs_export(self):
        class DocsProvider(BaseHTTPRequestHandler):
            def do_POST(self):
                self.rfile.read(int(self.headers["Content-Length"]))
                site = {"title": "Usage guide", "pages": [
                    {"slug": "index", "title": "Overview", "markdown": "# Overview"},
                ]}
                response = {"choices": [{"message": {"content": json.dumps(site)}}]}
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps(response).encode())

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), DocsProvider)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        Path(self.env["PIPKIN_CONFIG"]).write_text(json.dumps({
            "provider": "vllm",
            "base_url": f"http://127.0.0.1:{server.server_port}",
            "model": "test-model",
        }))
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("D MkDocs")
        terminal.send("D")
        terminal.expect("MkDocs brief")
        terminal.send("Write a usage guide\r")
        terminal.expect("MkDocs ZIP saved:")
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)
        site, = (self.output / "mkdocs").glob("*.zip")
        with ZipFile(site) as archive:
            self.assertIn(b"Usage guide", archive.read("mkdocs.yml"))
            self.assertEqual(archive.read("docs/index.md"), b"# Overview\n")

    def test_terminal_free_prompt(self):
        requests = []

        class TextProvider(BaseHTTPRequestHandler):
            def do_POST(self):
                requests.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
                reply = {"choices": [{"message": {"content": "Standalone answer"}}]}
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps(reply).encode())

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), TextProvider)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        Path(self.env["PIPKIN_CONFIG"]).write_text(json.dumps({
            "provider": "vllm",
            "base_url": f"http://127.0.0.1:{server.server_port}",
            "model": "test-model",
        }))
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("f Free prompt")
        terminal.send("nf")
        terminal.expect("PIPKIN / FREE PROMPT HISTORY")
        terminal.send("\r")
        terminal.expect("Ctrl+S send")
        terminal.send("First line\rSecond line\x13")
        terminal.expect("Standalone answer")
        terminal.send("\x06")
        terminal.expect("PIPKIN / CONTEXT FILES")
        terminal.expect("alpha.txt")
        terminal.send(" \r")
        terminal.expect("1 context files for this message")
        terminal.send("Explain the file\x13")
        terminal.expect("Waiting for test-model")
        terminal.expect("Standalone answer")
        terminal.send("\x1b")
        terminal.expect("PIPKIN / FREE PROMPT HISTORY")
        terminal.expect("First line Second line")
        terminal.send("jd")
        terminal.expect("Press Y to delete")
        terminal.send("Y")
        terminal.expect("Conversation deleted.")
        terminal.send("qq")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)
        self.assertEqual(requests[0]["messages"][-1]["content"], "First line\nSecond line")
        attachment = requests[1]["messages"][-1]["content"].splitlines()[-1]
        self.assertEqual(json.loads(attachment), {"path": "alpha.txt", "text": ORIGINAL["alpha.txt"]})
        self.assertFalse((self.output / "jobs").exists())

    @unittest.skipUnless(sys.platform.startswith("linux"), "requires xdg-open")
    def test_terminal_opens_output_folder(self):
        command = self.project.parent / "xdg-open"
        captured = self.project.parent / "opened"
        command.write_text('#!/bin/sh\nprintf "%s" "$1" > "$PIPKIN_TEST_OPENED"\n')
        command.chmod(0o700)
        self.env["PATH"] = str(command.parent) + os.pathsep + self.env["PATH"]
        self.env["PIPKIN_TEST_OPENED"] = str(captured)
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("O Open folder")
        terminal.expect("O Open output folder")
        terminal.send("z")
        terminal.expect("Saved:")
        terminal.expect("O Open output folder")
        terminal.send("Oq")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)
        deadline = time.monotonic() + TIMEOUT
        while not captured.exists() and time.monotonic() < deadline:
            time.sleep(0.01)
        self.assertEqual(captured.read_text(), str(self.output))

    def test_terminal_previews_file_without_selecting_it(self):
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("v View")
        terminal.send("n\r")
        terminal.expect("PIPKIN / FILE: alpha.txt")
        terminal.expect("Old alpha")
        terminal.send("q")
        terminal.expect("0 files selected")
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)

    def test_terminal_edits_text_from_preview(self):
        editor = self.project.parent / "codium"
        editor.write_text('#!/bin/sh\n[ "$#" -eq 2 ] && [ "$1" = "--wait" ] || exit 1\nprintf "Manual edit\\n" > "$2"\n')
        editor.chmod(0o700)
        self.env["PATH"] = str(editor.parent) + os.pathsep + self.env["PATH"]
        self.env["VISUAL"] = "/unused/fallback-editor"
        config = Path(self.env["PIPKIN_CONFIG"])
        settings = json.loads(config.read_text())
        settings["editor"] = "vscodium"
        config.write_text(json.dumps(settings))
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("e Edit")
        terminal.send("v")
        terminal.expect("PIPKIN / FILE: alpha.txt")
        terminal.send("e")
        terminal.expect("Saved alpha.txt")
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)
        self.assertEqual((self.project / "alpha.txt").read_text(), "Manual edit\n")

    def test_terminal_edits_xlsx_in_grid(self):
        import importlib.util

        if importlib.util.find_spec("openpyxl") is None:
            self.skipTest("openpyxl unavailable")
        from openpyxl import Workbook, load_workbook
        from openpyxl.styles import Font

        self.env["PIPKIN_XLSX_PYTHON"] = sys.executable
        source = self.project / "0.xlsx"
        book = Workbook()
        book.active.title = "Cover"
        data = book.create_sheet("Differences")
        data["A1"] = "Measure"
        data["A2"] = "Difference"
        data["B2"] = 4
        data["B2"].font = Font(bold=True)
        data["C2"] = "=B2*2"
        book.save(source)

        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("W Sheet")
        terminal.send("W")
        terminal.expect("PIPKIN / XLSX WORKSHEETS")
        terminal.send("j\r")
        terminal.expect("PIPKIN / XLSX: Differences")
        terminal.send("jl\r")
        terminal.expect("Edit B2")
        terminal.send("12.0\r")
        terminal.expect("1 unsaved cell edit")
        terminal.send("s")
        terminal.expect("Save a new XLSX copy")
        terminal.send("\r")
        terminal.expect("Saved:")
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)
        edited = load_workbook(self.project / "0-edited.xlsx")
        self.assertEqual(edited["Differences"]["B2"].value, 12)
        self.assertTrue(edited["Differences"]["B2"].font.bold)
        self.assertEqual(edited["Differences"]["C2"].value, "=B2*2")
        self.assertEqual(load_workbook(source)["Differences"]["B2"].value, 4)

    def test_terminal_edits_csv_in_grid(self):
        source = self.project / "0.csv"
        source.write_text('name,note\nAlice,"hello, world"\n')
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("v View")
        terminal.expect("W Sheet")
        terminal.send("W")
        terminal.expect("PIPKIN / CSV: 0.csv")
        terminal.send("jl\r")
        terminal.expect("Edit B2")
        terminal.send("revised, note\r")
        terminal.expect("1 unsaved cell edit")
        terminal.send("s")
        terminal.expect("Save a new CSV copy")
        terminal.send("\r")
        terminal.expect("Saved:")
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)
        self.assertEqual(source.read_text(), 'name,note\nAlice,"hello, world"\n')
        self.assertEqual((self.project / "0-edited.csv").read_text(), 'name,note\nAlice,"revised, note"\n')

    def test_terminal_select_one(self):
        folder = self.project / "folder"
        folder.mkdir()
        (folder / "keep.txt").write_text("Keep me\n")
        (folder / ".hidden").write_text("Leave me out\n")

        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("s Select one")
        terminal.expect("4 files selected")
        terminal.send("jsz")
        terminal.expect("Saved:")
        archive_path = self.output / "project.zip"
        with ZipFile(archive_path) as archive:
            self.assertEqual(archive.namelist(), ["alpha.txt"])

        terminal.send("Hksz")
        terminal.expect("Hidden files shown")
        terminal.expect("Saved:")
        with ZipFile(archive_path) as archive:
            self.assertEqual(archive.namelist(), ["folder/keep.txt"])
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)

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

    @unittest.skipUnless(shutil.which("git"), "Git unavailable")
    def test_terminal_git_history_and_hidden_files(self):
        def git(*args):
            subprocess.run(
                ["git", *args], cwd=self.project, env=self.env,
                check=True, capture_output=True,
            )

        git("init", "-b", "main")
        git("config", "user.name", "Pipkin Test")
        git("config", "user.email", "pipkin@example.test")
        git("add", ".")
        git("commit", "-m", "Base")
        git("update-ref", "refs/remotes/origin/main", "HEAD")
        (self.project / "alpha.txt").write_text("Local alpha\n")
        git("add", "alpha.txt")
        git("commit", "-m", "Change alpha")
        (self.project / ".github").mkdir()
        (self.project / ".github" / "ci.yml").write_text("CI\n")

        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("Parcel the otter")
        terminal.send("g")
        terminal.expect("GIT · alpha.txt")
        terminal.expect("Change alpha")
        terminal.send("\r")
        terminal.expect("PIPKIN / DIFF")
        terminal.expect("+Local alpha")
        terminal.send("qq")
        terminal.expect("Parcel the otter")
        terminal.send("G")
        terminal.expect("GIT · project (repository)")
        terminal.send("u")
        terminal.expect("Branch tip compared with origin/main")
        terminal.expect("+Local alpha")
        terminal.send("qqH")
        terminal.expect(".github")
        terminal.expect("Hidden files shown")
        terminal.send("z")
        terminal.expect("Saved:")
        with ZipFile(self.output / "project.zip") as archive:
            self.assertEqual(sorted(archive.namelist()), [
                "alpha.txt", "beta.txt", "unselected.txt",
            ])
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)

    def test_terminal_advertises_agentic_shortcut(self):
        config = Path(self.env["PIPKIN_CONFIG"])
        config.write_text(json.dumps({"provider": "vllm", "model": "local-model"}))
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("p Prompt · P Agentic")
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

    def test_terminal_switches_provider_and_cancels_prompt(self):
        config = Path(self.env["PIPKIN_CONFIG"])
        config.write_text(json.dumps({
            "provider_profile": "alpha",
            "provider_profiles": {
                "alpha": {"provider": "openai", "model": "alpha-model"},
                "beta": {"provider": "vllm", "model": "beta-model"},
            },
        }))
        editor = self.project.parent / "edit-profiles"
        editor.write_text(
            f"#!{sys.executable}\n"
            "import json, pathlib, sys\n"
            "path = pathlib.Path(sys.argv[1])\n"
            "settings = json.loads(path.read_text())\n"
            "settings['provider_profiles']['beta']['system_prompt'] = 'Beta guidance'\n"
            "path.write_text(json.dumps(settings))\n"
        )
        editor.chmod(0o700)
        self.env["VISUAL"] = str(editor)
        terminal = Terminal(self.start)
        self.addCleanup(terminal.close)
        terminal.expect("m Model")
        terminal.send("m")
        terminal.expect("PIPKIN / MODEL")
        terminal.send("j\r")
        terminal.expect("beta · vllm / beta-model")
        terminal.send("p")
        terminal.expect("Prompt (Enter send, Esc cancel)")
        terminal.send("ñ")
        terminal.expect("ñ")
        terminal.send("\x1b")
        terminal.expect("cancelled")
        terminal.send("m")
        terminal.expect("e Edit profiles")
        terminal.send("e")
        terminal.expect("Profiles saved")
        settings = json.loads(config.read_text())
        self.assertEqual(settings["provider_profile"], "alpha")
        self.assertEqual(settings["provider_profiles"]["beta"]["system_prompt"], "Beta guidance")
        terminal.send("q")
        self.assertEqual(terminal.process.wait(timeout=TIMEOUT), 0)

    def test_http_submit_review_and_copy(self):
        release = threading.Event()
        self.start_provider(release=release)
        config = Path(self.env["PIPKIN_CONFIG"])
        settings = json.loads(config.read_text())
        settings["endpoint"] = f"http://127.0.0.1:{self.provider.server_port}/deliver"
        config.write_text(json.dumps(settings))
        self.addCleanup(release.set)
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
        self.env["LISTEN_URL"] = f"http://127.0.0.1:{port}"
        log = tempfile.TemporaryFile()
        self.addCleanup(log.close)
        process = self.start("--web", stdout=log, stderr=log)

        def request(path, form=None, location=False):
            connection = http.client.HTTPConnection("127.0.0.1", port, timeout=2)
            try:
                if form is None:
                    connection.request("GET", path)
                else:
                    connection.request("POST", path, urlencode(form, doseq=True), {
                        "Content-Type": "application/x-www-form-urlencoded",
                    })
                response = connection.getresponse()
                return response.status, response.getheader("Location") if location else response.read().decode()
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
        status, review_url = request("/submit", form, location=True)
        self.assertEqual(status, 303)
        self.assertTrue(review_url.startswith("/review?job="), review_url)
        status, running = request(review_url)
        self.assertEqual(status, 200)
        self.assertIn("Sending to test-model", running)
        self.assertIn(PROMPT, running)
        release.set()
        deadline = time.monotonic() + TIMEOUT
        while True:
            status, page = request(review_url)
            if "2 pending" in page:
                break
            if time.monotonic() >= deadline:
                log.seek(0)
                self.fail(f"No completed web delivery: {log.read()!r}")
            time.sleep(0.05)

        self.assertEqual(status, 200)
        self.assertIn(PROMPT, page)
        self.assertIn('class="diff-line removed">-Old alpha', page)
        self.assertIn('class="diff-line added">&#43;New alpha', page)
        self.assertIn('class="diff-line hunk">@@', page)
        job = parse_qs(urlsplit(review_url).query)["job"][0]
        review = {"csrf": token[1], "job": job}
        self.assertEqual(request("/copy", dict(review, confirm="COPY"))[0], 409)
        self.assertIn('id="jump-file"', page)
        self.assertIn('id="pending-only"', page)
        status, redirect = request("/decision", dict(review, action="approve", path="alpha.txt"), location=True)
        self.assertEqual(status, 303)
        self.assertTrue(redirect.endswith("#file-1"), redirect)
        status, draft = request("/?" + urlencode({"from": job}))
        self.assertEqual(status, 200)
        self.assertIn(f">{PROMPT}</textarea>", draft)
        self.assertRegex(draft, r'name="files" value="alpha\.txt"[^>]*checked')
        self.assertNotRegex(draft, r'name="files" value="unselected\.txt"[^>]*checked')
        self.assertEqual(request("/decision", dict(review, action="reject", path="beta.txt"))[0], 303)
        self.assertEqual(request("/copy", review)[0], 400)
        self.assert_workspace(ORIGINAL["alpha.txt"])
        self.assertEqual(request("/copy", dict(review, confirm="COPY"))[0], 303)
        self.assert_workspace(PROPOSED["alpha.txt"])

    def test_snippet_uses_project_environment_and_archive_provider(self):
        traces = []

        class SnippetProvider(BaseHTTPRequestHandler):
            def do_POST(self):
                request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if self.path == "/api/public/otel/v1/traces":
                    traces.append(request)
                    self.send_response(200)
                    self.end_headers()
                    return
                if (self.path != "/v1/chat/completions"
                        or self.headers.get("Authorization") != "Bearer snippet-key"
                        or request["model"] != "snippet-model"
                        or request["messages"][1]["content"] != "Explain this snippet"):
                    self.send_error(400)
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"choices":[{"message":{"content":"Snippet response"}}],"usage":{"prompt_tokens":20,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":8}}}')

            def log_message(self, format, *args):
                pass

        with ThreadingHTTPServer(("127.0.0.1", 0), SnippetProvider) as server:
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                self.env.pop("PIPKIN_ENV_FILE")
                self.env["PIPKIN_LANGFUSE_CAPTURE_IO"] = "0"
                self.env["LANGFUSE_BASE_URL"] = f"http://127.0.0.1:{server.server_port}"
                self.env["LANGFUSE_PUBLIC_KEY"] = "pk-test"
                self.env["LANGFUSE_SECRET_KEY"] = "sk-test"
                (self.project / ".env").write_text(
                    f"PIPKIN_PROVIDER=openai-archive\n"
                    f"PIPKIN_BASE_URL=http://127.0.0.1:{server.server_port}/v1\n"
                    f"PIPKIN_MODEL=snippet-model\n"
                    f"PIPKIN_API_KEY_ENV=SNIPPET_KEY\nSNIPPET_KEY=snippet-key\n"
                )
                process = self.start("--snippet", stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                out, err = process.communicate(b'{"system":"Explain code","user":"Explain this snippet"}', timeout=TIMEOUT)
                self.assertEqual(process.returncode, 0, err)
                self.assertEqual(out.strip(), b"Snippet response")
                self.assertEqual(len(traces), 1)
                span = traces[0]["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
                self.assertEqual(span["name"], "pipkin-snippet")
                attrs = {item["key"]: item["value"] for item in span["attributes"]}
                self.assertEqual(attrs["pipkin.cached_tokens"]["intValue"], "8")
                self.assertNotIn("Explain this snippet", json.dumps(traces))
                self.assertNotIn("Snippet response", json.dumps(traces))
                self.env["PIPKIN_LANGFUSE_CAPTURE_IO"] = "1"
                process = self.start("--snippet", stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                out, err = process.communicate(b'{"system":"Explain code","user":"Explain this snippet"}', timeout=TIMEOUT)
                self.assertEqual(process.returncode, 0, err)
                self.assertEqual(out.strip(), b"Snippet response")
                self.assertEqual(len(traces), 2)
                span = traces[1]["resourceSpans"][0]["scopeSpans"][0]["spans"][0]
                attrs = {item["key"]: item["value"] for item in span["attributes"]}
                self.assertIn("Explain this snippet", attrs["langfuse.observation.input"]["stringValue"])
                self.assertEqual(attrs["langfuse.observation.output"]["stringValue"], "Snippet response")
            finally:
                server.shutdown()
                thread.join(TIMEOUT)

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
            "pipkin_prompt", "pipkin_instructions", "pipkin_conversations", "pipkin_project", "pipkin_workbook", "pipkin_mkdocs",
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
