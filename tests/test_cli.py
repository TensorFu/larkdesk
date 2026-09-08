"""Drive the shipped CLI entry point against a desktop-session fixture."""

from __future__ import annotations

import io
import json
import os
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest import mock

from larkdesk.cli import main

from test_contacts import _contacts_tree
from test_session import FIXTURE_NAME, FIXTURE_PASSWORD, FIXTURE_USER_ID


class CliTests(unittest.TestCase):
    def test_contacts_command_prints_name_and_id(self) -> None:
        root = _contacts_tree()
        env = {
            "LARKDESK_LARKSHELL": str(root),
            "LARKDESK_COOKIE_PASSWORD": FIXTURE_PASSWORD,
        }
        stdout = io.StringIO()
        stderr = io.StringIO()
        with mock.patch.dict(os.environ, env, clear=False):
            with redirect_stdout(stdout), redirect_stderr(stderr):
                code = main(["contacts"])
        self.assertEqual(code, 0, stderr.getvalue())
        payload = json.loads(stdout.getvalue())
        self.assertTrue(payload["ok"])
        self.assertGreaterEqual(payload["count"], 1)
        self.assertTrue(payload["contacts"])
        first = payload["contacts"][0]
        self.assertIn("name", first)
        self.assertIn("id", first)
        ids = {c["id"] for c in payload["contacts"]}
        names = {c["name"] for c in payload["contacts"]}
        self.assertIn(FIXTURE_USER_ID, ids)
        self.assertIn(FIXTURE_NAME, names)

    def test_whoami_command(self) -> None:
        root = _contacts_tree()
        env = {
            "LARKDESK_LARKSHELL": str(root),
            "LARKDESK_COOKIE_PASSWORD": FIXTURE_PASSWORD,
        }
        stdout = io.StringIO()
        with mock.patch.dict(os.environ, env, clear=False):
            with redirect_stdout(stdout):
                code = main(["whoami"])
        self.assertEqual(code, 0)
        payload = json.loads(stdout.getvalue())
        self.assertEqual(payload["id"], FIXTURE_USER_ID)
        self.assertEqual(payload["name"], FIXTURE_NAME)
        dumped = stdout.getvalue()
        self.assertNotIn(FIXTURE_PASSWORD, dumped)
        self.assertNotIn("fixture-session-token", dumped)

    def test_contacts_missing_session_nonzero(self) -> None:
        empty = Path(tempfile.mkdtemp(prefix="larkdesk-cli-empty-"))
        stdout = io.StringIO()
        stderr = io.StringIO()
        with mock.patch.dict(os.environ, {"LARKDESK_LARKSHELL": str(empty)}, clear=False):
            with redirect_stdout(stdout), redirect_stderr(stderr):
                code = main(["contacts"])
        self.assertNotEqual(code, 0)
        self.assertFalse(stdout.getvalue().strip().startswith("{") and '"ok": true' in stdout.getvalue())


if __name__ == "__main__":
    unittest.main()
