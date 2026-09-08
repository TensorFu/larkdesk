"""Drive the shipped desktop-session loader with sanitized fixtures."""

from __future__ import annotations

import hashlib
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path

from Crypto.Cipher import AES
from hashlib import pbkdf2_hmac

from larkdesk.session import (
    SessionError,
    decrypt_chromium_value,
    derive_chromium_key,
    load_identity,
    load_session,
    load_session_cookies,
    user_dir_hash,
)

FIXTURE_PASSWORD = "fixture-desktop-password"
FIXTURE_USER_ID = "10001"
FIXTURE_NAME = "Fixture User"
FIXTURE_SESSION = "fixture-session-token"
HOST = ".feishu.cn"


def _encrypt_v10(plaintext: bytes, password: str) -> bytes:
    key = pbkdf2_hmac("sha1", password.encode("utf-8"), b"saltysalt", 1003, 16)
    pad = 16 - (len(plaintext) % 16)
    padded = plaintext + bytes([pad]) * pad
    return b"v10" + AES.new(key, AES.MODE_CBC, b" " * 16).encrypt(padded)


def _write_persistent_db(root: Path, user_id: str, name: str) -> None:
    db = root / "persistent_storage.db"
    con = sqlite3.connect(db)
    con.execute(
        "CREATE TABLE kv_meta (user_id TEXT, partition TEXT, storage_id TEXT)"
    )
    con.execute("INSERT INTO kv_meta VALUES ('10001','default','1')")
    con.execute(
        "CREATE TABLE kv_default (storage_id TEXT, key TEXT, value TEXT, partition TEXT, update_time TEXT)"
    )
    profile = json.dumps(
        {
            "orgVal": json.dumps(
                {
                    "localizedName": name,
                    "nameWithAnotherName": name,
                    "tenantId": "tenant-1",
                },
                ensure_ascii=False,
            )
        },
        ensure_ascii=False,
    )
    tenant = json.dumps(
        {
            "orgVal": json.dumps(
                {
                    "userId": user_id,
                    "description": "Fixture Tenant",
                    "suiteFullDomain": "fixture.feishu.cn",
                },
                ensure_ascii=False,
            )
        },
        ensure_ascii=False,
    )
    con.execute(
        "INSERT INTO kv_default VALUES (?,?,?,?,?)",
        ("1", f"CURRENT_PROFILE_INFO__{user_id}", profile, "default", "0"),
    )
    con.execute(
        "INSERT INTO kv_default VALUES (?,?,?,?,?)",
        ("1", f"CURRENT_TENANT_INFO__{user_id}", tenant, "default", "0"),
    )
    con.commit()
    con.close()


def _write_cookies_db(path: Path, password: str, token: str, *, host: str = HOST, version: int = 24) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    con = sqlite3.connect(path)
    con.execute("CREATE TABLE meta (key TEXT, value TEXT)")
    con.execute("INSERT INTO meta VALUES ('version', ?)", (str(version),))
    con.execute(
        "CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB)"
    )
    inner = token.encode("utf-8")
    if version >= 24:
        inner = hashlib.sha256(host.encode("utf-8")).digest() + inner
    blob = _encrypt_v10(inner, password)
    con.execute(
        "INSERT INTO cookies VALUES (?,?,?,?)",
        (host, "session", "", blob),
    )
    con.commit()
    con.close()


def _fixture_tree(password: str = FIXTURE_PASSWORD) -> Path:
    root = Path(tempfile.mkdtemp(prefix="larkdesk-session-"))
    _write_persistent_db(root, FIXTURE_USER_ID, FIXTURE_NAME)
    cookies = (
        root
        / "aha"
        / "users"
        / user_dir_hash(FIXTURE_USER_ID)
        / "profile_explorer"
        / "Cookies"
    )
    _write_cookies_db(cookies, password, FIXTURE_SESSION)
    return root


class DecryptChromiumTests(unittest.TestCase):
    def test_shipped_decrypt_recovers_v24_cookie(self) -> None:
        host = HOST
        inner = hashlib.sha256(host.encode("utf-8")).digest() + FIXTURE_SESSION.encode()
        blob = _encrypt_v10(inner, FIXTURE_PASSWORD)
        got = decrypt_chromium_value(
            blob, FIXTURE_PASSWORD, host_key=host, db_version=24
        )
        self.assertEqual(got, FIXTURE_SESSION)

    def test_wrong_password_fails(self) -> None:
        blob = _encrypt_v10(b"secret", FIXTURE_PASSWORD)
        with self.assertRaises(SessionError):
            decrypt_chromium_value(blob, "wrong-password", host_key=HOST, db_version=13)

    def test_derive_key_is_16_bytes(self) -> None:
        key = derive_chromium_key(FIXTURE_PASSWORD)
        self.assertEqual(len(key), 16)


class LoadSessionTests(unittest.TestCase):
    def test_fixture_session_produces_http_cookie(self) -> None:
        root = _fixture_tree()
        auth = load_session(
            root, cookie_passwords=[("fixture", FIXTURE_PASSWORD)], require_decrypted_cookies=True
        )
        self.assertEqual(auth.identity.name, FIXTURE_NAME)
        self.assertEqual(auth.identity.user_id, FIXTURE_USER_ID)
        self.assertEqual(auth.cookies.get("session"), FIXTURE_SESSION)
        header = auth.cookie_header()
        self.assertIn("session=", header)
        self.assertIn(FIXTURE_SESSION, header)

    def test_load_session_cookies_unit(self) -> None:
        root = _fixture_tree()
        path = (
            root
            / "aha"
            / "users"
            / user_dir_hash(FIXTURE_USER_ID)
            / "profile_explorer"
            / "Cookies"
        )
        cookies, source = load_session_cookies(path, [("fixture", FIXTURE_PASSWORD)])
        self.assertEqual(source, "fixture")
        self.assertEqual(cookies["session"], FIXTURE_SESSION)

    def test_missing_session_store_fails(self) -> None:
        root = Path(tempfile.mkdtemp(prefix="larkdesk-empty-"))
        with self.assertRaises(SessionError):
            load_identity(root)

    def test_missing_cookies_fail_closed(self) -> None:
        root = Path(tempfile.mkdtemp(prefix="larkdesk-nocookie-"))
        _write_persistent_db(root, FIXTURE_USER_ID, FIXTURE_NAME)
        with self.assertRaises(SessionError):
            load_session(root, require_decrypted_cookies=True)

    def test_wrong_cookie_password_fails_when_required(self) -> None:
        root = _fixture_tree()
        with self.assertRaises(SessionError):
            load_session(
                root,
                cookie_passwords=[("bad", "not-the-password")],
                require_decrypted_cookies=True,
            )


if __name__ == "__main__":
    unittest.main()
